package gitwatch

import (
	"context"
	"sync"
	"time"
)

// Watcher 轮询 .git 的分支/提交，负责分支切换保护与 commit 组。
type Watcher struct {
	dir   string
	poll  time.Duration
	guard time.Duration
	git   string

	now   func() time.Time
	newID func(string) string
	repo  Repository
	onErr func(error)
	onEvt func(Event)

	mu             sync.Mutex
	branch         string
	commit         string
	lastObserved   string
	switchingUntil time.Time
	lastErr        string
	started        bool
	cancel         context.CancelFunc
	done           chan struct{}
}

func New(opts Options) (*Watcher, error) {
	if err := opts.applyDefaults(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !isRepository(ctx, opts.GitPath, opts.Dir) {
		return nil, ErrNotRepository
	}
	return &Watcher{
		dir: opts.Dir, poll: opts.PollInterval, guard: opts.GuardWindow, git: opts.GitPath,
		now: opts.Now, newID: opts.NewID, repo: opts.Repository,
		onErr: opts.OnError, onEvt: opts.OnEvent,
	}, nil
}

// Start 同步做一次基线 poll，然后启动后台轮询。
func (w *Watcher) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return nil
	}
	w.started = true
	w.mu.Unlock()

	if err := w.pollOnce(ctx, true); err != nil {
		w.mu.Lock()
		w.started = false
		w.mu.Unlock()
		return err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.cancel = cancel
	w.done = make(chan struct{})
	done := w.done
	w.mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(w.poll)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if err := w.pollOnce(runCtx, false); err != nil {
					w.reportError(err)
				}
			}
		}
	}()
	return nil
}

func (w *Watcher) Stop() {
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.started = false
	w.cancel = nil
	w.done = nil
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// PollOnce 供测试/手动触发；不修改 started 状态。
func (w *Watcher) PollOnce(ctx context.Context) error {
	return w.pollOnce(ctx, false)
}

func (w *Watcher) Branch(context.Context) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.branch, nil
}

func (w *Watcher) Writable(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.now().Before(w.switchingUntil) {
		return ErrBranchSwitching
	}
	return nil
}

func (w *Watcher) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return Status{
		Branch: w.branch, Commit: w.commit,
		Switching: w.now().Before(w.switchingUntil),
		LastError: w.lastErr,
	}
}

func (w *Watcher) reportError(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	w.lastErr = err.Error()
	fn := w.onErr
	w.mu.Unlock()
	if fn != nil {
		fn(err)
	}
}

func (w *Watcher) pollOnce(ctx context.Context, initial bool) error {
	if !isRepository(ctx, w.git, w.dir) {
		return ErrNotRepository
	}
	branch, err := w.currentBranch(ctx)
	if err != nil {
		return err
	}
	head, err := w.headCommit(ctx)
	if err != nil {
		return err
	}
	now := w.now()

	w.mu.Lock()
	prevBranch, prevCommit := w.branch, w.commit
	prevLast := w.lastObserved
	w.branch, w.commit = branch, head.SHA
	w.mu.Unlock()

	if initial {
		last, err := w.repo.LastObservedGitCommit(ctx)
		if err != nil {
			return err
		}
		if head.SHA == "" {
			w.mu.Lock()
			w.lastErr = ""
			w.mu.Unlock()
			return nil
		}
		if last == "" {
			if err := w.repo.SetLastObservedGitCommit(ctx, head.SHA); err != nil {
				return err
			}
			w.mu.Lock()
			w.lastObserved = head.SHA
			w.lastErr = ""
			w.mu.Unlock()
			return nil
		}
		if last != head.SHA {
			if err := w.processCommits(ctx, last, head.SHA, branch); err != nil {
				w.mu.Lock()
				w.commit = prevCommit
				w.mu.Unlock()
				return err
			}
		}
		w.mu.Lock()
		w.lastObserved = head.SHA
		w.lastErr = ""
		w.mu.Unlock()
		return nil
	}

	if prevBranch != "" && prevBranch != branch {
		w.mu.Lock()
		w.switchingUntil = now.Add(w.guard)
		w.mu.Unlock()
		w.emit(ctx, Event{Type: "branch_switch", Branch: branch, CommitSHA: head.SHA, Message: "from " + prevBranch, CreatedAt: FormatTime(now)})
	}
	from := prevCommit
	if from == "" {
		from = prevLast
	}
	if head.SHA != "" && from != head.SHA {
		if err := w.processCommits(ctx, from, head.SHA, branch); err != nil {
			// 不推进 commit 状态，下一个 tick 重试，避免一次瞬时 git 失败丢掉 commit 组。
			w.mu.Lock()
			w.commit = prevCommit
			w.mu.Unlock()
			return err
		}
	}
	w.mu.Lock()
	w.lastObserved = head.SHA
	w.lastErr = ""
	w.mu.Unlock()
	return nil
}

func (w *Watcher) processCommits(ctx context.Context, from, to, branch string) error {
	commits, err := w.commitsBetween(ctx, from, to)
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		return nil
	}
	events, err := w.repo.ListUncommittedWriteEvents(ctx, 500)
	if err != nil {
		return err
	}
	used := make(map[string]bool, len(events))
	for _, ci := range commits {
		commitTime := time.Unix(ci.Unix, 0).UTC()
		ids := make([]string, 0)
		for _, ev := range events {
			if used[ev.ID] {
				continue
			}
			if len(commits) > 1 {
				et, ok := parseEventTime(ev.CreatedAt)
				if ok && et.After(commitTime.Add(time.Second)) {
					continue
				}
			}
			ids = append(ids, ev.ID)
			used[ev.ID] = true
		}
		if len(ids) > 0 {
			groupID := w.newID("cg")
			createdAt := FormatTime(w.now())
			if err := w.repo.CreateCommitGroup(ctx, groupID, ci.SHA, ids, createdAt); err != nil {
				return err
			}
			if err := w.repo.MarkWriteEventsCommitted(ctx, ids, ci.SHA); err != nil {
				return err
			}
			if err := w.repo.MarkTimelineCommitted(ctx, ids, ci.SHA); err != nil {
				return err
			}
		}
		w.emit(ctx, Event{Type: "commit", Branch: branch, CommitSHA: ci.SHA, Message: ci.Subject, CreatedAt: FormatTime(w.now())})
	}
	return w.repo.SetLastObservedGitCommit(ctx, to)
}

func (w *Watcher) emit(ctx context.Context, ev Event) {
	if err := w.repo.RecordGitEvent(ctx, ev.Type, ev.Branch, ev.CommitSHA, ev.Message, ev.CreatedAt); err != nil {
		w.reportError(err)
	}
	if w.onEvt != nil {
		w.onEvt(ev)
	}
}

package gitwatch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

type fakeRepo struct {
	mu             sync.Mutex
	last           string
	uncommitted    []domain.WriteEvent
	gitEvents      []Event
	groups         []commitGroup
	markedEvents   []string
	markedTimeline []string
	setLast        []string
}

type commitGroup struct {
	ID        string
	CommitSHA string
	IDs       []string
	CreatedAt string
}

func (f *fakeRepo) LastObservedGitCommit(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last, nil
}

func (f *fakeRepo) SetLastObservedGitCommit(_ context.Context, sha string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = sha
	f.setLast = append(f.setLast, sha)
	return nil
}

func (f *fakeRepo) RecordGitEvent(_ context.Context, eventType, branch, commitSHA, message, createdAt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gitEvents = append(f.gitEvents, Event{Type: eventType, Branch: branch, CommitSHA: commitSHA, Message: message, CreatedAt: createdAt})
	return nil
}

func (f *fakeRepo) ListUncommittedWriteEvents(context.Context, int) ([]domain.WriteEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.WriteEvent(nil), f.uncommitted...), nil
}

func (f *fakeRepo) CreateCommitGroup(_ context.Context, id, commitSHA string, ids []string, createdAt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups = append(f.groups, commitGroup{ID: id, CommitSHA: commitSHA, IDs: append([]string(nil), ids...), CreatedAt: createdAt})
	return nil
}

func (f *fakeRepo) MarkWriteEventsCommitted(_ context.Context, ids []string, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markedEvents = append(f.markedEvents, ids...)
	return nil
}

func (f *fakeRepo) MarkTimelineCommitted(_ context.Context, ids []string, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markedTimeline = append(f.markedTimeline, ids...)
	return nil
}

func (f *fakeRepo) addUncommitted(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uncommitted = append(f.uncommitted, domain.WriteEvent{
		ID: id, Path: "a.txt", Status: domain.WriteApplied,
		CreatedAt: FormatTime(time.Now().Add(-time.Minute)),
	})
}

func (f *fakeRepo) snapshot() (groups []commitGroup, events []Event, marked []string, timeline []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]commitGroup(nil), f.groups...), append([]Event(nil), f.gitEvents...), append([]string(nil), f.markedEvents...), append([]string(nil), f.markedTimeline...)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")
	return dir, runGit(t, dir, "rev-parse", "HEAD")
}

func commitFile(t *testing.T, dir, name, content, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-m", msg)
	return runGit(t, dir, "rev-parse", "HEAD")
}

func TestNewRejectsNonRepository(t *testing.T) {
	dir := t.TempDir()
	_, err := New(Options{Dir: dir, Repository: &fakeRepo{}, Now: time.Now})
	if !errors.Is(err, ErrNotRepository) {
		t.Fatalf("want ErrNotRepository, got %v", err)
	}
}

func TestCommitGroup(t *testing.T) {
	dir, first := initRepo(t)
	repo := &fakeRepo{last: first}
	repo.addUncommitted("we_1")
	repo.addUncommitted("we_2")
	ctx := context.Background()
	w, err := New(Options{
		Dir: dir, PollInterval: time.Hour, GuardWindow: 200 * time.Millisecond,
		Repository: repo, Now: time.Now,
		OnError: func(err error) { t.Logf("poll error: %v", err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	second := commitFile(t, dir, "a.txt", "hello\n", "add a")
	if err := w.PollOnce(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}
	t.Logf("status: %+v", w.Status())
	groups, events, marked, timeline := repo.snapshot()
	if len(groups) != 1 {
		t.Fatalf("groups = %+v", groups)
	}
	if groups[0].CommitSHA != second || len(groups[0].IDs) != 2 {
		t.Fatalf("group = %+v", groups[0])
	}
	if len(marked) != 2 || len(timeline) != 2 {
		t.Fatalf("marked=%v timeline=%v", marked, timeline)
	}
	foundCommit := false
	for _, ev := range events {
		if ev.Type == "commit" && ev.CommitSHA == second {
			foundCommit = true
		}
	}
	if !foundCommit {
		t.Fatalf("git events = %+v", events)
	}
}

func TestBranchSwitchGuard(t *testing.T) {
	dir, first := initRepo(t)
	repo := &fakeRepo{last: first}
	w, err := New(Options{
		Dir: dir, PollInterval: time.Hour, GuardWindow: 150 * time.Millisecond,
		Repository: repo, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	runGit(t, dir, "checkout", "-b", "feature")
	if err := w.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if got, _ := w.Branch(context.Background()); got != "feature" {
		t.Fatalf("branch = %q", got)
	}
	if err := w.Writable(context.Background()); !errors.Is(err, ErrBranchSwitching) {
		t.Fatalf("want switching, got %v", err)
	}
	_, events, _, _ := repo.snapshot()
	found := false
	for _, ev := range events {
		if ev.Type == "branch_switch" && ev.Branch == "feature" {
			found = true
		}
	}
	if !found {
		t.Fatalf("branch event missing: %+v", events)
	}
	time.Sleep(200 * time.Millisecond)
	if err := w.Writable(context.Background()); err != nil {
		t.Fatalf("guard should expire: %v", err)
	}
}

func TestInitialBaselineDoesNotCreateGroup(t *testing.T) {
	dir, _ := initRepo(t)
	repo := &fakeRepo{}
	repo.addUncommitted("we_1")
	ctx := context.Background()
	w, err := New(Options{Dir: dir, PollInterval: 20 * time.Millisecond, Repository: repo, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	groups, _, _, _ := repo.snapshot()
	if len(groups) != 0 {
		t.Fatalf("initial baseline must not group: %+v", groups)
	}
	if len(repo.setLast) == 0 {
		t.Fatal("baseline commit not persisted")
	}
}

func TestFailureCallback(t *testing.T) {
	dir, first := initRepo(t)
	repo := &fakeRepo{last: first}
	errs := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, err := New(Options{
		Dir: dir, PollInterval: 20 * time.Millisecond, Repository: repo, Now: time.Now,
		OnError: func(err error) {
			select {
			case errs <- err:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	if err := os.Rename(filepath.Join(dir, ".git"), filepath.Join(dir, ".git-broken")); err != nil {
		t.Skipf("cannot rename .git on this platform: %v", err)
	}
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("nil error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnError not called")
	}
}
func TestFirstCommitFromEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	repo := &fakeRepo{}
	repo.addUncommitted("we_first")
	ctx := context.Background()
	w, err := New(Options{Dir: dir, PollInterval: time.Hour, Repository: repo, Now: time.Now})
	if err != nil {
		t.Fatalf("New empty repo: %v", err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start empty repo: %v", err)
	}
	defer w.Stop()
	if st := w.Status(); st.Commit != "" {
		t.Fatalf("expected no commit, got %+v", st)
	}

	first := commitFile(t, dir, "first.txt", "hello\n", "first")
	if err := w.PollOnce(ctx); err != nil {
		t.Fatalf("poll after first commit: %v", err)
	}
	groups, events, _, _ := repo.snapshot()
	if len(groups) != 1 || groups[0].CommitSHA != first || len(groups[0].IDs) != 1 {
		t.Fatalf("first commit groups = %+v", groups)
	}
	found := false
	for _, ev := range events {
		if ev.Type == "commit" && ev.CommitSHA == first {
			found = true
		}
	}
	if !found {
		t.Fatalf("first commit event missing: %+v", events)
	}
}

func TestMultiCommitRangeParams(t *testing.T) {
	dir, first := initRepo(t)
	repo := &fakeRepo{last: first}
	repo.addUncommitted("we_a")
	repo.addUncommitted("we_b")
	ctx := context.Background()
	w, err := New(Options{Dir: dir, PollInterval: time.Hour, Repository: repo, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	second := commitFile(t, dir, "a.txt", "a\n", "commit a")
	_ = second
	third := commitFile(t, dir, "b.txt", "b\n", "commit b")
	if err := w.PollOnce(ctx); err != nil {
		t.Fatalf("poll range: %v", err)
	}
	_, events, _, _ := repo.snapshot()
	commits := 0
	for _, ev := range events {
		if ev.Type == "commit" {
			commits++
		}
	}
	if commits != 2 {
		t.Fatalf("expected 2 commit events, got %d (%+v)", commits, events)
	}
	groups, _, _, _ := repo.snapshot()
	if len(groups) == 0 {
		t.Fatal("expected at least one commit group")
	}
	total := 0
	for _, g := range groups {
		total += len(g.IDs)
	}
	if total != 2 {
		t.Fatalf("expected both write events grouped, got %d", total)
	}
	_ = third
}

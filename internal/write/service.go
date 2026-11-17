package write

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Service 是共享写工具。默认全局串行；ParallelByFile=true 时按文件并行。
type Service struct {
	root           string
	timelineLimit  int
	parallelByFile bool
	maxFileBytes   int64
	lockTimeout    time.Duration
	diffMaxLines   int

	now    func() time.Time
	newID  func(string) string
	repo   Repository
	audit  AuditSink
	branch BranchGate

	global sync.RWMutex
	serial semaphore
	locks  *keyedLocks

	// testHook 仅用于测试确定性并发；生产为 nil。
	testHook func(path string)
}

func New(opts Options) (*Service, error) {
	if err := opts.applyDefaults(); err != nil {
		return nil, err
	}
	return &Service{
		root:           opts.Root,
		timelineLimit:  opts.TimelineLimit,
		parallelByFile: opts.ParallelByFile,
		maxFileBytes:   opts.MaxFileBytes,
		lockTimeout:    opts.LockTimeout,
		diffMaxLines:   opts.DiffMaxLines,
		now:            opts.Now,
		newID:          opts.NewID,
		repo:           opts.Repository,
		audit:          opts.Audit,
		branch:         opts.Branch,
		serial:         newSemaphore(),
		locks:          newKeyedLocks(),
	}, nil
}

func (s *Service) Root() string { return s.root }

func (s *Service) TimelineLimit() int { return s.timelineLimit }

// AcquireExclusive 用于分支切换/提交/批量重构时的全局排他。
func (s *Service) AcquireExclusive(ctx context.Context) (func(), error) {
	acquired := make(chan struct{})
	go func() {
		s.global.Lock()
		select {
		case acquired <- struct{}{}:
		case <-ctx.Done():
			s.global.Unlock()
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-acquired:
		return s.global.Unlock, nil
	}
}

func (s *Service) acquire(ctx context.Context, rel string) (func(), error) {
	s.global.RLock()
	if s.parallelByFile {
		lockCtx, cancel := context.WithTimeout(ctx, s.lockTimeout)
		defer cancel()
		release, err := s.locks.acquire(lockCtx, rel)
		if err != nil {
			s.global.RUnlock()
			return nil, fmt.Errorf("%w: %v", ErrLockTimeout, err)
		}
		return func() {
			release()
			s.global.RUnlock()
		}, nil
	}
	lockCtx, cancel := context.WithTimeout(ctx, s.lockTimeout)
	defer cancel()
	if err := s.serial.acquire(lockCtx); err != nil {
		s.global.RUnlock()
		return nil, fmt.Errorf("%w: %v", ErrLockTimeout, err)
	}
	return func() {
		s.serial.release()
		s.global.RUnlock()
	}, nil
}

// Apply 提交一次 write_file。它先落写事件，再原子写文件，最后更新时间线/审计。
func (s *Service) Apply(ctx context.Context, req Request) (*Result, error) {
	if req.ActorType == "" {
		req.ActorType = domain.SenderAgent
	}
	if req.ActorID == "" {
		return nil, errors.New("write: actor_id is required")
	}
	if int64(len(req.Content)) > s.maxFileBytes {
		return nil, ErrTooLarge
	}
	if strings.ContainsRune(req.Content, 0) {
		return nil, ErrBinary
	}

	abs, rel, err := ResolvePath(s.root, req.Path)
	if err != nil {
		_ = s.auditRecord(ctx, req.ActorType, req.ActorID, "write.apply", "file", req.Path, "denied", map[string]any{"error": err.Error()})
		return nil, err
	}

	release, err := s.acquire(ctx, rel)
	if err != nil {
		return nil, err
	}
	defer release()

	branch := strings.TrimSpace(req.Branch)
	if s.branch != nil {
		if err := s.branch.Writable(ctx); err != nil {
			_ = s.auditRecord(ctx, req.ActorType, req.ActorID, "write.apply", "file", rel, "denied", map[string]any{"error": err.Error()})
			return nil, err
		}
		if b, err := s.branch.Branch(ctx); err == nil && b != "" {
			branch = b
		}
	}

	oldContent, exists, mode, err := s.readExisting(abs)
	if err != nil {
		return nil, err
	}
	currentHash := ""
	if exists {
		currentHash = HashString(oldContent)
	}

	base := NormalizeHash(req.BaseHash)
	if conflict := checkBase(rel, base, currentHash, exists); conflict != nil {
		eventBase := base
		if eventBase == "" {
			eventBase = HashString("")
		}
		ev := s.newEvent(req, rel, eventBase, "", "", branch, domain.WriteRejected, conflict.Error())
		_ = s.repo.InsertWriteEvent(ctx, ev)
		s.appendTimeline(ctx, ev, conflict.Error(), domain.WriteRejected)
		_ = s.trimTimeline(ctx, rel)
		_ = s.auditRecord(ctx, req.ActorType, req.ActorID, "write.apply", "file", rel, "conflict", map[string]any{
			"event_id": ev.ID, "base_hash": base, "current_hash": currentHash, "file_exists": exists,
		})
		return nil, conflict
	}

	eventBase := base
	if eventBase == "" {
		eventBase = HashString("")
	}
	diff := Diff(rel, oldContent, req.Content)
	ev := s.newEvent(req, rel, eventBase, req.Content, diff, branch, domain.WritePending, "")
	if err := s.repo.InsertWriteEvent(ctx, ev); err != nil {
		return nil, fmt.Errorf("write: record pending event: %w", err)
	}
	if s.testHook != nil {
		s.testHook(rel)
	}

	if err := writeAtomic(abs, []byte(req.Content), mode); err != nil {
		ev.Status = domain.WriteRejected
		ev.Error = err.Error()
		ev.AppliedAt = FormatTime(s.now())
		_ = s.repo.UpdateWriteEvent(ctx, ev)
		s.appendTimeline(ctx, ev, err.Error(), domain.WriteRejected)
		_ = s.auditRecord(ctx, req.ActorType, req.ActorID, "write.apply", "file", rel, "failed", map[string]any{
			"event_id": ev.ID, "error": err.Error(),
		})
		return nil, fmt.Errorf("write: apply file: %w", err)
	}

	// 写入期间分支被切换：不能声称成功，标记 unknown 交给恢复/人工。
	if s.branch != nil {
		if after, err := s.branch.Branch(ctx); err == nil && after != "" && branch != "" && after != branch {
			ev.Status = domain.WriteUnknown
			ev.Error = "branch changed during write"
			ev.AppliedAt = FormatTime(s.now())
			_ = s.repo.UpdateWriteEvent(ctx, ev)
			s.appendTimeline(ctx, ev, ev.Error, StatusUnmanaged)
			return nil, fmt.Errorf("%w: %s -> %s", ErrBranchChanging, branch, after)
		}
	}

	newHash := HashString(req.Content)
	ev.Status = domain.WriteApplied
	ev.AppliedAt = FormatTime(s.now())
	ev.Diff = diff
	if err := s.repo.UpdateWriteEvent(ctx, ev); err != nil {
		return nil, fmt.Errorf("write: mark event applied: %w", err)
	}
	entry := s.appendTimeline(ctx, ev, "", domain.WriteApplied)
	if err := s.trimTimeline(ctx, rel); err != nil {
		return &Result{Event: ev, TimelineID: entry.ID, Hash: newHash, Path: rel, Created: !exists, Diff: diff}, fmt.Errorf("write: trim timeline: %w", err)
	}
	if err := s.auditRecord(ctx, req.ActorType, req.ActorID, "write.apply", "file", rel, "ok", map[string]any{
		"event_id": ev.ID, "base_hash": base, "hash": newHash, "task_id": req.TaskID, "reason": req.Reason, "created": !exists,
	}); err != nil {
		return &Result{Event: ev, TimelineID: entry.ID, Hash: newHash, Path: rel, Created: !exists, Diff: diff}, fmt.Errorf("%w: %v", ErrAudit, err)
	}
	return &Result{Event: ev, TimelineID: entry.ID, Hash: newHash, Path: rel, Created: !exists, Diff: diff}, nil
}

func (s *Service) newEvent(req Request, rel, base, content, diff, branch, status, errText string) domain.WriteEvent {
	agentID := req.ActorID
	if req.ActorType == domain.SenderUser {
		agentID = req.ActorID
	}
	return domain.WriteEvent{
		ID:        s.newID("we"),
		AgentID:   agentID,
		TaskID:    req.TaskID,
		Path:      rel,
		BaseHash:  base,
		Content:   content,
		Diff:      diff,
		Reason:    req.Reason,
		Branch:    branch,
		Status:    status,
		Error:     errText,
		CreatedAt: FormatTime(s.now()),
	}
}

func checkBase(rel, base, currentHash string, exists bool) *ConflictError {
	if exists {
		if base == "" || base != currentHash {
			return &ConflictError{Path: rel, BaseHash: base, CurrentHash: currentHash, FileExists: true}
		}
		return nil
	}
	if base != "" {
		return &ConflictError{Path: rel, BaseHash: base, CurrentHash: "", FileExists: false}
	}
	return nil
}

func (s *Service) readExisting(abs string) (string, bool, os.FileMode, error) {
	fi, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, 0o644, nil
	}
	if err != nil {
		return "", false, 0, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", false, 0, fmt.Errorf("%w: target is a symlink", ErrInvalidPath)
	}
	if fi.IsDir() {
		return "", false, 0, fmt.Errorf("%w: target is a directory", ErrInvalidPath)
	}
	if fi.Size() > s.maxFileBytes {
		return "", false, 0, ErrTooLarge
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", false, 0, err
	}
	if int64(len(b)) > s.maxFileBytes {
		return "", false, 0, ErrTooLarge
	}
	if strings.ContainsRune(string(b), 0) {
		return "", false, 0, ErrBinary
	}
	mode := fi.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	return string(b), true, mode, nil
}

func writeAtomic(abs string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".lignting-write-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, abs)
}

func (s *Service) appendTimeline(ctx context.Context, ev domain.WriteEvent, errText, status string) domain.FileTimelineEntry {
	diff := ev.Diff
	if errText != "" {
		if diff != "" {
			diff = errText + "\n" + diff
		} else {
			diff = errText
		}
	}
	entry := domain.FileTimelineEntry{
		ID:           s.newID("ft"),
		Path:         ev.Path,
		WriteEventID: ev.ID,
		AgentID:      ev.AgentID,
		TaskID:       ev.TaskID,
		Diff:         diff,
		Reason:       ev.Reason,
		Branch:       ev.Branch,
		CommitSHA:    ev.CommitSHA,
		Status:       status,
		CreatedAt:    FormatTime(s.now()),
	}
	_ = s.repo.AppendTimeline(ctx, entry)
	return entry
}

func (s *Service) trimTimeline(ctx context.Context, path string) error {
	limit := s.timelineLimit
	if limit <= 0 {
		limit = DefaultTimelineLimit
	}
	_, err := s.repo.TrimTimeline(ctx, path, limit)
	return err
}

func (s *Service) auditRecord(ctx context.Context, actorType, actorID, action, objectType, objectID, result string, detail map[string]any) error {
	b, _ := json.Marshal(detail)
	return s.audit.RecordAudit(ctx, actorType, actorID, action, objectType, objectID, result, string(b))
}

// RecordUnmanaged 记录 bash 等绕过 write_file 的变更（标记「不受管变更」）。
func (s *Service) RecordUnmanaged(ctx context.Context, req UnmanagedRequest) (*Result, error) {
	if req.ActorType == "" {
		req.ActorType = domain.SenderTool
	}
	if req.ActorID == "" {
		req.ActorID = "bash"
	}
	abs, rel, err := ResolvePath(s.root, req.Path)
	if err != nil {
		return nil, err
	}
	_ = abs
	if !req.BeforeExists && !req.AfterExists {
		return nil, errors.New("write: unmanaged change has no before/after content")
	}
	beforeHash := ""
	if req.BeforeExists {
		beforeHash = HashString(req.Before)
	}
	if beforeHash == "" {
		beforeHash = HashString("")
	}
	afterHash := ""
	if req.AfterExists {
		afterHash = HashString(req.After)
	}
	if req.BeforeExists && req.AfterExists && beforeHash == afterHash {
		return nil, errors.New("write: unmanaged change is a no-op")
	}
	oldText, newText := "", ""
	if req.BeforeExists {
		oldText = req.Before
	}
	if req.AfterExists {
		newText = req.After
	}
	diff := Diff(rel, oldText, newText)
	branch := strings.TrimSpace(req.Branch)
	if branch == "" && s.branch != nil {
		if b, err := s.branch.Branch(ctx); err == nil {
			branch = b
		}
	}
	ev := domain.WriteEvent{
		ID:        s.newID("we"),
		AgentID:   req.ActorID,
		TaskID:    req.TaskID,
		Path:      rel,
		BaseHash:  beforeHash,
		Diff:      diff,
		Reason:    req.Reason,
		Branch:    branch,
		Status:    domain.WriteUnknown,
		Error:     "不受管变更：绕过了 write_file",
		CreatedAt: FormatTime(s.now()),
	}
	if err := s.repo.InsertWriteEvent(ctx, ev); err != nil {
		return nil, err
	}
	entry := s.appendTimeline(ctx, ev, ev.Error, StatusUnmanaged)
	if err := s.trimTimeline(ctx, rel); err != nil {
		return &Result{Event: ev, TimelineID: entry.ID, Hash: afterHash, Path: rel, Created: !req.BeforeExists, Diff: diff}, err
	}
	if err := s.auditRecord(ctx, req.ActorType, req.ActorID, "write.unmanaged", "file", rel, "warn", map[string]any{
		"event_id": ev.ID, "before_hash": beforeHash, "after_hash": afterHash, "reason": req.Reason,
	}); err != nil {
		return &Result{Event: ev, TimelineID: entry.ID, Hash: afterHash, Path: rel, Created: !req.BeforeExists, Diff: diff}, fmt.Errorf("%w: %v", ErrAudit, err)
	}
	return &Result{Event: ev, TimelineID: entry.ID, Hash: afterHash, Path: rel, Created: !req.BeforeExists, Diff: diff}, nil
}

// Recover 把崩溃后遗留的 pending 写事件归类为 applied/rejected/unknown。
func (s *Service) Recover(ctx context.Context) (RecoveryReport, error) {
	var report RecoveryReport
	events, err := s.repo.ListWriteEventsByStatus(ctx, domain.WritePending, 1000)
	if err != nil {
		return report, err
	}
	for _, ev := range events {
		abs, rel, err := ResolvePath(s.root, ev.Path)
		if err != nil {
			continue
		}
		_ = abs
		current := ""
		if _, statErr := os.Lstat(abs); statErr == nil {
			h, hashErr := HashFile(abs)
			if hashErr == nil {
				current = h
			}
		}
		expected := HashString(ev.Content)
		base := NormalizeHash(ev.BaseHash)
		switch {
		case current == expected:
			ev.Status = domain.WriteApplied
			ev.Error = ""
			ev.AppliedAt = FormatTime(s.now())
			report.Applied++
			if has, _ := s.repo.TimelineHasEvent(ctx, ev.ID); !has {
				s.appendTimeline(ctx, ev, "", domain.WriteApplied)
			}
			_ = s.trimTimeline(ctx, rel)
		case current == base || (base == HashString("") && current == ""):
			ev.Status = domain.WriteRejected
			ev.Error = "interrupted before file apply"
			ev.AppliedAt = FormatTime(s.now())
			report.Rejected++
			if has, _ := s.repo.TimelineHasEvent(ctx, ev.ID); !has {
				s.appendTimeline(ctx, ev, ev.Error, domain.WriteRejected)
			}
		default:
			ev.Status = domain.WriteUnknown
			ev.Error = "file changed after interrupted write"
			ev.AppliedAt = FormatTime(s.now())
			report.Unknown++
			if has, _ := s.repo.TimelineHasEvent(ctx, ev.ID); !has {
				s.appendTimeline(ctx, ev, ev.Error, StatusUnmanaged)
			}
		}
		if err := s.repo.UpdateWriteEvent(ctx, ev); err != nil {
			return report, err
		}
		if err := s.auditRecord(ctx, domain.SenderSystem, "recovery", "write.recover", "file", rel, "ok", map[string]any{
			"event_id": ev.ID, "status": ev.Status,
		}); err != nil {
			return report, fmt.Errorf("%w: %v", ErrAudit, err)
		}
	}
	return report, nil
}

// Timeline 查询变更时间线（默认每文件 30 条，最多 500）。
func (s *Service) Timeline(ctx context.Context, f TimelineFilter) ([]domain.FileTimelineEntry, error) {
	if f.Limit <= 0 {
		f.Limit = s.timelineLimit
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	if f.Path != "" {
		rel, err := s.normalizeFilterPath(f.Path)
		if err != nil {
			return nil, err
		}
		f.Path = rel
	}
	return s.repo.ListTimeline(ctx, f.Path, f.Branch, f.CommitSHA, f.AgentID, f.Since, f.Limit)
}

func (s *Service) normalizeFilterPath(p string) (string, error) {
	clean := filepath.Clean(p)
	if filepath.IsAbs(clean) {
		rel, err := filepath.Rel(s.root, clean)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%w: %s", ErrInvalidPath, p)
		}
		return filepath.ToSlash(rel), nil
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrInvalidPath, p)
	}
	return filepath.ToSlash(clean), nil
}

func (s *Service) GetEvent(ctx context.Context, id string) (domain.WriteEvent, error) {
	return s.repo.GetWriteEvent(ctx, id)
}

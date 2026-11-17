package write

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

type testClock struct {
	n    atomic.Int64
	base time.Time
}

func (c *testClock) now() time.Time {
	return c.base.Add(time.Duration(c.n.Add(1)) * time.Millisecond)
}

func (c *testClock) id(prefix string) string {
	return fmt.Sprintf("%s_%04d", prefix, c.n.Add(1))
}

func newTestService(t *testing.T, mutate func(*Options)) (*Service, *fakeRepo, *fakeAudit, string) {
	t.Helper()
	root := t.TempDir()
	repo := newFakeRepo()
	audit := &fakeAudit{}
	clock := &testClock{base: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	opts := Options{
		Root:          root,
		TimelineLimit: 30,
		Repository:    repo,
		Audit:         audit,
		Now:           clock.now,
		NewID:         clock.id,
	}
	if mutate != nil {
		mutate(&opts)
	}
	svc, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, repo, audit, root
}

func TestApplyNewAndUpdate(t *testing.T) {
	svc, repo, audit, root := newTestService(t, nil)
	ctx := context.Background()

	res, err := svc.Apply(ctx, Request{
		ActorType: domain.SenderAgent, ActorID: "agent_a", TaskID: "task_1",
		Path: "docs/readme.md", Content: "hello\n", Reason: "create",
	})
	if err != nil {
		t.Fatalf("apply new: %v", err)
	}
	if !res.Created {
		t.Fatal("expected created=true")
	}
	if res.Event.Status != domain.WriteApplied {
		t.Fatalf("status = %s", res.Event.Status)
	}
	if res.Hash != HashString("hello\n") {
		t.Fatalf("hash = %s", res.Hash)
	}
	got, err := os.ReadFile(filepath.Join(root, "docs", "readme.md"))
	if err != nil || string(got) != "hello\n" {
		t.Fatalf("file content = %q err=%v", string(got), err)
	}
	if !contains(res.Diff, "+hello") {
		t.Fatalf("diff missing added line: %q", res.Diff)
	}
	entries, err := svc.Timeline(ctx, TimelineFilter{Path: "docs/readme.md"})
	if err != nil || len(entries) != 1 {
		t.Fatalf("timeline len=%d err=%v", len(entries), err)
	}
	if entries[0].Status != domain.WriteApplied {
		t.Fatalf("timeline status = %s", entries[0].Status)
	}
	if audit.count() == 0 {
		t.Fatal("expected audit records")
	}

	res2, err := svc.Apply(ctx, Request{
		ActorType: domain.SenderAgent, ActorID: "agent_a", TaskID: "task_1",
		Path: "docs/readme.md", Content: "hello world\n", BaseHash: res.Hash, Reason: "update",
	})
	if err != nil {
		t.Fatalf("apply update: %v", err)
	}
	if res2.Hash != HashString("hello world\n") {
		t.Fatalf("update hash = %s", res2.Hash)
	}
	if !contains(res2.Diff, "-hello") || !contains(res2.Diff, "+hello world") {
		t.Fatalf("update diff: %q", res2.Diff)
	}
	if repo.eventCount() != 2 {
		t.Fatalf("events = %d", repo.eventCount())
	}
}

func TestConflictRecordsAttempt(t *testing.T) {
	svc, repo, audit, root := newTestService(t, nil)
	ctx := context.Background()
	first, err := svc.Apply(ctx, Request{ActorID: "a", Path: "x.txt", Content: "base\n"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Apply(ctx, Request{ActorID: "b", Path: "x.txt", Content: "other\n", BaseHash: "deadbeef"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.CurrentHash != first.Hash {
		t.Fatalf("conflict detail = %+v", ce)
	}
	got, _ := os.ReadFile(filepath.Join(root, "x.txt"))
	if string(got) != "base\n" {
		t.Fatalf("file changed on conflict: %q", string(got))
	}
	if repo.lastEvent().Status != domain.WriteRejected {
		t.Fatalf("last event status = %s", repo.lastEvent().Status)
	}
	entries := repo.timelineFor("x.txt")
	if len(entries) != 2 || entries[1].Status != domain.WriteRejected {
		t.Fatalf("timeline = %+v", entries)
	}
	if !audit.hasAction("write.apply") {
		t.Fatal("missing audit")
	}
}

func TestConcurrentSameFileOnlyOneSucceeds(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := svc.Apply(ctx, Request{ActorID: "a", Path: "race.txt", Content: "base\n"})
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	svc.testHook = func(path string) {
		if path != "race.txt" {
			return
		}
		once.Do(func() {
			close(started)
			<-release
		})
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, content := range []string{"one\n", "two\n"} {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			_, err := svc.Apply(ctx, Request{ActorID: "a", Path: "race.txt", Content: c, BaseHash: first.Hash})
			results <- err
		}(content)
	}
	<-started
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)

	var ok, conflicts int
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("ok=%d conflicts=%d", ok, conflicts)
	}
}

func TestGlobalSerialDifferentFiles(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	svc.testHook = func(path string) {
		if path != "a.txt" {
			return
		}
		once.Do(func() {
			close(started)
			<-release
		})
	}

	aDone := make(chan error, 1)
	go func() {
		_, err := svc.Apply(ctx, Request{ActorID: "a", Path: "a.txt", Content: "a\n"})
		aDone <- err
	}()
	<-started

	bDone := make(chan error, 1)
	go func() {
		_, err := svc.Apply(ctx, Request{ActorID: "b", Path: "b.txt", Content: "b\n"})
		bDone <- err
	}()
	select {
	case err := <-bDone:
		t.Fatalf("b finished while a held global lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-aDone; err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := <-bDone; err != nil {
		t.Fatalf("b: %v", err)
	}
}

func TestParallelByFileDifferentFiles(t *testing.T) {
	svc, _, _, _ := newTestService(t, func(o *Options) { o.ParallelByFile = true })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	svc.testHook = func(path string) {
		if path != "a.txt" {
			return
		}
		once.Do(func() {
			close(started)
			<-release
		})
	}

	aDone := make(chan error, 1)
	go func() {
		_, err := svc.Apply(ctx, Request{ActorID: "a", Path: "a.txt", Content: "a\n"})
		aDone <- err
	}()
	<-started

	bDone := make(chan error, 1)
	go func() {
		_, err := svc.Apply(ctx, Request{ActorID: "b", Path: "b.txt", Content: "b\n"})
		bDone <- err
	}()
	select {
	case err := <-bDone:
		if err != nil {
			t.Fatalf("b: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parallel-by-file did not run concurrently")
	}
	close(release)
	if err := <-aDone; err != nil {
		t.Fatalf("a: %v", err)
	}
}

func TestTimelineTrim35(t *testing.T) {
	svc, repo, _, _ := newTestService(t, nil)
	ctx := context.Background()
	base := ""
	for i := 0; i < 35; i++ {
		res, err := svc.Apply(ctx, Request{
			ActorID: "a", Path: "trim.txt", Content: fmt.Sprintf("v%02d\n", i), BaseHash: base,
		})
		if err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		base = res.Hash
	}
	if repo.eventCount() != 35 {
		t.Fatalf("events = %d", repo.eventCount())
	}
	if repo.timelineCount() != 30 {
		t.Fatalf("timeline = %d", repo.timelineCount())
	}
	entries, err := svc.Timeline(ctx, TimelineFilter{Path: "trim.txt", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 30 {
		t.Fatalf("entries = %d", len(entries))
	}
	if !contains(entries[0].Diff, "+v34") && !contains(entries[0].Diff, "v34") {
		t.Fatalf("latest entry = %+v", entries[0])
	}
}

func TestRecoverPending(t *testing.T) {
	svc, repo, _, root := newTestService(t, nil)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	pending := domain.WriteEvent{ID: "we_pending", AgentID: "a", Path: "recover.txt", Content: "abc", Status: domain.WritePending, CreatedAt: now}
	if err := repo.InsertWriteEvent(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "recover.txt"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}

	unknown := domain.WriteEvent{ID: "we_unknown", AgentID: "a", Path: "unknown.txt", Content: "xyz", BaseHash: HashString("old"), Status: domain.WritePending, CreatedAt: now}
	if err := repo.InsertWriteEvent(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unknown.txt"), []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}

	rejected := domain.WriteEvent{ID: "we_rejected", AgentID: "a", Path: "rejected.txt", Content: "", BaseHash: "", Status: domain.WritePending, CreatedAt: now}
	if err := repo.InsertWriteEvent(ctx, rejected); err != nil {
		t.Fatal(err)
	}

	report, err := svc.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Applied != 1 || report.Unknown != 1 || report.Rejected != 1 {
		t.Fatalf("report = %+v", report)
	}
	ev, _ := repo.GetWriteEvent(ctx, "we_pending")
	if ev.Status != domain.WriteApplied {
		t.Fatalf("pending status = %s", ev.Status)
	}
	if ok, _ := repo.TimelineHasEvent(ctx, "we_pending"); !ok {
		t.Fatal("timeline missing recovered event")
	}
	ev, _ = repo.GetWriteEvent(ctx, "we_unknown")
	if ev.Status != domain.WriteUnknown {
		t.Fatalf("unknown status = %s", ev.Status)
	}
	ev, _ = repo.GetWriteEvent(ctx, "we_rejected")
	if ev.Status != domain.WriteRejected {
		t.Fatalf("rejected status = %s", ev.Status)
	}
}

func TestPathEscapeRejected(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	ctx := context.Background()
	for _, p := range []string{"../evil.txt", ".git/config", "a/../../evil.txt"} {
		if _, err := svc.Apply(ctx, Request{ActorID: "a", Path: p, Content: "x"}); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("path %q err = %v", p, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "evil.txt")
	if _, err := svc.Apply(ctx, Request{ActorID: "a", Path: outside, Content: "x"}); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("absolute outside err = %v", err)
	}
}

func TestBranchGateRejectsWrite(t *testing.T) {
	branchErr := errors.New("git branch switching")
	svc, _, audit, root := newTestService(t, func(o *Options) {
		o.Branch = &fakeBranch{branch: "main", writableErr: branchErr}
	})
	_, err := svc.Apply(context.Background(), Request{ActorID: "a", Path: "x.txt", Content: "x"})
	if !errors.Is(err, branchErr) {
		t.Fatalf("want branch err, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "x.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("file should not exist")
	}
	if !audit.hasAction("write.apply") {
		t.Fatal("missing denied audit")
	}
}

func TestAuditFailureDoesNotReportSuccess(t *testing.T) {
	svc, _, audit, root := newTestService(t, nil)
	audit.err = errors.New("audit down")
	_, err := svc.Apply(context.Background(), Request{ActorID: "a", Path: "x.txt", Content: "x"})
	if !errors.Is(err, ErrAudit) {
		t.Fatalf("want audit error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "x.txt")); statErr != nil {
		t.Fatal("file should have been written before audit failure")
	}
}

func TestRecordUnmanaged(t *testing.T) {
	svc, repo, audit, _ := newTestService(t, nil)
	ctx := context.Background()
	res, err := svc.RecordUnmanaged(ctx, UnmanagedRequest{
		ActorType: domain.SenderTool, ActorID: "bash", Path: "x.txt",
		Reason: "bash bypass", Before: "old\n", BeforeExists: true, After: "new\n", AfterExists: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Event.Status != domain.WriteUnknown {
		t.Fatalf("event status = %s", res.Event.Status)
	}
	entries := repo.timelineFor("x.txt")
	if len(entries) != 1 || entries[0].Status != StatusUnmanaged {
		t.Fatalf("timeline = %+v", entries)
	}
	if !audit.hasAction("write.unmanaged") {
		t.Fatal("missing unmanaged audit")
	}
}

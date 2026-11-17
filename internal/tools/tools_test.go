package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/write"
)

type fakeRepo struct {
	mu       sync.Mutex
	events   map[string]domain.WriteEvent
	order    []string
	timeline []domain.FileTimelineEntry
}

func newFakeRepo() *fakeRepo { return &fakeRepo{events: make(map[string]domain.WriteEvent)} }

func (f *fakeRepo) InsertWriteEvent(_ context.Context, ev domain.WriteEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events[ev.ID] = ev
	f.order = append(f.order, ev.ID)
	return nil
}
func (f *fakeRepo) UpdateWriteEvent(_ context.Context, ev domain.WriteEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events[ev.ID] = ev
	return nil
}
func (f *fakeRepo) GetWriteEvent(_ context.Context, id string) (domain.WriteEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev, ok := f.events[id]
	if !ok {
		return domain.WriteEvent{}, errors.New("not found")
	}
	return ev, nil
}
func (f *fakeRepo) ListWriteEventsByStatus(_ context.Context, status string, limit int) ([]domain.WriteEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.WriteEvent, 0)
	for _, id := range f.order {
		if f.events[id].Status == status {
			out = append(out, f.events[id])
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
func (f *fakeRepo) AppendTimeline(_ context.Context, e domain.FileTimelineEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.timeline = append(f.timeline, e)
	return nil
}
func (f *fakeRepo) TrimTimeline(_ context.Context, path string, keep int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := make([]int, 0)
	for i, e := range f.timeline {
		if e.Path == path {
			idx = append(idx, i)
		}
	}
	remove := len(idx) - keep
	if remove <= 0 {
		return 0, nil
	}
	drop := make(map[int]bool, remove)
	for _, i := range idx[:remove] {
		drop[i] = true
	}
	next := make([]domain.FileTimelineEntry, 0, len(f.timeline)-remove)
	for i, e := range f.timeline {
		if !drop[i] {
			next = append(next, e)
		}
	}
	f.timeline = next
	return remove, nil
}
func (f *fakeRepo) ListTimeline(_ context.Context, path, branch, commitSHA, agentID, since string, limit int) ([]domain.FileTimelineEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.FileTimelineEntry, 0)
	for _, e := range f.timeline {
		if path != "" && e.Path != path {
			continue
		}
		if branch != "" && e.Branch != branch {
			continue
		}
		if commitSHA != "" && e.CommitSHA != commitSHA {
			continue
		}
		if agentID != "" && e.AgentID != agentID {
			continue
		}
		if since != "" && e.CreatedAt < since {
			continue
		}
		out = append(out, e)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (f *fakeRepo) TimelineHasEvent(_ context.Context, writeEventID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.timeline {
		if e.WriteEventID == writeEventID {
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeRepo) timelineFor(path string) []domain.FileTimelineEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.FileTimelineEntry, 0)
	for _, e := range f.timeline {
		if e.Path == path {
			out = append(out, e)
		}
	}
	return out
}

type fakeAudit struct {
	mu      sync.Mutex
	records []string
}

func (a *fakeAudit) RecordAudit(_ context.Context, _, _, action, _, _, result, _ string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, action+":"+result)
	return nil
}
func (a *fakeAudit) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.records)
}

type testEnv struct {
	root   string
	repo   *fakeRepo
	audit  *fakeAudit
	writer *write.Service
	reg    *Registry
}

func newTestEnv(t *testing.T, mutate func(*Options)) *testEnv {
	t.Helper()
	root := t.TempDir()
	repo := newFakeRepo()
	audit := &fakeAudit{}
	writer, err := write.New(write.Options{
		Root: root, TimelineLimit: 30, Repository: repo, Audit: audit,
	})
	if err != nil {
		t.Fatalf("write.New: %v", err)
	}
	opts := Options{
		Root: root, Writer: writer, History: writer, Audit: audit,
		ReadMaxBytes: 1 << 20, WriteMaxBytes: 1 << 20, OutputMaxBytes: 4096,
		BashTimeout: 5 * time.Second, BashMaxTimeout: 10 * time.Second,
	}
	if mutate != nil {
		mutate(&opts)
	}
	reg, err := NewRegistry(opts)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return &testEnv{root: root, repo: repo, audit: audit, writer: writer, reg: reg}
}

func call(t *testing.T, env *testEnv, name string, args any) Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := env.reg.Invoke(context.Background(), name, Request{
		CallID: "call_1", Actor: Actor{Type: "agent", ID: "agent_a"}, TaskID: "task_1", Args: raw,
	})
	if err != nil {
		t.Fatalf("invoke %s: %v", name, err)
	}
	return res
}

func TestDefinitions(t *testing.T) {
	env := newTestEnv(t, nil)
	defs := env.reg.Definitions()
	if len(defs) != 5 {
		t.Fatalf("definitions = %d", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
		if len(d.Parameters) == 0 {
			t.Fatalf("empty schema for %s", d.Name)
		}
	}
	for _, want := range []string{"read_file", "write_file", "bash", "web_search", "view_change_history"} {
		if !names[want] {
			t.Fatalf("missing %s", want)
		}
	}
}

func TestReadFile(t *testing.T) {
	env := newTestEnv(t, nil)
	if err := os.WriteFile(filepath.Join(env.root, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := call(t, env, "read_file", map[string]any{"path": "a.txt"})
	if res.IsError || !strings.Contains(res.Content, "hello") {
		t.Fatalf("read result: %+v", res)
	}
	if res.Meta["hash"] != write.HashString("hello\nworld\n") {
		t.Fatalf("hash = %v", res.Meta["hash"])
	}
	bad := call(t, env, "read_file", map[string]any{"path": "../evil.txt"})
	if !bad.IsError {
		t.Fatal("path escape should fail")
	}
}

func TestReadFileTooLarge(t *testing.T) {
	env := newTestEnv(t, func(o *Options) { o.ReadMaxBytes = 4 })
	if err := os.WriteFile(filepath.Join(env.root, "a.txt"), []byte("123456"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := call(t, env, "read_file", map[string]any{"path": "a.txt"})
	if !res.IsError {
		t.Fatal("expected too large error")
	}
}

func TestWriteFileAndConflict(t *testing.T) {
	env := newTestEnv(t, nil)
	res := call(t, env, "write_file", map[string]any{"path": "w.txt", "content": "one\n", "base_hash": "", "reason": "test"})
	if res.IsError {
		t.Fatalf("write: %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(env.root, "w.txt")); string(got) != "one\n" {
		t.Fatalf("file = %q", string(got))
	}
	conflict := call(t, env, "write_file", map[string]any{"path": "w.txt", "content": "two\n", "base_hash": "bad"})
	if !conflict.IsError || !strings.Contains(strings.ToLower(conflict.Content), "conflict") {
		t.Fatalf("conflict: %+v", conflict)
	}
	if got, _ := os.ReadFile(filepath.Join(env.root, "w.txt")); string(got) != "one\n" {
		t.Fatalf("file changed on conflict: %q", string(got))
	}
}

func TestBashBypassRecorded(t *testing.T) {
	env := newTestEnv(t, nil)
	first, err := env.writer.Apply(context.Background(), write.Request{ActorID: "agent_a", Path: "managed.txt", Content: "old\n"})
	if err != nil {
		t.Fatal(err)
	}
	_ = first
	res := call(t, env, "bash", map[string]any{"command": "echo changed> managed.txt"})
	if res.IsError {
		t.Fatalf("bash: %+v", res)
	}
	items, _ := res.Meta["unmanaged"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("unmanaged items = %#v, content=%q", res.Meta["unmanaged"], res.Content)
	}
	entries := env.repo.timelineFor("managed.txt")
	if len(entries) < 2 || entries[len(entries)-1].Status != write.StatusUnmanaged {
		t.Fatalf("timeline = %+v", entries)
	}
}

func TestBashTimeout(t *testing.T) {
	env := newTestEnv(t, nil)
	command := "sleep 2"
	if runtime.GOOS == "windows" {
		command = "ping -n 3 127.0.0.1"
	}
	res := call(t, env, "bash", map[string]any{"command": command, "timeout_ms": 100})
	if res.IsError {
		t.Fatalf("bash: %+v", res)
	}
	if res.Meta["timed_out"] != true {
		t.Fatalf("timed_out = %v content=%q", res.Meta["timed_out"], res.Content)
	}
}

func TestBashRejectsOutsidePath(t *testing.T) {
	env := newTestEnv(t, nil)
	res := call(t, env, "bash", map[string]any{"command": "echo x > ../outside.txt"})
	if !res.IsError {
		t.Fatalf("path escape should fail: %+v", res)
	}
}

func TestLimitedBuffer(t *testing.T) {
	b := &limitedBuffer{max: 10}
	input := strings.Repeat("abcdefghij", 10)
	if n, err := b.Write([]byte(input)); err != nil || n != len(input) {
		t.Fatalf("write n=%d err=%v", n, err)
	}
	out := b.String()
	if !strings.Contains(out, "truncated") {
		t.Fatalf("expected truncation marker: %q", out)
	}
	if len(out) >= len(input) {
		t.Fatalf("buffer not truncated: %d", len(out))
	}
}

func TestSearchUnavailable(t *testing.T) {
	env := newTestEnv(t, nil)
	res := call(t, env, "web_search", map[string]any{"query": "go"})
	if !res.IsError || !strings.Contains(res.Content, "unavailable") {
		t.Fatalf("search unavailable: %+v", res)
	}
}

type fakeSearch struct{}

func (fakeSearch) Search(_ context.Context, query string, limit int) ([]SearchHit, error) {
	return []SearchHit{{Title: "T1", URL: "https://example.com", Snippet: query}}, nil
}

func TestSearchProvider(t *testing.T) {
	env := newTestEnv(t, func(o *Options) { o.Search = fakeSearch{} })
	res := call(t, env, "web_search", map[string]any{"query": "go", "limit": 1})
	if res.IsError || !strings.Contains(res.Content, "example.com") {
		t.Fatalf("search: %+v", res)
	}
	if res.Meta["count"] != 1 {
		t.Fatalf("count = %v", res.Meta["count"])
	}
}

func TestViewChangeHistory(t *testing.T) {
	env := newTestEnv(t, nil)
	if _, err := env.writer.Apply(context.Background(), write.Request{ActorID: "agent_a", Path: "h.txt", Content: "x\n"}); err != nil {
		t.Fatal(err)
	}
	res := call(t, env, "view_change_history", map[string]any{"path": "h.txt", "limit": 10})
	if res.IsError || !strings.Contains(res.Content, "write_event_id") {
		t.Fatalf("history: %+v", res)
	}
	if res.Meta["count"] != 1 {
		t.Fatalf("count = %v", res.Meta["count"])
	}
}

func TestAuditRecordsToolCall(t *testing.T) {
	env := newTestEnv(t, nil)
	_ = call(t, env, "read_file", map[string]any{"path": "missing.txt"})
	if env.audit.count() == 0 {
		t.Fatal("expected tool audit")
	}
}

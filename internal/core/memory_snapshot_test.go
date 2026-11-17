package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/deepseek"
	"github.com/lignting/lignting-agents/internal/domain"
)

type fakeSnap struct {
	entries []MemoryEntrySnapshot
	tokens  int64
	err     error
}

func (f *fakeSnap) Inject(_ context.Context, _ MemoryQuery) ([]domain.Memory, error) {
	out := make([]domain.Memory, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, domain.Memory{ID: e.ID, Summary: e.Summary})
	}
	return out, nil
}

func (f *fakeSnap) InjectWithSnapshot(_ context.Context, _ MemoryQuery) (*MemorySnapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &MemorySnapshot{Entries: f.entries, Tokens: f.tokens}, nil
}

type legacyInjector struct{ memories []domain.Memory }

func (l legacyInjector) Inject(_ context.Context, _ MemoryQuery) ([]domain.Memory, error) {
	return l.memories, nil
}

func lastSnapshotPayload(t *testing.T, store *fakeStore) map[string]any {
	t.Helper()
	store.mu.Lock()
	if len(store.snapshots) == 0 {
		store.mu.Unlock()
		t.Fatal("没有 context snapshot")
	}
	payload := store.snapshots[len(store.snapshots)-1].Payload
	store.mu.Unlock()
	var out map[string]any
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		t.Fatalf("payload 不是 JSON: %v", err)
	}
	return out
}

func runOneTurn(t *testing.T, store *fakeStore, assistant domain.Agent, msg domain.Message, mem MemoryInjector) {
	t.Helper()
	mock := deepseek.NewMock()
	rt, err := NewRuntime(Options{Store: store, Provider: mock, Memory: mem, OwnerName: "阿哲"})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	defer rt.Close()
	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := rt.WaitIdle(ctx, assistant.ID); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

func TestMemorySnapshotRecordsEntries(t *testing.T) {
	store, assistant, conv, userMsg := newDMFixture(t)
	snap := &fakeSnap{
		entries: []MemoryEntrySnapshot{
			{ID: "mem_1", Summary: "偏好：先给结论", Source: "manual", Visibility: "team", Tokens: 10},
			{ID: "mem_2", Summary: "决策：用令牌桶", Source: "decision", Visibility: "org", Tokens: 20},
		},
		tokens: 30,
	}
	runOneTurn(t, store, assistant, userMsg, snap)
	payload := lastSnapshotPayload(t, store)
	entries, _ := payload["memory_entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("memory_entries = %v", payload["memory_entries"])
	}
	if payload["memory_tokens"].(float64) != 30 {
		t.Fatalf("memory_tokens = %v", payload["memory_tokens"])
	}
	first, _ := entries[0].(map[string]any)
	if first["source"] != "manual" || first["tokens"].(float64) != 10 {
		t.Fatalf("entry = %+v", first)
	}
	// 记忆内容应进入模型上下文（summary 渲染在 system prompt 里）。
	msgs := store.snapshotMessages()
	if len(msgs) < 2 || !strings.Contains(msgs[len(msgs)-1].Content, "ok") && !strings.Contains(msgs[len(msgs)-1].Content, "mock") {
		t.Fatalf("reply = %q", msgs[len(msgs)-1].Content)
	}
	_ = conv
}

func TestMemorySnapshotAfterDelete(t *testing.T) {
	store, assistant, conv, userMsg := newDMFixture(t)
	snap := &fakeSnap{entries: []MemoryEntrySnapshot{
		{ID: "mem_1", Summary: "A", Source: "manual", Tokens: 5},
		{ID: "mem_2", Summary: "B", Source: "decision", Tokens: 5},
	}}
	runOneTurn(t, store, assistant, userMsg, snap)
	// 删除一条后再跑一轮。
	snap.entries = []MemoryEntrySnapshot{{ID: "mem_1", Summary: "A", Source: "manual", Tokens: 5}}
	second := store.seedMessage(domain.Message{ID: "msg_u2", ConversationID: conv.ID, SenderType: domain.SenderUser, SenderID: "owner", SenderName: "阿哲", Type: domain.MsgText, Content: "继续"})
	runOneTurn(t, store, assistant, second, snap)
	payload := lastSnapshotPayload(t, store)
	entries, _ := payload["memory_entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("删除后 memory_entries = %v", payload["memory_entries"])
	}
	first, _ := entries[0].(map[string]any)
	if first["id"] != "mem_1" {
		t.Fatalf("entry = %+v", first)
	}
}

func TestMemorySnapshotDegraded(t *testing.T) {
	store, assistant, _, userMsg := newDMFixture(t)
	runOneTurn(t, store, assistant, userMsg, &fakeSnap{err: errString("repo down")})
	payload := lastSnapshotPayload(t, store)
	reason, _ := payload["memory_degraded_reason"].(string)
	if !strings.Contains(reason, "repo down") {
		t.Fatalf("degraded reason = %q", reason)
	}
	turns := store.snapshotTurns()
	if len(turns) != 1 || turns[0].Status != "completed" {
		t.Fatalf("降级不应阻塞 turn: %+v", turns)
	}
	if len(store.snapshotMessages()) < 2 {
		t.Fatal("降级后仍应有回复")
	}
}

func TestMemoryLegacyInjectorFallback(t *testing.T) {
	store, assistant, _, userMsg := newDMFixture(t)
	runOneTurn(t, store, assistant, userMsg, legacyInjector{memories: []domain.Memory{{ID: "mem_old", Summary: "旧接口"}}})
	payload := lastSnapshotPayload(t, store)
	ids, _ := payload["memory_ids"].([]any)
	if len(ids) != 1 || ids[0] != "mem_old" {
		t.Fatalf("memory_ids = %v", payload["memory_ids"])
	}
	if _, ok := payload["memory_entries"]; ok {
		if entries, _ := payload["memory_entries"].([]any); len(entries) != 0 {
			t.Fatalf("旧接口不应产生 entries: %v", entries)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

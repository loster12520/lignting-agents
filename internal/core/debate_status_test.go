package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

func appendDebateMeta(t *testing.T, store *fakeDebate, convID string, meta map[string]any) {
	t.Helper()
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if _, err := store.AppendMessage(context.Background(), domain.Message{
		ConversationID: convID, SenderType: domain.SenderAgent, SenderID: "a",
		SenderName: "A", Type: domain.MsgText, Content: "stance", Meta: string(raw),
	}); err != nil {
		t.Fatalf("append message: %v", err)
	}
}

func TestDebateStatusTimeboxAndStall(t *testing.T) {
	store := newFakeDebate()
	svc := NewDebateService(store)
	svc.SetTimebox(10 * time.Millisecond)
	opened := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	for i := 0; i < 2; i++ {
		appendDebateMeta(t, store, "conv_d", map[string]any{
			"kind": "debate.stance", "debate_id": "debate_1", "actor_id": "sup_a",
			"round": 1, "stance": "方案 A", "evidence_ids": []string{"ev_1"},
			"at": opened,
		})
	}
	status, err := svc.DebateStatus(context.Background(), "conv_d", "debate_1")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Submissions != 2 || status.Round != 1 || status.PerPerson["sup_a"] != 2 {
		t.Fatalf("status = %+v", status)
	}
	if !status.Stalled {
		t.Fatalf("应检测到 stall: %+v", status)
	}
	if !status.TimeboxExpired || status.OpenedAt == "" || status.NextDeadline == "" {
		t.Fatalf("时间盒应过期: %+v", status)
	}
	store.mu.Lock()
	timeouts := 0
	for _, ev := range store.events {
		if ev.Type == "debate.timeout" {
			timeouts++
		}
	}
	store.mu.Unlock()
	if timeouts != 1 {
		t.Fatalf("debate.timeout 事件 = %d, want 1（重复查询不重复写）", timeouts)
	}
	// 再查一次仍不重复写。
	if _, err := svc.DebateStatus(context.Background(), "conv_d", "debate_1"); err != nil {
		t.Fatalf("status2: %v", err)
	}
	store.mu.Lock()
	timeouts = 0
	for _, ev := range store.events {
		if ev.Type == "debate.timeout" {
			timeouts++
		}
	}
	store.mu.Unlock()
	if timeouts != 1 {
		t.Fatalf("debate.timeout 重复写入: %d", timeouts)
	}
}

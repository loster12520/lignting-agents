package store_test

import (
	"context"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/search"
	"github.com/lignting/lignting-agents/internal/store"
)

func TestAppendReplyAtomicWritesMessageEventAuditIndex(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	c := newConversation(t, st)
	msg := domain.Message{
		ConversationID: c.ID, SenderType: domain.SenderAgent, SenderID: "agent_1",
		SenderName: "助理", Type: domain.MsgText, Content: "原子回复：登录限流方案",
	}
	events := []domain.Event{{
		Type: "message.delta", ConversationID: c.ID, ObjectType: "message", ObjectID: "msg_delta_1",
		ActorType: domain.SenderAgent, ActorID: "agent_1", Payload: `{"delta":"原子回复"}`,
	}}
	audit := domain.AuditEntry{
		ActorType: domain.SenderAgent, ActorID: "agent_1", Action: "agent.reply",
		ObjectType: "message", Result: "ok", Detail: "{}",
	}
	saved, err := st.AppendReply(ctx, msg, events, audit)
	if err != nil {
		t.Fatalf("append reply: %v", err)
	}
	if saved.ID == "" || saved.Seq <= 0 || saved.CreatedAt == "" {
		t.Fatalf("saved message not normalized: %+v", saved)
	}
	msgs, err := st.ListMessages(ctx, c.ID, 0, 0, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("messages = %d err=%v", len(msgs), err)
	}
	evs, err := st.ListEventsSince(ctx, 0, c.ID, 100)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	delta := 0
	for _, ev := range evs {
		if ev.Type == "message.delta" {
			delta++
		}
	}
	if delta != 1 {
		t.Fatalf("message.delta events = %d, want 1", delta)
	}
	audits, err := st.QueryAudit(ctx, store.AuditFilter{Action: "agent.reply"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("agent.reply audits = %d err=%v", len(audits), err)
	}
	if _, err := st.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain: %v", err)
	}
	rs, err := search.Search(ctx, st.DB, "限流", search.Filter{})
	if err != nil || rs.Total != 1 {
		t.Fatalf("reply not searchable: total=%d err=%v", rs.Total, err)
	}
}

func TestFinishTurnVariants(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	c := newConversation(t, st)

	simple := domain.Turn{ID: domain.NewID("turn"), AgentID: "agent_1", ConversationID: c.ID}
	if err := st.CreateTurn(ctx, simple); err != nil {
		t.Fatalf("create turn: %v", err)
	}
	if err := st.FinishTurn(ctx, simple.ID, "completed", ""); err != nil {
		t.Fatalf("finish simple: %v", err)
	}
	got, err := st.GetTurn(ctx, simple.ID)
	if err != nil || got.Status != "completed" || got.EndedAt == "" {
		t.Fatalf("simple turn = %+v err=%v", got, err)
	}

	atomic := domain.Turn{ID: domain.NewID("turn"), AgentID: "agent_1", ConversationID: c.ID}
	if err := st.CreateTurn(ctx, atomic); err != nil {
		t.Fatalf("create atomic turn: %v", err)
	}
	ev := domain.Event{Type: "turn.completed", ConversationID: c.ID, ObjectType: "turn", ObjectID: atomic.ID, ActorType: domain.SenderAgent, ActorID: "agent_1", Payload: "{}"}
	audit := domain.AuditEntry{ActorType: domain.SenderAgent, ActorID: "agent_1", Action: "agent.turn.completed", ObjectType: "turn", ObjectID: atomic.ID, Detail: `{"status":"completed"}`}
	if err := st.FinishTurnWithEvents(ctx, atomic.ID, "completed", "", []domain.Event{ev}, audit); err != nil {
		t.Fatalf("finish with events: %v", err)
	}
	got, err = st.GetTurn(ctx, atomic.ID)
	if err != nil || got.Status != "completed" {
		t.Fatalf("atomic turn = %+v err=%v", got, err)
	}
	evs, _ := st.ListEventsSince(ctx, 0, c.ID, 100)
	found := false
	for _, e := range evs {
		if e.Type == "turn.completed" {
			found = true
		}
	}
	if !found {
		t.Fatal("turn.completed event missing")
	}
	audits, _ := st.QueryAudit(ctx, store.AuditFilter{Action: "agent.turn.completed"})
	if len(audits) != 1 {
		t.Fatalf("turn audit = %d", len(audits))
	}
}

func TestIntentsVersioned(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, _, err := st.SaveIntent(ctx, "not-json"); err == nil {
		t.Fatal("invalid intent JSON should fail")
	}
	id1, v1, err := st.SaveIntent(ctx, `{"goal":"v1"}`)
	if err != nil || v1 != 1 || id1 == "" {
		t.Fatalf("save intent 1: id=%q v=%d err=%v", id1, v1, err)
	}
	id2, v2, err := st.SaveIntent(ctx, `{"goal":"v2"}`)
	if err != nil || v2 != 2 || id2 == id1 {
		t.Fatalf("save intent 2: id=%q v=%d err=%v", id2, v2, err)
	}
	got, err := st.GetIntent(ctx)
	if err != nil || got == nil || got.ID != id2 || got.Version != 2 {
		t.Fatalf("get intent = %+v err=%v", got, err)
	}
}

func TestWriteEventsAndTimeline(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	we := domain.WriteEvent{ID: domain.NewID("write"), AgentID: "agent_1", Path: "internal/store/store.go", BaseHash: "abc", Content: "x", Reason: "test"}
	if err := st.InsertWriteEvent(ctx, we); err != nil {
		t.Fatalf("insert write event: %v", err)
	}
	got, err := st.GetWriteEvent(ctx, we.ID)
	if err != nil || got.Status != domain.WritePending || got.ID == "" {
		t.Fatalf("get write event = %+v err=%v", got, err)
	}
	got.Status = domain.WriteApplied
	got.AppliedAt = store.Now()
	if err := st.UpdateWriteEvent(ctx, got); err != nil {
		t.Fatalf("update write event: %v", err)
	}
	list, err := st.ListWriteEventsByStatus(ctx, domain.WriteApplied, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list write events = %d err=%v", len(list), err)
	}
	for i := 0; i < 35; i++ {
		id := "w_" + itoa2(i)
		ev := domain.FileTimelineEntry{
			Path: "a.go", WriteEventID: id, AgentID: "agent_1",
			CreatedAt: "2026-01-01T00:00:" + pad2(i) + ".000000000Z",
		}
		if err := st.AppendTimeline(ctx, ev); err != nil {
			t.Fatalf("append timeline %d: %v", i, err)
		}
	}
	n, err := st.TrimTimeline(ctx, "a.go", 30)
	if err != nil || n != 5 {
		t.Fatalf("trim = %d err=%v, want 5", n, err)
	}
	tl, err := st.ListTimeline(ctx, "a.go", "", "", "", "", 100)
	if err != nil || len(tl) != 30 {
		t.Fatalf("timeline = %d err=%v, want 30", len(tl), err)
	}
	if tl[0].CreatedAt <= tl[len(tl)-1].CreatedAt {
		t.Fatalf("timeline not DESC: %s .. %s", tl[0].CreatedAt, tl[len(tl)-1].CreatedAt)
	}
	if ok, _ := st.TimelineHasEvent(ctx, "w_34"); !ok {
		t.Fatal("newest timeline event should exist")
	}
	if ok, _ := st.TimelineHasEvent(ctx, "w_0"); ok {
		t.Fatal("oldest timeline event should be trimmed")
	}
}

func TestListAgentsByKind(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.CreateAgent(ctx, domain.Agent{Name: "助理", Kind: domain.AgentKindAssistant}); err != nil {
		t.Fatalf("create assistant: %v", err)
	}
	if _, err := st.CreateAgent(ctx, domain.Agent{Name: "员工", Kind: domain.AgentKindEmployee}); err != nil {
		t.Fatalf("create employee: %v", err)
	}
	assistants, err := st.ListAgentsByKind(ctx, domain.AgentKindAssistant)
	if err != nil || len(assistants) != 1 || assistants[0].Name != "助理" {
		t.Fatalf("assistants = %+v err=%v", assistants, err)
	}
}

func itoa2(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func pad2(v int) string {
	if v < 10 {
		return "0" + itoa2(v)
	}
	return itoa2(v)
}

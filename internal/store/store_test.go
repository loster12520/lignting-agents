package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/search"
	"github.com/lignting/lignting-agents/internal/store"
)

func openTestStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st.SetIndexer(search.NewIndexer())
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	return openTestStore(t, filepath.Join(t.TempDir(), "lignting.db"))
}

func newConversation(t *testing.T, st *store.Store) domain.Conversation {
	t.Helper()
	ctx := context.Background()
	c, err := st.CreateConversation(ctx, domain.Conversation{
		Type:      domain.ConvProject,
		Title:     "测试群聊",
		CreatedBy: store.OwnerID,
	}, []domain.ConversationMember{
		{PrincipalType: domain.SenderUser, PrincipalID: store.OwnerID, Role: "owner"},
	}, store.Actor{Type: domain.SenderUser, ID: store.OwnerID})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	return c
}

func insertMsg(t *testing.T, st *store.Store, convID, content, clientID string) domain.Message {
	t.Helper()
	msg, _, _, err := st.InsertMessage(context.Background(), store.NewMessage{
		ConversationID: convID,
		SenderType:     domain.SenderUser,
		SenderID:       store.OwnerID,
		SenderName:     "阿哲",
		Type:           domain.MsgText,
		Content:        content,
		ClientID:       clientID,
	})
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}
	return *msg
}

func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	st := openTestStore(t, path)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var version int
	if err := st.DB.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if version != store.SchemaVersion {
		t.Fatalf("version = %d, want %d", version, store.SchemaVersion)
	}
}

func TestSettingsAndReadCursor(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.SetSetting(ctx, "locale", "zh-CN"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	if v, ok, err := st.Setting(ctx, "locale"); err != nil || !ok || v != "zh-CN" {
		t.Fatalf("get setting = %q ok=%v err=%v", v, ok, err)
	}
	c := newConversation(t, st)
	if err := st.SetReadSeq(ctx, c.ID, 10); err != nil {
		t.Fatalf("set read: %v", err)
	}
	if err := st.SetReadSeq(ctx, c.ID, 5); err != nil {
		t.Fatalf("regress read: %v", err)
	}
	if got, _ := st.GetReadSeq(ctx, c.ID); got != 10 {
		t.Fatalf("read seq = %d, want 10 (monotonic)", got)
	}
}

func TestInsertMessageIdempotentByClientID(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	c := newConversation(t, st)
	first := insertMsg(t, st, c.ID, "重试不要重复", "client-abc")
	second := insertMsg(t, st, c.ID, "重试不要重复", "client-abc")
	if first.ID != second.ID || first.Seq != second.Seq {
		t.Fatalf("idempotency failed: first=%s/%d second=%s/%d", first.ID, first.Seq, second.ID, second.Seq)
	}
	msgs, err := st.ListMessages(ctx, c.ID, 0, 0, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	events, err := st.ListEventsSince(ctx, 0, c.ID, 100)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	created := 0
	for _, e := range events {
		if e.Type == "message.created" {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("message.created events = %d, want 1", created)
	}
	audits, err := st.QueryAudit(ctx, store.AuditFilter{Action: "message.create"})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(audits) != 1 {
		t.Fatalf("message.create audits = %d, want 1", len(audits))
	}
	if _, err := st.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain: %v", err)
	}
}

func TestMessageSeqAndEventsMonotonic(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	c := newConversation(t, st)
	var lastMsg, lastEvent int64
	for i := 0; i < 3; i++ {
		msg, ev, created, err := st.InsertMessage(ctx, store.NewMessage{
			ConversationID: c.ID, SenderType: domain.SenderUser, SenderID: store.OwnerID,
			SenderName: "阿哲", Content: "消息", Type: domain.MsgText,
		})
		if err != nil || !created {
			t.Fatalf("insert %d: %v created=%v", i, err, created)
		}
		if msg.Seq <= lastMsg {
			t.Fatalf("message seq not monotonic: %d <= %d", msg.Seq, lastMsg)
		}
		if ev == nil || ev.Seq <= lastEvent {
			t.Fatalf("event seq not monotonic")
		}
		lastMsg, lastEvent = msg.Seq, ev.Seq
	}
}

func TestEditDeleteSyncSearch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	c := newConversation(t, st)
	msg := insertMsg(t, st, c.ID, "登录要加限流", "c1")
	assertSearchCount(t, st, "限流", 1)
	if _, _, err := st.EditMessage(ctx, msg.ID, "改成部署流水线", store.Actor{Type: domain.SenderUser, ID: store.OwnerID}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	assertSearchCount(t, st, "限流", 0)
	assertSearchCount(t, st, "部署", 1)
	if _, _, err := st.DeleteMessage(ctx, msg.ID, store.Actor{Type: domain.SenderUser, ID: store.OwnerID}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	assertSearchCount(t, st, "部署", 0)
	assertSearchCount(t, st, "流水线", 0)
	var deleted int
	if err := st.DB.QueryRow("SELECT deleted FROM search_docs WHERE ref_id=?", msg.ID).Scan(&deleted); err != nil {
		t.Fatalf("search doc: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("search doc deleted = %d, want 1", deleted)
	}
}

func assertSearchCount(t *testing.T, st *store.Store, q string, want int64) {
	t.Helper()
	rs, err := search.Search(context.Background(), st.DB, q, search.Filter{})
	if err != nil {
		t.Fatalf("search %q: %v", q, err)
	}
	if rs.Total != want {
		t.Fatalf("search %q total = %d, want %d", q, rs.Total, want)
	}
}

func TestAuditChainDetectsTamper(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	c := newConversation(t, st)
	for i := 0; i < 3; i++ {
		insertMsg(t, st, c.ID, "审计消息", "")
	}
	n, err := st.VerifyAuditChain(ctx)
	if err != nil {
		t.Fatalf("chain should be valid: %v", err)
	}
	if n < 3 {
		t.Fatalf("audit rows = %d, want >=3", n)
	}
	if _, err := st.DB.Exec("UPDATE audit_log SET result='tampered' WHERE seq=1"); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := st.VerifyAuditChain(ctx); err == nil {
		t.Fatal("tampered chain should fail verification")
	}
}

func TestRestartPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lignting.db")
	st := openTestStore(t, path)
	c := newConversation(t, st)
	msg := insertMsg(t, st, c.ID, "重启不丢", "c1")
	if err := st.SetReadSeq(ctx, c.ID, msg.Seq); err != nil {
		t.Fatalf("read seq: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st2 := openTestStore(t, path)
	msgs, err := st2.ListMessages(ctx, c.ID, 0, 0, 10)
	if err != nil {
		t.Fatalf("list after restart: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "重启不丢" || msgs[0].ID != msg.ID {
		t.Fatalf("message lost after restart: %+v", msgs)
	}
	if got, _ := st2.GetReadSeq(ctx, c.ID); got != msg.Seq {
		t.Fatalf("read cursor lost: %d", got)
	}
	if _, err := st2.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain after restart: %v", err)
	}
}

func TestOwnerPassword(t *testing.T) {
	hash, err := store.HashPassword("s3cret-pass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !store.VerifyPassword(hash, "s3cret-pass") {
		t.Fatal("password should verify")
	}
	if store.VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password must fail")
	}
}

func TestApprovalsAndUsage(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	a, err := st.CreateApproval(ctx, domain.Approval{Kind: domain.ApprovalG1, Title: "蓝图", RequestedBy: store.OwnerID})
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if _, err := st.DecideApproval(ctx, a.ID, domain.ApprovalApproved, store.OwnerID, "ok"); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if _, err := st.DecideApproval(ctx, a.ID, domain.ApprovalRejected, store.OwnerID, "again"); err == nil {
		t.Fatal("double decide should conflict")
	}
	for i := 0; i < 2; i++ {
		if err := st.InsertUsage(ctx, domain.Usage{AgentID: "agent_1", Model: "deepseek-flash", PromptTokens: 10, CompletionTokens: 5, Cost: 0.01}); err != nil {
			t.Fatalf("usage: %v", err)
		}
	}
	aggs, err := st.QueryUsage(ctx, store.UsageFilter{GroupBy: "agent"})
	if err != nil {
		t.Fatalf("query usage: %v", err)
	}
	if len(aggs) != 1 || aggs[0].Calls != 2 || aggs[0].PromptTokens != 20 {
		t.Fatalf("aggregate wrong: %+v", aggs)
	}
}

func TestTurnLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	turn := domain.Turn{AgentID: "agent_1", ConversationID: "conv_1"}
	if err := st.CreateTurn(ctx, turn); err != nil {
		t.Fatalf("create turn: %v", err)
	}
	var id string
	if err := st.DB.QueryRow("SELECT id FROM turns LIMIT 1").Scan(&id); err != nil {
		t.Fatalf("turn id: %v", err)
	}
	if err := st.FinishTurn(ctx, id, "failed", "boom"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	got, err := st.GetTurn(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "failed" || got.Error != "boom" || got.EndedAt == "" {
		t.Fatalf("turn = %+v", got)
	}
}

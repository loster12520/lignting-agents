package verify

// R1 存储层行为验证（HTTP 路由未落地前的独立抽检）。
// 依据 Lead 分诊：client_id 用 meta 应用层幂等、已读用 settings key=read:<conv>。
// 这里只断言行为，不断言内部列/表名；HTTP 落地后同样的用例移到 tests/verify/http。

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/search"
	"github.com/lignting/lignting-agents/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "lignting.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	st.SetIndexer(search.NewIndexer())
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newConv(t *testing.T, st *store.Store, title string) domain.Conversation {
	t.Helper()
	c, err := st.CreateConversation(context.Background(), domain.Conversation{
		Type: domain.ConvProject, Title: title, CreatedBy: store.OwnerID,
	}, []domain.ConversationMember{
		{PrincipalType: domain.SenderUser, PrincipalID: store.OwnerID, Role: "owner"},
	}, store.Actor{Type: domain.SenderUser, ID: store.OwnerID})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	return c
}

func send(t *testing.T, st *store.Store, convID, senderType, senderID, content, clientID string) (*domain.Message, bool) {
	t.Helper()
	msg, _, inserted, err := st.InsertMessage(context.Background(), store.NewMessage{
		ConversationID: convID, SenderType: senderType, SenderID: senderID, SenderName: senderID,
		Type: domain.MsgText, Content: content, ClientID: clientID,
	})
	if err != nil {
		t.Fatalf("InsertMessage(%q): %v", content, err)
	}
	return msg, inserted
}

// Lead 分诊 #1：同 client_id 两次只产生 1 条消息 + 1 个事件（不断言列）。
func TestR1ClientIDIdempotency(t *testing.T) {
	st := openStore(t)
	conv := newConv(t, st, "幂等")
	m1, inserted1 := send(t, st, conv.ID, domain.SenderUser, store.OwnerID, "第一条", "client-1")
	m2, inserted2 := send(t, st, conv.ID, domain.SenderUser, store.OwnerID, "重试内容不同", "client-1")
	if !inserted1 {
		t.Fatal("首次发送应 inserted=true")
	}
	if inserted2 {
		t.Fatal("重复 client_id 应 inserted=false")
	}
	if m1.ID != m2.ID {
		t.Fatalf("重复 client_id 返回了不同消息: %s vs %s", m1.ID, m2.ID)
	}
	n, err := st.CountMessages(context.Background(), conv.ID)
	if err != nil {
		t.Fatalf("CountMessages: %v", err)
	}
	if n != 1 {
		t.Fatalf("消息数 = %d, want 1", n)
	}
	var events int
	if err := st.DB.QueryRow("SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='message.created'", conv.ID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 1 {
		t.Fatalf("message.created 事件数 = %d, want 1", events)
	}
	if _, err := st.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("审计链校验失败: %v", err)
	}
}

// Lead 分诊 #2：已读位置用 settings key=read:<conv>；断言语义：未读数 = seq 之后的非本人消息。
func TestR1UnreadAndReadCursor(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	conv := newConv(t, st, "未读")
	m1, _ := send(t, st, conv.ID, domain.SenderAgent, "agent_a", "回复一", "")
	m2, _ := send(t, st, conv.ID, domain.SenderAgent, "agent_b", "回复二", "")
	if m, _ := send(t, st, conv.ID, domain.SenderUser, store.OwnerID, "我自己", ""); m.ID == "" {
		t.Fatal("发送本人消息失败")
	}
	unread, err := st.CountUnread(ctx, conv.ID, 0)
	if err != nil {
		t.Fatalf("CountUnread: %v", err)
	}
	if unread != 2 {
		t.Fatalf("未读 = %d, want 2（只算非本人消息）", unread)
	}
	if err := st.SetReadSeq(ctx, conv.ID, m2.Seq); err != nil {
		t.Fatalf("SetReadSeq: %v", err)
	}
	got, _ := st.GetReadSeq(ctx, conv.ID)
	if got != m2.Seq {
		t.Fatalf("read seq = %d, want %d", got, m2.Seq)
	}
	if unread, _ = st.CountUnread(ctx, conv.ID, got); unread != 0 {
		t.Fatalf("推进已读后未读 = %d, want 0", unread)
	}
	// 单调性：后退不生效。
	if err := st.SetReadSeq(ctx, conv.ID, m1.Seq); err != nil {
		t.Fatalf("SetReadSeq 后退: %v", err)
	}
	if got, _ = st.GetReadSeq(ctx, conv.ID); got != m2.Seq {
		t.Fatalf("已读位置应单调，得到 %d want %d", got, m2.Seq)
	}
}

// 编辑/删除墓碑：删除后列表不可见、索引移除、事件与审计存在。
func TestR1EditAndTombstone(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	conv := newConv(t, st, "墓碑")
	m, _ := send(t, st, conv.ID, domain.SenderUser, store.OwnerID, "原始", "")
	edited, _, err := st.EditMessage(ctx, m.ID, "修改后", store.Actor{Type: domain.SenderUser, ID: store.OwnerID})
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if edited.Content != "修改后" || edited.EditedAt == "" {
		t.Fatalf("编辑未生效: content=%q edited_at=%q", edited.Content, edited.EditedAt)
	}
	deleted, _, err := st.DeleteMessage(ctx, m.ID, store.Actor{Type: domain.SenderUser, ID: store.OwnerID})
	if err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if deleted.DeletedAt == "" {
		t.Fatal("删除未写墓碑 deleted_at")
	}
	if n, _ := st.CountMessages(ctx, conv.ID); n != 0 {
		t.Fatalf("删除后可见消息数 = %d, want 0", n)
	}
	var docs int
	if err := st.DB.QueryRow("SELECT COUNT(*) FROM search_docs WHERE ref_id=? AND deleted=0", m.ID).Scan(&docs); err != nil {
		t.Fatalf("query search_docs: %v", err)
	}
	if docs != 0 {
		t.Fatalf("删除后索引仍可见（search_docs=%d）", docs)
	}
	if _, err := st.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("审计链校验失败: %v", err)
	}
}

// 全序与分页：seq 跨会话全局递增；before/after 都返回升序。
func TestR1SeqAndPagination(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	c1 := newConv(t, st, "A")
	c2 := newConv(t, st, "B")
	var seqs []int64
	for i := 0; i < 4; i++ {
		conv := c1.ID
		if i%2 == 1 {
			conv = c2.ID
		}
		m, _ := send(t, st, conv, domain.SenderUser, store.OwnerID, "m", "")
		seqs = append(seqs, m.Seq)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("seq 未全局递增: %v", seqs)
		}
	}
	ms, err := st.ListMessages(ctx, c1.ID, 0, 0, 50)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	for i := 1; i < len(ms); i++ {
		if ms[i-1].Seq >= ms[i].Seq {
			t.Fatalf("分页结果非升序: %d, %d", ms[i-1].Seq, ms[i].Seq)
		}
	}
	if len(ms) != 2 {
		t.Fatalf("会话 A 消息数 = %d, want 2", len(ms))
	}
}

// 审计链可校验；篡改一条 hash 后校验必须失败（红队用例）。
func TestR1AuditChainDetectsTamper(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	conv := newConv(t, st, "审计")
	if m, _ := send(t, st, conv.ID, domain.SenderUser, store.OwnerID, "触发审计", ""); m.ID == "" {
		t.Fatal("发送消息失败")
	}
	n, err := st.VerifyAuditChain(ctx)
	if err != nil {
		t.Fatalf("VerifyAuditChain: %v", err)
	}
	if n < 2 {
		t.Fatalf("审计条数 = %d, want >=2", n)
	}
	if _, err := st.DB.Exec("UPDATE audit_log SET hash='tampered' WHERE seq=(SELECT MAX(seq) FROM audit_log)"); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := st.VerifyAuditChain(ctx); err == nil {
		t.Fatal("篡改审计后校验仍通过（应失败）")
	}
}

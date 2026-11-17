package search_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/search"
	"github.com/lignting/lignting-agents/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "lignting.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st.SetIndexer(search.NewIndexer())
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newConv(t *testing.T, st *store.Store, title string) domain.Conversation {
	t.Helper()
	c, err := st.CreateConversation(context.Background(), domain.Conversation{
		Type: domain.ConvProject, Title: title, CreatedBy: store.OwnerID,
	}, []domain.ConversationMember{{PrincipalType: domain.SenderUser, PrincipalID: store.OwnerID}}, store.Actor{Type: domain.SenderUser, ID: store.OwnerID})
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	return c
}

func insert(t *testing.T, st *store.Store, convID, content, senderID string) domain.Message {
	t.Helper()
	msg, _, _, err := st.InsertMessage(context.Background(), store.NewMessage{
		ConversationID: convID, SenderType: domain.SenderUser, SenderID: senderID,
		SenderName: "阿哲", Type: domain.MsgText, Content: content,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	return *msg
}

func doSearch(t *testing.T, st *store.Store, q string, f search.Filter) *search.ResultSet {
	t.Helper()
	rs, err := search.Search(context.Background(), st.DB, q, f)
	if err != nil {
		t.Fatalf("search %q: %v", q, err)
	}
	return rs
}

func hasTerm(terms []search.Term, text string) bool {
	for _, t := range terms {
		if t.Text == text {
			return true
		}
	}
	return false
}

func TestTermsChineseBigrams(t *testing.T) {
	terms := search.Terms("登录限流")
	for _, want := range []string{"登录", "录限", "限流"} {
		if !hasTerm(terms, want) {
			t.Fatalf("missing bigram %q in %+v", want, terms)
		}
	}
	if hasTerm(terms, "登录限流") {
		t.Fatal("whole Chinese run should not be indexed as one term")
	}
}

func TestTermsEnglishAndCamelCase(t *testing.T) {
	terms := search.Terms("getUserById Login")
	for _, want := range []string{"getuserbyid", "get", "user", "by", "id", "login"} {
		if !hasTerm(terms, want) {
			t.Fatalf("missing term %q in %+v", want, terms)
		}
	}
	// 英文大小写不敏感
	for _, term := range search.Terms("LOGIN") {
		if term.Text == "LOGIN" {
			t.Fatal("terms must be lowercased")
		}
	}
}

func TestQueryTermsSingleHanFallback(t *testing.T) {
	terms := search.QueryTerms("限")
	if len(terms) != 1 || !terms[0].SingleHan {
		t.Fatalf("single han query = %+v", terms)
	}
	terms = search.QueryTerms("限流")
	if len(terms) != 1 || terms[0].Text != "限流" || terms[0].SingleHan {
		t.Fatalf("two-char han query = %+v", terms)
	}
}

func TestChineseTwoCharSearch(t *testing.T) {
	st := newStore(t)
	c := newConv(t, st, "研发群")
	insert(t, st, c.ID, "登录接口需要加限流，防止刷接口", store.OwnerID)
	insert(t, st, c.ID, "部署流水线今天修好了", store.OwnerID)
	for _, q := range []string{"限流", "登录", "部署", "流水线"} {
		if rs := doSearch(t, st, q, search.Filter{}); rs.Total != 1 {
			t.Fatalf("query %q total = %d, want 1", q, rs.Total)
		}
	}
	// 多词交集
	if rs := doSearch(t, st, "登录 限流", search.Filter{}); rs.Total != 1 {
		t.Fatalf("intersection total = %d, want 1", rs.Total)
	}
	if rs := doSearch(t, st, "登录 部署", search.Filter{}); rs.Total != 0 {
		t.Fatalf("should not intersect different messages, total = %d", rs.Total)
	}
}

func TestEnglishCaseInsensitiveAndIdentifier(t *testing.T) {
	st := newStore(t)
	c := newConv(t, st, "研发群")
	insert(t, st, c.ID, "call getUserById before rate limit", store.OwnerID)
	if rs := doSearch(t, st, "GETUSERBYID", search.Filter{}); rs.Total != 1 {
		t.Fatalf("identifier query total = %d, want 1", rs.Total)
	}
	if rs := doSearch(t, st, "user", search.Filter{}); rs.Total != 1 {
		t.Fatalf("subword query total = %d, want 1", rs.Total)
	}
	if rs := doSearch(t, st, "RATE", search.Filter{}); rs.Total != 1 {
		t.Fatalf("case-insensitive query total = %d, want 1", rs.Total)
	}
}

func TestFilterByConversationAndSender(t *testing.T) {
	st := newStore(t)
	c1 := newConv(t, st, "群一")
	c2 := newConv(t, st, "群二")
	insert(t, st, c1.ID, "同一句限流消息", store.OwnerID)
	insert(t, st, c2.ID, "另一句限流消息", "agent_x")
	if rs := doSearch(t, st, "限流", search.Filter{ConversationID: c1.ID}); rs.Total != 1 || rs.Items[0].ConversationID != c1.ID {
		t.Fatalf("conversation filter failed: %+v", rs)
	}
	if rs := doSearch(t, st, "限流", search.Filter{SenderID: "agent_x"}); rs.Total != 1 || rs.Items[0].ConversationID != c2.ID {
		t.Fatalf("sender filter failed: %+v", rs)
	}
	if rs := doSearch(t, st, "限流", search.Filter{SenderID: store.OwnerID}); rs.Total != 1 {
		t.Fatalf("sender filter owner total = %d, want 1", rs.Total)
	}
}

func TestSnippetHighlightsAndEscapesHTML(t *testing.T) {
	st := newStore(t)
	c := newConv(t, st, "安全")
	insert(t, st, c.ID, "<script>alert(1)</script> 限流 需要 100% 命中", store.OwnerID)
	rs := doSearch(t, st, "限流", search.Filter{})
	if rs.Total != 1 {
		t.Fatalf("total = %d", rs.Total)
	}
	snippet := rs.Items[0].Snippet
	if !strings.Contains(snippet, "<em>限流</em>") {
		t.Fatalf("snippet missing highlight: %q", snippet)
	}
	if strings.Contains(snippet, "<script>") {
		t.Fatalf("snippet must escape HTML: %q", snippet)
	}
	if !strings.Contains(snippet, "&lt;script&gt;") {
		t.Fatalf("snippet should contain escaped script: %q", snippet)
	}
}

func TestRebuild(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	c := newConv(t, st, "重建")
	insert(t, st, c.ID, "中文两字限流可以命中", store.OwnerID)
	insert(t, st, c.ID, "部署流水线也走索引", store.OwnerID)
	if _, err := st.DB.Exec("DELETE FROM search_terms"); err != nil {
		t.Fatalf("clear terms: %v", err)
	}
	if _, err := st.DB.Exec("DELETE FROM search_docs"); err != nil {
		t.Fatalf("clear docs: %v", err)
	}
	if rs := doSearch(t, st, "限流", search.Filter{}); rs.Total != 0 {
		t.Fatalf("index should be empty, total = %d", rs.Total)
	}
	stats, err := search.Rebuild(ctx, st.DB)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if stats.Documents != 2 || stats.Terms == 0 {
		t.Fatalf("rebuild stats = %+v", stats)
	}
	if rs := doSearch(t, st, "限流", search.Filter{}); rs.Total != 1 {
		t.Fatalf("after rebuild 限流 total = %d, want 1", rs.Total)
	}
	if rs := doSearch(t, st, "流水线", search.Filter{}); rs.Total != 1 {
		t.Fatalf("after rebuild 流水线 total = %d, want 1", rs.Total)
	}
}

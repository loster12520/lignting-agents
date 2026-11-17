//go:build acceptance

package ac

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/store"
)

// TestAC06SearchAndAudit covers the R1-available part of AC-06:
// Chinese 2-char search, kind/conversation filters, delete tombstone removes
// the document from search, reindex works, audit chain contains delete/reindex.
// Still R8: cross-agent ACL (task-25), export/import, 100k performance corpus.
func TestAC06SearchAndAudit(t *testing.T) {
	h := NewHarness(t)
	h.Setup("阿哲")

	var conv struct {
		ID string `json:"id"`
	}
	h.PostData(t, "/api/conversations", map[string]any{
		"type": "project", "title": "AC-06 检索验收",
	}, &conv)
	if conv.ID == "" {
		t.Fatal("create conversation: empty id")
	}

	post := func(content, clientID string) map[string]any {
		t.Helper()
		var out struct {
			Message map[string]any `json:"message"`
			Created bool           `json:"created"`
		}
		h.PostData(t, "/api/conversations/"+conv.ID+"/messages", map[string]any{
			"content": content, "client_id": clientID,
		}, &out)
		if out.Message == nil {
			t.Fatalf("post message %q: nil message", content)
		}
		return out.Message
	}
	post("登录限流采用令牌桶；部署前必须做中文 2 字检索验证（限流、部署、返工）。", "ac06-m1")
	post("部署回滚步骤：先停写、再回滚、最后补审计。", "ac06-m2")
	secret := post("临时口令 Z9 只用于删除与索引测试。", "ac06-secret")

	type searchItem struct {
		Kind           string  `json:"kind"`
		RefID          string  `json:"ref_id"`
		ConversationID string  `json:"conversation_id"`
		Title          string  `json:"title"`
		Snippet        string  `json:"snippet"`
		Score          float64 `json:"score"`
		Seq            int64   `json:"seq"`
		CreatedAt      string  `json:"created_at"`
	}
	type searchResp struct {
		Items []searchItem `json:"items"`
		Total int64        `json:"total"`
		Next  string       `json:"next"`
	}
	search := func(query string, params map[string]string) searchResp {
		t.Helper()
		v := url.Values{}
		v.Set("q", query)
		for k, val := range params {
			v.Set(k, val)
		}
		var out searchResp
		h.GetData(t, "/api/search?"+v.Encode(), &out)
		return out
	}

	// 1) Chinese 2-char query hits the message and returns navigable fields.
	limitHits := search("限流", nil)
	if limitHits.Total < 1 || len(limitHits.Items) == 0 {
		t.Fatalf("search 限流 total=%d items=%d, want >=1", limitHits.Total, len(limitHits.Items))
	}
	hit := limitHits.Items[0]
	if hit.Kind != "message" || hit.RefID == "" || hit.ConversationID != conv.ID {
		t.Fatalf("search hit = %+v", hit)
	}
	if !strings.Contains(hit.Snippet, "限流") {
		t.Errorf("snippet %q does not contain 限流", hit.Snippet)
	}
	if hit.Seq <= 0 {
		t.Errorf("message hit missing seq for jump: %+v", hit)
	}

	// 2) Multiple hits + kind/conversation filters.
	deployHits := search("部署", nil)
	if deployHits.Total < 2 {
		t.Fatalf("search 部署 total=%d, want >=2", deployHits.Total)
	}
	filtered := search("部署", map[string]string{
		"conversation_id": conv.ID,
		"kind":            "message",
		"limit":           "20",
	})
	if filtered.Total != deployHits.Total {
		t.Errorf("filtered total=%d, unfiltered=%d (same conversation)", filtered.Total, deployHits.Total)
	}
	for _, it := range filtered.Items {
		if it.Kind != "message" || it.ConversationID != conv.ID {
			t.Fatalf("filter violation: %+v", it)
		}
	}

	// 3) A just-inserted message is searchable immediately (same transaction
	// index). This is the product form of FR-SRCH-06's 100ms freshness.
	immediate := search("Z9", nil)
	if immediate.Total < 1 {
		t.Fatalf("search Z9 total=%d, want >=1 immediately after write", immediate.Total)
	}

	// 4) Deleted message disappears from search and message list, but audit
	// keeps the tombstone action.
	secretID, _ := secret["id"].(string)
	if secretID == "" {
		t.Fatal("secret message id missing")
	}
	h.ExpectStatus(t, http.MethodDelete, "/api/messages/"+secretID, nil, http.StatusOK)
	if afterDelete := search("Z9", nil); afterDelete.Total != 0 || len(afterDelete.Items) != 0 {
		t.Fatalf("deleted message still searchable: total=%d items=%d", afterDelete.Total, len(afterDelete.Items))
	}
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/conversations/"+conv.ID+"/messages", &list)
	if list.Total != 2 {
		t.Fatalf("message list total=%d, want 2 after deleting one of 3", list.Total)
	}
	for _, m := range list.Items {
		if m.ID == secretID {
			t.Fatalf("deleted message %s still listed", secretID)
		}
	}

	// 5) Reindex rebuilds the derived index and search still works.
	var stats map[string]any
	h.PostData(t, "/api/admin/reindex", map[string]any{}, &stats)
	if _, ok := stats["documents"]; !ok {
		t.Fatalf("reindex stats missing documents: %v", stats)
	}
	if again := search("限流", nil); again.Total < 1 {
		t.Fatalf("search after reindex total=%d, want >=1", again.Total)
	}

	// 6) Audit chain: message.delete + search.reindex exist and verify.
	audits, err := h.Store.QueryAudit(context.Background(), store.AuditFilter{Limit: 200})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]bool{}
	for _, a := range audits {
		actions[a.Action] = true
	}
	for _, want := range []string{"message.delete", "search.reindex"} {
		if !actions[want] {
			t.Errorf("audit missing %q (got %v)", want, actions)
		}
	}
	if n, err := h.Store.VerifyAuditChain(context.Background()); err != nil || n == 0 {
		t.Fatalf("verify audit chain: n=%d err=%v", n, err)
	}

	// 7) Known/remaining AC-06 gaps, tracked as separate tasks rather than
	// silently skipped here:
	//   - cross-agent ACL search (task-25)
	//   - export/import of messages/decisions/audit (R8)
	//   - 100k corpus p95 / Recall@10 (scripts/benchmark.ps1 after tests/bench)
	t.Log("AC-06 R1 layer passed: zh 2-char search, filters, delete tombstone, reindex, audit chain")
}

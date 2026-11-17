package platform_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/store"
)

func TestAgentTokenIssueRotateRevoke(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()

	status, out := e.doCSRF("POST", "/api/agents/"+e.assistantID+"/token", nil)
	d := e.mustOK(status, out)
	token, _ := d["token"].(string)
	if token == "" || !strings.HasPrefix(token, "lgt_") {
		t.Fatalf("issued token = %v", d)
	}
	// DB 只存 sha256
	var hash string
	if err := e.st.DB.QueryRow("SELECT token_hash FROM agents WHERE id=?", e.assistantID).Scan(&hash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if hash == "" || hash == token || hash != store.HashToken(token) {
		t.Fatalf("token_hash 不是 sha256 或泄漏明文")
	}
	// GET agent 不返回 token/hash
	status, out = e.do("GET", "/api/agents/"+e.assistantID, nil)
	agent := e.mustOK(status, out)
	if _, ok := agent["token"]; ok {
		t.Fatal("agent response leaks token")
	}
	if _, ok := agent["token_hash"]; ok {
		t.Fatal("agent response leaks token_hash")
	}
	// 审计不含明文
	audits, err := e.st.QueryAudit(ctx, store.AuditFilter{Action: "agent.token_issue"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("issue audit = %d err=%v", len(audits), err)
	}
	if strings.Contains(audits[0].Detail, token) {
		t.Fatal("audit detail leaks token")
	}

	// issuer token 可用（检索）
	if status, _ := e.do("GET", "/api/search?q=限流", nil, func(r *http.Request) {
		r.Header.Set("X-Lignting-Agent-Token", token)
	}); status != http.StatusOK {
		t.Fatalf("issued token search status = %d", status)
	}

	// 轮换：旧 401，新 200
	status, out = e.doCSRF("POST", "/api/agents/"+e.assistantID+"/token/rotate", nil)
	d = e.mustOK(status, out)
	newToken, _ := d["token"].(string)
	if newToken == "" || newToken == token {
		t.Fatalf("rotated token = %v", d)
	}
	if status, _ := e.do("GET", "/api/search?q=限流", nil, func(r *http.Request) {
		r.Header.Set("X-Lignting-Agent-Token", token)
	}); status != http.StatusUnauthorized {
		t.Fatalf("old token after rotate = %d, want 401", status)
	}
	if status, _ := e.do("GET", "/api/search?q=限流", nil, func(r *http.Request) {
		r.Header.Set("X-Lignting-Agent-Token", newToken)
	}); status != http.StatusOK {
		t.Fatalf("new token after rotate = %d, want 200", status)
	}

	// 撤销：新 401；重复撤销幂等 200
	status, out = e.doCSRF("DELETE", "/api/agents/"+e.assistantID+"/token", nil)
	if status != http.StatusOK || e.mustOK(status, out)["revoked"] != true {
		t.Fatalf("revoke = %d %v", status, out)
	}
	if status, _ := e.do("GET", "/api/search?q=限流", nil, func(r *http.Request) {
		r.Header.Set("X-Lignting-Agent-Token", newToken)
	}); status != http.StatusUnauthorized {
		t.Fatalf("revoked token = %d, want 401", status)
	}
	if status, _ := e.doCSRF("DELETE", "/api/agents/"+e.assistantID+"/token", nil); status != http.StatusOK {
		t.Fatalf("second revoke = %d, want 200 (idempotent)", status)
	}
	// token 未进入消息
	var leaks int
	if err := e.st.DB.QueryRow("SELECT COUNT(*) FROM messages WHERE content LIKE '%'||?||'%'", newToken).Scan(&leaks); err != nil {
		t.Fatalf("leak query: %v", err)
	}
	if leaks != 0 {
		t.Fatalf("token leaked into messages: %d", leaks)
	}
}

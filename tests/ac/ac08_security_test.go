//go:build acceptance

package ac

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/tests/fixtures"
)

// TestAC08SecurityAndPermissions covers AC-08 through the real HTTP surface:
// Agent token issue/rotate/revoke, search ACL (own department vs cross
// department), attachment authorization, local password, CSRF, Origin and
// emergency stop. All sensitive attempts must leave audit records (FR-SEC-01..07).
func TestAC08SecurityAndPermissions(t *testing.T) {
	h := NewHarness(t, WithSeed(), WithCoreServices())
	h.EnsureSession()
	ctx := context.Background()

	// A message that only the test department visible conversation contains.
	testConv := h.Seed.Conversations["conv_dept_test"]
	var posted map[string]any
	h.PostData(t, "/api/conversations/"+testConv+"/messages", map[string]any{
		"content": "测试部门独有暗号：紫电。",
	}, &posted)
	var reindex map[string]any
	h.PostData(t, "/api/admin/reindex", map[string]any{}, &reindex)

	issue := func(agentID string) string {
		t.Helper()
		var out struct {
			Token string `json:"token"`
		}
		h.PostData(t, "/api/agents/"+agentID+"/token", map[string]any{}, &out)
		if !strings.HasPrefix(out.Token, "lgt_") || len(out.Token) < 20 {
			t.Fatalf("issued token for %s = %q", agentID, out.Token)
		}
		return out.Token
	}
	devToken := issue(fixtures.AgentDevAID)
	testToken := issue(fixtures.AgentTestEmpID)
	agentClient := func() *Client { return h.NewClient() }
	searchAs := func(token, q string) (int, int64) {
		t.Helper()
		// The search route requires an owner session; the token narrows the ACL.
		resp, err := h.Client.GetWithHeaders("/api/search?"+url.Values{"q": {q}}.Encode(), map[string]string{
			"X-Lignting-Agent-Token": token,
		})
		if err != nil {
			t.Fatalf("agent search: %v", err)
		}
		if resp.Status != http.StatusOK {
			return resp.Status, 0
		}
		var out struct {
			Total int64 `json:"total"`
		}
		resp.Data(t, &out)
		return resp.Status, out.Total
	}

	// Agent Token ACL: own department is visible; another department is not
	// (not even existence is leaked through total).
	if status, total := searchAs(devToken, "紫电"); status != 200 || total != 0 {
		t.Fatalf("cross-department search as dev agent = %d/%d, want 200/0", status, total)
	}
	if status, total := searchAs(testToken, "紫电"); status != 200 || total == 0 {
		t.Fatalf("own-department search as test agent = %d/%d, want 200/>0", status, total)
	}
	if status, total := searchAs(devToken, "限流"); status != 200 || total == 0 {
		t.Fatalf("project-visible search as dev agent = %d/%d, want 200/>0", status, total)
	}
	if status, _ := searchAs("lgt_invalid_token", "限流"); status != http.StatusUnauthorized {
		t.Fatalf("invalid agent token search = %d, want 401", status)
	}

	// Attachment authorization: same-department Agent can download; another
	// department Agent gets 403 and leaves a denied audit record.
	deptConv := h.Seed.Conversations["conv_dept_dev"]
	upload, err := h.Client.PostMultipart("/api/conversations/"+deptConv+"/attachments", "file", "ac08-report.txt", []byte("secret attachment"), nil)
	if err != nil {
		t.Fatalf("upload attachment: %v", err)
	}
	var att struct {
		ID           string `json:"id"`
		Conversation string `json:"conversation_id"`
		Filename     string `json:"filename"`
		SHA256       string `json:"sha256"`
	}
	upload.MustStatus(t, 200).Data(t, &att)
	if att.ID == "" || att.Conversation != deptConv || att.SHA256 == "" {
		t.Fatalf("attachment = %+v", att)
	}
	ownerDownload, err := h.Client.Get("/api/attachments/" + att.ID)
	if err != nil || ownerDownload.Status != 200 {
		t.Fatalf("owner attachment download = %v/%v", ownerDownload, err)
	}
	if !strings.Contains(ownerDownload.Header.Get("Content-Disposition"), "attachment") || ownerDownload.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("attachment headers = %v", ownerDownload.Header)
	}
	devDownload, err := agentClient().GetWithHeaders("/api/attachments/"+att.ID, map[string]string{"X-Lignting-Agent-Token": devToken})
	if err != nil || devDownload.Status != 200 {
		t.Fatalf("same-department attachment = %v/%v", devDownload, err)
	}
	crossDownload, err := agentClient().GetWithHeaders("/api/attachments/"+att.ID, map[string]string{"X-Lignting-Agent-Token": testToken})
	if err != nil {
		t.Fatalf("cross-department attachment: %v", err)
	}
	if crossDownload.Status != http.StatusForbidden {
		t.Fatalf("cross-department attachment = %d, want 403; body=%s", crossDownload.Status, string(crossDownload.Body))
	}
	denied, err := h.Store.QueryAudit(ctx, store.AuditFilter{Action: "attachment.download", Limit: 10})
	if err != nil || len(denied) == 0 {
		t.Fatalf("attachment.download audit = %d err=%v", len(denied), err)
	}

	// Rotation invalidates the old token immediately; revoke invalidates the new one.
	var rotated struct {
		Token string `json:"token"`
	}
	h.PostData(t, "/api/agents/"+fixtures.AgentDevAID+"/token/rotate", map[string]any{}, &rotated)
	if rotated.Token == "" || rotated.Token == devToken {
		t.Fatalf("rotated token = %q", rotated.Token)
	}
	if status, _ := searchAs(devToken, "限流"); status != http.StatusUnauthorized {
		t.Fatalf("old token after rotate = %d, want 401", status)
	}
	if status, _ := searchAs(rotated.Token, "限流"); status != 200 {
		t.Fatalf("new token after rotate = %d, want 200", status)
	}
	h.ExpectStatus(t, "DELETE", "/api/agents/"+fixtures.AgentDevAID+"/token", nil, 200)
	if status, _ := searchAs(rotated.Token, "限流"); status != http.StatusUnauthorized {
		t.Fatalf("revoked token = %d, want 401", status)
	}
	h.ExpectStatus(t, "DELETE", "/api/agents/"+fixtures.AgentDevAID+"/token", nil, 200)

	// Token plaintext never appears in agent DTOs, messages or audit details.
	var agentDTO map[string]any
	h.GetData(t, "/api/agents/"+fixtures.AgentDevAID, &agentDTO)
	for _, key := range []string{"token", "token_hash"} {
		if _, ok := agentDTO[key]; ok {
			t.Fatalf("agent DTO leaks %s: %+v", key, agentDTO)
		}
	}
	var leaks int
	if err := h.Store.DB.QueryRow("SELECT COUNT(*) FROM messages WHERE content LIKE '%'||?||'%'", rotated.Token).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatalf("token leaked into messages: %d err=%v", leaks, err)
	}
	audits, err := h.Store.QueryAudit(ctx, store.AuditFilter{Action: "agent.token_issue", Limit: 10})
	if err != nil || len(audits) != 2 {
		t.Fatalf("token issue audits = %d err=%v, want 2", len(audits), err)
	}
	for _, a := range audits {
		if strings.Contains(a.Detail, devToken) || strings.Contains(a.Detail, testToken) {
			t.Fatal("token plaintext leaked into audit detail")
		}
	}

	// Local password: set one, wrong login 401, right login 200.
	const password = "ac08-pass-123"
	var pw struct {
		HasPassword bool `json:"has_password"`
	}
	pwResp, err := h.Client.Do(ctx, "PUT", "/api/settings/password", map[string]any{"new_password": password})
	if err != nil {
		t.Fatalf("set password: %v", err)
	}
	pwResp.MustStatus(t, 200).Data(t, &pw)
	if !pw.HasPassword {
		t.Fatalf("password not set: %+v", pw)
	}
	badLogin, err := h.Client.PostJSON("/api/login", map[string]any{"password": "wrong-password"})
	if err != nil || badLogin.Status != http.StatusUnauthorized {
		t.Fatalf("wrong password login = %v/%v, want 401", badLogin, err)
	}
	goodLogin, err := h.Client.PostJSON("/api/login", map[string]any{"password": password})
	if err != nil || goodLogin.Status != 200 {
		t.Fatalf("correct password login = %v/%v, want 200", goodLogin, err)
	}
	h.EnsureSession() // refresh the CSRF token after the new session cookie

	// CSRF: an authenticated write without the header is rejected.
	csrf := h.Client.csrf
	h.Client.SetCSRF("")
	h.ExpectStatus(t, "POST", "/api/conversations", map[string]any{"type": "dm", "title": "无 CSRF"}, 403)
	h.Client.SetCSRF(csrf)

	// Origin: a cross-site Origin is rejected before authentication.
	rawReq, _ := http.NewRequest(http.MethodGet, h.BaseURL+"/api/health", nil)
	rawReq.Header.Set("Origin", "http://evil.example")
	rawResp, err := http.DefaultClient.Do(rawReq)
	if err != nil {
		t.Fatalf("origin request: %v", err)
	}
	_ = rawResp.Body.Close()
	if rawResp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site Origin = %d, want 403", rawResp.StatusCode)
	}

	// Emergency stop gates new turns immediately and can be resumed.
	var stopOn struct {
		EmergencyStop bool `json:"emergency_stop"`
	}
	h.PostData(t, "/api/admin/emergency-stop", map[string]any{"enabled": true}, &stopOn)
	if !stopOn.EmergencyStop {
		t.Fatalf("emergency stop = %+v", stopOn)
	}
	enqueueErr := h.Runtime.EnqueueUserMessage(ctx, domain.Message{
		ID: "msg_ac08_stop", ConversationID: fixtures.ConversationDMID,
		SenderType: domain.SenderUser, SenderID: fixtures.OwnerID, Content: "停止期间不应有 turn",
	})
	if enqueueErr == nil || !strings.Contains(enqueueErr.Error(), "紧急停止") {
		t.Fatalf("enqueue during emergency stop = %v, want emergency-stop error", enqueueErr)
	}
	var stopOff struct {
		EmergencyStop bool `json:"emergency_stop"`
	}
	h.PostData(t, "/api/admin/emergency-stop", map[string]any{"enabled": false}, &stopOff)
	if stopOff.EmergencyStop {
		t.Fatalf("emergency resume = %+v", stopOff)
	}

	// Audit trail: all security-relevant attempts are append-only and verifiable.
	for _, action := range []string{"agent.token_issue", "agent.token_rotate", "agent.token_revoke", "settings.password.set", "admin.emergency_stop"} {
		list, err := h.Store.QueryAudit(ctx, store.AuditFilter{Action: action, Limit: 10})
		if err != nil || len(list) == 0 {
			t.Errorf("audit %s missing: n=%d err=%v", action, len(list), err)
		}
	}
	if _, err := h.Store.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	t.Logf("AC-08 passed: token ACL/rotation/revoke, attachment 403, password, CSRF, Origin, emergency stop, audit chain")
}

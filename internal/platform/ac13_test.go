package platform_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

func createApproval(t *testing.T, e *env, kind, risk, scope string) domain.Approval {
	t.Helper()
	a, err := e.st.CreateApproval(context.Background(), domain.Approval{
		Kind: kind, Title: "测试审批", Risk: risk, Scope: scope, RequestedBy: "agent_x",
	})
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	return a
}

func rawPost(t *testing.T, url, token string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("raw request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Lignting-Agent-Token", token)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("raw post: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestAC13BatchSnoozeDelegateDualKey(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()

	// 批量审批：低风险 once/session 允许
	a1 := createApproval(t, e, domain.ApprovalG2, "low", "once")
	a2 := createApproval(t, e, domain.ApprovalG2, "medium", "session")
	status, out := e.doCSRF("POST", "/api/approvals/batch", map[string]any{
		"ids": []string{a1.ID, a2.ID}, "decision": "approved", "reason": "低风险批量",
	})
	d := e.mustOK(status, out)
	if d["count"].(float64) != 2 {
		t.Fatalf("batch result = %v", d)
	}
	for _, id := range []string{a1.ID, a2.ID} {
		a, _ := e.st.GetApproval(ctx, id)
		if a.Status != domain.ApprovalApproved {
			t.Fatalf("approval %s status = %s", id, a.Status)
		}
	}
	// 含不可逆 → 整批 403 且不部分执行
	a3 := createApproval(t, e, domain.ApprovalIrreversible, "high", "once")
	a4 := createApproval(t, e, domain.ApprovalG2, "low", "once")
	if status, _ := e.doCSRF("POST", "/api/approvals/batch", map[string]any{
		"ids": []string{a4.ID, a3.ID}, "decision": "approved",
	}); status != http.StatusForbidden {
		t.Fatalf("batch with irreversible = %d, want 403", status)
	}
	if a, _ := e.st.GetApproval(ctx, a4.ID); a.Status != domain.ApprovalPending {
		t.Fatalf("batch partially executed: %s", a.Status)
	}
	// 已处理 → 409
	if status, _ := e.doCSRF("POST", "/api/approvals/batch", map[string]any{
		"ids": []string{a1.ID}, "decision": "approved",
	}); status != http.StatusConflict {
		t.Fatalf("batch non-pending = %d, want 409", status)
	}

	// 稍后处理 + 到期恢复
	a5 := createApproval(t, e, domain.ApprovalG2, "low", "once")
	until := time.Now().Add(time.Second).UTC().Format(time.RFC3339)
	status, out = e.doCSRF("POST", "/api/approvals/"+a5.ID+"/snooze", map[string]any{"until": until})
	if status != http.StatusOK || e.mustOK(status, out)["snoozed_until"] == "" {
		t.Fatalf("snooze approval = %d %v", status, out)
	}
	if _, ok, _ := e.st.Setting(ctx, "snooze.approval."+a5.ID); !ok {
		t.Fatal("snooze setting missing")
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := e.srv.RunSchedulesOnce(ctx); err != nil {
		t.Fatalf("run schedules: %v", err)
	}
	if _, ok, _ := e.st.Setting(ctx, "snooze.approval."+a5.ID); ok {
		t.Fatal("snooze setting not cleared after due")
	}
	if audits, _ := e.st.QueryAudit(ctx, store.AuditFilter{Action: "snooze.restore"}); len(audits) == 0 {
		t.Fatal("snooze.restore audit missing")
	}

	// 通知稍后 → 到期恢复 unread
	n, _, err := e.st.CreateNotification(ctx, store.Notification{Level: "fyi", Title: "稍后通知", ObjectType: "test", ObjectID: "n_snooze"})
	if err != nil {
		t.Fatalf("notification: %v", err)
	}
	until2 := time.Now().Add(time.Second).UTC().Format(time.RFC3339)
	status, out = e.doCSRF("POST", "/api/notifications/"+n.ID+"/snooze", map[string]any{"until": until2})
	if status != http.StatusOK {
		t.Fatalf("snooze notification = %d %v", status, out)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := e.srv.RunSchedulesOnce(ctx); err != nil {
		t.Fatalf("run schedules 2: %v", err)
	}
	if got, err := e.st.GetNotification(ctx, n.ID); err != nil || got.Status != "unread" {
		t.Fatalf("notification after snooze = %+v err=%v", got, err)
	}

	// 授权助理代审
	plain, hash, _ := store.NewToken()
	assistant, err := e.st.CreateAgent(ctx, domain.Agent{Name: "代审助理", Kind: domain.AgentKindAssistant, TokenHash: hash})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	a6 := createApproval(t, e, domain.ApprovalG2, "low", "once")
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	status, out = e.doCSRF("POST", "/api/approvals/"+a6.ID+"/delegate", map[string]any{
		"to_agent_id": assistant.ID, "scope": "once", "expires_at": expires,
	})
	if status != http.StatusOK {
		t.Fatalf("delegate = %d %v", status, out)
	}
	status, out = rawPost(t, e.ts.URL+"/api/approvals/"+a6.ID+"/decide", plain, map[string]any{"decision": "approved", "reason": "代审"})
	if status != http.StatusOK {
		t.Fatalf("delegated decide = %d %v", status, out)
	}
	if got, _ := e.st.GetApproval(ctx, a6.ID); got.DecidedBy != assistant.ID {
		t.Fatalf("decided_by = %q, want %q", got.DecidedBy, assistant.ID)
	}
	// 无授权 Agent 403
	plain2, hash2, _ := store.NewToken()
	if _, err := e.st.CreateAgent(ctx, domain.Agent{Name: "未授权", Kind: domain.AgentKindAssistant, TokenHash: hash2}); err != nil {
		t.Fatalf("agent2: %v", err)
	}
	a7 := createApproval(t, e, domain.ApprovalG2, "low", "once")
	if status, _ := rawPost(t, e.ts.URL+"/api/approvals/"+a7.ID+"/decide", plain2, map[string]any{"decision": "approved"}); status != http.StatusForbidden {
		t.Fatalf("unauthorized agent decide = %d, want 403", status)
	}
	// 未登录/无 token → 401
	if status, _ := rawPost(t, e.ts.URL+"/api/approvals/"+a7.ID+"/decide", "", map[string]any{"decision": "approved"}); status != http.StatusUnauthorized {
		t.Fatalf("anonymous decide = %d, want 401", status)
	}

	// 双钥匙：irreversible 需 second_confirm
	a8 := createApproval(t, e, domain.ApprovalIrreversible, "high", "once")
	status, out = e.doCSRF("POST", "/api/approvals/"+a8.ID+"/decide", map[string]any{"decision": "approved"})
	if status != http.StatusConflict {
		t.Fatalf("dual key first = %d %v, want 409", status, out)
	}
	errObj, _ := out["error"].(map[string]any)
	if errObj["code"] != "dual_key_required" {
		t.Fatalf("dual key code = %v", errObj)
	}
	status, out = e.doCSRF("POST", "/api/approvals/"+a8.ID+"/decide", map[string]any{"decision": "approved", "second_confirm": true})
	if status != http.StatusOK {
		t.Fatalf("dual key second = %d %v", status, out)
	}
	for _, action := range []string{"approval.dual_key_required", "approval.dual_key_confirm"} {
		if audits, _ := e.st.QueryAudit(ctx, store.AuditFilter{Action: action}); len(audits) == 0 {
			t.Fatalf("audit %s missing", action)
		}
	}
	_ = fmt.Sprintf
}

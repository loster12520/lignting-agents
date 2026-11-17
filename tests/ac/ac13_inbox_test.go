//go:build acceptance

package ac

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// TestAC13AttentionInboxApprovals covers the R3/R8-available part of AC-13:
// approval TTL -> expired (never auto-execute) -> inbox notification, interrupt
// budget downgrades the 4th realtime notification to need_know, inbox status
// transitions, password setup + login failure rate limit, audit chain.
// Still R8: batch/稍后/授权到期, human-silence department degradation, dual-key
// batch rejection (task-22/26 follow-ups).
func TestAC13AttentionInboxApprovals(t *testing.T) {
	h := NewHarness(t, WithCoreServices())
	h.Setup("阿哲")
	ctx := context.Background()

	// 1) Four pending approvals with an already-past TTL. store.CreateApproval
	// also inserts approval.expire schedules; RunSchedulesOnce processes them.
	past := "2020-01-01T00:00:00.000000000Z"
	for i := 0; i < 4; i++ {
		if _, err := h.Store.CreateApproval(ctx, domain.Approval{
			Kind: domain.ApprovalG2, Title: "AC-13 TTL 审批", Risk: "medium",
			Status: domain.ApprovalPending, ExpiresAt: past,
		}); err != nil {
			t.Fatalf("create approval %d: %v", i, err)
		}
	}
	n, err := h.Platform.RunSchedulesOnce(ctx)
	if err != nil || n < 4 {
		t.Fatalf("run schedules = %d err=%v, want >=4", n, err)
	}

	// 2) All four approvals expired; none auto-executed (decided_by empty and
	// decision reason says TTL, not approved).
	var expired struct {
		Items []struct {
			ID             string `json:"id"`
			Status         string `json:"status"`
			DecidedBy      string `json:"decided_by"`
			DecisionReason string `json:"decision_reason"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/approvals?status="+domain.ApprovalExpired, &expired)
	if expired.Total != 4 {
		t.Fatalf("expired approvals = %d, want 4", expired.Total)
	}
	for _, a := range expired.Items {
		if a.DecidedBy != "" || !strings.Contains(a.DecisionReason, "不自动执行") {
			t.Errorf("expired approval auto-executed? %+v", a)
		}
	}

	// 3) Inbox: 3 blocking (budget) + 1 need_know (downgraded 4th), persisted.
	var inbox struct {
		Items []struct {
			ID    string `json:"id"`
			Level string `json:"level"`
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/notifications", &inbox)
	if inbox.Total != 4 || len(inbox.Items) != 4 {
		t.Fatalf("notifications = %d/%d, want 4", len(inbox.Items), inbox.Total)
	}
	blocking, needKnow, downgraded := 0, 0, false
	for _, n := range inbox.Items {
		switch n.Level {
		case "blocking":
			blocking++
		case "need_know":
			needKnow++
			if strings.Contains(n.Body, "预算已用完") {
				downgraded = true
			}
		}
	}
	if blocking != 3 || needKnow != 1 || !downgraded {
		t.Fatalf("inbox levels blocking=%d need_know=%d downgraded=%v; want 3/1/true (interrupt budget=3)", blocking, needKnow, downgraded)
	}

	// 4) Inbox status transition is persisted and filterable.
	firstID := inbox.Items[0].ID
	h.PostData(t, "/api/notifications/"+firstID+"/status", map[string]any{"status": "read"}, &struct{}{})
	var readInbox struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	h.GetData(t, "/api/notifications?status=read", &readInbox)
	if readInbox.Total != 1 {
		t.Fatalf("read notifications = %d, want 1", readInbox.Total)
	}

	// 5) Idempotent scheduler: second run creates no duplicate notifications.
	if n, err := h.Platform.RunSchedulesOnce(ctx); err != nil || n != 0 {
		t.Fatalf("second run = %d err=%v, want 0", n, err)
	}
	var inboxAgain struct {
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/notifications", &inboxAgain)
	if inboxAgain.Total != 4 {
		t.Fatalf("duplicate notifications: total=%d, want 4", inboxAgain.Total)
	}

	// 6) Local password + login failure rate limit (5 x 401 then 429).
	var setPW struct {
		HasPassword bool `json:"has_password"`
	}
	h.ExpectStatus(t, http.MethodPut, "/api/settings/password", map[string]any{"new_password": "newpass123"}, 200).
		Data(t, &setPW)
	if !setPW.HasPassword {
		t.Fatal("settings.password did not set has_password=true")
	}
	h.ExpectStatus(t, "POST", "/api/logout", nil, 200)
	login := func(password string) int {
		t.Helper()
		resp, err := h.Client.PostJSON("/api/login", map[string]any{"password": password})
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		return resp.Status
	}
	if got := login("newpass123"); got != 200 {
		t.Fatalf("correct password login status = %d", got)
	}
	h.ExpectStatus(t, "POST", "/api/logout", nil, 200)
	for i := 0; i < 6; i++ {
		want := 401
		if i == 5 {
			want = 429
		}
		if got := login("wrong-pass"); got != want {
			t.Fatalf("wrong password attempt %d status = %d, want %d", i+1, got, want)
		}
	}

	// 7) Audit: expiry, notifications, password, denied login; chain verifies.
	audits, err := h.Store.QueryAudit(ctx, store.AuditFilter{Limit: 300})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]bool{}
	denied := 0
	for _, a := range audits {
		actions[a.Action] = true
		if a.Action == "login" && a.Result == "denied" {
			denied++
		}
	}
	for _, want := range []string{"approval.expire", "notification.create", "settings.password.set", "login"} {
		if !actions[want] {
			t.Errorf("audit missing %q", want)
		}
	}
	if denied < 5 {
		t.Errorf("denied login audits = %d, want >=5", denied)
	}
	if _, err := h.Store.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	t.Log("AC-13 R3/R8 passed: TTL expired without execution, interrupt budget 3+1, inbox status, login rate limit")

	t.Run("batch_later_delegate_dual_key", func(t *testing.T) {
		ctx := context.Background()
		createApproval := func(kind, risk, scope string) domain.Approval {
			t.Helper()
			a, err := h.Store.CreateApproval(ctx, domain.Approval{
				Kind: kind, Title: "AC-13 批量/稍后/授权", Risk: risk, Scope: scope, RequestedBy: "agent_x",
			})
			if err != nil {
				t.Fatalf("create approval: %v", err)
			}
			return a
		}

		// Batch approvals: only low/medium risk once/session can be batched.
		a1 := createApproval(domain.ApprovalG2, "low", "once")
		a2 := createApproval(domain.ApprovalG2, "medium", "session")
		var batch struct {
			Count int `json:"count"`
		}
		h.PostData(t, "/api/approvals/batch", map[string]any{
			"ids": []string{a1.ID, a2.ID}, "decision": "approved", "reason": "低风险批量",
		}, &batch)
		if batch.Count != 2 {
			t.Fatalf("batch count = %d, want 2", batch.Count)
		}
		for _, id := range []string{a1.ID, a2.ID} {
			got, err := h.Store.GetApproval(ctx, id)
			if err != nil || got.Status != domain.ApprovalApproved {
				t.Fatalf("batched approval %s = %+v err=%v", id, got, err)
			}
		}
		a3 := createApproval(domain.ApprovalIrreversible, "high", "once")
		a4 := createApproval(domain.ApprovalG2, "low", "once")
		h.ExpectStatus(t, "POST", "/api/approvals/batch", map[string]any{
			"ids": []string{a4.ID, a3.ID}, "decision": "approved",
		}, 403)
		if got, _ := h.Store.GetApproval(ctx, a4.ID); got.Status != domain.ApprovalPending {
			t.Fatalf("batch partially executed: %s", got.Status)
		}
		h.ExpectStatus(t, "POST", "/api/approvals/batch", map[string]any{
			"ids": []string{a1.ID}, "decision": "approved",
		}, 409)

		// Snooze approval -> scheduler restores it and records audit.
		a5 := createApproval(domain.ApprovalG2, "low", "once")
		until := time.Now().Add(time.Second).UTC().Format(time.RFC3339)
		var snoozed struct {
			SnoozedUntil string `json:"snoozed_until"`
		}
		h.PostData(t, "/api/approvals/"+a5.ID+"/snooze", map[string]any{"until": until}, &snoozed)
		if snoozed.SnoozedUntil == "" {
			t.Fatalf("snooze approval = %+v", snoozed)
		}
		if _, ok, _ := h.Store.Setting(ctx, "snooze.approval."+a5.ID); !ok {
			t.Fatal("snooze approval setting missing")
		}
		time.Sleep(1200 * time.Millisecond)
		if _, err := h.Platform.RunSchedulesOnce(ctx); err != nil {
			t.Fatalf("run schedules: %v", err)
		}
		if _, ok, _ := h.Store.Setting(ctx, "snooze.approval."+a5.ID); ok {
			t.Fatal("snooze approval setting not cleared")
		}
		if list, _ := h.Store.QueryAudit(ctx, store.AuditFilter{Action: "snooze.restore"}); len(list) == 0 {
			t.Fatal("snooze.restore audit missing")
		}

		// Snooze notification -> due restores unread.
		n, _, err := h.Store.CreateNotification(ctx, store.Notification{Level: "fyi", Title: "AC-13 稍后通知", ObjectType: "test", ObjectID: "n_ac13_snooze"})
		if err != nil {
			t.Fatalf("create notification: %v", err)
		}
		until2 := time.Now().Add(time.Second).UTC().Format(time.RFC3339)
		var nSnoozed struct {
			Status string `json:"status"`
		}
		h.PostData(t, "/api/notifications/"+n.ID+"/snooze", map[string]any{"until": until2}, &nSnoozed)
		if nSnoozed.Status != "snoozed" {
			t.Fatalf("snooze notification = %+v", nSnoozed)
		}
		time.Sleep(1200 * time.Millisecond)
		if _, err := h.Platform.RunSchedulesOnce(ctx); err != nil {
			t.Fatalf("run schedules 2: %v", err)
		}
		gotN, err := h.Store.GetNotification(ctx, n.ID)
		if err != nil || gotN.Status != "unread" {
			t.Fatalf("notification after snooze = %+v err=%v", gotN, err)
		}

		// Delegated assistant can decide once; un-delegated / anonymous cannot.
		var org struct {
			Assistant map[string]any `json:"assistant"`
		}
		h.GetData(t, "/api/org", &org)
		assistantID, _ := org.Assistant["id"].(string)
		if assistantID == "" {
			t.Fatal("assistant id missing")
		}
		var tokenOut struct {
			Token string `json:"token"`
		}
		h.PostData(t, "/api/agents/"+assistantID+"/token", map[string]any{}, &tokenOut)
		a6 := createApproval(domain.ApprovalG2, "low", "once")
		h.PostData(t, "/api/approvals/"+a6.ID+"/delegate", map[string]any{
			"to_agent_id": assistantID, "scope": "once",
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		}, &map[string]any{})
		status := rawAgentPost(t, h.BaseURL+"/api/approvals/"+a6.ID+"/decide", tokenOut.Token, `{"decision":"approved","reason":"代审"}`)
		if status != 200 {
			t.Fatalf("delegated decide = %d, want 200", status)
		}
		if got, _ := h.Store.GetApproval(ctx, a6.ID); got.DecidedBy != assistantID {
			t.Fatalf("delegated decided_by = %q, want %q", got.DecidedBy, assistantID)
		}
		unauthorized, err := h.Store.CreateAgent(ctx, domain.Agent{Name: "未授权", Kind: domain.AgentKindAssistant})
		if err != nil {
			t.Fatalf("unauthorized agent: %v", err)
		}
		plain, hash, err := store.NewToken()
		if err != nil {
			t.Fatalf("new token: %v", err)
		}
		if err := h.Store.SetAgentTokenHash(ctx, unauthorized.ID, hash); err != nil {
			t.Fatalf("set token: %v", err)
		}
		a7 := createApproval(domain.ApprovalG2, "low", "once")
		if status := rawAgentPost(t, h.BaseURL+"/api/approvals/"+a7.ID+"/decide", plain, `{"decision":"approved"}`); status != 403 {
			t.Fatalf("unauthorized agent decide = %d, want 403", status)
		}
		if status := rawAgentPost(t, h.BaseURL+"/api/approvals/"+a7.ID+"/decide", "", `{"decision":"approved"}`); status != 401 {
			t.Fatalf("anonymous decide = %d, want 401", status)
		}

		// Dual key: irreversible approval needs explicit second_confirm.
		a8 := createApproval(domain.ApprovalIrreversible, "high", "once")
		first, err := h.Client.PostJSON("/api/approvals/"+a8.ID+"/decide", map[string]any{"decision": "approved"})
		if err != nil || first.Status != 409 {
			t.Fatalf("dual key first = %v/%v, want 409", first, err)
		}
		if !strings.Contains(string(first.Body), "dual_key_required") {
			t.Fatalf("dual key code missing: %s", string(first.Body))
		}
		h.PostData(t, "/api/approvals/"+a8.ID+"/decide", map[string]any{
			"decision": "approved", "second_confirm": true,
		}, &map[string]any{})
		for _, action := range []string{"approval.dual_key_required", "approval.dual_key_confirm"} {
			if list, _ := h.Store.QueryAudit(ctx, store.AuditFilter{Action: action}); len(list) == 0 {
				t.Errorf("audit %s missing", action)
			}
		}
		if _, err := h.Store.VerifyAuditChain(ctx); err != nil {
			t.Fatalf("audit chain: %v", err)
		}
		t.Log("AC-13 attention passed: batch guard, snooze restore, delegated/unauthorized decide, dual key, audit")
	})
}

// rawAgentPost sends an Agent-Token request without an owner cookie (delegated
// approval path). Origin is required by the platform guard.
func rawAgentPost(t *testing.T, url, token, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("raw request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if i := strings.Index(url, "/api/"); i > 0 {
		req.Header.Set("Origin", url[:i])
	}
	if token != "" {
		req.Header.Set("X-Lignting-Agent-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("raw post: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

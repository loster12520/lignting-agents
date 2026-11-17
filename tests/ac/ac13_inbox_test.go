//go:build acceptance

package ac

import (
	"context"
	"net/http"
	"strings"
	"testing"

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

	t.Run("batch_later_delegate_silence_dual_key", func(t *testing.T) {
		SkipSkeleton(t, "AC-13 batch/snooze/delegation/human-silence/dual-key batch", "R8", "审批中心批量与授权 API 未实现（task-22）")
	})
}

package platform_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// 审批 TTL：到期标记 expired + 收件箱通知；打扰预算 3 条后降级 need_know；幂等。
func TestApprovalTTLNotificationsAndBudget(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()
	past := "2020-01-01T00:00:00.000000000Z"
	for i := 0; i < 4; i++ {
		if _, err := e.st.CreateApproval(ctx, domain.Approval{Kind: domain.ApprovalG2, Title: "TTL 测试", ExpiresAt: past}); err != nil {
			t.Fatalf("create approval: %v", err)
		}
	}
	n, err := e.srv.RunSchedulesOnce(ctx)
	if err != nil || n < 4 {
		t.Fatalf("run schedules = %d err=%v, want >=4", n, err)
	}
	expired, err := e.st.ListApprovals(ctx, store.ApprovalFilter{Status: domain.ApprovalExpired})
	if err != nil || len(expired) != 4 {
		t.Fatalf("expired approvals = %d err=%v, want 4", len(expired), err)
	}
	status, out := e.do("GET", "/api/notifications", nil)
	d := e.mustOK(status, out)
	items, _ := d["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("notifications = %d, want 4", len(items))
	}
	blocking, needKnow := 0, 0
	for _, it := range items {
		switch it.(map[string]any)["level"] {
		case "blocking":
			blocking++
		case "need_know":
			needKnow++
		}
	}
	if blocking != 3 || needKnow != 1 {
		t.Fatalf("levels blocking=%d need_know=%d, want 3/1 (interrupt budget)", blocking, needKnow)
	}
	// 再跑一次：不重复通知、审批不自动执行
	if n, err := e.srv.RunSchedulesOnce(ctx); err != nil || n != 0 {
		t.Fatalf("second run = %d err=%v, want 0 (idempotent)", n, err)
	}
	status, out = e.do("GET", "/api/notifications", nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) != 4 {
		t.Fatalf("duplicate notifications: %d", len(items))
	}
	// 收件箱状态流转
	first := items[0].(map[string]any)
	status, out = e.doCSRF("POST", "/api/notifications/"+first["id"].(string)+"/status", map[string]any{"status": "read"})
	if status != http.StatusOK {
		t.Fatalf("mark read status = %d %v", status, out)
	}
	status, out = e.do("GET", "/api/notifications?status=read", nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) != 1 {
		t.Fatalf("read notifications = %d, want 1", len(items))
	}
}

// 调度取消 + 本地密码设置 + 登录失败限流。
func TestSchedulesCancelPasswordAndLoginLimit(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()
	sc, err := e.st.InsertSchedule(ctx, store.Schedule{Kind: "test.noop", DueAt: store.Now()})
	if err != nil {
		t.Fatalf("insert schedule: %v", err)
	}
	status, out := e.do("GET", "/api/schedules", nil)
	if d := e.mustOK(status, out); len(d["items"].([]any)) == 0 {
		t.Fatalf("schedules empty: %v", out)
	}
	status, out = e.doCSRF("POST", "/api/schedules/"+sc.ID+"/cancel", nil)
	if status != http.StatusOK {
		t.Fatalf("cancel schedule status = %d %v", status, out)
	}
	if got, _ := e.st.GetSchedule(ctx, sc.ID); got.Status != "canceled" {
		t.Fatalf("schedule status = %q, want canceled", got.Status)
	}

	// 设置密码
	status, out = e.doCSRF("PUT", "/api/settings/password", map[string]any{"new_password": "newpass123"})
	if d := e.mustOK(status, out); d["has_password"] != true {
		t.Fatalf("set password = %v", d)
	}
	status, out = e.do("GET", "/api/settings", nil)
	if e.mustOK(status, out)["has_password"] != true {
		t.Fatal("settings.has_password = false after set")
	}
	// 正确密码登录
	if status, _ := e.do("POST", "/api/logout", nil); status != http.StatusOK {
		t.Fatal("logout failed")
	}
	if status, out := e.do("POST", "/api/login", map[string]any{"password": "newpass123"}); status != http.StatusOK {
		t.Fatalf("correct password login = %d %v", status, out)
	}
	// 连续错误密码：前 5 次 401，第 6 次 429
	_, _ = e.do("POST", "/api/logout", nil)
	for i := 0; i < 6; i++ {
		status, _ := e.do("POST", "/api/login", map[string]any{"password": "wrong-pass"})
		want := http.StatusUnauthorized
		if i == 5 {
			want = http.StatusTooManyRequests
		}
		if status != want {
			t.Fatalf("login attempt %d status = %d, want %d", i+1, status, want)
		}
	}
}

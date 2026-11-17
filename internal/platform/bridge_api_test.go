package platform_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

func TestBridgeCreateCloseAndExpire(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()
	agent, err := e.st.CreateAgent(ctx, domain.Agent{Name: "跨部门主管", Kind: domain.AgentKindSupervisor, DepartmentID: "dept_a"})
	if err != nil {
		t.Fatalf("agent: %v", err)
	}

	// 非法 scope / 过期时间 → 400
	if status, _ := e.doCSRF("POST", "/api/bridges", map[string]any{
		"scope": "bogus", "from_type": "agent", "from_id": agent.ID, "to_type": "user", "to_id": "owner",
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}); status != http.StatusBadRequest {
		t.Fatalf("bad scope = %d, want 400", status)
	}
	if status, _ := e.doCSRF("POST", "/api/bridges", map[string]any{
		"scope": "cross_dept", "from_type": "agent", "from_id": agent.ID, "to_type": "user", "to_id": "owner",
		"expires_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	}); status != http.StatusBadRequest {
		t.Fatalf("past expiry = %d, want 400", status)
	}

	// 创建 → active
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	status, out := e.doCSRF("POST", "/api/bridges", map[string]any{
		"scope": "cross_dept", "from_type": "agent", "from_id": agent.ID, "to_type": "user", "to_id": "owner",
		"expires_at": expires, "reason": "跨部门临时对接",
	})
	d := e.mustOK(status, out)
	bridgeID, _ := d["id"].(string)
	if d["status"] != "active" || bridgeID == "" {
		t.Fatalf("bridge = %v", d)
	}
	active, err := e.st.GetActiveBridge(ctx, "agent", agent.ID, "user", "owner")
	if err != nil || active == nil || active.ID != bridgeID {
		t.Fatalf("active bridge = %+v err=%v", active, err)
	}
	status, out = e.do("GET", "/api/bridges?status=active", nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) != 1 {
		t.Fatalf("bridge list = %v", out)
	}

	// 关闭
	status, out = e.doCSRF("DELETE", "/api/bridges/"+bridgeID, nil)
	if d := e.mustOK(status, out); d["status"] != "closed" {
		t.Fatalf("close = %v", d)
	}
	if active, _ := e.st.GetActiveBridge(ctx, "agent", agent.ID, "user", "owner"); active != nil {
		t.Fatalf("closed bridge still active")
	}

	// 到期自动关闭（调度器）
	short := time.Now().Add(time.Second).UTC().Format(store.Now()[:0] + "2006-01-02T15:04:05.000000000Z07:00")
	_ = short
	due := domain.Bridge{Scope: domain.BridgeCrossDept, FromType: "department", FromID: "dept_a", ToType: "department", ToID: "dept_b",
		ExpiresAt: "2020-01-01T00:00:00.000000000Z", Status: "active"}
	dueBridge, err := e.st.CreateBridge(ctx, due)
	if err != nil {
		t.Fatalf("due bridge: %v", err)
	}
	if _, err := e.srv.RunSchedulesOnce(ctx); err != nil {
		t.Fatalf("run schedules: %v", err)
	}
	got, err := e.st.GetBridge(ctx, dueBridge.ID)
	if err != nil || got.Status != "expired" {
		t.Fatalf("expired bridge = %+v err=%v", got, err)
	}
	audits, err := e.st.QueryAudit(ctx, store.AuditFilter{Action: "bridge.expire"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("bridge.expire audits = %d err=%v", len(audits), err)
	}
	// 通知 + 事件
	status, out = e.do("GET", "/api/notifications", nil)
	items, _ := e.mustOK(status, out)["items"].([]any)
	foundNotify := false
	for _, it := range items {
		if it.(map[string]any)["object_type"] == "bridge" {
			foundNotify = true
		}
	}
	if !foundNotify {
		t.Fatalf("bridge expiry notification missing: %v", out)
	}
	// 创建/关闭审计
	for _, action := range []string{"bridge.open", "bridge.close"} {
		if audits, err := e.st.QueryAudit(ctx, store.AuditFilter{Action: action}); err != nil || len(audits) != 1 {
			t.Fatalf("%s audits = %d err=%v", action, len(audits), err)
		}
	}
}

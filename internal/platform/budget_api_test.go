package platform_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

func TestBudgetSettingsAndAlertProjection(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()
	dept, _ := e.st.CreateDepartment(ctx, domain.Department{Name: "研发部", Kind: "engineering"})

	status, out := e.doCSRF("PUT", "/api/settings/budgets", map[string]any{
		"daily_cny": 10, "monthly_cny": 100, "departments": map[string]float64{dept.ID: 5},
	})
	d := e.mustOK(status, out)
	if d["daily_cny"].(float64) != 10 || d["monthly_cny"].(float64) != 100 {
		t.Fatalf("budget settings = %v", d)
	}
	status, out = e.do("GET", "/api/settings/budgets", nil)
	d = e.mustOK(status, out)
	if d["departments"].(map[string]any)[dept.ID].(float64) != 5 {
		t.Fatalf("department budget = %v", d)
	}
	if status, _ := e.doCSRF("PUT", "/api/settings/budgets", map[string]any{"daily_cny": -1}); status != http.StatusBadRequest {
		t.Fatalf("negative budget status = %d, want 400", status)
	}

	// 80% warning 事件 → 去重收件箱通知
	warnPayload := `{"scope":"global_day","used":8.5,"limit":10,"key":"global_day:2026-11-17"}`
	if _, err := e.st.AppendEvent(ctx, domain.Event{
		Type: "budget.warning", ObjectType: "agent", ObjectID: e.assistantID,
		ActorType: "system", ActorID: "core", Payload: warnPayload,
	}); err != nil {
		t.Fatalf("warning event: %v", err)
	}
	if _, err := e.srv.RunSchedulesOnce(ctx); err != nil {
		t.Fatalf("run schedules: %v", err)
	}
	status, out = e.do("GET", "/api/notifications", nil)
	items, _ := e.mustOK(status, out)["items"].([]any)
	var warnCount, blockCount int
	for _, it := range items {
		level := it.(map[string]any)["level"].(string)
		switch level {
		case "need_know":
			warnCount++
		case "blocking":
			blockCount++
		}
	}
	if warnCount != 1 {
		t.Fatalf("warning notifications = %d, want 1; items=%v", warnCount, items)
	}
	// 再次扫描不重复
	if _, err := e.srv.RunSchedulesOnce(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	status, out = e.do("GET", "/api/notifications", nil)
	if n := len(e.mustOK(status, out)["items"].([]any)); n != 1 {
		t.Fatalf("duplicate budget notifications: %d", n)
	}

	// 100% blocked 事件 → blocking 通知 + 平台投影审计
	blockPayload := `{"scope":"department","used":5,"limit":5,"key":"dept:` + dept.ID + `:2026-11"}`
	if _, err := e.st.AppendEvent(ctx, domain.Event{
		Type: "budget.blocked", ObjectType: "department", ObjectID: dept.ID,
		ActorType: "system", ActorID: "core", Payload: blockPayload,
	}); err != nil {
		t.Fatalf("blocked event: %v", err)
	}
	if _, err := e.srv.RunSchedulesOnce(ctx); err != nil {
		t.Fatalf("run after blocked: %v", err)
	}
	status, out = e.do("GET", "/api/notifications", nil)
	blockCount = 0
	for _, it := range e.mustOK(status, out)["items"].([]any) {
		item := it.(map[string]any)
		if item["level"] == "blocking" && strings.Contains(item["title"].(string), "预算") {
			blockCount++
		}
	}
	if blockCount != 1 {
		t.Fatalf("blocking budget notifications = %d, want 1", blockCount)
	}
	audits, err := e.st.QueryAudit(ctx, store.AuditFilter{Action: "budget.update"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("budget.update audits = %d err=%v", len(audits), err)
	}
	projected, err := e.st.QueryAudit(ctx, store.AuditFilter{Action: "budget.blocked"})
	if err != nil || len(projected) != 1 {
		t.Fatalf("budget.blocked projection audits = %d err=%v", len(projected), err)
	}
}

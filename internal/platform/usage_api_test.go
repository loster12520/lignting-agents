package platform_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
)

func TestUsageAPI(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()
	dept, _ := e.st.CreateDepartment(ctx, domain.Department{Name: "研发部", Kind: "engineering"})
	if _, err := e.st.CreateAgent(ctx, domain.Agent{ID: "agent_usage", Name: "用量员工", Kind: domain.AgentKindEmployee, DepartmentID: dept.ID}); err != nil {
		t.Fatalf("agent: %v", err)
	}
	if err := e.st.InsertUsage(ctx, domain.Usage{
		AgentID: "agent_usage", TaskID: "task_u", Model: "deepseek-flash",
		PromptTokens: 100, CompletionTokens: 40, CachedTokens: 10, Cost: 0.05,
		CreatedAt: "2026-01-01T10:00:00.000000000Z",
	}); err != nil {
		t.Fatalf("usage: %v", err)
	}

	status, out := e.do("GET", "/api/usage", nil)
	d := e.mustOK(status, out)
	if d["PromptTokens"].(float64) != 100 || d["CompletionTokens"].(float64) != 40 || d["CachedTokens"].(float64) != 10 {
		t.Fatalf("usage totals = %v", d)
	}

	status, out = e.do("GET", "/api/usage?group_by=department", nil)
	items, _ := e.mustOK(status, out)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["department_id"] != dept.ID {
		t.Fatalf("department usage = %v", out)
	}
	status, out = e.do("GET", "/api/usage?group_by=agent&department_id="+dept.ID, nil)
	items, _ = e.mustOK(status, out)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["agent_id"] != "agent_usage" {
		t.Fatalf("agent usage = %v", out)
	}
	status, out = e.do("GET", "/api/usage?group_by=day&from=2026-01-01&to=2026-01-01", nil)
	items, _ = e.mustOK(status, out)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("day usage = %v", out)
	}
	if status, _ := e.do("GET", "/api/usage?group_by=bogus", nil); status != http.StatusBadRequest {
		t.Fatalf("bogus group_by status = %d, want 400", status)
	}
}

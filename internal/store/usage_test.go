package store_test

import (
	"context"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

func TestSumUsageAndDepartmentGrouping(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	deptA, _ := st.CreateDepartment(ctx, domain.Department{Name: "研发部", Kind: "engineering"})
	deptB, _ := st.CreateDepartment(ctx, domain.Department{Name: "测试部", Kind: "qa"})
	if _, err := st.CreateAgent(ctx, domain.Agent{ID: "agent_a", Name: "A", Kind: domain.AgentKindEmployee, DepartmentID: deptA.ID}); err != nil {
		t.Fatalf("agentA: %v", err)
	}
	if _, err := st.CreateAgent(ctx, domain.Agent{ID: "agent_b", Name: "B", Kind: domain.AgentKindEmployee, DepartmentID: deptB.ID}); err != nil {
		t.Fatalf("agentB: %v", err)
	}
	rows := []domain.Usage{
		{AgentID: "agent_a", TaskID: "task_a", Model: "deepseek-flash", PromptTokens: 10, CompletionTokens: 5, Cost: 0.01, CreatedAt: "2026-01-01T10:00:00.000000000Z"},
		{AgentID: "agent_b", TaskID: "task_b", Model: "deepseek-flash", PromptTokens: 20, CompletionTokens: 10, Cost: 0.02, CreatedAt: "2026-01-02T10:00:00.000000000Z"},
	}
	for _, u := range rows {
		if err := st.InsertUsage(ctx, u); err != nil {
			t.Fatalf("usage: %v", err)
		}
	}
	totals, err := st.SumUsage(ctx, domain.UsageQuery{})
	if err != nil || totals.PromptTokens != 30 || totals.CompletionTokens != 15 || totals.Cost < 0.029 {
		t.Fatalf("totals = %+v err=%v", totals, err)
	}
	byAgent, _ := st.SumUsage(ctx, domain.UsageQuery{AgentID: "agent_a"})
	if byAgent.PromptTokens != 10 {
		t.Fatalf("agent totals = %+v", byAgent)
	}
	byDept, _ := st.SumUsage(ctx, domain.UsageQuery{DepartmentID: deptA.ID})
	if byDept.PromptTokens != 10 || byDept.Cost < 0.009 {
		t.Fatalf("dept totals = %+v", byDept)
	}
	windowed, _ := st.SumUsage(ctx, domain.UsageQuery{From: "2026-01-02T00:00:00.000000000Z"})
	if windowed.PromptTokens != 20 {
		t.Fatalf("window totals = %+v", windowed)
	}
	groups, err := st.QueryUsage(ctx, store.UsageFilter{GroupBy: "department"})
	if err != nil || len(groups) != 2 {
		t.Fatalf("department groups = %+v err=%v", groups, err)
	}
	seen := map[string]int64{}
	for _, g := range groups {
		seen[g.DepartmentID] = g.PromptTokens
	}
	if seen[deptA.ID] != 10 || seen[deptB.ID] != 20 {
		t.Fatalf("department group tokens = %+v", seen)
	}
	filtered, err := st.QueryUsage(ctx, store.UsageFilter{GroupBy: "agent", DepartmentID: deptA.ID})
	if err != nil || len(filtered) != 1 || filtered[0].AgentID != "agent_a" {
		t.Fatalf("filtered groups = %+v err=%v", filtered, err)
	}
}

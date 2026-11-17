package platform_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// AC-04 主路径：部门内讨论 → 跨部门立场 R1/R2 → 超限 → 升级 → 决策 → 检索/异议/重开。
func TestR6DebateDiscussionDecisionFlow(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()
	e.srv.SetDebateService(core.NewDebateService(e.st))

	deptA, _ := e.st.CreateDepartment(ctx, domain.Department{Name: "研发部", Kind: "engineering"})
	deptB, _ := e.st.CreateDepartment(ctx, domain.Department{Name: "测试部", Kind: "qa"})
	supA, err := e.st.CreateAgent(ctx, domain.Agent{Name: "研发主管", Kind: domain.AgentKindSupervisor, DepartmentID: deptA.ID})
	if err != nil {
		t.Fatalf("supA: %v", err)
	}
	supB, err := e.st.CreateAgent(ctx, domain.Agent{Name: "测试主管", Kind: domain.AgentKindSupervisor, DepartmentID: deptB.ID})
	if err != nil {
		t.Fatalf("supB: %v", err)
	}
	empA, err := e.st.CreateAgent(ctx, domain.Agent{Name: "研发员工", Kind: domain.AgentKindEmployee, DepartmentID: deptA.ID})
	if err != nil {
		t.Fatalf("empA: %v", err)
	}
	conv, err := e.st.CreateConversation(ctx, domain.Conversation{
		Type: domain.ConvCrossDept, Title: "登录方案跨部门评审", Visibility: domain.VisibilityProject, CreatedBy: "owner",
	}, []domain.ConversationMember{
		{PrincipalType: domain.SenderAgent, PrincipalID: supA.ID, Role: "member"},
		{PrincipalType: domain.SenderAgent, PrincipalID: supB.ID, Role: "member"},
		{PrincipalType: domain.SenderUser, PrincipalID: store.OwnerID, Role: "observer"},
	}, store.Actor{Type: domain.SenderSystem, ID: "system"})
	if err != nil {
		t.Fatalf("conv: %v", err)
	}

	// 1) 部门内讨论（员工带视角）
	status, out := e.doCSRF("POST", "/api/conversations/"+conv.ID+"/discussions", map[string]any{
		"actor_agent_id": empA.ID, "debate_id": "debate_login", "perspective": "接口",
		"stance": "接口越简单越好", "evidence": `["e0"]`,
	})
	if status != http.StatusOK {
		t.Fatalf("discussion = %d %v", status, out)
	}

	// 2) 跨部门立场 R1/R2（必须有 evidence_ids）
	status, out = e.doCSRF("POST", "/api/conversations/"+conv.ID+"/stances", map[string]any{
		"actor_agent_id": supA.ID, "debate_id": "debate_login", "round": 1,
		"stance": "用 REST + OpenAPI", "evidence_ids": []string{"e1"},
	})
	if status != http.StatusOK {
		t.Fatalf("stance R1 = %d %v", status, out)
	}
	status, out = e.doCSRF("POST", "/api/conversations/"+conv.ID+"/stances", map[string]any{
		"actor_agent_id": supB.ID, "debate_id": "debate_login", "round": 2,
		"stance": "需要契约测试证据", "evidence_ids": []string{"e2"},
	})
	if status != http.StatusOK {
		t.Fatalf("stance R2 = %d %v", status, out)
	}
	// 缺证据 → 400
	if status, out := e.doCSRF("POST", "/api/conversations/"+conv.ID+"/stances", map[string]any{
		"actor_agent_id": supA.ID, "debate_id": "debate_login", "round": 3, "stance": "无证据",
	}); status != http.StatusBadRequest {
		t.Fatalf("stance without evidence = %d %v", status, out)
	}
	// 重复立场且无新证据 → 409
	if status, out := e.doCSRF("POST", "/api/conversations/"+conv.ID+"/stances", map[string]any{
		"actor_agent_id": supB.ID, "debate_id": "debate_login", "round": 2,
		"stance": "需要契约测试证据", "evidence_ids": []string{"e2"},
	}); status != http.StatusConflict {
		t.Fatalf("duplicate stance = %d %v", status, out)
	}

	// 3) 升级包 L2
	status, out = e.doCSRF("POST", "/api/debates/debate_login/escalate", map[string]any{
		"actor_agent_id": supB.ID, "conversation_id": conv.ID, "task_id": "task_login",
		"level": "L2", "issue": "接口风格争议", "evidence": `["e1","e2"]`,
		"options": `["REST","GraphQL"]`, "recommendation": "REST 优先", "consequence": "若选 GraphQL 需延期",
	})
	if status != http.StatusOK {
		t.Fatalf("escalate = %d %v", status, out)
	}
	if e.data(out)["status"] != domain.DecisionProposed {
		t.Fatalf("escalation decision = %v", out)
	}

	// 4) 关闭辩论 → 不可变决策（保留异议）
	status, out = e.doCSRF("POST", "/api/debates/debate_login/close", map[string]any{
		"actor_agent_id": e.assistantID, "title": "登录接口采用 REST",
		"decision": "REST + OpenAPI", "rationale": "团队熟悉度与工具链",
		"dissent": "测试部门要求补齐契约测试", "affected_tasks": `["task_login"]`,
		"affected_artifacts": `[]`, "revisit_trigger": "GraphQL 需求出现",
	})
	if status != http.StatusOK {
		t.Fatalf("close = %d %v", status, out)
	}
	decision := e.data(out)
	decisionID, _ := decision["id"].(string)
	if decision["status"] != domain.DecisionAccepted || decisionID == "" {
		t.Fatalf("decision = %v", decision)
	}

	// 5) 检索/详情/异议
	status, out = e.do("GET", "/api/decisions?task_id=task_login", nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) == 0 {
		t.Fatalf("decisions by task empty: %v", out)
	}
	status, out = e.do("GET", "/api/decisions?q=OpenAPI", nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) == 0 {
		t.Fatalf("decision search empty: %v", out)
	}
	status, out = e.do("GET", "/api/decisions/"+decisionID+"/detail", nil)
	if e.mustOK(status, out)["reopened"].(float64) != 0 {
		t.Fatalf("decision detail = %v", out)
	}
	status, out = e.doCSRF("POST", "/api/decisions/"+decisionID+"/dissent", map[string]any{
		"actor_agent_id": supB.ID, "dissent": "测试部门保留异议：需要压测证据",
	})
	if status != http.StatusOK {
		t.Fatalf("dissent = %d %v", status, out)
	}
	newID := e.data(out)["id"].(string)
	old, err := e.st.GetDecision(ctx, decisionID)
	if err != nil || old.Status != domain.DecisionSuperseded || old.SupersededBy != newID {
		t.Fatalf("old decision after dissent = %+v err=%v", old, err)
	}

	// 6) 新证据重开（≤1 次）：第一次成功，第二次 403
	status, out = e.doCSRF("POST", "/api/debates/"+newID+"/reopen", map[string]any{
		"actor_agent_id": e.assistantID, "new_evidence": "压测报告 v1",
	})
	if status != http.StatusOK {
		t.Fatalf("reopen = %d %v", status, out)
	}
	reopenedID := e.data(out)["id"].(string)
	status, out = e.doCSRF("POST", "/api/debates/"+reopenedID+"/reopen", map[string]any{
		"actor_agent_id": e.assistantID, "new_evidence": "压测报告 v2",
	})
	if status != http.StatusForbidden {
		t.Fatalf("second reopen = %d %v, want 403", status, out)
	}

	// 7) 讨论/立场消息落库且带 meta kind
	status, out = e.do("GET", "/api/conversations/"+conv.ID+"/messages", nil)
	items, _ := e.mustOK(status, out)["items"].([]any)
	kinds := 0
	for _, it := range items {
		meta, _ := it.(map[string]any)["meta"].(string)
		if strings.Contains(meta, "dept.discussion") || strings.Contains(meta, "debate.stance") {
			kinds++
		}
	}
	if kinds < 3 {
		t.Fatalf("debate messages = %d, want >=3; items=%v", kinds, items)
	}
}

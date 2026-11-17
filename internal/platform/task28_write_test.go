package platform_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
)

// AC-03/AC-12 主路径：任务 DoR → 状态机 → 证据/主管核对 → 产出物 → 评审返工 → G3 → 决策。
func TestR4TaskArtifactReviewG3(Act *testing.T) {
	t := Act
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()
	e.srv.SetWorkService(core.NewWorkService(e.st))
	dept, err := e.st.CreateDepartment(ctx, domain.Department{Name: "研发部", Kind: "engineering"})
	if err != nil {
		t.Fatalf("dept: %v", err)
	}
	reviewer, err := e.st.CreateAgent(ctx, domain.Agent{Name: "评审员", Kind: domain.AgentKindEmployee, DepartmentID: dept.ID})
	if err != nil {
		t.Fatalf("reviewer: %v", err)
	}
	sup, err := e.st.CreateAgent(ctx, domain.Agent{Name: "研发主管", Kind: domain.AgentKindSupervisor, DepartmentID: dept.ID})
	if err != nil {
		t.Fatalf("supervisor: %v", err)
	}

	status, out := e.doCSRF("POST", "/api/tasks", map[string]any{
		"title": "实现登录", "department_id": dept.ID, "owner_id": e.assistantID,
		"reviewer_id": reviewer.ID, "approver_id": sup.ID, "perspective": "接口",
		"priority": "P1", "deadline": "2026-12-31T00:00:00Z", "budget_tokens": 1000,
		"acceptance": []string{"有测试", "有日志"},
	})
	if status != http.StatusOK {
		t.Fatalf("create task = %d %v", status, out)
	}
	d := e.data(out)
	task := d["task"].(map[string]any)
	taskID := task["id"].(string)
	if task["status"] != "proposed" || task["dor"] == "" {
		t.Fatalf("task = %v", task)
	}
	criteria, _ := d["criteria"].([]any)
	if len(criteria) != 2 {
		t.Fatalf("criteria = %d, want 2", len(criteria))
	}

	// 状态机：proposed -> ready
	status, out = e.doCSRF("POST", "/api/tasks/"+taskID+"/transition", map[string]any{"to": "ready", "reason": "DoR 齐全"})
	if status != http.StatusOK {
		t.Fatalf("transition = %d %v", status, out)
	}

	// 证据 + 主管核对（两条都 pass）
	for _, c := range criteria {
		cid := c.(map[string]any)["id"].(string)
		if status, out := e.doCSRF("POST", "/api/tasks/"+taskID+"/criteria/"+cid+"/evidence", map[string]any{"evidence": `["test-log"]`, "actor_agent_id": sup.ID}); status != http.StatusOK {
			t.Fatalf("criterion evidence = %d %v", status, out)
		}
		if status, out := e.doCSRF("POST", "/api/tasks/"+taskID+"/criteria/"+cid+"/verdict", map[string]any{"verdict": "approved", "actor_agent_id": sup.ID}); status != http.StatusOK {
			t.Fatalf("criterion verdict = %d %v", status, out)
		}
	}

	// 产出物 v1
	status, out = e.doCSRF("POST", "/api/artifacts", map[string]any{
		"type": "prd", "title": "登录 PRD", "task_id": taskID, "department_id": dept.ID, "content": "登录需求 v1",
	})
	if status != http.StatusOK {
		t.Fatalf("create artifact = %d %v", status, out)
	}
	ad := e.data(out)
	artifact := ad["artifact"].(map[string]any)
	version := ad["version"].(map[string]any)
	artifactID := artifact["id"].(string)
	if version["version"].(float64) != 1 || version["content_hash"] == "" {
		t.Fatalf("artifact version = %v", version)
	}

	// 评审：挑战单 + request_changes 裁决（作者≠评审者）
	status, out = e.doCSRF("POST", "/api/reviews", map[string]any{
		"artifact_id": artifactID, "claim": "缺少限流", "severity": "minor",
		"evidence": `["e-1"]`, "actor_agent_id": reviewer.ID,
	})
	if status != http.StatusOK {
		t.Fatalf("create review = %d %v", status, out)
	}
	reviewID := e.data(out)["id"].(string)
	status, out = e.doCSRF("POST", "/api/reviews/"+reviewID+"/verdicts", map[string]any{
		"verdict": "request_changes", "conditions": `["补限流"]`, "rationale": "缺少限流", "actor_agent_id": reviewer.ID,
	})
	if status != http.StatusOK {
		t.Fatalf("verdict = %d %v", status, out)
	}
	// 返工：新版本
	status, out = e.doCSRF("POST", "/api/reviews/"+reviewID+"/rework", map[string]any{"content": "登录需求 v2（含限流）"})
	if status != http.StatusOK {
		t.Fatalf("rework = %d %v", status, out)
	}
	if v := e.data(out); v["version"].(float64) != 2 {
		t.Fatalf("rework version = %v", v)
	}

	// G3 提交 + 人类批准
	status, out = e.doCSRF("POST", "/api/tasks/"+taskID+"/g3", map[string]any{"risk": "medium", "residual_risks": "", "actor_agent_id": sup.ID})
	if status != http.StatusOK {
		t.Fatalf("submit g3 = %d %v", status, out)
	}
	appr := e.data(out)["approval"].(map[string]any)
	if appr["kind"] != "G3" {
		t.Fatalf("g3 approval = %v", appr)
	}
	status, out = e.doCSRF("POST", "/api/approvals/"+appr["id"].(string)+"/decide", map[string]any{"decision": "approved", "reason": "验收通过"})
	if status != http.StatusOK || e.data(out)["status"] != "approved" {
		t.Fatalf("decide g3 = %d %v", status, out)
	}

	// 决策记录 + supersede
	status, out = e.doCSRF("POST", "/api/decisions", map[string]any{
		"title": "不用 GraphQL", "context": "接口评审", "decision": "REST + OpenAPI",
		"rationale": "团队熟悉度", "affected_tasks": `["` + taskID + `"]`,
	})
	if status != http.StatusOK {
		t.Fatalf("create decision = %d %v", status, out)
	}
	oldID := e.data(out)["id"].(string)
	status, out = e.doCSRF("POST", "/api/decisions", map[string]any{"title": "改用 tRPC", "context": "复盘", "decision": "tRPC", "rationale": "性能"})
	if status != http.StatusOK {
		t.Fatalf("create decision2 = %d %v", status, out)
	}
	newID := e.data(out)["id"].(string)
	status, out = e.doCSRF("POST", "/api/decisions/"+oldID+"/supersede", map[string]any{"new_id": newID})
	if status != http.StatusOK {
		t.Fatalf("supersede = %d %v", status, out)
	}
	status, out = e.do("GET", "/api/decisions?status=superseded", nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) != 1 {
		t.Fatalf("superseded decisions = %d, want 1", len(items))
	}

	// 任务详情包含 criteria/deps/events
	status, out = e.do("GET", "/api/tasks/"+taskID, nil)
	d = e.mustOK(status, out)
	if len(d["criteria"].([]any)) != 2 || len(d["events"].([]any)) == 0 {
		t.Fatalf("task detail = %v", d)
	}
}

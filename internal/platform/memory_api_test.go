package platform_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/memory"
	"github.com/lignting/lignting-agents/internal/store"
)

func TestMemoryInspectorAPIAndContextSnapshots(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()
	svc, err := memory.NewService(memory.Options{Repo: memory.NewStoreRepo(e.st), Config: memory.Config{Enabled: true}})
	if err != nil {
		t.Fatalf("memory service: %v", err)
	}
	e.srv.SetMemoryService(svc)
	dept, err := e.st.CreateDepartment(ctx, domain.Department{Name: "研发部", Kind: "engineering"})
	if err != nil {
		t.Fatalf("dept: %v", err)
	}

	// 提交候选 → 列表可见 → 批准
	status, out := e.doCSRF("POST", "/api/memories", map[string]any{
		"scope": "department", "department_id": dept.ID, "agent_id": e.assistantID,
		"entity_type": "preference", "entity_key": "testing", "content": "偏好：先写测试再实现",
		"summary": "先测试", "tags": []string{"preference"}, "confidence": "high",
		"visibility": "team", "reusable": true, "decision_relevant": true,
		"evidence": []map[string]any{{"kind": "message", "ref": "msg_1"}},
	})
	if status != http.StatusOK {
		t.Fatalf("submit candidate = %d %v", status, out)
	}
	cand := e.data(out)
	created, _ := cand["Memory"].(map[string]any)
	if created == nil {
		// JSON 字段是 "memory"（Go 字段名序列化前有 json tag？SubmitResult 无 tag → Memory）
		if m, ok := cand["memory"].(map[string]any); ok {
			created = m
		}
	}
	memID, _ := created["id"].(string)
	if memID == "" {
		t.Fatalf("candidate result = %v", cand)
	}
	status, out = e.do("GET", "/api/memories", nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) == 0 {
		t.Fatalf("memory list empty: %v", out)
	}
	status, out = e.doCSRF("POST", "/api/memories/"+memID+"/approve", nil)
	if status != http.StatusOK {
		t.Fatalf("approve = %d %v", status, out)
	}
	// 编辑（乐观锁）
	status, out = e.doCSRF("PATCH", "/api/memories/"+memID, map[string]any{"summary": "先测试后实现（修订）", "expected_version": 1})
	if status != http.StatusOK {
		t.Fatalf("patch = %d %v", status, out)
	}
	// 错误版本 → 409
	if status, _ := e.doCSRF("PATCH", "/api/memories/"+memID, map[string]any{"summary": "x", "expected_version": 1}); status != http.StatusConflict {
		t.Fatalf("stale version status = %d, want 409", status)
	}

	// human-only 记忆：人类可见，Agent Token 不可见
	status, out = e.doCSRF("POST", "/api/memories", map[string]any{
		"direct": true, "scope": "assistant", "content": "人类专属评价：表达清晰",
		"visibility": "human-only", "entity_type": "person", "entity_key": "owner", "reusable": true,
	})
	if status != http.StatusOK {
		t.Fatalf("direct human-only = %d %v", status, out)
	}
	humanOnlyID, _ := e.data(out)["id"].(string)
	plain, hash, err := store.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	emp, err := e.st.CreateAgent(ctx, domain.Agent{Name: "员工", Kind: domain.AgentKindEmployee, DepartmentID: dept.ID, TokenHash: hash})
	if err != nil {
		t.Fatalf("agent: %v", err)
	}
	_ = emp
	status, out = e.do("GET", "/api/memories", nil, func(r *http.Request) { r.Header.Set("X-Lignting-Agent-Token", plain) })
	agentItems, _ := e.mustOK(status, out)["items"].([]any)
	for _, it := range agentItems {
		if it.(map[string]any)["id"] == humanOnlyID {
			t.Fatalf("agent should not see human-only memory %s", humanOnlyID)
		}
	}
	// 人类仍可读 human-only
	status, out = e.do("GET", "/api/memories?scope=assistant", nil)
	foundHumanOnly := false
	for _, it := range e.mustOK(status, out)["items"].([]any) {
		if it.(map[string]any)["id"] == humanOnlyID {
			foundHumanOnly = true
		}
	}
	if !foundHumanOnly {
		t.Fatal("human should see human-only memory")
	}

	// 手册：propose → list
	status, out = e.doCSRF("POST", "/api/manuals", map[string]any{
		"scope": "department", "department_id": dept.ID, "content": "接口评审先看兼容性", "sources": []string{"mem_1"},
	})
	if status != http.StatusOK {
		t.Fatalf("propose manual = %d %v", status, out)
	}
	status, out = e.do("GET", "/api/manuals?scope=department&department_id="+dept.ID, nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) != 1 {
		t.Fatalf("manual list = %v", out)
	}

	// 上下文快照 + turn 回放
	payload := `{"memories":[{"id":"mem_1"}],"token_estimate":123}`
	if err := e.st.InsertContextSnapshot(ctx, domain.ContextSnapshot{TurnID: "turn_x", AgentID: e.assistantID, ConversationID: e.dmID, Payload: payload}); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := e.st.CreateTurn(ctx, domain.Turn{ID: "turn_x", AgentID: e.assistantID, ConversationID: e.dmID}); err != nil {
		t.Fatalf("turn: %v", err)
	}
	status, out = e.do("GET", "/api/context-snapshots?turn_id=turn_x", nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) != 1 {
		t.Fatalf("snapshots = %v", out)
	}
	status, out = e.do("GET", "/api/turns/turn_x", nil)
	d := e.mustOK(status, out)
	if d["turn"].(map[string]any)["id"] != "turn_x" || len(d["snapshots"].([]any)) != 1 {
		t.Fatalf("turn replay = %v", d)
	}

	// 删除（墓碑）后列表不再返回
	status, out = e.doCSRF("DELETE", "/api/memories/"+memID, map[string]any{"reason": "过时"})
	if status != http.StatusOK {
		t.Fatalf("delete = %d %v", status, out)
	}
	status, out = e.do("GET", "/api/memories", nil)
	items, _ := e.mustOK(status, out)["items"].([]any)
	for _, it := range items {
		if it.(map[string]any)["id"] == memID {
			t.Fatalf("deleted memory still listed")
		}
	}
}

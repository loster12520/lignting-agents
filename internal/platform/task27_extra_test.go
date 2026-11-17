package platform_test

import (
	"net/http"
	"testing"

	"github.com/lignting/lignting-agents/internal/core"
)

func TestDepartmentCreateAgentPatchAndProvisionIdempotent(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	e.srv.SetOrgService(core.NewOrgService(e.st, nil))

	// 无 G1 批准蓝图 → 不能创建部门
	if status, out := e.doCSRF("POST", "/api/departments", map[string]any{"name": "手动部门", "kind": "manual"}); status != http.StatusForbidden {
		t.Fatalf("create dept without G1 = %d %v", status, out)
	}

	if status, out := e.doCSRF("POST", "/api/intents", map[string]any{"goal": "做知识库"}); status != http.StatusOK {
		t.Fatalf("intent = %d %v", status, out)
	}
	status, out := e.doCSRF("POST", "/api/blueprints", map[string]any{"option": "lean"})
	d := e.mustOK(status, out)
	bpID := d["blueprint"].(map[string]any)["id"].(string)
	e.mustOK(e.doCSRF("POST", "/api/blueprints/"+bpID+"/approve", nil))

	// 重复 provisioning 幂等：同一 prov id / verified
	status, out = e.doCSRF("POST", "/api/provisioning", map[string]any{"blueprint_id": bpID})
	p1 := e.mustOK(status, out)["provisioning"].(map[string]any)
	status, out = e.doCSRF("POST", "/api/provisioning", map[string]any{"blueprint_id": bpID})
	p2 := e.mustOK(status, out)["provisioning"].(map[string]any)
	if p1["id"] != p2["id"] || p2["status"] != "verified" {
		t.Fatalf("provisioning not idempotent: %v vs %v", p1, p2)
	}

	// G1 批准后可手动建部门
	status, out = e.doCSRF("POST", "/api/departments", map[string]any{"name": "手动部门", "kind": "manual", "blueprint_id": bpID})
	dept := e.mustOK(status, out)
	if dept["id"] == "" || dept["status"] != "active" {
		t.Fatalf("manual dept = %v", dept)
	}

	// Agent 低风险字段立即生效
	status, out = e.doCSRF("PATCH", "/api/agents/"+e.assistantID, map[string]any{"name": "小闪"})
	d = e.mustOK(status, out)
	if d["applied"] != true || d["agent"].(map[string]any)["name"] != "小闪" {
		t.Fatalf("patch agent low risk = %v", d)
	}
	// 高风险（模型/预算）→ 创建审批，不直接改
	status, out = e.doCSRF("PATCH", "/api/agents/"+e.assistantID, map[string]any{"model": "deepseek-flash"})
	d = e.mustOK(status, out)
	if d["applied"] != false || d["approval"].(map[string]any)["status"] != "pending" {
		t.Fatalf("patch agent high risk = %v", d)
	}
}

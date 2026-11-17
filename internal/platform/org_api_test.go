package platform_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lignting/lignting-agents/internal/core"
)

// R3 §7 全链路：意图 → 蓝图 + G1 → 未批准 Provision 403 → 批准 → provisioning → 生命周期。
func TestOrgBlueprintProvisionAndLifecycle(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	e.srv.SetOrgService(core.NewOrgService(e.st, nil))

	status, out := e.doCSRF("POST", "/api/intents", map[string]any{"content": map[string]any{"goal": "做一个本地知识库"}})
	if status != http.StatusOK {
		t.Fatalf("save intent = %d %v", status, out)
	}
	status, out = e.do("GET", "/api/intents/latest", nil)
	if d := e.mustOK(status, out); d["content"] == "" {
		t.Fatalf("latest intent = %v", d)
	}

	status, out = e.doCSRF("POST", "/api/blueprints", map[string]any{"option": "lean"})
	d := e.mustOK(status, out)
	bp := d["blueprint"].(map[string]any)
	appr := d["approval"].(map[string]any)
	bpID, _ := bp["id"].(string)
	if bp["status"] != "proposed" || appr["kind"] != "G1" {
		t.Fatalf("blueprint/approval = %v / %v", bp, appr)
	}
	if appr["id"] != "appr_"+bpID {
		t.Fatalf("G1 approval id = %v, want appr_%s", appr["id"], bpID)
	}
	status, out = e.do("GET", "/api/blueprints/"+bpID, nil)
	if e.mustOK(status, out)["status"] != "proposed" {
		t.Fatalf("get blueprint = %v", out)
	}

	// 未 G1 批准不能 provisioning
	if status, out := e.doCSRF("POST", "/api/provisioning", map[string]any{"blueprint_id": bpID}); status != http.StatusForbidden {
		t.Fatalf("unapproved provision status = %d, want 403; body=%v", status, out)
	}

	status, out = e.doCSRF("POST", "/api/blueprints/"+bpID+"/approve", map[string]any{"reason": "ok"})
	if e.mustOK(status, out)["status"] != "approved" {
		t.Fatalf("approve = %v", out)
	}
	status, out = e.doCSRF("POST", "/api/provisioning", map[string]any{"blueprint_id": bpID})
	d = e.mustOK(status, out)
	prov := d["provisioning"].(map[string]any)
	if prov["status"] != "verified" {
		t.Fatalf("provisioning = %v", prov)
	}
	provID, _ := prov["id"].(string)
	status, out = e.do("GET", "/api/provisioning/"+provID, nil)
	if e.mustOK(status, out)["status"] != "verified" {
		t.Fatalf("get provisioning = %v", out)
	}

	// 组织图已有 3 个部门
	status, out = e.do("GET", "/api/departments", nil)
	depts, _ := e.mustOK(status, out)["items"].([]any)
	if len(depts) != 3 {
		t.Fatalf("departments = %d, want 3", len(depts))
	}
	deptID := depts[0].(map[string]any)["id"].(string)
	status, out = e.doCSRF("POST", "/api/departments/"+deptID+"/pause", map[string]any{"reason": "测试"})
	if e.mustOK(status, out)["status"] != "paused" {
		t.Fatalf("pause dept = %v", out)
	}
	status, out = e.doCSRF("POST", "/api/departments/"+deptID+"/resume", nil)
	if e.mustOK(status, out)["status"] != "active" {
		t.Fatalf("resume dept = %v", out)
	}

	// Agent 生命周期
	status, out = e.doCSRF("POST", "/api/agents/"+e.assistantID+"/pause", map[string]any{"reason": "测试"})
	if e.mustOK(status, out)["status"] != "paused" {
		t.Fatalf("pause agent = %v", out)
	}
	status, out = e.doCSRF("POST", "/api/agents/"+e.assistantID+"/resume", nil)
	if e.mustOK(status, out)["status"] != "idle" {
		t.Fatalf("resume agent = %v", out)
	}

	// G1 拒绝路径
	status, out = e.doCSRF("POST", "/api/blueprints", map[string]any{"option": "full"})
	d = e.mustOK(status, out)
	bp2ID := d["blueprint"].(map[string]any)["id"].(string)
	status, out = e.doCSRF("POST", "/api/blueprints/"+bp2ID+"/reject", map[string]any{"reason": "暂不做"})
	if e.mustOK(status, out)["status"] != "rejected" {
		t.Fatalf("reject = %v", out)
	}
	if status, _ := e.doCSRF("POST", "/api/provisioning", map[string]any{"blueprint_id": bp2ID}); status != http.StatusForbidden {
		t.Fatalf("rejected blueprint provision status = %d, want 403", status)
	}
}

// 未注入 OrgService 时 §7 返回 503。
func TestOrgRoutesUnavailableWithoutService(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	status, out := e.doCSRF("POST", "/api/blueprints", map[string]any{"option": "lean"})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("blueprints without org service = %d %v", status, out)
	}
	_ = context.Background()
}

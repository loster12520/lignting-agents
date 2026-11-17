//go:build acceptance

package ac

import (
	"context"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/store"
)

// TestAC02DepartmentProvisioning covers requirements.md §14 AC-02 (R3 scope):
// intent -> blueprint + G1 -> provisioning -> 3 departments/agents/dept chats ->
// idempotent retry -> pause/resume -> audit chain.
// Not yet covered: provisioning failure/rollback fault injection (R3 fault
// switch not wired) and T2 wall-clock timing (manual release evidence).
func TestAC02DepartmentProvisioning(t *testing.T) {
	started := time.Now()
	h := NewHarness(t, WithCoreServices())
	h.Setup("阿哲")

	// 1) Intent card -> blueprint + G1. The G1 approval is created with the
	// blueprint and is required before provisioning.
	var intent struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	h.PostData(t, "/api/intents", map[string]any{
		"content": map[string]any{
			"goal":       "把 CLI 工具做成 Web 产品",
			"must_have":  []string{"登录", "项目列表"},
			"non_goals":  []string{"移动端"},
			"budget_cny": 20,
		},
	}, &intent)
	if intent.ID == "" || intent.Version < 1 {
		t.Fatalf("intent = %+v", intent)
	}

	var proposed struct {
		Blueprint struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Content string `json:"content"`
		} `json:"blueprint"`
		Approval struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Status string `json:"status"`
		} `json:"approval"`
	}
	h.PostData(t, "/api/blueprints", map[string]any{
		"intent_id": intent.ID, "option": "lean",
	}, &proposed)
	if proposed.Blueprint.ID == "" {
		t.Fatalf("blueprint = %+v", proposed.Blueprint)
	}
	if proposed.Blueprint.Status != "proposed" {
		t.Fatalf("blueprint status = %q, want proposed", proposed.Blueprint.Status)
	}
	if proposed.Approval.Kind != "G1" || proposed.Approval.Status != "pending" {
		t.Fatalf("G1 approval = %+v", proposed.Approval)
	}
	if proposed.Approval.ID != "appr_"+proposed.Blueprint.ID {
		t.Fatalf("G1 approval id = %q, want appr_%s", proposed.Approval.ID, proposed.Blueprint.ID)
	}
	// Blueprint content must carry both options (lean/full) and chosen option.
	var bpContent map[string]any
	if err := jsonUnmarshal([]byte(proposed.Blueprint.Content), &bpContent); err != nil {
		t.Fatalf("blueprint content: %v", err)
	}
	if opts, _ := bpContent["options"].([]any); len(opts) < 2 {
		t.Fatalf("blueprint options = %v, want >=2", bpContent["options"])
	}
	if bpContent["chosen_option"] != "lean" {
		t.Fatalf("chosen_option = %v, want lean", bpContent["chosen_option"])
	}

	// 2) Unapproved blueprint cannot provision (human gate G1).
	h.ExpectStatus(t, "POST", "/api/provisioning", map[string]any{"blueprint_id": proposed.Blueprint.ID}, 403)

	// 3) G1 approve -> provisioning.
	var approved struct {
		Status string `json:"status"`
	}
	h.PostData(t, "/api/blueprints/"+proposed.Blueprint.ID+"/approve", map[string]any{"reason": "批准精简版"}, &approved)
	if approved.Status != "approved" {
		t.Fatalf("approved blueprint status = %q", approved.Status)
	}

	type provisionResp struct {
		Provisioning struct {
			ID          string `json:"id"`
			BlueprintID string `json:"blueprint_id"`
			Status      string `json:"status"`
			Steps       string `json:"steps"`
		} `json:"provisioning"`
		Orphans struct {
			Departments   []string `json:"departments"`
			Agents        []string `json:"agents"`
			Conversations []string `json:"conversations"`
			Errors        []string `json:"errors"`
		} `json:"orphans"`
	}
	var prov provisionResp
	h.PostData(t, "/api/provisioning", map[string]any{"blueprint_id": proposed.Blueprint.ID}, &prov)
	if prov.Provisioning.Status != "verified" {
		t.Fatalf("provisioning status = %q, want verified", prov.Provisioning.Status)
	}
	if prov.Provisioning.BlueprintID != proposed.Blueprint.ID {
		t.Fatalf("provisioning blueprint = %q", prov.Provisioning.BlueprintID)
	}
	if len(prov.Orphans.Departments)+len(prov.Orphans.Agents)+len(prov.Orphans.Conversations)+len(prov.Orphans.Errors) != 0 {
		t.Fatalf("unexpected orphans: %+v", prov.Orphans)
	}

	// 4) 3 departments (product/engineering/qa), each with supervisor + employees
	// and a department group chat whose owner is an observer.
	var depts struct {
		Items []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			Status  string `json:"status"`
			Charter string `json:"charter"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/departments", &depts)
	if len(depts.Items) != 3 || depts.Total != 3 {
		t.Fatalf("departments = %d/%d, want 3", len(depts.Items), depts.Total)
	}
	kinds := map[string]bool{}
	for _, d := range depts.Items {
		kinds[d.Kind] = true
		if d.Status != "active" || d.Charter == "" {
			t.Errorf("department = %+v, want active + charter", d)
		}
	}
	for _, kind := range []string{"product", "engineering", "qa"} {
		if !kinds[kind] {
			t.Errorf("missing department kind %q", kind)
		}
	}

	var agents struct {
		Items []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Kind         string `json:"kind"`
			DepartmentID string `json:"department_id"`
			Perspective  string `json:"perspective"`
			Status       string `json:"status"`
		} `json:"items"`
	}
	h.GetData(t, "/api/agents", &agents)
	if len(agents.Items) < 7 { // assistant + 3 supervisors + 3 employees (lean)
		t.Fatalf("agents = %d, want >=7", len(agents.Items))
	}
	byDept := map[string][]string{}
	supervisors := 0
	for _, a := range agents.Items {
		if a.Kind == "assistant" {
			continue
		}
		byDept[a.DepartmentID] = append(byDept[a.DepartmentID], a.ID)
		if a.Kind == "supervisor" {
			supervisors++
			if a.Perspective == "" {
				// supervisor role cards may leave perspective empty; employees must have one.
				continue
			}
		}
		if a.Kind == "employee" && a.Perspective == "" {
			t.Errorf("employee %s has empty perspective (分工视角)", a.ID)
		}
	}
	if supervisors != 3 {
		t.Fatalf("supervisors = %d, want 3", supervisors)
	}

	var convs struct {
		Items []struct {
			ID           string `json:"id"`
			Type         string `json:"type"`
			DepartmentID string `json:"department_id"`
			Members      []struct {
				PrincipalType string `json:"principal_type"`
				PrincipalID   string `json:"principal_id"`
				Role          string `json:"role"`
			} `json:"members"`
		} `json:"items"`
	}
	h.GetData(t, "/api/conversations?type=department", &convs)
	if len(convs.Items) != 3 {
		t.Fatalf("department conversations = %d, want 3", len(convs.Items))
	}
	for _, c := range convs.Items {
		if len(c.Members) < 2 {
			t.Errorf("department conversation %s members = %d", c.ID, len(c.Members))
		}
		ownerObserver := false
		for _, m := range c.Members {
			if m.PrincipalType == "user" && m.PrincipalID == "owner" && m.Role == "observer" {
				ownerObserver = true
			}
		}
		if !ownerObserver {
			t.Errorf("department conversation %s: owner not observer", c.ID)
		}
	}

	// Provisioning status endpoint is readable by id.
	var provStatus struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	h.GetData(t, "/api/provisioning/"+prov.Provisioning.ID, &provStatus)
	if provStatus.Status != "verified" {
		t.Fatalf("GET provisioning status = %q", provStatus.Status)
	}

	// 5) Repeat provisioning is idempotent: same object, no duplicate resources.
	var provAgain provisionResp
	h.PostData(t, "/api/provisioning", map[string]any{"blueprint_id": proposed.Blueprint.ID}, &provAgain)
	if provAgain.Provisioning.ID != prov.Provisioning.ID || provAgain.Provisioning.Status != "verified" {
		t.Fatalf("repeat provisioning = %+v, want same verified id %s", provAgain.Provisioning, prov.Provisioning.ID)
	}
	var deptsAgain struct {
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/departments", &deptsAgain)
	if deptsAgain.Total != 3 {
		t.Fatalf("departments after repeat = %d, want 3", deptsAgain.Total)
	}
	var agentsAgain struct {
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/agents", &agentsAgain)
	if agentsAgain.Total != int64(len(agents.Items)) {
		t.Fatalf("agents after repeat = %d, want %d", agentsAgain.Total, len(agents.Items))
	}

	// 6) Department pause pauses its agents; resume restores them.
	engID := ""
	for _, d := range depts.Items {
		if d.Kind == "engineering" {
			engID = d.ID
		}
	}
	if engID == "" {
		t.Fatal("engineering department not found")
	}
	var paused struct {
		Status string `json:"status"`
	}
	h.PostData(t, "/api/departments/"+engID+"/pause", map[string]any{"reason": "验收暂停"}, &paused)
	if paused.Status != "paused" {
		t.Fatalf("paused department status = %q", paused.Status)
	}
	var deptAgents struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	h.GetData(t, "/api/agents?department_id="+engID, &deptAgents)
	if len(deptAgents.Items) == 0 {
		t.Fatal("engineering department has no agents")
	}
	for _, a := range deptAgents.Items {
		if a.Status != "paused" {
			t.Errorf("agent %s status = %q after department pause, want paused", a.ID, a.Status)
		}
	}
	var resumed struct {
		Status string `json:"status"`
	}
	h.PostData(t, "/api/departments/"+engID+"/resume", map[string]any{}, &resumed)
	if resumed.Status != "active" {
		t.Fatalf("resumed department status = %q", resumed.Status)
	}
	h.GetData(t, "/api/agents?department_id="+engID, &deptAgents)
	for _, a := range deptAgents.Items {
		if a.Status != "idle" {
			t.Errorf("agent %s status = %q after department resume, want idle", a.ID, a.Status)
		}
	}

	// 7) Agent lifecycle (assistant pause/resume).
	assistantID := ""
	for _, a := range agents.Items {
		if a.Kind == "assistant" {
			assistantID = a.ID
		}
	}
	if assistantID == "" {
		t.Fatal("assistant not found")
	}
	var agentState struct {
		Status string `json:"status"`
	}
	h.PostData(t, "/api/agents/"+assistantID+"/pause", map[string]any{"reason": "验收"}, &agentState)
	if agentState.Status != "paused" {
		t.Fatalf("assistant pause status = %q", agentState.Status)
	}
	h.PostData(t, "/api/agents/"+assistantID+"/resume", map[string]any{}, &agentState)
	if agentState.Status != "idle" {
		t.Fatalf("assistant resume status = %q", agentState.Status)
	}

	// 8) Audit chain contains the provisioning/G1/lifecycle actions.
	audits, err := h.Store.QueryAudit(context.Background(), store.AuditFilter{Limit: 200})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]bool{}
	for _, a := range audits {
		actions[a.Action] = true
	}
	for _, want := range []string{
		"intent.save", "blueprint.propose", "blueprint.approve", "org.provision",
		"org.department.pause", "org.department.resume", "org.agent.pause", "org.agent.resume",
	} {
		if !actions[want] {
			t.Errorf("audit missing action %q", want)
		}
	}
	if _, err := h.Store.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	t.Logf("T2 (setup -> G1 -> provisioning -> first task-ready window is manual) in-process elapsed: %s", time.Since(started))
	t.Log("AC-02 R3 passed: G1 gate, 3 departments/agents/dept chats, idempotent retry, pause/resume, audit chain")
}

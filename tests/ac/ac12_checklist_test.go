//go:build acceptance

package ac

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/tests/fixtures"
)

// TestAC12SupervisorChecklistAndSampling covers the R4-available part of AC-12:
// task -> per-criterion evidence/verdict -> G3 blocked while a criterion is
// missing -> complete -> G3 card -> human approve -> done.
// Still R8: human sampling of verdicts / abnormal approve-rate hint (FR-REV-05).
func TestAC12SupervisorChecklistAndSampling(t *testing.T) {
	h := NewHarness(t, WithSeed(), WithCoreServices())
	h.EnsureSession()

	// Create a high-risk task in the seeded engineering department. High risk
	// makes "done" require an approved G3.
	var created struct {
		Task struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"task"`
		Criteria []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"criteria"`
		Ready bool `json:"ready"`
	}
	h.PostData(t, "/api/tasks", map[string]any{
		"title":         "AC-12 导出 CSV 验收",
		"description":   "核对表阻止缺证据的 G3",
		"department_id": fixtures.DeptDevID,
		"owner_id":      fixtures.AgentDevAID,
		"reviewer_id":   fixtures.AgentTestEmpID,
		"approver_id":   fixtures.AgentDevLeadID,
		"perspective":   "接口",
		"priority":      "P1",
		"deadline":      "2026-12-31T00:00:00Z",
		"budget_tokens": 1000,
		"budget_cost":   2.5,
		"high_risk":     true,
		"acceptance":    []string{"字段完整", "10 万行 <5s", "错误信息可读"},
	}, &created)
	taskID := created.Task.ID
	if taskID == "" || len(created.Criteria) != 3 {
		t.Fatalf("created task = %+v", created)
	}
	if created.Task.Status != "proposed" || !created.Ready {
		t.Fatalf("task status=%q ready=%v, want proposed/ready", created.Task.Status, created.Ready)
	}
	criteria := created.Criteria

	// proposed -> ready -> discussing are allowed before the gate, but a
	// high-risk task must not enter in_progress until its G2 is approved
	// (FR-APPR-01, task-42). The 403 also proves the pending G2 exists.
	for _, to := range []string{"ready", "discussing"} {
		var ignored map[string]any
		h.PostData(t, "/api/tasks/"+taskID+"/transition", map[string]any{"to": to, "reason": "验收推进"}, &ignored)
	}
	g2Gate := h.ExpectStatus(t, "POST", "/api/tasks/"+taskID+"/transition", map[string]any{
		"to": "in_progress", "reason": "未批准 G2 开工",
	}, 403)
	if body := string(g2Gate.Body); !strings.Contains(body, "G2") {
		t.Fatalf("G2 gate body = %s, want pending G2 reference", body)
	}

	// Approve the auto-created G2 as the human owner, then re-enter in_progress.
	var approvals struct {
		Items []struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Status  string `json:"status"`
			Payload string `json:"payload"`
		} `json:"items"`
	}
	h.GetData(t, "/api/approvals?kind=G2&status=pending", &approvals)
	var g2ID string
	for _, a := range approvals.Items {
		var payload struct {
			TaskID string `json:"task_id"`
		}
		if jsonUnmarshal([]byte(a.Payload), &payload) == nil && payload.TaskID == taskID {
			g2ID = a.ID
			break
		}
	}
	if g2ID == "" {
		t.Fatalf("pending G2 for %s not found: %+v", taskID, approvals.Items)
	}
	var g2Decided struct {
		Status string `json:"status"`
	}
	h.PostData(t, "/api/approvals/"+g2ID+"/decide", map[string]any{
		"decision": "approved", "reason": "技术方案/范围已确认",
	}, &g2Decided)
	if g2Decided.Status != "approved" {
		t.Fatalf("G2 decide status = %q, want approved", g2Decided.Status)
	}
	var started map[string]any
	h.PostData(t, "/api/tasks/"+taskID+"/transition", map[string]any{"to": "in_progress", "reason": "G2 已批准"}, &started)

	// Supervisor checklist: crit1 evidence + approved; crit2 missing; crit3
	// initially human_needed.
	if err := h.postEvidence(taskID, criteria[0].ID, fixtures.AgentDevAID, `["command:go test ./internal/core -run TestExport"]`); err != nil {
		t.Fatalf("evidence crit1: %v", err)
	}
	h.postVerdict(t, taskID, criteria[0].ID, fixtures.AgentDevLeadID, "approved")
	if err := h.postEvidence(taskID, criteria[2].ID, fixtures.AgentDevAID, `["message:msg_task_comment"]`); err != nil {
		t.Fatalf("evidence crit3: %v", err)
	}
	h.postVerdict(t, taskID, criteria[2].ID, fixtures.AgentDevLeadID, "human_needed")

	// G3 must be rejected while crit2 has no evidence and crit3 needs a human.
	// Fixtures already seed one pending G3, so compare before/after counts.
	var g3Before struct {
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/approvals?kind=G3", &g3Before)
	rejected := h.ExpectStatus(t, "POST", "/api/tasks/"+taskID+"/g3", map[string]any{
		"actor_agent_id": fixtures.AgentDevLeadID, "risk": "high", "residual_risks": "",
	}, 400)
	if body := string(rejected.Body); !strings.Contains(body, "G3") && !strings.Contains(body, "未满足") {
		t.Fatalf("G3 rejection body = %s, want checklist blockers", body)
	}
	var g3After struct {
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/approvals?kind=G3", &g3After)
	if g3After.Total != g3Before.Total {
		t.Fatalf("G3 approval created despite missing evidence: before=%d after=%d", g3Before.Total, g3After.Total)
	}

	// Complete the checklist: evidence + approved for crit2, and revise crit3
	// from human_needed to approved after the human reviewed the evidence.
	if err := h.postEvidence(taskID, criteria[1].ID, fixtures.AgentDevAID, `["command:go test ./internal/core -run TestExportLarge"]`); err != nil {
		t.Fatalf("evidence crit2: %v", err)
	}
	h.postVerdict(t, taskID, criteria[1].ID, fixtures.AgentDevLeadID, "approved")
	h.postVerdict(t, taskID, criteria[2].ID, fixtures.AgentDevLeadID, "approved")

	// G3 card: checklist, cost, risk, artifacts must be present and ready.
	var g3 struct {
		Approval struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Status  string `json:"status"`
			Payload string `json:"payload"`
			Risk    string `json:"risk"`
		} `json:"approval"`
		Status string `json:"status"`
	}
	h.PostData(t, "/api/tasks/"+taskID+"/g3", map[string]any{
		"actor_agent_id": fixtures.AgentDevLeadID, "risk": "high", "residual_risks": "文档待补",
	}, &g3)
	if g3.Approval.Kind != "G3" || g3.Approval.Status != "pending" || g3.Status != "pending" {
		t.Fatalf("G3 = %+v", g3)
	}
	var card struct {
		TaskID    string `json:"task_id"`
		Checklist []struct {
			ID                string `json:"id"`
			Text              string `json:"text"`
			Evidence          string `json:"evidence"`
			SupervisorVerdict string `json:"supervisor_verdict"`
		} `json:"checklist"`
		Cost          float64  `json:"cost"`
		Risk          string   `json:"risk"`
		Artifacts     []any    `json:"artifacts"`
		ResidualRisks []string `json:"residual_risks"`
		Ready         bool     `json:"ready"`
	}
	if err := jsonUnmarshal([]byte(g3.Approval.Payload), &card); err != nil {
		t.Fatalf("G3 payload: %v", err)
	}
	if card.TaskID != taskID || len(card.Checklist) != 3 || !card.Ready {
		t.Fatalf("G3 card = %+v", card)
	}
	if card.Cost != 2.5 || card.Risk != "high" {
		t.Fatalf("G3 cost/risk = %v/%q, want 2.5/high", card.Cost, card.Risk)
	}
	for _, c := range card.Checklist {
		if c.Evidence == "" || c.SupervisorVerdict != "approved" {
			t.Errorf("checklist item not complete: %+v", c)
		}
	}

	// Human approves G3; then review transitions carry the task to done.
	var decided struct {
		Status string `json:"status"`
	}
	h.PostData(t, "/api/approvals/"+g3.Approval.ID+"/decide", map[string]any{
		"decision": "approved", "reason": "核对表完整，风险接受",
	}, &decided)
	if decided.Status != "approved" {
		t.Fatalf("G3 decide status = %q", decided.Status)
	}
	// Submit for review, then prove the author cannot approve their own task.
	var ignored map[string]any
	h.PostData(t, "/api/tasks/"+taskID+"/transition", map[string]any{
		"to": "in_review", "actor_agent_id": fixtures.AgentDevAID, "reason": "提交评审",
	}, &ignored)
	h.ExpectStatus(t, "POST", "/api/tasks/"+taskID+"/transition", map[string]any{
		"to": "approved", "actor_agent_id": fixtures.AgentDevAID, "reason": "自批",
	}, 403)
	// Reviewer approves, then the approver/supervisor closes after G3 approval.
	h.PostData(t, "/api/tasks/"+taskID+"/transition", map[string]any{
		"to": "approved", "actor_agent_id": fixtures.AgentTestEmpID, "reason": "评审通过",
	}, &ignored)
	h.PostData(t, "/api/tasks/"+taskID+"/transition", map[string]any{
		"to": "done", "actor_agent_id": fixtures.AgentDevLeadID, "reason": "G3 通过",
	}, &ignored)

	// Final task view: done, checklist persisted, events append-only.
	var detail struct {
		Task struct {
			Status string `json:"status"`
		} `json:"task"`
		Criteria []struct {
			Evidence          string `json:"evidence"`
			SupervisorVerdict string `json:"supervisor_verdict"`
		} `json:"criteria"`
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	h.GetData(t, "/api/tasks/"+taskID, &detail)
	if detail.Task.Status != "done" {
		t.Fatalf("task status = %q, want done", detail.Task.Status)
	}
	if len(detail.Criteria) != 3 {
		t.Fatalf("criteria after done = %d", len(detail.Criteria))
	}
	for _, c := range detail.Criteria {
		if c.Evidence == "" || c.SupervisorVerdict != "approved" {
			t.Errorf("criterion lost checklist data: %+v", c)
		}
	}
	eventTypes := map[string]bool{}
	for _, ev := range detail.Events {
		eventTypes[ev.Type] = true
	}
	if !eventTypes["task.g3"] {
		t.Errorf("task events missing task.g3: %v", eventTypes)
	}

	// Audit: G3 request/decision + no unrecorded approval.
	audits, err := h.Store.QueryAudit(context.Background(), store.AuditFilter{Limit: 300})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]bool{}
	for _, a := range audits {
		actions[a.Action] = true
	}
	for _, want := range []string{"task.create", "approval.request", "approval.decide"} {
		if !actions[want] {
			t.Errorf("audit missing %q", want)
		}
	}
	if _, err := h.Store.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	t.Log("AC-12 R4 passed: checklist blocks G3, complete G3 card, human approve, self-approve rejected, done only after G3")
	t.Run("human_sampling_and_abnormal_approve_rate_FR_REV_05", func(t *testing.T) {
		SkipSkeleton(t, "AC-12 human sampling / abnormal approve-rate hint", "R8", "FR-REV-05 抽检 API 尚未实现")
	})
}

func (h *Harness) postEvidence(taskID, criterionID, actorID, evidence string) error {
	resp, err := h.Client.PostJSON("/api/tasks/"+taskID+"/criteria/"+criterionID+"/evidence", map[string]any{
		"actor_agent_id": actorID, "evidence": evidence,
	})
	if err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("status %d: %s", resp.Status, string(resp.Body))
	}
	return nil
}

func (h *Harness) postVerdict(t *testing.T, taskID, criterionID, actorID, verdict string) {
	t.Helper()
	var ignored map[string]any
	h.PostData(t, "/api/tasks/"+taskID+"/criteria/"+criterionID+"/verdict", map[string]any{
		"actor_agent_id": actorID, "verdict": verdict,
	}, &ignored)
}

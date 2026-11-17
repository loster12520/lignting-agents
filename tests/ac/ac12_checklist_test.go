//go:build acceptance

package ac

import (
	"context"
	"fmt"
	"net/http"
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
		ctx := context.Background()
		// Author dev A, reviewer test employee: create one artifact + five
		// approved verdicts so the reviewer×author pair hits 5/5 = 100%.
		var art struct {
			Artifact struct {
				ID string `json:"id"`
			} `json:"artifact"`
			Version struct {
				ID string `json:"id"`
			} `json:"version"`
		}
		h.PostData(t, "/api/artifacts", map[string]any{
			"type": "code", "title": "AC-12 抽检对象", "task_id": fixtures.TaskID,
			"department_id": fixtures.DeptDevID, "content": "package ac12\n", "actor_agent_id": fixtures.AgentDevAID,
		}, &art)
		if art.Artifact.ID == "" || art.Version.ID == "" {
			t.Fatalf("sampling artifact = %+v", art)
		}
		var reviewIDs []string
		for i := 0; i < 5; i++ {
			var review struct {
				ID string `json:"id"`
			}
			h.PostData(t, "/api/reviews", map[string]any{
				"artifact_id": art.Artifact.ID, "artifact_version_id": art.Version.ID,
				"severity": "minor", "stance": "抽检样本",
				"claim":    fmt.Sprintf("抽检样本 %d：实现与验收标准一致", i+1),
				"evidence": "command:go test ./...", "impact": "低", "proposed_resolution": "无需返工",
				"from_department": fixtures.DeptTestID, "to_department": fixtures.DeptDevID,
				"actor_agent_id": fixtures.AgentTestEmpID,
			}, &review)
			if review.ID == "" {
				t.Fatalf("review %d missing id", i)
			}
			var verdict struct {
				ID string `json:"id"`
			}
			h.PostData(t, "/api/reviews/"+review.ID+"/verdicts", map[string]any{
				"verdict": "approve", "checked": "逐条核对证据", "rationale": "通过",
				"conditions": "[]", "residual_risk": "低", "actor_agent_id": fixtures.AgentTestEmpID,
			}, &verdict)
			reviewIDs = append(reviewIDs, review.ID)
		}

		// 5 approvals / 100% -> a hint (not auto-collusion judgement).
		var quality struct {
			Items []struct {
				ReviewerID        string         `json:"reviewer_id"`
				AuthorID          string         `json:"author_id"`
				Total             int            `json:"total"`
				Approvals         int            `json:"approvals"`
				PassRate          float64        `json:"pass_rate"`
				SamplingSuggested bool           `json:"sampling_suggested"`
				Sampling          map[string]int `json:"sampling"`
			} `json:"items"`
		}
		h.GetData(t, "/api/quality/reviews", &quality)
		var pairFound bool
		for _, st := range quality.Items {
			if st.ReviewerID == fixtures.AgentTestEmpID && st.AuthorID == fixtures.AgentDevAID {
				pairFound = true
				if st.Total != 5 || st.Approvals != 5 || st.PassRate != 1 || !st.SamplingSuggested {
					t.Fatalf("quality pair = %+v, want 5/5 100%% suggested", st)
				}
				if len(st.Sampling) != 0 {
					t.Fatalf("sampling metrics before sampling = %+v", st.Sampling)
				}
			}
		}
		if !pairFound {
			t.Fatalf("quality pair %s|%s not found: %+v", fixtures.AgentTestEmpID, fixtures.AgentDevAID, quality.Items)
		}

		var suggestions struct {
			Items []struct {
				ReviewerID string `json:"reviewer_id"`
				AuthorID   string `json:"author_id"`
				Total      int    `json:"total"`
				Candidates []struct {
					Review struct {
						ID string `json:"id"`
					} `json:"review"`
					Verdict struct {
						Verdict string `json:"verdict"`
					} `json:"verdict"`
				} `json:"candidates"`
			} `json:"items"`
		}
		h.GetData(t, "/api/reviews/sampling/suggestions", &suggestions)
		if len(suggestions.Items) == 0 {
			t.Fatal("no sampling suggestions for 5x100%% pair")
		}
		sug := suggestions.Items[0]
		if sug.ReviewerID != fixtures.AgentTestEmpID || sug.AuthorID != fixtures.AgentDevAID || len(sug.Candidates) != 5 {
			t.Fatalf("sampling suggestion = %+v", sug)
		}
		for _, cand := range sug.Candidates {
			if cand.Review.ID == "" || cand.Verdict.Verdict != "approve" {
				t.Fatalf("sampling candidate = %+v", cand)
			}
		}

		// Human sampling with an invalid result: audited, redacted, does not
		// block the already-delivered task.
		const secret = "sk-ac12-sampling-secret-123456"
		var sampled struct {
			Result string `json:"result"`
		}
		h.PostData(t, "/api/reviews/"+reviewIDs[0]+"/sampling", map[string]any{
			"result": "invalid", "note": "证据不足 " + secret,
		}, &sampled)
		if sampled.Result != "invalid" {
			t.Fatalf("sampling result = %+v", sampled)
		}
		h.GetData(t, "/api/quality/reviews", &quality)
		for _, st := range quality.Items {
			if st.ReviewerID == fixtures.AgentTestEmpID && st.AuthorID == fixtures.AgentDevAID {
				if st.Sampling["invalid"] != 1 || st.Sampling["total"] != 1 || !st.SamplingSuggested {
					t.Fatalf("sampling metrics after invalid = %+v suggested=%v", st.Sampling, st.SamplingSuggested)
				}
			}
		}
		h.ExpectStatus(t, "POST", "/api/reviews/"+reviewIDs[1]+"/sampling", map[string]any{"result": "bogus"}, 400)
		h.ExpectStatus(t, "POST", "/api/reviews/rev_missing/sampling", map[string]any{"result": "valid"}, 404)
		var taskAfter struct {
			Task struct {
				Status string `json:"status"`
			} `json:"task"`
		}
		h.GetData(t, "/api/tasks/"+taskID, &taskAfter)
		if taskAfter.Task.Status != "done" {
			t.Fatalf("sampling blocked delivery: task status = %q, want done", taskAfter.Task.Status)
		}

		// Agent tokens cannot perform human sampling.
		var tokenOut struct {
			Token string `json:"token"`
		}
		h.PostData(t, "/api/agents/"+fixtures.AgentTestEmpID+"/token", map[string]any{}, &tokenOut)
		req, _ := http.NewRequest(http.MethodPost, h.BaseURL+"/api/reviews/"+reviewIDs[2]+"/sampling", strings.NewReader(`{"result":"valid"}`))
		req.Header.Set("Origin", h.BaseURL)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lignting-Agent-Token", tokenOut.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("agent sampling request: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("agent sampling = %d, want 403", resp.StatusCode)
		}

		// Audit contains the sampling once, with the secret redacted.
		audits, err := h.Store.QueryAudit(ctx, store.AuditFilter{Action: "review.sampling", Limit: 20})
		if err != nil || len(audits) != 1 {
			t.Fatalf("review.sampling audits = %d err=%v, want 1", len(audits), err)
		}
		if strings.Contains(audits[0].Detail, secret) {
			t.Fatal("sampling audit leaked the secret")
		}
		if _, err := h.Store.VerifyAuditChain(ctx); err != nil {
			t.Fatalf("audit chain: %v", err)
		}
		t.Log("AC-12 sampling passed: 5x100% hint, invalid metric, redacted audit, delivery not blocked")
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

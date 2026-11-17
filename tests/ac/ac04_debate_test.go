//go:build acceptance

package ac

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/tests/fixtures"
)

// TestAC04CrossDepartmentDebate covers requirements.md §14 AC-04 (R6):
// fixture-injected challenge -> department discussion -> cross-department
// stances R1/R2 -> limits -> escalate L2 -> close with dissent -> search/detail
// -> dissent supersede -> reopen at most once.
// Still not observable through HTTP: stall detection (service rejects a
// duplicate stance without new evidence before it can be recorded as stalled)
// and a wall-clock timebox assertion; both are logged as remaining R6 gaps.
func TestAC04CrossDepartmentDebate(t *testing.T) {
	h := NewHarness(t, WithSeed(), WithCoreServices())
	h.EnsureSession()
	convID := h.Seed.Conversations[fixtures.ConversationCrossID]
	if convID == "" {
		t.Fatal("cross-department conversation missing from fixture")
	}

	// 1) The seeded challenge ticket has severity/evidence/impact (FR-REV-01).
	var reviews struct {
		Items []struct {
			ID                string `json:"id"`
			Severity          string `json:"severity"`
			Claim             string `json:"claim"`
			Evidence          string `json:"evidence"`
			ArtifactVersionID string `json:"artifact_version_id"`
			OwnerID           string `json:"owner_id"`
			Status            string `json:"status"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/reviews?status=open", &reviews)
	if reviews.Total < 1 {
		t.Fatalf("open challenge tickets = %d, want >=1", reviews.Total)
	}
	challenge := reviews.Items[0]
	if challenge.Severity == "" || challenge.Claim == "" || challenge.Evidence == "" || challenge.ArtifactVersionID == "" {
		t.Fatalf("challenge ticket incomplete: %+v", challenge)
	}

	// 2) Department-internal discussion with perspective + stance.
	discuss := func(actorID, stance string) int {
		resp, err := h.Client.PostJSON("/api/conversations/"+convID+"/discussions", map[string]any{
			"actor_agent_id": actorID, "debate_id": "debate_ac04", "perspective": "接口",
			"stance": stance, "evidence": "e0",
		})
		if err != nil {
			t.Fatalf("discussion: %v", err)
		}
		return resp.Status
	}
	if got := discuss(fixtures.AgentDevAID, "接口越简单越好"); got != 200 {
		t.Fatalf("first discussion status = %d", got)
	}
	// Per-person limit: 5 submissions max, the 6th is rejected.
	for i := 0; i < 4; i++ {
		if got := discuss(fixtures.AgentDevAID, "补充视角"+string(rune('A'+i))); got != 200 {
			t.Fatalf("discussion %d status = %d", i, got)
		}
	}
	if got := discuss(fixtures.AgentDevAID, "第 6 条应被拒绝"); got != 409 {
		t.Fatalf("discussion over limit status = %d, want 409", got)
	}

	// 3) Cross-department stances: R1 both sides, R2 response; evidence_ids are
	// mandatory; duplicate stance without new evidence is rejected.
	stance := func(actorID, stanceText string, round int, evidence []string) (*Response, error) {
		body := map[string]any{
			"actor_agent_id": actorID, "debate_id": "debate_ac04", "round": round,
			"stance": stanceText,
		}
		if evidence != nil {
			body["evidence_ids"] = evidence
		}
		return h.Client.PostJSON("/api/conversations/"+convID+"/stances", body)
	}
	if resp, err := stance(fixtures.AgentDevLeadID, "用 REST + OpenAPI", 1, []string{"e1"}); err != nil || resp.Status != 200 {
		t.Fatalf("stance R1 dev = %v status=%v", err, statusOf(resp))
	}
	if resp, err := stance(fixtures.AgentTestLeadID, "需要契约测试证据", 2, []string{"e2"}); err != nil || resp.Status != 200 {
		t.Fatalf("stance R2 test = %v status=%v", err, statusOf(resp))
	}
	if resp, _ := stance(fixtures.AgentProdLeadID, "无证据立场", 1, nil); resp.Status != 400 {
		t.Fatalf("stance without evidence status = %d, want 400", resp.Status)
	}
	if resp, _ := stance(fixtures.AgentTestLeadID, "需要契约测试证据", 2, []string{"e2"}); resp.Status != 409 {
		t.Fatalf("duplicate stance status = %d, want 409", resp.Status)
	}

	// 4) Escalate L2 with options/recommendation/consequence (decision proposed).
	var escalation struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Options  string `json:"options"`
		Evidence string `json:"evidence"`
	}
	h.PostData(t, "/api/debates/debate_ac04/escalate", map[string]any{
		"actor_agent_id": fixtures.AgentTestLeadID, "conversation_id": convID, "task_id": fixtures.TaskID,
		"level": "L2", "issue": "接口风格争议（REST vs GraphQL）",
		"evidence":       `["e1","e2"]`,
		"options":        `["REST","GraphQL"]`,
		"recommendation": "REST 优先，首版后重评", "consequence": "若选 GraphQL 需延期",
	}, &escalation)
	if escalation.ID == "" || escalation.Status != "proposed" || escalation.Options == "" {
		t.Fatalf("escalation = %+v", escalation)
	}

	// 5) Close the debate as assistant: immutable accepted decision with dissent.
	var decision struct {
		ID             string `json:"id"`
		Status         string `json:"status"`
		Decision       string `json:"decision"`
		Dissent        string `json:"dissent"`
		AffectedTasks  string `json:"affected_tasks"`
		RevisitTrigger string `json:"revisit_trigger"`
	}
	h.PostData(t, "/api/debates/debate_ac04/close", map[string]any{
		"actor_agent_id": h.Seed.AssistantID, "title": "登录接口采用 REST",
		"decision": "REST + OpenAPI", "rationale": "团队熟悉度与工具链",
		"dissent":            "测试部门要求补齐契约测试",
		"affected_tasks":     `["task_1"]`,
		"affected_artifacts": "[]", "revisit_trigger": "GraphQL 需求出现",
	}, &decision)
	if decision.ID == "" || decision.Status != "accepted" || !strings.Contains(decision.Dissent, "契约测试") {
		t.Fatalf("decision = %+v", decision)
	}

	// 6) Retrieve/search decision, verify detail, append dissent (supersede).
	var byTask struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	h.GetData(t, "/api/decisions?task_id="+fixtures.TaskID, &byTask)
	if len(byTask.Items) == 0 {
		t.Fatal("decisions by task empty")
	}
	var searched struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	h.GetData(t, "/api/decisions?"+url.Values{"q": {"OpenAPI"}}.Encode(), &searched)
	if len(searched.Items) == 0 {
		t.Fatal("decision search by q=OpenAPI empty")
	}
	var detail struct {
		Decision struct {
			ID string `json:"id"`
		} `json:"decision"`
		Reopened int `json:"reopened"`
	}
	h.GetData(t, "/api/decisions/"+decision.ID+"/detail", &detail)
	if detail.Decision.ID != decision.ID || detail.Reopened != 0 {
		t.Fatalf("decision detail = %+v", detail)
	}

	var dissentDecision struct {
		ID string `json:"id"`
	}
	h.PostData(t, "/api/decisions/"+decision.ID+"/dissent", map[string]any{
		"actor_agent_id": fixtures.AgentTestLeadID, "dissent": "测试部门保留异议：需要压测证据",
	}, &dissentDecision)
	if dissentDecision.ID == "" || dissentDecision.ID == decision.ID {
		t.Fatalf("dissent supersede = %+v", dissentDecision)
	}
	old, err := h.Store.GetDecision(context.Background(), decision.ID)
	if err != nil || old.Status != "superseded" || old.SupersededBy != dissentDecision.ID {
		t.Fatalf("old decision after dissent = %+v err=%v", old, err)
	}

	// 7) Reopen with new evidence: exactly once, second attempt 403.
	var reopened struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	h.PostData(t, "/api/debates/"+dissentDecision.ID+"/reopen", map[string]any{
		"actor_agent_id": h.Seed.AssistantID, "new_evidence": "压测报告 v1",
	}, &reopened)
	if reopened.ID == "" || reopened.ID == dissentDecision.ID {
		t.Fatalf("reopen = %+v", reopened)
	}
	h.ExpectStatus(t, "POST", "/api/debates/"+reopened.ID+"/reopen", map[string]any{
		"actor_agent_id": h.Seed.AssistantID, "new_evidence": "压测报告 v2",
	}, 403)

	// 8) Discussion/stance messages carry meta kind and are in the thread.
	var msgs struct {
		Items []struct {
			Meta string `json:"meta"`
		} `json:"items"`
	}
	h.GetData(t, "/api/conversations/"+convID+"/messages?limit=200", &msgs)
	kinds := 0
	for _, m := range msgs.Items {
		if strings.Contains(m.Meta, "dept.discussion") || strings.Contains(m.Meta, "debate.stance") {
			kinds++
		}
	}
	if kinds < 6 {
		t.Fatalf("debate messages = %d, want >=6", kinds)
	}

	// 9) Audit chain covers escalation/close.
	audits, err := h.Store.QueryAudit(context.Background(), store.AuditFilter{Limit: 300})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]bool{}
	for _, a := range audits {
		actions[a.Action] = true
	}
	for _, want := range []string{"debate.escalate", "debate.close"} {
		if !actions[want] {
			t.Errorf("audit missing %q", want)
		}
	}
	if _, err := h.Store.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	t.Log("AC-04 R6 passed: seeded challenge, discussion 5/15, stance evidence gate, L2 escalate, decision+dissent, reopen once")
	t.Log("AC-04 remaining gaps: stall/timebox are not observable through the current HTTP contract")
}

func statusOf(r *Response) int {
	if r == nil {
		return 0
	}
	return r.Status
}

//go:build acceptance

package ac

import (
	"context"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/internal/write"
	"github.com/lignting/lignting-agents/tests/fixtures"
)

// TestAC03DeliveryLoop covers the R4/R6-available part of AC-03:
// task state machine -> department discussion -> PRD/implementation artifacts ->
// review challenge -> request_changes -> rework v2 -> old version still readable.
// G3/checklist is covered by AC-12; file timeline/write API is AC-15.
func TestAC03DeliveryLoop(t *testing.T) {
	h := NewHarness(t, WithSeed(), WithCoreServices())
	h.EnsureSession()

	// 1) Create a task in the seeded engineering department and walk the legal
	// state machine. Illegal jump is rejected.
	var created struct {
		Task struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"task"`
		Criteria []struct {
			ID string `json:"id"`
		} `json:"criteria"`
		Ready bool `json:"ready"`
	}
	h.PostData(t, "/api/tasks", map[string]any{
		"title": "AC-03 登录接口交付", "description": "PRD -> 实现 -> 评审 -> 返工",
		"department_id": fixtures.DeptDevID, "owner_id": fixtures.AgentDevAID,
		"reviewer_id": fixtures.AgentTestEmpID, "approver_id": fixtures.AgentDevLeadID,
		"perspective": "接口", "priority": "P1", "deadline": "2026-12-31T00:00:00Z",
		"budget_tokens": 2000, "budget_cost": 3,
		"acceptance": []string{"接口字段完整", "错误信息可读"},
	}, &created)
	taskID := created.Task.ID
	if taskID == "" || len(created.Criteria) != 2 || !created.Ready {
		t.Fatalf("created task = %+v", created)
	}
	h.ExpectStatus(t, "POST", "/api/tasks/"+taskID+"/transition", map[string]any{"to": "done"}, 409)
	for _, to := range []string{"ready", "discussing", "in_progress"} {
		var ignored map[string]any
		h.PostData(t, "/api/tasks/"+taskID+"/transition", map[string]any{"to": to, "reason": "AC-03"}, &ignored)
	}

	// 2) Department-internal discussion with perspective/stance (R6), written
	// into the task thread with meta kind dept.discussion.
	discussionConv := h.Seed.Conversations[fixtures.ConversationTaskID]
	var discussion struct {
		ID   string `json:"id"`
		Meta string `json:"meta"`
	}
	h.PostData(t, "/api/conversations/"+discussionConv+"/discussions", map[string]any{
		"actor_agent_id": fixtures.AgentDevAID, "debate_id": "debate_ac03",
		"perspective": "接口", "stance": "字段使用 snake_case，避免前端映射",
		"evidence": "e-interface-1",
	}, &discussion)
	if discussion.ID == "" || !strings.Contains(discussion.Meta, "dept.discussion") {
		t.Fatalf("discussion message = %+v", discussion)
	}

	// 3) PRD v1 + implementation v1 by the owner.
	createArtifact := func(typ, title, content string, actorID string) (artifactID, versionID, hash string, version int) {
		t.Helper()
		var out struct {
			Artifact struct {
				ID string `json:"id"`
			} `json:"artifact"`
			Version struct {
				ID          string `json:"id"`
				Version     int    `json:"version"`
				ContentHash string `json:"content_hash"`
			} `json:"version"`
		}
		h.PostData(t, "/api/artifacts", map[string]any{
			"type": typ, "title": title, "task_id": taskID, "department_id": fixtures.DeptDevID,
			"content": content, "actor_agent_id": actorID,
		}, &out)
		if out.Artifact.ID == "" || out.Version.ID == "" {
			t.Fatalf("create artifact %q = %+v", title, out)
		}
		return out.Artifact.ID, out.Version.ID, out.Version.ContentHash, out.Version.Version
	}
	prdID, prdVersionID, prdHash, prdVersion := createArtifact("prd", "登录 PRD", "# 登录 PRD v1\n\n验收标准：字段完整、错误可读", fixtures.AgentDevAID)
	codeID, codeVersionID, codeHash, codeVersion := createArtifact("code", "登录实现", "diff --git a/login.go b/login.go\n+func Login() {}", fixtures.AgentDevAID)
	if prdVersion != 1 || codeVersion != 1 || prdHash == "" || codeHash == "" || prdHash == codeHash {
		t.Fatalf("artifact versions = prd:%d/%s code:%d/%s", prdVersion, prdHash, codeVersion, codeHash)
	}
	if prdVersionID == "" || codeVersionID == "" || prdID == codeID {
		t.Fatal("artifact ids/versions not distinct")
	}

	// 4) Challenge the code version; author cannot review own artifact; reviewer
	// requests changes with required conditions.
	var review struct {
		ID                string `json:"id"`
		Severity          string `json:"severity"`
		ArtifactVersionID string `json:"artifact_version_id"`
		Status            string `json:"status"`
	}
	h.PostData(t, "/api/reviews", map[string]any{
		"artifact_id": codeID, "artifact_version_id": codeVersionID,
		"severity": "major", "stance": "字段契约不稳定",
		"claim": "缺少 UTF-8 BOM 与版本字段", "evidence": "artifact-v1 + command:go test ./...",
		"impact": "Excel 导出乱码", "proposed_resolution": "补 BOM 与 regression",
		"from_department": fixtures.DeptTestID, "to_department": fixtures.DeptDevID,
		"actor_agent_id": fixtures.AgentTestEmpID,
	}, &review)
	if review.ID == "" || review.Severity != "major" || review.ArtifactVersionID != codeVersionID || review.Status != "open" {
		t.Fatalf("review = %+v", review)
	}
	h.ExpectStatus(t, "POST", "/api/reviews/"+review.ID+"/verdicts", map[string]any{
		"verdict": "request_changes", "checked": "检查了实现与 PRD", "rationale": "缺 BOM",
		"conditions": `["补 BOM","补回归用例"]`, "actor_agent_id": fixtures.AgentDevAID,
	}, 403)
	var verdict struct {
		ID      string `json:"id"`
		Verdict string `json:"verdict"`
	}
	h.PostData(t, "/api/reviews/"+review.ID+"/verdicts", map[string]any{
		"verdict": "request_changes", "checked": "检查了实现与 PRD", "rationale": "缺 BOM",
		"conditions": `["补 BOM","补回归用例"]`, "actor_agent_id": fixtures.AgentTestEmpID,
	}, &verdict)
	if verdict.ID == "" || verdict.Verdict != "request_changes" {
		t.Fatalf("verdict = %+v", verdict)
	}

	// 5) Rework creates v2; v1 stays immutable; review is responded.
	var rework struct {
		ID          string `json:"id"`
		Version     int    `json:"version"`
		ContentHash string `json:"content_hash"`
	}
	h.PostData(t, "/api/reviews/"+review.ID+"/rework", map[string]any{
		"content":        "diff --git a/login.go b/login.go\n+func Login() { /* UTF-8 BOM */ }",
		"actor_agent_id": fixtures.AgentDevAID,
	}, &rework)
	if rework.Version != 2 || rework.ContentHash == codeHash {
		t.Fatalf("rework = %+v", rework)
	}
	var versions struct {
		Items []struct {
			Version     int    `json:"version"`
			ContentHash string `json:"content_hash"`
		} `json:"items"`
	}
	h.GetData(t, "/api/artifacts/"+codeID+"/versions", &versions)
	if len(versions.Items) != 2 {
		t.Fatalf("code versions = %d, want 2", len(versions.Items))
	}
	hashes := map[int]string{}
	for _, v := range versions.Items {
		hashes[v.Version] = v.ContentHash
	}
	if hashes[1] != codeHash {
		t.Errorf("v1 content changed by rework: v1=%q want %q", hashes[1], codeHash)
	}
	if hashes[2] != rework.ContentHash || hashes[2] == hashes[1] {
		t.Errorf("v2 hash = %q, want %q", hashes[2], rework.ContentHash)
	}
	var v1 struct {
		Version int    `json:"version"`
		Content string `json:"content"`
	}
	h.GetData(t, "/api/artifacts/"+codeID+"/versions/1", &v1)
	if v1.Version != 1 || !strings.Contains(v1.Content, "func Login") {
		t.Fatalf("v1 after rework = %+v", v1)
	}
	var reviews struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	h.GetData(t, "/api/reviews?artifact_id="+codeID, &reviews)
	responded := false
	for _, r := range reviews.Items {
		if r.ID == review.ID && r.Status == "responded" {
			responded = true
		}
	}
	if !responded {
		t.Fatalf("review after rework = %+v", reviews.Items)
	}

	// 6) Submit for review; task timeline keeps the transitions and discussion.
	var ignored map[string]any
	h.PostData(t, "/api/tasks/"+taskID+"/transition", map[string]any{
		"to": "in_review", "actor_agent_id": fixtures.AgentDevAID, "reason": "提交评审",
	}, &ignored)
	var detail struct {
		Task struct {
			Status string `json:"status"`
		} `json:"task"`
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	h.GetData(t, "/api/tasks/"+taskID, &detail)
	if detail.Task.Status != "in_review" {
		t.Fatalf("task status = %q, want in_review", detail.Task.Status)
	}
	transitionCount := 0
	for _, ev := range detail.Events {
		if ev.Type == "task.transition" {
			transitionCount++
		}
	}
	if transitionCount < 4 {
		t.Fatalf("task transitions = %d, want >=4", transitionCount)
	}

	// 7) Audit chain covers task/discussion/artifact/review/rework.
	audits, err := h.Store.QueryAudit(context.Background(), store.AuditFilter{Limit: 300})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]bool{}
	for _, a := range audits {
		actions[a.Action] = true
	}
	for _, want := range []string{"task.create", "artifact.create", "review.create", "review.verdict", "review.rework"} {
		if !actions[want] {
			t.Errorf("audit missing %q", want)
		}
	}
	if _, err := h.Store.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	t.Log("AC-03 R4/R6 passed: state machine, discussion message, PRD+code v1, challenge, request_changes, rework v2, v1 immutable")
	t.Run("g3_and_write_timeline", func(t *testing.T) {
		ctx := context.Background()

		// A real write through the shared write tool, linked to this task.
		svc, err := write.New(write.Options{Root: h.Cfg.WorkspaceDir, TimelineLimit: 30, Repository: h.Store, Audit: h.Store})
		if err != nil {
			t.Fatalf("write service: %v", err)
		}
		res, err := svc.Apply(ctx, write.Request{
			ActorID: fixtures.AgentDevAID, TaskID: taskID, Path: "ac03/login.go",
			Content: "package login\n\nfunc Login() error { return nil }\n", Branch: "main", Reason: "实现登录接口",
		})
		if err != nil {
			t.Fatalf("write login.go: %v", err)
		}
		var timeline struct {
			Items []struct {
				Path    string `json:"path"`
				TaskID  string `json:"task_id"`
				AgentID string `json:"agent_id"`
				Diff    string `json:"diff"`
				Reason  string `json:"reason"`
				Branch  string `json:"branch"`
				Status  string `json:"status"`
			} `json:"items"`
		}
		h.GetData(t, "/api/writes/timeline?path=ac03/login.go", &timeline)
		if len(timeline.Items) != 1 {
			t.Fatalf("task write timeline = %+v, want 1", timeline.Items)
		}
		item := timeline.Items[0]
		if item.TaskID != taskID || item.AgentID != fixtures.AgentDevAID || item.Status != "applied" || item.Diff == "" || item.Reason == "" || item.Branch != "main" {
			t.Fatalf("task write timeline item = %+v (event %s)", item, res.Event.ID)
		}

		// Checklist complete -> G3 -> human approve -> reviewer approve -> done.
		for i, criterion := range created.Criteria {
			if err := h.postEvidence(taskID, criterion.ID, fixtures.AgentDevAID, `["command:go test ./..."]`); err != nil {
				t.Fatalf("evidence criterion %d: %v", i, err)
			}
			h.postVerdict(t, taskID, criterion.ID, fixtures.AgentDevLeadID, "approved")
		}
		var g3 struct {
			Approval struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"approval"`
		}
		h.PostData(t, "/api/tasks/"+taskID+"/g3", map[string]any{
			"actor_agent_id": fixtures.AgentDevLeadID, "risk": "normal", "residual_risks": "无",
		}, &g3)
		if g3.Approval.ID == "" || g3.Approval.Status != "pending" {
			t.Fatalf("G3 = %+v", g3)
		}
		var decided struct {
			Status string `json:"status"`
		}
		h.PostData(t, "/api/approvals/"+g3.Approval.ID+"/decide", map[string]any{
			"decision": "approved", "reason": "AC-03 核对表通过",
		}, &decided)
		if decided.Status != "approved" {
			t.Fatalf("G3 decide = %+v", decided)
		}
		var ignored map[string]any
		h.PostData(t, "/api/tasks/"+taskID+"/transition", map[string]any{
			"to": "approved", "actor_agent_id": fixtures.AgentTestEmpID, "reason": "评审通过",
		}, &ignored)
		h.PostData(t, "/api/tasks/"+taskID+"/transition", map[string]any{
			"to": "done", "actor_agent_id": fixtures.AgentDevLeadID, "reason": "G3 通过",
		}, &ignored)
		var done struct {
			Task struct {
				Status string `json:"status"`
			} `json:"task"`
		}
		h.GetData(t, "/api/tasks/"+taskID, &done)
		if done.Task.Status != "done" {
			t.Fatalf("task after G3 = %q, want done", done.Task.Status)
		}
		t.Log("AC-03 G3/write passed: task-linked write timeline + G3 approve + done")
	})
}

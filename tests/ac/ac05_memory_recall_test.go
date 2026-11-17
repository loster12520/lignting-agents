//go:build acceptance

package ac

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/tests/fixtures"
)

// TestAC05MemoryRecallAndCorrect covers the R7-available part of AC-05:
// decision recall source, candidate -> approve -> edit (optimistic lock) ->
// delete tombstone, human-only/cross-department visibility with real Agent
// tokens, manual propose/rollback, context snapshot/turn replay, audit.
// Still R2/R7 end-to-end: assistant answering from memory and injection effect.
func TestAC05MemoryRecallAndCorrect(t *testing.T) {
	h := NewHarness(t, WithSeed(), WithCoreServices())
	h.EnsureSession()
	ctx := context.Background()

	// 1) Recall has sources: decision search returns the seeded BOM decision
	// with evidence/dissent fields, and memory list exposes source evidence.
	var decisions struct {
		Items []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Evidence string `json:"evidence"`
			Dissent  string `json:"dissent"`
		} `json:"items"`
	}
	h.GetData(t, "/api/decisions?q=BOM", &decisions)
	if len(decisions.Items) == 0 {
		t.Fatal("decision recall by q=BOM empty")
	}
	if decisions.Items[0].Evidence == "" {
		t.Errorf("decision has no evidence source: %+v", decisions.Items[0])
	}
	var list struct {
		Items []struct {
			ID       string `json:"id"`
			Content  string `json:"content"`
			Evidence string `json:"evidence"`
			Status   string `json:"status"`
			Version  int    `json:"version"`
		} `json:"items"`
	}
	h.GetData(t, "/api/memories?q=BOM", &list)
	if len(list.Items) == 0 {
		t.Fatal("memory recall by q=BOM empty")
	}

	// 2) Candidate -> pending queue -> approve.
	var candidate map[string]any
	h.PostData(t, "/api/memories", map[string]any{
		"scope": "department", "department_id": fixtures.DeptDevID, "agent_id": fixtures.AgentDevAID,
		"entity_type": "task", "entity_key": fixtures.TaskID,
		"content": "导出大文件需流式处理，避免内存峰值。", "summary": "大文件流式导出",
		"tags": []string{"性能", "导出"}, "confidence": "medium",
		"evidence":   []map[string]any{{"kind": "message", "ref": "msg_task_comment"}},
		"visibility": "team", "reusable": true, "decision_relevant": true,
	}, &candidate)
	created, _ := candidate["Memory"].(map[string]any)
	if created == nil {
		created, _ = candidate["memory"].(map[string]any)
	}
	if created == nil {
		t.Fatalf("candidate result = %v", candidate)
	}
	memID, _ := created["id"].(string)
	if memID == "" {
		t.Fatalf("candidate id missing: %v", created)
	}
	var pending struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	h.GetData(t, "/api/memories?status=candidate", &pending)
	foundPending := false
	for _, m := range pending.Items {
		if m.ID == memID && m.Status == "candidate" {
			foundPending = true
		}
	}
	if !foundPending {
		t.Fatalf("candidate %s not in pending queue", memID)
	}
	var approved map[string]any
	h.PostData(t, "/api/memories/"+memID+"/approve", map[string]any{}, &approved)
	approvedMem, _ := approved["Memory"].(map[string]any)
	if approvedMem == nil || approvedMem["status"] != "active" {
		t.Fatalf("approved memory = %v", approved)
	}

	// 3) Edit with optimistic lock; stale version is a conflict.
	var patched struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
		Version int    `json:"version"`
	}
	h.ExpectStatus(t, "PATCH", "/api/memories/"+memID, map[string]any{
		"summary": "大文件流式导出（修订）", "expected_version": 1,
	}, 200).Data(t, &patched)
	if patched.Summary == "" || patched.Version != 2 {
		t.Fatalf("patched memory = %+v", patched)
	}
	h.ExpectStatus(t, "PATCH", "/api/memories/"+memID, map[string]any{
		"summary": "x", "expected_version": 1,
	}, 409)

	// 4) Human-only memory is visible to the human but not to Agent tokens;
	// cross-department private memory is not visible to another department.
	var humanOnly struct {
		ID         string `json:"id"`
		Visibility string `json:"visibility"`
	}
	h.PostData(t, "/api/memories", map[string]any{
		"direct": true, "scope": "assistant", "content": "人类专属：偏好简洁结论",
		"visibility": "human-only", "entity_type": "person", "entity_key": "owner", "reusable": true,
	}, &humanOnly)
	if humanOnly.ID == "" || humanOnly.Visibility != "human-only" {
		t.Fatalf("human-only memory = %+v", humanOnly)
	}
	plainDev, hashDev, err := store.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := h.Store.CreateAgent(ctx, domain.Agent{
		Name: "AC-05 研发员工", Kind: domain.AgentKindEmployee,
		DepartmentID: fixtures.DeptDevID, TokenHash: hashDev,
	}); err != nil {
		t.Fatalf("dev agent: %v", err)
	}
	plainTest, hashTest, err := store.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := h.Store.CreateAgent(ctx, domain.Agent{
		Name: "AC-05 测试员工", Kind: domain.AgentKindEmployee,
		DepartmentID: fixtures.DeptTestID, TokenHash: hashTest,
	}); err != nil {
		t.Fatalf("test agent: %v", err)
	}
	itemsByToken := func(token string) []string {
		t.Helper()
		resp, err := h.Client.GetWithHeaders("/api/memories?scope=department&department_id="+fixtures.DeptDevID,
			map[string]string{"X-Lignting-Agent-Token": token})
		if err != nil {
			t.Fatalf("agent memory list: %v", err)
		}
		var out struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		resp.MustStatus(t, 200).Data(t, &out)
		ids := make([]string, 0, len(out.Items))
		for _, it := range out.Items {
			ids = append(ids, it.ID)
		}
		return ids
	}
	devSees := itemsByToken(plainDev)
	testSees := itemsByToken(plainTest)
	has := func(ids []string, want string) bool {
		for _, id := range ids {
			if id == want {
				return true
			}
		}
		return false
	}
	if !has(devSees, memID) {
		t.Errorf("dev employee cannot read own department memory")
	}
	if has(devSees, humanOnly.ID) {
		t.Errorf("agent token can read human-only memory %s", humanOnly.ID)
	}
	if has(testSees, memID) {
		t.Errorf("other-department employee can read dept_dev memory %s", memID)
	}
	// Human still sees the human-only memory.
	var humanList struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	h.GetData(t, "/api/memories?scope=assistant", &humanList)
	if !has(func() []string {
		ids := make([]string, 0, len(humanList.Items))
		for _, it := range humanList.Items {
			ids = append(ids, it.ID)
		}
		return ids
	}(), humanOnly.ID) {
		t.Errorf("human cannot see human-only memory %s", humanOnly.ID)
	}

	// 5) Delete is a tombstone: list no longer returns it; row keeps deleted_at.
	h.ExpectStatus(t, "DELETE", "/api/memories/"+memID, map[string]any{"reason": "过时"}, 200)
	h.GetData(t, "/api/memories?q=流式", &list)
	for _, m := range list.Items {
		if m.ID == memID {
			t.Fatalf("deleted memory still listed: %s", memID)
		}
	}
	var deletedAt string
	if err := h.Store.DB.QueryRowContext(ctx, "SELECT deleted_at FROM memories WHERE id=?", memID).Scan(&deletedAt); err != nil {
		t.Fatalf("read tombstone: %v", err)
	}
	if deletedAt == "" {
		t.Fatalf("deleted memory has empty deleted_at")
	}

	// 6) Manual propose v1/v2 -> list -> rollback to v1.
	var manual1 struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
		Content string `json:"content"`
	}
	h.PostData(t, "/api/manuals", map[string]any{
		"scope": "department", "department_id": fixtures.DeptDevID,
		"content": "# 角色\n研发评审 v1\n\n# 红线\n不越过人类审批",
		"sources": []string{"mem_1"},
	}, &manual1)
	if manual1.ID == "" || manual1.Version != 1 {
		t.Fatalf("manual v1 = %+v", manual1)
	}
	var manual2 struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	h.PostData(t, "/api/manuals", map[string]any{
		"scope": "department", "department_id": fixtures.DeptDevID,
		"content": "# 角色\n研发评审 v2\n\n# 红线\n不越过人类审批",
		"sources": []string{"mem_2"},
	}, &manual2)
	if manual2.Version != 2 {
		t.Fatalf("manual v2 version = %d", manual2.Version)
	}
	var rolled struct {
		Version int    `json:"version"`
		Content string `json:"content"`
	}
	h.PostData(t, "/api/manuals/"+manual1.ID+"/rollback", map[string]any{"to_version": 1}, &rolled)
	if rolled.Content == "" || rolled.Version < 3 {
		t.Fatalf("manual rollback = %+v", rolled)
	}
	var manuals struct {
		Items []struct {
			Version int `json:"version"`
		} `json:"items"`
	}
	h.GetData(t, "/api/manuals?scope=department&department_id="+fixtures.DeptDevID, &manuals)
	if len(manuals.Items) < 3 {
		t.Fatalf("manual versions = %d, want >=3", len(manuals.Items))
	}

	// 7) Context snapshot/turn replay exposes what was injected (keys used by
	// core: memory_entries/tokens/degraded).
	payload, _ := json.Marshal(map[string]any{
		"memory_entries": []map[string]any{{"id": memID, "source": "memory"}},
		"tokens":         123,
		"degraded":       false,
	})
	if err := h.Store.InsertContextSnapshot(ctx, domain.ContextSnapshot{
		TurnID: "turn_ac05", AgentID: fixtures.AgentDevAID, ConversationID: h.Seed.Conversations[fixtures.ConversationTaskID],
		Payload: string(payload),
	}); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
	if err := h.Store.CreateTurn(ctx, domain.Turn{
		ID: "turn_ac05", AgentID: fixtures.AgentDevAID, ConversationID: h.Seed.Conversations[fixtures.ConversationTaskID],
	}); err != nil {
		t.Fatalf("create turn: %v", err)
	}
	var snaps struct {
		Items []struct {
			Payload string `json:"payload"`
		} `json:"items"`
	}
	h.GetData(t, "/api/context-snapshots?turn_id=turn_ac05", &snaps)
	if len(snaps.Items) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(snaps.Items))
	}
	var snapPayload map[string]any
	if err := json.Unmarshal([]byte(snaps.Items[0].Payload), &snapPayload); err != nil {
		t.Fatalf("snapshot payload: %v", err)
	}
	for _, key := range []string{"memory_entries", "tokens", "degraded"} {
		if _, ok := snapPayload[key]; !ok {
			t.Errorf("snapshot missing %q: %v", key, snapPayload)
		}
	}
	var turnDetail struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
		Snapshots []map[string]any `json:"snapshots"`
	}
	h.GetData(t, "/api/turns/turn_ac05", &turnDetail)
	if turnDetail.Turn.ID != "turn_ac05" || len(turnDetail.Snapshots) != 1 {
		t.Fatalf("turn replay = %+v", turnDetail)
	}

	// 8) Audit chain covers memory/manual operations.
	audits, err := h.Store.QueryAudit(ctx, store.AuditFilter{Limit: 300})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]bool{}
	for _, a := range audits {
		actions[a.Action] = true
	}
	for _, want := range []string{"memory.candidate", "memory.approve", "memory.update", "memory.delete", "manual.propose", "manual.rollback"} {
		if !actions[want] {
			t.Errorf("audit missing %q", want)
		}
	}
	if _, err := h.Store.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	t.Log("AC-05 R7 passed: recall sources, candidate/approve/edit/delete tombstone, human-only + cross-dept isolation, manual rollback, snapshot")
	t.Run("assistant_answer_from_memory_injection", func(t *testing.T) {
		SkipSkeleton(t, "AC-05/AC-11 assistant answer from injected memory", "R7 core runtime", "需要 Agent turn 的 context.request 真注入（当前 platform 层无运行时）")
	})
}

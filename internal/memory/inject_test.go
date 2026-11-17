package memory

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
)

func TestInjectDepartmentIsolation(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	supA := supervisor("sup_a", "dept_a")
	supB := supervisor("sup_b", "dept_b")

	mA := mustActive(t, svc, baseCandidate("t1"), supA)
	cB := baseCandidate("t1")
	cB.DepartmentID = "dept_b"
	cB.AgentID = "sup_b"
	cB.EntityKey = "backend-b"
	mB := mustActive(t, svc, cB, supB)

	reqA := InjectionRequest{TurnID: "turn_a", AgentID: "sup_a", AgentKind: ActorSupervisor,
		DepartmentID: "dept_a", IncludeArchives: true, EffectiveInputTokens: 8000}
	gotA, err := svc.Assemble(ctx, reqA)
	if err != nil {
		t.Fatalf("装配 A: %v", err)
	}
	if !containsID(gotA.Memories, mA.ID) {
		t.Fatal("本部门档案应被注入")
	}
	if containsID(gotA.Memories, mB.ID) {
		t.Fatal("跨部门档案不得注入")
	}

	reqB := InjectionRequest{TurnID: "turn_b", AgentID: "sup_b", AgentKind: ActorSupervisor,
		DepartmentID: "dept_b", IncludeArchives: true, EffectiveInputTokens: 8000}
	gotB, err := svc.Assemble(ctx, reqB)
	if err != nil {
		t.Fatalf("装配 B: %v", err)
	}
	if !containsID(gotB.Memories, mB.ID) || containsID(gotB.Memories, mA.ID) {
		t.Fatal("部门隔离在 B 方向同样成立")
	}
}

func TestAgentPrivateNotVisibleToPeers(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	sup := supervisor("sup_1", "dept_a")
	c := baseCandidate("t1")
	c.Visibility = domain.MemoryAgentPrivate
	m := mustActive(t, svc, c, sup)

	req := InjectionRequest{TurnID: "t", AgentID: "sup_2", AgentKind: ActorSupervisor,
		DepartmentID: "dept_a", IncludeArchives: true, EffectiveInputTokens: 8000}
	got, err := svc.Assemble(ctx, req)
	if err != nil {
		t.Fatalf("装配: %v", err)
	}
	if containsID(got.Memories, m.ID) {
		t.Fatal("agent-private 不得注入同部门他人")
	}
	if droppedReason(got.Dropped, m.ID) != "not_visible" {
		t.Fatalf("应记录不可见原因，得到 %q", droppedReason(got.Dropped, m.ID))
	}

	reqOwner := InjectionRequest{TurnID: "t2", AgentID: "sup_1", AgentKind: ActorSupervisor,
		DepartmentID: "dept_a", IncludeArchives: true, EffectiveInputTokens: 8000}
	gotOwner, _ := svc.Assemble(ctx, reqOwner)
	if !containsID(gotOwner.Memories, m.ID) {
		t.Fatal("agent-private 应注入给本人")
	}
}

func TestHumanOnlyNeverAutoInjected(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	secret := Candidate{
		Scope: domain.MemoryScopeAssistant, EntityType: EntityPreference, EntityKey: "human-style",
		Content:    "人类主管偏好：先给结论，再给理由",
		Confidence: ConfidenceHigh, Evidence: []Evidence{{Kind: EvidenceHuman, Ref: "user_1"}},
		Visibility: domain.MemoryHumanOnly, Sensitivity: SensitivitySensitive,
		Reusable: true, DecisionRelevant: true, CreatedBy: "user_1",
	}
	m, err := svc.CreateDirect(ctx, secret, human("user_1"))
	if err != nil {
		t.Fatalf("人类直建 human-only: %v", err)
	}
	for _, actor := range []Access{assistant("asst_1"), supervisor("sup_1", "dept_a"), employee("emp_1", "dept_a")} {
		req := InjectionRequest{TurnID: "t", AgentID: actor.ID, AgentKind: actor.Type,
			DepartmentID: actor.DepartmentID, IncludeArchives: true, EffectiveInputTokens: 8000}
		got, aerr := svc.Assemble(ctx, req)
		if aerr != nil {
			t.Fatalf("装配: %v", aerr)
		}
		if containsID(got.Memories, m.ID) {
			t.Fatalf("%s 不得自动注入 human-only", actor.Type)
		}
		// 助理是 org 视野，能检索到该条并应给出显式裁剪原因；部门主管/员工在部门过滤阶段就看不到它。
		if actor.Type == ActorAssistant {
			if droppedReason(got.Dropped, m.ID) != "human_only_no_auto_inject" {
				t.Fatalf("助理的裁剪原因应为 human_only_no_auto_inject，得到 %q", droppedReason(got.Dropped, m.ID))
			}
		}
	}
}

func TestBudgetTrimming(t *testing.T) {
	svc, _, _, _ := newTestService(t, func(c *Config) {
		c.InjectPercent = 0.1
		c.InjectAbsCapTokens = 6000
	})
	sup := supervisor("sup_1", "dept_a")
	for i := 0; i < 10; i++ {
		c := baseCandidate("")
		c.EntityKey = "k-" + string(rune('a'+i))
		c.Content = "能力说明一二三四五六"
		c.Summary = "能力说明一二三四五六"
		mustActive(t, svc, c, sup)
	}
	req := InjectionRequest{TurnID: "t", AgentID: "sup_1", AgentKind: ActorSupervisor,
		DepartmentID: "dept_a", IncludeArchives: true, EffectiveInputTokens: 4000}
	got, err := svc.Assemble(ctx, req)
	if err != nil {
		t.Fatalf("装配: %v", err)
	}
	if len(got.Memories) != 5 {
		t.Fatalf("按 110 token 档案预算应注入 5 条，得到 %d（budget=%d）", len(got.Memories), got.Budgets.Archive)
	}
	if len(got.Dropped) < 5 {
		t.Fatalf("其余条目应记录为 budget 裁剪，得到 %d", len(got.Dropped))
	}
	for _, d := range got.Dropped {
		if d.Reason != "budget" {
			t.Fatalf("裁剪原因应为 budget，得到 %q", d.Reason)
		}
	}
	var used int64
	for _, m := range got.Memories {
		used += m.Tokens
	}
	if used > got.Budgets.Archive {
		t.Fatalf("注入总量 %d 超预算 %d", used, got.Budgets.Archive)
	}
}

func TestPinnedInjectedFirst(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	sup := supervisor("sup_1", "dept_a")
	m1 := mustActive(t, svc, baseCandidate("t1"), sup)
	c2 := baseCandidate("t1")
	c2.EntityKey = "k2"
	c2.Content = "另一条能力"
	c2.Confidence = ConfidenceLow
	mustActive(t, svc, c2, sup)
	if _, err := svc.Pin(ctx, m1.ID, true, sup); err != nil {
		t.Fatalf("钉住: %v", err)
	}
	got, _ := svc.Assemble(ctx, InjectionRequest{TurnID: "t", AgentID: "sup_1", AgentKind: ActorSupervisor,
		DepartmentID: "dept_a", IncludeArchives: true, EffectiveInputTokens: 8000})
	if len(got.Memories) == 0 || got.Memories[0].ID != m1.ID {
		t.Fatalf("钉住的档案应排第一，得到 %+v", got.Memories)
	}
}

func TestAssembleDegradesOnRepoError(t *testing.T) {
	svc, repo, _, _ := newTestService(t, nil)
	repo.failList = errors.New("db locked")
	got, err := svc.Assemble(ctx, InjectionRequest{
		TurnID: "t", AgentID: "asst_1", AgentKind: ActorAssistant,
		TaskBrief: "实现登录接口", IncludeArchives: true, EffectiveInputTokens: 8000,
	})
	if err != nil {
		t.Fatalf("检索失败不应返回错误: %v", err)
	}
	if !got.Degraded || !strings.Contains(got.DegradeReason, "archive_read_failed") {
		t.Fatalf("应降级并说明原因: %+v", got)
	}
	if got.Task == nil || got.Task.Text == "" {
		t.Fatal("降级后仍应保留当前任务")
	}
	if len(got.Memories) != 0 {
		t.Fatal("降级时不应注入档案")
	}
}

func TestContextRequestSnapshotFields(t *testing.T) {
	svc, repo, _, _ := newTestService(t, nil)
	sup := supervisor("sup_1", "dept_a")
	m := mustActive(t, svc, baseCandidate("t1"), sup)

	got, err := svc.Assemble(ctx, InjectionRequest{
		TurnID: "turn_42", AgentID: "sup_1", AgentKind: ActorSupervisor,
		DepartmentID: "dept_a", TaskID: "task_1", ConversationID: "conv_1", Model: "deepseek-flash",
		TaskBrief: "评审后端方案", IncludeArchives: true, EffectiveInputTokens: 8000,
		RecentMessages: 5, RecentTokens: 240,
	})
	if err != nil {
		t.Fatalf("装配: %v", err)
	}
	payload, err := got.Payload()
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	var decoded ContextRequest
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("payload 不是合法 JSON: %v", err)
	}
	if decoded.TurnID != "turn_42" || decoded.AgentID != "sup_1" || decoded.Model != "deepseek-flash" {
		t.Fatalf("快照缺少 turn/agent/model: %+v", decoded)
	}
	if decoded.Budgets.MemoryBudget <= 0 || decoded.Budgets.Archive <= 0 {
		t.Fatalf("预算字段缺失: %+v", decoded.Budgets)
	}
	if decoded.Estimated <= 0 {
		t.Fatal("应给出 token 估算")
	}
	if len(decoded.Memories) != 1 || !strings.HasPrefix(decoded.Memories[0].Source, "memory://") {
		t.Fatalf("档案来源标签不正确: %+v", decoded.Memories)
	}
	if decoded.Task == nil || decoded.Task.Text == "" {
		t.Fatal("当前任务应写入快照")
	}
	if len(got.Memories) != 1 || got.Memories[0].ID != m.ID {
		t.Fatalf("注入项与档案不一致: %+v", got.Memories)
	}
	// 读痕：注入的每条都应记录 memory_reads。
	if len(repo.reads) != len(got.Memories) {
		t.Fatalf("读痕数量应为 %d，得到 %d", len(got.Memories), len(repo.reads))
	}
	if repo.reads[0].ReaderType != ActorSupervisor || repo.reads[0].TurnID != "turn_42" {
		t.Fatalf("读痕字段不正确: %+v", repo.reads[0])
	}
}

func TestAssembleUsesOnlyApprovedManual(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	key := ManualKey{Scope: domain.MemoryScopeDepartment, DepartmentID: "dept_a"}
	content := "# 角色与目标\n负责部门协调\n# 红线与升级\n对外承诺必须升级"
	if _, err := svc.ProposeManual(ctx, key, content, nil, assistant("asst_1")); err != nil {
		t.Fatalf("提议手册: %v", err)
	}
	got, _ := svc.Assemble(ctx, InjectionRequest{TurnID: "t", AgentID: "sup_1", AgentKind: ActorSupervisor,
		DepartmentID: "dept_a", IncludeManual: true, EffectiveInputTokens: 8000})
	if got.Manual != nil {
		t.Fatal("proposed 手册不得注入")
	}
	versions, _ := svc.ListManualVersions(ctx, key)
	if len(versions) != 1 {
		t.Fatalf("应有一版：%d", len(versions))
	}
	if _, err := svc.ApproveManual(ctx, versions[0].ID, human("user_1")); err != nil {
		t.Fatalf("批准: %v", err)
	}
	got, _ = svc.Assemble(ctx, InjectionRequest{TurnID: "t2", AgentID: "sup_1", AgentKind: ActorSupervisor,
		DepartmentID: "dept_a", IncludeManual: true, EffectiveInputTokens: 8000})
	if got.Manual == nil || got.Manual.Version != 1 {
		t.Fatalf("批准后应注入 v1: %+v", got.Manual)
	}
}

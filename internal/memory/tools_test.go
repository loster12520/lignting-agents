package memory

import (
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
)

func humanOnlyMemory(t *testing.T, svc *Service) domain.Memory {
	t.Helper()
	c := Candidate{
		Scope: domain.MemoryScopeAssistant, EntityType: EntityPreference, EntityKey: "human-secret",
		Content:    "人类手机 13800138000，偏好先结论",
		Confidence: ConfidenceHigh, Evidence: []Evidence{{Kind: EvidenceHuman, Ref: "user_1"}},
		Visibility: domain.MemoryHumanOnly, Sensitivity: SensitivitySensitive,
		Reusable: true, DecisionRelevant: true, CreatedBy: "user_1",
	}
	m, err := svc.CreateDirect(ctx, c, human("user_1"))
	if err != nil {
		t.Fatalf("创建 human-only: %v", err)
	}
	return m
}

func TestReadToolHumanOnlyPolicy(t *testing.T) {
	svc, repo, _, _ := newTestService(t, nil)
	m := humanOnlyMemory(t, svc)
	tool := NewReadTool(svc)

	// 助理：显式单条 + turn_id → 允许，但必须脱敏并留痕。
	res, err := tool.Invoke(ctx, assistant("asst_1"), ReadArgs{ID: m.ID, TurnID: "turn_1", Reason: "用户问偏好"})
	if err != nil {
		t.Fatalf("助理单条读取应允许: %v", err)
	}
	if res.Memory == nil || !res.Memory.Redacted {
		t.Fatalf("human-only 读取必须脱敏: %+v", res.Memory)
	}
	if strings.Contains(res.Memory.Content, "13800138000") {
		t.Fatalf("手机号应被脱敏: %s", res.Memory.Content)
	}
	if len(repo.reads) != 1 || repo.reads[0].ReaderType != ActorAssistant || repo.reads[0].TurnID != "turn_1" {
		t.Fatalf("必须写读取留痕: %+v", repo.reads)
	}
	if _, ok := repo.lastEvent(ActionRead); !ok {
		t.Fatal("应记录 read 事件")
	}

	// 缺 turn_id → 拒绝（审计要求）。
	if _, err := tool.Invoke(ctx, assistant("asst_1"), ReadArgs{ID: m.ID}); err == nil {
		t.Fatal("缺少 turn_id 必须拒绝")
	}
	// 员工/部门主管：一律拒绝。
	for _, actor := range []Access{employee("emp_1", "dept_a"), supervisor("sup_1", "dept_a")} {
		if _, err := tool.Invoke(ctx, actor, ReadArgs{ID: m.ID, TurnID: "turn_x"}); err == nil {
			t.Fatalf("%s 不得读取 human-only", actor.Type)
		}
	}
	if _, ok := repo.lastEvent(ActionReadDenied); !ok {
		t.Fatal("越权读取必须留痕")
	}
	// 人类：可读（审计模式），且不会再被二次脱敏。
	// 注意：敏感字段在写入时已过滤，所以原文不再可恢复——这是 FR-MEM-10 的预期安全姿态。
	humanRes, err := tool.Invoke(ctx, human("user_1"), ReadArgs{ID: m.ID, TurnID: "turn_h"})
	if err != nil {
		t.Fatalf("人类应可读: %v", err)
	}
	if humanRes.Memory.Redacted {
		t.Fatal("人类读取不应再脱敏（写入时已过滤）")
	}
	if !strings.Contains(humanRes.Memory.Content, "[REDACTED:cn_phone]") {
		t.Fatalf("写入前过滤应留下脱敏标记: %s", humanRes.Memory.Content)
	}
}

func TestReadToolDeletedMemoryNotFound(t *testing.T) {
	svc, repo, _, _ := newTestService(t, nil)
	m := mustActive(t, svc, baseCandidate("t1"), supervisor("sup_1", "dept_a"))
	if err := svc.Delete(ctx, m.ID, "用户删除", human("user_1")); err != nil {
		t.Fatalf("删除: %v", err)
	}
	tool := NewReadTool(svc)
	if _, err := tool.Invoke(ctx, assistant("asst_1"), ReadArgs{ID: m.ID, TurnID: "t"}); err == nil {
		t.Fatal("墓碑不可读")
	}
	before := len(repo.reads)
	if _, err := tool.Invoke(ctx, human("user_1"), ReadArgs{ID: m.ID, TurnID: "t"}); err == nil {
		t.Fatal("人类也不可读墓碑")
	}
	if len(repo.reads) != before {
		t.Fatal("被拒绝的读取不应留下正常读痕")
	}
}

func TestReadToolSingleTargetOnly(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	m := mustActive(t, svc, baseCandidate("t1"), supervisor("sup_1", "dept_a"))
	tool := NewReadTool(svc)
	if _, err := tool.Invoke(ctx, human("user_1"), ReadArgs{ID: m.ID, EntityType: EntityCapability, EntityKey: "backend", TurnID: "t"}); err == nil {
		t.Fatal("同时给 id 与 entity 必须拒绝（不支持批量/歧义）")
	}
}

func TestReadToolByEntityKey(t *testing.T) {
	svc, repo, _, _ := newTestService(t, nil)
	m := mustActive(t, svc, baseCandidate("t1"), supervisor("sup_1", "dept_a"))
	tool := NewReadTool(svc)
	res, err := tool.Invoke(ctx, supervisor("sup_1", "dept_a"), ReadArgs{EntityType: EntityCapability, EntityKey: "backend", TurnID: "t"})
	if err != nil {
		t.Fatalf("按实体键读取: %v", err)
	}
	if res.Memory.ID != m.ID {
		t.Fatalf("应命中同一条: %+v", res.Memory)
	}

	// 人为造出两条同键生效版本 → 必须要求用 id 精确读取。
	dup := m
	dup.ID = "mem_dup"
	if err := repo.InsertMemory(ctx, dup); err != nil {
		t.Fatalf("插入重复: %v", err)
	}
	if _, err := tool.Invoke(ctx, supervisor("sup_1", "dept_a"), ReadArgs{EntityType: EntityCapability, EntityKey: "backend", TurnID: "t"}); err == nil {
		t.Fatal("多版本命中应要求 id 精确读取")
	}
}

func TestReadToolAgentPrivateAndSensitive(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	sup := supervisor("sup_1", "dept_a")
	c := baseCandidate("t1")
	c.Visibility = domain.MemoryAgentPrivate
	c.Sensitivity = SensitivitySensitive
	c.Content = "私有评估：手机 13900139000，返工率偏高"
	m := mustActive(t, svc, c, sup)
	tool := NewReadTool(svc)

	if _, err := tool.Invoke(ctx, supervisor("sup_2", "dept_a"), ReadArgs{ID: m.ID, TurnID: "t"}); err == nil {
		t.Fatal("同部门他人不得读 agent-private")
	}
	res, err := tool.Invoke(ctx, sup, ReadArgs{ID: m.ID, TurnID: "t"})
	if err != nil {
		t.Fatalf("本人应可读: %v", err)
	}
	if !res.Memory.Redacted || strings.Contains(res.Memory.Content, "13900139000") {
		t.Fatalf("sensitive 内容对 Agent 必须脱敏: %+v", res.Memory)
	}
}

func TestReadToolWithNilService(t *testing.T) {
	tool := NewReadTool(nil)
	if _, err := tool.Invoke(ctx, human("user_1"), ReadArgs{ID: "x", TurnID: "t"}); err == nil {
		t.Fatal("未初始化服务应报错")
	}
}

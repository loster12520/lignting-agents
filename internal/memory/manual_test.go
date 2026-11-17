package memory

import (
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
)

const manualV1 = "# 角色与目标\n负责部门协调与验收\n" +
	"# 管理原则\n先对齐验收标准再派单\n" +
	"# 红线与升级\n对外承诺必须升级给人类\n" +
	"# 人类偏好\n先给结论，再给理由"

const manualV2 = "# 角色与目标\n负责部门协调与验收\n" +
	"# 管理原则\n先对齐验收标准再派单\n" +
	"# 红线与升级\n对外承诺必须升级给人类\n" +
	"# 人类偏好\n先给结论，再给证据与理由"

func deptKey() ManualKey {
	return ManualKey{Scope: domain.MemoryScopeDepartment, DepartmentID: "dept_a"}
}

func TestManualProposeOnlyByDefault(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	m, err := svc.ProposeManual(ctx, deptKey(), manualV1, nil, assistant("asst_1"))
	if err != nil {
		t.Fatalf("提议: %v", err)
	}
	if m.Status != ManualStatusProposed {
		t.Fatalf("默认应 propose-only，得到 %s", m.Status)
	}
	if m.Version != 1 {
		t.Fatalf("版本应为 1，得到 %d", m.Version)
	}
	if _, ok, _ := svc.CurrentApprovedManual(ctx, deptKey()); ok {
		t.Fatal("proposed 不得被视为生效版本")
	}
}

func TestManualApproveSupersedesPrevious(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	v1, err := svc.ProposeManual(ctx, deptKey(), manualV1, nil, human("user_1"))
	if err != nil {
		t.Fatalf("提议 v1: %v", err)
	}
	approved, err := svc.ApproveManual(ctx, v1.ID, human("user_1"))
	if err != nil {
		t.Fatalf("批准 v1: %v", err)
	}
	if approved.Status != ManualStatusApproved {
		t.Fatalf("状态应为 approved: %s", approved.Status)
	}
	v2, err := svc.ProposeManual(ctx, deptKey(), manualV2, nil, human("user_1"))
	if err != nil {
		t.Fatalf("提议 v2: %v", err)
	}
	if v2.Version != 2 || v2.Diff == "" {
		t.Fatalf("v2 应有版本与 diff: %+v", v2)
	}
	if _, err := svc.ApproveManual(ctx, v2.ID, human("user_1")); err != nil {
		t.Fatalf("批准 v2: %v", err)
	}
	versions, _ := svc.ListManualVersions(ctx, deptKey())
	if len(versions) != 2 {
		t.Fatalf("应有 2 个版本: %d", len(versions))
	}
	if versions[0].Status != ManualStatusSuperseded {
		t.Fatalf("旧版应 superseded，得到 %s", versions[0].Status)
	}
	cur, ok, _ := svc.CurrentApprovedManual(ctx, deptKey())
	if !ok || cur.Version != 2 {
		t.Fatalf("生效版本应为 v2: %+v", cur)
	}
}

func TestManualApprovalRequiresHuman(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	v1, _ := svc.ProposeManual(ctx, deptKey(), manualV1, nil, assistant("asst_1"))
	if _, err := svc.ApproveManual(ctx, v1.ID, assistant("asst_1")); err == nil {
		t.Fatal("助理不得自行批准手册")
	}
	if _, err := svc.ApproveManual(ctx, v1.ID, supervisor("sup_1", "dept_a")); err == nil {
		t.Fatal("部门主管不得自行批准手册")
	}
}

func TestManualValidation(t *testing.T) {
	svc, _, _, _ := newTestService(t, func(c *Config) { c.ManualMaxChars = 120 })
	if _, err := svc.ProposeManual(ctx, deptKey(), "# 角色与目标\n只有角色\n", nil, human("user_1")); err == nil {
		t.Fatal("缺少红线章节应被拒绝")
	}
	long := "# 角色与目标\n" + strings.Repeat("很长的内容", 40) + "\n# 红线与升级\n升级"
	if _, err := svc.ProposeManual(ctx, deptKey(), long, nil, human("user_1")); err == nil {
		t.Fatal("超长手册应被拒绝")
	}
}

func TestManualRollbackCreatesNewVersion(t *testing.T) {
	svc, _, _, _ := newTestService(t, nil)
	v1, _ := svc.ProposeManual(ctx, deptKey(), manualV1, nil, human("user_1"))
	if _, err := svc.ApproveManual(ctx, v1.ID, human("user_1")); err != nil {
		t.Fatalf("批准 v1: %v", err)
	}
	v2, _ := svc.ProposeManual(ctx, deptKey(), manualV2, nil, human("user_1"))
	if _, err := svc.ApproveManual(ctx, v2.ID, human("user_1")); err != nil {
		t.Fatalf("批准 v2: %v", err)
	}
	rolled, err := svc.RollbackManual(ctx, deptKey(), 1, human("user_1"))
	if err != nil {
		t.Fatalf("回滚: %v", err)
	}
	if rolled.Version != 3 || rolled.Content != manualV1 {
		t.Fatalf("回滚应生成 v3 且内容等于 v1: %+v", rolled)
	}
	if rolled.Status != ManualStatusApproved {
		t.Fatalf("人类回滚应直接生效: %s", rolled.Status)
	}
	cur, ok, _ := svc.CurrentApprovedManual(ctx, deptKey())
	if !ok || cur.Version != 3 || cur.Content != manualV1 {
		t.Fatalf("生效版本应为回滚后的 v3: %+v", cur)
	}
}

func TestManualAutoLowRiskOnlyForSmallNonRedlineChanges(t *testing.T) {
	svc, _, _, _ := newTestService(t, func(c *Config) {
		c.ManualMode = ManualAutoLowRisk
		c.ManualAutoMaxDiff = 4
	})
	v1, _ := svc.ProposeManual(ctx, deptKey(), manualV1, nil, human("user_1"))
	if _, err := svc.ApproveManual(ctx, v1.ID, human("user_1")); err != nil {
		t.Fatalf("批准 v1: %v", err)
	}
	// 小改动、非红线 → 自动生效。
	if _, err := svc.ProposeManual(ctx, deptKey(), manualV2, nil, assistant("asst_1")); err != nil {
		t.Fatalf("提议 v2: %v", err)
	}
	versions, _ := svc.ListManualVersions(ctx, deptKey())
	if versions[len(versions)-1].Status != ManualStatusApproved {
		t.Fatalf("低风险小改动应自动生效，得到 %s", versions[len(versions)-1].Status)
	}
	// 动到红线 → 必须人类批准。
	risky := strings.Replace(manualV2, "对外承诺必须升级给人类", "对外承诺可自行决定，无需审批", 1)
	got, err := svc.ProposeManual(ctx, deptKey(), risky, nil, assistant("asst_1"))
	if err != nil {
		t.Fatalf("提议 risky: %v", err)
	}
	if got.Status != ManualStatusProposed {
		t.Fatalf("红线类改动必须 pending 人类批准，得到 %s", got.Status)
	}
}

func TestComposeSystemPrecedence(t *testing.T) {
	layers := ComposeSystem("ROLE_CARD", manualV1)
	if !strings.Contains(layers.Safety, "系统安全边界") {
		t.Fatal("安全层缺失")
	}
	if !strings.Contains(layers.ManualRedline, "对外承诺必须升级") {
		t.Fatalf("红线章节应单列: %q", layers.ManualRedline)
	}
	rendered := layers.Render()
	iSafety := strings.Index(rendered, "系统安全边界")
	iRedline := strings.Index(rendered, "对外承诺必须升级")
	iRole := strings.Index(rendered, "ROLE_CARD")
	iOther := strings.Index(rendered, "先给结论")
	if !(iSafety < iRedline && iRedline < iRole && iRole < iOther) {
		t.Fatalf("优先级顺序应为 安全<红线<角色卡<其它，实际 %d/%d/%d/%d", iSafety, iRedline, iRole, iOther)
	}
}

func TestManualDiffLines(t *testing.T) {
	lines := LineDiff(manualV1, manualV2)
	added, removed := CountDiff(lines)
	if added == 0 || removed == 0 {
		t.Fatalf("应同时有新增与删除行: +%d -%d", added, removed)
	}
	if !touchesRedline(LineDiff(manualV1, strings.Replace(manualV1, "对外承诺必须升级给人类", "对外承诺可自行决定", 1))) {
		t.Fatal("改动红线内容应被识别为高风险")
	}
}

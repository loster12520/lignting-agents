//go:build acceptance

package ac

import "testing"

// TestAC12SupervisorChecklistAndSampling 覆盖 requirements.md §14 AC-12「部门主管核对与人类抽检」。
// 依赖轮次：R4。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC12SupervisorChecklistAndSampling -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. seed task_1：crit_1 有证据 pass、crit_2 无证据、crit_3 needs_human；主管填核对表（FR-TASK-07）。
//  2. crit_2 未核对时 POST /api/tasks/{id}/g3 → 409 并返回缺失项；补齐证据后 G3 可提交（FR-APPR-01）。
//  3. G3 卡片展示核对表、成本、风险、受影响产出物版本；审计记录核对表版本且不可篡改（FR-TASK-07）。
//  4. 人类随机抽检 verdict_1 approve：查看证据与 dissent，标记有效/无效/证据不足；质量指标更新（FR-REV-05、FR-OBS-02）。
//  5. 夹具制造同一对 Agent 互审 ≥5 次且通过率 100%→出现人类抽检提示；不自动判定串谋（AC-12 步骤 6）。
//  6. 主管授权小任务验收时仍留核对表与审计（FR-APPR-01）。
func TestAC12SupervisorChecklistAndSampling(t *testing.T) {
	SkipSkeleton(t, "AC-12", "R4", "核对表阻止 G3；完整后 G3；抽检；异常通过率提示")
}

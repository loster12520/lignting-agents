//go:build acceptance

package ac

import "testing"

// TestAC03DeliveryLoop 覆盖 requirements.md §14 AC-03「需求交付闭环」。
// 依赖轮次：R4/R5。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC03DeliveryLoop -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. seed 后从 task_1 走状态机；断言非法转换 409、discussing 只能在有视角/轮次记录时进入（FR-TASK-01..03）。
//  2. 产品提交 PRD v1；研发提交实现 v1；测试提交 blocker 挑战单→研发返工 v2→旧裁决仍可查（FR-ART-01..03、FR-REV-01..03）。
//  3. 部门内讨论每人≤5 条、总≤15 条；主管裁决记录进任务线程（FR-TASK-08）。
//  4. 主管填验收核对表：crit_2 无证据时 POST /api/tasks/{id}/g3 必须 409；补证据后成功（FR-TASK-07、FR-APPR-01）。
//  5. G3 approved 后任务 done；写事件进入 file_timeline；审计无未记录审批（FR-WRITE-01、FR-OPS-06）。
//  6. T3 ≤1 个工作日（人工记录）；搜索 PRD/代码/测试/评审/决策均可命中（AC-06 交叉）。
func TestAC03DeliveryLoop(t *testing.T) {
	SkipSkeleton(t, "AC-03", "R4/R5", "PRD→任务→部门内讨论→实现→评审→返工→核对表→G3；写时间线")
}

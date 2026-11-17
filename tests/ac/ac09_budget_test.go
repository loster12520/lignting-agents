//go:build acceptance

package ac

import "testing"

// TestAC09BudgetAndPause 覆盖 requirements.md §14 AC-09「预算与暂停」。
// 依赖轮次：R8。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC09BudgetAndPause -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. 设置部门日预算；用 fixtures.Faults{BudgetForcePercent:80} 触发 80% 告警，100% 时 POST /api/tasks 新任务被 409（FR-OPS-05）。
//  2. 断言在途任务完成当前工具调用后暂停，不静默超支；成本按部门/任务归因（FR-OBS-03）。
//  3. 人类追加预算→留审计→恢复接新任务（FR-OPS-05）。
//  4. 全局紧急停止→所有 Agent 状态暂停；恢复后任务/消息/成本一致（FR-SEC-07）。
//  5. usage_ledger 抽样与模型调用一致；成本视图可下钻（FR-OBS-03）。
func TestAC09BudgetAndPause(t *testing.T) {
	SkipSkeleton(t, "AC-09", "R8", "80%/100%/追加/紧急停止/成本一致")
}

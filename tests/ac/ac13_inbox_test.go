//go:build acceptance

package ac

import "testing"

// TestAC13AttentionInboxApprovals 覆盖 requirements.md §14 AC-13「注意力、收件箱与审批」。
// 依赖轮次：R3/R8。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC13AttentionInboxApprovals -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. seed 四类通知（fyi/need_know/need_decide/blocking）；断言只有后两类实时打扰，其余进收件箱（FR-CHAT-06）。
//  2. 收件箱是持久对象，同一升级包只有一个条目；批量/稍后/授权可追溯（FR-ASST-06）。
//  3. 批量批准低风险审批成功；批量批准 appr_irreversible（dual_key）→409（FR-APPR-02）。
//  4. 审批 TTL=1 分钟：超时标记 blocked/提醒，不自动执行（FR-APPR-02）。
//  5. 人类 24h 无响应→部门降级只读/暂停接新任务+日报（FR-SEC-07/FR-OPS-07）。
//  6. 授权给助理（范围+到期）→到期自动回收并通知；所有操作留审计（FR-ASST-06）。
func TestAC13AttentionInboxApprovals(t *testing.T) {
	SkipSkeleton(t, "AC-13", "R3/R8", "四类通知；收件箱；TTL；人类沉默；授权到期；批量限制")
}

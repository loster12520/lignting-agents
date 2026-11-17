//go:build acceptance

package ac

import "testing"

// TestAC04CrossDepartmentDebate 覆盖 requirements.md §14 AC-04「跨部门挑战与决策」。
// 依赖轮次：R6。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC04CrossDepartmentDebate -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. seed 已含 review_open（blocker，带 evidence_ids）；断言挑战单字段齐全（FR-REV-01）。
//  2. 双方提交决策包（stance/现状/提案/影响/evidence_ids/替代方案）；每人≤5、总计≤15，超限 409（FR-DEB-02/03）。
//  3. R1 双方立场+回应、R2 双方反驳；stall = 连续两次 stance 相同且 evidence_ids 无新增→自动升级（FR-DEB-03）。
//  4. 助理主持但不投票；裁决人按配置（部门主管/人类）；生成决策记录，dissent 非空且可检索（FR-DEB-04、FR-DEC-01/02）。
//  5. 夹具注入是唯一触发方式，不依赖「自然发现」；新证据重开最多 1 次（FR-ESC-01）。
func TestAC04CrossDepartmentDebate(t *testing.T) {
	SkipSkeleton(t, "AC-04", "R6", "夹具注入接口挑战→决策包→R1/R2→stall→升级→决策记录")
}

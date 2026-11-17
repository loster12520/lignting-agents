//go:build acceptance

package ac

import "testing"

// TestAC05MemoryRecallAndCorrect 覆盖 requirements.md §14 AC-05「记忆回忆与纠正」。
// 依赖轮次：R7。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC05MemoryRecallAndCorrect -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. 新会话问「上次为什么没上 GraphQL？」→ 回答带决策/消息/产出物来源（FR-MEM-08）。
//  2. 修改人类偏好 mem_pref→新会话生效；旧版本可查（FR-MEM-07/09）。
//  3. 删除 mem_dept_private→墓碑保留、后续检索/注入不再命中（FR-MEM-09/10）。
//  4. 待审队列批准/拒绝/合并 mem_candidate；拒绝必须带理由（FR-MEM-04/09）。
//  5. 以其他部门员工身份读 dev 私有记忆→403 且不泄漏存在性（FR-MEM-10）。
//  6. 手册 v2 有 diff/回滚；高风险修改需人类批准（FR-MEM-07）。
func TestAC05MemoryRecallAndCorrect(t *testing.T) {
	SkipSkeleton(t, "AC-05", "R7", "检索回忆→编辑→删除→待审候选→越权拒绝")
}

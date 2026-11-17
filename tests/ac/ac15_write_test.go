//go:build acceptance

package ac

import "testing"

// TestAC15ConcurrentWriteTimeline 覆盖 requirements.md §14 AC-15「并发写入与变更时间线」。
// 依赖轮次：R5。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC15ConcurrentWriteTimeline -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. 两个 Agent 同时 write_file 同一文件→仅一个成功，另一个 rejected/排队；base_hash 冲突可恢复（FR-WRITE-01）。
//  2. 被拒 Agent 重新读/变基后再写成功；write_events 记录两次尝试（FR-WRITE-02）。
//  3. 两个 Agent 写不同文件→默认全局串行；配置按文件并行后可并行（FR-WRITE-01）。
//  4. GET /api/writes/timeline?path=... 查看文件/时间/Agent/diff/任务/分支/commit/原因（FR-WRITE-03）。
//  5. 35 次连续修改后每文件只保留最近 30 次（可配）；旧记录按 created_at 淘汰（FR-WRITE-03）。
//  6. 切换分支/提交→git_events 记录；commit 把多次 write_event 合并为 commit 组（FR-WRITE-04）。
//  7. bash 绕过 write_file 写同一文件→审批/标记「不受管变更」（FR-SEC-03）。
func TestAC15ConcurrentWriteTimeline(t *testing.T) {
	SkipSkeleton(t, "AC-15", "R5", "同文件并发/变基/不同文件串行/35→30/分支/commit/bash 绕过")
}

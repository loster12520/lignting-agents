//go:build acceptance

package ac

import "testing"

// TestAC10BackupExportRestore 覆盖 requirements.md §14 AC-10「备份、导出与恢复」。
// 依赖轮次：R8。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC10BackupExportRestore -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. seed 后 POST /api/admin/backup；记录时间/大小；在另一个临时数据目录恢复并启动真实二进制。
//  2. 断言消息/任务/产出/评审/决策/记忆/审计/文件时间线完整；搜索可用（FR-OPS-01）。
//  3. POST /api/admin/reindex；抽样一致；索引版本不兼容时提示重建（FR-SRCH-05）。
//  4. POST /api/admin/export→外部工具解析 JSON/Markdown；敏感字段脱敏（FR-OPS-02）。
//  5. 导入空工作区：schema 版本校验、冲突提示、不覆盖（FR-OPS-02）。
//  6. 恢复失败可回滚；备份/恢复/导出留审计（NFR-REL-05）。
func TestAC10BackupExportRestore(t *testing.T) {
	SkipSkeleton(t, "AC-10", "R8", "备份→另一目录恢复→索引重建→导出→导入")
}

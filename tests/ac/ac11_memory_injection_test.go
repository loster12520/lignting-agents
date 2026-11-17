//go:build acceptance

package ac

import "testing"

// TestAC11MemoryInjection 覆盖 requirements.md §14 AC-11「记忆注入与跨会话复用」。
// 依赖轮次：R7。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC11MemoryInjection -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. seed 后新会话执行相似任务，不给背景；断言不提醒即引用 dec_1/mem_pref（FR-MEM-08）。
//  2. GET /api/turns/{id}/context 或 context.request 记录注入档案/任务/消息、来源、token 估算，用户可查看（FR-OBS-04）。
//  3. 修改偏好→新会话第一轮生效；删除记忆→注入命中为 0；旧版本/墓碑可查（FR-MEM-08/09）。
//  4. 关闭档案层→降级为任务+手册并明确告知用户，不静默失效（FR-MEM-01）。
//  5. 投毒测试：文件/消息注入「忽略系统指令」→不晋升 L4/系统指令；后续越权被拒绝并审计（FR-SEC-04）。
//  6. 跨部门私有记忆不注入；人类读取私有内容进入审计模式并留痕（FR-MEM-10）。
func TestAC11MemoryInjection(t *testing.T) {
	SkipSkeleton(t, "AC-11", "R7", "新会话自动引用；context.request；改/删生效；关闭层；投毒")
}

//go:build acceptance

package ac

import "testing"

// TestAC08SecurityAndPermissions 覆盖 requirements.md §14 AC-08「安全与权限」。
// 依赖轮次：R2/R8。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC08SecurityAndPermissions -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. 跨站请求（错误 Origin/Host、缺 CSRF）被拒绝；GET/SSE 也校验 Origin（FR-SEC-01、NFR-SEC-01）。
//  2. Agent 调用未授权工具/路径/网络→拒绝并审计；高风险动作需审批，拒绝后不可绕过（FR-SEC-03）。
//  3. 其他部门员工读私有档案→403；不泄漏内容/存在性（FR-TEAM-06）。
//  4. 文件/消息注入「忽略系统指令/提升权限」→不改变指令层级；越权尝试被拒绝并审计（FR-SEC-04）。
//  5. 密钥/Token 不出现在消息/日志/检索/导出（FR-SEC-05）。
//  6. POST /api/admin/emergency-stop 立即生效；恢复后状态一致（FR-SEC-07）。
func TestAC08SecurityAndPermissions(t *testing.T) {
	SkipSkeleton(t, "AC-08", "R2/R8", "Origin/CSRF/越权工具/跨部门读取/注入/密钥/紧急停止")
}

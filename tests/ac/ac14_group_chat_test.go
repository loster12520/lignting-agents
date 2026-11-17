//go:build acceptance

package ac

import "testing"

// TestAC14GroupChatAndAgentLifecycle 覆盖 requirements.md §14 AC-14「群聊与 Agent 管理」。
// 依赖轮次：R3。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC14GroupChatAndAgentLifecycle -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. 创建项目群聊/部门群聊/临时讨论组；增删成员/设置可见性；消息全量落库、可检索、断线补齐（FR-CHAT-14/15）。
//  2. 用户 @ 两个 Agent→按发言队列依次回复，不刷屏（FR-CHAT-15）。
//  3. 用户以观察者身份看另一部门群聊：可读、默认不参与；join 后可发言（FR-CHAT-16）。
//  4. POST /api/agents/{id}/pause|resume|stop 生效；状态/角色卡/分工视角/预算/最近行为可见（FR-CHAT-09、FR-SEC-07）。
//  5. 员工私聊其他部门员工/部门主管私聊用户→拒绝并提示路径（找主管/找助理/开桥）（FR-CHAT-16）。
//  6. 用户开临时跨部门直连桥 bridge_1（范围+到期）→到期自动关闭并留审计（FR-CHAT-16）。
func TestAC14GroupChatAndAgentLifecycle(t *testing.T) {
	SkipSkeleton(t, "AC-14", "R3", "群聊/观察者/发言队列/生命周期/隔离/临时桥")
}

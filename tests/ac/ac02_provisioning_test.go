//go:build acceptance

package ac

import "testing"

// TestAC02DepartmentProvisioning 覆盖 requirements.md §14 AC-02「部门组建」。
// 依赖轮次：R3。
// 证据命令：go test -tags=acceptance ./tests/ac -run TestAC02DepartmentProvisioning -v
//
// 实现时删除 SkipSkeleton，并按以下步骤断言（前置/通过标准见 requirements §14）：
//  1. h := NewHarness(t); 助理生成蓝图→POST /api/blueprints；G1 未批准时 POST /api/provisioning 必须 409（FR-ASST-03/04）。
//  2. POST /api/blueprints/{id}/approve 后 provisioning；断言三部门、主管、员工、职责视角、部门群聊、任务板、记忆空间（FR-TEAM-01..05）。
//  3. 重复 POST /api/provisioning 返回同一幂等结果，不产生重复部门/Agent（FR-ASST-04）。
//  4. POST /api/departments/{id}/pause 后新任务被拒绝；resume 后状态完整（FR-TEAM-03）。
//  5. 失败注入（fixtures.Faults{KillBeforeCommit:true}）不留半支团队；审计含完整 provisioning 链（FR-SEC-06）。
//  6. T2 累计 ≤30 分钟：从 setup 到 G1+首任务 ready（人工计时）。
func TestAC02DepartmentProvisioning(t *testing.T) {
	SkipSkeleton(t, "AC-02", "R3", "蓝图 G1→provisioning→产品/研发/测试部门与群聊/任务板/记忆空间；幂等/回滚")
}

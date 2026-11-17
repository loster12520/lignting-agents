package fixtures

import (
	"context"
	"database/sql"
)

func seedOrg(ctx context.Context, tx *sql.Tx, now string, r *SeedResult) error {
	// Owner: single local user (api-contract.md §2).
	if err := exec(ctx, tx,
		"INSERT INTO owner(id, name, created_at) VALUES (?,?,?)",
		OwnerID, "阿哲", now); err != nil {
		return err
	}

	// Three delivery departments (requirements §6.4 / implementation-plan R3).
	depts := []struct {
		id, name, kind, mission string
		charter                 map[string]any
	}{
		{DeptProductID, "产品部门", "product", "需求澄清、范围与验收标准", map[string]any{
			"success":     "验收标准可测试、范围不蔓延",
			"perspective": "问题/用户/范围",
		}},
		{DeptDevID, "研发部门", "dev", "方案、实现、测试与交付", map[string]any{
			"success":     "实现可评审、证据可复现",
			"perspective": "接口/数据/实现",
		}},
		{DeptTestID, "测试部门", "test", "独立验证与质量门", map[string]any{
			"success":     "缺陷有证据、质量门不被静默绕过",
			"perspective": "测试/边界/证据",
		}},
	}
	for _, d := range depts {
		if err := exec(ctx, tx,
			"INSERT INTO departments(id, name, kind, mission, status, charter, created_at, updated_at) VALUES (?,?,?,?, 'active', ?, ?, ?)",
			d.id, d.name, d.kind, d.mission, MustJSON(d.charter), now, now); err != nil {
			return err
		}
		r.Departments[d.kind] = d.id
	}

	// Agents: assistant outside departments, one supervisor + employees per
	// department. Employees carry a perspective (requirements §6.1/§15.7).
	roleCard := func(mission string, nonGoals []string) string {
		return MustJSON(map[string]any{
			"mission":      mission,
			"non_goals":    nonGoals,
			"memory_scope": "department",
		})
	}
	agents := []struct {
		id, name, kind, dept, perspective string
		card                              string
		budget                            float64
	}{
		{AssistantID, "助理", "assistant", "", "幕僚/澄清/协调", roleCard("把模糊想法变成目标、部门与首任务；过滤注意力", []string{"不替项目主管批准预算", "不创建未经确认的部门"}), 0},
		{AgentProdLeadID, "产品主管", "supervisor", DeptProductID, "问题/用户/范围", roleCard("产品部门交付与质量门", []string{"不替研发做技术决策"}), 5},
		{AgentProdEmpID, "产品员工", "employee", DeptProductID, "问题/用户/范围", roleCard("PRD、用户故事、验收标准", []string{"不扩大范围"}), 5},
		{AgentDevLeadID, "研发主管", "supervisor", DeptDevID, "接口/数据/实现", roleCard("研发部门拆解、评审、对外代表", []string{"不豁免测试红线"}), 8},
		{AgentDevAID, "研发员工 A", "employee", DeptDevID, "接口", roleCard("接口设计与实现", []string{"不自批自己的产出"}), 6},
		{AgentDevBID, "研发员工 B", "employee", DeptDevID, "数据/实现", roleCard("数据流与实现", []string{"不发生静默文件覆盖"}), 6},
		{AgentTestLeadID, "测试主管", "supervisor", DeptTestID, "测试/质量门", roleCard("测试部门质量门与证据", []string{"不以无证据放行"}), 5},
		{AgentTestEmpID, "测试员工", "employee", DeptTestID, "测试/边界", roleCard("用例、复现与证据", []string{"不只做点一遍"}), 5},
	}
	for _, a := range agents {
		if err := exec(ctx, tx,
			"INSERT INTO agents(id, name, kind, department_id, role_card, perspective, status, model, budget_limit, created_at, updated_at) VALUES (?,?,?,?,?,?, 'idle', 'deepseek-flash', ?, ?, ?)",
			a.id, a.name, a.kind, a.dept, a.card, a.perspective, a.budget, now, now); err != nil {
			return err
		}
		r.Agents = append(r.Agents, a.id)
	}
	r.Supervisors["product"] = AgentProdLeadID
	r.Supervisors["dev"] = AgentDevLeadID
	r.Supervisors["test"] = AgentTestLeadID
	r.Employees["product"] = AgentProdEmpID
	r.Employees["dev_a"] = AgentDevAID
	r.Employees["dev_b"] = AgentDevBID
	r.Employees["test"] = AgentTestEmpID
	return seedOrgConversations(ctx, tx, now, r)
}

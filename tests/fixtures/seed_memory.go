package fixtures

import (
	"context"
	"database/sql"
)

// seedMemory inserts the four memory layers (requirements §9):
// human-only preference, org decision, employee candidate, department-private
// fact and one deleted (tombstoned) memory.
func seedMemory(ctx context.Context, tx *sql.Tx, now string, r *SeedResult) error {
	memories := []struct {
		key, scope, dept, agent, entityType, entityKey, content, summary, tags, confidence, evidence, sensitivity, visibility, status, deletedAt string
	}{
		{"pref", "assistant", "", AssistantID, "human", OwnerID,
			"人类偏好：默认中文注释；导出 CSV 用 UTF-8 BOM。", "中文注释 + BOM 偏好",
			"偏好,导出", "high", MustJSON([]string{"message:msg_single_question"}),
			"normal", "human-only", "active", ""},
		{"decision", "decision", "", "", "decision", DecisionAcceptedID,
			"导出 CSV 采用 UTF-8 BOM；GBK 方案被 supersede。", "导出编码决策",
			"决策,导出", "high", MustJSON([]string{"decision:dec_1"}),
			"normal", "org", "active", ""},
		{"candidate", "employee", DeptDevID, AgentDevAID, "task", TaskID,
			"导出大文件需流式处理，避免内存峰值。", "大文件流式导出",
			"性能,导出", "medium", MustJSON([]string{"command:go test ./..."}),
			"normal", "team", "candidate", ""},
		{"dept_private", "department", DeptDevID, "", "department", DeptDevID,
			"研发部门内部约定：接口字段命名 snake_case。", "接口命名约定",
			"接口,约定", "high", MustJSON([]string{"message:msg_task_comment"}),
			"normal", "team", "active", ""},
		{"deleted", "employee", DeptTestID, AgentTestEmpID, "employee", AgentTestEmpID,
			"测试环境临时密码（已删除）。", "已删除记忆",
			"临时", "low", MustJSON([]string{"message:msg_deleted_secret"}),
			"secret", "agent-private", "deleted", now},
	}
	for _, m := range memories {
		id := "mem_" + m.key
		if err := exec(ctx, tx,
			"INSERT INTO memories(id, scope, department_id, agent_id, entity_type, entity_key, content, summary, tags, confidence, evidence, sensitivity, visibility, valid_from, valid_to, supersedes, version, status, created_by, created_at, updated_at, deleted_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?, '', '', 1, ?, ?, ?, ?, ?)",
			id, m.scope, m.dept, m.agent, m.entityType, m.entityKey, m.content, m.summary, m.tags,
			m.confidence, m.evidence, m.sensitivity, m.visibility, now, m.status,
			AssistantID, now, now, m.deletedAt); err != nil {
			return err
		}
		r.Memories[m.key] = id
	}

	// Manager manual (L4, adaptive prompt), one active version with sections.
	manual := "# 角色与目标\n助理负责澄清、组部门、协调与注意力过滤。\n\n# 管理原则\n先 DoR 再派单；验收看证据。\n\n# 红线与升级规则\n不可逆动作必须人类审批。\n\n# 人类主管的偏好\n中文优先；输出给结论与来源。\n\n# 部门现状速记\n产品/研发/测试三部门，研发 WIP=2。"
	if err := exec(ctx, tx,
		"INSERT INTO prompt_manuals(id, scope, department_id, agent_id, version, content, status, diff, created_by, created_at) VALUES ('manual_1', 'assistant', '', ?, 1, ?, 'active', '', ?, ?)",
		AssistantID, manual, AssistantID, now); err != nil {
		return err
	}
	r.ManualID = "manual_1"
	return nil
}

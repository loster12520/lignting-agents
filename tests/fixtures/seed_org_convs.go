package fixtures

import (
	"context"
	"database/sql"
)

func seedOrgConversations(ctx context.Context, tx *sql.Tx, now string, r *SeedResult) error {
	// Conversations: assistant DM, project, per-department, cross-department,
	// task thread and decision log (api-contract.md §3, requirements §10.2).
	convs := []struct {
		id, typ, title, dept, visibility string
	}{
		{ConversationDMID, "dm", "与助理的私聊", "", "private"},
		{ConversationProjectID, "project", "Lignting 0.0.1 项目", "", "project"},
		{"conv_dept_product", "department", "产品部门", DeptProductID, "department"},
		{"conv_dept_dev", "department", "研发部门", DeptDevID, "department"},
		{"conv_dept_test", "department", "测试部门", DeptTestID, "department"},
		{ConversationCrossID, "cross_dept", "跨部门评审", "", "org"},
		{ConversationTaskID, "task_thread", "任务 task_1 讨论", DeptDevID, "department"},
		{ConversationLogID, "decision_log", "决策日志", "", "project"},
	}
	for _, c := range convs {
		if err := exec(ctx, tx,
			"INSERT INTO conversations(id, type, title, department_id, visibility, created_by, status, created_at, updated_at) VALUES (?,?,?,?,?,?, 'active', ?, ?)",
			c.id, c.typ, c.title, c.dept, c.visibility, OwnerID, now, now); err != nil {
			return err
		}
		r.Conversations[c.id] = c.id
	}

	// Members. Department channels keep department + assistant only
	// (requirements §6.6 isolation); project/log are shared.
	addMember := func(conv, ptype, pid, role string) error {
		return exec(ctx, tx,
			"INSERT INTO conversation_members(conversation_id, principal_type, principal_id, role, joined_at) VALUES (?,?,?,?,?)",
			conv, ptype, pid, role, now)
	}
	if err := addMember(ConversationDMID, "user", OwnerID, "owner"); err != nil {
		return err
	}
	if err := addMember(ConversationDMID, "agent", AssistantID, "assistant"); err != nil {
		return err
	}
	for _, conv := range []string{ConversationProjectID, ConversationLogID} {
		if err := addMember(conv, "user", OwnerID, "owner"); err != nil {
			return err
		}
		for _, a := range r.Agents {
			if err := addMember(conv, "agent", a, "member"); err != nil {
				return err
			}
		}
	}
	deptMembers := map[string][]string{
		"conv_dept_product": {OwnerID, AssistantID, AgentProdLeadID, AgentProdEmpID},
		"conv_dept_dev":     {OwnerID, AssistantID, AgentDevLeadID, AgentDevAID, AgentDevBID},
		"conv_dept_test":    {OwnerID, AssistantID, AgentTestLeadID, AgentTestEmpID},
	}
	for conv, members := range deptMembers {
		for _, m := range members {
			ptype := "agent"
			if m == OwnerID {
				ptype = "user"
			}
			if err := addMember(conv, ptype, m, "member"); err != nil {
				return err
			}
		}
	}
	crossMembers := []struct{ ptype, id string }{
		{"user", OwnerID}, {"agent", AssistantID},
		{"agent", AgentProdLeadID}, {"agent", AgentDevLeadID}, {"agent", AgentTestLeadID},
	}
	for _, m := range crossMembers {
		if err := addMember(ConversationCrossID, m.ptype, m.id, "member"); err != nil {
			return err
		}
	}
	taskMembers := []struct{ ptype, id string }{
		{"user", OwnerID}, {"agent", AgentDevLeadID}, {"agent", AgentDevAID},
		{"agent", AgentTestEmpID}, {"agent", AgentProdLeadID},
	}
	for _, m := range taskMembers {
		if err := addMember(ConversationTaskID, m.ptype, m.id, "member"); err != nil {
			return err
		}
	}

	// Messages. Stable inputs for AC-01/03/05/06/11/13.
	addMessage := func(key, conv, senderType, senderID, senderName, typ, content, meta string) error {
		id := "msg_" + key
		if err := exec(ctx, tx,
			"INSERT INTO messages(id, conversation_id, thread_id, sender_type, sender_id, sender_name, type, content, meta, created_at) VALUES (?,?,'',?,?,?,?,?,?,?)",
			id, conv, senderType, senderID, senderName, typ, content, meta, now); err != nil {
			return err
		}
		r.Messages[key] = id
		return nil
	}
	messages := []struct {
		key, conv, senderType, senderID, senderName, typ, content, meta string
	}{
		{"fuzzy_goal", ConversationDMID, "user", OwnerID, "阿哲", "text",
			"我想把现在的 CLI 工具做成一个有 Web 界面的产品，先做最小可用版。", "{}"},
		{"assistant_greeting", ConversationDMID, "agent", AssistantID, "助理", "text",
			"我是你的助理：可以澄清目标、组建部门、跟踪任务与记忆；不会替你批准预算或创建部门。随时可以暂停我。", "{}"},
		{"single_question", ConversationDMID, "user", OwnerID, "阿哲", "text",
			"这个功能大概需要几天？", "{}"},
		{"search_limit", ConversationProjectID, "user", OwnerID, "阿哲", "text",
			"登录限流采用令牌桶；部署前必须做中文 2 字检索验证（限流、部署、返工）。", "{}"},
		{"deleted_secret", "conv_dept_dev", "agent", AgentDevBID, "研发员工 B", "text",
			"临时部署密码是 123456，稍后删除。", "{}"},
		{"challenge_card", ConversationCrossID, "agent", AgentProdLeadID, "产品主管", "card",
			"接口字段缺少 UTF-8 BOM，Excel 导出会乱码。",
			MustJSON(map[string]any{
				"object_type": "review", "review_id": ReviewOpenID, "severity": "blocker",
				"artifact_id": ArtifactCodeID, "artifact_version": 1,
			})},
		{"decision_card", ConversationLogID, "agent", AssistantID, "助理", "decision",
			"决策：导出 CSV 采用 UTF-8 BOM；异议保留。",
			MustJSON(map[string]any{"decision_id": DecisionAcceptedID})},
		{"task_comment", ConversationTaskID, "user", OwnerID, "阿哲", "text",
			"错误信息必须可读；请把复现命令贴进证据。", "{}"},
	}
	for _, m := range messages {
		if err := addMessage(m.key, m.conv, m.senderType, m.senderID, m.senderName, m.typ, m.content, m.meta); err != nil {
			return err
		}
	}
	if err := exec(ctx, tx, "UPDATE messages SET deleted_at=? WHERE id=?", now, "msg_deleted_secret"); err != nil {
		return err
	}

	// Intent card + blueprint + provisioning projection (AC-01/AC-02).
	if err := exec(ctx, tx,
		"INSERT INTO intents(id, version, content, status, created_at) VALUES ('intent_1', 1, ?, 'confirmed', ?)",
		MustJSON(map[string]any{
			"goal":        "把 CLI 工具做成 Web 产品",
			"scope":       []string{"登录", "项目列表", "导出 CSV"},
			"non_goals":   []string{"移动端", "多用户"},
			"constraints": []string{"本地优先", "deepseek-flash"},
		}), now); err != nil {
		return err
	}
	if err := exec(ctx, tx,
		"INSERT INTO blueprints(id, intent_id, content, status, created_at, approved_at) VALUES ('bp_1', 'intent_1', ?, 'approved', ?, ?)",
		MustJSON(map[string]any{
			"departments": []string{"产品", "研发", "测试"},
			"charter":     "三部门交付；G1 一次批准蓝图+章程",
			"budget_cny":  20,
		}), now, now); err != nil {
		return err
	}
	if err := exec(ctx, tx,
		"INSERT INTO provisioning(id, blueprint_id, status, steps, created_at, updated_at) VALUES ('prov_1', 'bp_1', 'done', ?, ?, ?)",
		MustJSON([]string{"departments", "agents", "channels", "task_board", "memory", "approvals"}), now, now); err != nil {
		return err
	}
	return nil
}

package fixtures

import (
	"context"
	"database/sql"
	"time"
)

func seedWork(ctx context.Context, tx *sql.Tx, now string, r *SeedResult) error {
	// Tasks: task_1 is the delivery closed loop target (approved, waiting G3,
	// three criteria with mixed evidence); task_2 is in progress for WIP/budget
	// scenarios.
	tasks := []struct {
		id, title, desc, status, dept, owner, reviewer, approver, perspective, priority string
		budgetTokens                                                                    int64
		budgetCost                                                                      float64
	}{
		{TaskID, "增加导出 CSV", "把项目列表导出为 CSV，字段完整、UTF-8 BOM、错误可读", "approved",
			DeptDevID, AgentDevAID, AgentTestEmpID, OwnerID, "接口", "P1", 200000, 3.5},
		{Task2ID, "登录限流", "令牌桶限流，中文错误文案", "in_progress",
			DeptDevID, AgentDevBID, AgentTestEmpID, AgentDevLeadID, "数据/实现", "P2", 120000, 2.0},
	}
	for _, t := range tasks {
		if err := exec(ctx, tx,
			"INSERT INTO tasks(id, title, description, status, department_id, owner_id, reviewer_id, approver_id, perspective, priority, budget_tokens, budget_cost, deadline, dor, dod, created_by, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?, '', ?, '{}', ?, ?, ?)",
			t.id, t.title, t.desc, t.status, t.dept, t.owner, t.reviewer, t.approver, t.perspective, t.priority,
			t.budgetTokens, t.budgetCost,
			MustJSON(map[string]any{"ready": true, "criteria": 3, "deps": []string{}}),
			AssistantID, now, now); err != nil {
			return err
		}
	}
	r.TaskID = TaskID
	r.Task2ID = Task2ID

	// Acceptance criteria: crit_1 has evidence + supervisor pass; crit_2 is
	// missing evidence; crit_3 needs human judgement. This is the AC-12 setup.
	criteria := []struct {
		id, task, text, evidence, supervisor string
		ord                                  int
	}{
		{"crit_1", TaskID, "导出字段完整（id/title/status/owner）", MustJSON([]string{"command:go test ./internal/core -run TestExport"}), "pass", 1},
		{"crit_2", TaskID, "10 万行导出 p95 < 5s", "[]", "pending", 2},
		{"crit_3", TaskID, "错误信息可读且不含内部堆栈", MustJSON([]string{"message:msg_task_comment"}), "needs_human", 3},
	}
	for _, c := range criteria {
		if err := exec(ctx, tx,
			"INSERT INTO task_criteria(id, task_id, ord, text, evidence, supervisor_verdict, human_decision, created_at, updated_at) VALUES (?,?,?,?,?,?, 'pending', ?, ?)",
			c.id, c.task, c.ord, c.text, c.evidence, c.supervisor, now, now); err != nil {
			return err
		}
		r.CriteriaIDs = append(r.CriteriaIDs, c.id)
	}

	// Task event hash chain (implementation-plan.md §2.2: prev_hash/hash,
	// append-only). The chain is intentionally simple and independently
	// recomputable in tests.
	events := []struct {
		typ, from, to, actor, reason string
	}{
		{"created", "", "proposed", AssistantID, "从消息创建任务"},
		{"transition", "proposed", "ready", AgentProdLeadID, "DoR 补齐"},
		{"transition", "ready", "discussing", AgentDevLeadID, "接口字段讨论"},
		{"transition", "discussing", "in_progress", AgentDevLeadID, "主管裁决后开工"},
		{"transition", "in_progress", "in_review", AgentDevAID, "提交实现 v1"},
		{"transition", "in_review", "approved", AgentTestLeadID, "独立评审通过"},
	}
	prev := ""
	for i, e := range events {
		payload := map[string]any{
			"task_id": TaskID, "type": e.typ, "from": e.from, "to": e.to,
			"actor_id": e.actor, "reason": e.reason, "created_at": now, "ord": i,
		}
		h := ChainHash(prev, payload)
		if err := exec(ctx, tx,
			"INSERT INTO task_events(task_id, type, from_status, to_status, actor_type, actor_id, reason, evidence, prev_hash, hash, created_at) VALUES (?,?,?,?, 'agent', ?,?, '[]', ?, ?, ?)",
			TaskID, e.typ, e.from, e.to, e.actor, e.reason, prev, h, now); err != nil {
			return err
		}
		prev = h
	}

	// Artifacts + immutable versions (requirements §8.4).
	artifacts := []struct {
		id, typ, title, task, dept, status, author, versionID, content string
	}{
		{"art_prd", "prd", "导出 CSV PRD", TaskID, DeptProductID, "approved", AgentProdEmpID,
			"art_prd_v1", "# 导出 CSV\n\n## 验收标准\n1. 字段完整\n2. 10 万行 < 5s\n3. 错误可读"},
		{"art_code", "code", "导出 CSV 实现", TaskID, DeptDevID, "in_review", AgentDevAID,
			"art_code_v1", "diff --git a/internal/core/export.go b/internal/core/export.go\n+func ExportCSV(...) error"},
	}
	for _, a := range artifacts {
		if err := exec(ctx, tx,
			"INSERT INTO artifacts(id, type, title, task_id, department_id, status, created_by, created_at) VALUES (?,?,?,?,?,?,?,?)",
			a.id, a.typ, a.title, a.task, a.dept, a.status, a.author, now); err != nil {
			return err
		}
		hash := ContentHash(a.content)
		if err := exec(ctx, tx,
			"INSERT INTO artifact_versions(id, artifact_id, version, content, content_hash, storage_path, author_id, created_at) VALUES (?,?,1,?,?,?,?,?)",
			a.versionID, a.id, a.content, hash, "artifacts/"+hash+".txt", a.author, now); err != nil {
			return err
		}
		if a.id == "art_prd" {
			r.ArtifactPRDID, r.ArtifactPRDVersion = a.id, a.versionID
		} else {
			r.ArtifactCodeID, r.ArtifactCodeVersion = a.id, a.versionID
		}
	}

	// Reviews: one open blocker (跨部门挑战) and one resolved with an approve
	// verdict (AC-12 抽检 object). Reviewer != author in both.
	if err := exec(ctx, tx,
		"INSERT INTO reviews(id, from_department, to_department, artifact_id, artifact_version_id, severity, stance, claim, evidence, impact, proposed_resolution, owner_id, due_at, status, thread_conversation_id, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		ReviewOpenID, DeptTestID, DeptDevID, ArtifactCodeID, "art_code_v1", "blocker",
		"导出必须带 UTF-8 BOM", "缺少 BOM 会导致 Excel 打开乱码",
		MustJSON([]string{"artifact:art_code@1", "command:go test ./...", "message:msg_task_comment"}),
		"Windows Excel 用户无法直接打开导出文件",
		"在 CSV 头部写入 UTF-8 BOM，并新增回归用例",
		AgentDevAID, time.Now().UTC().Add(24*time.Hour).Format(time.RFC3339), "open",
		ConversationCrossID, now); err != nil {
		return err
	}
	if err := exec(ctx, tx,
		"INSERT INTO reviews(id, from_department, to_department, artifact_id, artifact_version_id, severity, stance, claim, evidence, impact, proposed_resolution, owner_id, due_at, status, thread_conversation_id, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		ReviewResolvedID, DeptProductID, DeptDevID, ArtifactPRDID, "art_prd_v1", "major",
		"PRD 验收标准需可测试", "部分验收标准缺少阈值",
		MustJSON([]string{"artifact:art_prd@1"}),
		"测试无法判定通过与否", "为每条标准补阈值", AgentProdEmpID, now, "resolved",
		ConversationCrossID, now); err != nil {
		return err
	}
	if err := exec(ctx, tx,
		"INSERT INTO review_verdicts(id, review_id, verdict, checked, rationale, conditions, residual_risk, reviewer_id, decision_id, created_at) VALUES (?,?,?,?,?, '[]', ?,?,?,?)",
		"verdict_1", ReviewResolvedID, "approve",
		"逐条检查了 PRD 范围、验收标准与证据引用", "阈值已补齐，作者非评审人",
		"中文文案质量仍需人工抽检", AgentTestLeadID, DecisionAcceptedID, now); err != nil {
		return err
	}
	r.ReviewOpenID = ReviewOpenID
	r.ReviewResolvedID = ReviewResolvedID
	r.VerdictApproveID = "verdict_1"

	// Decisions: dec_0 superseded by dec_1 (AC-04/AC-06).
	if err := exec(ctx, tx,
		"INSERT INTO decisions(id, title, status, context, options, decision, rationale, evidence, dissent, deciders, affected_tasks, affected_artifacts, revisit_trigger, superseded_by, human_ack, created_by, created_at) VALUES (?,?,?,?,?,?,?,?, '[]', ?,?,?,?,?,?,?,?)",
		DecisionSupersededID, "导出 CSV 采用 GBK", "superseded",
		"Windows 用户默认编码", MustJSON([]string{"GBK", "UTF-8 BOM"}), "采用 GBK",
		"兼容旧版 Excel", MustJSON([]string{"message:msg_search_limit"}),
		MustJSON([]string{"owner"}), MustJSON([]string{TaskID}), MustJSON([]string{ArtifactCodeID}),
		"出现跨平台乱码时重评", DecisionAcceptedID, "approved", AssistantID, now); err != nil {
		return err
	}
	if err := exec(ctx, tx,
		"INSERT INTO decisions(id, title, status, context, options, decision, rationale, evidence, dissent, deciders, affected_tasks, affected_artifacts, revisit_trigger, supersedes, human_ack, created_by, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		DecisionAcceptedID, "导出 CSV 采用 UTF-8 BOM", "accepted",
		"Windows Excel 打开 UTF-8 无 BOM 的 CSV 会乱码",
		MustJSON([]string{"GBK", "UTF-8 BOM", "UTF-8 无 BOM"}), "采用 UTF-8 BOM",
		"兼容 Windows/macOS/Linux 且可被 Excel 正确识别",
		MustJSON([]string{"artifact:art_prd@1", "command:go test ./..."}),
		MustJSON([]string{"测试担心 BOM 影响其他工具解析"}),
		MustJSON([]string{"owner"}), MustJSON([]string{TaskID}), MustJSON([]string{ArtifactCodeID}),
		"首版后评估 BOM 对下游工具的影响", DecisionSupersededID, "approved", AssistantID, now); err != nil {
		return err
	}
	r.DecisionAcceptedID = DecisionAcceptedID
	r.DecisionSupersededID = DecisionSupersededID

	// Approvals: G1 approved, G3 pending (missing evidence), irreversible
	// pending with dual-key payload.
	approvals := []struct {
		id, kind, title, payload, risk, status, requestedBy, decidedBy, reason, scope, expiresAt, decidedAt string
	}{
		{ApprovalG1ID, "G1", "三部门蓝图与章程", MustJSON(map[string]any{
			"blueprint_id": "bp_1", "departments": []string{"产品", "研发", "测试"},
		}), "medium", "approved", AssistantID, OwnerID, "批准", "once", "", now},
		{ApprovalG3ID, "G3", "导出 CSV 最终验收", MustJSON(map[string]any{
			"task_id": TaskID, "checklist": "pending", "criteria": []string{"crit_1", "crit_2", "crit_3"},
		}), "medium", "pending", AgentDevLeadID, "", "", "once", "", ""},
		{ApprovalIrreversibleID, "irreversible", "force push origin/main", MustJSON(map[string]any{
			"action": "force_push", "target": "origin/main", "dual_key": true,
		}), "high", "pending", AgentDevAID, "", "", "once", time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339), ""},
	}
	for _, a := range approvals {
		if err := exec(ctx, tx,
			"INSERT INTO approvals(id, kind, title, payload, risk, status, requested_by, decided_by, decision_reason, scope, expires_at, created_at, decided_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
			a.id, a.kind, a.title, a.payload, a.risk, a.status, a.requestedBy, a.decidedBy, a.reason, a.scope, a.expiresAt, now, a.decidedAt); err != nil {
			return err
		}
	}
	r.ApprovalG1ID, r.ApprovalG3ID, r.ApprovalIrreversibleID = ApprovalG1ID, ApprovalG3ID, ApprovalIrreversibleID
	return nil
}

package fixtures

import (
	"context"
	"database/sql"
	"time"
)

// seedRuntime inserts write events + timeline, git events, audit hash chain,
// usage, turns/context snapshots, notifications and one expiring bridge.
func seedRuntime(ctx context.Context, tx *sql.Tx, now string, timelineEntries int, r *SeedResult) error {
	if timelineEntries <= 0 {
		timelineEntries = 35
	}
	oldMain := "package main\n\nfunc main() {}\n"
	newMain := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"lignting\") }\n"
	exportBody := "package core\n\nfunc ExportCSV() error { return nil }\n"

	writeEvents := []struct {
		id, agent, task, path, base, content, diff, reason, branch, status, errText, commit string
	}{
		{"write_applied", AgentDevAID, TaskID, TimelinePath, ContentHash(oldMain), newMain,
			"@@ -1,3 +1,6 @@\n-package main\n+package main\n+import \"fmt\"", "实现导出入口", "main", "applied", "", "abc123"},
		{"write_conflict", AgentDevBID, TaskID, TimelinePath, ContentHash("stale base"), "conflicting",
			"", "并发写同一文件", "main", "rejected", "base_hash 不匹配：文件已被写入", ""},
		{"write_other", AgentDevAID, TaskID, "internal/core/export.go", ContentHash(""), exportBody,
			"@@ -0,0 +1,3 @@\n+package core", "新增导出实现", "main", "applied", "", "abc123"},
	}
	for _, w := range writeEvents {
		appliedAt := ""
		if w.status == "applied" {
			appliedAt = now
		}
		if err := exec(ctx, tx,
			"INSERT INTO write_events(id, agent_id, task_id, path, base_hash, content, diff, reason, branch, status, error, applied_at, commit_sha, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			w.id, w.agent, w.task, w.path, w.base, w.content, w.diff, w.reason, w.branch, w.status, w.errText, appliedAt, w.commit, now); err != nil {
			return err
		}
	}
	r.WriteAppliedID, r.WriteConflictID, r.WriteOtherID = "write_applied", "write_conflict", "write_other"

	// Two applied timeline entries plus a long history for trim tests.
	timeline := []struct{ id, path, writeID, agent, status string }{
		{"tl_1", TimelinePath, "write_applied", AgentDevAID, "applied"},
		{"tl_2", "internal/core/export.go", "write_other", AgentDevAID, "applied"},
	}
	for _, t := range timeline {
		if err := exec(ctx, tx,
			"INSERT INTO file_timeline(id, path, write_event_id, agent_id, task_id, diff, reason, branch, commit_sha, status, created_at) VALUES (?,?,?,?,?, '', '实现', 'main', 'abc123', ?, ?)",
			t.id, t.path, t.writeID, t.agent, TaskID, t.status, now); err != nil {
			return err
		}
		r.TimelineEntryIDs = append(r.TimelineEntryIDs, t.id)
	}
	base := time.Now().UTC().Add(-time.Duration(timelineEntries) * time.Minute)
	for i := 0; i < timelineEntries; i++ {
		id := "tl_hist_" + twoDigits(i)
		created := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		if err := exec(ctx, tx,
			"INSERT INTO file_timeline(id, path, write_event_id, agent_id, task_id, diff, reason, branch, commit_sha, status, created_at) VALUES (?,?,?,?,?, '', '历史修改', 'main', '', 'applied', ?)",
			id, TimelinePath, "write_hist_"+twoDigits(i), AgentDevAID, TaskID, created); err != nil {
			return err
		}
		r.TimelineEntryIDs = append(r.TimelineEntryIDs, id)
	}

	// Git events + commit group.
	if err := exec(ctx, tx,
		"INSERT INTO git_events(type, branch, commit_sha, message, created_at) VALUES ('branch', 'main', '', 'checkout main', ?)", now); err != nil {
		return err
	}
	if err := exec(ctx, tx,
		"INSERT INTO git_events(type, branch, commit_sha, message, created_at) VALUES ('commit', 'main', 'abc123', 'feat: export csv', ?)", now); err != nil {
		return err
	}
	if err := exec(ctx, tx,
		"INSERT INTO commit_groups(id, commit_sha, write_event_ids, created_at) VALUES ('cg_1', 'abc123', ?, ?)",
		MustJSON([]string{"write_applied", "write_other"}), now); err != nil {
		return err
	}
	r.GitCommitSHA = "abc123"

	// Usage ledger (AC-09 cost attribution).
	usage := []struct {
		id, agent, model           string
		prompt, completion, cached int64
		cost                       float64
	}{
		{"usage_1", AgentDevAID, "deepseek-flash", 12000, 3000, 1000, 0.08},
		{"usage_2", AssistantID, "deepseek-flash", 4000, 800, 0, 0.02},
	}
	for _, u := range usage {
		if err := exec(ctx, tx,
			"INSERT INTO usage_ledger(id, agent_id, task_id, conversation_id, model, prompt_tokens, completion_tokens, cached_tokens, cost, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
			u.id, u.agent, TaskID, ConversationTaskID, u.model, u.prompt, u.completion, u.cached, u.cost, now); err != nil {
			return err
		}
	}
	r.UsageID = "usage_1"

	// Turns: one completed, one interrupted with unknown tool outcome (AC-07).
	turns := []struct {
		id, agent, status, errText, endedAt string
	}{
		{"turn_done", AgentDevAID, "completed", "", now},
		{"turn_interrupted", AgentDevBID, "failed", "TOOL_OUTCOME_UNKNOWN: write_file call_id=call_1", now},
	}
	for _, t := range turns {
		if err := exec(ctx, tx,
			"INSERT INTO turns(id, agent_id, conversation_id, status, error, started_at, ended_at) VALUES (?,?,?,?,?,?,?)",
			t.id, t.agent, ConversationTaskID, t.status, t.errText, now, t.endedAt); err != nil {
			return err
		}
	}
	r.TurnCompletedID, r.TurnInterruptedID = "turn_done", "turn_interrupted"
	if err := exec(ctx, tx,
		"INSERT INTO context_snapshots(id, turn_id, agent_id, conversation_id, payload, created_at) VALUES ('ctx_1', 'turn_done', ?, ?, ?, ?)",
		AgentDevAID, ConversationTaskID,
		MustJSON(map[string]any{
			"model_mode": "mock",
			"memories":   []string{"mem_pref", "mem_decision"},
			"task_id":    TaskID,
			"tokens":     map[string]int{"system": 800, "task": 1200, "memory": 600, "session": 900},
		}), now); err != nil {
		return err
	}

	// Notifications: one per attention level (AC-13).
	notifs := []struct{ key, level, title string }{
		{"fyi", "fyi", "本周记忆摘要已生成"},
		{"need_know", "need_know", "研发部门完成导出实现"},
		{"need_decide", "need_decide", "需要决定：导出编码选项"},
		{"blocking", "blocking", "阻塞：G3 缺少 crit_2 证据"},
	}
	for _, n := range notifs {
		id := "notif_" + n.key
		if err := exec(ctx, tx,
			"INSERT INTO notifications(id, level, title, body, conversation_id, object_type, object_id, status, created_at) VALUES (?,?,?, '', ?, 'task', ?, 'unread', ?)",
			id, n.level, n.title, ConversationTaskID, TaskID, now); err != nil {
			return err
		}
		r.NotificationIDs[n.key] = id
	}

	// Expiring cross-department bridge (AC-14).
	if err := exec(ctx, tx,
		"INSERT INTO bridges(id, scope, from_type, from_id, to_type, to_id, expires_at, status, created_at) VALUES ('bridge_1', 'cross_dept', 'agent', ?, 'agent', ?, ?, 'active', ?)",
		AgentDevAID, AgentTestEmpID, time.Now().UTC().Add(time.Hour).Format(time.RFC3339), now); err != nil {
		return err
	}
	r.BridgeExpiringID = "bridge_1"
	return nil
}

func twoDigits(i int) string {
	if i < 10 {
		return "0" + itoa(i)
	}
	return itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

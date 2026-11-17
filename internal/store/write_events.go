package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// 共享写工具 / 文件时间线 / Git 事件仓储（R5，由 internal/write、internal/gitwatch
// 的窄端口结构化调用）。时间统一用 store.Now() 的固定 9 位 RFC3339，可按字符串排序。

const writeEventColumns = "id, agent_id, task_id, path, base_hash, content, diff, reason, branch, status, error, applied_at, commit_sha, created_at"
const timelineColumns = "id, path, write_event_id, agent_id, task_id, diff, reason, branch, commit_sha, status, created_at"

func normalizeWriteEvent(e *domain.WriteEvent) {
	if e.ID == "" {
		e.ID = domain.NewID("write")
	}
	if e.Status == "" {
		e.Status = domain.WritePending
	}
	if e.CreatedAt == "" {
		e.CreatedAt = Now()
	}
}

// InsertWriteEvent 插入写事件（pending 表示等待消费）。
func (s *Store) InsertWriteEvent(ctx context.Context, e domain.WriteEvent) error {
	normalizeWriteEvent(&e)
	if strings.TrimSpace(e.AgentID) == "" || strings.TrimSpace(e.Path) == "" || e.BaseHash == "" {
		return fmt.Errorf("%w: write event requires agent_id/path/base_hash", ErrInvalid)
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO write_events("+writeEventColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		e.ID, e.AgentID, e.TaskID, e.Path, e.BaseHash, e.Content, e.Diff, e.Reason, e.Branch,
		e.Status, e.Error, e.AppliedAt, e.CommitSHA, e.CreatedAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("%w: write event %s", ErrConflict, e.ID)
		}
		return fmt.Errorf("insert write event: %w", err)
	}
	return nil
}

// UpdateWriteEvent 更新写事件状态/结果（applied/rejected/unknown）。
func (s *Store) UpdateWriteEvent(ctx context.Context, e domain.WriteEvent) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE write_events SET agent_id=?, task_id=?, path=?, base_hash=?, content=?, diff=?, reason=?, branch=?, status=?, error=?, applied_at=?, commit_sha=? WHERE id=?",
		e.AgentID, e.TaskID, e.Path, e.BaseHash, e.Content, e.Diff, e.Reason, e.Branch,
		e.Status, e.Error, e.AppliedAt, e.CommitSHA, e.ID)
	if err != nil {
		return fmt.Errorf("update write event: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: write event %s", ErrNotFound, e.ID)
	}
	return nil
}

// GetWriteEvent 按 ID 读取写事件。
func (s *Store) GetWriteEvent(ctx context.Context, id string) (domain.WriteEvent, error) {
	var e domain.WriteEvent
	err := s.DB.QueryRowContext(ctx, "SELECT "+writeEventColumns+" FROM write_events WHERE id=?", id).
		Scan(&e.ID, &e.AgentID, &e.TaskID, &e.Path, &e.BaseHash, &e.Content, &e.Diff, &e.Reason,
			&e.Branch, &e.Status, &e.Error, &e.AppliedAt, &e.CommitSHA, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WriteEvent{}, fmt.Errorf("%w: write event %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.WriteEvent{}, fmt.Errorf("get write event: %w", err)
	}
	return e, nil
}

// ListWriteEventsByStatus 按状态列出写事件（created_at 升序，便于按提交顺序消费）。
func (s *Store) ListWriteEventsByStatus(ctx context.Context, status string, limit int) ([]domain.WriteEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT "+writeEventColumns+" FROM write_events WHERE status=? ORDER BY created_at ASC LIMIT ?", status, limit)
	if err != nil {
		return nil, fmt.Errorf("list write events: %w", err)
	}
	defer rows.Close()
	out := make([]domain.WriteEvent, 0, limit)
	for rows.Next() {
		var e domain.WriteEvent
		if err := rows.Scan(&e.ID, &e.AgentID, &e.TaskID, &e.Path, &e.BaseHash, &e.Content, &e.Diff,
			&e.Reason, &e.Branch, &e.Status, &e.Error, &e.AppliedAt, &e.CommitSHA, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan write event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AppendTimeline 追加一条文件变更时间线记录。
func (s *Store) AppendTimeline(ctx context.Context, e domain.FileTimelineEntry) error {
	if e.ID == "" {
		e.ID = domain.NewID("tl")
	}
	if strings.TrimSpace(e.Path) == "" || strings.TrimSpace(e.WriteEventID) == "" {
		return fmt.Errorf("%w: timeline requires path/write_event_id", ErrInvalid)
	}
	if e.Status == "" {
		e.Status = "applied"
	}
	if e.CreatedAt == "" {
		e.CreatedAt = Now()
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO file_timeline("+timelineColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?)",
		e.ID, e.Path, e.WriteEventID, e.AgentID, e.TaskID, e.Diff, e.Reason, e.Branch, e.CommitSHA, e.Status, e.CreatedAt)
	if err != nil {
		return fmt.Errorf("append timeline: %w", err)
	}
	return nil
}

// TrimTimeline 每文件只保留最近 keep 条，返回删除条数。
func (s *Store) TrimTimeline(ctx context.Context, path string, keep int) (int, error) {
	if strings.TrimSpace(path) == "" {
		return 0, fmt.Errorf("%w: path required", ErrInvalid)
	}
	if keep < 0 {
		return 0, fmt.Errorf("%w: keep must be >= 0", ErrInvalid)
	}
	res, err := s.DB.ExecContext(ctx,
		"DELETE FROM file_timeline WHERE id IN (SELECT id FROM file_timeline WHERE path=? ORDER BY created_at DESC, id DESC LIMIT -1 OFFSET ?)",
		path, keep)
	if err != nil {
		return 0, fmt.Errorf("trim timeline: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ListTimeline 按文件/分支/提交/Agent/起始时间过滤时间线（created_at 倒序）。
func (s *Store) ListTimeline(ctx context.Context, path, branch, commitSHA, agentID, since string, limit int) ([]domain.FileTimelineEntry, error) {
	where := []string{"1=1"}
	args := make([]any, 0, 6)
	if path != "" {
		where = append(where, "path=?")
		args = append(args, path)
	}
	if branch != "" {
		where = append(where, "branch=?")
		args = append(args, branch)
	}
	if commitSHA != "" {
		where = append(where, "commit_sha=?")
		args = append(args, commitSHA)
	}
	if agentID != "" {
		where = append(where, "agent_id=?")
		args = append(args, agentID)
	}
	if since != "" {
		where = append(where, "created_at >= ?")
		args = append(args, since)
	}
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, "SELECT "+timelineColumns+" FROM file_timeline WHERE "+strings.Join(where, " AND ")+" ORDER BY created_at DESC, id DESC LIMIT ?", args...)
	if err != nil {
		return nil, fmt.Errorf("list timeline: %w", err)
	}
	defer rows.Close()
	out := make([]domain.FileTimelineEntry, 0, limit)
	for rows.Next() {
		var e domain.FileTimelineEntry
		if err := rows.Scan(&e.ID, &e.Path, &e.WriteEventID, &e.AgentID, &e.TaskID, &e.Diff,
			&e.Reason, &e.Branch, &e.CommitSHA, &e.Status, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan timeline: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TimelineHasEvent 判断某写事件是否已进入时间线（幂等消费用）。
func (s *Store) TimelineHasEvent(ctx context.Context, writeEventID string) (bool, error) {
	if writeEventID == "" {
		return false, nil
	}
	var one int
	err := s.DB.QueryRowContext(ctx, "SELECT 1 FROM file_timeline WHERE write_event_id=? LIMIT 1", writeEventID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("timeline has event: %w", err)
	}
	return true, nil
}

// RecordAudit 是 write / gitwatch 端口的审计入口（内部走统一 hash chain）。
func (s *Store) RecordAudit(ctx context.Context, actorType, actorID, action, objectType, objectID, result, detail string) error {
	_, err := s.AppendAudit(ctx, domain.AuditEntry{
		ActorType: actorType, ActorID: actorID, Action: action,
		ObjectType: objectType, ObjectID: objectID, Result: result, Detail: detail,
	})
	return err
}

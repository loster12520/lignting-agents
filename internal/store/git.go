package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

const lastGitCommitKey = "git.last_observed_commit"

// LastObservedGitCommit 返回上次观察到的 commit（gitwatch 基线）。
func (s *Store) LastObservedGitCommit(ctx context.Context) (string, error) {
	v, _, err := s.Setting(ctx, lastGitCommitKey)
	return v, err
}

// SetLastObservedGitCommit 保存上次观察到的 commit。
func (s *Store) SetLastObservedGitCommit(ctx context.Context, commitSHA string) error {
	return s.SetSetting(ctx, lastGitCommitKey, commitSHA)
}

// RecordGitEvent 记录一次 git 事件（commit/branch 切换）。
func (s *Store) RecordGitEvent(ctx context.Context, eventType, branch, commitSHA, message, createdAt string) error {
	if strings.TrimSpace(eventType) == "" {
		return fmt.Errorf("%w: git event type required", ErrInvalid)
	}
	if createdAt == "" {
		createdAt = Now()
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO git_events(type, branch, commit_sha, message, created_at) VALUES(?,?,?,?,?)",
		eventType, branch, commitSHA, message, createdAt); err != nil {
		return fmt.Errorf("record git event: %w", err)
	}
	return nil
}

// ListUncommittedWriteEvents 返回已应用但尚未提交的写事件（commit 组用）。
func (s *Store) ListUncommittedWriteEvents(ctx context.Context, limit int) ([]domain.WriteEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT "+writeEventColumns+" FROM write_events WHERE status=? AND commit_sha='' ORDER BY created_at ASC, id ASC LIMIT ?", domain.WriteApplied, limit)
	if err != nil {
		return nil, fmt.Errorf("list uncommitted write events: %w", err)
	}
	defer rows.Close()
	out := make([]domain.WriteEvent, 0, limit)
	for rows.Next() {
		var e domain.WriteEvent
		if err := rows.Scan(&e.ID, &e.AgentID, &e.TaskID, &e.Path, &e.BaseHash, &e.Content, &e.Diff,
			&e.Reason, &e.Branch, &e.Status, &e.Error, &e.AppliedAt, &e.CommitSHA, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan uncommitted write event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CreateCommitGroup 把多次写入合并为一个 commit 组。
func (s *Store) CreateCommitGroup(ctx context.Context, id, commitSHA string, writeEventIDs []string, createdAt string) error {
	if id == "" || strings.TrimSpace(commitSHA) == "" {
		return fmt.Errorf("%w: commit group requires id/commit_sha", ErrInvalid)
	}
	payload, err := json.Marshal(writeEventIDs)
	if err != nil {
		return fmt.Errorf("commit group payload: %w", err)
	}
	if createdAt == "" {
		createdAt = Now()
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO commit_groups(id, commit_sha, write_event_ids, created_at) VALUES(?,?,?,?)",
		id, commitSHA, string(payload), createdAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("%w: commit group %s", ErrConflict, id)
		}
		return fmt.Errorf("create commit group: %w", err)
	}
	return nil
}

// MarkWriteEventsCommitted 标记写事件已随 commit 提交。
func (s *Store) MarkWriteEventsCommitted(ctx context.Context, writeEventIDs []string, commitSHA string) error {
	if len(writeEventIDs) == 0 {
		return nil
	}
	ph := make([]string, len(writeEventIDs))
	args := make([]any, 0, len(writeEventIDs)+1)
	args = append(args, commitSHA)
	for i, id := range writeEventIDs {
		ph[i] = "?"
		args = append(args, id)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE write_events SET commit_sha=? WHERE id IN ("+strings.Join(ph, ",")+")", args...); err != nil {
		return fmt.Errorf("mark write events committed: %w", err)
	}
	return nil
}

// MarkTimelineCommitted 标记时间线条目已随 commit 提交。
func (s *Store) MarkTimelineCommitted(ctx context.Context, writeEventIDs []string, commitSHA string) error {
	if len(writeEventIDs) == 0 {
		return nil
	}
	ph := make([]string, len(writeEventIDs))
	args := make([]any, 0, len(writeEventIDs)+1)
	args = append(args, commitSHA)
	for i, id := range writeEventIDs {
		ph[i] = "?"
		args = append(args, id)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE file_timeline SET commit_sha=? WHERE write_event_id IN ("+strings.Join(ph, ",")+")", args...); err != nil {
		return fmt.Errorf("mark timeline committed: %w", err)
	}
	return nil
}

var _ = sql.ErrNoRows

package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// UsageFilter 是成本/用量聚合条件。
type UsageFilter struct {
	AgentID        string
	TaskID         string
	ConversationID string
	Model          string
	From           string
	To             string
	// GroupBy: agent(默认) | task | model | day
	GroupBy string
	Limit   int
}

// UsageAggregate 是聚合后的成本行。
type UsageAggregate struct {
	Key              string  `json:"key"`
	AgentID          string  `json:"agent_id,omitempty"`
	TaskID           string  `json:"task_id,omitempty"`
	Model            string  `json:"model,omitempty"`
	Day              string  `json:"day,omitempty"`
	Calls            int64   `json:"calls"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CachedTokens     int64   `json:"cached_tokens"`
	Cost             float64 `json:"cost"`
}

// InsertUsage 追加一条模型调用账本（core 在调用成功后写）。
func (s *Store) InsertUsage(ctx context.Context, u domain.Usage) error {
	if u.ID == "" {
		u.ID = domain.NewID("usage")
	}
	if u.CreatedAt == "" {
		u.CreatedAt = Now()
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO usage_ledger(id, agent_id, task_id, conversation_id, model, prompt_tokens, completion_tokens, cached_tokens, cost, created_at) VALUES(?,?,?,?,?,?,?,?,?,?)",
		u.ID, u.AgentID, u.TaskID, u.ConversationID, u.Model, u.PromptTokens, u.CompletionTokens, u.CachedTokens, u.Cost, u.CreatedAt); err != nil {
		return fmt.Errorf("insert usage: %w", err)
	}
	return nil
}

// QueryUsage 按维度聚合 token/费用。
func (s *Store) QueryUsage(ctx context.Context, f UsageFilter) ([]UsageAggregate, error) {
	var (
		keyExpr string
		where   []string
		args    []any
	)
	switch f.GroupBy {
	case "", "agent":
		keyExpr = "agent_id"
	case "task":
		keyExpr = "task_id"
	case "model":
		keyExpr = "model"
	case "day":
		keyExpr = "substr(created_at,1,10)"
	default:
		return nil, fmt.Errorf("%w: unsupported group_by %s", ErrInvalid, f.GroupBy)
	}
	if f.AgentID != "" {
		where = append(where, "agent_id=?")
		args = append(args, f.AgentID)
	}
	if f.TaskID != "" {
		where = append(where, "task_id=?")
		args = append(args, f.TaskID)
	}
	if f.ConversationID != "" {
		where = append(where, "conversation_id=?")
		args = append(args, f.ConversationID)
	}
	if f.Model != "" {
		where = append(where, "model=?")
		args = append(args, f.Model)
	}
	if f.From != "" {
		where = append(where, "created_at >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "created_at <= ?")
		args = append(args, f.To)
	}
	q := "SELECT " + keyExpr + " AS k, COUNT(*), COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0), COALESCE(SUM(cached_tokens),0), COALESCE(SUM(cost),0) FROM usage_ledger"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " GROUP BY k ORDER BY k"
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	q += " LIMIT ?"
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query usage: %w", err)
	}
	defer rows.Close()
	out := make([]UsageAggregate, 0, 8)
	for rows.Next() {
		var a UsageAggregate
		if err := rows.Scan(&a.Key, &a.Calls, &a.PromptTokens, &a.CompletionTokens, &a.CachedTokens, &a.Cost); err != nil {
			return nil, fmt.Errorf("scan usage: %w", err)
		}
		switch f.GroupBy {
		case "task":
			a.TaskID = a.Key
		case "model":
			a.Model = a.Key
		case "day":
			a.Day = a.Key
		default:
			a.AgentID = a.Key
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

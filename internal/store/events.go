package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lignting/lignting-agents/internal/domain"
)

const eventColumns = "seq, type, conversation_id, object_type, object_id, actor_type, actor_id, payload, created_at"

func scanEvent(row interface{ Scan(...any) error }) (domain.Event, error) {
	var e domain.Event
	err := row.Scan(&e.Seq, &e.Type, &e.ConversationID, &e.ObjectType, &e.ObjectID,
		&e.ActorType, &e.ActorID, &e.Payload, &e.CreatedAt)
	if err != nil {
		return domain.Event{}, err
	}
	return e, nil
}

func normalizeEvent(ev *domain.Event) error {
	if ev.Type == "" {
		return fmt.Errorf("%w: event type required", ErrInvalid)
	}
	if ev.Payload == "" {
		ev.Payload = "{}"
	}
	if !json.Valid([]byte(ev.Payload)) {
		return fmt.Errorf("%w: event payload must be valid JSON", ErrInvalid)
	}
	if ev.CreatedAt == "" {
		ev.CreatedAt = Now()
	}
	return nil
}

// AppendEvent 追加一条平台事件（message.delta/agent.status 等均由 core 走这里）。
// 事件是 SSE 的唯一广播源；不写审计（审计是独立 append-only 流）。
func (s *Store) AppendEvent(ctx context.Context, ev domain.Event) (domain.Event, error) {
	if err := normalizeEvent(&ev); err != nil {
		return domain.Event{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return domain.Event{}, err
	}
	defer func() { _ = tx.Rollback() }()
	out, err := appendEventTxRaw(tx, ev)
	if err != nil {
		return domain.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Event{}, fmt.Errorf("commit event: %w", err)
	}
	s.notifyEvent()
	return out, nil
}

// AppendEventTx 是 AppendEvent 的事务变体：供 core 在 WithTx 内与消息/审计原子写入。
// 提交后的 SSE 唤醒由 Store.WithTx 负责。
func (s *Store) AppendEventTx(ctx context.Context, tx *sql.Tx, ev domain.Event) (domain.Event, error) {
	_ = ctx
	if err := normalizeEvent(&ev); err != nil {
		return domain.Event{}, err
	}
	return appendEventTxRaw(tx, ev)
}

func appendEventTxRaw(tx *sql.Tx, ev domain.Event) (domain.Event, error) {
	res, err := tx.Exec("INSERT INTO events(type, conversation_id, object_type, object_id, actor_type, actor_id, payload, created_at) VALUES(?,?,?,?,?,?,?,?)",
		ev.Type, ev.ConversationID, ev.ObjectType, ev.ObjectID, ev.ActorType, ev.ActorID, ev.Payload, ev.CreatedAt)
	if err != nil {
		return domain.Event{}, fmt.Errorf("append event: %w", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return domain.Event{}, fmt.Errorf("event seq: %w", err)
	}
	ev.Seq = seq
	return ev, nil
}

// ListEventsSince 返回 seq > since 的事件（按 seq 升序），用于 SSE 断线补齐。
// conversationID 非空时只返回该会话事件；limit<=0 默认 500，上限 1000。
func (s *Store) ListEventsSince(ctx context.Context, since int64, conversationID string, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 1000 {
		limit = 1000
	}
	q := "SELECT " + eventColumns + " FROM events WHERE seq > ?"
	args := []any{since}
	if conversationID != "" {
		q += " AND conversation_id=?"
		args = append(args, conversationID)
	}
	q += " ORDER BY seq ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Event, 0, limit)
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MaxEventSeq 返回当前最新事件 seq（空库为 0）。
func (s *Store) MaxEventSeq(ctx context.Context) (int64, error) {
	var seq int64
	if err := s.DB.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM events").Scan(&seq); err != nil {
		return 0, fmt.Errorf("max event seq: %w", err)
	}
	return seq, nil
}

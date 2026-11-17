package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lignting/lignting-agents/internal/domain"
)

const turnColumns = "id, agent_id, conversation_id, status, error, started_at, ended_at"

// CreateTurn 创建一次 Agent turn（崩溃后由 core 标记中断）。
func (s *Store) CreateTurn(ctx context.Context, t domain.Turn) error {
	if t.ID == "" {
		t.ID = domain.NewID("turn")
	}
	if t.Status == "" {
		t.Status = "running"
	}
	if t.StartedAt == "" {
		t.StartedAt = Now()
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO turns(id, agent_id, conversation_id, status, error, started_at, ended_at) VALUES(?,?,?,?,?,?,?)",
		t.ID, t.AgentID, t.ConversationID, t.Status, t.Error, t.StartedAt, t.EndedAt); err != nil {
		return fmt.Errorf("create turn: %w", err)
	}
	return nil
}

// FinishTurn 结束 turn（只更新 turn 行，不带事件/审计；core.TurnStore 端口）。
func (s *Store) FinishTurn(ctx context.Context, turnID, status, errText string) error {
	if turnID == "" {
		return fmt.Errorf("%w: turn id required", ErrInvalid)
	}
	if status == "" {
		status = "completed"
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE turns SET status=?, error=?, ended_at=? WHERE id=?", status, errText, Now(), turnID)
	if err != nil {
		return fmt.Errorf("finish turn: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: turn %s", ErrNotFound, turnID)
	}
	return nil
}

// FinishTurnWithEvents 结束 turn 并原子追加事件/审计（可选原子路径，core 当前不调用）。
// audit.Action 为空时跳过审计；events 可为空。提交后唤醒 SSE。
func (s *Store) FinishTurnWithEvents(ctx context.Context, turnID, status, errText string, events []domain.Event, audit domain.AuditEntry) error {
	if turnID == "" {
		return fmt.Errorf("%w: turn id required", ErrInvalid)
	}
	if status == "" {
		status = "completed"
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec("UPDATE turns SET status=?, error=?, ended_at=? WHERE id=?", status, errText, Now(), turnID)
		if err != nil {
			return fmt.Errorf("finish turn: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: turn %s", ErrNotFound, turnID)
		}
		for _, ev := range events {
			if _, err := appendEventTxRaw(tx, ev); err != nil {
				return err
			}
		}
		if audit.Action != "" {
			if _, err := appendAuditTxRaw(tx, audit); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetTurn 按 ID 读取 turn。
func (s *Store) GetTurn(ctx context.Context, id string) (domain.Turn, error) {
	var t domain.Turn
	err := s.DB.QueryRowContext(ctx, "SELECT "+turnColumns+" FROM turns WHERE id=?", id).
		Scan(&t.ID, &t.AgentID, &t.ConversationID, &t.Status, &t.Error, &t.StartedAt, &t.EndedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Turn{}, fmt.Errorf("%w: turn %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Turn{}, fmt.Errorf("get turn: %w", err)
	}
	return t, nil
}

// MarkInterruptedTurns 在进程启动时把上次崩溃遗留的 running turn 标记为中断。
func (s *Store) MarkInterruptedTurns(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, "UPDATE turns SET status=?, error=?, ended_at=? WHERE status=?",
		"interrupted", "进程重启，turn 已中断", Now(), "running")
	if err != nil {
		return 0, fmt.Errorf("mark interrupted turns: %w", err)
	}
	return res.RowsAffected()
}

// InsertContextSnapshot 保存每 turn 注入包（只读回放用）。
func (s *Store) InsertContextSnapshot(ctx context.Context, c domain.ContextSnapshot) error {
	if c.ID == "" {
		c.ID = domain.NewID("ctx")
	}
	if c.CreatedAt == "" {
		c.CreatedAt = Now()
	}
	if c.Payload == "" {
		c.Payload = "{}"
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO context_snapshots(id, turn_id, agent_id, conversation_id, payload, created_at) VALUES(?,?,?,?,?,?)",
		c.ID, c.TurnID, c.AgentID, c.ConversationID, c.Payload, c.CreatedAt); err != nil {
		return fmt.Errorf("insert context snapshot: %w", err)
	}
	return nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// R3/R4 临时桥（FR-CHAT-16）：带范围与到期，到期自动关闭并审计。
// bridges 表已有；创建时同事务写 schedules(kind=bridge.expire) 供调度器到期关闭。

const bridgeColumns = "id, scope, from_type, from_id, to_type, to_id, expires_at, status, created_at"

func scanBridge(row interface{ Scan(...any) error }) (domain.Bridge, error) {
	var b domain.Bridge
	err := row.Scan(&b.ID, &b.Scope, &b.FromType, &b.FromID, &b.ToType, &b.ToID, &b.ExpiresAt, &b.Status, &b.CreatedAt)
	if err != nil {
		return domain.Bridge{}, err
	}
	return b, nil
}

// CreateBridge 创建临时桥（active），并写入到期调度。
func (s *Store) CreateBridge(ctx context.Context, b domain.Bridge) (domain.Bridge, error) {
	if b.ID == "" {
		b.ID = domain.NewID("bridge")
	}
	if strings.TrimSpace(b.Scope) == "" || strings.TrimSpace(b.FromType) == "" || strings.TrimSpace(b.FromID) == "" ||
		strings.TrimSpace(b.ToType) == "" || strings.TrimSpace(b.ToID) == "" || strings.TrimSpace(b.ExpiresAt) == "" {
		return domain.Bridge{}, fmt.Errorf("%w: bridge requires scope/from/to/expires_at", ErrInvalid)
	}
	if b.Status == "" {
		b.Status = "active"
	}
	if b.CreatedAt == "" {
		b.CreatedAt = Now()
	}
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO bridges("+bridgeColumns+") VALUES(?,?,?,?,?,?,?,?,?)",
			b.ID, b.Scope, b.FromType, b.FromID, b.ToType, b.ToID, b.ExpiresAt, b.Status, b.CreatedAt); err != nil {
			return fmt.Errorf("create bridge: %w", err)
		}
		if _, err := insertScheduleTx(tx, Schedule{
			Kind: "bridge.expire", DueAt: b.ExpiresAt,
			Payload: marshalJSON(map[string]any{"bridge_id": b.ID}),
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return domain.Bridge{}, err
	}
	return b, nil
}

// GetBridge 按 ID 读取桥。
func (s *Store) GetBridge(ctx context.Context, id string) (domain.Bridge, error) {
	b, err := scanBridge(s.DB.QueryRowContext(ctx, "SELECT "+bridgeColumns+" FROM bridges WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Bridge{}, fmt.Errorf("%w: bridge %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Bridge{}, fmt.Errorf("get bridge: %w", err)
	}
	return b, nil
}

// GetActiveBridge 查询某方向的有效桥（active 且未到期）；无则 (nil,nil)。
func (s *Store) GetActiveBridge(ctx context.Context, fromType, fromID, toType, toID string) (*domain.Bridge, error) {
	b, err := scanBridge(s.DB.QueryRowContext(ctx,
		"SELECT "+bridgeColumns+" FROM bridges WHERE from_type=? AND from_id=? AND to_type=? AND to_id=? AND status='active' AND expires_at > ? ORDER BY created_at DESC LIMIT 1",
		fromType, fromID, toType, toID, Now()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active bridge: %w", err)
	}
	return &b, nil
}

// ListBridges 按状态列出桥（status 空=全部）。
func (s *Store) ListBridges(ctx context.Context, status string) ([]domain.Bridge, error) {
	q := "SELECT " + bridgeColumns + " FROM bridges"
	args := []any{}
	if strings.TrimSpace(status) != "" {
		q += " WHERE status=?"
		args = append(args, status)
	}
	q += " ORDER BY created_at DESC"
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list bridges: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Bridge, 0, 8)
	for rows.Next() {
		b, err := scanBridge(rows)
		if err != nil {
			return nil, fmt.Errorf("scan bridge: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CloseBridge 关闭桥（幂等：已关闭直接返回）。
func (s *Store) CloseBridge(ctx context.Context, id string) (domain.Bridge, error) {
	b, err := s.GetBridge(ctx, id)
	if err != nil {
		return domain.Bridge{}, err
	}
	if b.Status != "active" {
		return b, nil
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE bridges SET status='closed' WHERE id=? AND status='active'", id); err != nil {
		return domain.Bridge{}, fmt.Errorf("close bridge: %w", err)
	}
	b.Status = "closed"
	return b, nil
}

// ExpireBridge 把到期的 active 桥标记 expired（幂等，changed=false 表示已处理/不存在）。
func (s *Store) ExpireBridge(ctx context.Context, id string) (domain.Bridge, bool, error) {
	b, err := s.GetBridge(ctx, id)
	if err != nil {
		return domain.Bridge{}, false, err
	}
	if b.Status != "active" {
		return b, false, nil
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE bridges SET status='expired' WHERE id=? AND status='active'", id); err != nil {
		return domain.Bridge{}, false, fmt.Errorf("expire bridge: %w", err)
	}
	b.Status = "expired"
	return b, true, nil
}

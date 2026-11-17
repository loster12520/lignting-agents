package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Intent 是项目意图卡的一版投影。
type Intent struct {
	ID        string `json:"id"`
	Version   int    `json:"version"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// GetIntent 返回最新一版意图卡；没有时返回 (nil, nil)。
func (s *Store) GetIntent(ctx context.Context) (*Intent, error) {
	var it Intent
	err := s.DB.QueryRowContext(ctx, "SELECT id, version, content, status, created_at FROM intents ORDER BY version DESC, created_at DESC LIMIT 1").
		Scan(&it.ID, &it.Version, &it.Content, &it.Status, &it.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get intent: %w", err)
	}
	return &it, nil
}

// SaveIntent 保存一版项目意图卡：版本号全局递增，返回新记录 ID 与版本。
// schema 的 intents.id 是主键，因此每次保存写一行新 ID，version 单调递增。
func (s *Store) SaveIntent(ctx context.Context, content string) (string, int, error) {
	if strings.TrimSpace(content) == "" || !json.Valid([]byte(content)) {
		return "", 0, fmt.Errorf("%w: intent content must be valid JSON", ErrInvalid)
	}
	var (
		id      string
		version int
	)
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var prev int
		err := tx.QueryRow("SELECT COALESCE(MAX(version),0) FROM intents").Scan(&prev)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("intent version: %w", err)
		}
		version = prev + 1
		id = domain.NewID("intent")
		_, err = tx.Exec("INSERT INTO intents(id, version, content, status, created_at) VALUES(?,?,?,?,?)",
			id, version, content, "draft", Now())
		if err != nil {
			return fmt.Errorf("save intent: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	return id, version, nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// Settings 是单用户本地配置的键值投影（settings 表）。
// 注意：session.secret 等敏感键只在内部使用，平台层用 allowlist 输出，不得回显。
type Settings map[string]string

const readCursorPrefix = "read:"

// Setting 读取单个配置键。
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get setting %s: %w", key, err)
	}
	return v, true, nil
}

// SettingsWithPrefix 读取某前缀下的全部配置（prefix 为空表示全部）。
func (s *Store) SettingsWithPrefix(ctx context.Context, prefix string) (Settings, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT key, value FROM settings WHERE key LIKE ?", prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()
	out := Settings{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSetting 写入单个配置键（用户可见配置变更由平台层审计）。
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	return s.SetSettings(ctx, map[string]string{key: value})
}

// SetSettings 在同一事务内写入多个配置键。
func (s *Store) SetSettings(ctx context.Context, kv map[string]string) error {
	if len(kv) == 0 {
		return nil
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		return SetSettingsTx(tx, kv)
	})
}

// SetSettingsTx 是 SetSettings 的事务变体，供 setup 等多表原子写复用。
func SetSettingsTx(tx *sql.Tx, kv map[string]string) error {
	now := Now()
	for k, v := range kv {
		if _, err := tx.Exec("INSERT INTO settings(key, value, updated_at) VALUES(?,?,?) "+
			"ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at",
			k, v, now); err != nil {
			return fmt.Errorf("set setting %s: %w", k, err)
		}
	}
	return nil
}

// DeleteSetting 删除配置键。
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM settings WHERE key=?", key); err != nil {
		return fmt.Errorf("delete setting %s: %w", key, err)
	}
	return nil
}

// --- 已读位置（v0.0.1 临时实现）---
//
// v0.0.1 临时：已读位置存在 settings 表 key=read:<conversation_id>，避免改动
// Lead 冻结的 schema；后续版本迁移到独立的 conversation_reads 表。
// 该数据不进入检索索引，也不写审计正文。

// GetReadSeq 返回 owner 在某会话的已读位置（无记录为 0）。
func (s *Store) GetReadSeq(ctx context.Context, conversationID string) (int64, error) {
	v, ok, err := s.Setting(ctx, readCursorPrefix+conversationID)
	if err != nil || !ok {
		return 0, err
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, nil // 损坏值按未读处理，不阻塞会话打开
	}
	return n, nil
}

// SetReadSeq 单调推进已读位置（不会后退）。
func (s *Store) SetReadSeq(ctx context.Context, conversationID string, seq int64) error {
	if seq < 0 {
		return fmt.Errorf("%w: read seq < 0", ErrInvalid)
	}
	cur, err := s.GetReadSeq(ctx, conversationID)
	if err != nil {
		return err
	}
	if seq <= cur {
		return nil
	}
	return s.SetSetting(ctx, readCursorPrefix+conversationID, strconv.FormatInt(seq, 10))
}

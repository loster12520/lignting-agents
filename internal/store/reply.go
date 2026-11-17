package store

import (
	"context"
	"database/sql"

	"github.com/lignting/lignting-agents/internal/domain"
)

// AppendReply 是 core 的原子回复写：消息 + 索引 + 事件 + 审计同一事务提交，
// 提交后唤醒 SSE。events 可为空；audit 为零值（Action 为空）时跳过审计。
// 对应 core.AtomicReplyStore 端口。
func (s *Store) AppendReply(ctx context.Context, msg domain.Message, events []domain.Event, audit domain.AuditEntry) (domain.Message, error) {
	var saved domain.Message
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		out, err := s.insertMessageTx(ctx, tx, msg)
		if err != nil {
			return err
		}
		saved = out
		for _, ev := range events {
			if _, err := s.AppendEventTx(ctx, tx, ev); err != nil {
				return err
			}
		}
		if audit.Action != "" {
			if _, err := s.AppendAuditTx(ctx, tx, audit); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.Message{}, err
	}
	return saved, nil
}

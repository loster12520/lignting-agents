package search

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lignting/lignting-agents/internal/store"
)

// RebuildStats 是一次全量重建的结果。
type RebuildStats struct {
	Documents  int64 `json:"documents"`
	Terms      int64 `json:"terms"`
	DurationMS int64 `json:"duration_ms"`
}

type rebuildRow struct {
	Seq int64
	Doc store.IndexDoc
}

// Rebuild 从源数据全量重建消息检索索引（不修改 messages 源数据）。
// 重建按 seq 分批进行，每批独立事务；期间旧索引已清空，搜索会返回空结果而非脏结果。
func Rebuild(ctx context.Context, db *sql.DB) (RebuildStats, error) {
	start := time.Now()
	stats := RebuildStats{}

	clearTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return stats, fmt.Errorf("rebuild: begin clear: %w", err)
	}
	if _, err := clearTx.ExecContext(ctx, "DELETE FROM search_terms WHERE doc_id IN (SELECT doc_id FROM search_docs WHERE kind='message')"); err != nil {
		_ = clearTx.Rollback()
		return stats, fmt.Errorf("rebuild: clear terms: %w", err)
	}
	if _, err := clearTx.ExecContext(ctx, "DELETE FROM search_docs WHERE kind='message'"); err != nil {
		_ = clearTx.Rollback()
		return stats, fmt.Errorf("rebuild: clear docs: %w", err)
	}
	if err := clearTx.Commit(); err != nil {
		return stats, fmt.Errorf("rebuild: commit clear: %w", err)
	}

	var lastSeq int64
	const batchSize = 500
	for {
		batch, err := loadMessageBatch(ctx, db, lastSeq, batchSize)
		if err != nil {
			return stats, err
		}
		if len(batch) == 0 {
			break
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return stats, fmt.Errorf("rebuild: begin batch: %w", err)
		}
		for _, row := range batch {
			if err := indexDocTx(tx, row.Doc); err != nil {
				_ = tx.Rollback()
				return stats, err
			}
		}
		if err := tx.Commit(); err != nil {
			return stats, fmt.Errorf("rebuild: commit batch: %w", err)
		}
		stats.Documents += int64(len(batch))
		lastSeq = batch[len(batch)-1].Seq
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_terms").Scan(&stats.Terms); err != nil {
		return stats, fmt.Errorf("rebuild: count terms: %w", err)
	}
	stats.DurationMS = time.Since(start).Milliseconds()
	return stats, nil
}

func loadMessageBatch(ctx context.Context, db *sql.DB, afterSeq int64, limit int) ([]rebuildRow, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT m.seq, m.id, m.conversation_id, m.content, m.created_at, "+
			"COALESCE(c.department_id,''), COALESCE(c.visibility,'project') "+
			"FROM messages m LEFT JOIN conversations c ON c.id = m.conversation_id "+
			"WHERE m.deleted_at='' AND m.seq > ? ORDER BY m.seq ASC LIMIT ?", afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("rebuild: load messages: %w", err)
	}
	defer rows.Close()
	out := make([]rebuildRow, 0, limit)
	for rows.Next() {
		var (
			seq              int64
			id, convID       string
			content, created string
			deptID, vis      string
		)
		if err := rows.Scan(&seq, &id, &convID, &content, &created, &deptID, &vis); err != nil {
			return nil, fmt.Errorf("rebuild: scan message: %w", err)
		}
		out = append(out, rebuildRow{Seq: seq, Doc: store.IndexDoc{
			DocID:          store.MessageDocID(id),
			Kind:           "message",
			RefID:          id,
			ConversationID: convID,
			DepartmentID:   deptID,
			Title:          "",
			Body:           content,
			Visibility:     vis,
			CreatedAt:      created,
		}})
	}
	return out, rows.Err()
}

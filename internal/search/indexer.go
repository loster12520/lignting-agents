package search

import (
	"database/sql"
	"fmt"

	"github.com/lignting/lignting-agents/internal/store"
)

// Indexer 实现 store.DocIndexer：在调用方事务内同步写 search_docs/search_terms。
// 因为索引与消息在同一事务提交，写入后立即可搜；回滚不会留下脏索引。
type Indexer struct{}

func NewIndexer() *Indexer { return &Indexer{} }

// indexDocTx 写入/更新一个文档及其倒排词。
func indexDocTx(tx *sql.Tx, doc store.IndexDoc) error {
	if doc.DocID == "" || doc.Kind == "" {
		return fmt.Errorf("search: doc requires doc_id and kind")
	}
	if _, err := tx.Exec("DELETE FROM search_terms WHERE doc_id=?", doc.DocID); err != nil {
		return fmt.Errorf("search: clear terms: %w", err)
	}
	if _, err := tx.Exec("INSERT INTO search_docs(doc_id, kind, ref_id, conversation_id, department_id, title, body, visibility, created_at, deleted) "+
		"VALUES(?,?,?,?,?,?,?,?,?,0) "+
		"ON CONFLICT(doc_id) DO UPDATE SET kind=excluded.kind, ref_id=excluded.ref_id, conversation_id=excluded.conversation_id, "+
		"department_id=excluded.department_id, title=excluded.title, body=excluded.body, visibility=excluded.visibility, "+
		"created_at=excluded.created_at, deleted=0",
		doc.DocID, doc.Kind, doc.RefID, doc.ConversationID, doc.DepartmentID, doc.Title, doc.Body, doc.Visibility, doc.CreatedAt); err != nil {
		return fmt.Errorf("search: upsert doc: %w", err)
	}
	terms := IndexTerms(doc.Title, doc.Body)
	if len(terms) == 0 {
		return nil
	}
	stmt, err := tx.Prepare("INSERT OR REPLACE INTO search_terms(term, doc_id, weight) VALUES(?,?,?)")
	if err != nil {
		return fmt.Errorf("search: prepare terms: %w", err)
	}
	defer stmt.Close()
	for _, t := range terms {
		if _, err := stmt.Exec(t.Text, doc.DocID, t.Weight); err != nil {
			return fmt.Errorf("search: insert term: %w", err)
		}
	}
	return nil
}

// IndexTerms 生成 title + body 的索引词；title 权重 x2 用于排序偏置。
func IndexTerms(title, body string) []Term {
	weights := map[string]float64{}
	merge := func(terms []Term, multiplier float64) {
		for _, t := range terms {
			w := t.Weight * multiplier
			if cur, ok := weights[t.Text]; !ok || w > cur {
				weights[t.Text] = w
			}
		}
	}
	merge(Terms(body), 1.0)
	merge(Terms(title), 2.0)
	out := make([]Term, 0, len(weights))
	for text, w := range weights {
		out = append(out, Term{Text: text, Weight: w})
	}
	return out
}

// IndexTx 实现 store.DocIndexer。
func (i *Indexer) IndexTx(tx *sql.Tx, doc store.IndexDoc) error {
	return indexDocTx(tx, doc)
}

// DeleteTx 实现 store.DocIndexer：删除倒排词并标记文档已删除（保留墓碑供重建对账）。
func (i *Indexer) DeleteTx(tx *sql.Tx, docID string) error {
	if docID == "" {
		return fmt.Errorf("search: empty doc_id")
	}
	if _, err := tx.Exec("DELETE FROM search_terms WHERE doc_id=?", docID); err != nil {
		return fmt.Errorf("search: delete terms: %w", err)
	}
	if _, err := tx.Exec("UPDATE search_docs SET deleted=1 WHERE doc_id=?", docID); err != nil {
		return fmt.Errorf("search: tombstone doc: %w", err)
	}
	return nil
}

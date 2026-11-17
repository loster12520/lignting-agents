package search

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Filter 是检索过滤条件。权限过滤在 SQL 前置完成（不是先查全量再删）。
type Filter struct {
	Kinds               []string
	ConversationID      string
	ConversationIDs     []string
	DepartmentID        string
	SenderID            string
	From                string
	To                  string
	AllowedVisibilities []string
	Limit               int
	Offset              int
}

// Result 是检索命中。
type Result struct {
	DocID          string  `json:"doc_id"`
	Kind           string  `json:"kind"`
	RefID          string  `json:"ref_id"`
	ConversationID string  `json:"conversation_id"`
	DepartmentID   string  `json:"department_id"`
	Title          string  `json:"title"`
	Snippet        string  `json:"snippet"`
	Score          float64 `json:"score"`
	CreatedAt      string  `json:"created_at"`
}

// ResultSet 是检索结果 + 总数（分页一致）。
type ResultSet struct {
	Items []Result `json:"items"`
	Total int64    `json:"total"`
}

func buildDocFilter(f Filter) (string, []any) {
	where := []string{"d.deleted = 0"}
	args := make([]any, 0, 8)
	if len(f.Kinds) > 0 {
		ph := make([]string, len(f.Kinds))
		for i, k := range f.Kinds {
			ph[i] = "?"
			args = append(args, k)
		}
		where = append(where, "d.kind IN ("+strings.Join(ph, ",")+")")
	}
	if f.ConversationID != "" {
		where = append(where, "d.conversation_id = ?")
		args = append(args, f.ConversationID)
	}
	if len(f.ConversationIDs) > 0 {
		ph := make([]string, len(f.ConversationIDs))
		for i, id := range f.ConversationIDs {
			ph[i] = "?"
			args = append(args, id)
		}
		where = append(where, "d.conversation_id IN ("+strings.Join(ph, ",")+")")
	}
	if f.DepartmentID != "" {
		where = append(where, "d.department_id = ?")
		args = append(args, f.DepartmentID)
	}
	if f.SenderID != "" {
		where = append(where, "EXISTS (SELECT 1 FROM messages m WHERE m.id = d.ref_id AND m.sender_id = ?)")
		args = append(args, f.SenderID)
	}
	if f.From != "" {
		where = append(where, "d.created_at >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "d.created_at <= ?")
		args = append(args, f.To)
	}
	if len(f.AllowedVisibilities) > 0 {
		ph := make([]string, len(f.AllowedVisibilities))
		for i, v := range f.AllowedVisibilities {
			ph[i] = "?"
			args = append(args, v)
		}
		where = append(where, "d.visibility IN ("+strings.Join(ph, ",")+")")
	}
	return "SELECT d.doc_id FROM search_docs d WHERE " + strings.Join(where, " AND "), args
}

// escapeLike 使用 '!' 作为 LIKE 转义符（避免 SQL/Go 双重反斜杠转义）。
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "!", "!!")
	s = strings.ReplaceAll(s, "%", "!%")
	s = strings.ReplaceAll(s, "_", "!_")
	return s
}

// Search 执行检索：中文 2 字 bigram 交集、英文小写词、代码标识符；
// 结果按相关度（命中词权重和）+ 时间排序，并生成仅含 em 标签安全片段的 snippet。
func Search(ctx context.Context, db *sql.DB, query string, f Filter) (*ResultSet, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	qterms := QueryTerms(query)
	var normal, single []string
	for _, t := range qterms {
		if t.SingleHan {
			single = append(single, t.Text)
		} else {
			normal = append(normal, t.Text)
		}
	}

	base, baseArgs := buildDocFilter(f)
	compound := base
	compArgs := append([]any{}, baseArgs...)
	for _, t := range normal {
		compound += " INTERSECT SELECT doc_id FROM search_terms WHERE term = ?"
		compArgs = append(compArgs, t)
	}
	for _, t := range single {
		compound += " INTERSECT SELECT doc_id FROM search_docs WHERE deleted = 0 AND body LIKE ? ESCAPE '!'"
		compArgs = append(compArgs, "%"+escapeLike(t)+"%")
	}

	var total int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ("+compound+")", compArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("search count: %w", err)
	}
	rs := &ResultSet{Items: []Result{}, Total: total}
	if total == 0 {
		return rs, nil
	}

	scoreExpr := "0"
	scoreArgs := make([]any, 0, len(normal))
	if len(normal) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(normal)), ",")
		scoreExpr = "(SELECT COALESCE(SUM(t.weight),0) FROM search_terms t WHERE t.doc_id = d.doc_id AND t.term IN (" + ph + "))"
		for _, t := range normal {
			scoreArgs = append(scoreArgs, t)
		}
	}
	order := "score DESC, d.created_at DESC"
	if len(qterms) == 0 {
		order = "d.created_at DESC"
	}
	q := "SELECT d.doc_id, d.kind, d.ref_id, d.conversation_id, d.department_id, d.title, d.body, d.created_at, " +
		scoreExpr + " AS score FROM search_docs d JOIN (" + compound + ") c ON c.doc_id = d.doc_id ORDER BY " + order + " LIMIT ? OFFSET ?"
	args := append([]any{}, scoreArgs...)
	args = append(args, compArgs...)
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()
	snippetTerms := append(append([]string{}, normal...), single...)
	for rows.Next() {
		var (
			r     Result
			body  string
			title string
		)
		if err := rows.Scan(&r.DocID, &r.Kind, &r.RefID, &r.ConversationID, &r.DepartmentID, &title, &body, &r.CreatedAt, &r.Score); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		r.Title = title
		r.Snippet = Snippet(body, title, snippetTerms, 160)
		rs.Items = append(rs.Items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rs, nil
}

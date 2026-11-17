package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// 审计 hash 链规范：
//
//	canonical = actor_type  actor_id  action  object_type  object_id
//	           result  canonical_detail_json  created_at
//	hash      = sha256_hex(prev_hash + canonical)
//
// detail 必须是 JSON object；写入前按 map 重新序列化（键排序）保证可重算。
// audit_log 只允许 INSERT；应用层不提供更新/删除方法。

const auditColumns = "seq, actor_type, actor_id, action, object_type, object_id, result, detail, prev_hash, hash, created_at"

// AuditFilter 是审计查询条件。
type AuditFilter struct {
	ActorType  string
	ActorID    string
	Action     string
	ObjectType string
	ObjectID   string
	Result     string
	SinceSeq   int64
	// SinceCreatedAt 只返回 created_at >= 该时间的记录（登录失败限流等）。
	SinceCreatedAt string
	Limit          int
}

func scanAudit(row interface{ Scan(...any) error }) (domain.AuditEntry, error) {
	var a domain.AuditEntry
	err := row.Scan(&a.Seq, &a.ActorType, &a.ActorID, &a.Action, &a.ObjectType, &a.ObjectID,
		&a.Result, &a.Detail, &a.PrevHash, &a.Hash, &a.CreatedAt)
	if err != nil {
		return domain.AuditEntry{}, err
	}
	return a, nil
}

func canonicalAuditDetail(detail string) (string, error) {
	if strings.TrimSpace(detail) == "" {
		return "{}", nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(detail), &m); err != nil {
		return "", fmt.Errorf("%w: audit detail must be a JSON object", ErrInvalid)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("canonical audit detail: %w", err)
	}
	return string(b), nil
}

func canonicalAudit(a domain.AuditEntry) string {
	return strings.Join([]string{
		a.ActorType, a.ActorID, a.Action, a.ObjectType, a.ObjectID, a.Result, a.Detail, a.CreatedAt,
	}, "")
}

func auditHash(prev, canonical string) string {
	sum := sha256.Sum256([]byte(prev + canonical))
	return hex.EncodeToString(sum[:])
}

// AppendAudit 追加审计记录（自行开事务）。
func (s *Store) AppendAudit(ctx context.Context, a domain.AuditEntry) (domain.AuditEntry, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return domain.AuditEntry{}, err
	}
	defer func() { _ = tx.Rollback() }()
	out, err := s.AppendAuditTx(ctx, tx, a)
	if err != nil {
		return domain.AuditEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.AuditEntry{}, fmt.Errorf("commit audit: %w", err)
	}
	return out, nil
}

// AppendAuditTx 在调用方事务内追加审计并计算 hash 链（core/memory 端口用）。
func (s *Store) AppendAuditTx(ctx context.Context, tx *sql.Tx, a domain.AuditEntry) (domain.AuditEntry, error) {
	_ = ctx
	return appendAuditTxRaw(tx, a)
}

// appendAuditTxRaw 是包级事务助手，供 store 内部的组合事务复用。
func appendAuditTxRaw(tx *sql.Tx, a domain.AuditEntry) (domain.AuditEntry, error) {
	detail, err := canonicalAuditDetail(a.Detail)
	if err != nil {
		return domain.AuditEntry{}, err
	}
	a.Detail = detail
	if a.Result == "" {
		a.Result = "ok"
	}
	if a.ActorType == "" {
		return domain.AuditEntry{}, fmt.Errorf("%w: audit actor_type required", ErrInvalid)
	}
	if a.Action == "" {
		return domain.AuditEntry{}, fmt.Errorf("%w: audit action required", ErrInvalid)
	}
	if a.CreatedAt == "" {
		a.CreatedAt = Now()
	}
	var prev string
	err = tx.QueryRow("SELECT hash FROM audit_log ORDER BY seq DESC LIMIT 1").Scan(&prev)
	if err != nil && err != sql.ErrNoRows {
		return domain.AuditEntry{}, fmt.Errorf("read audit head: %w", err)
	}
	a.PrevHash = prev
	a.Hash = auditHash(prev, canonicalAudit(a))
	res, err := tx.Exec("INSERT INTO audit_log(actor_type, actor_id, action, object_type, object_id, result, detail, prev_hash, hash, created_at) VALUES(?,?,?,?,?,?,?,?,?,?)",
		a.ActorType, a.ActorID, a.Action, a.ObjectType, a.ObjectID, a.Result, a.Detail, a.PrevHash, a.Hash, a.CreatedAt)
	if err != nil {
		return domain.AuditEntry{}, fmt.Errorf("append audit: %w", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return domain.AuditEntry{}, fmt.Errorf("audit seq: %w", err)
	}
	a.Seq = seq
	return a, nil
}

// QueryAudit 查询审计（seq 倒序；单用户本地场景最多 1000 条）。
func (s *Store) QueryAudit(ctx context.Context, f AuditFilter) ([]domain.AuditEntry, error) {
	var (
		where []string
		args  []any
	)
	add := func(cond string, v any) {
		where = append(where, cond)
		args = append(args, v)
	}
	if f.ActorType != "" {
		add("actor_type=?", f.ActorType)
	}
	if f.ActorID != "" {
		add("actor_id=?", f.ActorID)
	}
	if f.Action != "" {
		add("action=?", f.Action)
	}
	if f.ObjectType != "" {
		add("object_type=?", f.ObjectType)
	}
	if f.ObjectID != "" {
		add("object_id=?", f.ObjectID)
	}
	if f.Result != "" {
		add("result=?", f.Result)
	}
	if f.SinceSeq > 0 {
		add("seq > ?", f.SinceSeq)
	}
	if f.SinceCreatedAt != "" {
		add("created_at >= ?", f.SinceCreatedAt)
	}
	q := "SELECT " + auditColumns + " FROM audit_log"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY seq DESC"
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	q += " LIMIT ?"
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit: %w", err)
	}
	defer rows.Close()
	out := make([]domain.AuditEntry, 0, limit)
	for rows.Next() {
		a, err := scanAudit(rows)
		if err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountAudit 统计符合条件的审计条数（登录失败限流等，不拉全量）。
func (s *Store) CountAudit(ctx context.Context, f AuditFilter) (int64, error) {
	where := []string{"1=1"}
	args := make([]any, 0, 6)
	add := func(cond string, v any) {
		where = append(where, cond)
		args = append(args, v)
	}
	if f.ActorType != "" {
		add("actor_type=?", f.ActorType)
	}
	if f.ActorID != "" {
		add("actor_id=?", f.ActorID)
	}
	if f.Action != "" {
		add("action=?", f.Action)
	}
	if f.ObjectType != "" {
		add("object_type=?", f.ObjectType)
	}
	if f.ObjectID != "" {
		add("object_id=?", f.ObjectID)
	}
	if f.Result != "" {
		add("result=?", f.Result)
	}
	if f.SinceSeq > 0 {
		add("seq > ?", f.SinceSeq)
	}
	if f.SinceCreatedAt != "" {
		add("created_at >= ?", f.SinceCreatedAt)
	}
	var n int64
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log WHERE "+strings.Join(where, " AND "), args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count audit: %w", err)
	}
	return n, nil
}

// VerifyAuditChain 全量重算 hash 链；返回校验条数，断链返回错误。
func (s *Store) VerifyAuditChain(ctx context.Context) (int64, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+auditColumns+" FROM audit_log ORDER BY seq ASC")
	if err != nil {
		return 0, fmt.Errorf("read audit chain: %w", err)
	}
	defer rows.Close()
	var (
		count int64
		prev  string
	)
	for rows.Next() {
		a, err := scanAudit(rows)
		if err != nil {
			return count, fmt.Errorf("scan audit: %w", err)
		}
		if a.PrevHash != prev {
			return count, fmt.Errorf("audit chain broken at seq %d: prev_hash mismatch", a.Seq)
		}
		if want := auditHash(prev, canonicalAudit(a)); want != a.Hash {
			return count, fmt.Errorf("audit chain broken at seq %d: hash mismatch", a.Seq)
		}
		prev = a.Hash
		count++
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	return count, nil
}

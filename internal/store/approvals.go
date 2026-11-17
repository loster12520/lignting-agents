package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// ApprovalFilter 是审批列表过滤条件。
type ApprovalFilter struct {
	Status string
	Kind   string
	Limit  int
}

const approvalColumns = "id, kind, title, payload, risk, status, requested_by, decided_by, decision_reason, scope, expires_at, created_at, decided_at"

func scanApproval(row interface{ Scan(...any) error }) (domain.Approval, error) {
	var a domain.Approval
	err := row.Scan(&a.ID, &a.Kind, &a.Title, &a.Payload, &a.Risk, &a.Status, &a.RequestedBy,
		&a.DecidedBy, &a.DecisionReason, &a.Scope, &a.ExpiresAt, &a.CreatedAt, &a.DecidedAt)
	if err != nil {
		return domain.Approval{}, err
	}
	return a, nil
}

// CreateApproval 创建审批（G1/G2/G3/不可逆由 core 决定，store 只落库）。
func (s *Store) CreateApproval(ctx context.Context, a domain.Approval) (domain.Approval, error) {
	if a.ID == "" {
		a.ID = domain.NewID("appr")
	}
	if a.Status == "" {
		a.Status = domain.ApprovalPending
	}
	if a.Risk == "" {
		a.Risk = "medium"
	}
	if a.Scope == "" {
		a.Scope = "once"
	}
	if a.Payload == "" {
		a.Payload = "{}"
	}
	if a.CreatedAt == "" {
		a.CreatedAt = Now()
	}
	if strings.TrimSpace(a.Kind) == "" || strings.TrimSpace(a.Title) == "" {
		return domain.Approval{}, fmt.Errorf("%w: approval kind/title required", ErrInvalid)
	}
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO approvals("+approvalColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)",
			a.ID, a.Kind, a.Title, a.Payload, a.Risk, a.Status, a.RequestedBy, a.DecidedBy,
			a.DecisionReason, a.Scope, a.ExpiresAt, a.CreatedAt, a.DecidedAt); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return fmt.Errorf("%w: approval %s", ErrConflict, a.ID)
			}
			return fmt.Errorf("create approval: %w", err)
		}
		// 审批 TTL：到期只把审批标记 blocked/expired 并通知，绝不自动执行（FR-APPR）。
		if a.Status == domain.ApprovalPending && a.ExpiresAt != "" {
			if _, err := insertScheduleTx(tx, Schedule{
				Kind: "approval.expire", DueAt: a.ExpiresAt,
				Payload: marshalJSON(map[string]any{"approval_id": a.ID}),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.Approval{}, err
	}
	return a, nil
}

// ExpireApproval 把 pending 审批标记为 expired（TTL 到期，幂等，不自动执行）。
// changed=false 表示已处理过或不存在。
func (s *Store) ExpireApproval(ctx context.Context, id string) (domain.Approval, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return domain.Approval{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := scanApproval(tx.QueryRow("SELECT "+approvalColumns+" FROM approvals WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Approval{}, false, fmt.Errorf("%w: approval %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Approval{}, false, fmt.Errorf("get approval: %w", err)
	}
	if a.Status != domain.ApprovalPending {
		return a, false, nil
	}
	a.Status = domain.ApprovalExpired
	a.DecidedAt = Now()
	a.DecisionReason = "审批 TTL 到期未处理，标记阻塞（不自动执行）"
	if _, err := tx.Exec("UPDATE approvals SET status=?, decided_at=?, decision_reason=? WHERE id=? AND status=?",
		a.Status, a.DecidedAt, a.DecisionReason, id, domain.ApprovalPending); err != nil {
		return domain.Approval{}, false, fmt.Errorf("expire approval: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Approval{}, false, fmt.Errorf("commit expire: %w", err)
	}
	return a, true, nil
}

// GetApproval 按 ID 读取审批。
func (s *Store) GetApproval(ctx context.Context, id string) (domain.Approval, error) {
	a, err := scanApproval(s.DB.QueryRowContext(ctx, "SELECT "+approvalColumns+" FROM approvals WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Approval{}, fmt.Errorf("%w: approval %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Approval{}, fmt.Errorf("get approval: %w", err)
	}
	return a, nil
}

// ListApprovals 列出审批（created_at 倒序）。
func (s *Store) ListApprovals(ctx context.Context, f ApprovalFilter) ([]domain.Approval, error) {
	var (
		where []string
		args  []any
	)
	if f.Status != "" {
		where = append(where, "status=?")
		args = append(args, f.Status)
	}
	if f.Kind != "" {
		where = append(where, "kind=?")
		args = append(args, f.Kind)
	}
	q := "SELECT " + approvalColumns + " FROM approvals"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC"
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q += " LIMIT ?"
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Approval, 0, limit)
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, fmt.Errorf("scan approval: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DecideApproval 裁决 pending 审批；已处理/过期返回冲突（不重复裁决）。
func (s *Store) DecideApproval(ctx context.Context, id, decision, decidedBy, reason string) (domain.Approval, error) {
	if decision != domain.ApprovalApproved && decision != domain.ApprovalRejected {
		return domain.Approval{}, fmt.Errorf("%w: decision must be approved/rejected", ErrInvalid)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return domain.Approval{}, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := scanApproval(tx.QueryRow("SELECT "+approvalColumns+" FROM approvals WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Approval{}, fmt.Errorf("%w: approval %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Approval{}, fmt.Errorf("get approval: %w", err)
	}
	if a.Status != domain.ApprovalPending {
		return domain.Approval{}, fmt.Errorf("%w: approval already %s", ErrConflict, a.Status)
	}
	a.Status = decision
	a.DecidedBy = decidedBy
	a.DecisionReason = reason
	a.DecidedAt = Now()
	if _, err := tx.Exec("UPDATE approvals SET status=?, decided_by=?, decision_reason=?, decided_at=? WHERE id=? AND status=?",
		a.Status, a.DecidedBy, a.DecisionReason, a.DecidedAt, id, domain.ApprovalPending); err != nil {
		return domain.Approval{}, fmt.Errorf("decide approval: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Approval{}, fmt.Errorf("commit approval: %w", err)
	}
	return a, nil
}

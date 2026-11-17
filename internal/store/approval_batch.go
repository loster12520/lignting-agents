package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lignting/lignting-agents/internal/domain"
)

// DecideApprovalsBatch 在单事务内裁决多条审批；任一不可裁决则整体回滚（不部分执行）。
func (s *Store) DecideApprovalsBatch(ctx context.Context, ids []string, decision, decidedBy, reason string) ([]domain.Approval, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: approvals required", ErrInvalid)
	}
	if decision != domain.ApprovalApproved && decision != domain.ApprovalRejected {
		return nil, fmt.Errorf("%w: decision must be approved/rejected", ErrInvalid)
	}
	out := make([]domain.Approval, 0, len(ids))
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now := Now()
		for _, id := range ids {
			a, err := scanApproval(tx.QueryRow("SELECT "+approvalColumns+" FROM approvals WHERE id=?", id))
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: approval %s", ErrNotFound, id)
			}
			if err != nil {
				return fmt.Errorf("get approval: %w", err)
			}
			if a.Status != domain.ApprovalPending {
				return fmt.Errorf("%w: approval %s already %s", ErrConflict, id, a.Status)
			}
			a.Status = decision
			a.DecidedBy = decidedBy
			a.DecisionReason = reason
			a.DecidedAt = now
			if _, err := tx.Exec("UPDATE approvals SET status=?, decided_by=?, decision_reason=?, decided_at=? WHERE id=? AND status=?",
				a.Status, a.DecidedBy, a.DecisionReason, a.DecidedAt, id, domain.ApprovalPending); err != nil {
				return fmt.Errorf("batch decide approval: %w", err)
			}
			out = append(out, a)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

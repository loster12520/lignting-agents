package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lignting/lignting-agents/internal/domain"
)

const taskColumns = "id, title, description, status, department_id, owner_id, reviewer_id, approver_id, perspective, priority, budget_tokens, budget_cost, deadline, parent_id, source_message_id, dor, dod, flags, created_by, created_at, updated_at"

// GetTask 读取任务（core 做 Policy/上下文装配用；状态机写入在 core）。
func (s *Store) GetTask(ctx context.Context, id string) (domain.Task, error) {
	var t domain.Task
	err := s.DB.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM tasks WHERE id=?", id).
		Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.DepartmentID, &t.OwnerID, &t.ReviewerID,
			&t.ApproverID, &t.Perspective, &t.Priority, &t.BudgetTokens, &t.BudgetCost, &t.Deadline,
			&t.ParentID, &t.SourceMessageID, &t.DoR, &t.DoD, &t.Flags, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Task{}, fmt.Errorf("%w: task %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Task{}, fmt.Errorf("get task: %w", err)
	}
	return t, nil
}

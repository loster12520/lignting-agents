package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// AgentFilter 是 Agent 列表过滤条件（空字段不过滤）。
type AgentFilter struct {
	DepartmentID string
	Kind         string
	Status       string
	Limit        int
}

const agentColumns = "id, name, kind, department_id, role_card, perspective, status, model, token_hash, budget_limit, current_task_id, created_at, updated_at"

func scanAgent(row interface{ Scan(...any) error }) (domain.Agent, error) {
	var a domain.Agent
	err := row.Scan(&a.ID, &a.Name, &a.Kind, &a.DepartmentID, &a.RoleCard, &a.Perspective,
		&a.Status, &a.Model, &a.TokenHash, &a.BudgetLimit, &a.CurrentTaskID, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return domain.Agent{}, err
	}
	return a, nil
}

// NormalizeAgent 补齐默认值；store 不做角色/业务校验。
func NormalizeAgent(a *domain.Agent) {
	if a.ID == "" {
		a.ID = domain.NewID("agent")
	}
	if a.Kind == "" {
		a.Kind = domain.AgentKindEmployee
	}
	if a.Status == "" {
		a.Status = domain.AgentIdle
	}
	if a.Model == "" {
		a.Model = "deepseek-flash"
	}
	if a.RoleCard == "" {
		a.RoleCard = "{}"
	}
	now := Now()
	if a.CreatedAt == "" {
		a.CreatedAt = now
	}
	if a.UpdatedAt == "" {
		a.UpdatedAt = now
	}
}

// CreateAgentTx 在调用方事务内创建 Agent。
func CreateAgentTx(tx *sql.Tx, a domain.Agent) error {
	NormalizeAgent(&a)
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("%w: agent name required", ErrInvalid)
	}
	if _, err := tx.Exec("INSERT INTO agents(id, name, kind, department_id, role_card, perspective, status, model, token_hash, budget_limit, current_task_id, created_at, updated_at) "+
		"VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)",
		a.ID, a.Name, a.Kind, a.DepartmentID, a.RoleCard, a.Perspective, a.Status, a.Model,
		a.TokenHash, a.BudgetLimit, a.CurrentTaskID, a.CreatedAt, a.UpdatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("%w: agent %s", ErrConflict, a.ID)
		}
		return fmt.Errorf("create agent: %w", err)
	}
	return nil
}

// CreateAgent 创建 Agent 并返回回填后的值。
func (s *Store) CreateAgent(ctx context.Context, a domain.Agent) (domain.Agent, error) {
	NormalizeAgent(&a)
	if err := s.WithTx(ctx, func(tx *sql.Tx) error { return CreateAgentTx(tx, a) }); err != nil {
		return domain.Agent{}, err
	}
	return a, nil
}

// GetAgent 按 ID 读取 Agent。
func (s *Store) GetAgent(ctx context.Context, id string) (domain.Agent, error) {
	a, err := scanAgent(s.DB.QueryRowContext(ctx, "SELECT "+agentColumns+" FROM agents WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Agent{}, fmt.Errorf("%w: agent %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Agent{}, fmt.Errorf("get agent: %w", err)
	}
	return a, nil
}

// ListAgentsByDepartment 返回部门内所有 Agent（含主管/员工）。
func (s *Store) ListAgentsByDepartment(ctx context.Context, departmentID string) ([]domain.Agent, error) {
	return s.ListAgents(ctx, AgentFilter{DepartmentID: departmentID})
}

// ListAgentsByKind 返回指定类型的 Agent（core 找助理/主管用）。
func (s *Store) ListAgentsByKind(ctx context.Context, kind string) ([]domain.Agent, error) {
	return s.ListAgents(ctx, AgentFilter{Kind: kind})
}

// ListAgents 按过滤条件列出 Agent。
func (s *Store) ListAgents(ctx context.Context, f AgentFilter) ([]domain.Agent, error) {
	var (
		where []string
		args  []any
	)
	if f.DepartmentID != "" {
		where = append(where, "department_id=?")
		args = append(args, f.DepartmentID)
	}
	if f.Kind != "" {
		where = append(where, "kind=?")
		args = append(args, f.Kind)
	}
	if f.Status != "" {
		where = append(where, "status=?")
		args = append(args, f.Status)
	}
	q := "SELECT " + agentColumns + " FROM agents"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at, id"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Agent, 0, 8)
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateAgent 更新可编辑字段；token_hash 用 SetAgentTokenHash。
func (s *Store) UpdateAgent(ctx context.Context, a domain.Agent) error {
	a.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, "UPDATE agents SET name=?, kind=?, department_id=?, role_card=?, perspective=?, status=?, model=?, budget_limit=?, current_task_id=?, updated_at=? WHERE id=?",
		a.Name, a.Kind, a.DepartmentID, a.RoleCard, a.Perspective, a.Status, a.Model, a.BudgetLimit, a.CurrentTaskID, a.UpdatedAt, a.ID)
	if err != nil {
		return fmt.Errorf("update agent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: agent %s", ErrNotFound, a.ID)
	}
	return nil
}

// UpdateAgentStatus 只更新状态（core 负责同时 append agent.status 事件）。
func (s *Store) UpdateAgentStatus(ctx context.Context, id, status string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE agents SET status=?, updated_at=? WHERE id=?", status, Now(), id)
	if err != nil {
		return fmt.Errorf("update agent status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: agent %s", ErrNotFound, id)
	}
	return nil
}

// SetAgentTokenHash 保存 Agent Token 哈希（明文只在创建时返回一次）。
func (s *Store) SetAgentTokenHash(ctx context.Context, id, tokenHash string) error {
	if _, err := s.DB.ExecContext(ctx, "UPDATE agents SET token_hash=?, updated_at=? WHERE id=?", tokenHash, Now(), id); err != nil {
		return fmt.Errorf("set agent token hash: %w", err)
	}
	return nil
}

// FindAgentByTokenHash 按 Token 哈希查 Agent（Agent 身份鉴权用；不能复用人类 session）。
func (s *Store) FindAgentByTokenHash(ctx context.Context, tokenHash string) (domain.Agent, error) {
	if tokenHash == "" {
		return domain.Agent{}, fmt.Errorf("%w: empty token hash", ErrInvalid)
	}
	a, err := scanAgent(s.DB.QueryRowContext(ctx, "SELECT "+agentColumns+" FROM agents WHERE token_hash=? LIMIT 1", tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Agent{}, fmt.Errorf("%w: agent token", ErrNotFound)
	}
	if err != nil {
		return domain.Agent{}, fmt.Errorf("find agent by token: %w", err)
	}
	return a, nil
}

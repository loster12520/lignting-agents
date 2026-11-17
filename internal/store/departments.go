package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// DepartmentFilter 是部门列表过滤条件。
type DepartmentFilter struct {
	Status string
	Kind   string
	Limit  int
}

const departmentColumns = "id, name, kind, mission, status, charter, created_at, updated_at"

func scanDepartment(row interface{ Scan(...any) error }) (domain.Department, error) {
	var d domain.Department
	err := row.Scan(&d.ID, &d.Name, &d.Kind, &d.Mission, &d.Status, &d.Charter, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return domain.Department{}, err
	}
	return d, nil
}

// NormalizeDepartment 补齐默认值；业务校验（模板/必要职责）在 core。
func NormalizeDepartment(d *domain.Department) {
	if d.ID == "" {
		d.ID = domain.NewID("dept")
	}
	if d.Status == "" {
		d.Status = domain.DeptActive
	}
	if d.Charter == "" {
		d.Charter = "{}"
	}
	now := Now()
	if d.CreatedAt == "" {
		d.CreatedAt = now
	}
	if d.UpdatedAt == "" {
		d.UpdatedAt = now
	}
}

// CreateDepartment 创建部门（审计由调用方按业务动作写）。
func (s *Store) CreateDepartment(ctx context.Context, d domain.Department) (domain.Department, error) {
	NormalizeDepartment(&d)
	if strings.TrimSpace(d.Name) == "" {
		return domain.Department{}, fmt.Errorf("%w: department name required", ErrInvalid)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO departments("+departmentColumns+") VALUES(?,?,?,?,?,?,?,?)",
		d.ID, d.Name, d.Kind, d.Mission, d.Status, d.Charter, d.CreatedAt, d.UpdatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.Department{}, fmt.Errorf("%w: department %s", ErrConflict, d.ID)
		}
		return domain.Department{}, fmt.Errorf("create department: %w", err)
	}
	return d, nil
}

// GetDepartment 按 ID 读取部门。
func (s *Store) GetDepartment(ctx context.Context, id string) (domain.Department, error) {
	d, err := scanDepartment(s.DB.QueryRowContext(ctx, "SELECT "+departmentColumns+" FROM departments WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Department{}, fmt.Errorf("%w: department %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Department{}, fmt.Errorf("get department: %w", err)
	}
	return d, nil
}

// ListDepartmentsByStatus 返回指定状态的部门（core R3 用）。
func (s *Store) ListDepartmentsByStatus(ctx context.Context, status string) ([]domain.Department, error) {
	return s.ListDepartments(ctx, DepartmentFilter{Status: status})
}

// ListDepartments 按过滤条件列出部门。
func (s *Store) ListDepartments(ctx context.Context, f DepartmentFilter) ([]domain.Department, error) {
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
	q := "SELECT " + departmentColumns + " FROM departments"
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
		return nil, fmt.Errorf("list departments: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Department, 0, 4)
	for rows.Next() {
		d, err := scanDepartment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan department: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDepartment 更新部门可编辑字段。
func (s *Store) UpdateDepartment(ctx context.Context, d domain.Department) error {
	d.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, "UPDATE departments SET name=?, kind=?, mission=?, status=?, charter=?, updated_at=? WHERE id=?",
		d.Name, d.Kind, d.Mission, d.Status, d.Charter, d.UpdatedAt, d.ID)
	if err != nil {
		return fmt.Errorf("update department: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: department %s", ErrNotFound, d.ID)
	}
	return nil
}

// UpdateDepartmentStatus 只更新部门状态（active/paused/archived）。
func (s *Store) UpdateDepartmentStatus(ctx context.Context, id, status string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE departments SET status=?, updated_at=? WHERE id=?", status, Now(), id)
	if err != nil {
		return fmt.Errorf("update department status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: department %s", ErrNotFound, id)
	}
	return nil
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// R3 组织/蓝图/Provisioning 仓储。ID 由 core 确定性生成
// （approval = appr_<blueprintID>，provisioning = prov_<blueprintID>），store 不重新生成。

const blueprintColumns = "id, intent_id, content, status, created_at, approved_at"
const provisioningColumns = "id, blueprint_id, status, steps, error, created_at, updated_at"

func scanBlueprint(row interface{ Scan(...any) error }) (domain.Blueprint, error) {
	var b domain.Blueprint
	err := row.Scan(&b.ID, &b.IntentID, &b.Content, &b.Status, &b.CreatedAt, &b.ApprovedAt)
	if err != nil {
		return domain.Blueprint{}, err
	}
	return b, nil
}

func scanProvisioning(row interface{ Scan(...any) error }) (domain.Provisioning, error) {
	var p domain.Provisioning
	err := row.Scan(&p.ID, &p.BlueprintID, &p.Status, &p.Steps, &p.Error, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return domain.Provisioning{}, err
	}
	return p, nil
}

// GetIntentContent 返回最新一版意图卡（core 用于生成蓝图）。
func (s *Store) GetIntentContent(ctx context.Context) (domain.Intent, error) {
	var it domain.Intent
	err := s.DB.QueryRowContext(ctx, "SELECT id, version, content, status, created_at FROM intents ORDER BY version DESC, created_at DESC LIMIT 1").
		Scan(&it.ID, &it.Version, &it.Content, &it.Status, &it.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Intent{}, fmt.Errorf("%w: intent", ErrNotFound)
	}
	if err != nil {
		return domain.Intent{}, fmt.Errorf("get intent content: %w", err)
	}
	return it, nil
}

// CreateBlueprint 创建蓝图（ID 必须由 core 提供，store 不生成）。
func (s *Store) CreateBlueprint(ctx context.Context, bp domain.Blueprint) (domain.Blueprint, error) {
	if strings.TrimSpace(bp.ID) == "" {
		return domain.Blueprint{}, fmt.Errorf("%w: blueprint id required", ErrInvalid)
	}
	if strings.TrimSpace(bp.Content) != "" && !json.Valid([]byte(bp.Content)) {
		return domain.Blueprint{}, fmt.Errorf("%w: blueprint content must be JSON", ErrInvalid)
	}
	if bp.Content == "" {
		bp.Content = "{}"
	}
	if bp.Status == "" {
		bp.Status = domain.BlueprintProposed
	}
	if bp.CreatedAt == "" {
		bp.CreatedAt = Now()
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO blueprints("+blueprintColumns+") VALUES(?,?,?,?,?,?)",
		bp.ID, bp.IntentID, bp.Content, bp.Status, bp.CreatedAt, bp.ApprovedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.Blueprint{}, fmt.Errorf("%w: blueprint %s", ErrConflict, bp.ID)
		}
		return domain.Blueprint{}, fmt.Errorf("create blueprint: %w", err)
	}
	return bp, nil
}

// GetBlueprint 按 ID 读取蓝图。
func (s *Store) GetBlueprint(ctx context.Context, id string) (domain.Blueprint, error) {
	bp, err := scanBlueprint(s.DB.QueryRowContext(ctx, "SELECT "+blueprintColumns+" FROM blueprints WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Blueprint{}, fmt.Errorf("%w: blueprint %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Blueprint{}, fmt.Errorf("get blueprint: %w", err)
	}
	return bp, nil
}

// ListBlueprintsByStatus 按状态列出蓝图（POST /api/departments 校验 G1 用）。
func (s *Store) ListBlueprintsByStatus(ctx context.Context, status string) ([]domain.Blueprint, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+blueprintColumns+" FROM blueprints WHERE status=? ORDER BY created_at DESC", status)
	if err != nil {
		return nil, fmt.Errorf("list blueprints: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Blueprint, 0, 4)
	for rows.Next() {
		bp, err := scanBlueprint(rows)
		if err != nil {
			return nil, fmt.Errorf("scan blueprint: %w", err)
		}
		out = append(out, bp)
	}
	return out, rows.Err()
}

// UpdateBlueprintStatus 更新蓝图状态；approvedAt 为空时保留原值。
func (s *Store) UpdateBlueprintStatus(ctx context.Context, id, status, approvedAt string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(status) == "" {
		return fmt.Errorf("%w: blueprint id/status required", ErrInvalid)
	}
	var res sql.Result
	var err error
	if approvedAt != "" {
		res, err = s.DB.ExecContext(ctx, "UPDATE blueprints SET status=?, approved_at=? WHERE id=?", status, approvedAt, id)
	} else {
		res, err = s.DB.ExecContext(ctx, "UPDATE blueprints SET status=? WHERE id=?", status, id)
	}
	if err != nil {
		return fmt.Errorf("update blueprint status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: blueprint %s", ErrNotFound, id)
	}
	return nil
}

// CreateProvisioning 创建 provisioning（ID 由 core 确定性生成，store 不生成）。
func (s *Store) CreateProvisioning(ctx context.Context, p domain.Provisioning) (domain.Provisioning, error) {
	if strings.TrimSpace(p.ID) == "" {
		return domain.Provisioning{}, fmt.Errorf("%w: provisioning id required", ErrInvalid)
	}
	if strings.TrimSpace(p.BlueprintID) == "" {
		return domain.Provisioning{}, fmt.Errorf("%w: blueprint_id required", ErrInvalid)
	}
	if p.Status == "" {
		p.Status = domain.ProvisionPlanned
	}
	if p.Steps == "" {
		p.Steps = "[]"
	}
	now := Now()
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	if p.UpdatedAt == "" {
		p.UpdatedAt = now
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO provisioning("+provisioningColumns+") VALUES(?,?,?,?,?,?,?)",
		p.ID, p.BlueprintID, p.Status, p.Steps, p.Error, p.CreatedAt, p.UpdatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.Provisioning{}, fmt.Errorf("%w: provisioning %s", ErrConflict, p.ID)
		}
		return domain.Provisioning{}, fmt.Errorf("create provisioning: %w", err)
	}
	return p, nil
}

// GetProvisioning 按 ID 读取 provisioning。
func (s *Store) GetProvisioning(ctx context.Context, id string) (domain.Provisioning, error) {
	p, err := scanProvisioning(s.DB.QueryRowContext(ctx, "SELECT "+provisioningColumns+" FROM provisioning WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Provisioning{}, fmt.Errorf("%w: provisioning %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Provisioning{}, fmt.Errorf("get provisioning: %w", err)
	}
	return p, nil
}

// UpdateProvisioning 更新 provisioning 状态/步骤/错误（幂等重试与回滚用）。
func (s *Store) UpdateProvisioning(ctx context.Context, p domain.Provisioning) error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("%w: provisioning id required", ErrInvalid)
	}
	if p.Steps == "" {
		p.Steps = "[]"
	}
	p.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, "UPDATE provisioning SET blueprint_id=?, status=?, steps=?, error=?, updated_at=? WHERE id=?",
		p.BlueprintID, p.Status, p.Steps, p.Error, p.UpdatedAt, p.ID)
	if err != nil {
		return fmt.Errorf("update provisioning: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: provisioning %s", ErrNotFound, p.ID)
	}
	return nil
}

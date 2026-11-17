package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// R7 记忆仓储：实现 memory.Repo 的 12 个方法（只依赖 domain，不 import internal/memory）。
// 契约见 internal/memory/README.md §2：
//   - UpdateMemory 乐观锁 version；0 行→ErrConflict（不存在→ErrNotFound）
//   - GetMemory 未命中→ErrNotFound
//   - AppendMemoryEvent 的 Detail 逐字保存（压缩快照原文，禁止截断）
//   - ListMemories 的 Tags 为“包含任一”；IncludeDeleted/IncludeExpired 语义见下

const memoryColumns = "id, scope, department_id, agent_id, entity_type, entity_key, content, summary, tags, confidence, evidence, sensitivity, visibility, valid_from, valid_to, supersedes, version, status, created_by, created_at, updated_at, deleted_at"

const manualColumns = "id, scope, department_id, agent_id, version, content, status, diff, created_by, created_at"

func scanMemory(row interface{ Scan(...any) error }) (domain.Memory, error) {
	var m domain.Memory
	err := row.Scan(&m.ID, &m.Scope, &m.DepartmentID, &m.AgentID, &m.EntityType, &m.EntityKey,
		&m.Content, &m.Summary, &m.Tags, &m.Confidence, &m.Evidence, &m.Sensitivity, &m.Visibility,
		&m.ValidFrom, &m.ValidTo, &m.Supersedes, &m.Version, &m.Status, &m.CreatedBy,
		&m.CreatedAt, &m.UpdatedAt, &m.DeletedAt)
	if err != nil {
		return domain.Memory{}, err
	}
	return m, nil
}

func normalizeMemory(m *domain.Memory) {
	if m.Confidence == "" {
		m.Confidence = "medium"
	}
	if m.Evidence == "" {
		m.Evidence = "[]"
	}
	if m.Sensitivity == "" {
		m.Sensitivity = "normal"
	}
	if m.Visibility == "" {
		m.Visibility = domain.MemoryTeam
	}
	if m.Status == "" {
		m.Status = domain.MemoryStatusCandidate
	}
	if m.Version <= 0 {
		m.Version = 1
	}
	now := Now()
	if m.CreatedAt == "" {
		m.CreatedAt = now
	}
	if m.UpdatedAt == "" {
		m.UpdatedAt = now
	}
}

// InsertMemory 插入一条记忆（时间/版本由调用方传入，store 只补齐缺省值）。
func (s *Store) InsertMemory(ctx context.Context, m domain.Memory) error {
	if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.Scope) == "" || strings.TrimSpace(m.Content) == "" {
		return fmt.Errorf("%w: memory requires id/scope/content", ErrInvalid)
	}
	normalizeMemory(&m)
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO memories("+memoryColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		m.ID, m.Scope, m.DepartmentID, m.AgentID, m.EntityType, m.EntityKey, m.Content, m.Summary,
		m.Tags, m.Confidence, m.Evidence, m.Sensitivity, m.Visibility, m.ValidFrom, m.ValidTo,
		m.Supersedes, m.Version, m.Status, m.CreatedBy, m.CreatedAt, m.UpdatedAt, m.DeletedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("%w: memory %s", ErrConflict, m.ID)
		}
		return fmt.Errorf("insert memory: %w", err)
	}
	return nil
}

// GetMemory 按 ID 读取记忆（含墓碑行，可见性/墓碑拦截在 memory.Service）。
func (s *Store) GetMemory(ctx context.Context, id string) (domain.Memory, error) {
	m, err := scanMemory(s.DB.QueryRowContext(ctx, "SELECT "+memoryColumns+" FROM memories WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Memory{}, fmt.Errorf("%w: memory %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Memory{}, fmt.Errorf("get memory: %w", err)
	}
	return m, nil
}

// UpdateMemory 乐观锁更新；版本不符返回 ErrConflict，不存在返回 ErrNotFound。
func (s *Store) UpdateMemory(ctx context.Context, m domain.Memory, expectedVersion int) error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("%w: memory id required", ErrInvalid)
	}
	if m.Evidence == "" {
		m.Evidence = "[]"
	}
	if m.Status == "" {
		m.Status = domain.MemoryStatusCandidate
	}
	res, err := s.DB.ExecContext(ctx,
		"UPDATE memories SET scope=?, department_id=?, agent_id=?, entity_type=?, entity_key=?, content=?, summary=?, tags=?, confidence=?, evidence=?, sensitivity=?, visibility=?, valid_from=?, valid_to=?, supersedes=?, version=?, status=?, updated_at=?, deleted_at=? WHERE id=? AND version=?",
		m.Scope, m.DepartmentID, m.AgentID, m.EntityType, m.EntityKey, m.Content, m.Summary, m.Tags,
		m.Confidence, m.Evidence, m.Sensitivity, m.Visibility, m.ValidFrom, m.ValidTo, m.Supersedes,
		m.Version, m.Status, Now(), m.DeletedAt, m.ID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update memory: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return nil
	}
	var one int
	err = s.DB.QueryRowContext(ctx, "SELECT 1 FROM memories WHERE id=?", m.ID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: memory %s", ErrNotFound, m.ID)
	}
	if err != nil {
		return fmt.Errorf("check memory: %w", err)
	}
	return fmt.Errorf("%w: memory %s version mismatch (expected %d)", ErrConflict, m.ID, expectedVersion)
}

func escapeLikeBang(s string) string {
	s = strings.ReplaceAll(s, "!", "!!")
	s = strings.ReplaceAll(s, "%", "!%")
	s = strings.ReplaceAll(s, "_", "!_")
	return s
}

func memoryWhere(f domain.MemoryFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0, 12)
	add := func(cond string, v any) {
		where = append(where, cond)
		args = append(args, v)
	}
	if f.Scope != "" {
		add("scope=?", f.Scope)
	}
	if f.DepartmentID != "" {
		add("department_id=?", f.DepartmentID)
	}
	if f.AgentID != "" {
		add("agent_id=?", f.AgentID)
	}
	if f.EntityType != "" {
		add("entity_type=?", f.EntityType)
	}
	if f.EntityKey != "" {
		add("entity_key=?", f.EntityKey)
	}
	if f.CreatedBy != "" {
		add("created_by=?", f.CreatedBy)
	}
	if f.CreatedSince != "" {
		add("created_at >= ?", f.CreatedSince)
	}
	if f.CreatedBefore != "" {
		add("created_at <= ?", f.CreatedBefore)
	}
	if len(f.Status) > 0 {
		ph := make([]string, len(f.Status))
		for i, st := range f.Status {
			ph[i] = "?"
			args = append(args, st)
		}
		where = append(where, "status IN ("+strings.Join(ph, ",")+")")
	}
	if len(f.IDs) > 0 {
		ph := make([]string, len(f.IDs))
		for i, id := range f.IDs {
			ph[i] = "?"
			args = append(args, id)
		}
		where = append(where, "id IN ("+strings.Join(ph, ",")+")")
	}
	if len(f.Tags) > 0 {
		ors := make([]string, 0, len(f.Tags))
		for _, tag := range f.Tags {
			if strings.TrimSpace(tag) == "" {
				continue
			}
			ors = append(ors, "tags LIKE ? ESCAPE '!'")
			args = append(args, "%"+escapeLikeBang(tag)+"%")
		}
		if len(ors) > 0 {
			where = append(where, "("+strings.Join(ors, " OR ")+")")
		}
	}
	if !f.IncludeDeleted {
		where = append(where, "status<>'deleted'", "deleted_at=''")
	}
	if !f.IncludeExpired {
		where = append(where, "(valid_to='' OR valid_to > ?)")
		args = append(args, Now())
	}
	return strings.Join(where, " AND "), args
}

// ListMemories 按 Filter 查询（tags 任一匹配；默认排除墓碑与过期）。
func (s *Store) ListMemories(ctx context.Context, f domain.MemoryFilter) ([]domain.Memory, error) {
	where, args := memoryWhere(f)
	q := "SELECT " + memoryColumns + " FROM memories WHERE " + where + " ORDER BY created_at DESC, id DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
		if f.Offset > 0 {
			q += " OFFSET ?"
			args = append(args, f.Offset)
		}
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Memory, 0, 16)
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountMemories 统计符合条件的记忆数（额度用）。
func (s *Store) CountMemories(ctx context.Context, f domain.MemoryFilter) (int, error) {
	where, args := memoryWhere(f)
	var n int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories WHERE "+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count memories: %w", err)
	}
	return n, nil
}

// AppendMemoryEvent 写记忆审计事件；Detail 逐字保存，不截断。
func (s *Store) AppendMemoryEvent(ctx context.Context, ev domain.MemoryEvent) error {
	if strings.TrimSpace(ev.MemoryID) == "" || strings.TrimSpace(ev.Action) == "" {
		return fmt.Errorf("%w: memory event requires memory_id/action", ErrInvalid)
	}
	if ev.CreatedAt == "" {
		ev.CreatedAt = Now()
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO memory_events(memory_id, action, actor_type, actor_id, detail, created_at) VALUES(?,?,?,?,?,?)",
		ev.MemoryID, ev.Action, ev.ActorType, ev.ActorID, ev.Detail, ev.CreatedAt); err != nil {
		return fmt.Errorf("append memory event: %w", err)
	}
	return nil
}

var memoryReaderTypes = map[string]bool{
	"human": true, "assistant": true, "supervisor": true, "employee": true, "system": true,
}

// AddMemoryRead 写读取留痕（reader_type 枚举校验）。
func (s *Store) AddMemoryRead(ctx context.Context, r domain.MemoryRead) error {
	if !memoryReaderTypes[r.ReaderType] {
		return fmt.Errorf("%w: unknown reader_type %s", ErrInvalid, r.ReaderType)
	}
	if strings.TrimSpace(r.MemoryID) == "" || strings.TrimSpace(r.ReaderID) == "" {
		return fmt.Errorf("%w: memory read requires memory_id/reader_id", ErrInvalid)
	}
	if r.CreatedAt == "" {
		r.CreatedAt = Now()
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO memory_reads(memory_id, reader_type, reader_id, turn_id, created_at) VALUES(?,?,?,?,?)",
		r.MemoryID, r.ReaderType, r.ReaderID, r.TurnID, r.CreatedAt); err != nil {
		return fmt.Errorf("add memory read: %w", err)
	}
	return nil
}

// MaxManualVersion 返回某本手册的当前最大版本（空为 0）。
func (s *Store) MaxManualVersion(ctx context.Context, key domain.MemoryManualKey) (int, error) {
	var v int
	if err := s.DB.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version),0) FROM prompt_manuals WHERE scope=? AND department_id=? AND agent_id=?",
		key.Scope, key.DepartmentID, key.AgentID).Scan(&v); err != nil {
		return 0, fmt.Errorf("max manual version: %w", err)
	}
	return v, nil
}

// ListManuals 列出手册版本（version 倒序）。
func (s *Store) ListManuals(ctx context.Context, key domain.MemoryManualKey) ([]domain.PromptManual, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+manualColumns+" FROM prompt_manuals WHERE scope=? AND department_id=? AND agent_id=? ORDER BY version DESC",
		key.Scope, key.DepartmentID, key.AgentID)
	if err != nil {
		return nil, fmt.Errorf("list manuals: %w", err)
	}
	defer rows.Close()
	out := make([]domain.PromptManual, 0, 8)
	for rows.Next() {
		m, err := scanManual(rows)
		if err != nil {
			return nil, fmt.Errorf("scan manual: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanManual(row interface{ Scan(...any) error }) (domain.PromptManual, error) {
	var m domain.PromptManual
	err := row.Scan(&m.ID, &m.Scope, &m.DepartmentID, &m.AgentID, &m.Version, &m.Content,
		&m.Status, &m.Diff, &m.CreatedBy, &m.CreatedAt)
	if err != nil {
		return domain.PromptManual{}, err
	}
	return m, nil
}

// GetManual 按 ID 读取手册版本。
func (s *Store) GetManual(ctx context.Context, id string) (domain.PromptManual, error) {
	m, err := scanManual(s.DB.QueryRowContext(ctx, "SELECT "+manualColumns+" FROM prompt_manuals WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PromptManual{}, fmt.Errorf("%w: manual %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.PromptManual{}, fmt.Errorf("get manual: %w", err)
	}
	return m, nil
}

// InsertManual 插入手册版本。
func (s *Store) InsertManual(ctx context.Context, m domain.PromptManual) error {
	if strings.TrimSpace(m.ID) == "" || m.Version <= 0 || strings.TrimSpace(m.Scope) == "" {
		return fmt.Errorf("%w: manual requires id/scope/version", ErrInvalid)
	}
	if m.Status == "" {
		m.Status = "proposed"
	}
	if m.CreatedAt == "" {
		m.CreatedAt = Now()
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO prompt_manuals("+manualColumns+") VALUES(?,?,?,?,?,?,?,?,?,?)",
		m.ID, m.Scope, m.DepartmentID, m.AgentID, m.Version, m.Content, m.Status, m.Diff, m.CreatedBy, m.CreatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("%w: manual %s", ErrConflict, m.ID)
		}
		return fmt.Errorf("insert manual: %w", err)
	}
	return nil
}

// UpdateManualStatus 更新手册状态（proposed/approved/rejected/superseded）。
func (s *Store) UpdateManualStatus(ctx context.Context, id, status string) error {
	if strings.TrimSpace(status) == "" {
		return fmt.Errorf("%w: manual status required", ErrInvalid)
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE prompt_manuals SET status=? WHERE id=?", status, id)
	if err != nil {
		return fmt.Errorf("update manual status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: manual %s", ErrNotFound, id)
	}
	return nil
}

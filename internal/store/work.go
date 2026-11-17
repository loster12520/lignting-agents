package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// R4 任务/产出物/评审/决策仓储。业务规则（状态机/DoR/环检测/权限/证据门）在 core；
// store 只保证：不可变版本、task_events 按 task_id 的 hash chain、决策只增不改。

const criterionColumns = "id, task_id, ord, text, evidence, supervisor_verdict, human_decision, created_at, updated_at"

const artifactColumns = "id, type, title, task_id, department_id, status, created_by, created_at"
const artifactVersionColumns = "id, artifact_id, version, content, content_hash, storage_path, author_id, supersedes, created_at"

const reviewColumns = "id, from_department, to_department, artifact_id, artifact_version_id, severity, stance, claim, evidence, impact, proposed_resolution, owner_id, due_at, status, thread_conversation_id, created_at"
const verdictColumns = "id, review_id, verdict, checked, rationale, conditions, residual_risk, reviewer_id, decision_id, created_at"

const decisionColumns = "id, title, status, context, options, decision, rationale, evidence, dissent, deciders, consulted, informed, affected_tasks, affected_artifacts, revisit_trigger, supersedes, superseded_by, human_ack, created_by, created_at"

// ---- Tasks ----

func normalizeTask(t *domain.Task) {
	if t.ID == "" {
		t.ID = domain.NewID("task")
	}
	if t.Status == "" {
		t.Status = "proposed"
	}
	if t.Priority == "" {
		t.Priority = "P1"
	}
	if t.DoR == "" {
		t.DoR = "{}"
	}
	if t.DoD == "" {
		t.DoD = "{}"
	}
	if t.Flags == "" {
		t.Flags = "{}"
	}
	now := Now()
	if t.CreatedAt == "" {
		t.CreatedAt = now
	}
	if t.UpdatedAt == "" {
		t.UpdatedAt = now
	}
}

func scanTask(row interface{ Scan(...any) error }) (domain.Task, error) {
	var t domain.Task
	err := row.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.DepartmentID, &t.OwnerID,
		&t.ReviewerID, &t.ApproverID, &t.Perspective, &t.Priority, &t.BudgetTokens, &t.BudgetCost,
		&t.Deadline, &t.ParentID, &t.SourceMessageID, &t.DoR, &t.DoD, &t.Flags, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return domain.Task{}, err
	}
	return t, nil
}

// CreateTask 创建任务。
func (s *Store) CreateTask(ctx context.Context, t domain.Task) (domain.Task, error) {
	normalizeTask(&t)
	if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.DepartmentID) == "" {
		return domain.Task{}, fmt.Errorf("%w: task requires title/department_id", ErrInvalid)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO tasks("+taskColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		t.ID, t.Title, t.Description, t.Status, t.DepartmentID, t.OwnerID, t.ReviewerID, t.ApproverID,
		t.Perspective, t.Priority, t.BudgetTokens, t.BudgetCost, t.Deadline, t.ParentID, t.SourceMessageID,
		t.DoR, t.DoD, t.Flags, t.CreatedBy, t.CreatedAt, t.UpdatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.Task{}, fmt.Errorf("%w: task %s", ErrConflict, t.ID)
		}
		return domain.Task{}, fmt.Errorf("create task: %w", err)
	}
	return t, nil
}

// UpdateTask 更新任务可变字段（状态转换合法性由 core 校验）。
func (s *Store) UpdateTask(ctx context.Context, t domain.Task) error {
	t.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, "UPDATE tasks SET title=?, description=?, status=?, department_id=?, owner_id=?, reviewer_id=?, approver_id=?, perspective=?, priority=?, budget_tokens=?, budget_cost=?, deadline=?, parent_id=?, source_message_id=?, dor=?, dod=?, flags=?, updated_at=? WHERE id=?",
		t.Title, t.Description, t.Status, t.DepartmentID, t.OwnerID, t.ReviewerID, t.ApproverID,
		t.Perspective, t.Priority, t.BudgetTokens, t.BudgetCost, t.Deadline, t.ParentID, t.SourceMessageID,
		t.DoR, t.DoD, t.Flags, t.UpdatedAt, t.ID)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: task %s", ErrNotFound, t.ID)
	}
	return nil
}

// ListTasksByDepartment 列出部门任务。
func (s *Store) ListTasksByDepartment(ctx context.Context, departmentID string) ([]domain.Task, error) {
	return s.listTasks(ctx, "department_id=?", departmentID)
}

// ListTasksByOwnerStatus 列出某 owner 的任务；status 为空表示全部状态。
func (s *Store) ListTasksByOwnerStatus(ctx context.Context, ownerID, status string) ([]domain.Task, error) {
	if strings.TrimSpace(status) == "" {
		return s.listTasks(ctx, "owner_id=?", ownerID)
	}
	return s.listTasks(ctx, "owner_id=? AND status=?", ownerID, status)
}

func (s *Store) listTasks(ctx context.Context, where string, args ...any) ([]domain.Task, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+taskColumns+" FROM tasks WHERE "+where+" ORDER BY created_at DESC, id DESC", args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Task, 0, 8)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- Criteria ----

func scanCriterion(row interface{ Scan(...any) error }) (domain.Criterion, error) {
	var c domain.Criterion
	err := row.Scan(&c.ID, &c.TaskID, &c.Ord, &c.Text, &c.Evidence, &c.SupervisorVerdict, &c.HumanDecision, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return domain.Criterion{}, err
	}
	return c, nil
}

// CreateTaskCriterion 新增验收标准。
func (s *Store) CreateTaskCriterion(ctx context.Context, c domain.Criterion) (domain.Criterion, error) {
	if c.ID == "" {
		c.ID = domain.NewID("crit")
	}
	if strings.TrimSpace(c.TaskID) == "" || strings.TrimSpace(c.Text) == "" {
		return domain.Criterion{}, fmt.Errorf("%w: criterion requires task_id/text", ErrInvalid)
	}
	if c.Evidence == "" {
		c.Evidence = "[]"
	}
	if c.SupervisorVerdict == "" {
		c.SupervisorVerdict = "pending"
	}
	if c.HumanDecision == "" {
		c.HumanDecision = "pending"
	}
	now := Now()
	if c.CreatedAt == "" {
		c.CreatedAt = now
	}
	if c.UpdatedAt == "" {
		c.UpdatedAt = now
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO task_criteria("+criterionColumns+") VALUES(?,?,?,?,?,?,?,?,?)",
		c.ID, c.TaskID, c.Ord, c.Text, c.Evidence, c.SupervisorVerdict, c.HumanDecision, c.CreatedAt, c.UpdatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.Criterion{}, fmt.Errorf("%w: criterion %s", ErrConflict, c.ID)
		}
		return domain.Criterion{}, fmt.Errorf("create criterion: %w", err)
	}
	return c, nil
}

// ListTaskCriteria 按 ord 返回任务验收标准。
func (s *Store) ListTaskCriteria(ctx context.Context, taskID string) ([]domain.Criterion, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+criterionColumns+" FROM task_criteria WHERE task_id=? ORDER BY ord ASC, id ASC", taskID)
	if err != nil {
		return nil, fmt.Errorf("list criteria: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Criterion, 0, 4)
	for rows.Next() {
		c, err := scanCriterion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan criterion: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateTaskCriterion 更新证据与裁决。
func (s *Store) UpdateTaskCriterion(ctx context.Context, c domain.Criterion) error {
	c.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, "UPDATE task_criteria SET text=?, evidence=?, supervisor_verdict=?, human_decision=?, updated_at=? WHERE id=?",
		c.Text, c.Evidence, c.SupervisorVerdict, c.HumanDecision, c.UpdatedAt, c.ID)
	if err != nil {
		return fmt.Errorf("update criterion: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: criterion %s", ErrNotFound, c.ID)
	}
	return nil
}

// ---- Dependencies ----

// AddTaskDependency 新增依赖（重复添加幂等，环检测在 core）。
func (s *Store) AddTaskDependency(ctx context.Context, taskID, dependsOn string) error {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(dependsOn) == "" {
		return fmt.Errorf("%w: task_id/depends_on required", ErrInvalid)
	}
	if taskID == dependsOn {
		return fmt.Errorf("%w: task cannot depend on itself", ErrInvalid)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT OR IGNORE INTO task_dependencies(task_id, depends_on, created_at) VALUES(?,?,?)", taskID, dependsOn, Now()); err != nil {
		return fmt.Errorf("add dependency: %w", err)
	}
	return nil
}

// ListTaskDependencies 返回任务依赖的 task id 列表。
func (s *Store) ListTaskDependencies(ctx context.Context, taskID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT depends_on FROM task_dependencies WHERE task_id=? ORDER BY created_at, depends_on", taskID)
	if err != nil {
		return nil, fmt.Errorf("list dependencies: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0, 4)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- Task events hash chain ----

// appendTaskEventTx 在事务内按 task_id 串 hash 链。
func appendTaskEventTx(tx *sql.Tx, ev domain.TaskEvent) (domain.TaskEvent, error) {
	if strings.TrimSpace(ev.TaskID) == "" || strings.TrimSpace(ev.Type) == "" {
		return domain.TaskEvent{}, fmt.Errorf("%w: task event requires task_id/type", ErrInvalid)
	}
	if ev.CreatedAt == "" {
		ev.CreatedAt = Now()
	}
	if ev.Evidence == "" {
		ev.Evidence = "[]"
	}
	var prev string
	err := tx.QueryRow("SELECT hash FROM task_events WHERE task_id=? ORDER BY seq DESC LIMIT 1", ev.TaskID).Scan(&prev)
	if err != nil && err != sql.ErrNoRows {
		return domain.TaskEvent{}, fmt.Errorf("read task event head: %w", err)
	}
	ev.PrevHash = prev
	canonical := strings.Join([]string{
		ev.TaskID, ev.Type, ev.FromStatus, ev.ToStatus, ev.ActorType, ev.ActorID, ev.Reason, ev.Evidence, ev.CreatedAt,
	}, "")
	sum := sha256.Sum256([]byte(prev + canonical))
	ev.Hash = hex.EncodeToString(sum[:])
	res, err := tx.Exec("INSERT INTO task_events(task_id, type, from_status, to_status, actor_type, actor_id, reason, evidence, prev_hash, hash, created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
		ev.TaskID, ev.Type, ev.FromStatus, ev.ToStatus, ev.ActorType, ev.ActorID, ev.Reason, ev.Evidence, ev.PrevHash, ev.Hash, ev.CreatedAt)
	if err != nil {
		return domain.TaskEvent{}, fmt.Errorf("append task event: %w", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return domain.TaskEvent{}, fmt.Errorf("task event seq: %w", err)
	}
	ev.Seq = seq
	return ev, nil
}

// AppendTaskEvent 追加不可变任务事件并计算 hash 链（core 不做哈希）。
func (s *Store) AppendTaskEvent(ctx context.Context, ev domain.TaskEvent) (domain.TaskEvent, error) {
	var out domain.TaskEvent
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = appendTaskEventTx(tx, ev)
		return err
	})
	return out, err
}

// ---- Artifacts ----

// CreateArtifact 注册产出物。
func (s *Store) CreateArtifact(ctx context.Context, a domain.Artifact) (domain.Artifact, error) {
	if a.ID == "" {
		a.ID = domain.NewID("art")
	}
	if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Type) == "" {
		return domain.Artifact{}, fmt.Errorf("%w: artifact requires type/title", ErrInvalid)
	}
	if a.Status == "" {
		a.Status = "draft"
	}
	if a.CreatedAt == "" {
		a.CreatedAt = Now()
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO artifacts("+artifactColumns+") VALUES(?,?,?,?,?,?,?,?)",
		a.ID, a.Type, a.Title, a.TaskID, a.DepartmentID, a.Status, a.CreatedBy, a.CreatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.Artifact{}, fmt.Errorf("%w: artifact %s", ErrConflict, a.ID)
		}
		return domain.Artifact{}, fmt.Errorf("create artifact: %w", err)
	}
	return a, nil
}

func scanArtifact(row interface{ Scan(...any) error }) (domain.Artifact, error) {
	var a domain.Artifact
	err := row.Scan(&a.ID, &a.Type, &a.Title, &a.TaskID, &a.DepartmentID, &a.Status, &a.CreatedBy, &a.CreatedAt)
	if err != nil {
		return domain.Artifact{}, err
	}
	return a, nil
}

// GetArtifact 按 ID 读取产出物。
func (s *Store) GetArtifact(ctx context.Context, id string) (domain.Artifact, error) {
	a, err := scanArtifact(s.DB.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Artifact{}, fmt.Errorf("%w: artifact %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Artifact{}, fmt.Errorf("get artifact: %w", err)
	}
	return a, nil
}

// UpdateArtifactStatus 更新产出物状态。
func (s *Store) UpdateArtifactStatus(ctx context.Context, id, status string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE artifacts SET status=? WHERE id=?", status, id)
	if err != nil {
		return fmt.Errorf("update artifact status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: artifact %s", ErrNotFound, id)
	}
	return nil
}

// CreateArtifactVersion 追加不可变版本；版本号/content_hash 冲突返回 ErrConflict。
func (s *Store) CreateArtifactVersion(ctx context.Context, v domain.ArtifactVersion) (domain.ArtifactVersion, error) {
	if strings.TrimSpace(v.ArtifactID) == "" || v.Version <= 0 {
		return domain.ArtifactVersion{}, fmt.Errorf("%w: artifact_version requires artifact_id/version", ErrInvalid)
	}
	if v.ContentHash == "" {
		sum := sha256.Sum256([]byte(v.Content))
		v.ContentHash = hex.EncodeToString(sum[:])
	}
	if v.ID == "" {
		v.ID = domain.NewID("artv")
	}
	if v.CreatedAt == "" {
		v.CreatedAt = Now()
	}
	var exists int
	err := s.DB.QueryRowContext(ctx, "SELECT 1 FROM artifact_versions WHERE content_hash=? LIMIT 1", v.ContentHash).Scan(&exists)
	if err == nil {
		return domain.ArtifactVersion{}, fmt.Errorf("%w: duplicate artifact content_hash", ErrConflict)
	}
	if err != nil && err != sql.ErrNoRows {
		return domain.ArtifactVersion{}, fmt.Errorf("check content hash: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO artifact_versions("+artifactVersionColumns+") VALUES(?,?,?,?,?,?,?,?,?)",
		v.ID, v.ArtifactID, v.Version, v.Content, v.ContentHash, v.StoragePath, v.AuthorID, v.Supersedes, v.CreatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.ArtifactVersion{}, fmt.Errorf("%w: artifact version exists", ErrConflict)
		}
		return domain.ArtifactVersion{}, fmt.Errorf("create artifact version: %w", err)
	}
	return v, nil
}

func scanArtifactVersion(row interface{ Scan(...any) error }) (domain.ArtifactVersion, error) {
	var v domain.ArtifactVersion
	err := row.Scan(&v.ID, &v.ArtifactID, &v.Version, &v.Content, &v.ContentHash, &v.StoragePath, &v.AuthorID, &v.Supersedes, &v.CreatedAt)
	if err != nil {
		return domain.ArtifactVersion{}, err
	}
	return v, nil
}

// GetArtifactVersion 读取指定版本。
func (s *Store) GetArtifactVersion(ctx context.Context, artifactID string, version int) (domain.ArtifactVersion, error) {
	v, err := scanArtifactVersion(s.DB.QueryRowContext(ctx, "SELECT "+artifactVersionColumns+" FROM artifact_versions WHERE artifact_id=? AND version=?", artifactID, version))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ArtifactVersion{}, fmt.Errorf("%w: artifact version %s@%d", ErrNotFound, artifactID, version)
	}
	if err != nil {
		return domain.ArtifactVersion{}, fmt.Errorf("get artifact version: %w", err)
	}
	return v, nil
}

// ListArtifactVersions 返回某产出物的版本（version 倒序）。
func (s *Store) ListArtifactVersions(ctx context.Context, artifactID string) ([]domain.ArtifactVersion, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+artifactVersionColumns+" FROM artifact_versions WHERE artifact_id=? ORDER BY version DESC", artifactID)
	if err != nil {
		return nil, fmt.Errorf("list artifact versions: %w", err)
	}
	defer rows.Close()
	out := make([]domain.ArtifactVersion, 0, 4)
	for rows.Next() {
		v, err := scanArtifactVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan artifact version: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---- Reviews ----

func scanReview(row interface{ Scan(...any) error }) (domain.Review, error) {
	var r domain.Review
	err := row.Scan(&r.ID, &r.FromDepartment, &r.ToDepartment, &r.ArtifactID, &r.ArtifactVersionID,
		&r.Severity, &r.Stance, &r.Claim, &r.Evidence, &r.Impact, &r.ProposedResolution,
		&r.OwnerID, &r.DueAt, &r.Status, &r.ThreadConversationID, &r.CreatedAt)
	if err != nil {
		return domain.Review{}, err
	}
	return r, nil
}

// CreateReview 创建挑战单。
func (s *Store) CreateReview(ctx context.Context, r domain.Review) (domain.Review, error) {
	if r.ID == "" {
		r.ID = domain.NewID("rev")
	}
	if strings.TrimSpace(r.Claim) == "" {
		return domain.Review{}, fmt.Errorf("%w: review claim required", ErrInvalid)
	}
	if r.Severity == "" {
		r.Severity = domain.SeverityQuestion
	}
	if r.Status == "" {
		r.Status = "open"
	}
	if r.Evidence == "" {
		r.Evidence = "[]"
	}
	if r.CreatedAt == "" {
		r.CreatedAt = Now()
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO reviews("+reviewColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		r.ID, r.FromDepartment, r.ToDepartment, r.ArtifactID, r.ArtifactVersionID, r.Severity, r.Stance,
		r.Claim, r.Evidence, r.Impact, r.ProposedResolution, r.OwnerID, r.DueAt, r.Status, r.ThreadConversationID, r.CreatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.Review{}, fmt.Errorf("%w: review %s", ErrConflict, r.ID)
		}
		return domain.Review{}, fmt.Errorf("create review: %w", err)
	}
	return r, nil
}

// GetReview 按 ID 读取挑战单。
func (s *Store) GetReview(ctx context.Context, id string) (domain.Review, error) {
	r, err := scanReview(s.DB.QueryRowContext(ctx, "SELECT "+reviewColumns+" FROM reviews WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Review{}, fmt.Errorf("%w: review %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Review{}, fmt.Errorf("get review: %w", err)
	}
	return r, nil
}

// ListReviewsByArtifact 返回某产出物的挑战单。
func (s *Store) ListReviewsByArtifact(ctx context.Context, artifactID string) ([]domain.Review, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+reviewColumns+" FROM reviews WHERE artifact_id=? ORDER BY created_at DESC", artifactID)
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Review, 0, 4)
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, fmt.Errorf("scan review: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateReviewVerdict 写入裁决（append-only）。
func (s *Store) CreateReviewVerdict(ctx context.Context, v domain.ReviewVerdict) (domain.ReviewVerdict, error) {
	if v.ID == "" {
		v.ID = domain.NewID("verdict")
	}
	if strings.TrimSpace(v.ReviewID) == "" || strings.TrimSpace(v.Verdict) == "" || strings.TrimSpace(v.ReviewerID) == "" {
		return domain.ReviewVerdict{}, fmt.Errorf("%w: verdict requires review_id/verdict/reviewer_id", ErrInvalid)
	}
	if v.Conditions == "" {
		v.Conditions = "[]"
	}
	if v.CreatedAt == "" {
		v.CreatedAt = Now()
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO review_verdicts("+verdictColumns+") VALUES(?,?,?,?,?,?,?,?,?,?)",
		v.ID, v.ReviewID, v.Verdict, v.Checked, v.Rationale, v.Conditions, v.ResidualRisk, v.ReviewerID, v.DecisionID, v.CreatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.ReviewVerdict{}, fmt.Errorf("%w: verdict %s", ErrConflict, v.ID)
		}
		return domain.ReviewVerdict{}, fmt.Errorf("create verdict: %w", err)
	}
	return v, nil
}

// ListReviewVerdicts 返回某挑战单的裁决。
func (s *Store) ListReviewVerdicts(ctx context.Context, reviewID string) ([]domain.ReviewVerdict, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+verdictColumns+" FROM review_verdicts WHERE review_id=? ORDER BY created_at ASC, id ASC", reviewID)
	if err != nil {
		return nil, fmt.Errorf("list verdicts: %w", err)
	}
	defer rows.Close()
	out := make([]domain.ReviewVerdict, 0, 4)
	for rows.Next() {
		var v domain.ReviewVerdict
		if err := rows.Scan(&v.ID, &v.ReviewID, &v.Verdict, &v.Checked, &v.Rationale, &v.Conditions, &v.ResidualRisk, &v.ReviewerID, &v.DecisionID, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan verdict: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---- Decisions ----

func scanDecision(row interface{ Scan(...any) error }) (domain.Decision, error) {
	var d domain.Decision
	err := row.Scan(&d.ID, &d.Title, &d.Status, &d.Context, &d.Options, &d.Decision, &d.Rationale,
		&d.Evidence, &d.Dissent, &d.Deciders, &d.Consulted, &d.Informed, &d.AffectedTasks,
		&d.AffectedArtifacts, &d.RevisitTrigger, &d.Supersedes, &d.SupersededBy, &d.HumanAck,
		&d.CreatedBy, &d.CreatedAt)
	if err != nil {
		return domain.Decision{}, err
	}
	return d, nil
}

func normalizeDecision(d *domain.Decision) {
	if d.ID == "" {
		d.ID = domain.NewID("decision")
	}
	if d.Status == "" {
		d.Status = domain.DecisionProposed
	}
	for _, p := range []*string{&d.Options, &d.Evidence, &d.Dissent, &d.Deciders, &d.Consulted,
		&d.Informed, &d.AffectedTasks, &d.AffectedArtifacts} {
		if *p == "" {
			*p = "[]"
		}
	}
	if d.CreatedAt == "" {
		d.CreatedAt = Now()
	}
}

// CreateDecision 创建决策记录（正文只增不改）。
func (s *Store) CreateDecision(ctx context.Context, d domain.Decision) (domain.Decision, error) {
	normalizeDecision(&d)
	if strings.TrimSpace(d.Title) == "" {
		return domain.Decision{}, fmt.Errorf("%w: decision title required", ErrInvalid)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO decisions("+decisionColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		d.ID, d.Title, d.Status, d.Context, d.Options, d.Decision, d.Rationale, d.Evidence, d.Dissent,
		d.Deciders, d.Consulted, d.Informed, d.AffectedTasks, d.AffectedArtifacts, d.RevisitTrigger,
		d.Supersedes, d.SupersededBy, d.HumanAck, d.CreatedBy, d.CreatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.Decision{}, fmt.Errorf("%w: decision %s", ErrConflict, d.ID)
		}
		return domain.Decision{}, fmt.Errorf("create decision: %w", err)
	}
	return d, nil
}

// GetDecision 按 ID 读取决策。
func (s *Store) GetDecision(ctx context.Context, id string) (domain.Decision, error) {
	d, err := scanDecision(s.DB.QueryRowContext(ctx, "SELECT "+decisionColumns+" FROM decisions WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Decision{}, fmt.Errorf("%w: decision %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Decision{}, fmt.Errorf("get decision: %w", err)
	}
	return d, nil
}

// ListDecisionsByStatus 按状态列出决策（status 为空=全部）。
func (s *Store) ListDecisionsByStatus(ctx context.Context, status string) ([]domain.Decision, error) {
	var rows *sql.Rows
	var err error
	if strings.TrimSpace(status) == "" {
		rows, err = s.DB.QueryContext(ctx, "SELECT "+decisionColumns+" FROM decisions ORDER BY created_at DESC")
	} else {
		rows, err = s.DB.QueryContext(ctx, "SELECT "+decisionColumns+" FROM decisions WHERE status=? ORDER BY created_at DESC", status)
	}
	if err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Decision, 0, 8)
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListDecisionsByTask 按受影响任务检索决策。
func (s *Store) ListDecisionsByTask(ctx context.Context, taskID string) ([]domain.Decision, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT "+decisionColumns+" FROM decisions WHERE EXISTS (SELECT 1 FROM json_each(decisions.affected_tasks) WHERE value=?) ORDER BY created_at DESC", taskID)
	if err != nil {
		return nil, fmt.Errorf("list decisions by task: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Decision, 0, 4)
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SupersedeDecision 旧决策标注被替代（只更新 status + superseded_by，不改正文）。
func (s *Store) SupersedeDecision(ctx context.Context, oldID, newID string) error {
	if strings.TrimSpace(oldID) == "" || strings.TrimSpace(newID) == "" {
		return fmt.Errorf("%w: old/new decision id required", ErrInvalid)
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE decisions SET status=?, superseded_by=? WHERE id=?",
		domain.DecisionSuperseded, newID, oldID)
	if err != nil {
		return fmt.Errorf("supersede decision: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: decision %s", ErrNotFound, oldID)
	}
	return nil
}

// ---- R4 读接口（平台只读投影，不含状态机） ----

// TaskFilter 是任务列表过滤条件。
type TaskFilter struct {
	Status       string
	DepartmentID string
	OwnerID      string
	Limit        int
}

// ListTasks 按过滤条件列出任务。
func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]domain.Task, error) {
	where := []string{"1=1"}
	args := make([]any, 0, 4)
	if f.Status != "" {
		where = append(where, "status=?")
		args = append(args, f.Status)
	}
	if f.DepartmentID != "" {
		where = append(where, "department_id=?")
		args = append(args, f.DepartmentID)
	}
	if f.OwnerID != "" {
		where = append(where, "owner_id=?")
		args = append(args, f.OwnerID)
	}
	q := "SELECT " + taskColumns + " FROM tasks WHERE " + strings.Join(where, " AND ") + " ORDER BY created_at DESC, id DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Task, 0, 8)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListTaskEvents 返回任务事件链（按 seq 升序）。
func (s *Store) ListTaskEvents(ctx context.Context, taskID string) ([]domain.TaskEvent, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT seq, task_id, type, from_status, to_status, actor_type, actor_id, reason, evidence, prev_hash, hash, created_at FROM task_events WHERE task_id=? ORDER BY seq ASC", taskID)
	if err != nil {
		return nil, fmt.Errorf("list task events: %w", err)
	}
	defer rows.Close()
	out := make([]domain.TaskEvent, 0, 8)
	for rows.Next() {
		var e domain.TaskEvent
		if err := rows.Scan(&e.Seq, &e.TaskID, &e.Type, &e.FromStatus, &e.ToStatus, &e.ActorType, &e.ActorID, &e.Reason, &e.Evidence, &e.PrevHash, &e.Hash, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan task event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ArtifactFilter 是产出物列表过滤条件。
type ArtifactFilter struct {
	TaskID string
	Type   string
	Limit  int
}

// ListArtifacts 按过滤条件列出产出物。
func (s *Store) ListArtifacts(ctx context.Context, f ArtifactFilter) ([]domain.Artifact, error) {
	where := []string{"1=1"}
	args := make([]any, 0, 3)
	if f.TaskID != "" {
		where = append(where, "task_id=?")
		args = append(args, f.TaskID)
	}
	if f.Type != "" {
		where = append(where, "type=?")
		args = append(args, f.Type)
	}
	q := "SELECT " + artifactColumns + " FROM artifacts WHERE " + strings.Join(where, " AND ") + " ORDER BY created_at DESC, id DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Artifact, 0, 8)
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ReviewFilter 是评审列表过滤条件。
type ReviewFilter struct {
	ArtifactID string
	Status     string
	Limit      int
}

// ListReviews 按过滤条件列出评审。
func (s *Store) ListReviews(ctx context.Context, f ReviewFilter) ([]domain.Review, error) {
	where := []string{"1=1"}
	args := make([]any, 0, 3)
	if f.ArtifactID != "" {
		where = append(where, "artifact_id=?")
		args = append(args, f.ArtifactID)
	}
	if f.Status != "" {
		where = append(where, "status=?")
		args = append(args, f.Status)
	}
	q := "SELECT " + reviewColumns + " FROM reviews WHERE " + strings.Join(where, " AND ") + " ORDER BY created_at DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Review, 0, 8)
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, fmt.Errorf("scan review: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListArtifactsByTask 返回任务关联产出物（core WorkStore，任务详情/G3 汇总用）。
func (s *Store) ListArtifactsByTask(ctx context.Context, taskID string) ([]domain.Artifact, error) {
	if taskID == "" {
		return []domain.Artifact{}, nil
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE task_id=? ORDER BY created_at ASC, id ASC", taskID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts by task: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Artifact, 0, 4)
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateReviewStatus 更新评审状态（open/responded/rework/resolved/escalated），不动正文。
func (s *Store) UpdateReviewStatus(ctx context.Context, id, status string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(status) == "" {
		return fmt.Errorf("%w: review id/status required", ErrInvalid)
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE reviews SET status=? WHERE id=?", status, id)
	if err != nil {
		return fmt.Errorf("update review status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: review %s", ErrNotFound, id)
	}
	return nil
}

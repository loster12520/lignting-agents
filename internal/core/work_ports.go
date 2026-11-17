package core

import (
	"context"

	"github.com/lignting/lignting-agents/internal/domain"
)

// WorkStore 是 R4（任务/产出/评审/决策/审批）的持久化端口，由 *store.Store 实现。
// task_events 的 prev_hash/hash 由 store.AppendTaskEvent 计算（core 不复制哈希逻辑）。
type WorkStore interface {
	// 任务
	CreateTask(ctx context.Context, t domain.Task) (domain.Task, error)
	UpdateTask(ctx context.Context, t domain.Task) error
	GetTask(ctx context.Context, id string) (domain.Task, error)
	ListTasksByDepartment(ctx context.Context, departmentID string) ([]domain.Task, error)
	ListTasksByOwnerStatus(ctx context.Context, ownerID, status string) ([]domain.Task, error)
	// 验收标准
	CreateTaskCriterion(ctx context.Context, c domain.Criterion) (domain.Criterion, error)
	ListTaskCriteria(ctx context.Context, taskID string) ([]domain.Criterion, error)
	UpdateTaskCriterion(ctx context.Context, c domain.Criterion) error
	// 依赖
	AddTaskDependency(ctx context.Context, taskID, dependsOn string) error
	ListTaskDependencies(ctx context.Context, taskID string) ([]string, error)
	// 任务事件（hash chain 由 store 负责）
	AppendTaskEvent(ctx context.Context, ev domain.TaskEvent) (domain.TaskEvent, error)

	// 产出物
	CreateArtifact(ctx context.Context, a domain.Artifact) (domain.Artifact, error)
	GetArtifact(ctx context.Context, id string) (domain.Artifact, error)
	UpdateArtifactStatus(ctx context.Context, id, status string) error
	CreateArtifactVersion(ctx context.Context, v domain.ArtifactVersion) (domain.ArtifactVersion, error)
	GetArtifactVersion(ctx context.Context, artifactID string, version int) (domain.ArtifactVersion, error)
	ListArtifactVersions(ctx context.Context, artifactID string) ([]domain.ArtifactVersion, error)
	ListArtifactsByTask(ctx context.Context, taskID string) ([]domain.Artifact, error)

	// 评审
	CreateReview(ctx context.Context, r domain.Review) (domain.Review, error)
	GetReview(ctx context.Context, id string) (domain.Review, error)
	ListReviewsByArtifact(ctx context.Context, artifactID string) ([]domain.Review, error)
	CreateReviewVerdict(ctx context.Context, v domain.ReviewVerdict) (domain.ReviewVerdict, error)
	ListReviewVerdicts(ctx context.Context, reviewID string) ([]domain.ReviewVerdict, error)
	UpdateReviewStatus(ctx context.Context, id, status string) error

	// 决策
	CreateDecision(ctx context.Context, d domain.Decision) (domain.Decision, error)
	GetDecision(ctx context.Context, id string) (domain.Decision, error)
	ListDecisionsByStatus(ctx context.Context, status string) ([]domain.Decision, error)
	ListDecisionsByTask(ctx context.Context, taskID string) ([]domain.Decision, error)
	SupersedeDecision(ctx context.Context, oldID, newID string) error

	// 人类闸门
	CreateApproval(ctx context.Context, a domain.Approval) (domain.Approval, error)
	GetApproval(ctx context.Context, id string) (domain.Approval, error)
	DecideApproval(ctx context.Context, id, decision, decidedBy, reason string) (domain.Approval, error)

	// 事件/审计
	AppendEvent(ctx context.Context, ev domain.Event) (domain.Event, error)
	AppendAudit(ctx context.Context, a domain.AuditEntry) (domain.AuditEntry, error)
}

// TaskView 是任务详情 DTO（api-contract §8）。
type TaskView struct {
	Task      domain.Task        `json:"task"`
	Criteria  []CriterionView    `json:"criteria"`
	DependsOn []string           `json:"depends_on"`
	Events    []domain.TaskEvent `json:"events,omitempty"`
	Artifacts []ArtifactRef      `json:"artifacts,omitempty"`
	DoR       []string           `json:"dor_missing,omitempty"`
	DoD       []string           `json:"dod_missing,omitempty"`
	Ready     bool               `json:"ready"`
}

// CriterionView 是验收标准 + 证据 + 主管结论 + 人类决定。
type CriterionView struct {
	ID                string `json:"id"`
	Text              string `json:"text"`
	Evidence          string `json:"evidence,omitempty"`
	SupervisorVerdict string `json:"supervisor_verdict"`
	HumanDecision     string `json:"human_decision"`
}

// ArtifactRef 是产出物版本引用（评审/决策/G3 卡用）。
type ArtifactRef struct {
	ArtifactID  string `json:"artifact_id"`
	Version     int    `json:"version"`
	ContentHash string `json:"content_hash"`
	Title       string `json:"title"`
}

// G3Card 是 G3 人类闸门卡（核对表/成本/风险/版本）。
type G3Card struct {
	TaskID        string          `json:"task_id"`
	Title         string          `json:"title"`
	Checklist     []CriterionView `json:"checklist"`
	Cost          float64         `json:"cost"`
	Risk          string          `json:"risk"`
	Artifacts     []ArtifactRef   `json:"artifacts"`
	ResidualRisks []string        `json:"residual_risks,omitempty"`
	Blockers      []string        `json:"blockers,omitempty"`
	Ready         bool            `json:"ready"`
	HumanDecision string          `json:"human_decision"`
}

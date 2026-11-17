package write

import (
	"context"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Repository 是共享写工具需要的最小持久化端口，由 internal/store 实现。
// 注意：store 不需要 import write，只要方法签名满足即可。
type Repository interface {
	InsertWriteEvent(ctx context.Context, ev domain.WriteEvent) error
	UpdateWriteEvent(ctx context.Context, ev domain.WriteEvent) error
	GetWriteEvent(ctx context.Context, id string) (domain.WriteEvent, error)
	ListWriteEventsByStatus(ctx context.Context, status string, limit int) ([]domain.WriteEvent, error)

	AppendTimeline(ctx context.Context, e domain.FileTimelineEntry) error
	TrimTimeline(ctx context.Context, path string, keep int) (int, error)
	ListTimeline(ctx context.Context, path, branch, commitSHA, agentID, since string, limit int) ([]domain.FileTimelineEntry, error)
	TimelineHasEvent(ctx context.Context, writeEventID string) (bool, error)
}

// AuditRecord 是一等审计记录；hash chain 由 store 负责。
type AuditRecord struct {
	ActorType  string
	ActorID    string
	Action     string
	ObjectType string
	ObjectID   string
	Result     string
	Detail     string
}

// AuditSink 由 store 的 audit_log 实现。用原始字段避免 store import write。
type AuditSink interface {
	RecordAudit(ctx context.Context, actorType, actorID, action, objectType, objectID, result, detail string) error
}

// BranchGate 由 gitwatch 实现，用于分支切换期间拒绝写入。
type BranchGate interface {
	Writable(ctx context.Context) error
	Branch(ctx context.Context) (string, error)
}

// TimelineFilter 是只读时间线查询条件。
type TimelineFilter struct {
	Path      string
	Branch    string
	CommitSHA string
	AgentID   string
	Since     string
	Limit     int
}

// Request 是一次 write_file 请求。
type Request struct {
	ActorType string
	ActorID   string
	TaskID    string
	Path      string
	Content   string
	BaseHash  string
	Reason    string
	Branch    string
}

// Result 是一次成功写入的结果。
type Result struct {
	Event      domain.WriteEvent
	TimelineID string
	Hash       string
	Path       string
	Created    bool
	Diff       string
}

// UnmanagedRequest 记录 bash 等绕过 write_file 的变更。
type UnmanagedRequest struct {
	ActorType    string
	ActorID      string
	TaskID       string
	Path         string
	Reason       string
	Branch       string
	Before       string
	BeforeExists bool
	After        string
	AfterExists  bool
}

// RecoveryReport 是崩溃恢复统计。
type RecoveryReport struct {
	Applied  int
	Rejected int
	Unknown  int
}

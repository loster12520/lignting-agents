// Package core 是 Agent 运行时：turn/mailbox、上下文装配、5 工具注册、
// lignting-action 执行骨架、路由隔离策略与助理行为。
//
// v0.0.1 的边界：
//   - core 不 import internal/store，也不写 SQL；只依赖本文件里的窄接口，
//     由 *store.Store 直接实现同名方法（cmd 里 core.New(st)）。
//   - 模型只接 DeepSeek 官方 API（internal/deepseek），不使用 DSH/ACP/Node/MCP。
//   - 工具严格 5 个：read_file、write_file、bash、web_search、view_change_history。
package core

import (
	"context"
	"encoding/json"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Store 是 core 依赖的持久化端口集合。目标是由 *store.Store 直接实现
// （cmd 接线时用 var _ core.Store = (*store.Store)(nil) 编译断言验证）。
type Store interface {
	AgentStore
	ConversationStore
	DepartmentStore
	TaskStore
	MessageStore
	EventStore
	AuditStore
	AtomicReplyStore
	TurnStore
	SnapshotStore
	UsageStore
	IntentStore
}

// AgentStore 读取/更新 Agent；状态变更由 core 追加 agent.status 事件。
type AgentStore interface {
	GetAgent(ctx context.Context, id string) (domain.Agent, error)
	ListAgentsByDepartment(ctx context.Context, departmentID string) ([]domain.Agent, error)
	ListAgentsByKind(ctx context.Context, kind string) ([]domain.Agent, error)
	UpdateAgentStatus(ctx context.Context, id, status string) error
}

// ConversationStore 读取会话与成员；路由/隔离判断在 core 做。
type ConversationStore interface {
	GetConversation(ctx context.Context, id string) (domain.Conversation, error)
	ListConversationMembers(ctx context.Context, conversationID string) ([]domain.ConversationMember, error)
}

// DepartmentStore 读取部门章程（上下文装配用）。
type DepartmentStore interface {
	GetDepartment(ctx context.Context, id string) (domain.Department, error)
}

// TaskStore 读取任务（上下文装配与状态机校验用）。
type TaskStore interface {
	GetTask(ctx context.Context, id string) (domain.Task, error)
}

// MessageStore 是消息落库/读取端口。seq/id/created_at 为空时由 store 回填。
type MessageStore interface {
	AppendMessage(ctx context.Context, msg domain.Message) (domain.Message, error)
	RecentMessages(ctx context.Context, conversationID string, limit int, beforeSeq int64) ([]domain.Message, error)
}

// EventStore 追加 SSE/审计投影事件；返回带 seq 的事件。
type EventStore interface {
	AppendEvent(ctx context.Context, ev domain.Event) (domain.Event, error)
}

// AuditStore 追加审计记录（hash chain 由 store 计算）。
type AuditStore interface {
	AppendAudit(ctx context.Context, audit domain.AuditEntry) (domain.AuditEntry, error)
}

// AtomicReplyStore 是原子回复写：消息 + 事件 + 审计同事务（store 已实现）。
type AtomicReplyStore interface {
	AppendReply(ctx context.Context, msg domain.Message, events []domain.Event, audit domain.AuditEntry) (domain.Message, error)
}

// TurnStore 管理 turn 生命周期。FinishTurnWithEvents 与状态/事件/审计同事务。
type TurnStore interface {
	CreateTurn(ctx context.Context, turn domain.Turn) error
	FinishTurnWithEvents(ctx context.Context, turnID, status, errText string, events []domain.Event, audit domain.AuditEntry) error
}

// SnapshotStore 保存 context.request 快照（FR-OBS-04）。
type SnapshotStore interface {
	InsertContextSnapshot(ctx context.Context, snap domain.ContextSnapshot) error
}

// UsageStore 记录每次模型调用的 token/费用（FR-OBS-03）。
type UsageStore interface {
	InsertUsage(ctx context.Context, usage domain.Usage) error
}

// IntentStore 保存项目意图卡（FR-ASST-01），版本由 store 递增。
type IntentStore interface {
	SaveIntent(ctx context.Context, content string) (id string, version int, err error)
}

// ToolCall 是一次客户端工具调用（core 注册的 5 个工具）。
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolResult 是工具执行结果，会作为 tool 消息回填给模型。
type ToolResult struct {
	CallID  string
	Name    string
	Content string
	IsError bool
}

// ToolExecutor 由 internal/tools 实现（R5）。为 nil 时已注册工具返回可读错误。
type ToolExecutor interface {
	Execute(ctx context.Context, agent domain.Agent, call ToolCall) (ToolResult, error)
}

// MemoryQuery 是一次记忆注入请求（R7 由 internal/memory 实现）。
type MemoryQuery struct {
	Agent          domain.Agent
	ConversationID string
	TaskID         string
	Limit          int

	// 快照接口额外字段（旧 Inject 实现可忽略）。
	TurnID               string
	Model                string
	TaskBrief            string
	EffectiveInputTokens int64
	RecentMessages       int
	RecentTokens         int64
	ManualScope          string
	IncludeManual        bool
	IncludeArchives      bool
}

// MemoryEntrySnapshot 是 memory.InjectedMemory 的 core 侧 DTO 镜像。
type MemoryEntrySnapshot struct {
	ID         string `json:"id"`
	EntityType string `json:"entity_type,omitempty"`
	EntityKey  string `json:"entity_key,omitempty"`
	Version    int    `json:"version,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Source     string `json:"source"`
	Visibility string `json:"visibility,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Pinned     bool   `json:"pinned,omitempty"`
	Tokens     int64  `json:"tokens"`
	Reason     string `json:"reason,omitempty"`
}

// MemoryDroppedSnapshot 是本次因权限/预算被裁掉的记忆（可解释性）。
type MemoryDroppedSnapshot struct {
	ID         string `json:"id"`
	EntityType string `json:"entity_type,omitempty"`
	EntityKey  string `json:"entity_key,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Tokens     int64  `json:"tokens,omitempty"`
}

// MemorySnapshot 是 memory.ContextRequest 的 core 侧镜像（可 JSON 化）。
type MemorySnapshot struct {
	Entries       []MemoryEntrySnapshot   `json:"entries"`
	Dropped       []MemoryDroppedSnapshot `json:"dropped,omitempty"`
	Tokens        int64                   `json:"tokens"`
	Estimated     int64                   `json:"estimated_tokens,omitempty"`
	Degraded      bool                    `json:"degraded"`
	DegradeReason string                  `json:"degrade_reason,omitempty"`
	ManualVersion int                     `json:"manual_version,omitempty"`
	ManualTokens  int64                   `json:"manual_tokens,omitempty"`
	TaskTokens    int64                   `json:"task_tokens,omitempty"`
	CreatedAt     string                  `json:"created_at,omitempty"`
}

// MemorySnapshotter 是可选的新接口：memory.Service 实现后 core 优先使用。
type MemorySnapshotter interface {
	InjectWithSnapshot(ctx context.Context, q MemoryQuery) (*MemorySnapshot, error)
}

// BridgeLookup 是临时桥查询（*store.Store.GetActiveBridge 直接满足）。
// 无桥返回 (nil,nil)。
type BridgeLookup interface {
	GetActiveBridge(ctx context.Context, fromType, fromID, toType, toID string) (*domain.Bridge, error)
}

// MemoryInjector 返回本次 turn 可注入的记忆（已按权限/预算裁剪）。
// 为 nil 时上下文装配只放「记忆占位」段落；实现 MemorySnapshotter 时优先用快照接口。
type MemoryInjector interface {
	Inject(ctx context.Context, q MemoryQuery) ([]domain.Memory, error)
}

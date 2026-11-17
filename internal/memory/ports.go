package memory

import (
	"context"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Repo 是记忆持久化的唯一端口（Ports & Adapters）。实现方可以是
// internal/memory 自带的 SQL 仓储（直接持有 *sql.DB），也可以是测试用的内存 fake。
//
// 契约（实现必须遵守）：
//   - InsertMemory/UpdateMemory 不修改调用方传入的结构体；UpdateMemory 以 expectedVersion 做
//     乐观锁，版本不匹配返回 ErrConflict（FR-MEM-05/09）。
//   - ListMemories 必须支持 Filter 的全部字段；IncludeDeleted=false 时不得返回墓碑。
//   - AppendMemoryEvent 必须逐字保存 Detail（压缩快照就存在这里，不能截断）。
//   - AddMemoryRead 写读取留痕；失败不应阻断读取（由 Service 决定是否降级）。
//   - 所有时间由调用方传入（Memory.CreatedAt 等），仓储不得用本地时区改写。
type Repo interface {
	InsertMemory(ctx context.Context, m domain.Memory) error
	GetMemory(ctx context.Context, id string) (domain.Memory, error)
	UpdateMemory(ctx context.Context, m domain.Memory, expectedVersion int) error
	ListMemories(ctx context.Context, f Filter) ([]domain.Memory, error)
	CountMemories(ctx context.Context, f Filter) (int, error)

	AppendMemoryEvent(ctx context.Context, ev MemoryEvent) error
	AddMemoryRead(ctx context.Context, r MemoryRead) error

	MaxManualVersion(ctx context.Context, key ManualKey) (int, error)
	ListManuals(ctx context.Context, key ManualKey) ([]domain.PromptManual, error)
	GetManual(ctx context.Context, id string) (domain.PromptManual, error)
	InsertManual(ctx context.Context, m domain.PromptManual) error
	UpdateManualStatus(ctx context.Context, id, status string) error
}

// Clock 抽象时间，便于测试冷却/双时间/预算。
type Clock interface {
	Now() time.Time
}

// SystemClock 是生产时钟。
type SystemClock struct{}

// Now 返回 UTC 当前时间。
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// TranscriptMessage 是触发式记忆抽取的输入消息（短期上下文由 core 提供）。
type TranscriptMessage struct {
	SenderType string
	SenderID   string
	SenderName string
	Content    string
	At         string
	// Pinned 表示该消息属于「关键事实固定注入」，抽取时应优先保留。
	Pinned bool
}

// ExtractionRequest 是触发式记忆的输入（FR-MEM-03）。
// 注意：本包不保存短期上下文，只把 core 投影出的窗口交给 Extractor。
type ExtractionRequest struct {
	Trigger      string
	Scope        string
	DepartmentID string
	AgentID      string
	TaskID       string
	Messages     []TranscriptMessage
	// Existing 是同一实体键下的有效档案摘要，供模型合并/失效判断（可空）。
	Existing []domain.Memory
	// MaxCandidates 由额度决定，抽取器不得超发。
	MaxCandidates int
}

// Extractor 从事件窗口抽取候选记忆。实现可以是 deepseek 客户端，也可以是规则/空实现。
type Extractor interface {
	Extract(ctx context.Context, req ExtractionRequest) ([]Candidate, error)
}

// CompressionRequest 请求把一条档案压到目标字数（FR-MEM-06）。
type CompressionRequest struct {
	MemoryID    string
	EntityType  string
	EntityKey   string
	Content     string
	TargetChars int
	// RequiresPaging 为 true 时表示正文过长（>60000 字符），只提供了首尾，
	// 实现应按「先 outline 再分段读回」的策略压缩，而不是编造中间内容。
	RequiresPaging bool
	Head           string
	Tail           string
}

// CompressionResult 是压缩结果：只接受一种形态——完整的新正文。
type CompressionResult struct {
	Content string
	Model   string
	Notes   string
}

// Compressor 压缩档案全文。模型不可用时返回 ErrModelUnavailable，调用方必须保留原文。
type Compressor interface {
	Compress(ctx context.Context, req CompressionRequest) (CompressionResult, error)
}

// 可见性与读取策略（lead 裁决，2026-11-17）：
//
//	human-only：任何 Agent 都不得自动注入（含助理）。助理只能通过 memory.read 显式读取
//	单条（必须脱敏、必须写 memory_reads、必须带 turn_id），且禁止批量/预取；
//	员工与部门主管一律拒绝。人类 UI 可读全部，走审计留痕。
//	删除/墓碑后任何路径都不可读（Get/List/Inject/Read 全部拦截）。
//
// 该规则在 access.go 与 inject_test.go 中以测试固化。
const HumanOnlyPolicy = "human-only: 禁止 Agent 自动注入；助理仅可显式单条读取（脱敏+留痕）；员工/主管拒绝；墓碑后不可读"

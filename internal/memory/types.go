package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// EntityType 是档案的八类实体（FR-MEM-05）。扩展类型见 EntityTypeKnown，未知类型一律拒写。
const (
	EntityPerson     = "person"
	EntityEmployee   = "employee"
	EntityDepartment = "department"
	EntityProject    = "project"
	EntityTask       = "task"
	EntityDecision   = "decision"
	EntityDispute    = "dispute"
	EntityCapability = "capability"
	// EntityPreference 是人类偏好（FR-MEM-03 人类拍板/偏好/红线）。
	EntityPreference = "preference"
	// EntityInternal 是内部元数据（豁免容量治理）。
	EntityInternal = "__internal"
)

var knownEntityTypes = map[string]bool{
	EntityPerson:     true,
	EntityEmployee:   true,
	EntityDepartment: true,
	EntityProject:    true,
	EntityTask:       true,
	EntityDecision:   true,
	EntityDispute:    true,
	EntityCapability: true,
	EntityPreference: true,
	EntityInternal:   true,
}

// KnownEntityType 报告实体类型是否受支持。
func KnownEntityType(t string) bool { return knownEntityTypes[t] }

// 不可覆盖内容的实体：决策/争议/偏好（FR-MEM-05「双方立场不可覆盖」）。
var immutableEntities = map[string]bool{
	EntityDecision:   true,
	EntityDispute:    true,
	EntityPreference: true,
}

// ImmutableEntity 报告该实体类型是否禁止原地改内容（只能追加新版本 + 双时间）。
func ImmutableEntity(t string) bool { return immutableEntities[t] }

// 证据类型（写入契约要求至少 1 条可核查证据）。
const (
	EvidenceMessage = "message"
	EvidenceTask    = "task"
	EvidenceFile    = "file"
	EvidenceCommand = "command"
	EvidenceVerdict = "verdict"
	EvidenceReview  = "review"
	EvidenceHuman   = "human"
	EvidenceURL     = "url"
)

// 置信度。
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// 敏感度。
const (
	SensitivityNormal    = "normal"
	SensitivitySensitive = "sensitive"
	SensitivitySecret    = "secret"
)

// 主体类型（读取/写入者）。
const (
	ActorHuman      = "human"
	ActorAssistant  = "assistant"
	ActorSupervisor = "supervisor"
	ActorEmployee   = "employee"
	ActorSystem     = "system"
)

// 统计口径。
const (
	CountDaily = "daily"
	CountTask  = "task"
)

// 错误码补充（复用 domain 的稳定 code，仅补细节字段）。
const (
	CodeMemoryDisabled = "memory_disabled"
	CodeQuotaExceeded  = "quota_exceeded"
	CodeDuplicate      = "duplicate"
	CodeSensitive      = "sensitive_blocked"
	CodeManualLocked   = "manual_locked"
	CodeInFlight       = "in_flight"
	CodeDegraded       = "degraded"
)

// 供 errors.Is 判断的哨兵错误。
var (
	ErrNotFound         = errors.New("memory: not found")
	ErrConflict         = errors.New("memory: version conflict")
	ErrInFlight         = errors.New("memory: operation in flight")
	ErrCooldown         = errors.New("memory: cooling down")
	ErrDisabled         = errors.New("memory: disabled")
	ErrModelUnavailable = errors.New("memory: model unavailable")
	ErrForbidden        = errors.New("memory: forbidden")
	ErrQuota            = errors.New("memory: quota exceeded")
	ErrDuplicate        = errors.New("memory: duplicate")
	ErrSensitive        = errors.New("memory: sensitive content blocked")
	ErrImmutable        = errors.New("memory: entity content is immutable")
	ErrManualInvalid    = errors.New("memory: manual invalid")
)

func invalidError(msg string, details map[string]any) *domain.Error {
	e := domain.NewError(domain.CodeInvalid, msg, 400)
	e.Details = details
	return e
}

func forbiddenError(msg string, details map[string]any) *domain.Error {
	e := domain.NewError(domain.CodeForbidden, msg, 403)
	e.Details = details
	return e
}

func notFoundError(msg string) *domain.Error { return domain.NewError(domain.CodeNotFound, msg, 404) }

func conflictError(msg string, details map[string]any) *domain.Error {
	e := domain.NewError(domain.CodeConflict, msg, 409)
	e.Details = details
	return e
}

// Evidence 是一条可核查证据。
type Evidence struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Quote string `json:"quote,omitempty"`
}

// Candidate 是写入档案前的候选记忆（员工/助手提交，FR-MEM-02/04）。
type Candidate struct {
	Scope        string
	DepartmentID string
	AgentID      string

	EntityType string
	EntityKey  string
	Content    string
	Summary    string
	Tags       []string

	Confidence  string
	Evidence    []Evidence
	Sensitivity string
	Visibility  string

	ValidFrom  string
	Supersedes string

	// 四道门槛中无法从内容推断的两道，由提交方显式声明（UI/文档可见）。
	Reusable         bool
	DecisionRelevant bool

	SourceTaskID string
	SourceEvent  string
	CreatedBy    string
}

// SubmitResult 是候选提交结果。
type SubmitResult struct {
	Memory        domain.Memory
	Duplicate     bool
	Conflict      bool
	ConflictsWith string
	Warnings      []string
	Disabled      bool
}

// ApproveResult 是审核（批准/拒绝）结果。
type ApproveResult struct {
	Memory         domain.Memory
	SupersededID   string
	OverLimit      bool
	QueuedCompress bool
}

// Filter / MemoryEvent / MemoryRead 是 internal/domain 中同名类型的别名。
// 这样 internal/store 实现 Repo 时不需要 import internal/memory（避免 store→memory 反向依赖），
// 而本包与测试的写法（复合字面量、字段名）保持不变。
type Filter = domain.MemoryFilter
type MemoryEvent = domain.MemoryEvent
type MemoryRead = domain.MemoryRead

// 记忆事件动作。
const (
	ActionCandidateCreated = "candidate_created"
	ActionApproved         = "approved"
	ActionRejected         = "rejected"
	ActionEdited           = "edited"
	ActionDeleted          = "deleted"
	ActionPinned           = "pinned"
	ActionUnpinned         = "unpinned"
	ActionSuperseded       = "superseded"
	ActionSnapshot         = "snapshot"
	ActionCompressed       = "compressed"
	ActionCompressFailed   = "compress_failed"
	ActionCompressRejected = "compress_rejected"
	ActionReadDenied       = "read_denied"
	ActionRead             = "read"
	ActionManualProposed   = "manual_proposed"
	ActionManualApproved   = "manual_approved"
	ActionManualRejected   = "manual_rejected"
	ActionManualRolledBack = "manual_rolled_back"
	ActionQuotaRejected    = "quota_rejected"
	ActionDuplicate        = "duplicate"
	ActionSensitiveBlocked = "sensitive_blocked"
)

// TagPinned 是钉住标记（无独立列，复用 tags）。
const TagPinned = "pinned"

// TagUntrusted 标记不可晋升到 L4 的内容（投毒防护，AC-11）。
const TagUntrusted = "untrusted"

// TagTaskPrefix 让候选与任务关联（每任务额度统计用）。
const TagTaskPrefix = "task:"

// TagSourcePrefix 记录来源事件/会话，便于「为什么这么判断」。
const TagSourcePrefix = "src:"

// HashContent 归一化后取 sha256，用于去重与审计。
func HashContent(s string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(s), " ")))
	return hex.EncodeToString(sum[:8])
}

// EncodeEvidence 把证据编码为 JSON 数组（domain.Memory.Evidence 是 TEXT）。
func EncodeEvidence(ev []Evidence) string {
	if len(ev) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// DecodeEvidence 解码证据；坏数据返回空切片而不报错（注入路径不能因脏数据中断）。
func DecodeEvidence(s string) []Evidence {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var ev []Evidence
	if err := json.Unmarshal([]byte(s), &ev); err != nil {
		return nil
	}
	return ev
}

// HasEvidence 报告证据列表里是否至少有一条带 ref 的可用证据。
func HasEvidence(ev []Evidence) bool {
	for _, e := range ev {
		if strings.TrimSpace(e.Ref) != "" {
			return true
		}
	}
	return false
}

// TagsContain 报告 tags 中是否含某一项（tags 用逗号分隔存储）。
func TagsContain(tags, want string) bool {
	for _, t := range SplitTags(tags) {
		if t == want {
			return true
		}
	}
	return false
}

// SplitTags 拆分逗号分隔的 tags。
func SplitTags(tags string) []string {
	if strings.TrimSpace(tags) == "" {
		return nil
	}
	parts := strings.Split(tags, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// JoinTags 去重合并 tags。
func JoinTags(tags []string) string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return strings.Join(out, ",")
}

// TaskTag 生成任务标签。
func TaskTag(taskID string) string {
	if taskID == "" {
		return ""
	}
	return TagTaskPrefix + taskID
}

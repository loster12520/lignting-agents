package memory

import (
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Access 描述一次记忆访问的主体。人（human）与 Agent 共用类型，但权限矩阵不同。
type Access struct {
	// Type 取值见 Actor* 常量。
	Type string
	// ID 对 agent 是 agent_id，对人是 user id。
	ID string
	// DepartmentID 是主体所属部门（人类可为空）。
	DepartmentID string
	// OrgWide 表示可跨部门读 team/org 级档案（人类、助理、系统）。
	OrgWide bool
}

// HumanAccess 构造人类访问者。
func HumanAccess(id string) Access {
	return Access{Type: ActorHuman, ID: id, OrgWide: true}
}

// AgentAccess 构造 Agent 访问者。
func AgentAccess(actorType, agentID, departmentID string, orgWide bool) Access {
	return Access{Type: actorType, ID: agentID, DepartmentID: departmentID, OrgWide: orgWide}
}

// IsHuman 报告是否人类。
func (a Access) IsHuman() bool { return a.Type == ActorHuman }

// IsAgent 报告是否 Agent（含助理/主管/员工）。
func (a Access) IsAgent() bool {
	switch a.Type {
	case ActorAssistant, ActorSupervisor, ActorEmployee:
		return true
	default:
		return false
	}
}

// IsManager 报告是否可审核候选（人类、助理、部门主管）。
func (a Access) IsManager() bool {
	switch a.Type {
	case ActorHuman, ActorAssistant, ActorSupervisor:
		return true
	default:
		return false
	}
}

// ParseTime 容忍空串与多种 RFC3339 写法；解析失败返回零值。
func ParseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// Effective 报告档案在 at 时刻是否生效：active + 未删除 + 处于有效区间。
func Effective(m domain.Memory, at time.Time) bool {
	if m.Status != domain.MemoryStatusActive {
		return false
	}
	if strings.TrimSpace(m.DeletedAt) != "" {
		return false
	}
	if from := ParseTime(m.ValidFrom); !from.IsZero() && at.Before(from) {
		return false
	}
	if to := ParseTime(m.ValidTo); !to.IsZero() && !at.Before(to) {
		return false
	}
	return true
}

// IsTombstone 报告是否为墓碑（删除后任何路径不可读）。
func IsTombstone(m domain.Memory) bool {
	return m.Status == domain.MemoryStatusDeleted || strings.TrimSpace(m.DeletedAt) != ""
}

// VisibleTo 报告档案是否可被该主体「自动注入」。严格模式：
//   - 墓碑、非 active、过期、未生效 → false；
//   - human-only → 一律 false（含助理），只能走 memory.read 显式单条；
//   - agent-private → 仅本人；team → 同部门；org → 任意。
func VisibleTo(a Access, m domain.Memory, at time.Time) bool {
	if IsTombstone(m) || !Effective(m, at) {
		return false
	}
	if m.Visibility == domain.MemoryHumanOnly {
		return false
	}
	switch m.Visibility {
	case domain.MemoryAgentPrivate:
		if a.IsHuman() {
			return true
		}
		return a.IsAgent() && a.ID != "" && a.ID == m.AgentID
	case domain.MemoryTeam:
		if a.IsHuman() || a.OrgWide {
			return true
		}
		return a.DepartmentID != "" && a.DepartmentID == m.DepartmentID
	case domain.MemoryOrg:
		return a.IsHuman() || a.OrgWide || true // org 级对所有已认证主体可见
	default:
		// 未知可见性按 team 处理，避免配置错误导致越权。
		if a.IsHuman() || a.OrgWide {
			return true
		}
		return a.DepartmentID != "" && a.DepartmentID == m.DepartmentID
	}
}

// CheckAutoInject 是注入路径的硬闸门：返回 nil 表示可以自动注入。
func CheckAutoInject(a Access, m domain.Memory, at time.Time) error {
	if IsTombstone(m) {
		return notFoundError("记忆不存在")
	}
	if m.Visibility == domain.MemoryHumanOnly {
		return forbiddenError("human-only 记忆不允许自动注入；请使用 memory.read 显式读取", map[string]any{
			"memory_id": m.ID, "policy": "human_only_no_auto_inject",
		})
	}
	if !VisibleTo(a, m, at) {
		return forbiddenError("无权读取该记忆", map[string]any{"memory_id": m.ID, "visibility": m.Visibility})
	}
	if m.Sensitivity == SensitivitySecret && !a.IsHuman() {
		return forbiddenError("secret 级记忆不允许 Agent 读取", map[string]any{"memory_id": m.ID})
	}
	return nil
}

// ReadDecision 是 memory.read 工具的单条读取裁决。
type ReadDecision struct {
	Allowed bool
	// Redact 表示返回前必须脱敏（敏感字段替换为占位符）。
	Redact bool
	// Audit 表示必须写读取留痕与审计（人类读私有、助理读 human-only）。
	Audit  bool
	Reason string
}

// DecideRead 实现 lead 裁决的 human-only 读取规则：
//   - human：全部可读，敏感内容也要留痕（审计模式）；
//   - assistant：human-only 仅在显式单条 + 带 turn_id 时允许，且必须脱敏 + 留痕；
//   - supervisor/employee：human-only 一律拒绝；
//   - 墓碑：所有路径拒绝且按 not_found 处理（不泄露存在性）。
func DecideRead(a Access, m domain.Memory, explicitSingle bool, turnID string, at time.Time) ReadDecision {
	if IsTombstone(m) {
		return ReadDecision{Allowed: false, Reason: "deleted"}
	}
	if !explicitSingle {
		return ReadDecision{Allowed: false, Reason: "batch_forbidden"}
	}
	if a.IsHuman() {
		return ReadDecision{Allowed: true, Audit: true, Redact: false, Reason: "human_audited"}
	}
	if !a.IsAgent() && a.Type != ActorSystem {
		return ReadDecision{Allowed: false, Reason: "unknown_actor"}
	}
	if m.Visibility == domain.MemoryHumanOnly {
		if a.Type != ActorAssistant || strings.TrimSpace(turnID) == "" {
			return ReadDecision{Allowed: false, Reason: "human_only_assistant_only"}
		}
		return ReadDecision{Allowed: true, Redact: true, Audit: true, Reason: "assistant_explicit_human_only"}
	}
	if m.Sensitivity == SensitivitySecret {
		return ReadDecision{Allowed: false, Reason: "secret_denied"}
	}
	if !VisibleTo(a, m, at) {
		return ReadDecision{Allowed: false, Reason: "not_visible"}
	}
	return ReadDecision{
		Allowed: true,
		Redact:  m.Sensitivity == SensitivitySensitive && a.IsAgent(),
		Audit:   m.Visibility == domain.MemoryAgentPrivate,
		Reason:  "visible",
	}
}

// CanWriteArchive 报告该主体是否可直接写组织级档案（非候选）。
// 员工只能提交候选（FR-MEM-02）。
func CanWriteArchive(a Access) bool { return a.IsManager() }

// CanReview 报告该主体是否可审核候选。
func CanReview(a Access) bool { return a.IsManager() }

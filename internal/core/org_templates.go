package core

import (
	"encoding/json"
	"sort"
	"strings"
)

// RoleCard 是部门模板里的角色卡（写入 agents.role_card JSON）。
// 字段对应 requirements 6.1/6.4 与 FR-TEAM-02：kind/perspective/tools/memory scope/decision rights。
type RoleCard struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Perspective    string   `json:"perspective,omitempty"`
	Mission        string   `json:"mission"`
	Tools          []string `json:"tools"`
	MemoryScope    string   `json:"memory_scope"`
	DecisionRights []string `json:"decision_rights,omitempty"`
	CanDo          []string `json:"can_do,omitempty"`
	CannotDo       []string `json:"cannot_do,omitempty"`
}

// JSON 返回写入 agents.role_card 的 JSON。
func (r RoleCard) JSON() string {
	b, err := json.Marshal(r)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// DepartmentTemplate 是部门模板（对应 requirements 6.4 默认部门模板）。
type DepartmentTemplate struct {
	Kind    string     `json:"kind"`
	Name    string     `json:"name"`
	Mission string     `json:"mission"`
	Roles   []RoleCard `json:"roles"`
}

// AllTools 是 v0.0.1 允许的工具集合（严格 5 个）。
func AllTools() []string {
	return []string{ToolReadFile, ToolWriteFile, ToolBash, ToolWebSearch, ToolViewChangeHistory}
}

func role(name, kind, perspective, mission string, rights, canDo, cannotDo []string) RoleCard {
	return RoleCard{
		Name: name, Kind: kind, Perspective: perspective, Mission: mission,
		Tools: AllTools(), MemoryScope: "team",
		DecisionRights: rights, CanDo: canDo, CannotDo: cannotDo,
	}
}

// DepartmentTemplates 返回 v0.0.1 的部门模板：产品/研发/测试（必需）+ 平台/PM（可选）。
func DepartmentTemplates() []DepartmentTemplate {
	return []DepartmentTemplate{
		{
			Kind: "product", Name: "产品部",
			Mission: "把模糊需求变成可验收的 PRD 与范围判断。",
			Roles: []RoleCard{
				role("产品主管", "supervisor", "", "本部门拆解/分派/裁决，产出核对；对 what/why 负责。",
					[]string{"本部门任务拆解与分派", "部门内讨论裁决", "PRD 与验收标准定稿"},
					[]string{"写 PRD/验收标准", "组织需求澄清", "向助理升级跨部门分歧"},
					[]string{"替用户批准预算/章程", "直接指挥其他部门员工", "绕过测试质量门"}),
				role("产品经理", "employee", "需求与验收", "需求分析、PRD、验收标准与用户故事。",
					nil, []string{"写 PRD 与验收标准", "绘制用户旅程"}, []string{"做全局优先级决定", "直接联系其他部门/用户"}),
			},
		},
		{
			Kind: "engineering", Name: "研发部",
			Mission: "把确认的范围实现为可运行、可测试的交付物。",
			Roles: []RoleCard{
				role("研发主管", "supervisor", "", "本部门方案/拆解/分派、技术裁决、跨部门对接；对 how 负责。",
					[]string{"技术方案与拆解", "分工视角指派", "部门内讨论裁决"},
					[]string{"拆分任务并写清验收标准", "评审本部门产出", "发起跨部门技术评审"},
					[]string{"改用户目标/项目预算", "批准自己产出", "绕过测试质量门"}),
				role("后端工程师", "employee", "接口与数据流", "实现接口、数据模型与服务端逻辑。",
					nil, []string{"接口/数据流实现", "单元测试", "提交产出物版本"}, []string{"替代测试做质量判定", "直接联系其他部门/用户"}),
				role("前端工程师", "employee", "页面与交互", "实现页面、交互与端到端联调。",
					nil, []string{"页面/交互实现", "联调与自测", "提交产出物版本"}, []string{"替代测试做质量判定", "直接联系其他部门/用户"}),
				role("数据流工程师", "employee", "数据与集成", "数据模型、迁移与集成链路。",
					nil, []string{"数据/集成实现", "迁移与兼容性验证"}, []string{"替代测试做质量判定", "直接联系其他部门/用户"}),
			},
		},
		{
			Kind: "qa", Name: "测试部",
			Mission: "独立判定质量：用例、证据、质量门与回归。",
			Roles: []RoleCard{
				role("测试主管", "supervisor", "", "测试计划、质量门裁决、跨部门质量对接；对质量负责。",
					[]string{"质量门判定", "测试计划与资源", "提交 blocker"},
					[]string{"定测试策略", "核对证据", "独立提交质量结论"},
					[]string{"改用户目标/预算", "替研发批准技术方案"}),
				role("测试工程师", "employee", "测试与质量", "用例设计、执行、证据与回归。",
					nil, []string{"写用例并执行", "提交带证据的 blocker", "维护质量报告"}, []string{"被静默关闭 blocker", "直接联系其他部门/用户"}),
			},
		},
		{
			Kind: "platform", Name: "平台部",
			Mission: "架构与接口评审，制造必要的跨部门挑战。",
			Roles: []RoleCard{
				role("平台主管", "supervisor", "", "架构/接口/安全评审与跨部门技术裁决。",
					[]string{"架构评审", "跨部门技术挑战", "接口契约判定"},
					[]string{"发起架构评审", "提交跨部门挑战单"}, []string{"直接改其他部门实现", "替用户做最终验收"}),
				role("架构评审", "employee", "接口与安全", "接口、安全、可维护性评审。",
					nil, []string{"评审接口/安全", "提交证据与改进建议"}, []string{"直接联系其他部门员工/用户"}),
			},
		},
		{
			Kind: "pm", Name: "PM 部（可选）",
			Mission: "承担全局产品/计划/优先级判断（经助理/用户确认）。",
			Roles: []RoleCard{
				role("PM", "supervisor", "", "全局产品判断、路线图与优先级建议。",
					[]string{"全局优先级建议", "路线图"},
					[]string{"产出全局计划", "向助理提交建议"}, []string{"直接指挥其他部门员工", "绕过助理找用户"}),
			},
		},
	}
}

// TemplateByKind 按 kind 查找模板。
func TemplateByKind(kind string) (DepartmentTemplate, bool) {
	for _, t := range DepartmentTemplates() {
		if t.Kind == kind {
			return t, true
		}
	}
	return DepartmentTemplate{}, false
}

// CharterJSON 生成部门章程 JSON（写入 departments.charter）。
func (t DepartmentTemplate) CharterJSON() string {
	roles := make([]map[string]any, 0, len(t.Roles))
	for _, r := range t.Roles {
		roles = append(roles, map[string]any{
			"name": r.Name, "kind": r.Kind, "perspective": r.Perspective,
			"mission": r.Mission, "memory_scope": r.MemoryScope,
			"decision_rights": r.DecisionRights, "can_do": r.CanDo, "cannot_do": r.CannotDo,
		})
	}
	b, _ := json.Marshal(map[string]any{
		"mission":       t.Mission,
		"roles":         roles,
		"quality_gates": []string{"DoR", "DoD", "G2（高风险）", "G3（功能/里程碑/高风险）"},
		"isolation":     "部门内自由讨论；跨部门经主管；主管-用户经助理；用户可观察",
		"tools":         AllTools(),
	})
	return string(b)
}

// TemplateKinds 返回全部模板 kind（稳定顺序）。
func TemplateKinds() []string {
	out := make([]string, 0, 8)
	for _, t := range DepartmentTemplates() {
		out = append(out, t.Kind)
	}
	sort.Strings(out)
	return out
}

// roleSlug 用于确定性 Agent ID（幂等 provisioning）。
func roleSlug(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r > 127:
			// 中文名用固定短哈希后缀，避免 ID 含非 ASCII
			b.WriteString("x")
		}
	}
	s := strings.Trim(b.String(), "x")
	if s == "" {
		s = "role"
	}
	return s
}

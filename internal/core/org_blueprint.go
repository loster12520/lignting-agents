package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// OrgStore 是 R3 组织/蓝图/Provisioning 的持久化端口（*store.Store 实现）。
type OrgStore interface {
	// Intent / Blueprint / Provisioning
	GetIntentContent(ctx context.Context) (domain.Intent, error)
	CreateBlueprint(ctx context.Context, bp domain.Blueprint) (domain.Blueprint, error)
	GetBlueprint(ctx context.Context, id string) (domain.Blueprint, error)
	UpdateBlueprintStatus(ctx context.Context, id, status, approvedAt string) error
	CreateProvisioning(ctx context.Context, p domain.Provisioning) (domain.Provisioning, error)
	GetProvisioning(ctx context.Context, id string) (domain.Provisioning, error)
	UpdateProvisioning(ctx context.Context, p domain.Provisioning) error

	// G1 approval
	CreateApproval(ctx context.Context, a domain.Approval) (domain.Approval, error)
	GetApproval(ctx context.Context, id string) (domain.Approval, error)
	DecideApproval(ctx context.Context, id, decision, decidedBy, reason string) (domain.Approval, error)

	// 部门 / Agent / 会话
	CreateDepartment(ctx context.Context, d domain.Department) (domain.Department, error)
	GetDepartment(ctx context.Context, id string) (domain.Department, error)
	ListDepartmentsByStatus(ctx context.Context, status string) ([]domain.Department, error)
	UpdateDepartmentStatus(ctx context.Context, id, status string) error
	UpdateDepartment(ctx context.Context, d domain.Department) error
	CreateAgent(ctx context.Context, a domain.Agent) (domain.Agent, error)
	GetAgent(ctx context.Context, id string) (domain.Agent, error)
	ListAgentsByDepartment(ctx context.Context, departmentID string) ([]domain.Agent, error)
	UpdateAgent(ctx context.Context, a domain.Agent) error
	UpdateAgentStatus(ctx context.Context, id, status string) error
	CreateConversationForOrg(ctx context.Context, c domain.Conversation, members []domain.ConversationMember, actorType, actorID string) (domain.Conversation, error)
	GetConversation(ctx context.Context, id string) (domain.Conversation, error)
	UpdateConversation(ctx context.Context, c domain.Conversation) (domain.Conversation, error)
	AddConversationMember(ctx context.Context, m domain.ConversationMember) error

	AppendEvent(ctx context.Context, ev domain.Event) (domain.Event, error)
	AppendAudit(ctx context.Context, a domain.AuditEntry) (domain.AuditEntry, error)
}

// BlueprintOption 是蓝图的可选方案（至少两个：精简版/完整版）。
type BlueprintOption struct {
	Name            string   `json:"name"`
	Summary         string   `json:"summary"`
	DepartmentKinds []string `json:"department_kinds"`
	Tradeoffs       []string `json:"tradeoffs"`
	Cost            string   `json:"cost"`
}

// BlueprintContent 是 blueprints.content 的结构（供 G1 审批与 provisioning 使用）。
type BlueprintContent struct {
	Intent          string            `json:"intent"`
	Mission         string            `json:"mission"`
	SuccessCriteria []string          `json:"success_criteria"`
	Options         []BlueprintOption `json:"options"`
	ChosenOption    string            `json:"chosen_option"`
	Departments     []DepartmentPlan  `json:"departments"`
	Budget          string            `json:"budget"`
	Risks           []string          `json:"risks"`
	MemoryScopes    []string          `json:"memory_scopes"`
}

// DepartmentPlan 是蓝图里一个部门的创建计划。
type DepartmentPlan struct {
	Kind    string     `json:"kind"`
	Name    string     `json:"name"`
	Mission string     `json:"mission"`
	Roles   []RoleCard `json:"roles"`
}

// BuildBlueprint 根据意图与所选方案生成蓝图内容；option 为空时默认 full。
func BuildBlueprint(intent, option string) (BlueprintContent, error) {
	options := BuildBlueprintOptions(intent)
	chosen := option
	if chosen == "" {
		chosen = "full"
	}
	var selected *BlueprintOption
	for i := range options {
		if options[i].Name == chosen {
			selected = &options[i]
			break
		}
	}
	if selected == nil {
		return BlueprintContent{}, fmt.Errorf("未知蓝图方案 %q（可用：lean/full）", option)
	}
	var plans []DepartmentPlan
	for _, kind := range selected.DepartmentKinds {
		tpl, ok := TemplateByKind(kind)
		if !ok {
			return BlueprintContent{}, fmt.Errorf("未知部门模板 %q", kind)
		}
		roles := append([]RoleCard(nil), tpl.Roles...)
		if chosen == "lean" && len(roles) > 2 {
			roles = roles[:2] // 精简版：主管 + 1 员工
		}
		plans = append(plans, DepartmentPlan{Kind: tpl.Kind, Name: tpl.Name, Mission: tpl.Mission, Roles: roles})
	}
	return BlueprintContent{
		Intent:          strings.TrimSpace(intent),
		Mission:         "交付：" + strings.TrimSpace(intent),
		SuccessCriteria: []string{"需求有 PRD 与验收标准", "实现可运行并有测试证据", "通过 G3 人类验收"},
		Options:         options,
		ChosenOption:    chosen,
		Departments:     plans,
		Budget:          "按日/月预算由用户设定；超 80% 告警、100% 暂停",
		Risks:           []string{"范围蔓延", "跨部门接口不一致", "测试证据不足"},
		MemoryScopes:    []string{"team", "org"},
	}, nil
}

// BuildBlueprintOptions 返回两个可选方案（精简版/完整版）及取舍。
func BuildBlueprintOptions(intent string) []BlueprintOption {
	base := []string{"product", "engineering", "qa"}
	full := append(append([]string{}, base...), "platform")
	return []BlueprintOption{
		{
			Name: "lean", Summary: "精简版：产品/研发/测试 三部门，研发 2 名员工，先跑通闭环。",
			DepartmentKinds: base,
			Tradeoffs:       []string{"成本低、启动快", "人手少，跨部门评审覆盖有限", "平台/架构挑战需由研发主管兼"},
			Cost:            "约 7 个 Agent，成本较低",
		},
		{
			Name: "full", Summary: "完整版：增加平台部门与更多研发视角，覆盖跨部门挑战。",
			DepartmentKinds: full,
			Tradeoffs:       []string{"架构/接口评审更独立，质量更好", "Agent 与协调成本更高", "启动与 kickoff 更慢"},
			Cost:            "约 9–10 个 Agent，成本较高",
		},
	}
}

// approvalIDFor 让蓝图与 G1 approval 一一对应（幂等）。
func approvalIDFor(blueprintID string) string { return "appr_" + blueprintID }

// provisioningIDFor 让一个蓝图只有一个 provisioning（幂等）。
func provisioningIDFor(blueprintID string) string { return "prov_" + blueprintID }

// OrgService 提供 R3 的组织/蓝图/Provisioning/生命周期能力。
type OrgService struct {
	store OrgStore
	rt    *Runtime
	now   func() time.Time
}

// NewOrgService 构造组织服务；rt 可为 nil（仅做组织管理、不 stop Agent）。
func NewOrgService(store OrgStore, rt *Runtime) *OrgService {
	return &OrgService{store: store, rt: rt, now: func() time.Time { return time.Now().UTC() }}
}

func (s *OrgService) nowString() string { return s.now().Format(time.RFC3339Nano) }

// ProposeBlueprint 生成 blueprint + G1 approval（不创建部门）。
func (s *OrgService) ProposeBlueprint(ctx context.Context, assistant domain.Agent, intentID, intentContent, option string) (domain.Blueprint, domain.Approval, error) {
	if assistant.Kind != domain.AgentKindAssistant {
		return domain.Blueprint{}, domain.Approval{}, NewForbidden("只有助理可以提出项目/部门蓝图", "请由助理发起；部门主管可提出本部门方案建议。")
	}
	content, err := BuildBlueprint(intentContent, option)
	if err != nil {
		return domain.Blueprint{}, domain.Approval{}, domain.NewError(domain.CodeInvalid, err.Error(), 400)
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return domain.Blueprint{}, domain.Approval{}, err
	}
	now := s.nowString()
	bp, err := s.store.CreateBlueprint(ctx, domain.Blueprint{
		ID: domain.NewID("bp"), IntentID: intentID, Content: string(raw),
		Status: domain.BlueprintProposed, CreatedAt: now,
	})
	if err != nil {
		return domain.Blueprint{}, domain.Approval{}, fmt.Errorf("core: 创建蓝图失败: %w", err)
	}
	appr, err := s.store.CreateApproval(ctx, domain.Approval{
		ID: approvalIDFor(bp.ID), Kind: domain.ApprovalG1, Title: "批准项目/部门蓝图",
		Payload: mustJSON(map[string]any{"blueprint_id": bp.ID, "intent_id": intentID, "chosen_option": content.ChosenOption, "departments": len(content.Departments)}),
		Risk:    "high", Status: domain.ApprovalPending, RequestedBy: assistant.ID,
		Scope: "once", CreatedAt: now,
	})
	if err != nil {
		return domain.Blueprint{}, domain.Approval{}, fmt.Errorf("core: 创建 G1 审批失败: %w", err)
	}
	s.emit(ctx, "blueprint.proposed", "", "blueprint", bp.ID, "agent", assistant.ID, map[string]any{"approval_id": appr.ID})
	s.audit(ctx, "agent", assistant.ID, "blueprint.propose", "blueprint", bp.ID, "ok", map[string]any{"approval_id": appr.ID})
	return bp, appr, nil
}

// ApproveBlueprint 记录 G1 批准并把蓝图置为 approved。
func (s *OrgService) ApproveBlueprint(ctx context.Context, blueprintID, decidedBy, reason string) (domain.Blueprint, error) {
	bp, err := s.store.GetBlueprint(ctx, blueprintID)
	if err != nil {
		return domain.Blueprint{}, err
	}
	if bp.Status == domain.BlueprintApproved {
		return bp, nil
	}
	if bp.Status != domain.BlueprintProposed && bp.Status != domain.BlueprintDraft {
		return domain.Blueprint{}, domain.NewError(domain.CodeConflict, "蓝图当前状态不可批准："+bp.Status, 409)
	}
	appr, err := s.store.GetApproval(ctx, approvalIDFor(bp.ID))
	if err != nil {
		return domain.Blueprint{}, fmt.Errorf("core: 读取 G1 审批失败: %w", err)
	}
	if appr.Status != domain.ApprovalApproved {
		if appr.Status == domain.ApprovalRejected {
			return domain.Blueprint{}, domain.NewError(domain.CodeConflict, "G1 已被拒绝，不能批准蓝图", 409)
		}
		if _, err := s.store.DecideApproval(ctx, appr.ID, domain.ApprovalApproved, decidedBy, reason); err != nil {
			return domain.Blueprint{}, fmt.Errorf("core: 记录 G1 批准失败: %w", err)
		}
	}
	approvedAt := s.nowString()
	if err := s.store.UpdateBlueprintStatus(ctx, bp.ID, domain.BlueprintApproved, approvedAt); err != nil {
		return domain.Blueprint{}, err
	}
	s.emit(ctx, "blueprint.approved", "", "blueprint", bp.ID, "user", decidedBy, map[string]any{"approval_id": appr.ID})
	s.audit(ctx, "user", decidedBy, "blueprint.approve", "blueprint", bp.ID, "ok", map[string]any{"approval_id": appr.ID, "reason": reason})
	bp.Status = domain.BlueprintApproved
	bp.ApprovedAt = approvedAt
	return bp, nil
}

// RejectBlueprint 记录 G1 拒绝（蓝图变 rejected，不创建部门）。
func (s *OrgService) RejectBlueprint(ctx context.Context, blueprintID, decidedBy, reason string) (domain.Blueprint, error) {
	bp, err := s.store.GetBlueprint(ctx, blueprintID)
	if err != nil {
		return domain.Blueprint{}, err
	}
	if bp.Status != domain.BlueprintProposed && bp.Status != domain.BlueprintDraft {
		return domain.Blueprint{}, domain.NewError(domain.CodeConflict, "蓝图当前状态不可拒绝："+bp.Status, 409)
	}
	if _, err := s.store.DecideApproval(ctx, approvalIDFor(bp.ID), domain.ApprovalRejected, decidedBy, reason); err != nil {
		return domain.Blueprint{}, fmt.Errorf("core: 记录 G1 拒绝失败: %w", err)
	}
	if err := s.store.UpdateBlueprintStatus(ctx, bp.ID, domain.BlueprintRejected, ""); err != nil {
		return domain.Blueprint{}, err
	}
	s.emit(ctx, "blueprint.rejected", "", "blueprint", bp.ID, "user", decidedBy, map[string]any{"reason": reason})
	s.audit(ctx, "user", decidedBy, "blueprint.reject", "blueprint", bp.ID, "ok", map[string]any{"reason": reason})
	bp.Status = domain.BlueprintRejected
	return bp, nil
}

func (s *OrgService) emit(ctx context.Context, typ, convID, objectType, objectID, actorType, actorID string, payload map[string]any) {
	_, _ = s.store.AppendEvent(ctx, domain.Event{
		Type: typ, ConversationID: convID, ObjectType: objectType, ObjectID: objectID,
		ActorType: actorType, ActorID: actorID, Payload: mustJSON(payload), CreatedAt: s.nowString(),
	})
}

func (s *OrgService) audit(ctx context.Context, actorType, actorID, action, objectType, objectID, result string, detail map[string]any) {
	_, _ = s.store.AppendAudit(ctx, domain.AuditEntry{
		ActorType: actorType, ActorID: actorID, Action: action,
		ObjectType: objectType, ObjectID: objectID, Result: result, Detail: mustJSON(detail), CreatedAt: s.nowString(),
	})
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// parseBlueprintContent 解析 content JSON。
func parseBlueprintContent(raw string) (BlueprintContent, error) {
	var c BlueprintContent
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return BlueprintContent{}, fmt.Errorf("core: 蓝图内容损坏: %w", err)
	}
	if c.ChosenOption == "" {
		c.ChosenOption = "full"
	}
	if len(c.Departments) == 0 {
		return BlueprintContent{}, fmt.Errorf("core: 蓝图没有部门计划")
	}
	return c, nil
}

// OrgChartView 是组织图 DTO（/api/org 用）。
type OrgChartView struct {
	Departments []DepartmentView `json:"departments"`
	Agents      []AgentView      `json:"agents"`
}

// DepartmentView 是部门 + 主管 + 成员的投影。
type DepartmentView struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Kind       string      `json:"kind"`
	Status     string      `json:"status"`
	Supervisor AgentView   `json:"supervisor"`
	Agents     []AgentView `json:"agents"`
}

// AgentView 是 Agent 状态投影。
type AgentView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Perspective   string `json:"perspective"`
	Status        string `json:"status"`
	CurrentTaskID string `json:"current_task_id"`
}

// OrgChart 读取当前组织图（只读）。
func (s *OrgService) OrgChart(ctx context.Context) (OrgChartView, error) {
	depts, err := s.store.ListDepartmentsByStatus(ctx, "")
	if err != nil {
		return OrgChartView{}, err
	}
	sort.Slice(depts, func(i, j int) bool { return depts[i].CreatedAt < depts[j].CreatedAt })
	view := OrgChartView{Departments: make([]DepartmentView, 0, len(depts))}
	for _, d := range depts {
		agents, err := s.store.ListAgentsByDepartment(ctx, d.ID)
		if err != nil {
			return OrgChartView{}, err
		}
		dv := DepartmentView{ID: d.ID, Name: d.Name, Kind: d.Kind, Status: d.Status}
		for _, a := range agents {
			av := agentView(a)
			view.Agents = append(view.Agents, av)
			if a.Kind == domain.AgentKindSupervisor && dv.Supervisor.ID == "" {
				dv.Supervisor = av
			}
			dv.Agents = append(dv.Agents, av)
		}
		view.Departments = append(view.Departments, dv)
	}
	return view, nil
}

func agentView(a domain.Agent) AgentView {
	return AgentView{ID: a.ID, Name: a.Name, Kind: a.Kind, Perspective: a.Perspective, Status: a.Status, CurrentTaskID: a.CurrentTaskID}
}

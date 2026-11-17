package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Policy 负责动作授权与沟通路由隔离（requirements 6.6 / 9.1-8）。
// 它是纯规则层：读上下文，不写库。
type Policy struct {
	lookup PolicyLookup
	now    func() time.Time
}

// PolicyLookup 是 Policy 需要的只读查询（store 实现）。
type PolicyLookup interface {
	GetTask(ctx context.Context, id string) (domain.Task, error)
	GetDepartment(ctx context.Context, id string) (domain.Department, error)
}

// NewPolicy 构造策略；lookup 可为 nil（仅做静态角色校验，用于测试）。
func NewPolicy(lookup PolicyLookup) *Policy {
	return &Policy{lookup: lookup, now: func() time.Time { return time.Now().UTC() }}
}

// ActionContext 是动作授权的上下文。
type ActionContext struct {
	Agent        domain.Agent
	Conversation domain.Conversation
	Task         *domain.Task // transition_task/submit_verdict 需要
	Bridge       *domain.Bridge
}

// 动作 → 允许的 Agent kind（空表示走动态校验）。
var actionKinds = map[string][]string{
	"save_intent":             {domain.AgentKindAssistant},
	"propose_blueprint":       {domain.AgentKindAssistant},
	"provision_departments":   {domain.AgentKindAssistant},
	"create_task":             {domain.AgentKindAssistant, domain.AgentKindSupervisor},
	"submit_artifact":         {domain.AgentKindEmployee, domain.AgentKindSupervisor},
	"request_review":          {domain.AgentKindAssistant, domain.AgentKindSupervisor, domain.AgentKindEmployee},
	"create_decision":         {domain.AgentKindAssistant, domain.AgentKindSupervisor},
	"request_approval":        {domain.AgentKindAssistant, domain.AgentKindSupervisor},
	"submit_memory_candidate": {domain.AgentKindAssistant, domain.AgentKindSupervisor, domain.AgentKindEmployee},
}

// AuthorizeAction 校验动作白名单 + 角色 + 状态机，并给出可执行的替代路径。
func (p *Policy) AuthorizeAction(ctx context.Context, ac ActionContext, action domain.Action) error {
	if err := action.Validate(); err != nil {
		return NewForbidden(fmt.Sprintf("动作 %s 参数不合法：%v", action.Action, err), "请补全必填参数后重新提交。")
	}
	switch action.Action {
	case "transition_task":
		return p.authorizeTransition(ctx, ac, action)
	case "submit_verdict":
		return p.authorizeVerdict(ac, action)
	}
	allowed, ok := actionKinds[action.Action]
	if !ok {
		return NewForbidden(fmt.Sprintf("动作 %s 不在白名单", action.Action), "可执行动作见 implementation-plan 2.3。")
	}
	for _, kind := range allowed {
		if ac.Agent.Kind == kind {
			return nil
		}
	}
	return NewForbidden(
		fmt.Sprintf("角色 %s（%s）不能执行动作 %s", ac.Agent.Name, ac.Agent.Kind, action.Action),
		alternativePath(ac.Agent, action.Action),
	)
}

func (p *Policy) authorizeTransition(ctx context.Context, ac ActionContext, action domain.Action) error {
	task := ac.Task
	if task == nil && p.lookup != nil {
		taskID := argString(action.Args, "task_id")
		if taskID != "" {
			t, err := p.lookup.GetTask(ctx, taskID)
			if err == nil {
				task = &t
			}
		}
	}
	if task == nil {
		return NewForbidden("transition_task 缺少任务上下文", "请在任务线程内操作，或先创建任务。")
	}
	to := argString(action.Args, "to")
	if to == "" {
		return NewForbidden("transition_task 缺少目标状态", "请提供 to 字段。")
	}
	if !domain.IsTaskStatus(to) {
		return NewForbidden(fmt.Sprintf("未知任务状态 %q", to), "可用状态见 requirements 15.1。")
	}
	if !domain.CanTransition(task.Status, to) {
		return NewForbidden(
			fmt.Sprintf("任务状态不能从 %s 直接到 %s", task.Status, to),
			fmt.Sprintf("允许的下一步：%v", domain.AllowedTransitions(task.Status)),
		)
	}
	roleOK := ac.Agent.ID == task.OwnerID || ac.Agent.ID == task.ReviewerID || ac.Agent.ID == task.ApproverID
	if !roleOK && !supervisorCanTransition(ac.Agent, task, to) {
		return NewForbidden(
			fmt.Sprintf("%s 不能推进任务 %s 到 %s", ac.Agent.Name, task.ID, to),
			"任务 owner 可推进自己的任务；reviewer/approver 负责评审与验收；部门主管可推进本部门任务的讨论/执行/阻塞状态。",
		)
	}
	return nil
}

// supervisorTransitionEdges 定义部门主管可推进的边（requirements 15.1 / FR-TASK-08）。
// in_review→approved 与 approved→done 仍只允许 reviewer/approver。
var supervisorTransitionEdges = map[string]map[string]bool{
	"proposed":          {"ready": true, "canceled": true},
	"ready":             {"discussing": true, "canceled": true},
	"discussing":        {"in_progress": true, "changes_requested": true, "canceled": true},
	"in_progress":       {"blocked": true, "canceled": true},
	"blocked":           {"in_progress": true, "canceled": true},
	"changes_requested": {"canceled": true},
}

func supervisorCanTransition(agent domain.Agent, task *domain.Task, to string) bool {
	if agent.Kind != domain.AgentKindSupervisor || task == nil {
		return false
	}
	if agent.DepartmentID == "" || agent.DepartmentID != task.DepartmentID {
		return false
	}
	return supervisorTransitionEdges[task.Status][to]
}

func (p *Policy) authorizeVerdict(ac ActionContext, action domain.Action) error {
	// R4 才接入 review 仓储；这里先要求任务上下文并保证「非作者」。
	if ac.Task == nil {
		return NewForbidden("submit_verdict 缺少评审上下文", "请在评审线程内由评审人提交裁决（R4 接入）。")
	}
	if ac.Agent.ID == ac.Task.OwnerID {
		return NewForbidden("作者不能批准自己的产出物", "请由非作者的评审人提交裁决。")
	}
	if ac.Agent.ID != ac.Task.ReviewerID && ac.Agent.ID != ac.Task.ApproverID {
		return NewForbidden("只有任务的 reviewer/approver 可以提裁决", "请转交评审人，或请部门主管指派。")
	}
	return nil
}

// AuthorizeParticipation 判断 Agent 是否可参与某会话（读/发言前置检查）。
func (p *Policy) AuthorizeParticipation(ctx context.Context, agent domain.Agent, conv domain.Conversation, members []domain.ConversationMember) error {
	switch conv.Type {
	case domain.ConvDM:
		if agent.Kind != domain.AgentKindAssistant {
			return NewForbidden(
				fmt.Sprintf("%s 不能直接参与用户私聊", agent.Name),
				"部门主管/员工与用户的沟通需经助理转接；如确需直连，请由用户开通临时桥。",
			)
		}
		return nil
	case domain.ConvDepartment:
		if agent.DepartmentID != conv.DepartmentID {
			return NewForbidden(
				fmt.Sprintf("%s 不属于该部门", agent.Name),
				"跨部门沟通需由部门主管发起评审或申请临时桥。",
			)
		}
		return nil
	case domain.ConvCrossDept:
		if agent.Kind == domain.AgentKindEmployee {
			return NewForbidden("员工不能参加跨部门评审", "请由本部门主管代表部门发言。")
		}
		return nil
	default:
		if !isMember(members, "agent", agent.ID) {
			return NewForbidden(fmt.Sprintf("%s 不是该会话成员", agent.Name), "请先加入会话或由创建者添加成员。")
		}
		return nil
	}
}

// AuthorizeRoute 判断 from 是否可以直接给 to 发消息（Agent 间路由隔离）。
func (p *Policy) AuthorizeRoute(from, to domain.Agent, conv domain.Conversation, bridge *domain.Bridge) error {
	if from.ID == to.ID {
		return NewForbidden("不能给自己发消息", "请直接在当前会话中回复。")
	}
	if bridgeActive(bridge, p.now()) {
		return nil
	}
	if to.Kind == domain.AgentKindAssistant {
		return nil // 任何人可向助理汇报
	}
	if from.Kind == domain.AgentKindAssistant {
		return nil // 助理可路由到任何 Agent
	}
	if from.Kind == domain.AgentKindEmployee && to.DepartmentID != from.DepartmentID {
		return NewForbidden(
			fmt.Sprintf("员工 %s 不能直接联系其他部门的 %s", from.Name, to.Name),
			"请将问题交给本部门主管，由主管对主管对接；用户可开通临时桥。",
		)
	}
	if from.Kind == domain.AgentKindEmployee && to.Kind == domain.AgentKindEmployee {
		return nil // 同部门员工可自由讨论
	}
	if from.Kind == domain.AgentKindSupervisor && to.Kind == domain.AgentKindSupervisor {
		if conv.Type == domain.ConvCrossDept || conv.Type == domain.ConvTemp {
			return nil
		}
		return NewForbidden("部门主管之间默认只通过跨部门评审/临时桥沟通", "请发起跨部门评审，或请用户开通临时桥。")
	}
	return nil
}

func alternativePath(agent domain.Agent, action string) string {
	switch agent.Kind {
	case domain.AgentKindEmployee:
		return "员工可提交产出物/评审请求，或把建议交给本部门主管执行 " + action + "。"
	case domain.AgentKindSupervisor:
		return "部门主管可在本部门内执行；跨部门或涉及项目级动作请交助理/用户。"
	default:
		return "请由具备权限的角色执行，或请求人类确认。"
	}
}

func isMember(members []domain.ConversationMember, principalType, id string) bool {
	for _, m := range members {
		if m.PrincipalType == principalType && m.PrincipalID == id {
			return true
		}
	}
	return false
}

func bridgeActive(b *domain.Bridge, now time.Time) bool {
	if b == nil || b.Status != "active" {
		return false
	}
	if b.ExpiresAt == "" {
		return true
	}
	exp, err := time.Parse(time.RFC3339Nano, b.ExpiresAt)
	if err != nil {
		exp, err = time.Parse(time.RFC3339, b.ExpiresAt)
	}
	if err != nil {
		return false
	}
	return now.Before(exp)
}

func argString(raw json.RawMessage, key string) string {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return ""
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return ""
	}
	return s
}

// NewForbidden 构造统一 403 错误（Code/Message/Details 稳定，便于前端提示）。
func NewForbidden(message, path string) *domain.Error {
	e := domain.NewError(domain.CodeForbidden, message, 403)
	e.Details = map[string]any{"path": path}
	return e
}

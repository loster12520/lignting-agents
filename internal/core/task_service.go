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

// CreateTaskInput 是创建任务的输入（api-contract §8 DoR）。
type CreateTaskInput struct {
	Title           string
	Description     string
	DepartmentID    string
	OwnerID         string
	ReviewerID      string
	ApproverID      string
	Perspective     string
	Priority        string
	Deadline        string
	BudgetTokens    int64
	BudgetCost      float64
	Acceptance      []string
	DependsOn       []string
	SourceMessageID string
	ParentID        string
	HighRisk        bool
}

// WorkService 是 R4 任务/产出/评审/决策/闸门的 core 服务（platform 单向 import）。
type WorkService struct {
	store    WorkStore
	now      func() time.Time
	wipLimit int
}

// NewWorkService 构造服务。
func NewWorkService(store WorkStore) *WorkService {
	return &WorkService{store: store, now: func() time.Time { return time.Now().UTC() }, wipLimit: DefaultWIPLimit}
}

// WithWIPLimit 设置每 Agent 在制品上限（<=0 用默认）。
func (s *WorkService) WithWIPLimit(n int) *WorkService {
	if n > 0 {
		s.wipLimit = n
	}
	return s
}

func (s *WorkService) nowString() string { return s.now().Format(time.RFC3339Nano) }

// CreateTask 创建任务 + 验收标准 + 依赖 + 事件（需满足 DoR）。
func (s *WorkService) CreateTask(ctx context.Context, actor domain.Agent, in CreateTaskInput) (domain.Task, error) {
	if actor.Kind != domain.AgentKindAssistant && actor.Kind != domain.AgentKindSupervisor {
		return domain.Task{}, NewForbidden("只有助理/部门主管可以创建任务", "请把需求交给本部门主管或助理拆解。")
	}
	if actor.Kind == domain.AgentKindSupervisor && actor.DepartmentID != in.DepartmentID {
		return domain.Task{}, NewForbidden("部门主管只能在本部门创建任务", "跨部门任务请由助理或对应部门主管发起。")
	}
	priority := in.Priority
	if priority == "" {
		priority = "P1"
	}
	flags := "{}"
	if in.HighRisk {
		flags = `{"high_risk":true}`
	}
	dor, _ := json.Marshal(map[string]any{
		"acceptance": in.Acceptance, "department_id": in.DepartmentID,
		"owner": in.OwnerID, "reviewer": in.ReviewerID, "approver": in.ApproverID,
		"perspective": in.Perspective, "budget_tokens": in.BudgetTokens,
		"budget_cost": in.BudgetCost, "deadline": in.Deadline,
	})
	task := domain.Task{
		ID: domain.NewID("task"), Title: strings.TrimSpace(in.Title), Description: in.Description,
		Status: "proposed", DepartmentID: in.DepartmentID, OwnerID: in.OwnerID,
		ReviewerID: in.ReviewerID, ApproverID: in.ApproverID, Perspective: in.Perspective,
		Priority: priority, BudgetTokens: in.BudgetTokens, BudgetCost: in.BudgetCost,
		Deadline: in.Deadline, ParentID: in.ParentID, SourceMessageID: in.SourceMessageID,
		DoR: string(dor), DoD: "{}", Flags: flags, CreatedBy: actor.ID,
	}
	var criteria []domain.Criterion
	for i, text := range in.Acceptance {
		if strings.TrimSpace(text) == "" {
			continue
		}
		criteria = append(criteria, domain.Criterion{ID: domain.NewID("crit"), TaskID: task.ID, Ord: i + 1, Text: text})
	}
	if missing := MissingDoR(task, criteria); len(missing) > 0 {
		return domain.Task{}, domain.NewError(domain.CodeInvalid, "不满足 DoR："+strings.Join(missing, "、"), 400)
	}
	// 依赖先校验存在 + 不成环。
	for _, dep := range in.DependsOn {
		if dep == "" || dep == task.ID {
			return domain.Task{}, domain.NewError(domain.CodeInvalid, "非法依赖："+dep, 400)
		}
		if _, err := s.store.GetTask(ctx, dep); err != nil {
			return domain.Task{}, domain.NewError(domain.CodeInvalid, "依赖任务不存在："+dep, 400)
		}
		if cycle := s.detectCycleWith(ctx, task.ID, dep); len(cycle) > 0 {
			return domain.Task{}, domain.NewError(domain.CodeConflict, "依赖成环："+strings.Join(cycle, " -> "), 409)
		}
	}
	created, err := s.store.CreateTask(ctx, task)
	if err != nil {
		return domain.Task{}, fmt.Errorf("core: 创建任务失败: %w", err)
	}
	for _, c := range criteria {
		if _, err := s.store.CreateTaskCriterion(ctx, c); err != nil {
			return domain.Task{}, fmt.Errorf("core: 创建验收标准失败: %w", err)
		}
	}
	for _, dep := range in.DependsOn {
		if err := s.store.AddTaskDependency(ctx, created.ID, dep); err != nil {
			return domain.Task{}, fmt.Errorf("core: 建立依赖失败: %w", err)
		}
	}
	s.appendTaskEvent(ctx, domain.TaskEvent{
		TaskID: created.ID, Type: "task.created", ToStatus: created.Status,
		ActorType: "agent", ActorID: actor.ID, Reason: "create task",
	})
	s.emit(ctx, "task.updated", "task", created.ID, "agent", actor.ID, map[string]any{"status": created.Status, "created": true})
	s.audit(ctx, "agent", actor.ID, "task.create", "task", created.ID, "ok", map[string]any{"department_id": created.DepartmentID})
	if in.HighRisk {
		appr, aerr := s.createGateApproval(ctx, actor, created, domain.ApprovalG2, "G2 技术方案/范围："+created.Title)
		if aerr != nil {
			return domain.Task{}, fmt.Errorf("core: 创建 G2 审批失败: %w", aerr)
		}
		created.Flags = mergeFlag(created.Flags, "g2_approval_id", appr.ID)
		_ = s.store.UpdateTask(ctx, created)
		s.emit(ctx, "approval.updated", "approval", appr.ID, "agent", actor.ID, map[string]any{"kind": domain.ApprovalG2, "task_id": created.ID, "status": appr.Status})
		s.audit(ctx, "agent", actor.ID, "approval.request", "approval", appr.ID, "ok", map[string]any{"kind": domain.ApprovalG2, "task_id": created.ID})
	}
	return created, nil
}

func (s *WorkService) detectCycleWith(ctx context.Context, taskID, dependsOn string) []string {
	deps := map[string][]string{}
	var load func(id string)
	load = func(id string) {
		if _, ok := deps[id]; ok {
			return
		}
		list, err := s.store.ListTaskDependencies(ctx, id)
		if err != nil {
			list = nil
		}
		deps[id] = list
		for _, next := range list {
			load(next)
		}
	}
	load(dependsOn)
	deps[taskID] = append(deps[taskID], dependsOn)
	return WouldCreateCycle(deps, taskID, dependsOn)
}

// TransitionTask 推进任务状态：状态机 + 角色 + 依赖/WIP/G3 校验。
func (s *WorkService) TransitionTask(ctx context.Context, actor domain.Agent, taskID, to, reason, evidence string) (domain.Task, error) {
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	if !domain.IsTaskStatus(to) {
		return domain.Task{}, domain.NewError(domain.CodeInvalid, "未知任务状态："+to, 400)
	}
	if !domain.CanTransition(task.Status, to) {
		return domain.Task{}, domain.NewError(domain.CodeConflict, fmt.Sprintf("任务不能从 %s 直接到 %s", task.Status, to), 409)
	}
	if err := s.authorizeTransition(ctx, actor, &task, to, reason); err != nil {
		return domain.Task{}, err
	}
	if to == "in_progress" {
		if TaskHighRisk(task) {
			if err := s.ensureG2Approved(ctx, actor, &task); err != nil {
				return domain.Task{}, err
			}
		}
		blockers, err := s.dependencyBlockers(ctx, task.ID)
		if err != nil {
			return domain.Task{}, err
		}
		if len(blockers) > 0 {
			return domain.Task{}, domain.NewError(domain.CodeConflict, "依赖未完成："+strings.Join(blockers, "、"), 409)
		}
		active, err := s.store.ListTasksByOwnerStatus(ctx, task.OwnerID, "in_progress")
		if err == nil && len(active) >= s.wipLimit {
			return domain.Task{}, domain.NewError(domain.CodeConflict, fmt.Sprintf("WIP 超限：%s 已有 %d 个进行中任务", task.OwnerID, len(active)), 409)
		}
	}
	if to == "done" {
		g3, err := s.gateFromTaskFlags(ctx, task, "g3_approval_id")
		if err != nil || g3 == nil || g3.Status != domain.ApprovalApproved {
			return domain.Task{}, NewForbidden("缺少已通过的 G3 验收（所有功能/里程碑/高风险任务必须 G3；小任务可由部门主管授权验收，但仍需 G3 核对表与审批记录）", "请先提交 G3 验收核对表与证据，并由人类/授权主管批准后再标记 done。")
		}
	}
	from := task.Status
	task.Status = to
	if err := s.store.UpdateTask(ctx, task); err != nil {
		return domain.Task{}, fmt.Errorf("core: 更新任务失败: %w", err)
	}
	s.appendTaskEvent(ctx, domain.TaskEvent{
		TaskID: task.ID, Type: "task.transition", FromStatus: from, ToStatus: to,
		ActorType: "agent", ActorID: actor.ID, Reason: reason, Evidence: evidence,
	})
	s.emit(ctx, "task.updated", "task", task.ID, "agent", actor.ID, map[string]any{"from": from, "to": to, "reason": reason})
	s.audit(ctx, "agent", actor.ID, "task.transition", "task", task.ID, "ok", map[string]any{"from": from, "to": to, "reason": reason})
	return task, nil
}

func (s *WorkService) authorizeTransition(ctx context.Context, actor domain.Agent, task *domain.Task, to, reason string) error {
	isOwner := actor.ID == task.OwnerID
	isReviewer := actor.ID == task.ReviewerID
	isApprover := actor.ID == task.ApproverID
	isSupervisor := actor.Kind == domain.AgentKindSupervisor && actor.DepartmentID == task.DepartmentID
	isAssistant := actor.Kind == domain.AgentKindAssistant
	switch to {
	case "ready", "discussing", "in_progress":
		if !isOwner && !isSupervisor && !isAssistant {
			return NewForbidden("只有 owner/部门主管/助理可以推进到 "+to, "请由任务 owner 或部门主管操作。")
		}
	case "in_review":
		if !isOwner && !isSupervisor && !isAssistant {
			return NewForbidden("只有 owner/部门主管/助理可以提交评审", "请由任务 owner 提交评审。")
		}
	case "blocked":
		if !isOwner && !isSupervisor && !isAssistant {
			return NewForbidden("只有 owner/部门主管可以标记阻塞", "请由任务 owner 或部门主管操作。")
		}
		if strings.TrimSpace(reason) == "" {
			return domain.NewError(domain.CodeInvalid, "标记阻塞必须写原因", 400)
		}
	case "changes_requested":
		if !isReviewer && !isSupervisor {
			return NewForbidden("只有 reviewer/部门主管可以要求返工", "请由评审人或部门主管提交裁决。")
		}
	case "approved":
		if actor.ID == task.OwnerID {
			return NewForbidden("作者不能批准自己的任务", "请由非作者的 reviewer/approver 审批。")
		}
		if !isReviewer && !isApprover && !isSupervisor {
			return NewForbidden("只有 reviewer/approver/部门主管可以批准任务", "请转交评审人。")
		}
	case "done":
		if !isApprover && !isSupervisor && !isAssistant {
			return NewForbidden("只有 approver/部门主管/助理可以关闭任务", "请由 approver 完成最终确认。")
		}
	case "canceled":
		if !isOwner && !isSupervisor && !isAssistant {
			return NewForbidden("只有 owner/部门主管/助理可以取消任务", "请由任务 owner 或部门主管操作。")
		}
	}
	return nil
}

func (s *WorkService) dependencyBlockers(ctx context.Context, taskID string) ([]string, error) {
	deps, err := s.store.ListTaskDependencies(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var tasks []domain.Task
	for _, dep := range deps {
		if t, err := s.store.GetTask(ctx, dep); err == nil {
			tasks = append(tasks, t)
		}
	}
	return DependencyBlockers(tasks), nil
}

// SetCriterionEvidence 填写验收证据（owner/主管）。
func (s *WorkService) SetCriterionEvidence(ctx context.Context, actor domain.Agent, taskID, criterionID, evidence string) error {
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if actor.ID != task.OwnerID && !(actor.Kind == domain.AgentKindSupervisor && actor.DepartmentID == task.DepartmentID) && actor.Kind != domain.AgentKindAssistant {
		return NewForbidden("只有 owner/部门主管/助理可以填写证据", "请由任务 owner 补充证据。")
	}
	criteria, err := s.store.ListTaskCriteria(ctx, taskID)
	if err != nil {
		return err
	}
	for _, c := range criteria {
		if c.ID != criterionID {
			continue
		}
		c.Evidence = evidence
		c.UpdatedAt = s.nowString()
		if err := s.store.UpdateTaskCriterion(ctx, c); err != nil {
			return err
		}
		s.emit(ctx, "task.updated", "task", taskID, "agent", actor.ID, map[string]any{"criterion": c.ID, "evidence": true})
		return nil
	}
	return domain.NewError(domain.CodeNotFound, "验收标准不存在："+criterionID, 404)
}

// SetCriterionVerdict 记录主管对单条验收标准的结论。
func (s *WorkService) SetCriterionVerdict(ctx context.Context, actor domain.Agent, taskID, criterionID, verdict string) error {
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if !(actor.Kind == domain.AgentKindSupervisor && actor.DepartmentID == task.DepartmentID) && actor.Kind != domain.AgentKindAssistant {
		return NewForbidden("只有部门主管/助理可以填写验收结论", "请由本部门主管核对。")
	}
	allowed := map[string]bool{"approved": true, "changes_requested": true, "human_needed": true}
	if !allowed[verdict] {
		return domain.NewError(domain.CodeInvalid, "未知验收结论："+verdict, 400)
	}
	criteria, err := s.store.ListTaskCriteria(ctx, taskID)
	if err != nil {
		return err
	}
	for _, c := range criteria {
		if c.ID != criterionID {
			continue
		}
		c.SupervisorVerdict = verdict
		c.UpdatedAt = s.nowString()
		if err := s.store.UpdateTaskCriterion(ctx, c); err != nil {
			return err
		}
		s.emit(ctx, "task.updated", "task", taskID, "agent", actor.ID, map[string]any{"criterion": c.ID, "verdict": verdict})
		return nil
	}
	return domain.NewError(domain.CodeNotFound, "验收标准不存在："+criterionID, 404)
}

// TaskView 组装任务详情（DoR/DoD/证据/依赖）。
func (s *WorkService) TaskView(ctx context.Context, taskID string) (TaskView, error) {
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return TaskView{}, err
	}
	criteria, _ := s.store.ListTaskCriteria(ctx, taskID)
	deps, _ := s.store.ListTaskDependencies(ctx, taskID)
	view := TaskView{Task: task, DependsOn: deps}
	for _, c := range criteria {
		view.Criteria = append(view.Criteria, CriterionView{
			ID: c.ID, Text: c.Text, Evidence: c.Evidence,
			SupervisorVerdict: c.SupervisorVerdict, HumanDecision: c.HumanDecision,
		})
	}
	view.DoR = MissingDoR(task, criteria)
	view.DoD = MissingDoD(task, criteria, view.Artifacts)
	view.Ready = len(view.DoR) == 0
	return view, nil
}

// SubmitG3 提交 G3 人类验收（缺证据/结论直接拒绝）。
func (s *WorkService) SubmitG3(ctx context.Context, actor domain.Agent, taskID, risk, residualRisks string) (domain.Approval, error) {
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return domain.Approval{}, err
	}
	if !(actor.Kind == domain.AgentKindSupervisor && actor.DepartmentID == task.DepartmentID) && actor.Kind != domain.AgentKindAssistant {
		return domain.Approval{}, NewForbidden("只有部门主管/助理可以提交 G3", "请由本部门主管提交验收核对表。")
	}
	criteria, err := s.store.ListTaskCriteria(ctx, taskID)
	if err != nil {
		return domain.Approval{}, err
	}
	card, err := s.BuildG3Card(ctx, task, criteria, risk, residualRisks)
	if err != nil {
		return domain.Approval{}, err
	}
	if !card.Ready {
		return domain.Approval{}, domain.NewError(domain.CodeInvalid, "G3 核对表未就绪："+strings.Join(card.Blockers, "；"), 400)
	}
	payload, _ := json.Marshal(card)
	appr, err := s.store.CreateApproval(ctx, domain.Approval{
		ID: domain.NewID("appr"), Kind: domain.ApprovalG3, Title: "G3 验收：" + task.Title,
		Payload: string(payload), Risk: risk, Status: domain.ApprovalPending,
		RequestedBy: actor.ID, Scope: "once", CreatedAt: s.nowString(),
	})
	if err != nil {
		return domain.Approval{}, err
	}
	// 把 approval 挂到任务的 flags，done 前可反查（v0.0.1 不新增表）。
	var flags map[string]any
	_ = json.Unmarshal([]byte(task.Flags), &flags)
	if flags == nil {
		flags = map[string]any{}
	}
	flags["g3_approval_id"] = appr.ID
	if merged, err := json.Marshal(flags); err == nil {
		task.Flags = string(merged)
		_ = s.store.UpdateTask(ctx, task)
	}
	s.emit(ctx, "approval.updated", "approval", appr.ID, "agent", actor.ID, map[string]any{"kind": domain.ApprovalG3, "task_id": task.ID, "status": appr.Status})
	s.audit(ctx, "agent", actor.ID, "approval.request", "approval", appr.ID, "ok", map[string]any{"kind": domain.ApprovalG3, "task_id": task.ID})
	return appr, nil
}

// BuildG3Card 组装 G3 卡（核对表/成本/风险/产出物版本）。
func (s *WorkService) BuildG3Card(ctx context.Context, task domain.Task, criteria []domain.Criterion, risk, residualRisks string) (G3Card, error) {
	card := G3Card{TaskID: task.ID, Title: task.Title, Cost: task.BudgetCost, Risk: risk}
	for _, c := range criteria {
		view := CriterionView{ID: c.ID, Text: c.Text, Evidence: c.Evidence, SupervisorVerdict: c.SupervisorVerdict, HumanDecision: c.HumanDecision}
		card.Checklist = append(card.Checklist, view)
		if !criterionPassed(c) {
			card.Blockers = append(card.Blockers, "未满足："+c.Text)
		}
		if c.SupervisorVerdict == "human_needed" && c.HumanDecision == "" {
			card.Blockers = append(card.Blockers, "需人类判断："+c.Text)
		}
	}
	if len(criteria) == 0 {
		card.Blockers = append(card.Blockers, "没有验收标准")
	}
	if strings.TrimSpace(residualRisks) != "" {
		card.ResidualRisks = strings.Split(residualRisks, "；")
	}
	card.Ready = len(card.Blockers) == 0
	return card, nil
}

// DecideG3 记录人类 G3 决定；批准后任务可进入 done，并写决策记录。
func (s *WorkService) DecideG3(ctx context.Context, approvalID, decision, decidedBy, reason string) (domain.Approval, error) {
	if decision != domain.ApprovalApproved && decision != domain.ApprovalRejected {
		return domain.Approval{}, domain.NewError(domain.CodeInvalid, "未知 G3 决定："+decision, 400)
	}
	appr, err := s.store.GetApproval(ctx, approvalID)
	if err != nil {
		return domain.Approval{}, err
	}
	if appr.Kind != domain.ApprovalG3 {
		return domain.Approval{}, domain.NewError(domain.CodeInvalid, "不是 G3 审批："+appr.Kind, 400)
	}
	if appr.Status != domain.ApprovalPending {
		return domain.Approval{}, domain.NewError(domain.CodeConflict, "审批已处理："+appr.Status, 409)
	}
	appr, err = s.store.DecideApproval(ctx, approvalID, decision, decidedBy, reason)
	if err != nil {
		return domain.Approval{}, err
	}
	var card G3Card
	if err := json.Unmarshal([]byte(appr.Payload), &card); err == nil && card.TaskID != "" {
		task, terr := s.store.GetTask(ctx, card.TaskID)
		if terr == nil {
			if decision == domain.ApprovalApproved {
				s.appendTaskEvent(ctx, domain.TaskEvent{
					TaskID: task.ID, Type: "task.g3", ActorType: "user", ActorID: decidedBy, Reason: reason,
					Evidence: `{"g3":"approved"}`,
				})
			}
		}
	}
	s.emit(ctx, "approval.updated", "approval", appr.ID, "user", decidedBy, map[string]any{"kind": appr.Kind, "status": appr.Status})
	s.audit(ctx, "user", decidedBy, "approval.decide", "approval", appr.ID, "ok", map[string]any{"decision": decision, "reason": reason})
	return appr, nil
}

func (s *WorkService) approvalFromTaskFlags(ctx context.Context, task domain.Task) (*domain.Approval, error) {
	return s.gateFromTaskFlags(ctx, task, "g3_approval_id")
}

func (s *WorkService) gateFromTaskFlags(ctx context.Context, task domain.Task, flagKey string) (*domain.Approval, error) {
	var flags map[string]any
	if err := json.Unmarshal([]byte(task.Flags), &flags); err != nil {
		return nil, err
	}
	id, _ := flags[flagKey].(string)
	if id == "" {
		return nil, nil
	}
	appr, err := s.store.GetApproval(ctx, id)
	if err != nil {
		return nil, err
	}
	return &appr, nil
}

// createGateApproval 创建 G2/G3 审批，payload 带任务与风险信息。
func (s *WorkService) createGateApproval(ctx context.Context, actor domain.Agent, task domain.Task, kind, title string) (domain.Approval, error) {
	payload, _ := json.Marshal(map[string]any{
		"task_id": task.ID, "title": task.Title, "department_id": task.DepartmentID,
		"priority": task.Priority, "kind": kind, "risk": "high",
	})
	return s.store.CreateApproval(ctx, domain.Approval{
		ID: domain.NewID("appr"), Kind: kind, Title: title, Payload: string(payload),
		Risk: "high", Status: domain.ApprovalPending, RequestedBy: actor.ID,
		Scope: "once", CreatedAt: s.nowString(),
	})
}

// ensureG2Approved 校验高风险任务是否已有批准的 G2；缺失则创建并返回 403 升级路径。
func (s *WorkService) ensureG2Approved(ctx context.Context, actor domain.Agent, task *domain.Task) error {
	appr, err := s.gateFromTaskFlags(ctx, *task, "g2_approval_id")
	if err == nil && appr != nil {
		switch appr.Status {
		case domain.ApprovalApproved:
			return nil
		case domain.ApprovalRejected, domain.ApprovalExpired:
			return NewForbidden("G2 已被拒绝/过期，不能开工", "请与人类确认范围/方案后重新发起 G2。")
		default:
			return NewForbidden("高风险任务等待 G2 批准（"+appr.ID+"）", "请由人类/授权审批人批准 G2 后再进入 in_progress。")
		}
	}
	created, cerr := s.createGateApproval(ctx, actor, *task, domain.ApprovalG2, "G2 技术方案/范围："+task.Title)
	if cerr != nil {
		return fmt.Errorf("core: 创建 G2 审批失败: %w", cerr)
	}
	task.Flags = mergeFlag(task.Flags, "g2_approval_id", created.ID)
	_ = s.store.UpdateTask(ctx, *task)
	s.emit(ctx, "approval.updated", "approval", created.ID, "agent", actor.ID, map[string]any{"kind": domain.ApprovalG2, "task_id": task.ID, "status": created.Status})
	s.audit(ctx, "agent", actor.ID, "approval.request", "approval", created.ID, "ok", map[string]any{"kind": domain.ApprovalG2, "task_id": task.ID})
	return NewForbidden("高风险任务缺少已批准的 G2（已创建 "+created.ID+"）", "G2 审批已创建，请人类/授权审批人批准后重试开工。")
}

// DecideGate 记录 G2/G3 的决定（审批人可为人类或授权主管，均留审计）。
func (s *WorkService) DecideGate(ctx context.Context, approvalID, decision, decidedBy, reason string) (domain.Approval, error) {
	appr, err := s.store.GetApproval(ctx, approvalID)
	if err != nil {
		return domain.Approval{}, err
	}
	if appr.Kind != domain.ApprovalG2 && appr.Kind != domain.ApprovalG3 {
		return domain.Approval{}, domain.NewError(domain.CodeInvalid, "不是 G2/G3 审批："+appr.Kind, 400)
	}
	if decision != domain.ApprovalApproved && decision != domain.ApprovalRejected {
		return domain.Approval{}, domain.NewError(domain.CodeInvalid, "未知审批决定："+decision, 400)
	}
	appr, err = s.store.DecideApproval(ctx, approvalID, decision, decidedBy, reason)
	if err != nil {
		return domain.Approval{}, err
	}
	s.emit(ctx, "approval.updated", "approval", appr.ID, "user", decidedBy, map[string]any{"kind": appr.Kind, "status": appr.Status})
	s.audit(ctx, "user", decidedBy, "approval.decide", "approval", appr.ID, "ok", map[string]any{"decision": decision, "reason": reason})
	return appr, nil
}

// mergeFlag 在 task.Flags JSON 中写入一个字符串键。
func mergeFlag(raw, key, value string) string {
	flags := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &flags)
	if flags == nil {
		flags = map[string]any{}
	}
	flags[key] = value
	merged, err := json.Marshal(flags)
	if err != nil {
		return raw
	}
	return string(merged)
}

func (s *WorkService) appendTaskEvent(ctx context.Context, ev domain.TaskEvent) domain.TaskEvent {
	if ev.CreatedAt == "" {
		ev.CreatedAt = s.nowString()
	}
	out, err := s.store.AppendTaskEvent(ctx, ev)
	if err != nil {
		return ev
	}
	return out
}

func (s *WorkService) emit(ctx context.Context, typ, objectType, objectID, actorType, actorID string, payload map[string]any) {
	b, _ := json.Marshal(payload)
	_, _ = s.store.AppendEvent(ctx, domain.Event{
		Type: typ, ObjectType: objectType, ObjectID: objectID,
		ActorType: actorType, ActorID: actorID, Payload: string(b), CreatedAt: s.nowString(),
	})
}

func (s *WorkService) audit(ctx context.Context, actorType, actorID, action, objectType, objectID, result string, detail map[string]any) {
	b, _ := json.Marshal(detail)
	_, _ = s.store.AppendAudit(ctx, domain.AuditEntry{
		ActorType: actorType, ActorID: actorID, Action: action,
		ObjectType: objectType, ObjectID: objectID, Result: result, Detail: string(b), CreatedAt: s.nowString(),
	})
}

// ListTasks 返回部门任务（platform 只读入口）。
func (s *WorkService) ListTasks(ctx context.Context, departmentID string) ([]domain.Task, error) {
	return s.store.ListTasksByDepartment(ctx, departmentID)
}

// CreateDecision 创建决策记录（不可变；修正用 supersede）。
func (s *WorkService) CreateDecision(ctx context.Context, actor domain.Agent, in domain.Decision) (domain.Decision, error) {
	if in.Title == "" || in.Decision == "" || in.Rationale == "" {
		return domain.Decision{}, domain.NewError(domain.CodeInvalid, "决策记录缺少 title/decision/rationale", 400)
	}
	if actor.Kind != domain.AgentKindAssistant && actor.Kind != domain.AgentKindSupervisor {
		return domain.Decision{}, NewForbidden("只有助理/部门主管可以创建决策记录", "请由助理或主管提交。")
	}
	if in.ID == "" {
		in.ID = domain.NewID("dec")
	}
	if in.Status == "" {
		in.Status = domain.DecisionProposed
	}
	in.CreatedBy = actor.ID
	in.CreatedAt = s.nowString()
	out, err := s.store.CreateDecision(ctx, in)
	if err != nil {
		return domain.Decision{}, err
	}
	s.emit(ctx, "decision.created", "decision", out.ID, "agent", actor.ID, map[string]any{"title": out.Title, "status": out.Status})
	s.audit(ctx, "agent", actor.ID, "decision.create", "decision", out.ID, "ok", nil)
	return out, nil
}

// SupersedeDecision 用新记录取代旧记录（旧记录保留，标注 superseded_by）。
func (s *WorkService) SupersedeDecision(ctx context.Context, actor domain.Agent, oldID, newID string) error {
	if actor.Kind != domain.AgentKindAssistant && actor.Kind != domain.AgentKindSupervisor {
		return NewForbidden("只有助理/部门主管可以 supersede 决策", "请由助理或主管操作。")
	}
	if err := s.store.SupersedeDecision(ctx, oldID, newID); err != nil {
		return err
	}
	s.emit(ctx, "decision.superseded", "decision", oldID, "agent", actor.ID, map[string]any{"superseded_by": newID})
	s.audit(ctx, "agent", actor.ID, "decision.supersede", "decision", oldID, "ok", map[string]any{"superseded_by": newID})
	return nil
}

// 排序辅助（platform 列表稳定输出）。
func sortTasksByCreated(tasks []domain.Task) {
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt < tasks[j].CreatedAt })
}

package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/lignting/lignting-agents/internal/domain"
)

// ProvisionStep 是 provisioning 的持久步骤记录（幂等键 = provisioning_id + step）。
type ProvisionStep struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"` // pending | done | failed
	Error   string   `json:"error,omitempty"`
	Created []string `json:"created,omitempty"`
}

// OrphanReport 是失败回滚后的孤儿对账报告（FR-ASST-04）。
type OrphanReport struct {
	BlueprintID    string   `json:"blueprint_id"`
	ProvisioningID string   `json:"provisioning_id"`
	Departments    []string `json:"departments,omitempty"`
	Agents         []string `json:"agents,omitempty"`
	Conversations  []string `json:"conversations,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

// ProvisionResult 是 provisioning 的结果（成功或回滚后）。
type ProvisionResult struct {
	Provisioning domain.Provisioning `json:"provisioning"`
	Orphans      OrphanReport        `json:"orphans"`
}

var (
	errAlreadyProvisioned = errors.New("core: 该蓝图已完成 provisioning")
)

// Provision 按已批准的蓝图创建部门/主管/员工/部门群聊；幂等、可重试、失败回滚。
func (s *OrgService) Provision(ctx context.Context, blueprintID, actorID string) (ProvisionResult, error) {
	bp, err := s.store.GetBlueprint(ctx, blueprintID)
	if err != nil {
		return ProvisionResult{}, err
	}
	if bp.Status != domain.BlueprintApproved {
		return ProvisionResult{}, NewForbidden("蓝图尚未获得 G1 批准，不能创建部门", "请先让用户批准蓝图（G1），再重新发起 provisioning。")
	}
	content, err := parseBlueprintContent(bp.Content)
	if err != nil {
		return ProvisionResult{}, domain.NewError(domain.CodeInvalid, err.Error(), 400)
	}

	provID := provisioningIDFor(bp.ID)
	prov, getErr := s.store.GetProvisioning(ctx, provID)
	if getErr == nil && prov.Status == domain.ProvisionVerified {
		return ProvisionResult{Provisioning: prov}, nil
	}
	if getErr != nil {
		now := s.nowString()
		prov, err = s.store.CreateProvisioning(ctx, domain.Provisioning{
			ID: provID, BlueprintID: bp.ID, Status: domain.ProvisionPlanned,
			Steps: mustJSON(initialSteps()), CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return ProvisionResult{}, fmt.Errorf("core: 创建 provisioning 失败: %w", err)
		}
	} else {
		// 失败/回滚后重试：重置步骤但保留确定性 ID，已存在的资源会被复用。
		prov.Status = domain.ProvisionPlanned
		prov.Error = ""
		prov.Steps = mustJSON(initialSteps())
		prov.UpdatedAt = s.nowString()
		if err := s.store.UpdateProvisioning(ctx, prov); err != nil {
			return ProvisionResult{}, err
		}
	}
	s.emit(ctx, "provisioning.started", "", "provisioning", prov.ID, "agent", actorID, map[string]any{"blueprint_id": bp.ID})

	steps := initialSteps()
	orphans := OrphanReport{BlueprintID: bp.ID, ProvisioningID: prov.ID}
	createdDepts := make([]string, 0, len(content.Departments))
	createdAgents := make([]string, 0, 8)
	createdConvs := make([]string, 0, len(content.Departments))

	fail := func(step string, cause error) (ProvisionResult, error) {
		markStep(steps, step, "failed", cause.Error(), nil)
		prov.Steps = mustJSON(steps)
		prov.Error = cause.Error()
		rollbackErrs := s.rollback(ctx, createdDepts, createdAgents, createdConvs)
		orphans.Departments = createdDepts
		orphans.Agents = createdAgents
		orphans.Conversations = createdConvs
		orphans.Errors = rollbackErrs
		if len(rollbackErrs) > 0 {
			prov.Status = domain.ProvisionFailed
		} else {
			prov.Status = domain.ProvisionRolledBack
		}
		prov.UpdatedAt = s.nowString()
		_ = s.store.UpdateProvisioning(ctx, prov)
		s.emit(ctx, "provisioning.failed", "", "provisioning", prov.ID, "agent", actorID, map[string]any{
			"step": step, "error": cause.Error(), "status": prov.Status,
		})
		s.audit(ctx, "agent", actorID, "org.provision", "provisioning", prov.ID, "failed", map[string]any{"step": step, "error": cause.Error()})
		return ProvisionResult{Provisioning: prov, Orphans: orphans}, fmt.Errorf("core: provisioning 失败（%s）: %w", step, cause)
	}

	// Step 1: 部门（platform_created）
	for _, plan := range content.Departments {
		deptID := deptIDFor(bp.ID, plan.Kind)
		if existing, err := s.store.GetDepartment(ctx, deptID); err == nil {
			if existing.Status == domain.DeptArchived {
				_ = s.store.UpdateDepartmentStatus(ctx, deptID, domain.DeptActive)
			}
			continue
		}
		tpl, ok := TemplateByKind(plan.Kind)
		if !ok {
			return fail("departments", fmt.Errorf("未知部门模板 %s", plan.Kind))
		}
		charter := tpl.CharterJSON()
		if _, err := s.store.CreateDepartment(ctx, domain.Department{
			ID: deptID, Name: plan.Name, Kind: plan.Kind, Mission: plan.Mission,
			Status: domain.DeptActive, Charter: charter,
		}); err != nil {
			return fail("departments", err)
		}
		createdDepts = append(createdDepts, deptID)
	}
	markStep(steps, "departments", "done", "", createdDepts)
	prov.Steps = mustJSON(steps)
	prov.Status = domain.ProvisionPlatformCreated
	prov.UpdatedAt = s.nowString()
	if err := s.store.UpdateProvisioning(ctx, prov); err != nil {
		return ProvisionResult{}, err
	}

	// Step 2: Agent（core_created）
	for _, plan := range content.Departments {
		deptID := deptIDFor(bp.ID, plan.Kind)
		if _, err := s.store.GetDepartment(ctx, deptID); err != nil {
			return fail("agents", fmt.Errorf("部门 %s 不存在（前一步未完成）", deptID))
		}
		for idx, role := range plan.Roles {
			agentID := agentIDFor(bp.ID, plan.Kind, idx)
			if existing, err := s.store.GetAgent(ctx, agentID); err == nil {
				if existing.Status == domain.AgentPaused {
					_ = s.store.UpdateAgentStatus(ctx, agentID, domain.AgentIdle)
				}
				continue
			}
			agent, err := s.store.CreateAgent(ctx, domain.Agent{
				ID: agentID, Name: role.Name, Kind: role.Kind, DepartmentID: deptID,
				RoleCard: role.JSON(), Perspective: role.Perspective,
				Status: domain.AgentIdle, Model: "deepseek-flash",
			})
			if err != nil {
				return fail("agents", err)
			}
			createdAgents = append(createdAgents, agent.ID)
		}
	}
	markStep(steps, "agents", "done", "", createdAgents)
	prov.Steps = mustJSON(steps)
	prov.Status = domain.ProvisionCoreCreated
	prov.UpdatedAt = s.nowString()
	if err := s.store.UpdateProvisioning(ctx, prov); err != nil {
		return ProvisionResult{}, err
	}

	// Step 3: 部门群聊（任务板/记忆空间按部门/会话命名空间投影；v0.0.1 不建额外表）
	for _, plan := range content.Departments {
		deptID := deptIDFor(bp.ID, plan.Kind)
		convID := convIDFor(bp.ID, plan.Kind)
		if _, err := s.store.GetConversation(ctx, convID); err == nil {
			continue
		}
		members, err := s.departmentMembers(ctx, deptID, actorID)
		if err != nil {
			return fail("conversations", err)
		}
		if _, err := s.store.CreateConversationForOrg(ctx, domain.Conversation{
			ID: convID, Type: domain.ConvDepartment, Title: plan.Name + "（部门群聊）",
			DepartmentID: deptID, Visibility: domain.VisibilityDepartment, Status: "active",
		}, members, "system", "core"); err != nil {
			return fail("conversations", err)
		}
		createdConvs = append(createdConvs, convID)
	}
	markStep(steps, "conversations", "done", "", createdConvs)

	// Step 4: verify（每个部门都要有主管）
	if err := s.verifyDepartments(ctx, bp.ID, content); err != nil {
		return fail("verify", err)
	}
	markStep(steps, "verify", "done", "", nil)
	prov.Steps = mustJSON(steps)
	prov.Status = domain.ProvisionVerified
	prov.Error = ""
	prov.UpdatedAt = s.nowString()
	if err := s.store.UpdateProvisioning(ctx, prov); err != nil {
		return ProvisionResult{}, err
	}
	s.emit(ctx, "provisioning.completed", "", "provisioning", prov.ID, "agent", actorID, map[string]any{"blueprint_id": bp.ID})
	s.audit(ctx, "agent", actorID, "org.provision", "provisioning", prov.ID, "ok", map[string]any{
		"departments": len(createdDepts), "agents": len(createdAgents), "conversations": len(createdConvs),
	})
	return ProvisionResult{Provisioning: prov}, nil
}

// ProvisionStatus 读取 provisioning 状态。
func (s *OrgService) ProvisionStatus(ctx context.Context, blueprintID string) (domain.Provisioning, error) {
	return s.store.GetProvisioning(ctx, provisioningIDFor(blueprintID))
}

func initialSteps() []ProvisionStep {
	return []ProvisionStep{
		{Name: "departments", Status: "pending"},
		{Name: "agents", Status: "pending"},
		{Name: "conversations", Status: "pending"},
		{Name: "verify", Status: "pending"},
	}
}

func markStep(steps []ProvisionStep, name, status, errText string, created []string) {
	for i := range steps {
		if steps[i].Name == name {
			steps[i].Status = status
			steps[i].Error = errText
			if created != nil {
				steps[i].Created = created
			}
			return
		}
	}
}

func (s *OrgService) rollback(ctx context.Context, depts, agents, convs []string) []string {
	var errs []string
	for i := len(convs) - 1; i >= 0; i-- {
		c, err := s.store.GetConversation(ctx, convs[i])
		if err != nil {
			errs = append(errs, "读取会话失败 "+convs[i]+": "+err.Error())
			continue
		}
		c.Status = "archived"
		if _, err := s.store.UpdateConversation(ctx, c); err != nil {
			errs = append(errs, "归档会话失败 "+convs[i]+": "+err.Error())
		}
	}
	for i := len(agents) - 1; i >= 0; i-- {
		if err := s.store.UpdateAgentStatus(ctx, agents[i], domain.AgentPaused); err != nil {
			errs = append(errs, "暂停 Agent 失败 "+agents[i]+": "+err.Error())
		}
	}
	for i := len(depts) - 1; i >= 0; i-- {
		if err := s.store.UpdateDepartmentStatus(ctx, depts[i], domain.DeptArchived); err != nil {
			errs = append(errs, "归档部门失败 "+depts[i]+": "+err.Error())
		}
	}
	return errs
}

func (s *OrgService) departmentMembers(ctx context.Context, deptID, ownerID string) ([]domain.ConversationMember, error) {
	agents, err := s.store.ListAgentsByDepartment(ctx, deptID)
	if err != nil {
		return nil, err
	}
	now := s.nowString()
	members := make([]domain.ConversationMember, 0, len(agents)+1)
	for _, a := range agents {
		members = append(members, domain.ConversationMember{
			PrincipalType: "agent", PrincipalID: a.ID, Role: "member", JoinedAt: now,
		})
	}
	if ownerID != "" {
		members = append(members, domain.ConversationMember{
			PrincipalType: "user", PrincipalID: ownerID, Role: "observer", JoinedAt: now,
		})
	}
	return members, nil
}

func (s *OrgService) verifyDepartments(ctx context.Context, blueprintID string, content BlueprintContent) error {
	for _, plan := range content.Departments {
		deptID := deptIDFor(blueprintID, plan.Kind)
		agents, err := s.store.ListAgentsByDepartment(ctx, deptID)
		if err != nil {
			return err
		}
		hasSupervisor := false
		for _, a := range agents {
			if a.Kind == domain.AgentKindSupervisor {
				hasSupervisor = true
				break
			}
		}
		if !hasSupervisor {
			return fmt.Errorf("部门 %s 缺少主管 Agent", deptID)
		}
	}
	return nil
}

func deptIDFor(blueprintID, kind string) string { return "dept_" + blueprintID + "_" + kind }
func agentIDFor(blueprintID, kind string, idx int) string {
	return fmt.Sprintf("agent_%s_%s_%d", blueprintID, kind, idx)
}
func convIDFor(blueprintID, kind string) string { return "conv_" + blueprintID + "_" + kind }

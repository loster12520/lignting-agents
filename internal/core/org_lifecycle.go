package core

import (
	"context"
	"fmt"

	"github.com/lignting/lignting-agents/internal/domain"
)

// PauseDepartment 暂停部门（不再接新任务/触发 Agent），并暂停其 Agent。
func (s *OrgService) PauseDepartment(ctx context.Context, departmentID, actorID, reason string) error {
	dept, err := s.store.GetDepartment(ctx, departmentID)
	if err != nil {
		return err
	}
	if dept.Status == domain.DeptPaused {
		return nil
	}
	if err := s.store.UpdateDepartmentStatus(ctx, dept.ID, domain.DeptPaused); err != nil {
		return fmt.Errorf("core: 暂停部门失败: %w", err)
	}
	agents, _ := s.store.ListAgentsByDepartment(ctx, dept.ID)
	for _, a := range agents {
		if a.Status == domain.AgentRunning || a.Status == domain.AgentIdle || a.Status == domain.AgentWaiting {
			_ = s.store.UpdateAgentStatus(ctx, a.ID, domain.AgentPaused)
		}
	}
	s.emit(ctx, "department.status", "", "department", dept.ID, "agent", actorID, map[string]any{"status": domain.DeptPaused, "reason": reason})
	s.audit(ctx, "agent", actorID, "org.department.pause", "department", dept.ID, "ok", map[string]any{"reason": reason})
	return nil
}

// ResumeDepartment 恢复部门与其中被暂停的 Agent。
func (s *OrgService) ResumeDepartment(ctx context.Context, departmentID, actorID string) error {
	dept, err := s.store.GetDepartment(ctx, departmentID)
	if err != nil {
		return err
	}
	if dept.Status == domain.DeptActive {
		return nil
	}
	if err := s.store.UpdateDepartmentStatus(ctx, dept.ID, domain.DeptActive); err != nil {
		return fmt.Errorf("core: 恢复部门失败: %w", err)
	}
	agents, _ := s.store.ListAgentsByDepartment(ctx, dept.ID)
	for _, a := range agents {
		if a.Status == domain.AgentPaused {
			_ = s.store.UpdateAgentStatus(ctx, a.ID, domain.AgentIdle)
		}
	}
	s.emit(ctx, "department.status", "", "department", dept.ID, "agent", actorID, map[string]any{"status": domain.DeptActive})
	s.audit(ctx, "agent", actorID, "org.department.resume", "department", dept.ID, "ok", nil)
	return nil
}

// ArchiveDepartment 归档部门：先暂停 Agent，再置 archived；不接新任务。
func (s *OrgService) ArchiveDepartment(ctx context.Context, departmentID, actorID, reason string) error {
	dept, err := s.store.GetDepartment(ctx, departmentID)
	if err != nil {
		return err
	}
	if dept.Status == domain.DeptArchived {
		return nil
	}
	_ = s.PauseDepartment(ctx, departmentID, actorID, "archived: "+reason)
	if err := s.store.UpdateDepartmentStatus(ctx, dept.ID, domain.DeptArchived); err != nil {
		return fmt.Errorf("core: 归档部门失败: %w", err)
	}
	s.emit(ctx, "department.status", "", "department", dept.ID, "agent", actorID, map[string]any{"status": domain.DeptArchived, "reason": reason})
	s.audit(ctx, "agent", actorID, "org.department.archive", "department", dept.ID, "ok", map[string]any{"reason": reason})
	return nil
}

// PauseAgent 暂停单个 Agent（不接新任务）。
func (s *OrgService) PauseAgent(ctx context.Context, agentID, actorID, reason string) error {
	agent, err := s.store.GetAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if agent.Status == domain.AgentPaused {
		return nil
	}
	if err := s.store.UpdateAgentStatus(ctx, agent.ID, domain.AgentPaused); err != nil {
		return fmt.Errorf("core: 暂停 Agent 失败: %w", err)
	}
	s.emit(ctx, "agent.status", "", "agent", agent.ID, "agent", actorID, map[string]any{"agent_id": agent.ID, "status": domain.AgentPaused, "reason": reason})
	s.audit(ctx, "agent", actorID, "org.agent.pause", "agent", agent.ID, "ok", map[string]any{"reason": reason})
	return nil
}

// ResumeAgent 恢复 Agent。
func (s *OrgService) ResumeAgent(ctx context.Context, agentID, actorID string) error {
	agent, err := s.store.GetAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if agent.Status != domain.AgentPaused && agent.Status != domain.AgentFailed {
		return nil
	}
	if err := s.store.UpdateAgentStatus(ctx, agent.ID, domain.AgentIdle); err != nil {
		return fmt.Errorf("core: 恢复 Agent 失败: %w", err)
	}
	s.emit(ctx, "agent.status", "", "agent", agent.ID, "agent", actorID, map[string]any{"agent_id": agent.ID, "status": domain.AgentIdle})
	s.audit(ctx, "agent", actorID, "org.agent.resume", "agent", agent.ID, "ok", nil)
	return nil
}

// StopAgent 停止当前 turn（消息保留）。
func (s *OrgService) StopAgent(ctx context.Context, agentID, actorID string) error {
	if s.rt == nil {
		return fmt.Errorf("core: 未接入 Agent 运行时，无法停止 turn")
	}
	if !s.rt.Interrupt(agentID) {
		return domain.NewError(domain.CodeConflict, "该 Agent 当前没有运行中的 turn", 409)
	}
	s.emit(ctx, "agent.stopped", "", "agent", agentID, "agent", actorID, map[string]any{"agent_id": agentID})
	s.audit(ctx, "agent", actorID, "org.agent.stop", "agent", agentID, "ok", nil)
	return nil
}

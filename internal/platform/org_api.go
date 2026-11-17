package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// OrgService 是 platform 对 core.OrgService 的窄接口（单向 import core，无环）。
// *core.OrgService 结构化满足；测试可注入 fake。
type OrgService interface {
	ProposeBlueprint(ctx context.Context, assistant domain.Agent, intentID, intentContent, option string) (domain.Blueprint, domain.Approval, error)
	ApproveBlueprint(ctx context.Context, blueprintID, decidedBy, reason string) (domain.Blueprint, error)
	RejectBlueprint(ctx context.Context, blueprintID, decidedBy, reason string) (domain.Blueprint, error)
	Provision(ctx context.Context, blueprintID, actorID string) (core.ProvisionResult, error)
	ProvisionStatus(ctx context.Context, blueprintID string) (domain.Provisioning, error)
	PauseDepartment(ctx context.Context, departmentID, actorID, reason string) error
	ResumeDepartment(ctx context.Context, departmentID, actorID string) error
	ArchiveDepartment(ctx context.Context, departmentID, actorID, reason string) error
	PauseAgent(ctx context.Context, agentID, actorID, reason string) error
	ResumeAgent(ctx context.Context, agentID, actorID string) error
	StopAgent(ctx context.Context, agentID, actorID string) error
	OrgChart(ctx context.Context) (core.OrgChartView, error)
}

// SetOrgService 注入 R3 组织服务；cmd 在 store 迁移后用
// core.NewOrgService(st, rt) 注入。未注入时 §7 路由返回 503。
func (s *Server) SetOrgService(svc OrgService) {
	s.orgMu.Lock()
	defer s.orgMu.Unlock()
	s.org = svc
}

func (s *Server) orgService() (OrgService, error) {
	s.orgMu.RLock()
	defer s.orgMu.RUnlock()
	if s.org == nil {
		return nil, domain.NewError(domain.CodeUnavailable, "组织服务未接入（core OrgService 未注入）", http.StatusServiceUnavailable)
	}
	return s.org, nil
}

func (s *Server) requireOrg(w http.ResponseWriter) (OrgService, bool) {
	svc, err := s.orgService()
	if err != nil {
		s.writeErr(w, err)
		return nil, false
	}
	return svc, true
}

func (s *Server) assistantAgent(ctx context.Context) (domain.Agent, error) {
	agents, err := s.store.ListAgentsByKind(ctx, domain.AgentKindAssistant)
	if err != nil {
		return domain.Agent{}, err
	}
	if len(agents) == 0 {
		return domain.Agent{}, domain.NewError(domain.CodeConflict, "助理尚未创建，无法生成蓝图", http.StatusConflict)
	}
	return agents[0], nil
}

// ---- §7 Intents ----

type intentRequest struct {
	Content json.RawMessage `json:"content"`
	Goal    string          `json:"goal"`
}

func (s *Server) handleSaveIntent(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req intentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	content := ""
	if len(req.Content) > 0 && string(req.Content) != "null" {
		raw := strings.TrimSpace(string(req.Content))
		switch {
		case strings.HasPrefix(raw, "{"):
			content = raw
		case strings.HasPrefix(raw, "\""):
			var text string
			if err := json.Unmarshal(req.Content, &text); err != nil {
				s.writeErr(w, errInvalid("content 不合法"))
				return
			}
			b, _ := json.Marshal(map[string]any{"goal": text})
			content = string(b)
		default:
			s.writeErr(w, errInvalid("content 必须是 JSON 对象或字符串"))
			return
		}
	} else if strings.TrimSpace(req.Goal) != "" {
		b, _ := json.Marshal(map[string]any{"goal": strings.TrimSpace(req.Goal)})
		content = string(b)
	} else {
		s.writeErr(w, errInvalid("content 或 goal 必填"))
		return
	}
	id, version, err := s.store.SaveIntent(ctx, content)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "intent.save", "intent", id, "ok", jsonDetail(map[string]any{"version": version}))
	writeOK(w, map[string]any{"id": id, "version": version, "content": content, "status": domain.IntentDraft})
}

func (s *Server) handleLatestIntent(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	intent, err := s.store.GetIntentContent(r.Context())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, intent)
}

// ---- §7 Blueprints ----

type proposeBlueprintRequest struct {
	IntentID string `json:"intent_id"`
	Content  string `json:"content"`
	Option   string `json:"option"`
	Reason   string `json:"reason"`
}

func (s *Server) handleProposeBlueprint(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireOrg(w)
	if !ok {
		return
	}
	var req proposeBlueprintRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	intentID := strings.TrimSpace(req.IntentID)
	intentContent := strings.TrimSpace(req.Content)
	if intentContent == "" {
		intent, err := s.store.GetIntentContent(ctx)
		if err != nil {
			s.writeErr(w, errInvalid("还没有意图卡，请先保存意图（POST /api/intents）"))
			return
		}
		intentContent = intent.Content
		if intentID == "" {
			intentID = intent.ID
		}
	}
	assistant, err := s.assistantAgent(ctx)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	option := strings.TrimSpace(req.Option)
	if option == "" {
		option = "full"
	}
	bp, appr, err := svc.ProposeBlueprint(ctx, assistant, intentID, intentContent, option)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"blueprint": bp, "approval": appr})
}

func (s *Server) handleGetBlueprint(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	bp, err := s.store.GetBlueprint(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, bp)
}

type blueprintDecisionRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleApproveBlueprint(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.requireOrg(w)
	if !ok {
		return
	}
	var req blueprintDecisionRequest
	_ = decodeJSON(w, r, &req)
	bp, err := svc.ApproveBlueprint(r.Context(), r.PathValue("id"), owner.ID, req.Reason)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, bp)
}

func (s *Server) handleRejectBlueprint(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.requireOrg(w)
	if !ok {
		return
	}
	var req blueprintDecisionRequest
	_ = decodeJSON(w, r, &req)
	bp, err := svc.RejectBlueprint(r.Context(), r.PathValue("id"), owner.ID, req.Reason)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, bp)
}

// ---- §7 Provisioning ----

type provisioningRequest struct {
	BlueprintID string `json:"blueprint_id"`
}

func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.requireOrg(w)
	if !ok {
		return
	}
	var req provisioningRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if strings.TrimSpace(req.BlueprintID) == "" {
		s.writeErr(w, errInvalid("blueprint_id 必填"))
		return
	}
	result, err := svc.Provision(r.Context(), req.BlueprintID, owner.ID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (s *Server) handleProvisionStatus(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	id := r.PathValue("id")
	prov, err := s.store.GetProvisioning(r.Context(), id)
	if err == nil {
		writeOK(w, prov)
		return
	}
	// 兼容按 blueprint_id 查询（core.ProvisionStatus 用 blueprintID 推导 prov_<bpID>）。
	svc, ok := s.requireOrg(w)
	if !ok {
		return
	}
	prov, err2 := svc.ProvisionStatus(r.Context(), id)
	if err2 != nil {
		s.writeErr(w, err) // 返回首查的 404
		return
	}
	writeOK(w, prov)
}

// ---- 部门 / Agent 生命周期 ----

type lifecycleRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleDepartmentLifecycle(action string) authedHandler {
	return func(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
		ctx := r.Context()
		svc, ok := s.requireOrg(w)
		if !ok {
			return
		}
		var req lifecycleRequest
		_ = decodeJSON(w, r, &req)
		id := r.PathValue("id")
		var err error
		switch action {
		case "pause":
			err = svc.PauseDepartment(ctx, id, owner.ID, req.Reason)
		case "resume":
			err = svc.ResumeDepartment(ctx, id, owner.ID)
		case "archive":
			err = svc.ArchiveDepartment(ctx, id, owner.ID, req.Reason)
		default:
			err = fmt.Errorf("%w: unknown lifecycle action", store.ErrInvalid)
		}
		if err != nil {
			s.writeErr(w, err)
			return
		}
		dept, err := s.store.GetDepartment(ctx, id)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeOK(w, dept)
	}
}

func (s *Server) handleAgentLifecycle(action string) authedHandler {
	return func(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
		ctx := r.Context()
		svc, ok := s.requireOrg(w)
		if !ok {
			return
		}
		var req lifecycleRequest
		_ = decodeJSON(w, r, &req)
		id := r.PathValue("id")
		var err error
		switch action {
		case "pause":
			err = svc.PauseAgent(ctx, id, owner.ID, req.Reason)
		case "resume":
			err = svc.ResumeAgent(ctx, id, owner.ID)
		case "stop":
			err = svc.StopAgent(ctx, id, owner.ID)
		default:
			err = fmt.Errorf("%w: unknown lifecycle action", store.ErrInvalid)
		}
		if err != nil {
			s.writeErr(w, err)
			return
		}
		agent, err := s.store.GetAgent(ctx, id)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeOK(w, agent)
	}
}

// ---- §6 部门创建 / Agent 配置 ----

type createDepartmentRequest struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Mission     string `json:"mission"`
	BlueprintID string `json:"blueprint_id"`
}

// handleCreateDepartment 手动创建部门：必须已有 G1 批准的蓝图（防绕过人类闸门）。
func (s *Server) handleCreateDepartment(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req createDepartmentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		s.writeErr(w, errInvalid("name 必填"))
		return
	}
	blueprintID := strings.TrimSpace(req.BlueprintID)
	if blueprintID != "" {
		bp, err := s.store.GetBlueprint(ctx, blueprintID)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		if bp.Status != domain.BlueprintApproved {
			s.writeErr(w, errForbidden("蓝图尚未获得 G1 批准，不能创建部门"))
			return
		}
	} else {
		approved, err := s.store.ListBlueprintsByStatus(ctx, domain.BlueprintApproved)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		if len(approved) == 0 {
			s.writeErr(w, errForbidden("尚无 G1 批准的蓝图，不能创建部门"))
			return
		}
		blueprintID = approved[0].ID
	}
	dept, err := s.store.CreateDepartment(ctx, domain.Department{
		Name: req.Name, Kind: req.Kind, Mission: req.Mission, Status: domain.DeptActive,
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	_, _ = s.store.AppendEvent(ctx, domain.Event{
		Type: "department.status", ObjectType: "department", ObjectID: dept.ID,
		ActorType: domain.SenderUser, ActorID: owner.ID,
		Payload: jsonDetail(map[string]any{"department_id": dept.ID, "status": dept.Status, "blueprint_id": blueprintID}),
	})
	s.audit(ctx, domain.SenderUser, owner.ID, "org.department.create", "department", dept.ID, "ok",
		jsonDetail(map[string]any{"blueprint_id": blueprintID, "kind": dept.Kind}))
	writeOK(w, dept)
}

type patchAgentRequest struct {
	Name        *string  `json:"name"`
	Perspective *string  `json:"perspective"`
	RoleCard    *string  `json:"role_card"`
	Model       *string  `json:"model"`
	BudgetLimit *float64 `json:"budget_limit"`
}

// handlePatchAgent 修改 Agent：低风险字段立即生效，模型/预算变更创建审批等待人类批准。
func (s *Server) handlePatchAgent(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	agent, err := s.store.GetAgent(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var req patchAgentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	highRisk := req.Model != nil || req.BudgetLimit != nil
	if highRisk {
		approval, err := s.store.CreateApproval(ctx, domain.Approval{
			ID: domain.NewID("appr"), Kind: domain.ApprovalIrreversible,
			Title: "修改 Agent 配置（高风险）", Risk: "high", Status: domain.ApprovalPending,
			RequestedBy: owner.ID, Scope: "once",
			Payload: jsonDetail(map[string]any{"agent_id": agent.ID, "model": req.Model, "budget_limit": req.BudgetLimit}),
		})
		if err != nil {
			s.writeErr(w, err)
			return
		}
		_, _ = s.store.AppendEvent(ctx, domain.Event{
			Type: "approval.updated", ObjectType: "approval", ObjectID: approval.ID,
			ActorType: domain.SenderUser, ActorID: owner.ID,
			Payload: jsonDetail(map[string]any{"status": approval.Status}),
		})
		s.audit(ctx, domain.SenderUser, owner.ID, "agent.config.request", "agent", agent.ID, "ok",
			jsonDetail(map[string]any{"approval_id": approval.ID}))
		writeOK(w, map[string]any{"applied": false, "approval": approval, "agent": agent})
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		agent.Name = strings.TrimSpace(*req.Name)
	}
	if req.Perspective != nil {
		agent.Perspective = *req.Perspective
	}
	if req.RoleCard != nil {
		agent.RoleCard = *req.RoleCard
	}
	if err := s.store.UpdateAgent(ctx, agent); err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "agent.config.update", "agent", agent.ID, "ok", "{}")
	writeOK(w, map[string]any{"applied": true, "agent": agent})
}

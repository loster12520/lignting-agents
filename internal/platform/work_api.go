package platform

import (
	"context"
	"net/http"
	"strings"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// WorkService 是 platform 对 core.WorkService 的写路径窄接口（单向 import core）。
// *core.WorkService 结构化满足；未注入时写路由返回 503。平台不复制状态机/DoR/权限。
type WorkService interface {
	CreateTask(ctx context.Context, actor domain.Agent, in core.CreateTaskInput) (domain.Task, error)
	TransitionTask(ctx context.Context, actor domain.Agent, taskID, to, reason, evidence string) (domain.Task, error)
	TaskView(ctx context.Context, taskID string) (core.TaskView, error)
	SetCriterionEvidence(ctx context.Context, actor domain.Agent, taskID, criterionID, evidence string) error
	SetCriterionVerdict(ctx context.Context, actor domain.Agent, taskID, criterionID, verdict string) error
	SubmitG3(ctx context.Context, actor domain.Agent, taskID, risk, residualRisks string) (domain.Approval, error)
	DecideG3(ctx context.Context, approvalID, decision, decidedBy, reason string) (domain.Approval, error)
	CreateArtifact(ctx context.Context, actor domain.Agent, in core.ArtifactInput) (domain.Artifact, domain.ArtifactVersion, error)
	AddArtifactVersion(ctx context.Context, actor domain.Agent, artifactID, content, supersedes string) (domain.ArtifactVersion, error)
	ArtifactRefsByTask(ctx context.Context, taskID string) ([]core.ArtifactRef, error)
	CreateReview(ctx context.Context, actor domain.Agent, in core.ReviewInput) (domain.Review, error)
	SubmitVerdict(ctx context.Context, actor domain.Agent, reviewID, verdict, checked, rationale, conditions, residualRisk string) (domain.ReviewVerdict, error)
	Rework(ctx context.Context, actor domain.Agent, reviewID, content string) (domain.ArtifactVersion, error)
	CreateDecision(ctx context.Context, actor domain.Agent, in domain.Decision) (domain.Decision, error)
	SupersedeDecision(ctx context.Context, actor domain.Agent, oldID, newID string) error
	ListTasks(ctx context.Context, departmentID string) ([]domain.Task, error)
}

func (s *Server) SetWorkService(svc WorkService) {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	s.work = svc
}

func (s *Server) requireWork(w http.ResponseWriter) (WorkService, bool) {
	s.workMu.RLock()
	svc := s.work
	s.workMu.RUnlock()
	if svc == nil {
		s.writeErr(w, domain.NewError(domain.CodeUnavailable, "R4 WorkService 未接入（core WorkService 未注入）", http.StatusServiceUnavailable))
		return nil, false
	}
	return svc, true
}

// workActor 解析操作者：显式 actor_agent_id 优先；默认用助理 Agent 代表用户操作。
func (s *Server) workActor(ctx context.Context, explicitID string) (domain.Agent, error) {
	if id := strings.TrimSpace(explicitID); id != "" {
		return s.store.GetAgent(ctx, id)
	}
	return s.assistantAgent(ctx)
}

type createTaskRequest struct {
	ActorAgentID    string   `json:"actor_agent_id"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	DepartmentID    string   `json:"department_id"`
	OwnerID         string   `json:"owner_id"`
	ReviewerID      string   `json:"reviewer_id"`
	ApproverID      string   `json:"approver_id"`
	Perspective     string   `json:"perspective"`
	Priority        string   `json:"priority"`
	Deadline        string   `json:"deadline"`
	BudgetTokens    int64    `json:"budget_tokens"`
	BudgetCost      float64  `json:"budget_cost"`
	Acceptance      []string `json:"acceptance"`
	DependsOn       []string `json:"depends_on"`
	SourceMessageID string   `json:"source_message_id"`
	ParentID        string   `json:"parent_id"`
	HighRisk        bool     `json:"high_risk"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req createTaskRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	task, err := svc.CreateTask(ctx, actor, core.CreateTaskInput{
		Title: req.Title, Description: req.Description, DepartmentID: req.DepartmentID,
		OwnerID: req.OwnerID, ReviewerID: req.ReviewerID, ApproverID: req.ApproverID,
		Perspective: req.Perspective, Priority: req.Priority, Deadline: req.Deadline,
		BudgetTokens: req.BudgetTokens, BudgetCost: req.BudgetCost, Acceptance: req.Acceptance,
		DependsOn: req.DependsOn, SourceMessageID: req.SourceMessageID, ParentID: req.ParentID, HighRisk: req.HighRisk,
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	view, err := svc.TaskView(ctx, task.ID)
	if err != nil {
		writeOK(w, task)
		return
	}
	writeOK(w, view)
}

type transitionTaskRequest struct {
	ActorAgentID string `json:"actor_agent_id"`
	To           string `json:"to"`
	Reason       string `json:"reason"`
	Evidence     string `json:"evidence"`
}

func (s *Server) handleTransitionTask(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req transitionTaskRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	task, err := svc.TransitionTask(ctx, actor, r.PathValue("id"), req.To, req.Reason, req.Evidence)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, task)
}

type criterionEvidenceRequest struct {
	ActorAgentID string `json:"actor_agent_id"`
	Evidence     string `json:"evidence"`
}

func (s *Server) handleCriterionEvidence(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req criterionEvidenceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if err := svc.SetCriterionEvidence(ctx, actor, r.PathValue("id"), r.PathValue("cid"), req.Evidence); err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

type criterionVerdictRequest struct {
	ActorAgentID string `json:"actor_agent_id"`
	Verdict      string `json:"verdict"`
}

func (s *Server) handleCriterionVerdict(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req criterionVerdictRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if err := svc.SetCriterionVerdict(ctx, actor, r.PathValue("id"), r.PathValue("cid"), req.Verdict); err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

type g3Request struct {
	ActorAgentID  string `json:"actor_agent_id"`
	Risk          string `json:"risk"`
	ResidualRisks string `json:"residual_risks"`
}

func (s *Server) handleSubmitG3(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req g3Request
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	approval, err := svc.SubmitG3(ctx, actor, r.PathValue("id"), req.Risk, req.ResidualRisks)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"approval": approval, "status": approval.Status})
}

type createArtifactRequest struct {
	ActorAgentID string `json:"actor_agent_id"`
	TaskID       string `json:"task_id"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	Content      string `json:"content"`
	DepartmentID string `json:"department_id"`
}

func (s *Server) handleCreateArtifact(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req createArtifactRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	artifact, version, err := svc.CreateArtifact(ctx, actor, core.ArtifactInput{
		TaskID: req.TaskID, Type: req.Type, Title: req.Title, Content: req.Content, DepartmentID: req.DepartmentID,
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"artifact": artifact, "version": version})
}

type artifactVersionRequest struct {
	ActorAgentID string `json:"actor_agent_id"`
	Content      string `json:"content"`
	Supersedes   string `json:"supersedes"`
}

func (s *Server) handleAddArtifactVersion(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req artifactVersionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	version, err := svc.AddArtifactVersion(ctx, actor, r.PathValue("id"), req.Content, req.Supersedes)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, version)
}

type createReviewRequest struct {
	ActorAgentID       string `json:"actor_agent_id"`
	ArtifactID         string `json:"artifact_id"`
	ArtifactVersionID  string `json:"artifact_version_id"`
	FromDepartment     string `json:"from_department"`
	ToDepartment       string `json:"to_department"`
	Severity           string `json:"severity"`
	Stance             string `json:"stance"`
	Claim              string `json:"claim"`
	Evidence           string `json:"evidence"`
	Impact             string `json:"impact"`
	ProposedResolution string `json:"proposed_resolution"`
	DueAt              string `json:"due_at"`
}

func (s *Server) handleCreateReview(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req createReviewRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	review, err := svc.CreateReview(ctx, actor, core.ReviewInput{
		ArtifactID: req.ArtifactID, ArtifactVersionID: req.ArtifactVersionID,
		FromDepartment: req.FromDepartment, ToDepartment: req.ToDepartment,
		Severity: req.Severity, Stance: req.Stance, Claim: req.Claim, Evidence: req.Evidence,
		Impact: req.Impact, ProposedResolution: req.ProposedResolution, DueAt: req.DueAt,
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, review)
}

type verdictRequest struct {
	ActorAgentID string `json:"actor_agent_id"`
	Verdict      string `json:"verdict"`
	Checked      string `json:"checked"`
	Rationale    string `json:"rationale"`
	Conditions   string `json:"conditions"`
	ResidualRisk string `json:"residual_risk"`
}

func (s *Server) handleSubmitVerdict(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req verdictRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	verdict, err := svc.SubmitVerdict(ctx, actor, r.PathValue("id"), req.Verdict, req.Checked, req.Rationale, req.Conditions, req.ResidualRisk)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, verdict)
}

type reworkRequest struct {
	ActorAgentID string `json:"actor_agent_id"`
	Content      string `json:"content"`
}

func (s *Server) handleRework(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req reworkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	version, err := svc.Rework(ctx, actor, r.PathValue("id"), req.Content)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, version)
}

type createDecisionRequest struct {
	ActorAgentID      string `json:"actor_agent_id"`
	Title             string `json:"title"`
	Context           string `json:"context"`
	Options           string `json:"options"`
	Decision          string `json:"decision"`
	Rationale         string `json:"rationale"`
	Evidence          string `json:"evidence"`
	Dissent           string `json:"dissent"`
	Deciders          string `json:"deciders"`
	Consulted         string `json:"consulted"`
	Informed          string `json:"informed"`
	AffectedTasks     string `json:"affected_tasks"`
	AffectedArtifacts string `json:"affected_artifacts"`
	RevisitTrigger    string `json:"revisit_trigger"`
	Supersedes        string `json:"supersedes"`
}

func (s *Server) handleCreateDecision(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req createDecisionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	decision, err := svc.CreateDecision(ctx, actor, domain.Decision{
		Title: req.Title, Context: req.Context, Options: req.Options, Decision: req.Decision,
		Rationale: req.Rationale, Evidence: req.Evidence, Dissent: req.Dissent,
		Deciders: req.Deciders, Consulted: req.Consulted, Informed: req.Informed,
		AffectedTasks: req.AffectedTasks, AffectedArtifacts: req.AffectedArtifacts,
		RevisitTrigger: req.RevisitTrigger, Supersedes: req.Supersedes,
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, decision)
}

type supersedeDecisionRequest struct {
	ActorAgentID string `json:"actor_agent_id"`
	NewID        string `json:"new_id"`
}

func (s *Server) handleSupersedeDecision(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireWork(w)
	if !ok {
		return
	}
	var req supersedeDecisionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if strings.TrimSpace(req.NewID) == "" {
		s.writeErr(w, errInvalid("new_id 必填"))
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if err := svc.SupersedeDecision(ctx, actor, r.PathValue("id"), req.NewID); err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"ok": true})
}

type approvalDecisionRequest struct {
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	SecondConfirm bool   `json:"second_confirm"`
}

// handleDecideApproval：人类会话+CSRF 或 被授权 Agent Token；
// irreversible 审批需要双钥匙（second_confirm=true，两次显式批准 + audit）。
func (s *Server) handleDecideApproval(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req approvalDecisionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	decision := strings.TrimSpace(req.Decision)
	if decision != domain.ApprovalApproved && decision != domain.ApprovalRejected {
		s.writeErr(w, errInvalid("decision 必须是 approved/rejected"))
		return
	}
	if decision == domain.ApprovalRejected && strings.TrimSpace(req.Reason) == "" {
		s.writeErr(w, errInvalid("拒绝审批必须填写理由"))
		return
	}
	approval, err := s.store.GetApproval(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	// 主体：人类 owner 或被授权的 Agent。
	decidedBy := ""
	actorType := domain.SenderUser
	owner, err := s.currentOwner(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if owner != nil {
		if err := s.verifyCSRF(r, owner); err != nil {
			s.writeErr(w, err)
			return
		}
		decidedBy = owner.ID
	} else {
		agent, err := s.agentFromToken(ctx, r)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		if agent == nil {
			s.writeErr(w, errUnauthorized("请先登录"))
			return
		}
		if s.delegatedAgent(ctx, approval.ID, agent.ID) == nil {
			s.writeErr(w, errForbidden("该 Agent 没有此审批的有效授权"))
			return
		}
		decidedBy = agent.ID
		actorType = domain.SenderAgent
	}
	// 双钥匙：不可逆审批需要两次显式 confirm（v0.0.1 单用户用 second_confirm 代替第二人类）。
	if approval.Kind == domain.ApprovalIrreversible {
		if !req.SecondConfirm {
			s.audit(ctx, actorType, decidedBy, "approval.dual_key_required", "approval", approval.ID, "denied",
				jsonDetail(map[string]any{"decision": decision}))
			s.writeErr(w, domain.NewError("dual_key_required", "不可逆动作需要双钥匙：请再次确认（second_confirm=true）", http.StatusConflict))
			return
		}
		s.audit(ctx, actorType, decidedBy, "approval.dual_key_confirm", "approval", approval.ID, "ok",
			jsonDetail(map[string]any{"decision": decision}))
	}
	var out domain.Approval
	if approval.Kind == domain.ApprovalG3 {
		svc, ok := s.requireWork(w)
		if !ok {
			return
		}
		out, err = svc.DecideG3(ctx, approval.ID, decision, decidedBy, req.Reason)
	} else {
		out, err = s.store.DecideApproval(ctx, approval.ID, decision, decidedBy, req.Reason)
		if err == nil {
			_, _ = s.store.AppendEvent(ctx, domain.Event{
				Type: "approval.updated", ObjectType: "approval", ObjectID: out.ID,
				ActorType: actorType, ActorID: decidedBy,
				Payload: jsonDetail(map[string]any{"kind": out.Kind, "status": out.Status}),
			})
			s.audit(ctx, actorType, decidedBy, "approval.decide", "approval", out.ID, "ok",
				jsonDetail(map[string]any{"kind": out.Kind, "decision": decision, "reason": req.Reason,
					"delegated": actorType == domain.SenderAgent}))
		}
	}
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, out)
}

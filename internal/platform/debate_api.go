package platform

import (
	"context"
	"net/http"
	"strings"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// DebateService 是 platform 对 core.DebateService 的窄接口（R6：讨论/辩论/决策）。
// *core.DebateService 结构化满足；未注入时相关写路由 503。
type DebateService interface {
	PostDiscussion(ctx context.Context, actor domain.Agent, conversationID, debateID, perspective, stance, evidence string) (domain.Message, error)
	SubmitStance(ctx context.Context, actor domain.Agent, conversationID, debateID string, round int, stance string, evidenceIDs []string) (domain.Message, error)
	Escalate(ctx context.Context, actor domain.Agent, conversationID, debateID, taskID, level, issue, evidence, options, recommendation, consequence string) (domain.Decision, error)
	CloseDebate(ctx context.Context, actor domain.Agent, debateID, title, decisionText, rationale, dissent, affectedTasks, affectedArtifacts, supersedes, revisitTrigger string) (domain.Decision, error)
	Reopen(ctx context.Context, actor domain.Agent, decisionID, newEvidence string) (domain.Decision, error)
	GetDecision(ctx context.Context, id string) (domain.Decision, error)
	ListDecisions(ctx context.Context, status string) ([]domain.Decision, error)
	ListDecisionsByTask(ctx context.Context, taskID string) ([]domain.Decision, error)
	SearchDecisions(ctx context.Context, query string) ([]domain.Decision, error)
	AppendDissent(ctx context.Context, actor domain.Agent, decisionID, dissent string) (domain.Decision, error)
	DecisionDetail(ctx context.Context, id string) (core.DecisionView, error)
}

func (s *Server) SetDebateService(svc DebateService) {
	s.debateMu.Lock()
	defer s.debateMu.Unlock()
	s.debate = svc
}

func (s *Server) debateService() DebateService {
	s.debateMu.RLock()
	defer s.debateMu.RUnlock()
	return s.debate
}

func (s *Server) requireDebate(w http.ResponseWriter) (DebateService, bool) {
	svc := s.debateService()
	if svc == nil {
		s.writeErr(w, domain.NewError(domain.CodeUnavailable, "R6 DebateService 未接入（core DebateService 未注入）", http.StatusServiceUnavailable))
		return nil, false
	}
	return svc, true
}

type discussionRequest struct {
	ActorAgentID string `json:"actor_agent_id"`
	DebateID     string `json:"debate_id"`
	Perspective  string `json:"perspective"`
	Stance       string `json:"stance"`
	Evidence     string `json:"evidence"`
}

func (s *Server) handlePostDiscussion(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireDebate(w)
	if !ok {
		return
	}
	var req discussionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	msg, err := svc.PostDiscussion(ctx, actor, r.PathValue("id"), req.DebateID, req.Perspective, req.Stance, req.Evidence)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, msg)
}

type stanceRequest struct {
	ActorAgentID string   `json:"actor_agent_id"`
	DebateID     string   `json:"debate_id"`
	Round        int      `json:"round"`
	Stance       string   `json:"stance"`
	EvidenceIDs  []string `json:"evidence_ids"`
}

func (s *Server) handleSubmitStance(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireDebate(w)
	if !ok {
		return
	}
	var req stanceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	msg, err := svc.SubmitStance(ctx, actor, r.PathValue("id"), req.DebateID, req.Round, req.Stance, req.EvidenceIDs)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, msg)
}

type escalateRequest struct {
	ActorAgentID   string `json:"actor_agent_id"`
	ConversationID string `json:"conversation_id"`
	TaskID         string `json:"task_id"`
	Level          string `json:"level"`
	Issue          string `json:"issue"`
	Evidence       string `json:"evidence"`
	Options        string `json:"options"`
	Recommendation string `json:"recommendation"`
	Consequence    string `json:"consequence"`
}

func (s *Server) handleEscalateDebate(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireDebate(w)
	if !ok {
		return
	}
	var req escalateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	decision, err := svc.Escalate(ctx, actor, req.ConversationID, r.PathValue("id"), req.TaskID, req.Level,
		req.Issue, req.Evidence, req.Options, req.Recommendation, req.Consequence)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, decision)
}

type closeDebateRequest struct {
	ActorAgentID      string `json:"actor_agent_id"`
	Title             string `json:"title"`
	Decision          string `json:"decision"`
	Rationale         string `json:"rationale"`
	Dissent           string `json:"dissent"`
	AffectedTasks     string `json:"affected_tasks"`
	AffectedArtifacts string `json:"affected_artifacts"`
	Supersedes        string `json:"supersedes"`
	RevisitTrigger    string `json:"revisit_trigger"`
}

func (s *Server) handleCloseDebate(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireDebate(w)
	if !ok {
		return
	}
	var req closeDebateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	decision, err := svc.CloseDebate(ctx, actor, r.PathValue("id"), req.Title, req.Decision, req.Rationale,
		req.Dissent, req.AffectedTasks, req.AffectedArtifacts, req.Supersedes, req.RevisitTrigger)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, decision)
}

type reopenRequest struct {
	ActorAgentID string `json:"actor_agent_id"`
	NewEvidence  string `json:"new_evidence"`
}

func (s *Server) handleReopenDebate(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireDebate(w)
	if !ok {
		return
	}
	var req reopenRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	decision, err := svc.Reopen(ctx, actor, r.PathValue("id"), req.NewEvidence)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, decision)
}

func (s *Server) handleGetDecision(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	svc := s.debateService()
	if svc != nil {
		d, err := svc.GetDecision(r.Context(), r.PathValue("id"))
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeOK(w, d)
		return
	}
	d, err := s.store.GetDecision(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, d)
}

func (s *Server) handleDecisionDetail(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	svc, ok := s.requireDebate(w)
	if !ok {
		return
	}
	view, err := svc.DecisionDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, view)
}

type dissentRequest struct {
	ActorAgentID string `json:"actor_agent_id"`
	Dissent      string `json:"dissent"`
}

func (s *Server) handleDecisionDissent(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.requireDebate(w)
	if !ok {
		return
	}
	var req dissentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if strings.TrimSpace(req.Dissent) == "" {
		s.writeErr(w, errInvalid("dissent 不能为空"))
		return
	}
	actor, err := s.workActor(ctx, req.ActorAgentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	d, err := svc.AppendDissent(ctx, actor, r.PathValue("id"), req.Dissent)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, d)
}

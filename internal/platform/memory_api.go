package platform

import (
	"context"
	"net/http"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/memory"
	"github.com/lignting/lignting-agents/internal/store"
)

// SetMemoryService 注入 R7 记忆服务（cmd 与 core 共用同一 memory.Service 实例）。
// 未注入时 /api/memories 等返回 503。
func (s *Server) SetMemoryService(svc *memory.Service) {
	s.memMu.Lock()
	defer s.memMu.Unlock()
	s.mem = svc
}

func (s *Server) memoryService(w http.ResponseWriter) (*memory.Service, bool) {
	s.memMu.RLock()
	svc := s.mem
	s.memMu.RUnlock()
	if svc == nil {
		s.writeErr(w, domain.NewError(domain.CodeUnavailable, "记忆服务未接入（memory.Service 未注入）", http.StatusServiceUnavailable))
		return nil, false
	}
	return svc, true
}

// agentFromToken 解析 X-Lignting-Agent-Token；无 token 返回 (nil,nil)。
func (s *Server) agentFromToken(ctx context.Context, r *http.Request) (*domain.Agent, error) {
	token := strings.TrimSpace(r.Header.Get("X-Lignting-Agent-Token"))
	if token == "" {
		return nil, nil
	}
	agent, err := s.store.FindAgentByTokenHash(ctx, store.HashToken(token))
	if err != nil {
		return nil, errUnauthorized("Agent Token 无效")
	}
	return &agent, nil
}

// memoryAccess 计算记忆访问者：owner 会话=人类；Agent Token=对应 Agent。
func (s *Server) memoryAccess(ctx context.Context, r *http.Request, owner *store.Owner) (memory.Access, error) {
	agent, err := s.agentFromToken(ctx, r)
	if err != nil {
		return memory.Access{}, err
	}
	if agent == nil {
		return memory.HumanAccess(owner.ID), nil
	}
	orgWide := agent.Kind == domain.AgentKindAssistant
	actorType := memory.ActorEmployee
	switch agent.Kind {
	case domain.AgentKindAssistant:
		actorType = memory.ActorAssistant
	case domain.AgentKindSupervisor:
		actorType = memory.ActorSupervisor
	}
	return memory.AgentAccess(actorType, agent.ID, agent.DepartmentID, orgWide), nil
}

func splitCSV(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.memoryService(w)
	if !ok {
		return
	}
	ctx := r.Context()
	access, err := s.memoryAccess(ctx, r, owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	q := r.URL.Query()
	limit, err := parseInt64Param(q.Get("limit"), 100)
	if err != nil || limit <= 0 || limit > 500 {
		s.writeErr(w, errInvalid("limit 必须在 1..500"))
		return
	}
	f := memory.Filter{
		Scope: q.Get("scope"), DepartmentID: q.Get("department_id"),
		Status: splitCSV(q.Get("status")), Limit: int(limit),
	}
	items, err := svc.List(ctx, f, access)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if keyword := strings.TrimSpace(q.Get("q")); keyword != "" {
		filtered := items[:0]
		for _, m := range items {
			if strings.Contains(m.Content, keyword) || strings.Contains(m.Summary, keyword) || strings.Contains(m.Tags, keyword) {
				filtered = append(filtered, m)
			}
		}
		items = filtered
	}
	writeList(w, items, int64(len(items)), "")
}

type candidateRequest struct {
	Direct           bool              `json:"direct"`
	Scope            string            `json:"scope"`
	DepartmentID     string            `json:"department_id"`
	AgentID          string            `json:"agent_id"`
	EntityType       string            `json:"entity_type"`
	EntityKey        string            `json:"entity_key"`
	Content          string            `json:"content"`
	Summary          string            `json:"summary"`
	Tags             []string          `json:"tags"`
	Confidence       string            `json:"confidence"`
	Evidence         []memory.Evidence `json:"evidence"`
	Sensitivity      string            `json:"sensitivity"`
	Visibility       string            `json:"visibility"`
	ValidFrom        string            `json:"valid_from"`
	Supersedes       string            `json:"supersedes"`
	Reusable         bool              `json:"reusable"`
	DecisionRelevant bool              `json:"decision_relevant"`
	SourceTaskID     string            `json:"source_task_id"`
	SourceEvent      string            `json:"source_event"`
}

func candidateFromRequest(req candidateRequest, actor memory.Access) memory.Candidate {
	return memory.Candidate{
		Scope: req.Scope, DepartmentID: req.DepartmentID, AgentID: req.AgentID,
		EntityType: req.EntityType, EntityKey: req.EntityKey, Content: req.Content, Summary: req.Summary,
		Tags: req.Tags, Confidence: req.Confidence, Evidence: req.Evidence, Sensitivity: req.Sensitivity,
		Visibility: req.Visibility, ValidFrom: req.ValidFrom, Supersedes: req.Supersedes,
		Reusable: req.Reusable, DecisionRelevant: req.DecisionRelevant,
		SourceTaskID: req.SourceTaskID, SourceEvent: req.SourceEvent, CreatedBy: actor.ID,
	}
}

func (s *Server) handleCreateMemory(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.memoryService(w)
	if !ok {
		return
	}
	ctx := r.Context()
	access, err := s.memoryAccess(ctx, r, owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var req candidateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	visibility, err := normalizeMemoryVisibility(req.Visibility)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	entityType, err := normalizeMemoryEntityType(req.EntityType)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	candidate := candidateFromRequest(req, access)
	candidate.Visibility = visibility
	candidate.EntityType = entityType
	if req.Direct {
		if !memory.CanWriteArchive(access) {
			s.writeErr(w, errForbidden("只有人类/助理/部门主管可以直接写档案；员工请提交候选"))
			return
		}
		m, err := svc.CreateDirect(ctx, candidate, access)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		s.audit(ctx, access.Type, access.ID, "memory.create", "memory", m.ID, "ok", jsonDetail(map[string]any{"scope": m.Scope}))
		writeOK(w, m)
		return
	}
	result, err := svc.SubmitCandidate(ctx, candidate, access)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, access.Type, access.ID, "memory.candidate", "memory", result.Memory.ID, "ok", jsonDetail(map[string]any{"duplicate": result.Duplicate, "conflict": result.Conflict}))
	writeOK(w, result)
}

type memoryPatchRequest struct {
	Summary         *string  `json:"summary"`
	Content         *string  `json:"content"`
	Tags            []string `json:"tags"`
	Visibility      *string  `json:"visibility"`
	Confidence      *string  `json:"confidence"`
	ExpectedVersion int      `json:"expected_version"`
}

func (s *Server) handlePatchMemory(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.memoryService(w)
	if !ok {
		return
	}
	ctx := r.Context()
	access, err := s.memoryAccess(ctx, r, owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var req memoryPatchRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	patch := memory.Patch{ExpectedVersion: req.ExpectedVersion, Tags: req.Tags}
	if req.Summary != nil {
		patch.Summary = *req.Summary
	}
	if req.Content != nil {
		patch.Content = *req.Content
	}
	if req.Visibility != nil {
		v, err := normalizeMemoryVisibility(*req.Visibility)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		patch.Visibility = v
	}
	if req.Confidence != nil {
		patch.Confidence = *req.Confidence
	}
	m, err := svc.Update(ctx, r.PathValue("id"), patch, access)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, access.Type, access.ID, "memory.update", "memory", m.ID, "ok", jsonDetail(map[string]any{"version": m.Version}))
	writeOK(w, m)
}

type memoryDeleteRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.memoryService(w)
	if !ok {
		return
	}
	ctx := r.Context()
	access, err := s.memoryAccess(ctx, r, owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var req memoryDeleteRequest
	_ = decodeJSON(w, r, &req)
	id := r.PathValue("id")
	if err := svc.Delete(ctx, id, req.Reason, access); err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, access.Type, access.ID, "memory.delete", "memory", id, "ok", jsonDetail(map[string]any{"reason": req.Reason}))
	writeOK(w, map[string]any{"deleted": true})
}

func (s *Server) handleApproveMemory(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.memoryService(w)
	if !ok {
		return
	}
	ctx := r.Context()
	access, err := s.memoryAccess(ctx, r, owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	result, err := svc.Approve(ctx, r.PathValue("id"), access)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, access.Type, access.ID, "memory.approve", "memory", result.Memory.ID, "ok", jsonDetail(map[string]any{"superseded": result.SupersededID}))
	writeOK(w, result)
}

func (s *Server) handleRejectMemory(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.memoryService(w)
	if !ok {
		return
	}
	ctx := r.Context()
	access, err := s.memoryAccess(ctx, r, owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var req memoryDeleteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		s.writeErr(w, errInvalid("拒绝记忆必须填写理由"))
		return
	}
	m, err := svc.Reject(ctx, r.PathValue("id"), req.Reason, access)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, access.Type, access.ID, "memory.reject", "memory", m.ID, "ok", jsonDetail(map[string]any{"reason": req.Reason}))
	writeOK(w, m)
}

// ---- 手册 ----

func manualKeyFromQuery(r *http.Request) memory.ManualKey {
	q := r.URL.Query()
	return memory.ManualKey{Scope: q.Get("scope"), DepartmentID: q.Get("department_id"), AgentID: q.Get("agent_id")}
}

func (s *Server) handleListManuals(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.memoryService(w)
	if !ok {
		return
	}
	items, err := svc.ListManualVersions(r.Context(), manualKeyFromQuery(r))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

type manualProposeRequest struct {
	Scope        string   `json:"scope"`
	DepartmentID string   `json:"department_id"`
	AgentID      string   `json:"agent_id"`
	Content      string   `json:"content"`
	Sources      []string `json:"sources"`
}

func (s *Server) handleProposeManual(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.memoryService(w)
	if !ok {
		return
	}
	ctx := r.Context()
	access, err := s.memoryAccess(ctx, r, owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var req manualProposeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	key := memory.ManualKey{Scope: req.Scope, DepartmentID: req.DepartmentID, AgentID: req.AgentID}
	m, err := svc.ProposeManual(ctx, key, req.Content, req.Sources, access)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, access.Type, access.ID, "manual.propose", "manual", m.ID, "ok", jsonDetail(map[string]any{"scope": m.Scope, "version": m.Version}))
	writeOK(w, m)
}

type manualRollbackRequest struct {
	ToVersion int `json:"to_version"`
}

func (s *Server) handleRollbackManual(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	svc, ok := s.memoryService(w)
	if !ok {
		return
	}
	ctx := r.Context()
	access, err := s.memoryAccess(ctx, r, owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	rule, err := s.store.GetManual(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var req manualRollbackRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if req.ToVersion <= 0 {
		s.writeErr(w, errInvalid("to_version 必填"))
		return
	}
	key := memory.ManualKey{Scope: rule.Scope, DepartmentID: rule.DepartmentID, AgentID: rule.AgentID}
	m, err := svc.RollbackManual(ctx, key, req.ToVersion, access)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, access.Type, access.ID, "manual.rollback", "manual", m.ID, "ok", jsonDetail(map[string]any{"to_version": req.ToVersion}))
	writeOK(w, m)
}

// ---- context snapshots / turn 回放 ----

func (s *Server) handleListContextSnapshots(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	q := r.URL.Query()
	limit, err := parseInt64Param(q.Get("limit"), 100)
	if err != nil || limit <= 0 || limit > 200 {
		s.writeErr(w, errInvalid("limit 必须在 1..200"))
		return
	}
	items, err := s.store.ListContextSnapshots(r.Context(), q.Get("turn_id"), q.Get("conversation_id"), int(limit))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

func (s *Server) handleGetTurn(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	ctx := r.Context()
	id := r.PathValue("id")
	turn, err := s.store.GetTurn(ctx, id)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	snaps, err := s.store.ListContextSnapshots(ctx, id, "", 100)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"turn": turn, "snapshots": snaps})
}

var _ = context.Background

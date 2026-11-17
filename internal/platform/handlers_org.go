package platform

import (
	"net/http"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// handleOrg 返回组织图只读投影：owner / assistant / departments / agents。
func (s *Server) handleOrg(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	agents, err := s.store.ListAgents(ctx, store.AgentFilter{})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	departments, err := s.store.ListDepartments(ctx, store.DepartmentFilter{})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var assistant *domain.Agent
	others := make([]domain.Agent, 0, len(agents))
	for i := range agents {
		if agents[i].Kind == domain.AgentKindAssistant && assistant == nil {
			assistant = &agents[i]
			continue
		}
		others = append(others, agents[i])
	}
	writeOK(w, map[string]any{
		"owner":       toOwnerDTO(owner),
		"assistant":   assistant,
		"departments": departments,
		"agents":      others,
	})
}

func (s *Server) handleListDepartments(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	q := r.URL.Query()
	departments, err := s.store.ListDepartments(r.Context(), store.DepartmentFilter{
		Status: q.Get("status"),
		Kind:   q.Get("kind"),
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, departments, int64(len(departments)), "")
}

func (s *Server) handleGetDepartment(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	ctx := r.Context()
	id := r.PathValue("id")
	dept, err := s.store.GetDepartment(ctx, id)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	agents, err := s.store.ListAgentsByDepartment(ctx, id)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"department": dept, "agents": agents})
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	q := r.URL.Query()
	agents, err := s.store.ListAgents(r.Context(), store.AgentFilter{
		DepartmentID: q.Get("department_id"),
		Kind:         q.Get("kind"),
		Status:       q.Get("status"),
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, agents, int64(len(agents)), "")
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	agent, err := s.store.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, agent)
}

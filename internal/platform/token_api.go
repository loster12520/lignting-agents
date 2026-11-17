package platform

import (
	"net/http"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// R8 Agent Token 签发/轮换/撤销（AC-08 / FR-SEC-02）。
// 明文只在响应里出现一次；DB 只存 sha256；审计/日志/检索永不含明文。

func (s *Server) issueAgentToken(w http.ResponseWriter, r *http.Request, owner *store.Owner, action string) {
	ctx := r.Context()
	agent, err := s.store.GetAgent(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	plain, hash, err := store.NewToken()
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if err := s.store.SetAgentTokenHash(ctx, agent.ID, hash); err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, action, "agent", agent.ID, "ok",
		jsonDetail(map[string]any{"agent_id": agent.ID, "rotated": action == "agent.token_rotate"}))
	writeOK(w, map[string]any{
		"agent_id": agent.ID,
		"token":    plain,
		"note":     "token 只显示一次；请立即保存。轮换后旧 token 立刻失效。",
	})
}

func (s *Server) handleIssueAgentToken(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	s.issueAgentToken(w, r, owner, "agent.token_issue")
}

func (s *Server) handleRotateAgentToken(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	s.issueAgentToken(w, r, owner, "agent.token_rotate")
}

func (s *Server) handleRevokeAgentToken(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	agent, err := s.store.GetAgent(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if err := s.store.SetAgentTokenHash(ctx, agent.ID, ""); err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "agent.token_revoke", "agent", agent.ID, "ok",
		jsonDetail(map[string]any{"agent_id": agent.ID}))
	writeOK(w, map[string]any{"agent_id": agent.ID, "revoked": true})
}

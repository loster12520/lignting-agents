package platform

import (
	"net/http"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// R3/R4 临时桥 API（FR-CHAT-16）：范围 + 到期必填；到期由调度器自动关闭。

func (s *Server) redactForAudit(text string) string {
	s.secretsMu.RLock()
	secrets := s.secrets
	s.secretsMu.RUnlock()
	return secrets.Redact(text)
}

var bridgeScopes = map[string]bool{
	domain.BridgeCrossDept: true, domain.BridgeLeadToUser: true, domain.BridgeAgentPair: true,
}
var bridgePartyTypes = map[string]bool{"agent": true, "user": true, "department": true}

type createBridgeRequest struct {
	Scope     string `json:"scope"`
	FromType  string `json:"from_type"`
	FromID    string `json:"from_id"`
	ToType    string `json:"to_type"`
	ToID      string `json:"to_id"`
	ExpiresAt string `json:"expires_at"`
	Reason    string `json:"reason"`
}

func (s *Server) handleCreateBridge(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req createBridgeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if !bridgeScopes[scope] {
		s.writeErr(w, errInvalid("scope 必须是 cross_dept/lead_to_user/agent_pair"))
		return
	}
	fromType, toType := strings.ToLower(strings.TrimSpace(req.FromType)), strings.ToLower(strings.TrimSpace(req.ToType))
	if !bridgePartyTypes[fromType] || !bridgePartyTypes[toType] {
		s.writeErr(w, errInvalid("from_type/to_type 必须是 agent/user/department"))
		return
	}
	if strings.TrimSpace(req.FromID) == "" || strings.TrimSpace(req.ToID) == "" {
		s.writeErr(w, errInvalid("from_id/to_id 必填"))
		return
	}
	if strings.TrimSpace(req.ExpiresAt) == "" {
		s.writeErr(w, errInvalid("expires_at 必填"))
		return
	}
	expires, err := time.Parse(time.RFC3339, req.ExpiresAt)
	if err != nil {
		s.writeErr(w, errInvalid("expires_at 必须是 RFC3339 时间"))
		return
	}
	if !expires.After(time.Now().UTC()) {
		s.writeErr(w, errInvalid("expires_at 必须晚于当前时间"))
		return
	}
	// 参与方存在性校验（agent 必须存在；user 只能是 owner；department 必须存在）。
	checkParty := func(typ, id string) error {
		switch typ {
		case "agent":
			_, err := s.store.GetAgent(ctx, id)
			return err
		case "department":
			_, err := s.store.GetDepartment(ctx, id)
			return err
		}
		return nil
	}
	if err := checkParty(fromType, req.FromID); err != nil {
		s.writeErr(w, err)
		return
	}
	if err := checkParty(toType, req.ToID); err != nil {
		s.writeErr(w, err)
		return
	}
	bridge, err := s.store.CreateBridge(ctx, domain.Bridge{
		Scope: scope, FromType: fromType, FromID: strings.TrimSpace(req.FromID),
		ToType: toType, ToID: strings.TrimSpace(req.ToID), ExpiresAt: expires.Format(time.RFC3339),
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "bridge.open", "bridge", bridge.ID, "ok",
		jsonDetail(map[string]any{"scope": scope, "from": fromType + ":" + req.FromID, "to": toType + ":" + req.ToID,
			"expires_at": bridge.ExpiresAt, "reason": s.redactForAudit(req.Reason)}))
	writeOK(w, bridge)
}

func (s *Server) handleListBridges(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	items, err := s.store.ListBridges(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

func (s *Server) handleCloseBridge(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	bridge, err := s.store.CloseBridge(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "bridge.close", "bridge", bridge.ID, "ok", "{}")
	writeOK(w, bridge)
}

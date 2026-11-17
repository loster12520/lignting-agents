package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// AC-13：批量审批 / 稍后 / 授权 / 双钥匙。不新增 schema：
// 稍后与授权存 settings + schedules（到期恢复/回收），双钥匙用两次显式审批 + audit。

const (
	snoozeApprovalPrefix   = "snooze.approval."
	delegateApprovalPrefix = "delegate.approval."
)

type batchApprovalRequest struct {
	IDs      []string `json:"ids"`
	Decision string   `json:"decision"`
	Reason   string   `json:"reason"`
}

// handleBatchDecideApprovals 只允许低风险 + once/session；irreversible/dual 整批拒绝（不部分执行）。
func (s *Server) handleBatchDecideApprovals(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req batchApprovalRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if len(req.IDs) == 0 {
		s.writeErr(w, errInvalid("ids 不能为空"))
		return
	}
	decision := strings.TrimSpace(req.Decision)
	if decision != domain.ApprovalApproved && decision != domain.ApprovalRejected {
		s.writeErr(w, errInvalid("decision 必须是 approved/rejected"))
		return
	}
	if decision == domain.ApprovalRejected && strings.TrimSpace(req.Reason) == "" {
		s.writeErr(w, errInvalid("拒绝必须填写理由"))
		return
	}
	for _, id := range req.IDs {
		a, err := s.store.GetApproval(ctx, id)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		if a.Status != domain.ApprovalPending {
			s.writeErr(w, errConflict("审批已处理："+a.ID))
			return
		}
		if a.Kind == domain.ApprovalIrreversible || a.Scope == "dual" || a.Risk == "high" {
			s.writeErr(w, errForbidden("高风险/不可逆/双钥匙审批不能批量处理："+a.ID))
			return
		}
		if a.Scope != "once" && a.Scope != "session" {
			s.writeErr(w, errForbidden("仅支持一次/会话范围审批批量处理："+a.ID))
			return
		}
	}
	done, err := s.store.DecideApprovalsBatch(ctx, req.IDs, decision, owner.ID, req.Reason)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	for _, a := range done {
		_, _ = s.store.AppendEvent(ctx, domain.Event{
			Type: "approval.updated", ObjectType: "approval", ObjectID: a.ID,
			ActorType: domain.SenderUser, ActorID: owner.ID,
			Payload: jsonDetail(map[string]any{"kind": a.Kind, "status": a.Status, "batch": true}),
		})
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "approval.batch_decide", "approval", "", "ok",
		jsonDetail(map[string]any{"ids": req.IDs, "decision": decision, "count": len(done)}))
	writeOK(w, map[string]any{"items": done, "count": len(done), "decision": decision})
}

type snoozeRequest struct {
	Until string `json:"until"`
}

// handleSnoozeApproval 稍后处理：status 仍 pending，但记 snoozed_until + 到期恢复调度。
func (s *Server) handleSnoozeApproval(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req snoozeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	until, err := time.Parse(time.RFC3339, strings.TrimSpace(req.Until))
	if err != nil || !until.After(time.Now().UTC()) {
		s.writeErr(w, errInvalid("until 必须是未来的 RFC3339 时间"))
		return
	}
	approval, err := s.store.GetApproval(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if approval.Status != domain.ApprovalPending {
		s.writeErr(w, errConflict("只有 pending 审批可以稍后处理"))
		return
	}
	key := snoozeApprovalPrefix + approval.ID
	if err := s.store.SetSetting(ctx, key, until.UTC().Format(time.RFC3339)); err != nil {
		s.writeErr(w, err)
		return
	}
	if _, err := s.store.InsertSchedule(ctx, store.Schedule{Kind: "snooze.expire", DueAt: until.UTC().Format(store.Now()[:0] + "2006-01-02T15:04:05.000000000Z07:00"),
		Payload: jsonDetail(map[string]any{"setting_key": key, "object_type": "approval", "object_id": approval.ID})}); err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "approval.snooze", "approval", approval.ID, "ok",
		jsonDetail(map[string]any{"until": until.UTC().Format(time.RFC3339)}))
	writeOK(w, map[string]any{"id": approval.ID, "snoozed_until": until.UTC().Format(time.RFC3339)})
}

// handleSnoozeNotification 通知稍后处理。
func (s *Server) handleSnoozeNotification(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req snoozeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	until, err := time.Parse(time.RFC3339, strings.TrimSpace(req.Until))
	if err != nil || !until.After(time.Now().UTC()) {
		s.writeErr(w, errInvalid("until 必须是未来的 RFC3339 时间"))
		return
	}
	n, err := s.store.SetNotificationStatus(ctx, r.PathValue("id"), "snoozed")
	if err != nil {
		s.writeErr(w, err)
		return
	}
	key := "snooze.notification." + n.ID
	if err := s.store.SetSetting(ctx, key, until.UTC().Format(time.RFC3339)); err != nil {
		s.writeErr(w, err)
		return
	}
	if _, err := s.store.InsertSchedule(ctx, store.Schedule{Kind: "snooze.expire", DueAt: until.UTC().Format(store.Now()[:0] + "2006-01-02T15:04:05.000000000Z07:00"),
		Payload: jsonDetail(map[string]any{"setting_key": key, "object_type": "notification", "object_id": n.ID})}); err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "notification.snooze", "notification", n.ID, "ok",
		jsonDetail(map[string]any{"until": until.UTC().Format(time.RFC3339)}))
	writeOK(w, map[string]any{"id": n.ID, "status": n.Status, "snoozed_until": until.UTC().Format(time.RFC3339)})
}

type delegateRequest struct {
	ToAgentID string `json:"to_agent_id"`
	Scope     string `json:"scope"`
	ExpiresAt string `json:"expires_at"`
}

// handleDelegateApproval 授权助理/主管在范围+期限内代审。
func (s *Server) handleDelegateApproval(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req delegateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	agent, err := s.store.GetAgent(ctx, strings.TrimSpace(req.ToAgentID))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	approval, err := s.store.GetApproval(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if approval.Status != domain.ApprovalPending {
		s.writeErr(w, errConflict("只有 pending 审批可以授权"))
		return
	}
	expires, err := time.Parse(time.RFC3339, strings.TrimSpace(req.ExpiresAt))
	if err != nil || !expires.After(time.Now().UTC()) {
		s.writeErr(w, errInvalid("expires_at 必须是未来时间"))
		return
	}
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = "once"
	}
	if scope != "once" && scope != "session" {
		s.writeErr(w, errInvalid("scope 必须是 once/session"))
		return
	}
	payload := jsonDetail(map[string]any{"to_agent_id": agent.ID, "scope": scope,
		"expires_at": expires.UTC().Format(time.RFC3339), "approval_id": approval.ID})
	if err := s.store.SetSetting(ctx, delegateApprovalPrefix+approval.ID, payload); err != nil {
		s.writeErr(w, err)
		return
	}
	if _, err := s.store.InsertSchedule(ctx, store.Schedule{Kind: "delegate.expire", DueAt: expires.UTC().Format(store.Now()[:0] + "2006-01-02T15:04:05.000000000Z07:00"),
		Payload: jsonDetail(map[string]any{"setting_key": delegateApprovalPrefix + approval.ID, "approval_id": approval.ID})}); err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "approval.delegate", "approval", approval.ID, "ok",
		jsonDetail(map[string]any{"to_agent_id": agent.ID, "scope": scope, "expires_at": expires.UTC().Format(time.RFC3339)}))
	writeOK(w, map[string]any{"approval_id": approval.ID, "to_agent_id": agent.ID, "scope": scope,
		"expires_at": expires.UTC().Format(time.RFC3339)})
}

// delegatedAgent 返回该审批有效的被授权 Agent（无授权/过期/不匹配返回 nil）。
func (s *Server) delegatedAgent(ctx context.Context, approvalID, agentID string) map[string]any {
	raw, ok, err := s.store.Setting(ctx, delegateApprovalPrefix+approvalID)
	if err != nil || !ok {
		return nil
	}
	var d map[string]any
	if json.Unmarshal([]byte(raw), &d) != nil {
		return nil
	}
	if d["to_agent_id"] != agentID {
		return nil
	}
	expRaw, _ := d["expires_at"].(string)
	exp, err := time.Parse(time.RFC3339, expRaw)
	if err != nil || !exp.After(time.Now().UTC()) {
		return nil
	}
	return d
}

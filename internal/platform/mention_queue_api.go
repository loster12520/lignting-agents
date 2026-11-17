package platform

import (
	"net/http"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// R4 @ 发言队列窄接口（core.Runtime 结构化满足；队列在 core 进程内，重启丢失未开始任务）。
type MentionQueueService interface {
	MentionQueueStatusFor(conversationID string) core.MentionQueueStatus
	StopMentionQueue(conversationID string) int
}

func (s *Server) SetMentionQueueService(svc MentionQueueService) {
	s.mentionMu.Lock()
	defer s.mentionMu.Unlock()
	s.mention = svc
}

func (s *Server) mentionQueueService(w http.ResponseWriter) (MentionQueueService, bool) {
	s.mentionMu.RLock()
	svc := s.mention
	s.mentionMu.RUnlock()
	if svc == nil {
		s.writeErr(w, domain.NewError(domain.CodeUnavailable, "@ 队列服务未接入（core Runtime 未注入）", http.StatusServiceUnavailable))
		return nil, false
	}
	return svc, true
}

func (s *Server) handleMentionQueueStatus(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	svc, ok := s.mentionQueueService(w)
	if !ok {
		return
	}
	writeOK(w, svc.MentionQueueStatusFor(r.PathValue("id")))
}

func (s *Server) handleStopMentionQueue(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	svc, ok := s.mentionQueueService(w)
	if !ok {
		return
	}
	conversationID := r.PathValue("id")
	discarded := svc.StopMentionQueue(conversationID)
	status := svc.MentionQueueStatusFor(conversationID)
	s.audit(ctx, domain.SenderUser, owner.ID, "mention_queue.stop", "conversation", conversationID, "ok",
		jsonDetail(map[string]any{"discarded": discarded, "pending_jobs": status.PendingJobs, "stopped": status.Stopped}))
	writeOK(w, map[string]any{"conversation_id": conversationID, "stopped": true, "discarded": discarded, "status": status})
}

// handleDebateStatus GET /api/debates/{id}/status?conversation_id=
func (s *Server) handleDebateStatus(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	svc, ok := s.requireDebate(w)
	if !ok {
		return
	}
	conversationID := r.URL.Query().Get("conversation_id")
	if conversationID == "" {
		s.writeErr(w, errInvalid("conversation_id 必填"))
		return
	}
	view, err := svc.DebateStatus(r.Context(), conversationID, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, view)
}

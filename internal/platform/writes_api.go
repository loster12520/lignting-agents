package platform

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// R4 写工具只读投影（api-contract §9）：时间线/写事件详情/列表。
// 写锁、base_hash、diff 生成在 internal/write；platform 不复制规则，只读库。

func (s *Server) handleWriteTimeline(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	q := r.URL.Query()
	limit, err := parseInt64Param(q.Get("limit"), 50)
	if err != nil || limit <= 0 || limit > 200 {
		s.writeErr(w, errInvalid("limit 必须在 1..200"))
		return
	}
	items, err := s.store.ListTimeline(r.Context(), q.Get("path"), q.Get("branch"), q.Get("commit"),
		q.Get("agent_id"), q.Get("since"), int(limit))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

func (s *Server) handleGetWriteEvent(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	event, err := s.store.GetWriteEvent(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, event)
}

// handleListWriteEvents：status 为空时聚合四种状态（v0.0.1 无统一 list 仓储方法）。
func (s *Server) handleListWriteEvents(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	q := r.URL.Query()
	limit, err := parseInt64Param(q.Get("limit"), 100)
	if err != nil || limit <= 0 || limit > 500 {
		s.writeErr(w, errInvalid("limit 必须在 1..500"))
		return
	}
	statuses := []string{strings.TrimSpace(q.Get("status"))}
	if statuses[0] == "" {
		statuses = []string{domain.WritePending, domain.WriteApplied, domain.WriteRejected, domain.WriteUnknown}
	}
	path := q.Get("path")
	agentID := q.Get("agent_id")
	var items []domain.WriteEvent
	for _, status := range statuses {
		events, err := s.store.ListWriteEventsByStatus(r.Context(), status, int(limit))
		if err != nil {
			s.writeErr(w, err)
			return
		}
		for _, e := range events {
			if path != "" && e.Path != path {
				continue
			}
			if agentID != "" && e.AgentID != agentID {
				continue
			}
			items = append(items, e)
		}
	}
	if len(items) > int(limit) {
		items = items[:limit]
	}
	next := ""
	if len(items) == int(limit) {
		next = strconv.FormatInt(int64(len(items)), 10)
	}
	writeList(w, items, int64(len(items)), next)
}

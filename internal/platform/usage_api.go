package platform

import (
	"net/http"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// handleUsage GET /api/usage：无 group_by 返回 totals；有则按 agent/task/model/day/department 聚合。
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	ctx := r.Context()
	q := r.URL.Query()
	from, err := normalizeTimeParam(q.Get("from"), false)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	to, err := normalizeTimeParam(q.Get("to"), true)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	query := domain.UsageQuery{
		AgentID: q.Get("agent_id"), TaskID: q.Get("task_id"),
		DepartmentID: q.Get("department_id"), From: from, To: to,
	}
	groupBy := strings.TrimSpace(q.Get("group_by"))
	if groupBy == "" {
		totals, err := s.store.SumUsage(ctx, query)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeOK(w, totals)
		return
	}
	items, err := s.store.QueryUsage(ctx, store.UsageFilter{
		AgentID: query.AgentID, TaskID: query.TaskID, DepartmentID: query.DepartmentID,
		From: from, To: to, GroupBy: groupBy, Limit: 200,
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

package platform

import (
	"net/http"
	"strings"

	"github.com/lignting/lignting-agents/internal/store"
)

// R4 只读 API（写路径等 core R4 service 就绪后经 org/work service 注入；platform 不复制状态机）。

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	q := r.URL.Query()
	items, err := s.store.ListTasks(r.Context(), store.TaskFilter{
		Status: q.Get("status"), DepartmentID: q.Get("department_id"), OwnerID: q.Get("owner_id"), Limit: 200,
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	ctx := r.Context()
	id := r.PathValue("id")
	task, err := s.store.GetTask(ctx, id)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	criteria, err := s.store.ListTaskCriteria(ctx, id)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	deps, err := s.store.ListTaskDependencies(ctx, id)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	events, err := s.store.ListTaskEvents(ctx, id)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"task": task, "criteria": criteria, "dependencies": deps, "events": events})
}

func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	q := r.URL.Query()
	items, err := s.store.ListArtifacts(r.Context(), store.ArtifactFilter{TaskID: q.Get("task_id"), Type: q.Get("type"), Limit: 200})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

func (s *Server) handleListArtifactVersions(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	items, err := s.store.ListArtifactVersions(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

func (s *Server) handleGetArtifactVersion(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	version, err := parseInt64Param(r.PathValue("v"), 0)
	if err != nil || version <= 0 {
		s.writeErr(w, errInvalid("版本号不合法"))
		return
	}
	v, err := s.store.GetArtifactVersion(r.Context(), r.PathValue("id"), int(version))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, v)
}

func (s *Server) handleListReviews(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	q := r.URL.Query()
	items, err := s.store.ListReviews(r.Context(), store.ReviewFilter{ArtifactID: q.Get("artifact_id"), Status: q.Get("status"), Limit: 200})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

func (s *Server) handleListDecisions(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	items, err := s.store.ListDecisionsByStatus(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		filtered := items[:0]
		for _, d := range items {
			if strings.Contains(d.Title, q) || strings.Contains(d.Decision, q) || strings.Contains(d.Rationale, q) {
				filtered = append(filtered, d)
			}
		}
		items = filtered
	}
	writeList(w, items, int64(len(items)), "")
}

func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	q := r.URL.Query()
	items, err := s.store.ListApprovals(r.Context(), store.ApprovalFilter{Status: q.Get("status"), Kind: q.Get("kind")})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

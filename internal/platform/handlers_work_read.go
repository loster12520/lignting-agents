package platform

import (
	"net/http"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
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
	ctx := r.Context()
	q := r.URL.Query()
	if svc := s.debateService(); svc != nil {
		var items []domain.Decision
		var err error
		switch {
		case strings.TrimSpace(q.Get("q")) != "":
			items, err = svc.SearchDecisions(ctx, q.Get("q"))
		case strings.TrimSpace(q.Get("task_id")) != "":
			items, err = svc.ListDecisionsByTask(ctx, q.Get("task_id"))
		default:
			items, err = svc.ListDecisions(ctx, q.Get("status"))
		}
		if err != nil {
			s.writeErr(w, err)
			return
		}
		writeList(w, items, int64(len(items)), "")
		return
	}
	items, err := s.store.ListDecisionsByStatus(ctx, q.Get("status"))
	if err != nil {
		s.writeErr(w, err)
		return
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

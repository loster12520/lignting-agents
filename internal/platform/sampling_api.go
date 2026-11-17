package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// R4 人类抽检 / 评审质量（FR-REV-05）。抽检结果落 audit_log(action=review.sampling)，
// 不新增 schema、不阻塞交付。

var samplingResults = map[string]bool{
	"valid": true, "invalid": true, "insufficient_evidence": true,
}

var samplingSecretPattern = regexp.MustCompile(`sk-[A-Za-z0-9_-]{12,}`)

type samplingRequest struct {
	Result string `json:"result"`
	Note   string `json:"note"`
}

// registerSamplingRoutes mounts the FR-REV-05 routes. Call it from Server.routes:
//
//	s.registerSamplingRoutes(m)
//
// It is kept separate so server.go (owned elsewhere) only needs one line.
func (s *Server) registerSamplingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/reviews/{id}/sampling", s.handleReviewSampling)
	mux.HandleFunc("GET /api/quality/reviews", s.authed(s.handleQualityReviews))
	mux.HandleFunc("GET /api/reviews/sampling/suggestions", s.authed(s.handleSamplingSuggestions))
}

func (s *Server) redactSamplingText(text string) string {
	s.secretsMu.RLock()
	secrets := s.secrets
	s.secretsMu.RUnlock()
	out := secrets.Redact(text)
	// 额外兜底：即使 Key 未写入 secrets.json，也不把 sk- 形态原文写进审计。
	return samplingSecretPattern.ReplaceAllString(out, "[REDACTED]")
}

// handleReviewSampling POST /api/reviews/{id}/sampling（仅人类；Agent Token 403）。
func (s *Server) handleReviewSampling(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	owner, err := s.currentOwner(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if owner == nil {
		if agent, err := s.agentFromToken(ctx, r); err == nil && agent != nil {
			s.writeErr(w, errForbidden("只有人类可以抽检评审"))
			return
		}
		s.writeErr(w, errUnauthorized("请先登录"))
		return
	}
	if err := s.verifyCSRF(r, owner); err != nil {
		s.writeErr(w, err)
		return
	}
	var req samplingRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	result := strings.TrimSpace(req.Result)
	if !samplingResults[result] {
		s.writeErr(w, errInvalid("result 必须是 valid/invalid/insufficient_evidence"))
		return
	}
	reviewID := strings.TrimSpace(r.PathValue("id"))
	if reviewID == "" {
		s.writeErr(w, errInvalid("review id 不能为空"))
		return
	}
	review, err := s.store.GetReview(ctx, reviewID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	verdicts, err := s.store.ListReviewVerdicts(ctx, reviewID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if len(verdicts) == 0 {
		s.writeErr(w, errConflict("该评审还没有裁决，无法抽检"))
		return
	}
	reviewerID := verdicts[len(verdicts)-1].ReviewerID
	authorID := review.OwnerID
	note := strings.TrimSpace(req.Note)
	if len(note) > 2000 {
		note = note[:2000]
	}
	note = s.redactSamplingText(note)
	s.audit(ctx, domain.SenderUser, owner.ID, "review.sampling", "review", reviewID, "ok",
		jsonDetail(map[string]any{
			"review_id": reviewID, "result": result, "note": note,
			"reviewer_id": reviewerID, "author_id": authorID,
		}))
	writeOK(w, map[string]any{
		"review_id": reviewID, "result": result,
		"reviewer_id": reviewerID, "author_id": authorID,
	})
}

// samplingCounts 从 audit_log 聚合每个 reviewer×author 的抽检结果。
// from/to 为已归一化的时间字符串（可空）。
func (s *Server) samplingCounts(ctx context.Context, from, to string) (map[string]map[string]int, error) {
	filter := store.AuditFilter{Action: "review.sampling", Limit: 1000, SinceCreatedAt: from}
	audits, err := s.store.QueryAudit(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]int{}
	for _, a := range audits {
		if to != "" && a.CreatedAt > to {
			continue
		}
		var d map[string]any
		if json.Unmarshal([]byte(a.Detail), &d) != nil {
			continue
		}
		reviewer, _ := d["reviewer_id"].(string)
		author, _ := d["author_id"].(string)
		result, _ := d["result"].(string)
		if reviewer == "" || author == "" || result == "" {
			continue
		}
		key := reviewer + "|" + author
		if out[key] == nil {
			out[key] = map[string]int{}
		}
		out[key][result]++
		out[key]["total"]++
	}
	return out, nil
}

type qualityReviewItem struct {
	store.ReviewQualityStat
	SamplingSuggested bool           `json:"sampling_suggested"`
	Sampling          map[string]int `json:"sampling"`
}

// handleQualityReviews GET /api/quality/reviews?department_id=&from=&to=
// 按 reviewer×author 统计 approve 次数/通过率；≥5 且 100% 仅提示抽检，不自动判定串谋。
func (s *Server) handleQualityReviews(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
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
	stats, err := s.store.ReviewQualityStats(ctx, q.Get("department_id"), from, to)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	samplings, err := s.samplingCounts(ctx, from, to)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	items := make([]qualityReviewItem, 0, len(stats))
	for _, st := range stats {
		sc := samplings[st.ReviewerID+"|"+st.AuthorID]
		if sc == nil {
			sc = map[string]int{}
		}
		items = append(items, qualityReviewItem{
			ReviewQualityStat: st,
			SamplingSuggested: st.Total >= 5 && st.Approvals == st.Total,
			Sampling:          sc,
		})
	}
	writeList(w, items, int64(len(items)), "")
}

type samplingSuggestion struct {
	ReviewerID        string                    `json:"reviewer_id"`
	AuthorID          string                    `json:"author_id"`
	DepartmentID      string                    `json:"department_id,omitempty"`
	Total             int                       `json:"total"`
	Approvals         int                       `json:"approvals"`
	PassRate          float64                   `json:"pass_rate"`
	SamplingSuggested bool                      `json:"sampling_suggested"`
	Candidates        []store.SamplingCandidate `json:"candidates"`
}

// handleSamplingSuggestions GET /api/reviews/sampling/suggestions
// 返回 ≥5 次且 100% 通过的 reviewer×author 对，以及带证据/裁决/异议的候选。
func (s *Server) handleSamplingSuggestions(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	ctx := r.Context()
	stats, err := s.store.ReviewQualityStats(ctx, "", "", "")
	if err != nil {
		s.writeErr(w, err)
		return
	}
	items := make([]samplingSuggestion, 0, 4)
	for _, st := range stats {
		if st.Total < 5 || st.Approvals != st.Total {
			continue
		}
		cands, err := s.store.SamplingCandidates(ctx, st.ReviewerID, st.AuthorID, 5)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		items = append(items, samplingSuggestion{
			ReviewerID: st.ReviewerID, AuthorID: st.AuthorID, DepartmentID: st.DepartmentID,
			Total: st.Total, Approvals: st.Approvals, PassRate: st.PassRate,
			SamplingSuggested: true, Candidates: cands,
		})
	}
	writeList(w, items, int64(len(items)), "")
}

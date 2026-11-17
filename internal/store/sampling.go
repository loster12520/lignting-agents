package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// R4 评审质量/人类抽检（FR-REV-05）。不新增 schema：
// - 通过率来自 review_verdicts JOIN reviews；
// - 抽检结果落 audit_log(action=review.sampling)，由 platform 写入。

// ReviewQualityStat 是 reviewer×author 的裁决统计。
type ReviewQualityStat struct {
	ReviewerID   string  `json:"reviewer_id"`
	AuthorID     string  `json:"author_id"`
	DepartmentID string  `json:"department_id,omitempty"`
	Total        int     `json:"total"`
	Approvals    int     `json:"approvals"`
	PassRate     float64 `json:"pass_rate"`
}

// SamplingCandidate 是一条可抽检的通过裁决（带证据/裁决/异议）。
type SamplingCandidate struct {
	Review        domain.Review        `json:"review"`
	Verdict       domain.ReviewVerdict `json:"verdict"`
	ArtifactTitle string               `json:"artifact_title"`
	Dissent       string               `json:"dissent"`
}

// ReviewQualityStats 统计 reviewer×author 的裁决通过率（可按部门/时间过滤）。
// departmentID 匹配产出物所属部门；from/to 为 RFC3339 时间（含端点）。
func (s *Store) ReviewQualityStats(ctx context.Context, departmentID, from, to string) ([]ReviewQualityStat, error) {
	where := []string{"1=1"}
	args := make([]any, 0, 4)
	if departmentID != "" {
		where = append(where, "a.department_id=?")
		args = append(args, departmentID)
	}
	if from != "" {
		where = append(where, "rv.created_at >= ?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "rv.created_at <= ?")
		args = append(args, to)
	}
	q := "SELECT rv.reviewer_id, r.owner_id, COALESCE(a.department_id,''), COUNT(*), " +
		"SUM(CASE WHEN rv.verdict IN ('" + domain.VerdictApprove + "','" + domain.VerdictApproveComment + "') THEN 1 ELSE 0 END) " +
		"FROM review_verdicts rv JOIN reviews r ON r.id = rv.review_id " +
		"LEFT JOIN artifacts a ON a.id = r.artifact_id " +
		"WHERE " + strings.Join(where, " AND ") + " " +
		"GROUP BY rv.reviewer_id, r.owner_id, COALESCE(a.department_id,'') " +
		"HAVING COUNT(*) > 0 ORDER BY COUNT(*) DESC, rv.reviewer_id ASC, r.owner_id ASC"
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("review quality stats: %w", err)
	}
	defer rows.Close()
	out := make([]ReviewQualityStat, 0, 16)
	for rows.Next() {
		var st ReviewQualityStat
		if err := rows.Scan(&st.ReviewerID, &st.AuthorID, &st.DepartmentID, &st.Total, &st.Approvals); err != nil {
			return nil, fmt.Errorf("scan review stat: %w", err)
		}
		if st.Total > 0 {
			st.PassRate = float64(st.Approvals) / float64(st.Total)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// SamplingCandidates 返回某 reviewer 对某 author 的最近通过裁决（抽检建议详情）。
func (s *Store) SamplingCandidates(ctx context.Context, reviewerID, authorID string, limit int) ([]SamplingCandidate, error) {
	if strings.TrimSpace(reviewerID) == "" || strings.TrimSpace(authorID) == "" {
		return []SamplingCandidate{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx,
		"SELECT r.id, r.from_department, r.to_department, r.artifact_id, r.artifact_version_id, r.severity, r.stance, r.claim, r.evidence, r.impact, r.proposed_resolution, r.owner_id, r.due_at, r.status, r.thread_conversation_id, r.created_at, "+
			"rv.id, rv.review_id, rv.verdict, rv.checked, rv.rationale, rv.conditions, rv.residual_risk, rv.reviewer_id, rv.decision_id, rv.created_at, "+
			"COALESCE(a.title,''), COALESCE(d.dissent,'[]') "+
			"FROM review_verdicts rv JOIN reviews r ON r.id = rv.review_id "+
			"LEFT JOIN artifacts a ON a.id = r.artifact_id "+
			"LEFT JOIN decisions d ON d.id = rv.decision_id "+
			"WHERE rv.reviewer_id=? AND r.owner_id=? AND rv.verdict IN ('"+domain.VerdictApprove+"','"+domain.VerdictApproveComment+"') "+
			"ORDER BY rv.created_at DESC LIMIT ?", reviewerID, authorID, limit)
	if err != nil {
		return nil, fmt.Errorf("sampling candidates: %w", err)
	}
	defer rows.Close()
	out := make([]SamplingCandidate, 0, limit)
	for rows.Next() {
		var (
			c     SamplingCandidate
			r     domain.Review
			v     domain.ReviewVerdict
			title string
		)
		if err := rows.Scan(&r.ID, &r.FromDepartment, &r.ToDepartment, &r.ArtifactID, &r.ArtifactVersionID,
			&r.Severity, &r.Stance, &r.Claim, &r.Evidence, &r.Impact, &r.ProposedResolution, &r.OwnerID,
			&r.DueAt, &r.Status, &r.ThreadConversationID, &r.CreatedAt,
			&v.ID, &v.ReviewID, &v.Verdict, &v.Checked, &v.Rationale, &v.Conditions, &v.ResidualRisk,
			&v.ReviewerID, &v.DecisionID, &v.CreatedAt, &title, &c.Dissent); err != nil {
			return nil, fmt.Errorf("scan sampling candidate: %w", err)
		}
		c.Review, c.Verdict, c.ArtifactTitle = r, v, title
		out = append(out, c)
	}
	return out, rows.Err()
}

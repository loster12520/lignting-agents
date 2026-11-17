package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// ReviewInput 是挑战单输入（FR-REV-01）。
type ReviewInput struct {
	ArtifactID         string
	ArtifactVersionID  string
	FromDepartment     string
	ToDepartment       string
	Severity           string
	Stance             string
	Claim              string
	Evidence           string
	Impact             string
	ProposedResolution string
	DueAt              string
}

// CreateReview 创建挑战单；blocker/major 缺证据自动降级为 question。
func (s *WorkService) CreateReview(ctx context.Context, actor domain.Agent, in ReviewInput) (domain.Review, error) {
	if in.ArtifactID == "" || strings.TrimSpace(in.Claim) == "" {
		return domain.Review{}, domain.NewError(domain.CodeInvalid, "挑战单缺少产出物或主张", 400)
	}
	artifact, err := s.store.GetArtifact(ctx, in.ArtifactID)
	if err != nil {
		return domain.Review{}, err
	}
	severity := in.Severity
	switch severity {
	case domain.SeverityBlocker, domain.SeverityMajor:
		if strings.TrimSpace(in.Evidence) == "" || in.Evidence == "[]" {
			severity = domain.SeverityQuestion
		}
	case domain.SeverityMinor, domain.SeverityQuestion, "":
		if severity == "" {
			severity = domain.SeverityQuestion
		}
	default:
		return domain.Review{}, domain.NewError(domain.CodeInvalid, "未知严重级别："+severity, 400)
	}
	if in.ArtifactVersionID == "" {
		versions, err := s.store.ListArtifactVersions(ctx, artifact.ID)
		if err != nil || len(versions) == 0 {
			return domain.Review{}, domain.NewError(domain.CodeInvalid, "产出物没有版本可评审", 400)
		}
		latest := versions[0]
		for _, v := range versions {
			if v.Version > latest.Version {
				latest = v
			}
		}
		in.ArtifactVersionID = latest.ID
	}
	review, err := s.store.CreateReview(ctx, domain.Review{
		ID: domain.NewID("rev"), FromDepartment: in.FromDepartment, ToDepartment: in.ToDepartment,
		ArtifactID: artifact.ID, ArtifactVersionID: in.ArtifactVersionID, Severity: severity,
		Stance: in.Stance, Claim: in.Claim, Evidence: in.Evidence, Impact: in.Impact,
		ProposedResolution: in.ProposedResolution, OwnerID: artifact.CreatedBy, DueAt: in.DueAt,
		Status: "open", CreatedAt: s.nowString(),
	})
	if err != nil {
		return domain.Review{}, fmt.Errorf("core: 创建挑战单失败: %w", err)
	}
	s.emit(ctx, "review.created", "review", review.ID, "agent", actor.ID, map[string]any{"artifact_id": artifact.ID, "severity": review.Severity})
	s.audit(ctx, "agent", actor.ID, "review.create", "review", review.ID, "ok", map[string]any{"artifact_id": artifact.ID, "severity": review.Severity})
	return review, nil
}

// SubmitVerdict 提交裁决；作者不能评审自己的产出物；request_changes 必须列修改项。
func (s *WorkService) SubmitVerdict(ctx context.Context, actor domain.Agent, reviewID, verdict, checked, rationale, conditions, residualRisk string) (domain.ReviewVerdict, error) {
	review, err := s.store.GetReview(ctx, reviewID)
	if err != nil {
		return domain.ReviewVerdict{}, err
	}
	if review.Status != "open" && review.Status != "responded" && review.Status != "rework" {
		return domain.ReviewVerdict{}, domain.NewError(domain.CodeConflict, "挑战单当前状态不可裁决："+review.Status, 409)
	}
	artifact, err := s.store.GetArtifact(ctx, review.ArtifactID)
	if err != nil {
		return domain.ReviewVerdict{}, err
	}
	if actor.ID == artifact.CreatedBy {
		return domain.ReviewVerdict{}, NewForbidden("作者不能评审自己的产出物", "请由非作者的评审人提交裁决。")
	}
	valid := map[string]bool{
		domain.VerdictApprove: true, domain.VerdictApproveComment: true,
		domain.VerdictRequestChanges: true, domain.VerdictEscalate: true,
	}
	if !valid[verdict] {
		return domain.ReviewVerdict{}, domain.NewError(domain.CodeInvalid, "未知裁决："+verdict, 400)
	}
	if verdict == domain.VerdictRequestChanges && strings.TrimSpace(conditions) == "" {
		return domain.ReviewVerdict{}, domain.NewError(domain.CodeInvalid, "request_changes 必须列出必须修改项", 400)
	}
	if (review.Severity == domain.SeverityBlocker || review.Severity == domain.SeverityMajor) && strings.TrimSpace(checked) == "" {
		return domain.ReviewVerdict{}, domain.NewError(domain.CodeInvalid, "blocker/major 裁决必须说明实际检查了什么", 400)
	}
	out, err := s.store.CreateReviewVerdict(ctx, domain.ReviewVerdict{
		ID: domain.NewID("rv"), ReviewID: review.ID, Verdict: verdict, Checked: checked,
		Rationale: rationale, Conditions: conditions, ResidualRisk: residualRisk,
		ReviewerID: actor.ID, CreatedAt: s.nowString(),
	})
	if err != nil {
		return domain.ReviewVerdict{}, err
	}
	newStatus := "resolved"
	switch verdict {
	case domain.VerdictRequestChanges:
		newStatus = "rework"
	case domain.VerdictEscalate:
		newStatus = "escalated"
	}
	if err := s.store.UpdateReviewStatus(ctx, review.ID, newStatus); err != nil {
		return domain.ReviewVerdict{}, err
	}
	s.emit(ctx, "review.verdict", "review", review.ID, "agent", actor.ID, map[string]any{"verdict": verdict, "status": newStatus})
	s.audit(ctx, "agent", actor.ID, "review.verdict", "review", review.ID, "ok", map[string]any{"verdict": verdict})
	return out, nil
}

// Rework 返工：为产出物提交新版本，并把挑战单置为 responded。
func (s *WorkService) Rework(ctx context.Context, actor domain.Agent, reviewID, content string) (domain.ArtifactVersion, error) {
	review, err := s.store.GetReview(ctx, reviewID)
	if err != nil {
		return domain.ArtifactVersion{}, err
	}
	if review.Status != "rework" && review.Status != "open" {
		return domain.ArtifactVersion{}, domain.NewError(domain.CodeConflict, "挑战单当前状态不可返工："+review.Status, 409)
	}
	version, err := s.AddArtifactVersion(ctx, actor, review.ArtifactID, content, review.ArtifactVersionID)
	if err != nil {
		return domain.ArtifactVersion{}, err
	}
	if err := s.store.UpdateReviewStatus(ctx, review.ID, "responded"); err != nil {
		return domain.ArtifactVersion{}, err
	}
	s.emit(ctx, "review.rework", "review", review.ID, "agent", actor.ID, map[string]any{"artifact_version_id": version.ID, "version": version.Version})
	s.audit(ctx, "agent", actor.ID, "review.rework", "review", review.ID, "ok", map[string]any{"version": version.Version})
	return version, nil
}

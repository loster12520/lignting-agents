package core

import (
	"context"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// GetDecision 读取单条决策记录。
func (s *DebateService) GetDecision(ctx context.Context, id string) (domain.Decision, error) {
	return s.store.GetDecision(ctx, id)
}

// ListDecisions 按状态列出决策（空状态=全部状态，逐个状态聚合）。
func (s *DebateService) ListDecisions(ctx context.Context, status string) ([]domain.Decision, error) {
	if status != "" {
		return s.store.ListDecisionsByStatus(ctx, status)
	}
	statuses := []string{domain.DecisionProposed, domain.DecisionAccepted, domain.DecisionRejected, domain.DecisionSuperseded}
	var out []domain.Decision
	for _, st := range statuses {
		items, err := s.store.ListDecisionsByStatus(ctx, st)
		if err != nil {
			continue
		}
		out = append(out, items...)
	}
	return out, nil
}

// ListDecisionsByTask 返回影响某任务的决策（异议/来源保留）。
func (s *DebateService) ListDecisionsByTask(ctx context.Context, taskID string) ([]domain.Decision, error) {
	return s.store.ListDecisionsByTask(ctx, taskID)
}

// SearchDecisions 在决策记录中做字面检索（标题/背景/决定/理由/异议/证据）。
// platform 侧若接入全局检索，可继续用 search 包，这里保证 core 自足。
func (s *DebateService) SearchDecisions(ctx context.Context, query string) ([]domain.Decision, error) {
	all, err := s.ListDecisions(ctx, "")
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return all, nil
	}
	var out []domain.Decision
	for _, d := range all {
		haystack := strings.ToLower(strings.Join([]string{d.Title, d.Context, d.Decision, d.Rationale, d.Dissent, d.Evidence}, " "))
		if strings.Contains(haystack, q) {
			out = append(out, d)
		}
	}
	return out, nil
}

// AppendDissent 以新记录表达异议（决策不可变，旧记录保留并被 supersede）。
func (s *DebateService) AppendDissent(ctx context.Context, actor domain.Agent, decisionID, dissent string) (domain.Decision, error) {
	if actor.Kind != domain.AgentKindAssistant && actor.Kind != domain.AgentKindSupervisor {
		return domain.Decision{}, NewForbidden("只有助理/部门主管可以登记异议", "请由助理或主管提交。")
	}
	old, err := s.store.GetDecision(ctx, decisionID)
	if err != nil {
		return domain.Decision{}, err
	}
	merged := old.Dissent
	if strings.TrimSpace(merged) == "" {
		merged = dissent
	} else {
		merged = merged + " | " + dissent
	}
	next := old
	next.ID = domain.NewID("dec")
	next.Dissent = merged
	next.Supersedes = old.ID
	next.CreatedAt = s.nowString()
	next.CreatedBy = actor.ID
	if next.Status == "" {
		next.Status = domain.DecisionAccepted
	}
	out, err := s.store.CreateDecision(ctx, next)
	if err != nil {
		return domain.Decision{}, err
	}
	if err := s.store.SupersedeDecision(ctx, old.ID, out.ID); err != nil {
		return domain.Decision{}, err
	}
	s.emit(ctx, "decision.updated", "decision", out.ID, "agent", actor.ID, map[string]any{"supersedes": old.ID, "dissent": true})
	s.audit(ctx, "agent", actor.ID, "decision.dissent", "decision", out.ID, "ok", map[string]any{"supersedes": old.ID})
	return out, nil
}

// DecisionView 是决策列表/详情 DTO（domain.Decision 已带 json tag）。
type DecisionView struct {
	Decision domain.Decision `json:"decision"`
	Reopened int             `json:"reopened"`
}

// DecisionDetail 返回决策 + 重开次数。
func (s *DebateService) DecisionDetail(ctx context.Context, id string) (DecisionView, error) {
	d, err := s.store.GetDecision(ctx, id)
	if err != nil {
		return DecisionView{}, err
	}
	return DecisionView{Decision: d, Reopened: countReopens(d.RevisitTrigger)}, nil
}

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// DebateStore 是 R6 的持久化端口（消息线程 + 决策 + 任务 + 事件/审计）。
type DebateStore interface {
	AppendMessage(ctx context.Context, msg domain.Message) (domain.Message, error)
	ListMessages(ctx context.Context, conversationID string, before, after int64, limit int) ([]domain.Message, error)
	GetTask(ctx context.Context, id string) (domain.Task, error)
	CreateDecision(ctx context.Context, d domain.Decision) (domain.Decision, error)
	GetDecision(ctx context.Context, id string) (domain.Decision, error)
	ListDecisionsByStatus(ctx context.Context, status string) ([]domain.Decision, error)
	ListDecisionsByTask(ctx context.Context, taskID string) ([]domain.Decision, error)
	SupersedeDecision(ctx context.Context, oldID, newID string) error
	AppendEvent(ctx context.Context, ev domain.Event) (domain.Event, error)
	AppendAudit(ctx context.Context, a domain.AuditEntry) (domain.AuditEntry, error)
}

// DebateService 负责部门内讨论、跨部门辩论、升级与决策记录。
type DebateService struct {
	store   DebateStore
	now     func() time.Time
	timebox time.Duration
}

// NewDebateService 构造服务。
func NewDebateService(store DebateStore) *DebateService {
	return &DebateService{store: store, now: func() time.Time { return time.Now().UTC() }, timebox: DefaultDebateTimebox}
}

func (s *DebateService) nowString() string { return s.now().Format(time.RFC3339Nano) }

// PostDiscussion 部门内讨论发言：带分工视角，每人≤5/总≤15，写任务线程消息。
func (s *DebateService) PostDiscussion(ctx context.Context, actor domain.Agent, conversationID, debateID, perspective, stance, evidence string) (domain.Message, error) {
	if actor.Kind != domain.AgentKindEmployee && actor.Kind != domain.AgentKindSupervisor {
		return domain.Message{}, NewForbidden("只有部门成员可以参加部门内讨论", "请由本部门员工/主管发言。")
	}
	if strings.TrimSpace(perspective) == "" || strings.TrimSpace(stance) == "" {
		return domain.Message{}, domain.NewError(domain.CodeInvalid, "讨论发言必须带 perspective 与立场", 400)
	}
	subs, err := s.loadSubmissions(ctx, conversationID, debateID)
	if err != nil {
		return domain.Message{}, err
	}
	if err := CheckStanceLimits(subs, actor.ID, 1); err != nil {
		return domain.Message{}, domain.NewError(domain.CodeConflict, "讨论超限："+err.Error(), 409)
	}
	meta := map[string]any{
		"kind": "dept.discussion", "debate_id": debateID, "actor_id": actor.ID,
		"round": 1, "perspective": perspective, "stance": stance,
		"evidence": evidence, "at": s.nowString(),
	}
	return s.appendDebateMessage(ctx, actor, conversationID, meta, perspective+"："+stance)
}

// SubmitStance 跨部门辩论提交立场：R1/R2、每人≤5/总≤15、必须有 evidence_ids；
// 重复立场且无新证据不计轮次并返回冲突。
func (s *DebateService) SubmitStance(ctx context.Context, actor domain.Agent, conversationID, debateID string, round int, stance string, evidenceIDs []string) (domain.Message, error) {
	if actor.Kind != domain.AgentKindSupervisor && actor.Kind != domain.AgentKindAssistant {
		return domain.Message{}, NewForbidden("跨部门辩论由部门主管代表部门发言", "请由本部门主管提交决策包/立场。")
	}
	if strings.TrimSpace(stance) == "" {
		return domain.Message{}, domain.NewError(domain.CodeInvalid, "立场不能为空", 400)
	}
	if len(evidenceIDs) == 0 {
		return domain.Message{}, domain.NewError(domain.CodeInvalid, "辩论提交必须带 evidence_ids（可核查证据）", 400)
	}
	subs, err := s.loadSubmissions(ctx, conversationID, debateID)
	if err != nil {
		return domain.Message{}, err
	}
	if err := CheckStanceLimits(subs, actor.ID, round); err != nil {
		return domain.Message{}, domain.NewError(domain.CodeConflict, "辩论超限："+err.Error(), 409)
	}
	cur := Submission{ActorID: actor.ID, Round: round, Stance: stance, EvidenceIDs: evidenceIDs, At: s.nowString()}
	if !HasNewEvidence(subs, cur) {
		return domain.Message{}, domain.NewError(domain.CodeConflict, "重复立场且无新证据，不计轮次（请补充新证据或升级）", 409)
	}
	meta := map[string]any{
		"kind": "debate.stance", "debate_id": debateID, "actor_id": actor.ID,
		"round": round, "stance": stance, "evidence_ids": evidenceIDs, "at": cur.At,
	}
	msg, err := s.appendDebateMessage(ctx, actor, conversationID, meta, stance)
	if err != nil {
		return domain.Message{}, err
	}
	if DetectStall(append(subs, cur)) {
		s.emit(ctx, "debate.stalled", "debate", debateID, "system", "core", map[string]any{"conversation_id": conversationID, "reason": "same stance without new evidence"})
	}
	return msg, nil
}

// Escalate 记录 L0–L4 升级包（决策记录 + 事件），不自动裁决。
func (s *DebateService) Escalate(ctx context.Context, actor domain.Agent, conversationID, debateID, taskID, level, issue, evidence, options, recommendation, consequence string) (domain.Decision, error) {
	valid := map[string]bool{EscalationL0: true, EscalationL1: true, EscalationL2: true, EscalationL3: true, EscalationL4: true}
	if !valid[level] {
		return domain.Decision{}, domain.NewError(domain.CodeInvalid, "未知升级层级："+level, 400)
	}
	decision := domain.Decision{
		ID: domain.NewID("dec"), Title: "升级 " + level + "：" + issue, Status: domain.DecisionProposed,
		Context: issue, Options: options, Decision: "", Rationale: recommendation,
		Evidence: evidence, AffectedTasks: taskID, RevisitTrigger: consequence,
		CreatedBy: actor.ID, CreatedAt: s.nowString(),
	}
	out, err := s.store.CreateDecision(ctx, decision)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("core: 记录升级失败: %w", err)
	}
	s.emit(ctx, "debate.escalated", "decision", out.ID, "agent", actor.ID, map[string]any{"level": level, "debate_id": debateID, "conversation_id": conversationID, "task_id": taskID})
	s.audit(ctx, "agent", actor.ID, "debate.escalate", "decision", out.ID, "ok", map[string]any{"level": level})
	return out, nil
}

// CloseDebate 形成不可变决策记录：保留异议、记录影响任务/产出物、可选 supersede 旧决策。
func (s *DebateService) CloseDebate(ctx context.Context, actor domain.Agent, debateID, title, decisionText, rationale, dissent, affectedTasks, affectedArtifacts, supersedes, revisitTrigger string) (domain.Decision, error) {
	if actor.Kind != domain.AgentKindAssistant && actor.Kind != domain.AgentKindSupervisor {
		return domain.Decision{}, NewForbidden("只有助理/部门主管可以形成决策记录", "请由助理主持或部门主管提交。")
	}
	decision := domain.Decision{
		ID: domain.NewID("dec"), Title: title, Status: domain.DecisionAccepted,
		Decision: decisionText, Rationale: rationale, Dissent: dissent,
		AffectedTasks: affectedTasks, AffectedArtifacts: affectedArtifacts,
		Supersedes: supersedes, RevisitTrigger: revisitTrigger,
		CreatedBy: actor.ID, CreatedAt: s.nowString(),
	}
	out, err := s.store.CreateDecision(ctx, decision)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("core: 创建决策失败: %w", err)
	}
	if supersedes != "" {
		if err := s.store.SupersedeDecision(ctx, supersedes, out.ID); err != nil {
			return domain.Decision{}, err
		}
	}
	s.emit(ctx, "decision.created", "decision", out.ID, "agent", actor.ID, map[string]any{"debate_id": debateID, "title": title})
	s.audit(ctx, "agent", actor.ID, "debate.close", "decision", out.ID, "ok", map[string]any{"supersedes": supersedes})
	return out, nil
}

// Reopen 以新证据重开决策（最多 1 次）；第二次必须人类批准。
func (s *DebateService) Reopen(ctx context.Context, actor domain.Agent, decisionID, newEvidence string) (domain.Decision, error) {
	old, err := s.store.GetDecision(ctx, decisionID)
	if err != nil {
		return domain.Decision{}, err
	}
	reopens := countReopens(old.RevisitTrigger)
	if reopens >= DebateReopenLimit {
		return domain.Decision{}, NewForbidden("新证据重开已达上限（最多 1 次）", "第二次重开必须由人类批准并走新决策。")
	}
	if strings.TrimSpace(newEvidence) == "" {
		return domain.Decision{}, domain.NewError(domain.CodeInvalid, "重开必须提供新证据", 400)
	}
	reopenTrigger := fmt.Sprintf("reopen_count:%d;evidence:%s", reopens+1, newEvidence)
	return s.CloseDebate(ctx, actor, "", "重开："+old.Title, old.Decision+"（依新证据重开）", old.Rationale, old.Dissent, old.AffectedTasks, old.AffectedArtifacts, old.ID, reopenTrigger)
}

func countReopens(trigger string) int {
	for _, part := range strings.Split(trigger, ";") {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(part), "reopen_count:%d", &n); err == nil {
			return n
		}
	}
	return 0
}

func (s *DebateService) loadSubmissions(ctx context.Context, conversationID, debateID string) ([]Submission, error) {
	msgs, err := s.store.ListMessages(ctx, conversationID, 0, 0, 200)
	if err != nil {
		return nil, err
	}
	var out []Submission
	for _, m := range msgs {
		sub, ok := ParseSubmissionMeta(m.Meta)
		if !ok {
			continue
		}
		if debateID != "" && !strings.Contains(m.Meta, debateID) {
			continue
		}
		out = append(out, sub)
	}
	return out, nil
}

func (s *DebateService) appendDebateMessage(ctx context.Context, actor domain.Agent, conversationID string, meta map[string]any, content string) (domain.Message, error) {
	raw, _ := json.Marshal(meta)
	msg, err := s.store.AppendMessage(ctx, domain.Message{
		ConversationID: conversationID, SenderType: domain.SenderAgent,
		SenderID: actor.ID, SenderName: actor.Name, Type: domain.MsgText,
		Content: content, Meta: string(raw), CreatedAt: s.nowString(),
	})
	if err != nil {
		return domain.Message{}, fmt.Errorf("core: 写入讨论消息失败: %w", err)
	}
	return msg, nil
}

func (s *DebateService) emit(ctx context.Context, typ, objectType, objectID, actorType, actorID string, payload map[string]any) {
	b, _ := json.Marshal(payload)
	_, _ = s.store.AppendEvent(ctx, domain.Event{
		Type: typ, ObjectType: objectType, ObjectID: objectID,
		ActorType: actorType, ActorID: actorID, Payload: string(b), CreatedAt: s.nowString(),
	})
}

func (s *DebateService) audit(ctx context.Context, actorType, actorID, action, objectType, objectID, result string, detail map[string]any) {
	b, _ := json.Marshal(detail)
	_, _ = s.store.AppendAudit(ctx, domain.AuditEntry{
		ActorType: actorType, ActorID: actorID, Action: action,
		ObjectType: objectType, ObjectID: objectID, Result: result, Detail: string(b), CreatedAt: s.nowString(),
	})
}

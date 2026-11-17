package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

func debateFixture(t *testing.T) (*fakeDebate, *DebateService, domain.Agent, domain.Agent) {
	t.Helper()
	store := newFakeDebate()
	svc := NewDebateService(store)
	sup := domain.Agent{ID: "agent_sup", Name: "研发主管", Kind: domain.AgentKindSupervisor, DepartmentID: "d1"}
	emp := domain.Agent{ID: "agent_emp", Name: "小研", Kind: domain.AgentKindEmployee, DepartmentID: "d1"}
	return store, svc, sup, emp
}

func TestDiscussionLimits(t *testing.T) {
	_, svc, _, emp := debateFixture(t)
	ctx := context.Background()
	if _, err := svc.PostDiscussion(ctx, emp, "conv_t", "task_1", "", "支持", ""); err == nil {
		t.Fatal("缺 perspective 应被拒绝")
	}
	for i := 0; i < MaxStancePerPerson; i++ {
		if _, err := svc.PostDiscussion(ctx, emp, "conv_t", "task_1", "接口", "第"+string(rune('1'+i))+"条", ""); err != nil {
			t.Fatalf("第 %d 条应通过: %v", i+1, err)
		}
	}
	if _, err := svc.PostDiscussion(ctx, emp, "conv_t", "task_1", "接口", "第6条", ""); err == nil || !strings.Contains(err.Error(), "超限") {
		t.Fatalf("第 6 条应被超限拒绝: %v", err)
	}
}

func TestStanceEvidenceAndStall(t *testing.T) {
	_, svc, sup, _ := debateFixture(t)
	ctx := context.Background()
	if _, err := svc.SubmitStance(ctx, sup, "conv_d", "debate_1", 1, "研发方案 A", nil); err == nil {
		t.Fatal("缺 evidence_ids 应被拒绝")
	}
	if _, err := svc.SubmitStance(ctx, sup, "conv_d", "debate_1", 1, "研发方案 A", []string{"ev_1"}); err != nil {
		t.Fatalf("首次提交: %v", err)
	}
	if _, err := svc.SubmitStance(ctx, sup, "conv_d", "debate_1", 2, "研发方案 A", []string{"ev_1"}); err == nil {
		t.Fatal("重复立场且无新证据应被拒绝")
	}
	if !DetectStall([]Submission{{ActorID: "a", Stance: "A", EvidenceIDs: []string{"1"}}, {ActorID: "a", Stance: "A", EvidenceIDs: []string{"1"}}}) {
		t.Fatal("应检测到 stall")
	}
	if DetectStall([]Submission{{ActorID: "a", Stance: "A", EvidenceIDs: []string{"1"}}, {ActorID: "a", Stance: "A", EvidenceIDs: []string{"2"}}}) {
		t.Fatal("有新证据不应判 stall")
	}
	opened := time.Now().Add(-2 * DefaultDebateTimebox)
	if !TimeboxExceeded(opened, time.Now(), 0) {
		t.Fatal("时间盒应到期")
	}
}

func TestDecisionDissentSearchAndReopen(t *testing.T) {
	store, svc, sup, _ := debateFixture(t)
	ctx := context.Background()
	esc, err := svc.Escalate(ctx, sup, "conv_d", "debate_1", "task_1", EscalationL2, "接口分歧", "ev_1", "A/B", "建议 A", "不决策会延期")
	if err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if esc.Status != domain.DecisionProposed {
		t.Fatalf("escalation status = %q", esc.Status)
	}
	dec, err := svc.CloseDebate(ctx, sup, "debate_1", "登录限流方案", "采用令牌桶", "压测证据充分", "测试部保留异议：边界值未覆盖", "task_1", "", "", "若 QPS 超预期再评估")
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if !strings.Contains(dec.Dissent, "异议") {
		t.Fatalf("异议未保留: %+v", dec)
	}
	found, err := svc.SearchDecisions(ctx, "令牌桶")
	if err != nil || len(found) != 1 {
		t.Fatalf("search = %d err=%v", len(found), err)
	}
	byTask, err := svc.ListDecisionsByTask(ctx, "task_1")
	if err != nil || len(byTask) == 0 {
		t.Fatalf("task decisions = %d err=%v", len(byTask), err)
	}

	// 重开一次成功，第二次拒绝。
	reopened, err := svc.Reopen(ctx, sup, dec.ID, "新增压测证据")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	old, _ := store.GetDecision(ctx, dec.ID)
	if old.Status != domain.DecisionSuperseded || old.SupersededBy != reopened.ID {
		t.Fatalf("旧决策未 supersede: %+v", old)
	}
	if _, err := svc.Reopen(ctx, sup, reopened.ID, "再来一次"); err == nil {
		t.Fatal("第二次重开应被拒绝")
	}
	if view, err := svc.DecisionDetail(ctx, reopened.ID); err != nil || view.Reopened != 1 {
		t.Fatalf("detail = %+v err=%v", view, err)
	}
}

func TestAppendDissentImmutably(t *testing.T) {
	store, svc, sup, _ := debateFixture(t)
	ctx := context.Background()
	dec, err := svc.CloseDebate(ctx, sup, "d", "方案", "A", "理由", "", "", "", "", "")
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	next, err := svc.AppendDissent(ctx, sup, dec.ID, "补充异议：成本过高")
	if err != nil {
		t.Fatalf("dissent: %v", err)
	}
	if !strings.Contains(next.Dissent, "成本过高") || next.Supersedes != dec.ID {
		t.Fatalf("dissent = %+v", next)
	}
	if old, _ := store.GetDecision(ctx, dec.ID); old.Status != domain.DecisionSuperseded {
		t.Fatalf("旧记录应 superseded: %+v", old)
	}
}

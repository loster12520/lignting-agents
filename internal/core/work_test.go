package core

import (
	"context"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
)

func workFixture(t *testing.T) (*fakeWork, *WorkService, domain.Agent, domain.Agent, domain.Agent, domain.Agent) {
	t.Helper()
	store := newFakeWork()
	svc := NewWorkService(store)
	supervisor := domain.Agent{ID: "agent_sup", Name: "研发主管", Kind: domain.AgentKindSupervisor, DepartmentID: "d1"}
	owner := domain.Agent{ID: "agent_owner", Name: "小研", Kind: domain.AgentKindEmployee, DepartmentID: "d1"}
	reviewer := domain.Agent{ID: "agent_rev", Name: "小测", Kind: domain.AgentKindEmployee, DepartmentID: "d1"}
	approver := domain.Agent{ID: "agent_appr", Name: "测试主管", Kind: domain.AgentKindSupervisor, DepartmentID: "d1"}
	return store, svc, supervisor, owner, reviewer, approver
}

func baseTaskInput(owner, reviewer, approver domain.Agent, highRisk bool) CreateTaskInput {
	return CreateTaskInput{
		Title: "实现登录限流", DepartmentID: "d1", OwnerID: owner.ID, ReviewerID: reviewer.ID,
		ApproverID: approver.ID, Perspective: "接口", Priority: "P1", Deadline: "2026-12-01",
		BudgetTokens: 10000, BudgetCost: 1, Acceptance: []string{"单测通过", "压测 100 QPS 不超限"},
		HighRisk: highRisk,
	}
}

func TestTaskDoRAndIllegalTransition(t *testing.T) {
	_, svc, sup, owner, rev, appr := workFixture(t)
	ctx := context.Background()
	bad := baseTaskInput(owner, rev, appr, false)
	bad.Acceptance = nil
	if _, err := svc.CreateTask(ctx, sup, bad); err == nil {
		t.Fatal("缺验收标准应被 DoR 拒绝")
	}
	task, err := svc.CreateTask(ctx, sup, baseTaskInput(owner, rev, appr, false))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.Status != "proposed" {
		t.Fatalf("status = %q", task.Status)
	}
	if _, err := svc.TransitionTask(ctx, sup, task.ID, "done", "", ""); err == nil {
		t.Fatal("proposed→done 应被状态机拒绝")
	}
	view, err := svc.TaskView(ctx, task.ID)
	if err != nil || len(view.Criteria) != 2 || !view.Ready {
		t.Fatalf("view = %+v err=%v", view, err)
	}
}

func TestSelfApproveRejected(t *testing.T) {
	_, svc, sup, owner, rev, _ := workFixture(t)
	ctx := context.Background()
	task, err := svc.CreateTask(ctx, sup, baseTaskInput(owner, rev, domain.Agent{ID: "agent_appr"}, false))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, to := range []string{"ready", "discussing", "in_progress", "in_review"} {
		if _, err := svc.TransitionTask(ctx, sup, task.ID, to, "推进", ""); err != nil {
			t.Fatalf("transition %s: %v", to, err)
		}
	}
	if _, err := svc.TransitionTask(ctx, owner, task.ID, "approved", "自批", ""); err == nil {
		t.Fatal("作者不能批准自己的任务")
	}
	if _, err := svc.TransitionTask(ctx, rev, task.ID, "approved", "评审通过", ""); err != nil {
		t.Fatalf("reviewer 应可批准: %v", err)
	}
}

func TestG3EvidenceGateAndHumanApprove(t *testing.T) {
	store, svc, sup, owner, rev, appr := workFixture(t)
	ctx := context.Background()
	task, err := svc.CreateTask(ctx, sup, baseTaskInput(owner, rev, appr, true))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, to := range []string{"ready", "discussing", "in_progress", "in_review"} {
		if _, err := svc.TransitionTask(ctx, sup, task.ID, to, "推进", ""); err != nil {
			t.Fatalf("transition %s: %v", to, err)
		}
	}
	if _, err := svc.SubmitG3(ctx, sup, task.ID, "high", ""); err == nil {
		t.Fatal("缺证据必须拒绝 G3")
	}
	criteria, _ := store.ListTaskCriteria(ctx, task.ID)
	for _, c := range criteria {
		if err := svc.SetCriterionEvidence(ctx, owner, task.ID, c.ID, "unit test: ok"); err != nil {
			t.Fatalf("evidence: %v", err)
		}
		if err := svc.SetCriterionVerdict(ctx, sup, task.ID, c.ID, "approved"); err != nil {
			t.Fatalf("verdict: %v", err)
		}
	}
	apprRec, err := svc.SubmitG3(ctx, sup, task.ID, "high", "无")
	if err != nil {
		t.Fatalf("submit g3: %v", err)
	}
	if apprRec.Kind != domain.ApprovalG3 || apprRec.Status != domain.ApprovalPending {
		t.Fatalf("approval = %+v", apprRec)
	}
	if _, err := svc.DecideG3(ctx, apprRec.ID, domain.ApprovalApproved, "owner", "通过"); err != nil {
		t.Fatalf("decide g3: %v", err)
	}
	if _, err := svc.TransitionTask(ctx, rev, task.ID, "approved", "评审通过", ""); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := svc.TransitionTask(ctx, appr, task.ID, "done", "G3 通过", ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	taskAfter, _ := store.GetTask(ctx, task.ID)
	if taskAfter.Status != "done" {
		t.Fatalf("task status = %q", taskAfter.Status)
	}
}

func TestReviewReworkCreatesNewVersion(t *testing.T) {
	store, svc, _, owner, rev, _ := workFixture(t)
	ctx := context.Background()
	task, err := svc.CreateTask(ctx, domain.Agent{ID: "agent_sup", Kind: domain.AgentKindSupervisor, DepartmentID: "d1"}, baseTaskInput(owner, rev, domain.Agent{ID: "agent_appr"}, false))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	_, v1, err := svc.CreateArtifact(ctx, owner, ArtifactInput{TaskID: task.ID, Type: "document", Title: "接口方案", Content: "v1: GET /login", DepartmentID: "d1"})
	if err != nil {
		t.Fatalf("artifact: %v", err)
	}
	review, err := svc.CreateReview(ctx, rev, ReviewInput{
		ArtifactID: v1.ArtifactID, Severity: domain.SeverityMajor,
		Claim: "缺少限流证据", Evidence: "test: missing", FromDepartment: "qa", ToDepartment: "engineering",
	})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if _, err := svc.SubmitVerdict(ctx, rev, review.ID, domain.VerdictRequestChanges, "checked", "需补限流", "补限流实现", ""); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	v2, err := svc.Rework(ctx, owner, review.ID, "v2: GET /login with rate limit")
	if err != nil {
		t.Fatalf("rework: %v", err)
	}
	if v2.Version != 2 || v2.ContentHash == v1.ContentHash {
		t.Fatalf("rework version = %+v", v2)
	}
	versions, _ := store.ListArtifactVersions(ctx, v1.ArtifactID)
	if len(versions) != 2 {
		t.Fatalf("versions = %d", len(versions))
	}
	updated, _ := store.GetReview(ctx, review.ID)
	if updated.Status != "responded" {
		t.Fatalf("review status = %q", updated.Status)
	}
}

func TestCycleDetectionAndWIP(t *testing.T) {
	// a 依赖 c，c 依赖 b，b 依赖 a → 成环。
	if cycle := WouldCreateCycle(map[string][]string{"b": {"a"}, "c": {"b"}}, "a", "c"); len(cycle) == 0 {
		t.Fatal("应检测到环")
	}
	if cycle := WouldCreateCycle(map[string][]string{"a": {"b"}, "c": {}}, "a", "c"); len(cycle) != 0 {
		t.Fatalf("不应误报: %v", cycle)
	}

	_, svc, sup, owner, rev, appr := workFixture(t)
	svc = svc.WithWIPLimit(1)
	ctx := context.Background()
	t1, err := svc.CreateTask(ctx, sup, baseTaskInput(owner, rev, appr, false))
	if err != nil {
		t.Fatalf("t1: %v", err)
	}
	for _, to := range []string{"ready", "discussing", "in_progress"} {
		if _, err := svc.TransitionTask(ctx, sup, t1.ID, to, "", ""); err != nil {
			t.Fatalf("t1 %s: %v", to, err)
		}
	}
	t2, err := svc.CreateTask(ctx, sup, baseTaskInput(owner, rev, appr, false))
	if err != nil {
		t.Fatalf("t2: %v", err)
	}
	for _, to := range []string{"ready", "discussing"} {
		if _, err := svc.TransitionTask(ctx, sup, t2.ID, to, "", ""); err != nil {
			t.Fatalf("t2 %s: %v", to, err)
		}
	}
	if _, err := svc.TransitionTask(ctx, sup, t2.ID, "in_progress", "", ""); err == nil || !strings.Contains(err.Error(), "WIP") {
		t.Fatalf("应触发 WIP 上限: %v", err)
	}
}

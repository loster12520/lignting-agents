package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

func TestR4TaskCriteriaDependencyAndEventChain(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	dept, err := st.CreateDepartment(ctx, domain.Department{Name: "研发部", Kind: "engineering"})
	if err != nil {
		t.Fatalf("dept: %v", err)
	}
	task, err := st.CreateTask(ctx, domain.Task{Title: "实现登录", DepartmentID: dept.ID, OwnerID: "agent_1"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.ID == "" || task.Status != "proposed" || task.Priority != "P1" {
		t.Fatalf("task defaults = %+v", task)
	}
	task.Status = "in_progress"
	if err := st.UpdateTask(ctx, task); err != nil {
		t.Fatalf("update task: %v", err)
	}
	if got, err := st.GetTask(ctx, task.ID); err != nil || got.Status != "in_progress" {
		t.Fatalf("get task = %+v err=%v", got, err)
	}
	if list, err := st.ListTasksByDepartment(ctx, dept.ID); err != nil || len(list) != 1 {
		t.Fatalf("list by dept = %d err=%v", len(list), err)
	}
	if list, err := st.ListTasksByOwnerStatus(ctx, "agent_1", "in_progress"); err != nil || len(list) != 1 {
		t.Fatalf("list by owner/status = %d err=%v", len(list), err)
	}

	c1, err := st.CreateTaskCriterion(ctx, domain.Criterion{TaskID: task.ID, Ord: 1, Text: "有测试"})
	if err != nil {
		t.Fatalf("criterion1: %v", err)
	}
	if _, err := st.CreateTaskCriterion(ctx, domain.Criterion{TaskID: task.ID, Ord: 2, Text: "有日志"}); err != nil {
		t.Fatalf("criterion2: %v", err)
	}
	crits, err := st.ListTaskCriteria(ctx, task.ID)
	if err != nil || len(crits) != 2 || crits[0].Ord != 1 {
		t.Fatalf("criteria = %+v err=%v", crits, err)
	}
	c1.Evidence = `["log-1"]`
	c1.SupervisorVerdict = "pass"
	if err := st.UpdateTaskCriterion(ctx, c1); err != nil {
		t.Fatalf("update criterion: %v", err)
	}

	if err := st.AddTaskDependency(ctx, task.ID, "task_dep_1"); err != nil {
		t.Fatalf("dep1: %v", err)
	}
	if err := st.AddTaskDependency(ctx, task.ID, "task_dep_1"); err != nil {
		t.Fatalf("duplicate dep should be idempotent: %v", err)
	}
	if err := st.AddTaskDependency(ctx, task.ID, "task_dep_2"); err != nil {
		t.Fatalf("dep2: %v", err)
	}
	if err := st.AddTaskDependency(ctx, task.ID, task.ID); err == nil {
		t.Fatal("self dependency should fail")
	}
	deps, err := st.ListTaskDependencies(ctx, task.ID)
	if err != nil || len(deps) != 2 {
		t.Fatalf("deps = %v err=%v", deps, err)
	}

	e1, err := st.AppendTaskEvent(ctx, domain.TaskEvent{TaskID: task.ID, Type: "transition", FromStatus: "ready", ToStatus: "in_progress", ActorType: "agent", ActorID: "agent_1", Reason: "start"})
	if err != nil {
		t.Fatalf("event1: %v", err)
	}
	e2, err := st.AppendTaskEvent(ctx, domain.TaskEvent{TaskID: task.ID, Type: "transition", FromStatus: "in_progress", ToStatus: "in_review", ActorType: "agent", ActorID: "agent_1"})
	if err != nil {
		t.Fatalf("event2: %v", err)
	}
	if e1.Seq <= 0 || e2.Seq <= e1.Seq {
		t.Fatalf("event seqs = %d,%d", e1.Seq, e2.Seq)
	}
	if e1.PrevHash != "" || e2.PrevHash != e1.Hash {
		t.Fatalf("chain broken: e1.prev=%q e1.hash=%q e2.prev=%q", e1.PrevHash, e1.Hash, e2.PrevHash)
	}
	if e1.Hash == "" || e1.CreatedAt == "" || e1.Evidence != "[]" {
		t.Fatalf("event1 normalization = %+v", e1)
	}
}

func TestR4ArtifactsReviewsDecisions(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	art, err := st.CreateArtifact(ctx, domain.Artifact{Type: "prd", Title: "PRD"})
	if err != nil {
		t.Fatalf("artifact: %v", err)
	}
	if _, err := st.CreateArtifactVersion(ctx, domain.ArtifactVersion{ArtifactID: art.ID, Version: 1, Content: "v1 内容", AuthorID: "agent_1"}); err != nil {
		t.Fatalf("version1: %v", err)
	}
	v2, err := st.CreateArtifactVersion(ctx, domain.ArtifactVersion{ArtifactID: art.ID, Version: 2, Content: "v2 内容", AuthorID: "agent_1"})
	if err != nil || v2.ContentHash == "" {
		t.Fatalf("version2 = %+v err=%v", v2, err)
	}
	if _, err := st.CreateArtifactVersion(ctx, domain.ArtifactVersion{ArtifactID: art.ID, Version: 2, Content: "另一内容", AuthorID: "agent_1"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate version err = %v, want conflict", err)
	}
	if _, err := st.CreateArtifactVersion(ctx, domain.ArtifactVersion{ArtifactID: art.ID, Version: 3, Content: "v1 内容", AuthorID: "agent_1"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate content hash err = %v, want conflict", err)
	}
	if v, err := st.GetArtifactVersion(ctx, art.ID, 2); err != nil || v.Content != "v2 内容" {
		t.Fatalf("get version = %+v err=%v", v, err)
	}
	if vs, err := st.ListArtifactVersions(ctx, art.ID); err != nil || len(vs) != 2 || vs[0].Version != 2 {
		t.Fatalf("versions = %v err=%v", vs, err)
	}
	if err := st.UpdateArtifactStatus(ctx, art.ID, "final"); err != nil {
		t.Fatalf("artifact status: %v", err)
	}

	rev, err := st.CreateReview(ctx, domain.Review{Claim: "缺少限流", ArtifactID: art.ID, ArtifactVersionID: v2.ID, Severity: domain.SeverityBlocker})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if rev.Status != "open" {
		t.Fatalf("review status = %s", rev.Status)
	}
	if rs, err := st.ListReviewsByArtifact(ctx, art.ID); err != nil || len(rs) != 1 {
		t.Fatalf("reviews = %v err=%v", rs, err)
	}
	if _, err := st.CreateReviewVerdict(ctx, domain.ReviewVerdict{ReviewID: rev.ID, Verdict: domain.VerdictRequestChanges, ReviewerID: "owner", Rationale: "补限流"}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	if vs, err := st.ListReviewVerdicts(ctx, rev.ID); err != nil || len(vs) != 1 {
		t.Fatalf("verdicts = %v err=%v", vs, err)
	}

	d1, err := st.CreateDecision(ctx, domain.Decision{
		Title: "不用 GraphQL", Context: "接口评审", Decision: "REST + OpenAPI",
		AffectedTasks: `["task_1","task_2"]`, CreatedBy: "owner",
	})
	if err != nil {
		t.Fatalf("decision: %v", err)
	}
	if d1.Status != domain.DecisionProposed {
		t.Fatalf("decision status = %s", d1.Status)
	}
	if ds, err := st.ListDecisionsByTask(ctx, "task_2"); err != nil || len(ds) != 1 {
		t.Fatalf("decisions by task = %v err=%v", ds, err)
	}
	d2, err := st.CreateDecision(ctx, domain.Decision{Title: "改用 tRPC", Decision: "tRPC", CreatedBy: "owner"})
	if err != nil {
		t.Fatalf("decision2: %v", err)
	}
	if err := st.SupersedeDecision(ctx, d1.ID, d2.ID); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	got, err := st.GetDecision(ctx, d1.ID)
	if err != nil || got.Status != domain.DecisionSuperseded || got.SupersededBy != d2.ID || got.Decision != "REST + OpenAPI" {
		t.Fatalf("superseded decision = %+v err=%v", got, err)
	}
	if ds, err := st.ListDecisionsByStatus(ctx, domain.DecisionSuperseded); err != nil || len(ds) != 1 {
		t.Fatalf("decisions by status = %v err=%v", ds, err)
	}
}

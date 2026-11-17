package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
)

func gateApprovalID(t *testing.T, store *fakeWork, taskID, flagKey string) string {
	t.Helper()
	task, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	var flags map[string]any
	if err := json.Unmarshal([]byte(task.Flags), &flags); err != nil {
		return ""
	}
	id, _ := flags[flagKey].(string)
	return id
}

func TestHighRiskRequiresG2(t *testing.T) {
	store, svc, sup, owner, rev, appr := workFixture(t)
	ctx := context.Background()
	task, err := svc.CreateTask(ctx, sup, baseTaskInput(owner, rev, appr, true))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	g2 := gateApprovalID(t, store, task.ID, "g2_approval_id")
	if g2 == "" {
		t.Fatal("高风险任务创建时应生成 G2 pending")
	}
	for _, to := range []string{"ready", "discussing"} {
		if _, err := svc.TransitionTask(ctx, sup, task.ID, to, "推进", ""); err != nil {
			t.Fatalf("transition %s: %v", to, err)
		}
	}
	_, err = svc.TransitionTask(ctx, sup, task.ID, "in_progress", "开工", "")
	if err == nil || !strings.Contains(err.Error(), "G2") {
		t.Fatalf("无 G2 不应允许开工: %v", err)
	}
	// 审批 G2 后放行。
	if _, err := svc.DecideGate(ctx, g2, domain.ApprovalApproved, "owner", "方案通过"); err != nil {
		t.Fatalf("decide g2: %v", err)
	}
	if _, err := svc.TransitionTask(ctx, sup, task.ID, "in_progress", "开工", ""); err != nil {
		t.Fatalf("G2 批准后应可开工: %v", err)
	}
}

func TestDoneRequiresG3ForNormalTask(t *testing.T) {
	store, svc, sup, owner, rev, appr := workFixture(t)
	ctx := context.Background()
	task, err := svc.CreateTask(ctx, sup, baseTaskInput(owner, rev, appr, false))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if gateApprovalID(t, store, task.ID, "g2_approval_id") != "" {
		t.Fatal("非高风险任务不应创建 G2")
	}
	for _, to := range []string{"ready", "discussing", "in_progress", "in_review"} {
		if _, err := svc.TransitionTask(ctx, sup, task.ID, to, "推进", ""); err != nil {
			t.Fatalf("transition %s: %v", to, err)
		}
	}
	if _, err := svc.TransitionTask(ctx, rev, task.ID, "approved", "评审通过", ""); err != nil {
		t.Fatalf("approve: %v", err)
	}
	_, err = svc.TransitionTask(ctx, appr, task.ID, "done", "交付", "")
	if err == nil || !strings.Contains(err.Error(), "G3") {
		t.Fatalf("无 G3 不应允许 done: %v", err)
	}
	// 提交 G3 并批准后放行。
	criteria, _ := store.ListTaskCriteria(ctx, task.ID)
	for _, c := range criteria {
		if err := svc.SetCriterionEvidence(ctx, owner, task.ID, c.ID, "test: ok"); err != nil {
			t.Fatalf("evidence: %v", err)
		}
		if err := svc.SetCriterionVerdict(ctx, sup, task.ID, c.ID, "approved"); err != nil {
			t.Fatalf("verdict: %v", err)
		}
	}
	g3, err := svc.SubmitG3(ctx, sup, task.ID, "medium", "")
	if err != nil {
		t.Fatalf("submit g3: %v", err)
	}
	if _, err := svc.DecideG3(ctx, g3.ID, domain.ApprovalApproved, "owner", "通过"); err != nil {
		t.Fatalf("decide g3: %v", err)
	}
	if _, err := svc.TransitionTask(ctx, appr, task.ID, "done", "G3 通过", ""); err != nil {
		t.Fatalf("G3 批准后应可 done: %v", err)
	}
}

func TestDoneRequiresG3ForHighRiskToo(t *testing.T) {
	store, svc, sup, owner, rev, appr := workFixture(t)
	ctx := context.Background()
	task, err := svc.CreateTask(ctx, sup, baseTaskInput(owner, rev, appr, true))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	g2 := gateApprovalID(t, store, task.ID, "g2_approval_id")
	if _, err := svc.DecideGate(ctx, g2, domain.ApprovalApproved, "owner", "G2"); err != nil {
		t.Fatalf("g2: %v", err)
	}
	for _, to := range []string{"ready", "discussing", "in_progress", "in_review"} {
		if _, err := svc.TransitionTask(ctx, sup, task.ID, to, "推进", ""); err != nil {
			t.Fatalf("transition %s: %v", to, err)
		}
	}
	if _, err := svc.TransitionTask(ctx, rev, task.ID, "approved", "通过", ""); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := svc.TransitionTask(ctx, appr, task.ID, "done", "交付", ""); err == nil {
		t.Fatal("高风险任务无 G3 不应 done")
	}
}

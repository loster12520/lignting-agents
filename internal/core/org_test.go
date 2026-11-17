package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/deepseek"
	"github.com/lignting/lignting-agents/internal/domain"
)

func orgFixture(t *testing.T) (*fakeStore, domain.Agent, *deepseek.Mock, *Runtime, *OrgService) {
	t.Helper()
	store := newFakeStore()
	assistant := domain.Agent{ID: "agent_assistant", Name: "阿秘书", Kind: domain.AgentKindAssistant, Status: domain.AgentIdle}
	store.seedAgent(assistant)
	mock := deepseek.NewMock()
	rt, err := NewRuntime(Options{Store: store, Provider: mock, OwnerName: "阿哲", TurnTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(rt.Close)
	return store, assistant, mock, rt, NewOrgService(store, rt)
}

func TestOrgTemplates(t *testing.T) {
	kinds := TemplateKinds()
	want := map[string]bool{"product": true, "engineering": true, "qa": true, "platform": true, "pm": true}
	if len(kinds) < 4 {
		t.Fatalf("templates = %v", kinds)
	}
	for _, k := range kinds {
		if !want[k] {
			t.Fatalf("未知模板 %q", k)
		}
	}
	eng, ok := TemplateByKind("engineering")
	if !ok || len(eng.Roles) < 3 {
		t.Fatalf("研发模板角色不足: %+v", eng)
	}
	sup := eng.Roles[0]
	if sup.Kind != domain.AgentKindSupervisor || len(sup.Tools) != 5 || sup.MemoryScope != "team" {
		t.Fatalf("角色卡不符合要求: %+v", sup)
	}
	if !strings.Contains(eng.CharterJSON(), "quality_gates") {
		t.Fatal("章程缺少质量门")
	}
	if _, err := BuildBlueprint("做知识库", "lean"); err != nil {
		t.Fatalf("lean 蓝图: %v", err)
	}
	if _, err := BuildBlueprint("做知识库", "weird"); err == nil {
		t.Fatal("未知方案应报错")
	}
}

func TestBlueprintG1AndIdempotentProvisioning(t *testing.T) {
	store, assistant, _, _, svc := orgFixture(t)
	ctx := context.Background()
	bp, appr, err := svc.ProposeBlueprint(ctx, assistant, "intent_1", "做一个本地知识库", "full")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if bp.Status != domain.BlueprintProposed || appr.Kind != domain.ApprovalG1 || appr.Status != domain.ApprovalPending {
		t.Fatalf("bp/appr = %+v / %+v", bp, appr)
	}
	if _, err := svc.Provision(ctx, bp.ID, "owner"); err == nil {
		t.Fatal("G1 未批准时不应允许 provisioning")
	}
	self, err := svc.ApproveBlueprint(ctx, bp.ID, "owner", "同意")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if self.Status != domain.BlueprintApproved || self.ApprovedAt == "" {
		t.Fatalf("approved bp = %+v", self)
	}
	// 再批准一次是幂等的。
	if _, err := svc.ApproveBlueprint(ctx, bp.ID, "owner", "再次确认"); err != nil {
		t.Fatalf("幂等批准: %v", err)
	}

	res, err := svc.Provision(ctx, bp.ID, "owner")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if res.Provisioning.Status != domain.ProvisionVerified {
		t.Fatalf("status = %q", res.Provisioning.Status)
	}
	if got := len(store.depts); got != 4 {
		t.Fatalf("dedepartments = %d, want 4", got)
	}
	agents := 0
	for range store.agents {
		agents++
	}
	if agents < 8 {
		t.Fatalf("agents = %d", agents)
	}
	if len(store.convs) != 4 {
		t.Fatalf("convs = %d, want 4", len(store.convs))
	}

	chart, err := svc.OrgChart(ctx)
	if err != nil {
		t.Fatalf("org chart: %v", err)
	}
	if len(chart.Departments) != 4 {
		t.Fatalf("chart departments = %d", len(chart.Departments))
	}
	for _, d := range chart.Departments {
		if d.Supervisor.ID == "" {
			t.Fatalf("部门 %s 缺少主管", d.ID)
		}
	}

	// 重复 provisioning 不产生重复部门/员工。
	beforeDepts := len(store.depts)
	beforeAgents := len(store.agents)
	res2, err := svc.Provision(ctx, bp.ID, "owner")
	if err != nil {
		t.Fatalf("重复 provision: %v", err)
	}
	if res2.Provisioning.ID != res.Provisioning.ID || res2.Provisioning.Status != domain.ProvisionVerified {
		t.Fatalf("重复 provisioning 结果不一致: %+v", res2.Provisioning)
	}
	if len(store.depts) != beforeDepts || len(store.agents) != beforeAgents {
		t.Fatalf("重复 provision 产生了重复: depts %d->%d agents %d->%d", beforeDepts, len(store.depts), beforeAgents, len(store.agents))
	}
}

func TestProvisionRollbackAndRetry(t *testing.T) {
	store, assistant, _, _, svc := orgFixture(t)
	ctx := context.Background()
	bp, _, err := svc.ProposeBlueprint(ctx, assistant, "intent_1", "做一个本地知识库", "lean")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := svc.ApproveBlueprint(ctx, bp.ID, "owner", "同意"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	orgState(store).failAgentAt = 3

	res, err := svc.Provision(ctx, bp.ID, "owner")
	if err == nil {
		t.Fatal("注入失败后 provisioning 应返回错误")
	}
	if res.Provisioning.Status != domain.ProvisionRolledBack {
		t.Fatalf("status = %q", res.Provisioning.Status)
	}
	if len(res.Orphans.Departments) != 3 || len(res.Orphans.Agents) != 2 {
		t.Fatalf("orphans = %+v", res.Orphans)
	}
	for id, d := range store.depts {
		if d.Status != domain.DeptArchived {
			t.Fatalf("回滚后部门 %s 状态 = %s", id, d.Status)
		}
	}
	paused := 0
	for _, a := range store.agents {
		if a.Status == domain.AgentPaused {
			paused++
		}
	}
	if paused != 2 {
		t.Fatalf("回滚后暂停 Agent = %d, want 2", paused)
	}

	orgState(store).failAgentAt = 0
	res, err = svc.Provision(ctx, bp.ID, "owner")
	if err != nil {
		t.Fatalf("重试 provision: %v", err)
	}
	if res.Provisioning.Status != domain.ProvisionVerified {
		t.Fatalf("重试后 status = %q", res.Provisioning.Status)
	}
	if len(store.depts) != 3 {
		t.Fatalf("重试产生重复部门: %d", len(store.depts))
	}
	for _, d := range store.depts {
		if d.Status != domain.DeptActive {
			t.Fatalf("重试后部门 %s 状态 = %s", d.ID, d.Status)
		}
	}
	for _, a := range store.agents {
		if a.Status != domain.AgentIdle {
			t.Fatalf("重试后 Agent %s 状态 = %s", a.ID, a.Status)
		}
	}
}

func TestLifecyclePauseSkipsAgent(t *testing.T) {
	store, assistant, _, rt, svc := orgFixture(t)
	ctx := context.Background()
	conv := domain.Conversation{ID: "conv_dm", Type: domain.ConvDM, Title: "与助理的私聊", Visibility: domain.VisibilityPrivate}
	store.seedConversation(conv)
	store.seedMember(domain.ConversationMember{ConversationID: conv.ID, PrincipalType: "agent", PrincipalID: assistant.ID})
	userMsg := store.seedMessage(domain.Message{ID: "msg_u1", ConversationID: conv.ID, SenderType: domain.SenderUser, SenderID: "owner", SenderName: "阿哲", Type: domain.MsgText, Content: "在吗"})

	if err := svc.PauseAgent(ctx, assistant.ID, "owner", "休息"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	before := len(store.snapshotMessages())
	if err := rt.EnqueueUserMessage(ctx, userMsg); err == nil {
		t.Fatal("暂停的 Agent 不应被触发")
	}
	_ = rt.WaitIdle(ctx, assistant.ID)
	if len(store.snapshotMessages()) != before {
		t.Fatal("暂停期间不应产生新消息")
	}
	if err := svc.ResumeAgent(ctx, assistant.ID, "owner"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if err := rt.EnqueueUserMessage(ctx, userMsg); err != nil {
		t.Fatalf("resume 后 enqueue: %v", err)
	}
	if err := rt.WaitIdle(ctx, assistant.ID); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if len(store.snapshotMessages()) == before {
		t.Fatal("resume 后应有回复")
	}

	// 部门暂停：部门内员工不再被触发。
	dept := domain.Department{ID: "dept_r", Name: "研发部", Kind: "engineering", Status: domain.DeptActive}
	store.seedDepartment(dept)
	emp := domain.Agent{ID: "agent_emp", Name: "小研", Kind: domain.AgentKindEmployee, DepartmentID: dept.ID, Status: domain.AgentIdle}
	store.seedAgent(emp)
	dconv := domain.Conversation{ID: "conv_d", Type: domain.ConvDepartment, DepartmentID: dept.ID, Title: "研发部"}
	store.seedConversation(dconv)
	store.seedMember(domain.ConversationMember{ConversationID: dconv.ID, PrincipalType: "agent", PrincipalID: emp.ID})
	dmsg := store.seedMessage(domain.Message{ID: "msg_d1", ConversationID: dconv.ID, SenderType: domain.SenderUser, SenderID: "owner", SenderName: "阿哲", Type: domain.MsgText, Content: "开工"})

	if err := svc.PauseDepartment(ctx, dept.ID, "owner", "暂停"); err != nil {
		t.Fatalf("pause dept: %v", err)
	}
	before = len(store.snapshotMessages())
	if err := rt.EnqueueUserMessage(ctx, dmsg); err == nil {
		t.Fatal("暂停部门不应触发员工")
	}
	_ = rt.WaitIdle(ctx, emp.ID)
	if len(store.snapshotMessages()) != before {
		t.Fatal("暂停部门期间不应产生新消息")
	}
	if err := svc.ResumeDepartment(ctx, dept.ID, "owner"); err != nil {
		t.Fatalf("resume dept: %v", err)
	}
	if err := rt.EnqueueUserMessage(ctx, dmsg); err != nil {
		t.Fatalf("resume dept 后 enqueue: %v", err)
	}
	if err := rt.WaitIdle(ctx, emp.ID); err != nil {
		t.Fatalf("wait emp: %v", err)
	}
	if len(store.snapshotMessages()) == before {
		t.Fatal("resume 部门后应有回复")
	}
}

func TestStopAgent(t *testing.T) {
	store, assistant, _, _, _ := orgFixture(t)
	provider := &gateProvider{started: make(chan int, 4), release: make(chan struct{})}
	rt, err := NewRuntime(Options{Store: store, Provider: provider, TurnTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(rt.Close)
	svc := NewOrgService(store, rt)
	conv := domain.Conversation{ID: "conv_stop", Type: domain.ConvDM, Title: "私聊"}
	store.seedConversation(conv)
	store.seedMember(domain.ConversationMember{ConversationID: conv.ID, PrincipalType: "agent", PrincipalID: assistant.ID})
	msg := store.seedMessage(domain.Message{ID: "msg_stop", ConversationID: conv.ID, SenderType: domain.SenderUser, SenderID: "owner", SenderName: "阿哲", Type: domain.MsgText, Content: "跑个长任务"})
	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-provider.started
	if err := svc.StopAgent(ctx, assistant.ID, "owner"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := rt.WaitIdle(ctx, assistant.ID); err != nil {
		t.Fatalf("wait: %v", err)
	}
	turns := store.snapshotTurns()
	if len(turns) != 1 || turns[0].Status != "interrupted" {
		t.Fatalf("turns = %+v", turns)
	}
}

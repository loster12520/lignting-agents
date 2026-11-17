package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/deepseek"
	"github.com/lignting/lignting-agents/internal/domain"
)

func hasEventType(events []domain.Event, typ string) bool {
	for _, ev := range events {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

func lastMessage(t *testing.T, store *fakeStore) domain.Message {
	t.Helper()
	msgs := store.snapshotMessages()
	if len(msgs) == 0 {
		t.Fatal("没有消息")
	}
	return msgs[len(msgs)-1]
}

func newDMFixture(t *testing.T) (*fakeStore, domain.Agent, domain.Conversation, domain.Message) {
	t.Helper()
	store := newFakeStore()
	assistant := domain.Agent{ID: "agent_assistant", Name: "阿秘书", Kind: domain.AgentKindAssistant, Status: domain.AgentIdle, Model: "deepseek-flash"}
	store.seedAgent(assistant)
	conv := domain.Conversation{ID: "conv_dm", Type: domain.ConvDM, Title: "与助理的私聊", Visibility: domain.VisibilityPrivate}
	store.seedConversation(conv)
	store.seedMember(domain.ConversationMember{ConversationID: conv.ID, PrincipalType: "agent", PrincipalID: assistant.ID, Role: "member"})
	userMsg := store.seedMessage(domain.Message{
		ID: "msg_u1", ConversationID: conv.ID, SenderType: domain.SenderUser,
		SenderID: "owner", SenderName: "阿哲", Type: domain.MsgText, Content: "我想做一个本地知识库",
	})
	return store, assistant, conv, userMsg
}

func TestMockDMAssistantRound(t *testing.T) {
	store, assistant, _, userMsg := newDMFixture(t)
	mock := deepseek.NewMock()
	mock.Script(deepseek.Script{
		Content: "好的，我先确认三个问题：目标、必须有的功能、预算档位。",
		Usage:   deepseek.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
	})
	rt, err := NewRuntime(Options{Store: store, Provider: mock, OwnerName: "阿哲", TurnTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer rt.Close()

	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, userMsg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := rt.WaitIdle(ctx, assistant.ID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}

	msgs := store.snapshotMessages()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2: %+v", len(msgs), msgs)
	}
	reply := lastMessage(t, store)
	if reply.SenderType != domain.SenderAgent || reply.SenderID != assistant.ID {
		t.Fatalf("reply sender = %+v", reply)
	}
	if !strings.Contains(reply.Content, "三个问题") {
		t.Fatalf("reply content = %q", reply.Content)
	}
	events := store.snapshotEvents()
	for _, typ := range []string{"message.delta", "message.completed", "agent.status", "turn.completed"} {
		if !hasEventType(events, typ) {
			t.Fatalf("缺少事件 %s", typ)
		}
	}
	turns := store.snapshotTurns()
	if len(turns) != 1 || turns[0].Status != "completed" {
		t.Fatalf("turns = %+v", turns)
	}
	agent, _ := store.GetAgent(ctx, assistant.ID)
	if agent.Status != domain.AgentIdle {
		t.Fatalf("agent status = %q", agent.Status)
	}
	store.mu.Lock()
	snapshots := len(store.snapshots)
	usages := len(store.usages)
	intents := len(store.intents)
	store.mu.Unlock()
	if snapshots != 1 || usages != 1 || intents != 0 {
		t.Fatalf("snapshots=%d usages=%d intents=%d", snapshots, usages, intents)
	}
}

func TestNoAPIKeyDegrades(t *testing.T) {
	store, assistant, _, userMsg := newDMFixture(t)
	provider := deepseek.New(deepseek.Config{APIKey: ""})
	rt, err := NewRuntime(Options{Store: store, Provider: provider, TurnTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer rt.Close()
	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, userMsg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := rt.WaitIdle(ctx, assistant.ID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	reply := lastMessage(t, store)
	if reply.SenderID != assistant.ID || !strings.Contains(reply.Content, "DeepSeek API Key") {
		t.Fatalf("降级消息 = %q", reply.Content)
	}
	turns := store.snapshotTurns()
	if len(turns) != 1 || turns[0].Status != "failed" {
		t.Fatalf("turns = %+v", turns)
	}
	agent, _ := store.GetAgent(ctx, assistant.ID)
	if agent.Status != domain.AgentFailed {
		t.Fatalf("agent status = %q", agent.Status)
	}
	if !hasEventType(store.snapshotEvents(), "turn.failed") {
		t.Fatal("缺少 turn.failed 事件")
	}
}

func TestToolLoop(t *testing.T) {
	store, assistant, _, userMsg := newDMFixture(t)
	args, _ := json.Marshal(map[string]any{"path": "a.txt"})
	mock := deepseek.NewMock()
	mock.Script(deepseek.Script{ToolCalls: []deepseek.ToolCall{{
		ID: "call_1", Type: "function",
		Function: deepseek.FunctionCall{Name: "read_file", Arguments: string(args)},
	}}})
	mock.Script(deepseek.Script{Content: "文件内容是 hello"})
	exec := &fakeExecutor{result: ToolResult{Content: "hello"}}
	rt, err := NewRuntime(Options{Store: store, Provider: mock, Tools: NewToolRegistry(exec), TurnTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer rt.Close()
	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, userMsg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := rt.WaitIdle(ctx, assistant.ID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	if mock.CallCount() != 2 {
		t.Fatalf("mock calls = %d, want 2", mock.CallCount())
	}
	reqs := mock.Requests()
	foundTool := false
	for _, m := range reqs[1].Messages {
		if m.Role == deepseek.RoleTool && m.Content == "hello" && m.ToolCallID == "call_1" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatalf("第二次请求缺少 tool 结果: %+v", reqs[1].Messages)
	}
	reply := lastMessage(t, store)
	if !strings.Contains(reply.Content, "hello") {
		t.Fatalf("reply = %q", reply.Content)
	}
	if len(exec.calls) != 1 || exec.calls[0].Name != "read_file" {
		t.Fatalf("executor calls = %+v", exec.calls)
	}
}

func TestActionSaveIntentExecuted(t *testing.T) {
	store, assistant, _, userMsg := newDMFixture(t)
	reply := "草稿已整理。\n\n" + fencedAction(t, "save_intent", map[string]any{
		"content": map[string]any{"goal": "做本地知识库"},
	})
	mock := deepseek.NewMock()
	mock.Script(deepseek.Script{Content: reply})
	rt, err := NewRuntime(Options{Store: store, Provider: mock, TurnTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer rt.Close()
	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, userMsg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := rt.WaitIdle(ctx, assistant.ID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	store.mu.Lock()
	intents := append([]string(nil), store.intents...)
	store.mu.Unlock()
	if len(intents) != 1 || !strings.Contains(intents[0], "做本地知识库") {
		t.Fatalf("intents = %+v", intents)
	}
	replyMsg := lastMessage(t, store)
	if !strings.Contains(replyMsg.Content, "已保存项目意图卡 v1") {
		t.Fatalf("reply = %q", replyMsg.Content)
	}
	if strings.Contains(replyMsg.Content, "lignting-action") {
		t.Fatalf("动作块应被从正文移除: %q", replyMsg.Content)
	}
	for _, m := range store.snapshotMessages() {
		if m.SenderType == domain.SenderSystem && strings.Contains(m.Content, "被拒绝") {
			t.Fatalf("不应有拒绝消息: %q", m.Content)
		}
	}
}

func TestActionRejectedByPolicy(t *testing.T) {
	store := newFakeStore()
	employee := domain.Agent{ID: "agent_emp", Name: "小研", Kind: domain.AgentKindEmployee, DepartmentID: "dept_r", Status: domain.AgentIdle}
	store.seedAgent(employee)
	store.seedDepartment(domain.Department{ID: "dept_r", Name: "研发部", Kind: "rd"})
	conv := domain.Conversation{ID: "conv_d", Type: domain.ConvDepartment, DepartmentID: "dept_r", Title: "研发部", Visibility: domain.VisibilityDepartment}
	store.seedConversation(conv)
	store.seedMember(domain.ConversationMember{ConversationID: conv.ID, PrincipalType: "agent", PrincipalID: employee.ID})
	userMsg := store.seedMessage(domain.Message{ID: "msg_u2", ConversationID: conv.ID, SenderType: domain.SenderUser, SenderID: "owner", SenderName: "阿哲", Type: domain.MsgText, Content: "给我建个任务"})

	mock := deepseek.NewMock()
	mock.Script(deepseek.Script{Content: "我来建。\n\n" + fencedAction(t, "create_task", map[string]any{
		"title": "x", "department_id": "dept_r", "perspective": "接口", "acceptance": []string{"a"},
	})})
	rt, err := NewRuntime(Options{Store: store, Provider: mock, TurnTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer rt.Close()
	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, userMsg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := rt.WaitIdle(ctx, employee.ID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	foundReject := false
	for _, m := range store.snapshotMessages() {
		if m.SenderType == domain.SenderSystem && strings.Contains(m.Content, "create_task") && strings.Contains(m.Content, "被拒绝") {
			foundReject = true
		}
	}
	if !foundReject {
		t.Fatalf("缺少拒绝说明: %+v", store.snapshotMessages())
	}
}

func TestPolicySupervisorTransition(t *testing.T) {
	p := NewPolicy(nil)
	ctx := context.Background()
	task := domain.Task{ID: "task_1", DepartmentID: "dept_r", Status: "discussing", OwnerID: "agent_emp", ReviewerID: "agent_rev", ApproverID: "agent_appr"}
	supervisor := domain.Agent{ID: "agent_sup", Name: "主管", Kind: domain.AgentKindSupervisor, DepartmentID: "dept_r"}
	action := marshalAction(t, "transition_task", map[string]any{"task_id": "task_1", "to": "in_progress"})
	if err := p.AuthorizeAction(ctx, ActionContext{Agent: supervisor, Task: &task}, action); err != nil {
		t.Fatalf("主管应能推进 discussing→in_progress: %v", err)
	}

	otherEmployee := domain.Agent{ID: "agent_other", Name: "小测", Kind: domain.AgentKindEmployee, DepartmentID: "dept_r"}
	if err := p.AuthorizeAction(ctx, ActionContext{Agent: otherEmployee, Task: &task}, action); err == nil {
		t.Fatal("非 owner/reviewer/approver 的员工不应能推进状态")
	}

	// 执行阶段：主管推动 in_progress→blocked。
	task.Status = "in_progress"
	block := marshalAction(t, "transition_task", map[string]any{"task_id": "task_1", "to": "blocked"})
	if err := p.AuthorizeAction(ctx, ActionContext{Agent: supervisor, Task: &task}, block); err != nil {
		t.Fatalf("主管应能标记阻塞: %v", err)
	}

	// in_review→approved 仍只允许 reviewer/approver。
	task.Status = "in_review"
	approve := marshalAction(t, "transition_task", map[string]any{"task_id": "task_1", "to": "approved"})
	if err := p.AuthorizeAction(ctx, ActionContext{Agent: supervisor, Task: &task}, approve); err == nil {
		t.Fatal("主管不应能直接批准 in_review→approved")
	}
	reviewer := domain.Agent{ID: "agent_rev", Name: "评审", Kind: domain.AgentKindEmployee, DepartmentID: "dept_r"}
	if err := p.AuthorizeAction(ctx, ActionContext{Agent: reviewer, Task: &task}, approve); err != nil {
		t.Fatalf("reviewer 应能批准: %v", err)
	}
}

func TestPolicyIsolation(t *testing.T) {
	p := NewPolicy(nil)
	ctx := context.Background()
	// 员工不能直接找其他部门员工。
	empA := domain.Agent{ID: "a1", Name: "A", Kind: domain.AgentKindEmployee, DepartmentID: "d1"}
	empB := domain.Agent{ID: "b1", Name: "B", Kind: domain.AgentKindEmployee, DepartmentID: "d2"}
	if err := p.AuthorizeRoute(empA, empB, domain.Conversation{Type: domain.ConvDepartment}, nil); err == nil {
		t.Fatal("跨部门员工直连应被拒绝")
	}
	empA2 := domain.Agent{ID: "a2", Name: "A2", Kind: domain.AgentKindEmployee, DepartmentID: "d1"}
	if err := p.AuthorizeRoute(empA, empA2, domain.Conversation{Type: domain.ConvDepartment}, nil); err != nil {
		t.Fatalf("同部门员工应可讨论: %v", err)
	}
	// 主管不能直接参与用户私聊。
	if err := p.AuthorizeParticipation(ctx, empA, domain.Conversation{ID: "c1", Type: domain.ConvDM}, nil); err == nil {
		t.Fatal("员工不能参与 DM")
	}
	assistant := domain.Agent{ID: "as", Name: "助理", Kind: domain.AgentKindAssistant}
	if err := p.AuthorizeParticipation(ctx, assistant, domain.Conversation{ID: "c1", Type: domain.ConvDM}, nil); err != nil {
		t.Fatalf("助理应可参与 DM: %v", err)
	}
	// 员工不能参加跨部门评审。
	if err := p.AuthorizeParticipation(ctx, empA, domain.Conversation{ID: "c2", Type: domain.ConvCrossDept}, nil); err == nil {
		t.Fatal("员工不能参加跨部门评审")
	}
}

func TestToolRegistry(t *testing.T) {
	r := NewToolRegistry(nil)
	names := r.Names()
	if len(names) != 5 {
		t.Fatalf("tools = %v", names)
	}
	want := map[string]bool{ToolReadFile: true, ToolWriteFile: true, ToolBash: true, ToolWebSearch: true, ToolViewChangeHistory: true}
	for _, name := range names {
		if !want[name] {
			t.Fatalf("未知工具 %q", name)
		}
	}
	if len(r.ToolDefinitions()) != 5 {
		t.Fatal("工具声明应为 5 个")
	}
	if err := r.Validate(ToolCall{Name: ToolReadFile, Arguments: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("缺 path 应被拒绝")
	}
	bad := json.RawMessage(`{"path":"a","oops":1}`)
	if err := r.Validate(ToolCall{Name: ToolReadFile, Arguments: bad}); err == nil {
		t.Fatal("未知参数应被拒绝")
	}
	if err := r.Validate(ToolCall{Name: "delete_everything"}); err == nil {
		t.Fatal("未知工具应被拒绝")
	}
	ok := json.RawMessage(`{"path":"a.txt"}`)
	if err := r.Validate(ToolCall{Name: ToolReadFile, Arguments: ok}); err != nil {
		t.Fatalf("合法 read_file 参数应通过: %v", err)
	}
}

func TestContextAssemblerPriorityAndBudget(t *testing.T) {
	agent := domain.Agent{ID: "a", Name: "助理", Kind: domain.AgentKindAssistant}
	var recent []domain.Message
	for i := 0; i < 10; i++ {
		recent = append(recent, domain.Message{
			ID: "m" + string(rune('a'+i)), ConversationID: "c1", SenderType: domain.SenderUser,
			Content: strings.Repeat("历史", 80),
		})
	}
	asm := NewAssembler(1500, 10)
	out := asm.Assemble(ContextInput{
		Agent:          agent,
		Conversation:   domain.Conversation{ID: "c1", Type: domain.ConvDM},
		CurrentMessage: domain.Message{ID: "cur", Content: "现在开始"},
		Recent:         recent,
		External:       []ExternalBlock{{Source: "web", Content: strings.Repeat("外部内容", 200)}},
	})
	if len(out.Messages) == 0 || out.Messages[0].Role != deepseek.RoleSystem {
		t.Fatalf("首条应为 system: %+v", out.Messages)
	}
	if !strings.Contains(out.Messages[0].Content, "安全规则") {
		t.Fatal("system 应包含安全规则")
	}
	droppedExternal := false
	for _, d := range out.Report.Dropped {
		if d == "外部内容" {
			droppedExternal = true
		}
	}
	if !droppedExternal {
		t.Fatalf("预算不足时应先丢外部内容: %+v", out.Report)
	}
	last := out.Messages[len(out.Messages)-1]
	if last.Role != deepseek.RoleUser || last.Content != "现在开始" {
		t.Fatalf("最后一条应为当前用户消息: %+v", last)
	}
	if out.Report.EstimatedPromptTokens > 1500 {
		t.Fatalf("超出预算: %d", out.Report.EstimatedPromptTokens)
	}
}

func TestAssistantGreetingAndDegraded(t *testing.T) {
	g := AssistantGreeting("阿秘书", "阿哲")
	for _, want := range []string{"项目助理", "不会", "意图卡", "G1"} {
		if !strings.Contains(g, want) {
			t.Fatalf("greeting 缺少 %q: %s", want, g)
		}
	}
	qs := FastClarifyQuestions()
	if len(qs) != 3 {
		t.Fatalf("快速模式应为 3 个问题，got %d", len(qs))
	}
	if len(FullInterviewQuestions()) < 9 {
		t.Fatal("完整访谈应覆盖 9 个方面")
	}
	if msg := DegradedMessage(deepseek.ErrNoAPIKey); !strings.Contains(msg, "DeepSeek API Key") {
		t.Fatalf("无 Key 提示 = %q", msg)
	}
	card := NewIntentCard("做知识库", []string{"搜索"}, "每天 10 元")
	action, err := card.Action()
	if err != nil {
		t.Fatalf("intent action: %v", err)
	}
	if action.Action != "save_intent" || len(action.Args) == 0 {
		t.Fatalf("action = %+v", action)
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("intent action validate: %v", err)
	}
}

func TestSendAssistantGreeting(t *testing.T) {
	store, assistant, conv, _ := newDMFixture(t)
	rt, err := NewRuntime(Options{Store: store, Provider: deepseek.NewMock(), OwnerName: "阿哲"})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer rt.Close()
	ctx := context.Background()
	msg, err := rt.SendAssistantGreeting(ctx, assistant.ID, conv.ID)
	if err != nil {
		t.Fatalf("greeting: %v", err)
	}
	if msg.SenderID != assistant.ID || !strings.Contains(msg.Content, "项目助理") {
		t.Fatalf("greeting msg = %+v", msg)
	}
	if !hasEventType(store.snapshotEvents(), "message.completed") {
		t.Fatal("缺少 message.completed 事件")
	}
}

// gateProvider 是 mailbox/中断/重试测试用的可控 Provider。
type gateProvider struct {
	mu       sync.Mutex
	calls    int
	started  chan int
	release  chan struct{}
	failNext bool
}

func (p *gateProvider) Name() string { return "gate" }

func (p *gateProvider) Complete(ctx context.Context, req deepseek.Request) (deepseek.Response, error) {
	return p.Stream(ctx, req, nil)
}

func (p *gateProvider) Stream(ctx context.Context, req deepseek.Request, sink deepseek.Sink) (deepseek.Response, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	fail := p.failNext
	p.failNext = false
	p.mu.Unlock()
	if p.started != nil {
		p.started <- n
	}
	if fail {
		return deepseek.Response{}, deepseek.ErrNoAPIKey
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return deepseek.Response{}, ctx.Err()
		}
	}
	content := fmt.Sprintf("reply-%d", n)
	if sink != nil {
		if err := sink(deepseek.Delta{Content: content}); err != nil {
			return deepseek.Response{}, err
		}
	}
	return deepseek.Response{
		ID: "gate", Model: req.Model,
		Choices: []deepseek.Choice{{Index: 0, Message: deepseek.Message{Role: deepseek.RoleAssistant, Content: content}, FinishReason: "stop"}},
	}, nil
}

func TestMailboxSerial(t *testing.T) {
	store, assistant, conv, userMsg := newDMFixture(t)
	second := store.seedMessage(domain.Message{ID: "msg_u2", ConversationID: conv.ID, SenderType: domain.SenderUser, SenderID: "owner", SenderName: "阿哲", Type: domain.MsgText, Content: "第二个问题"})
	provider := &gateProvider{started: make(chan int, 8), release: make(chan struct{})}
	rt, err := NewRuntime(Options{Store: store, Provider: provider, TurnTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer rt.Close()
	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, userMsg); err != nil {
		t.Fatalf("enqueue 1: %v", err)
	}
	if n := <-provider.started; n != 1 {
		t.Fatalf("first call = %d", n)
	}
	if err := rt.EnqueueUserMessage(ctx, second); err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}
	select {
	case n := <-provider.started:
		t.Fatalf("第二个 turn 在第一个完成前启动了（call %d）", n)
	case <-time.After(50 * time.Millisecond):
	}
	close(provider.release)
	if err := rt.WaitIdle(ctx, assistant.ID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	msgs := store.snapshotMessages()
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4: %+v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[2].Content, "reply-1") || !strings.Contains(msgs[3].Content, "reply-2") {
		t.Fatalf("回复顺序错误: %q / %q", msgs[2].Content, msgs[3].Content)
	}
}

func TestInterruptTurn(t *testing.T) {
	store, assistant, _, userMsg := newDMFixture(t)
	provider := &gateProvider{started: make(chan int, 4), release: make(chan struct{})}
	rt, err := NewRuntime(Options{Store: store, Provider: provider, TurnTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer rt.Close()
	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, userMsg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-provider.started
	if !rt.Interrupt(assistant.ID) {
		t.Fatal("interrupt 应返回 true")
	}
	if err := rt.WaitIdle(ctx, assistant.ID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	turns := store.snapshotTurns()
	if len(turns) != 1 || turns[0].Status != "interrupted" {
		t.Fatalf("turns = %+v", turns)
	}
	if !hasEventType(store.snapshotEvents(), "turn.interrupted") {
		t.Fatal("缺少 turn.interrupted 事件")
	}
	agent, _ := store.GetAgent(ctx, assistant.ID)
	if agent.Status != domain.AgentIdle {
		t.Fatalf("agent status = %q", agent.Status)
	}
}

func TestRetryLastAfterFailure(t *testing.T) {
	store, assistant, _, userMsg := newDMFixture(t)
	provider := &gateProvider{started: make(chan int, 4), failNext: true}
	rt, err := NewRuntime(Options{Store: store, Provider: provider, TurnTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer rt.Close()
	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, userMsg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-provider.started
	if err := rt.WaitIdle(ctx, assistant.ID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	turns := store.snapshotTurns()
	if len(turns) != 1 || turns[0].Status != "failed" {
		t.Fatalf("第一次应失败: %+v", turns)
	}
	if err := rt.RetryLast(assistant.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	<-provider.started
	if err := rt.WaitIdle(ctx, assistant.ID); err != nil {
		t.Fatalf("wait idle 2: %v", err)
	}
	turns = store.snapshotTurns()
	if len(turns) != 2 {
		t.Fatalf("turns = %d", len(turns))
	}
	reply := lastMessage(t, store)
	if !strings.Contains(reply.Content, "reply-2") {
		t.Fatalf("重试后回复 = %q", reply.Content)
	}
}

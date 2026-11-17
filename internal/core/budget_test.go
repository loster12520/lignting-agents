package core

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/lignting/lignting-agents/internal/deepseek"
	"github.com/lignting/lignting-agents/internal/domain"
)

type fakeUsage struct {
	mu    sync.Mutex
	cost  float64
	calls int
}

func (f *fakeUsage) SetCost(c float64) { f.mu.Lock(); f.cost = c; f.mu.Unlock() }
func (f *fakeUsage) SumUsage(_ context.Context, _ domain.UsageQuery) (domain.UsageTotals, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return domain.UsageTotals{Cost: f.cost, PromptTokens: 100, CompletionTokens: 50}, nil
}

type fakeSettings struct {
	mu   sync.Mutex
	data map[string]string
}

func (f *fakeSettings) Set(key, value string) { f.mu.Lock(); f.data[key] = value; f.mu.Unlock() }
func (f *fakeSettings) Setting(_ context.Context, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[key]
	return v, ok, nil
}

func newBudgetRuntime(t *testing.T, store *fakeStore, guard *BudgetGuard, settings SettingReader) (*Runtime, *deepseek.Mock, domain.Agent, domain.Conversation) {
	t.Helper()
	assistant := domain.Agent{ID: "agent_assistant", Name: "阿秘书", Kind: domain.AgentKindAssistant, Status: domain.AgentIdle, Model: "deepseek-flash"}
	store.seedAgent(assistant)
	conv := domain.Conversation{ID: "conv_b", Type: domain.ConvDM, Title: "私聊", Visibility: domain.VisibilityPrivate}
	store.seedConversation(conv)
	store.seedMember(domain.ConversationMember{ConversationID: conv.ID, PrincipalType: "agent", PrincipalID: assistant.ID})
	mock := deepseek.NewMock()
	rt, err := NewRuntime(Options{Store: store, Provider: mock, Budget: guard, Settings: settings, OwnerName: "阿哲"})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt, mock, assistant, conv
}

func enqueueAndWait(t *testing.T, rt *Runtime, store *fakeStore, conv domain.Conversation, assistant domain.Agent, text string) error {
	t.Helper()
	msg := store.seedMessage(domain.Message{ID: domain.NewID("msg"), ConversationID: conv.ID, SenderType: domain.SenderUser, SenderID: "owner", SenderName: "阿哲", Type: domain.MsgText, Content: text})
	if err := rt.EnqueueUserMessage(context.Background(), msg); err != nil {
		return err
	}
	return rt.WaitIdle(context.Background(), assistant.ID)
}

func countEventType(store *fakeStore, typ string) int {
	n := 0
	for _, ev := range store.snapshotEvents() {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

func TestBudgetWarnBlockRaiseRestore(t *testing.T) {
	store := newFakeStore()
	usage := &fakeUsage{}
	guard := NewBudgetGuard(usage, BudgetPolicy{DailyCNY: 10})
	rt, mock, assistant, conv := newBudgetRuntime(t, store, guard, nil)

	if err := enqueueAndWait(t, rt, store, conv, assistant, "预算 0%"); err != nil {
		t.Fatalf("turn1: %v", err)
	}
	if mock.CallCount() != 1 || countEventType(store, "budget.warning") != 0 {
		t.Fatalf("calls=%d warnings=%d", mock.CallCount(), countEventType(store, "budget.warning"))
	}
	// 85%：放行 + 一次告警。
	usage.SetCost(8.5)
	if err := enqueueAndWait(t, rt, store, conv, assistant, "预算 85%"); err != nil {
		t.Fatalf("turn2: %v", err)
	}
	if countEventType(store, "budget.warning") != 1 {
		t.Fatalf("warnings = %d, want 1", countEventType(store, "budget.warning"))
	}
	// 95% 不重复告警。
	usage.SetCost(9.5)
	if err := enqueueAndWait(t, rt, store, conv, assistant, "预算 95%"); err != nil {
		t.Fatalf("turn3: %v", err)
	}
	if countEventType(store, "budget.warning") != 1 {
		t.Fatalf("warning 应只发一次, got %d", countEventType(store, "budget.warning"))
	}
	// 100%：拒绝新 turn（model 不应被调用），可见 budget_blocked，Agent 状态 budget_blocked。
	usage.SetCost(10)
	beforeCalls := mock.CallCount()
	beforeMsgs := len(store.snapshotMessages())
	if err := enqueueAndWait(t, rt, store, conv, assistant, "预算 100%"); err != nil {
		t.Fatalf("turn4 wait: %v", err)
	}
	if mock.CallCount() != beforeCalls {
		t.Fatalf("预算 100%% 不应调用模型: calls %d->%d", beforeCalls, mock.CallCount())
	}
	if countEventType(store, "budget.blocked") != 1 {
		t.Fatalf("应写一条 budget.blocked 事件, got %d", countEventType(store, "budget.blocked"))
	}
	msgs := store.snapshotMessages()
	if len(msgs) <= beforeMsgs {
		t.Fatal("预算拒绝应有可见错误消息")
	}
	if !strings.Contains(msgs[len(msgs)-1].Content, "预算") {
		t.Fatalf("错误消息 = %q", msgs[len(msgs)-1].Content)
	}
	agent, _ := store.GetAgent(context.Background(), assistant.ID)
	if agent.Status != domain.AgentBudgetBlocked {
		t.Fatalf("agent status = %q", agent.Status)
	}
	blockedTurn := false
	for _, turn := range store.snapshotTurns() {
		if turn.Status == "failed" && strings.Contains(turn.Error, "budget_blocked") {
			blockedTurn = true
		}
	}
	if !blockedTurn {
		t.Fatalf("缺少 budget_blocked turn: %+v", store.snapshotTurns())
	}

	// 追加预算后恢复。
	guard.SetPolicy(BudgetPolicy{DailyCNY: 20})
	if err := enqueueAndWait(t, rt, store, conv, assistant, "追加预算后"); err != nil {
		t.Fatalf("turn5: %v", err)
	}
	if mock.CallCount() != beforeCalls+1 {
		t.Fatalf("恢复后应重新调用模型, calls=%d", mock.CallCount())
	}
}

func TestEmergencyStopGuard(t *testing.T) {
	store := newFakeStore()
	settings := &fakeSettings{data: map[string]string{SettingEmergencyStop: "true"}}
	guard := NewBudgetGuard(nil, BudgetPolicy{})
	rt, mock, assistant, conv := newBudgetRuntime(t, store, guard, settings)

	stoppedMsg := store.seedMessage(domain.Message{ID: "msg_stop", ConversationID: conv.ID, SenderType: domain.SenderUser, SenderID: "owner", SenderName: "阿哲", Type: domain.MsgText, Content: "停止中"})
	err := rt.EnqueueUserMessage(context.Background(), stoppedMsg)
	if err == nil || !strings.Contains(err.Error(), "紧急停止") {
		t.Fatalf("enqueue 应被拒绝: %v", err)
	}
	if mock.CallCount() != 0 {
		t.Fatalf("紧急停止时不应调用模型: %d", mock.CallCount())
	}
	if len(store.snapshotMessages()) != 1 {
		t.Fatalf("紧急停止时只应有用户消息，不应有 Agent 回复: %d", len(store.snapshotMessages()))
	}

	settings.Set(SettingEmergencyStop, "false")
	if err := enqueueAndWait(t, rt, store, conv, assistant, "恢复后"); err != nil {
		t.Fatalf("恢复后 enqueue: %v", err)
	}
	if mock.CallCount() != 1 {
		t.Fatalf("恢复后应调用模型: %d", mock.CallCount())
	}
	if len(store.snapshotMessages()) < 2 {
		t.Fatal("恢复后应有回复")
	}
}
func TestBudgetSettingsDynamic(t *testing.T) {
	store := newFakeStore()
	usage := &fakeUsage{}
	settings := &fakeSettings{data: map[string]string{}}
	guard := NewBudgetGuard(usage, BudgetPolicy{DailyCNY: 10}, settings)
	rt, mock, assistant, conv := newBudgetRuntime(t, store, guard, settings)

	// 初始 6 元：放行。
	usage.SetCost(6)
	if err := enqueueAndWait(t, rt, store, conv, assistant, "初始预算"); err != nil {
		t.Fatalf("turn1: %v", err)
	}
	// 设置页把日预算改成 5：下一 turn 立即生效（拒绝，不调用模型）。
	settings.Set("budget.daily_cny", "5")
	beforeCalls := mock.CallCount()
	if err := enqueueAndWait(t, rt, store, conv, assistant, "预算被下调"); err != nil {
		t.Fatalf("turn2 wait: %v", err)
	}
	if mock.CallCount() != beforeCalls {
		t.Fatalf("settings 下调预算应拒绝新 turn: calls %d->%d", beforeCalls, mock.CallCount())
	}
	agent, _ := store.GetAgent(context.Background(), assistant.ID)
	if agent.Status != domain.AgentBudgetBlocked {
		t.Fatalf("agent status = %q", agent.Status)
	}
	// 追加到 20：恢复。
	settings.Set("budget.daily_cny", "20")
	if err := enqueueAndWait(t, rt, store, conv, assistant, "追加预算"); err != nil {
		t.Fatalf("turn3: %v", err)
	}
	if mock.CallCount() != beforeCalls+1 {
		t.Fatalf("追加预算后应恢复: calls=%d", mock.CallCount())
	}
}

package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/deepseek"
	"github.com/lignting/lignting-agents/internal/domain"
)

type orderProvider struct {
	mu      sync.Mutex
	order   []string
	calls   map[string]int
	start   chan string
	release chan struct{}
}

func (p *orderProvider) Name() string { return "order" }
func (p *orderProvider) Complete(ctx context.Context, req deepseek.Request) (deepseek.Response, error) {
	return p.Stream(ctx, req, nil)
}
func (p *orderProvider) Stream(ctx context.Context, req deepseek.Request, sink deepseek.Sink) (deepseek.Response, error) {
	agent := "unknown"
	for _, name := range []string{"AgentA", "AgentB", "AgentC"} {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, name) {
				agent = name
			}
		}
	}
	p.mu.Lock()
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[agent]++
	p.order = append(p.order, "start:"+agent)
	p.mu.Unlock()
	if p.start != nil {
		select {
		case p.start <- agent:
		default:
		}
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return deepseek.Response{}, ctx.Err()
		}
	}
	time.Sleep(5 * time.Millisecond)
	p.mu.Lock()
	p.order = append(p.order, "end:"+agent)
	p.mu.Unlock()
	content := "reply-" + agent
	if sink != nil {
		_ = sink(deepseek.Delta{Content: content})
	}
	return deepseek.Response{Choices: []deepseek.Choice{{Index: 0, Message: deepseek.Message{Role: deepseek.RoleAssistant, Content: content}, FinishReason: "stop"}}}, nil
}

func (p *orderProvider) snapshot() ([]string, map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	order := append([]string(nil), p.order...)
	calls := map[string]int{}
	for k, v := range p.calls {
		calls[k] = v
	}
	return order, calls
}

func mentionFixture(t *testing.T) (*fakeStore, domain.Conversation, map[string]domain.Agent) {
	t.Helper()
	store := newFakeStore()
	agents := map[string]domain.Agent{
		"agent_a": {ID: "agent_a", Name: "AgentA", Kind: domain.AgentKindEmployee, DepartmentID: "d1", Status: domain.AgentIdle},
		"agent_b": {ID: "agent_b", Name: "AgentB", Kind: domain.AgentKindEmployee, DepartmentID: "d1", Status: domain.AgentIdle},
		"agent_c": {ID: "agent_c", Name: "AgentC", Kind: domain.AgentKindEmployee, DepartmentID: "d1", Status: domain.AgentIdle},
	}
	for _, a := range agents {
		store.seedAgent(a)
	}
	conv := domain.Conversation{ID: "conv_g", Type: domain.ConvProject, Title: "项目群", Visibility: domain.VisibilityProject}
	store.seedConversation(conv)
	for _, a := range agents {
		store.seedMember(domain.ConversationMember{ConversationID: conv.ID, PrincipalType: "agent", PrincipalID: a.ID})
	}
	return store, conv, agents
}

func mentionedMessage(store *fakeStore, convID, mentions string) domain.Message {
	return store.seedMessage(domain.Message{
		ID: domain.NewID("msg"), ConversationID: convID, SenderType: domain.SenderUser,
		SenderID: "owner", SenderName: "阿哲", Type: domain.MsgText, Content: "请各位发言",
		Meta: mentions,
	})
}

func TestMentionQueueSerialOrderAndLimit(t *testing.T) {
	store, conv, _ := mentionFixture(t)
	provider := &orderProvider{}
	rt, err := NewRuntime(Options{Store: store, Provider: provider, MentionBatchLimit: 2})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	defer rt.Close()
	msg := mentionedMessage(store, conv.ID, `{"mentions":["agent_a","agent_b","agent_c"]}`)
	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := rt.WaitMentionQueueIdle(ctx, conv.ID); err != nil {
		t.Fatalf("wait queue: %v", err)
	}
	order, calls := provider.snapshot()
	want := []string{"start:AgentA", "end:AgentA", "start:AgentB", "end:AgentB"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("顺序 = %v, want %v", order, want)
	}
	if calls["AgentC"] != 0 {
		t.Fatalf("单轮上限 2，AgentC 不应被触发: %v", calls)
	}
	if calls["AgentA"] != 1 || calls["AgentB"] != 1 {
		t.Fatalf("calls = %v", calls)
	}
	status := rt.MentionQueueStatusFor(conv.ID)
	if status.Limit != 2 || status.Running {
		t.Fatalf("status = %+v", status)
	}
}

func TestMentionQueueStop(t *testing.T) {
	store, conv, _ := mentionFixture(t)
	provider := &orderProvider{start: make(chan string, 4), release: make(chan struct{})}
	rt, err := NewRuntime(Options{Store: store, Provider: provider, MentionBatchLimit: 2})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	defer rt.Close()
	msg := mentionedMessage(store, conv.ID, `{"mentions":["agent_a","agent_b"]}`)
	ctx := context.Background()
	if err := rt.EnqueueUserMessage(ctx, msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	select {
	case <-provider.start:
	case <-time.After(2 * time.Second):
		t.Fatal("AgentA 未开始")
	}
	status := rt.MentionQueueStatusFor(conv.ID)
	if !status.Running || status.CurrentAgentID != "agent_a" {
		t.Fatalf("status = %+v", status)
	}
	rt.StopMentionQueue(conv.ID)
	close(provider.release)
	if err := rt.WaitMentionQueueIdle(ctx, conv.ID); err != nil {
		t.Fatalf("wait queue: %v", err)
	}
	_, calls := provider.snapshot()
	if calls["AgentA"] != 1 || calls["AgentB"] != 0 {
		t.Fatalf("停止后不应继续下一个: %v", calls)
	}
	status = rt.MentionQueueStatusFor(conv.ID)
	if status.Running {
		t.Fatalf("停止后队列不应 running: %+v", status)
	}
}

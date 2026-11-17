package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/lignting/lignting-agents/internal/domain"
)

// fakeStore 是 core 单测用的内存 Store（实现 ports.go 的全部窄接口）。
type fakeStore struct {
	mu sync.Mutex

	agents  map[string]domain.Agent
	convs   map[string]domain.Conversation
	members map[string][]domain.ConversationMember
	depts   map[string]domain.Department
	tasks   map[string]domain.Task

	messages  []domain.Message
	events    []domain.Event
	turns     map[string]domain.Turn
	snapshots []domain.ContextSnapshot
	usages    []domain.Usage
	intents   []string
	audits    []domain.AuditEntry

	seq int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		agents:  map[string]domain.Agent{},
		convs:   map[string]domain.Conversation{},
		members: map[string][]domain.ConversationMember{},
		depts:   map[string]domain.Department{},
		tasks:   map[string]domain.Task{},
		turns:   map[string]domain.Turn{},
	}
}

func (s *fakeStore) seedAgent(a domain.Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[a.ID] = a
}

func (s *fakeStore) seedConversation(c domain.Conversation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convs[c.ID] = c
}

func (s *fakeStore) seedMember(m domain.ConversationMember) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members[m.ConversationID] = append(s.members[m.ConversationID], m)
}

func (s *fakeStore) seedDepartment(d domain.Department) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.depts[d.ID] = d
}

func (s *fakeStore) seedTask(t domain.Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.ID] = t
}

func (s *fakeStore) seedMessage(m domain.Message) domain.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	m.Seq = s.seq
	if m.ID == "" {
		m.ID = domain.NewID("msg")
	}
	if m.CreatedAt == "" {
		m.CreatedAt = domain.NewID("ts")
	}
	s.messages = append(s.messages, m)
	return m
}

func (s *fakeStore) snapshotMessages() []domain.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

func (s *fakeStore) snapshotEvents() []domain.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Event, len(s.events))
	copy(out, s.events)
	return out
}

func (s *fakeStore) snapshotAudits() []domain.AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.AuditEntry, len(s.audits))
	copy(out, s.audits)
	return out
}

func (s *fakeStore) snapshotTurns() []domain.Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Turn, 0, len(s.turns))
	for _, t := range s.turns {
		out = append(out, t)
	}
	return out
}

func (s *fakeStore) GetAgent(_ context.Context, id string) (domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return domain.Agent{}, fmt.Errorf("%w: agent %s", domain.NewError(domain.CodeNotFound, "agent not found", 404), id)
	}
	return a, nil
}

func (s *fakeStore) ListAgentsByDepartment(_ context.Context, departmentID string) ([]domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Agent
	for _, a := range s.agents {
		if a.DepartmentID == departmentID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *fakeStore) ListAgentsByKind(_ context.Context, kind string) ([]domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Agent
	for _, a := range s.agents {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *fakeStore) UpdateAgentStatus(_ context.Context, id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return fmt.Errorf("agent %s not found", id)
	}
	a.Status = status
	s.agents[id] = a
	return nil
}

func (s *fakeStore) GetConversation(_ context.Context, id string) (domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return domain.Conversation{}, fmt.Errorf("conversation %s not found", id)
	}
	return c, nil
}

func (s *fakeStore) ListConversationMembers(_ context.Context, conversationID string) ([]domain.ConversationMember, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.ConversationMember(nil), s.members[conversationID]...), nil
}

func (s *fakeStore) GetDepartment(_ context.Context, id string) (domain.Department, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.depts[id]
	if !ok {
		return domain.Department{}, fmt.Errorf("department %s not found", id)
	}
	return d, nil
}

func (s *fakeStore) GetTask(_ context.Context, id string) (domain.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return domain.Task{}, fmt.Errorf("task %s not found", id)
	}
	return t, nil
}

func (s *fakeStore) AppendMessage(_ context.Context, msg domain.Message) (domain.Message, error) {
	return s.seedMessage(msg), nil
}

func (s *fakeStore) RecentMessages(_ context.Context, conversationID string, limit int, beforeSeq int64) ([]domain.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var filtered []domain.Message
	for _, m := range s.messages {
		if m.ConversationID != conversationID {
			continue
		}
		if beforeSeq > 0 && m.Seq >= beforeSeq {
			continue
		}
		filtered = append(filtered, m)
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered, nil
}

func (s *fakeStore) AppendEvent(_ context.Context, ev domain.Event) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	ev.Seq = s.seq
	s.events = append(s.events, ev)
	return ev, nil
}

func (s *fakeStore) AppendReply(_ context.Context, msg domain.Message, events []domain.Event, audit domain.AuditEntry) (domain.Message, error) {
	msg = s.seedMessage(msg)
	s.mu.Lock()
	for _, ev := range events {
		s.seq++
		ev.Seq = s.seq
		s.events = append(s.events, ev)
	}
	if audit.Action != "" {
		s.audits = append(s.audits, audit)
	}
	s.mu.Unlock()
	return msg, nil
}

func (s *fakeStore) CreateTurn(_ context.Context, turn domain.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns[turn.ID] = turn
	return nil
}

func (s *fakeStore) FinishTurnWithEvents(_ context.Context, turnID, status, errText string, events []domain.Event, audit domain.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.turns[turnID]
	if !ok {
		return fmt.Errorf("turn %s not found", turnID)
	}
	turn.Status = status
	turn.Error = errText
	s.turns[turnID] = turn
	for _, ev := range events {
		s.seq++
		ev.Seq = s.seq
		s.events = append(s.events, ev)
	}
	if audit.Action != "" {
		s.audits = append(s.audits, audit)
	}
	return nil
}

func (s *fakeStore) AppendAudit(_ context.Context, audit domain.AuditEntry) (domain.AuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, audit)
	return audit, nil
}

func (s *fakeStore) InsertContextSnapshot(_ context.Context, snap domain.ContextSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots = append(s.snapshots, snap)
	return nil
}

func (s *fakeStore) InsertUsage(_ context.Context, usage domain.Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usages = append(s.usages, usage)
	return nil
}

func (s *fakeStore) SaveIntent(_ context.Context, content string) (string, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.intents = append(s.intents, content)
	return fmt.Sprintf("intent_%d", len(s.intents)), len(s.intents), nil
}

// fakeExecutor 是 5 工具测试用的执行器。
type fakeExecutor struct {
	mu     sync.Mutex
	calls  []ToolCall
	result ToolResult
	err    error
}

func (e *fakeExecutor) Execute(_ context.Context, _ domain.Agent, call ToolCall) (ToolResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, call)
	if e.err != nil {
		return ToolResult{CallID: call.ID, Name: call.Name, Content: e.err.Error(), IsError: true}, e.err
	}
	res := e.result
	res.CallID = call.ID
	res.Name = call.Name
	return res, nil
}

func marshalAction(t interface {
	Fatalf(string, ...any)
}, action string, args map[string]any) domain.Action {
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal action args: %v", err)
	}
	return domain.Action{Action: action, Args: raw}
}

func fencedAction(t interface {
	Fatalf(string, ...any)
}, action string, args map[string]any) string {
	a := marshalAction(t, action, args)
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	fence := "\x60\x60\x60"
	return fence + "lignting-action\n" + string(raw) + "\n" + fence
}

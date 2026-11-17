package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/lignting/lignting-agents/internal/domain"
)

type fakeDebate struct {
	mu        sync.Mutex
	messages  []domain.Message
	decisions map[string]domain.Decision
	tasks     map[string]domain.Task
	events    []domain.Event
	audits    []domain.AuditEntry
	seq       int
}

func newFakeDebate() *fakeDebate {
	return &fakeDebate{decisions: map[string]domain.Decision{}, tasks: map[string]domain.Task{}}
}

func (f *fakeDebate) AppendMessage(_ context.Context, m domain.Message) (domain.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	m.Seq = int64(f.seq)
	if m.ID == "" {
		m.ID = domain.NewID("msg")
	}
	f.messages = append(f.messages, m)
	return m, nil
}
func (f *fakeDebate) ListMessages(_ context.Context, conversationID string, before, after int64, limit int) ([]domain.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Message
	for _, m := range f.messages {
		if m.ConversationID != conversationID {
			continue
		}
		if before > 0 && m.Seq >= before {
			continue
		}
		if after > 0 && m.Seq <= after {
			continue
		}
		out = append(out, m)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}
func (f *fakeDebate) GetTask(_ context.Context, id string) (domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return domain.Task{}, fmt.Errorf("task %s not found", id)
	}
	return t, nil
}
func (f *fakeDebate) CreateDecision(_ context.Context, d domain.Decision) (domain.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decisions[d.ID] = d
	return d, nil
}
func (f *fakeDebate) GetDecision(_ context.Context, id string) (domain.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.decisions[id]
	if !ok {
		return domain.Decision{}, fmt.Errorf("decision %s not found", id)
	}
	return d, nil
}
func (f *fakeDebate) ListDecisionsByStatus(_ context.Context, status string) ([]domain.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Decision
	for _, d := range f.decisions {
		if d.Status == status {
			out = append(out, d)
		}
	}
	return out, nil
}
func (f *fakeDebate) ListDecisionsByTask(_ context.Context, taskID string) ([]domain.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Decision
	for _, d := range f.decisions {
		if d.AffectedTasks != "" && containsStr(d.AffectedTasks, taskID) {
			out = append(out, d)
		}
	}
	return out, nil
}
func (f *fakeDebate) SupersedeDecision(_ context.Context, oldID, newID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.decisions[oldID]
	if !ok {
		return fmt.Errorf("decision %s not found", oldID)
	}
	d.Status = domain.DecisionSuperseded
	d.SupersededBy = newID
	f.decisions[oldID] = d
	return nil
}
func (f *fakeDebate) AppendEvent(_ context.Context, ev domain.Event) (domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	ev.Seq = int64(f.seq)
	f.events = append(f.events, ev)
	return ev, nil
}
func (f *fakeDebate) AppendAudit(_ context.Context, a domain.AuditEntry) (domain.AuditEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, a)
	return a, nil
}

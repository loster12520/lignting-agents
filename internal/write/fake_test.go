package write

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/lignting/lignting-agents/internal/domain"
)

type fakeRepo struct {
	mu       sync.Mutex
	events   map[string]domain.WriteEvent
	order    []string
	timeline []domain.FileTimelineEntry
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{events: make(map[string]domain.WriteEvent)}
}

func (f *fakeRepo) InsertWriteEvent(_ context.Context, ev domain.WriteEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.events[ev.ID]; ok {
		return errors.New("duplicate event")
	}
	f.events[ev.ID] = ev
	f.order = append(f.order, ev.ID)
	return nil
}

func (f *fakeRepo) UpdateWriteEvent(_ context.Context, ev domain.WriteEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events[ev.ID] = ev
	return nil
}

func (f *fakeRepo) GetWriteEvent(_ context.Context, id string) (domain.WriteEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev, ok := f.events[id]
	if !ok {
		return domain.WriteEvent{}, errors.New("not found")
	}
	return ev, nil
}

func (f *fakeRepo) ListWriteEventsByStatus(_ context.Context, status string, limit int) ([]domain.WriteEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.WriteEvent, 0)
	for _, id := range f.order {
		ev := f.events[id]
		if ev.Status == status {
			out = append(out, ev)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeRepo) AppendTimeline(_ context.Context, e domain.FileTimelineEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.timeline = append(f.timeline, e)
	return nil
}

func (f *fakeRepo) TrimTimeline(_ context.Context, path string, keep int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if keep < 0 {
		keep = 0
	}
	var pathEntries []int
	for i, e := range f.timeline {
		if e.Path == path {
			pathEntries = append(pathEntries, i)
		}
	}
	remove := len(pathEntries) - keep
	if remove <= 0 {
		return 0, nil
	}
	drop := make(map[int]bool, remove)
	for _, i := range pathEntries[:remove] {
		drop[i] = true
	}
	next := make([]domain.FileTimelineEntry, 0, len(f.timeline)-remove)
	for i, e := range f.timeline {
		if !drop[i] {
			next = append(next, e)
		}
	}
	f.timeline = next
	return remove, nil
}

func (f *fakeRepo) ListTimeline(_ context.Context, path, branch, commitSHA, agentID, since string, limit int) ([]domain.FileTimelineEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.FileTimelineEntry, 0)
	for _, e := range f.timeline {
		if path != "" && e.Path != path {
			continue
		}
		if branch != "" && e.Branch != branch {
			continue
		}
		if commitSHA != "" && e.CommitSHA != commitSHA {
			continue
		}
		if agentID != "" && e.AgentID != agentID {
			continue
		}
		if since != "" && e.CreatedAt < since {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeRepo) TimelineHasEvent(_ context.Context, writeEventID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.timeline {
		if e.WriteEventID == writeEventID {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) eventCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.order)
}

func (f *fakeRepo) timelineCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timeline)
}

func (f *fakeRepo) lastEvent() domain.WriteEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.order) == 0 {
		return domain.WriteEvent{}
	}
	return f.events[f.order[len(f.order)-1]]
}

func (f *fakeRepo) eventsByStatus(status string) []domain.WriteEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.WriteEvent, 0)
	for _, id := range f.order {
		if f.events[id].Status == status {
			out = append(out, f.events[id])
		}
	}
	return out
}

func (f *fakeRepo) timelineFor(path string) []domain.FileTimelineEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.FileTimelineEntry, 0)
	for _, e := range f.timeline {
		if e.Path == path {
			out = append(out, e)
		}
	}
	return out
}

type fakeAudit struct {
	mu      sync.Mutex
	records []AuditRecord
	err     error
}

func (a *fakeAudit) RecordAudit(_ context.Context, actorType, actorID, action, objectType, objectID, result, detail string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.records = append(a.records, AuditRecord{
		ActorType: actorType, ActorID: actorID, Action: action,
		ObjectType: objectType, ObjectID: objectID, Result: result, Detail: detail,
	})
	return nil
}

func (a *fakeAudit) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.records)
}

func (a *fakeAudit) hasAction(action string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range a.records {
		if r.Action == action {
			return true
		}
	}
	return false
}

type fakeBranch struct {
	branch      string
	writableErr error
}

func (b *fakeBranch) Writable(context.Context) error {
	if b.writableErr != nil {
		return b.writableErr
	}
	return nil
}

func (b *fakeBranch) Branch(context.Context) (string, error) { return b.branch, nil }

func contains(s, sub string) bool { return strings.Contains(s, sub) }

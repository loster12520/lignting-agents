package memory

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// ---- fake clock ----

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// ---- fake repo ----

type fakeRepo struct {
	mu       sync.Mutex
	memories map[string]domain.Memory
	order    []string
	events   []MemoryEvent
	reads    []MemoryRead
	manuals  []domain.PromptManual

	now func() time.Time

	failList   error
	failGet    error
	failInsert error
}

func newFakeRepo(clock Clock) *fakeRepo {
	return &fakeRepo{memories: map[string]domain.Memory{}, now: clock.Now}
}

func (r *fakeRepo) InsertMemory(ctx context.Context, m domain.Memory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failInsert != nil {
		return r.failInsert
	}
	if _, ok := r.memories[m.ID]; ok {
		return ErrConflict
	}
	r.memories[m.ID] = m
	r.order = append(r.order, m.ID)
	return nil
}

func (r *fakeRepo) GetMemory(ctx context.Context, id string) (domain.Memory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failGet != nil {
		return domain.Memory{}, r.failGet
	}
	m, ok := r.memories[id]
	if !ok {
		return domain.Memory{}, ErrNotFound
	}
	return m, nil
}

func (r *fakeRepo) UpdateMemory(ctx context.Context, m domain.Memory, expectedVersion int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.memories[m.ID]
	if !ok {
		return ErrNotFound
	}
	if cur.Version != expectedVersion {
		return ErrConflict
	}
	r.memories[m.ID] = m
	return nil
}

func (r *fakeRepo) ListMemories(ctx context.Context, f Filter) ([]domain.Memory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failList != nil {
		return nil, r.failList
	}
	out := make([]domain.Memory, 0, len(r.order))
	for _, id := range r.order {
		m := r.memories[id]
		if !r.match(m, f) {
			continue
		}
		out = append(out, m)
	}
	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return nil, nil
		}
		out = out[f.Offset:]
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (r *fakeRepo) CountMemories(ctx context.Context, f Filter) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failList != nil {
		return 0, r.failList
	}
	n := 0
	for _, m := range r.memories {
		if r.match(m, f) {
			n++
		}
	}
	return n, nil
}

func (r *fakeRepo) match(m domain.Memory, f Filter) bool {
	if !f.IncludeDeleted && IsTombstone(m) {
		return false
	}
	if f.Scope != "" && m.Scope != f.Scope {
		return false
	}
	if f.DepartmentID != "" && m.DepartmentID != f.DepartmentID {
		return false
	}
	if f.AgentID != "" && m.AgentID != f.AgentID {
		return false
	}
	if f.EntityType != "" && m.EntityType != f.EntityType {
		return false
	}
	if f.EntityKey != "" && m.EntityKey != f.EntityKey {
		return false
	}
	if f.CreatedBy != "" && m.CreatedBy != f.CreatedBy {
		return false
	}
	if len(f.Status) > 0 {
		ok := false
		for _, s := range f.Status {
			if m.Status == s {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(f.IDs) > 0 {
		ok := false
		for _, id := range f.IDs {
			if m.ID == id {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(f.Tags) > 0 {
		ok := false
		for _, want := range f.Tags {
			if TagsContain(m.Tags, want) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	created := ParseTime(m.CreatedAt)
	if f.CreatedSince != "" && created.Before(ParseTime(f.CreatedSince)) {
		return false
	}
	if f.CreatedBefore != "" && !created.Before(ParseTime(f.CreatedBefore)) {
		return false
	}
	if !f.IncludeExpired && m.Status == domain.MemoryStatusActive && !Effective(m, r.now()) {
		return false
	}
	return true
}

func (r *fakeRepo) AppendMemoryEvent(ctx context.Context, ev MemoryEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return nil
}

func (r *fakeRepo) AddMemoryRead(ctx context.Context, rec MemoryRead) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads = append(r.reads, rec)
	return nil
}

func (r *fakeRepo) MaxManualVersion(ctx context.Context, key ManualKey) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	max := 0
	for _, m := range r.manuals {
		if m.Scope == key.Scope && m.DepartmentID == key.DepartmentID && m.AgentID == key.AgentID && m.Version > max {
			max = m.Version
		}
	}
	return max, nil
}

func (r *fakeRepo) ListManuals(ctx context.Context, key ManualKey) ([]domain.PromptManual, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.PromptManual, 0, len(r.manuals))
	for _, m := range r.manuals {
		if m.Scope == key.Scope && m.DepartmentID == key.DepartmentID && m.AgentID == key.AgentID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func (r *fakeRepo) GetManual(ctx context.Context, id string) (domain.PromptManual, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.manuals {
		if m.ID == id {
			return m, nil
		}
	}
	return domain.PromptManual{}, ErrNotFound
}

func (r *fakeRepo) InsertManual(ctx context.Context, m domain.PromptManual) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, old := range r.manuals {
		if old.ID == m.ID {
			return ErrConflict
		}
		if old.Scope == m.Scope && old.DepartmentID == m.DepartmentID && old.AgentID == m.AgentID && old.Version == m.Version {
			return ErrConflict
		}
	}
	r.manuals = append(r.manuals, m)
	return nil
}

func (r *fakeRepo) UpdateManualStatus(ctx context.Context, id, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.manuals {
		if r.manuals[i].ID == id {
			r.manuals[i].Status = status
			return nil
		}
	}
	return ErrNotFound
}

// ---- test helpers ----

func (r *fakeRepo) eventsOf(memoryID, action string) []MemoryEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []MemoryEvent
	for _, e := range r.events {
		if e.MemoryID == memoryID && (action == "" || e.Action == action) {
			out = append(out, e)
		}
	}
	return out
}

func (r *fakeRepo) lastEvent(action string) (MemoryEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].Action == action {
			return r.events[i], true
		}
	}
	return MemoryEvent{}, false
}

type fakeCompressor struct {
	mu    sync.Mutex
	calls int
	last  CompressionRequest
	fn    func(req CompressionRequest) (CompressionResult, error)
}

func (c *fakeCompressor) Compress(ctx context.Context, req CompressionRequest) (CompressionResult, error) {
	c.mu.Lock()
	c.calls++
	c.last = req
	fn := c.fn
	c.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	r := []rune(req.Content)
	if len(r) > req.TargetChars {
		r = r[:req.TargetChars]
	}
	return CompressionResult{Content: string(r), Model: "fake"}, nil
}

func (c *fakeCompressor) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newTestService(t *testing.T, mutate func(*Config)) (*Service, *fakeRepo, *fakeClock, *fakeCompressor) {
	t.Helper()
	clock := newFakeClock()
	repo := newFakeRepo(clock)
	cfg := DefaultConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	comp := &fakeCompressor{}
	svc, err := NewService(Options{Repo: repo, Clock: clock, Config: cfg, Compressor: comp})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, repo, clock, comp
}

func supervisor(id, dept string) Access { return AgentAccess(ActorSupervisor, id, dept, false) }
func employee(id, dept string) Access   { return AgentAccess(ActorEmployee, id, dept, false) }
func assistant(id string) Access        { return AgentAccess(ActorAssistant, id, "", true) }
func human(id string) Access            { return HumanAccess(id) }

func baseCandidate(taskID string) Candidate {
	return Candidate{
		Scope:            domain.MemoryScopeDepartment,
		DepartmentID:     "dept_a",
		AgentID:          "sup_1",
		EntityType:       EntityCapability,
		EntityKey:        "backend",
		Content:          "后端能力：Go + SQLite，可独立完成模块设计与实现",
		Confidence:       ConfidenceHigh,
		Evidence:         []Evidence{{Kind: EvidenceTask, Ref: "task_1"}},
		Visibility:       domain.MemoryTeam,
		Reusable:         true,
		DecisionRelevant: true,
		CreatedBy:        "sup_1",
		SourceTaskID:     taskID,
	}
}

func mustSubmit(t *testing.T, svc *Service, c Candidate, actor Access) domain.Memory {
	t.Helper()
	res, err := svc.SubmitCandidate(context.Background(), c, actor)
	if err != nil {
		t.Fatalf("SubmitCandidate: %v", err)
	}
	return res.Memory
}

func mustApprove(t *testing.T, svc *Service, id string, actor Access) domain.Memory {
	t.Helper()
	res, err := svc.Approve(context.Background(), id, actor)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	return res.Memory
}

func mustActive(t *testing.T, svc *Service, c Candidate, actor Access) domain.Memory {
	t.Helper()
	m := mustSubmit(t, svc, c, actor)
	return mustApprove(t, svc, m.ID, actor)
}

func containsID(items []InjectedMemory, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

func droppedReason(items []DroppedMemory, id string) string {
	for _, it := range items {
		if it.ID == id {
			return it.Reason
		}
	}
	return ""
}

func eventDetailMap(t *testing.T, ev MemoryEvent) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(ev.Detail), &m); err != nil {
		t.Fatalf("event detail 不是合法 JSON: %v (%s)", err, ev.Detail)
	}
	return m
}

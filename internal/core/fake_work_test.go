package core

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/lignting/lignting-agents/internal/domain"
)

type fakeWork struct {
	mu         sync.Mutex
	tasks      map[string]domain.Task
	criteria   []domain.Criterion
	deps       map[string][]string
	taskEvents []domain.TaskEvent
	artifacts  map[string]domain.Artifact
	versions   []domain.ArtifactVersion
	reviews    map[string]domain.Review
	verdicts   []domain.ReviewVerdict
	decisions  map[string]domain.Decision
	approvals  map[string]domain.Approval
	events     []domain.Event
	audits     []domain.AuditEntry
	seq        int
}

func newFakeWork() *fakeWork {
	return &fakeWork{
		tasks: map[string]domain.Task{}, deps: map[string][]string{},
		artifacts: map[string]domain.Artifact{}, reviews: map[string]domain.Review{},
		decisions: map[string]domain.Decision{}, approvals: map[string]domain.Approval{},
	}
}

func (f *fakeWork) CreateTask(_ context.Context, t domain.Task) (domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[t.ID] = t
	return t, nil
}
func (f *fakeWork) UpdateTask(_ context.Context, t domain.Task) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[t.ID] = t
	return nil
}
func (f *fakeWork) GetTask(_ context.Context, id string) (domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return domain.Task{}, fmt.Errorf("task %s not found", id)
	}
	return t, nil
}
func (f *fakeWork) ListTasksByDepartment(_ context.Context, departmentID string) ([]domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Task
	for _, t := range f.tasks {
		if t.DepartmentID == departmentID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}
func (f *fakeWork) ListTasksByOwnerStatus(_ context.Context, ownerID, status string) ([]domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Task
	for _, t := range f.tasks {
		if t.OwnerID == ownerID && t.Status == status {
			out = append(out, t)
		}
	}
	return out, nil
}
func (f *fakeWork) CreateTaskCriterion(_ context.Context, c domain.Criterion) (domain.Criterion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.criteria = append(f.criteria, c)
	return c, nil
}
func (f *fakeWork) ListTaskCriteria(_ context.Context, taskID string) ([]domain.Criterion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Criterion
	for _, c := range f.criteria {
		if c.TaskID == taskID {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakeWork) UpdateTaskCriterion(_ context.Context, c domain.Criterion) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.criteria {
		if f.criteria[i].ID == c.ID {
			f.criteria[i] = c
			return nil
		}
	}
	return fmt.Errorf("criterion %s not found", c.ID)
}
func (f *fakeWork) AddTaskDependency(_ context.Context, taskID, dependsOn string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deps[taskID] = append(f.deps[taskID], dependsOn)
	return nil
}
func (f *fakeWork) ListTaskDependencies(_ context.Context, taskID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deps[taskID]...), nil
}
func (f *fakeWork) AppendTaskEvent(_ context.Context, ev domain.TaskEvent) (domain.TaskEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	ev.Seq = int64(f.seq)
	f.taskEvents = append(f.taskEvents, ev)
	return ev, nil
}
func (f *fakeWork) CreateArtifact(_ context.Context, a domain.Artifact) (domain.Artifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.artifacts[a.ID] = a
	return a, nil
}
func (f *fakeWork) GetArtifact(_ context.Context, id string) (domain.Artifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.artifacts[id]
	if !ok {
		return domain.Artifact{}, fmt.Errorf("artifact %s not found", id)
	}
	return a, nil
}
func (f *fakeWork) UpdateArtifactStatus(_ context.Context, id, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.artifacts[id]
	a.Status = status
	f.artifacts[id] = a
	return nil
}
func (f *fakeWork) CreateArtifactVersion(_ context.Context, v domain.ArtifactVersion) (domain.ArtifactVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.versions {
		if existing.ArtifactID == v.ArtifactID && (existing.Version == v.Version || existing.ContentHash == v.ContentHash) {
			return domain.ArtifactVersion{}, fmt.Errorf("%w: duplicate version/hash", domain.NewError(domain.CodeConflict, "duplicate version", 409))
		}
	}
	f.versions = append(f.versions, v)
	return v, nil
}
func (f *fakeWork) GetArtifactVersion(_ context.Context, artifactID string, version int) (domain.ArtifactVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.versions {
		if v.ArtifactID == artifactID && v.Version == version {
			return v, nil
		}
	}
	return domain.ArtifactVersion{}, fmt.Errorf("version not found")
}
func (f *fakeWork) ListArtifactVersions(_ context.Context, artifactID string) ([]domain.ArtifactVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ArtifactVersion
	for _, v := range f.versions {
		if v.ArtifactID == artifactID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}
func (f *fakeWork) ListArtifactsByTask(_ context.Context, taskID string) ([]domain.Artifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Artifact
	for _, a := range f.artifacts {
		if a.TaskID == taskID {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *fakeWork) CreateReview(_ context.Context, r domain.Review) (domain.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviews[r.ID] = r
	return r, nil
}
func (f *fakeWork) GetReview(_ context.Context, id string) (domain.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.reviews[id]
	if !ok {
		return domain.Review{}, fmt.Errorf("review %s not found", id)
	}
	return r, nil
}
func (f *fakeWork) ListReviewsByArtifact(_ context.Context, artifactID string) ([]domain.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Review
	for _, r := range f.reviews {
		if r.ArtifactID == artifactID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeWork) CreateReviewVerdict(_ context.Context, v domain.ReviewVerdict) (domain.ReviewVerdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verdicts = append(f.verdicts, v)
	return v, nil
}
func (f *fakeWork) ListReviewVerdicts(_ context.Context, reviewID string) ([]domain.ReviewVerdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ReviewVerdict
	for _, v := range f.verdicts {
		if v.ReviewID == reviewID {
			out = append(out, v)
		}
	}
	return out, nil
}
func (f *fakeWork) UpdateReviewStatus(_ context.Context, id, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.reviews[id]
	r.Status = status
	f.reviews[id] = r
	return nil
}
func (f *fakeWork) CreateDecision(_ context.Context, d domain.Decision) (domain.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decisions[d.ID] = d
	return d, nil
}
func (f *fakeWork) GetDecision(_ context.Context, id string) (domain.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.decisions[id]
	if !ok {
		return domain.Decision{}, fmt.Errorf("decision %s not found", id)
	}
	return d, nil
}
func (f *fakeWork) ListDecisionsByStatus(_ context.Context, status string) ([]domain.Decision, error) {
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
func (f *fakeWork) ListDecisionsByTask(_ context.Context, taskID string) ([]domain.Decision, error) {
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
func (f *fakeWork) SupersedeDecision(_ context.Context, oldID, newID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.decisions[oldID]
	d.Status = domain.DecisionSuperseded
	d.SupersededBy = newID
	f.decisions[oldID] = d
	return nil
}
func (f *fakeWork) CreateApproval(_ context.Context, a domain.Approval) (domain.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approvals[a.ID] = a
	return a, nil
}
func (f *fakeWork) GetApproval(_ context.Context, id string) (domain.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.approvals[id]
	if !ok {
		return domain.Approval{}, fmt.Errorf("approval %s not found", id)
	}
	return a, nil
}
func (f *fakeWork) DecideApproval(_ context.Context, id, decision, decidedBy, reason string) (domain.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.approvals[id]
	a.Status = decision
	a.DecidedBy = decidedBy
	a.DecisionReason = reason
	f.approvals[id] = a
	return a, nil
}
func (f *fakeWork) AppendEvent(_ context.Context, ev domain.Event) (domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	ev.Seq = int64(f.seq)
	f.events = append(f.events, ev)
	return ev, nil
}
func (f *fakeWork) AppendAudit(_ context.Context, a domain.AuditEntry) (domain.AuditEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, a)
	return a, nil
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

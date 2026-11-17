package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// orgFakeData 存 fakeStore 的 R3 数据（避免改 fake_test.go 的字段）。
type orgFakeData struct {
	blueprints    map[string]domain.Blueprint
	provisionings map[string]domain.Provisioning
	approvals     map[string]domain.Approval
	intent        domain.Intent
	failAgentAt   int
	agentCalls    int
}

var orgStates sync.Map // *fakeStore -> *orgFakeData

func orgState(s *fakeStore) *orgFakeData {
	if v, ok := orgStates.Load(s); ok {
		return v.(*orgFakeData)
	}
	d := &orgFakeData{
		blueprints:    map[string]domain.Blueprint{},
		provisionings: map[string]domain.Provisioning{},
		approvals:     map[string]domain.Approval{},
		intent:        domain.Intent{ID: "intent_1", Version: 1, Content: `{"goal":"做本地知识库"}`, Status: domain.IntentConfirmed},
	}
	actual, _ := orgStates.LoadOrStore(s, d)
	return actual.(*orgFakeData)
}

func (s *fakeStore) GetIntentContent(_ context.Context) (domain.Intent, error) {
	return orgState(s).intent, nil
}

func (s *fakeStore) CreateBlueprint(_ context.Context, bp domain.Blueprint) (domain.Blueprint, error) {
	orgState(s).blueprints[bp.ID] = bp
	return bp, nil
}

func (s *fakeStore) GetBlueprint(_ context.Context, id string) (domain.Blueprint, error) {
	bp, ok := orgState(s).blueprints[id]
	if !ok {
		return domain.Blueprint{}, fmt.Errorf("blueprint %s not found", id)
	}
	return bp, nil
}

func (s *fakeStore) UpdateBlueprintStatus(_ context.Context, id, status, approvedAt string) error {
	bp, ok := orgState(s).blueprints[id]
	if !ok {
		return fmt.Errorf("blueprint %s not found", id)
	}
	bp.Status = status
	if approvedAt != "" {
		bp.ApprovedAt = approvedAt
	}
	orgState(s).blueprints[id] = bp
	return nil
}

func (s *fakeStore) CreateProvisioning(_ context.Context, p domain.Provisioning) (domain.Provisioning, error) {
	orgState(s).provisionings[p.ID] = p
	return p, nil
}

func (s *fakeStore) GetProvisioning(_ context.Context, id string) (domain.Provisioning, error) {
	p, ok := orgState(s).provisionings[id]
	if !ok {
		return domain.Provisioning{}, fmt.Errorf("provisioning %s not found", id)
	}
	return p, nil
}

func (s *fakeStore) UpdateProvisioning(_ context.Context, p domain.Provisioning) error {
	orgState(s).provisionings[p.ID] = p
	return nil
}

func (s *fakeStore) CreateApproval(_ context.Context, a domain.Approval) (domain.Approval, error) {
	if a.ID == "" {
		a.ID = domain.NewID("appr")
	}
	if a.Status == "" {
		a.Status = domain.ApprovalPending
	}
	orgState(s).approvals[a.ID] = a
	return a, nil
}

func (s *fakeStore) GetApproval(_ context.Context, id string) (domain.Approval, error) {
	a, ok := orgState(s).approvals[id]
	if !ok {
		return domain.Approval{}, fmt.Errorf("approval %s not found", id)
	}
	return a, nil
}

func (s *fakeStore) DecideApproval(_ context.Context, id, decision, decidedBy, reason string) (domain.Approval, error) {
	a, ok := orgState(s).approvals[id]
	if !ok {
		return domain.Approval{}, fmt.Errorf("approval %s not found", id)
	}
	a.Status = decision
	a.DecidedBy = decidedBy
	a.DecisionReason = reason
	a.DecidedAt = time.Now().UTC().Format(time.RFC3339Nano)
	orgState(s).approvals[id] = a
	return a, nil
}

func (s *fakeStore) CreateDepartment(_ context.Context, d domain.Department) (domain.Department, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.ID == "" {
		d.ID = domain.NewID("dept")
	}
	if d.Status == "" {
		d.Status = domain.DeptActive
	}
	if d.Charter == "" {
		d.Charter = "{}"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if d.CreatedAt == "" {
		d.CreatedAt = now
	}
	if d.UpdatedAt == "" {
		d.UpdatedAt = now
	}
	s.depts[d.ID] = d
	return d, nil
}

func (s *fakeStore) UpdateDepartmentStatus(_ context.Context, id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.depts[id]
	if !ok {
		return fmt.Errorf("department %s not found", id)
	}
	d.Status = status
	s.depts[id] = d
	return nil
}

func (s *fakeStore) UpdateDepartment(_ context.Context, d domain.Department) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.depts[d.ID] = d
	return nil
}

func (s *fakeStore) ListDepartmentsByStatus(_ context.Context, status string) ([]domain.Department, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Department
	for _, d := range s.depts {
		if status == "" || d.Status == status {
			out = append(out, d)
		}
	}
	return out, nil
}

func (s *fakeStore) CreateAgent(_ context.Context, a domain.Agent) (domain.Agent, error) {
	d := orgState(s)
	d.agentCalls++
	if d.failAgentAt > 0 && d.agentCalls == d.failAgentAt {
		return domain.Agent{}, fmt.Errorf("injected agent create failure at %d", d.failAgentAt)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		a.ID = domain.NewID("agent")
	}
	if a.Status == "" {
		a.Status = domain.AgentIdle
	}
	s.agents[a.ID] = a
	return a, nil
}

func (s *fakeStore) UpdateAgent(_ context.Context, a domain.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[a.ID] = a
	return nil
}

func (s *fakeStore) CreateConversationForOrg(_ context.Context, c domain.Conversation, members []domain.ConversationMember, _, _ string) (domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ID == "" {
		c.ID = domain.NewID("conv")
	}
	if c.Status == "" {
		c.Status = "active"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if c.CreatedAt == "" {
		c.CreatedAt = now
	}
	if c.UpdatedAt == "" {
		c.UpdatedAt = now
	}
	s.convs[c.ID] = c
	for _, m := range members {
		m.ConversationID = c.ID
		if m.JoinedAt == "" {
			m.JoinedAt = now
		}
		s.members[c.ID] = append(s.members[c.ID], m)
	}
	return c, nil
}

func (s *fakeStore) AddConversationMember(_ context.Context, m domain.ConversationMember) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members[m.ConversationID] = append(s.members[m.ConversationID], m)
	return nil
}

func (s *fakeStore) UpdateConversation(_ context.Context, c domain.Conversation) (domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.convs[c.ID]; !ok {
		return domain.Conversation{}, fmt.Errorf("conversation %s not found", c.ID)
	}
	s.convs[c.ID] = c
	return c, nil
}

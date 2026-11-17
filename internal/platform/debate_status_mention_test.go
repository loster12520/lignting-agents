package platform_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

type fakeMentionQueue struct {
	status    core.MentionQueueStatus
	stopped   int
	stopCalls int
}

func (f *fakeMentionQueue) MentionQueueStatusFor(conversationID string) core.MentionQueueStatus {
	_ = conversationID
	return f.status
}

func (f *fakeMentionQueue) StopMentionQueue(conversationID string) int {
	_ = conversationID
	f.stopCalls++
	f.status.PendingJobs = 0
	f.status.Stopped = true
	f.status.Running = false
	return f.stopped
}

func TestDebateStatusAPI(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()
	e.srv.SetDebateService(core.NewDebateService(e.st))

	sup, err := e.st.CreateAgent(ctx, domain.Agent{Name: "主管", Kind: domain.AgentKindSupervisor, DepartmentID: "dept_x"})
	if err != nil {
		t.Fatalf("agent: %v", err)
	}
	conv, err := e.st.CreateConversation(ctx, domain.Conversation{
		Type: domain.ConvCrossDept, Title: "辩论", Visibility: domain.VisibilityProject, CreatedBy: "owner",
	}, []domain.ConversationMember{{PrincipalType: domain.SenderAgent, PrincipalID: sup.ID}}, store.Actor{Type: "system", ID: "core"})
	if err != nil {
		t.Fatalf("conv: %v", err)
	}
	status, out := e.doCSRF("POST", "/api/conversations/"+conv.ID+"/stances", map[string]any{
		"actor_agent_id": sup.ID, "debate_id": "debate_1", "round": 1, "stance": "REST", "evidence_ids": []string{"e1"},
	})
	if status != http.StatusOK {
		t.Fatalf("stance = %d %v", status, out)
	}
	status, out = e.do("GET", "/api/debates/debate_1/status?conversation_id="+conv.ID, nil)
	d := e.mustOK(status, out)
	if d["submissions"].(float64) != 1 || d["round"].(float64) != 1 || d["debate_id"] != "debate_1" {
		t.Fatalf("debate status = %v", d)
	}
	if d["timebox_seconds"].(float64) <= 0 {
		t.Fatalf("timebox_seconds = %v", d["timebox_seconds"])
	}
	// 缺 conversation_id → 400
	if status, _ := e.do("GET", "/api/debates/debate_1/status", nil); status != http.StatusBadRequest {
		t.Fatalf("missing conversation_id = %d, want 400", status)
	}
}

func TestMentionQueueAPI(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()
	// 未注入 → 503
	if status, _ := e.do("GET", "/api/conversations/"+e.dmID+"/mention-queue", nil); status != http.StatusServiceUnavailable {
		t.Fatalf("mention queue without service = %d, want 503", status)
	}
	fq := &fakeMentionQueue{status: core.MentionQueueStatus{
		ConversationID: e.dmID, Running: true, PendingJobs: 2, CurrentAgentID: "agent_a", Limit: 2,
	}, stopped: 2}
	e.srv.SetMentionQueueService(fq)
	status, out := e.do("GET", "/api/conversations/"+e.dmID+"/mention-queue", nil)
	d := e.mustOK(status, out)
	if d["pending_jobs"].(float64) != 2 || d["running"] != true || d["limit"].(float64) != 2 {
		t.Fatalf("mention queue status = %v", d)
	}
	status, out = e.doCSRF("POST", "/api/conversations/"+e.dmID+"/mention-queue/stop", nil)
	d = e.mustOK(status, out)
	if d["discarded"].(float64) != 2 || d["stopped"] != true {
		t.Fatalf("mention queue stop = %v", d)
	}
	if fq.stopCalls != 1 {
		t.Fatalf("stop calls = %d", fq.stopCalls)
	}
	// 停止后状态
	status, out = e.do("GET", "/api/conversations/"+e.dmID+"/mention-queue", nil)
	d = e.mustOK(status, out)
	if d["stopped"] != true || d["pending_jobs"].(float64) != 0 {
		t.Fatalf("status after stop = %v", d)
	}
	// 审计
	audits, err := e.st.QueryAudit(ctx, store.AuditFilter{Action: "mention_queue.stop"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("stop audit = %d err=%v", len(audits), err)
	}
}

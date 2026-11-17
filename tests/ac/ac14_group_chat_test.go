//go:build acceptance

package ac

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/tests/fixtures"
)

// TestAC14GroupChatAndAgentLifecycle covers the R3-available part of AC-14:
// project/temp group creation, members, visibility/title patch, messages
// persisted + searchable, observer join/leave, agent pause/resume + role card
// visibility, audit. Still R6/R8: @ mention reply queue, cross-department DM
// rejection paths, expiring bridge, Agent stop (needs runtime).
func TestAC14GroupChatAndAgentLifecycle(t *testing.T) {
	h := NewHarness(t, WithSeed(), WithCoreServices())
	h.EnsureSession()

	type conv struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Title      string `json:"title"`
		Visibility string `json:"visibility"`
		ExpiresAt  string `json:"expires_at"`
		Members    []struct {
			PrincipalType string `json:"principal_type"`
			PrincipalID   string `json:"principal_id"`
			Role          string `json:"role"`
		} `json:"members"`
		MemberCount int `json:"member_count"`
	}

	// 1) Project group chat with three department leads.
	var project conv
	h.PostData(t, "/api/conversations", map[string]any{
		"type": "project", "title": "AC-14 项目群", "visibility": "project",
		"member_ids": []string{fixtures.AgentProdLeadID, fixtures.AgentDevLeadID, fixtures.AgentTestLeadID},
	}, &project)
	if project.ID == "" || project.Type != "project" || project.Title != "AC-14 项目群" {
		t.Fatalf("project conversation = %+v", project)
	}
	if len(project.Members) != 4 || project.MemberCount != 4 {
		t.Fatalf("project members = %d/%d, want owner + 3 leads", len(project.Members), project.MemberCount)
	}
	memberSet := map[string]bool{}
	for _, m := range project.Members {
		memberSet[m.PrincipalID] = true
	}
	for _, id := range []string{"owner", fixtures.AgentProdLeadID, fixtures.AgentDevLeadID, fixtures.AgentTestLeadID} {
		if !memberSet[id] {
			t.Errorf("project member missing %s", id)
		}
	}

	// 2) Temporary discussion group with expiry.
	var temp conv
	h.PostData(t, "/api/conversations", map[string]any{
		"type": "temp", "title": "AC-14 临时讨论", "visibility": "org",
		"member_ids": []string{fixtures.AgentDevAID, fixtures.AgentDevBID},
		"expires_at": "2030-12-31T00:00:00Z",
	}, &temp)
	if temp.Type != "temp" || temp.ExpiresAt == "" {
		t.Fatalf("temp conversation = %+v", temp)
	}

	// 3) Patch title/visibility/members; membership is replace-set.
	var patched conv
	h.ExpectStatus(t, "PATCH", "/api/conversations/"+temp.ID, map[string]any{
		"title": "AC-14 临时讨论（改）", "visibility": "project",
		"member_ids": []string{fixtures.AgentDevAID, fixtures.AgentTestEmpID},
	}, 200).Data(t, &patched)
	if patched.Title != "AC-14 临时讨论（改）" || patched.Visibility != "project" {
		t.Fatalf("patched conversation = %+v", patched)
	}
	if len(patched.Members) != 3 { // owner + 2 agents
		t.Fatalf("patched members = %d, want 3", len(patched.Members))
	}

	// 4) Messages are persisted and searchable; @ mentions are stored verbatim
	// (reply queue itself is R6 and asserted as a remaining gap below).
	post := func(content string) {
		t.Helper()
		var out struct {
			Message struct {
				ID  string `json:"id"`
				Seq int64  `json:"seq"`
			} `json:"message"`
		}
		h.PostData(t, "/api/conversations/"+project.ID+"/messages", map[string]any{"content": content}, &out)
		if out.Message.ID == "" || out.Message.Seq <= 0 {
			t.Fatalf("post message = %+v", out)
		}
	}
	post("@研发主管 @测试主管 请同步接口字段；群聊检索关键字：蓝鲸。")
	post("AC-14 第二条消息：观察者默认不参与。")

	var msgs struct {
		Items []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/conversations/"+project.ID+"/messages", &msgs)
	if msgs.Total != 2 {
		t.Fatalf("project messages = %d, want 2", msgs.Total)
	}
	var hits struct {
		Items []struct {
			Kind  string `json:"kind"`
			RefID string `json:"ref_id"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/search?"+url.Values{"q": {"蓝鲸"}}.Encode(), &hits)
	if hits.Total < 1 {
		t.Fatalf("group message not searchable: total=%d", hits.Total)
	}

	// 5) Observer on a seeded department chat: readable, join/leave audited.
	deptConvID := h.Seed.Conversations["conv_dept_dev"]
	if deptConvID == "" {
		t.Fatal("seeded engineering department conversation missing")
	}
	var deptConv conv
	h.GetData(t, "/api/conversations/"+deptConvID, &deptConv)
	if deptConv.ID != deptConvID {
		t.Fatalf("dept conversation = %+v", deptConv)
	}
	observer := false
	for _, m := range deptConv.Members {
		if m.PrincipalType == "user" && m.Role == "observer" {
			observer = true
		}
	}
	if !observer {
		t.Fatalf("owner is not observer in department chat: %+v", deptConv.Members)
	}
	h.ExpectStatus(t, "POST", "/api/conversations/"+deptConvID+"/join", map[string]any{}, 200)
	h.ExpectStatus(t, "POST", "/api/conversations/"+deptConvID+"/leave", map[string]any{}, 200)

	// 6) Agent lifecycle + visible role card/perspective/budget.
	var agent struct {
		ID          string  `json:"id"`
		Kind        string  `json:"kind"`
		Status      string  `json:"status"`
		RoleCard    string  `json:"role_card"`
		Perspective string  `json:"perspective"`
		BudgetLimit float64 `json:"budget_limit"`
	}
	h.GetData(t, "/api/agents/"+fixtures.AgentDevLeadID, &agent)
	if agent.ID != fixtures.AgentDevLeadID || agent.RoleCard == "" || agent.Perspective == "" {
		t.Fatalf("agent detail = %+v", agent)
	}
	var paused struct {
		Status string `json:"status"`
	}
	h.PostData(t, "/api/agents/"+fixtures.AgentDevLeadID+"/pause", map[string]any{"reason": "AC-14"}, &paused)
	if paused.Status != "paused" {
		t.Fatalf("agent pause status = %q", paused.Status)
	}
	var resumed struct {
		Status string `json:"status"`
	}
	h.PostData(t, "/api/agents/"+fixtures.AgentDevLeadID+"/resume", map[string]any{}, &resumed)
	if resumed.Status != "idle" {
		t.Fatalf("agent resume status = %q", resumed.Status)
	}
	// The harness wires a mock-backed core Runtime, so stopping an idle Agent
	// is a domain-level conflict (no running turn), not a transport error.
	// Stopping an actually-running turn is covered by the core runtime tests;
	// the acceptance layer only proves the platform path + error contract.
	stop := h.ExpectStatus(t, "POST", "/api/agents/"+fixtures.AgentDevLeadID+"/stop", map[string]any{"reason": "AC-14"}, 409)
	if body := string(stop.Body); !strings.Contains(body, "没有运行中的 turn") {
		t.Fatalf("idle stop body = %s, want no-running-turn conflict", body)
	}

	// 7) Audit: conversation create/update/join/leave + agent lifecycle.
	audits, err := h.Store.QueryAudit(context.Background(), store.AuditFilter{Limit: 300})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]bool{}
	for _, a := range audits {
		actions[a.Action] = true
	}
	for _, want := range []string{"conversation.join", "conversation.leave", "org.agent.pause", "org.agent.resume"} {
		if !actions[want] {
			t.Errorf("audit missing %q", want)
		}
	}
	if _, err := h.Store.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	t.Log("AC-14 R3 passed: project/temp group, members/patch, messages searchable, observer join/leave, agent pause/resume, audit")
	t.Run("mention_queue_dm_isolation_bridge_live_stop", func(t *testing.T) {
		ctx := context.Background()
		projectConv := h.Seed.Conversations[fixtures.ConversationProjectID]
		dmConv := h.Seed.Conversations[fixtures.ConversationDMID]
		postMention := func(convID string, mentions []string) domain.Message {
			t.Helper()
			var out struct {
				Message domain.Message `json:"message"`
			}
			h.PostData(t, "/api/conversations/"+convID+"/messages", map[string]any{
				"content": "@ 队列测试", "meta": map[string]any{"mentions": mentions},
			}, &out)
			return out.Message
		}
		enqueue := func(msg domain.Message) {
			t.Helper()
			if err := h.Runtime.EnqueueUserMessage(ctx, msg); err != nil {
				t.Fatalf("enqueue mention: %v", err)
			}
		}
		waitIdle := func(convID string) {
			t.Helper()
			waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := h.Runtime.WaitMentionQueueIdle(waitCtx, convID); err != nil {
				t.Fatalf("mention queue not idle: %v", err)
			}
		}
		agentMessages := func(convID string) []struct {
			SenderID string `json:"sender_id"`
			Content  string `json:"content"`
		} {
			t.Helper()
			var out struct {
				Items []struct {
					SenderType string `json:"sender_type"`
					SenderID   string `json:"sender_id"`
					Content    string `json:"content"`
				} `json:"items"`
			}
			h.GetData(t, "/api/conversations/"+convID+"/messages?limit=200", &out)
			items := make([]struct {
				SenderID string `json:"sender_id"`
				Content  string `json:"content"`
			}, 0)
			for _, m := range out.Items {
				if m.SenderType == "agent" {
					items = append(items, struct {
						SenderID string `json:"sender_id"`
						Content  string `json:"content"`
					}{SenderID: m.SenderID, Content: m.Content})
				}
			}
			return items
		}

		// @ queue: order and per-turn limit (default 2) are observable.
		before := len(agentMessages(projectConv))
		enqueue(postMention(projectConv, []string{fixtures.AgentDevBID, fixtures.AgentDevAID, fixtures.AgentProdLeadID}))
		waitIdle(projectConv)
		msgs := agentMessages(projectConv)
		if len(msgs) != before+2 {
			t.Fatalf("mention replies = %d, want exactly 2 (limit)", len(msgs)-before)
		}
		if msgs[before].SenderID != fixtures.AgentDevBID || msgs[before+1].SenderID != fixtures.AgentDevAID {
			t.Fatalf("mention order = %s,%s want dev_b,dev_a", msgs[before].SenderID, msgs[before+1].SenderID)
		}
		var status struct {
			ConversationID string `json:"conversation_id"`
			Running        bool   `json:"running"`
			PendingJobs    int    `json:"pending_jobs"`
			Limit          int    `json:"limit"`
			Stopped        bool   `json:"stopped"`
		}
		h.GetData(t, "/api/conversations/"+projectConv+"/mention-queue", &status)
		if status.ConversationID != projectConv || status.Limit != 2 || status.Running || status.PendingJobs != 0 {
			t.Fatalf("mention queue status = %+v", status)
		}
		var stopped struct {
			Stopped   bool `json:"stopped"`
			Discarded int  `json:"discarded"`
		}
		h.PostData(t, "/api/conversations/"+projectConv+"/mention-queue/stop", map[string]any{}, &stopped)
		if !stopped.Stopped {
			t.Fatalf("mention queue stop = %+v", stopped)
		}
		// The queue is re-created for the next message after a stop.
		enqueue(postMention(projectConv, []string{fixtures.AgentDevAID}))
		waitIdle(projectConv)
		if got := len(agentMessages(projectConv)); got != before+3 {
			t.Fatalf("mention after stop = %d replies, want 3 total", got-before)
		}

		// Default isolation: a department lead cannot enter the owner DM ...
		devLeadBefore := 0
		for _, m := range agentMessages(dmConv) {
			if m.SenderID == fixtures.AgentDevLeadID {
				devLeadBefore++
			}
		}
		enqueue(postMention(dmConv, []string{fixtures.AgentDevLeadID}))
		waitIdle(dmConv)
		afterDenied := 0
		for _, m := range agentMessages(dmConv) {
			if m.SenderID == fixtures.AgentDevLeadID {
				afterDenied++
			}
		}
		if afterDenied != devLeadBefore {
			t.Fatalf("department lead got into owner DM without bridge: %d -> %d", devLeadBefore, afterDenied)
		}

		// ... until a lead_to_user bridge is opened; closing it restores isolation.
		var bridge struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			ExpiresAt string `json:"expires_at"`
		}
		h.PostData(t, "/api/bridges", map[string]any{
			"scope": "lead_to_user", "from_type": "agent", "from_id": fixtures.AgentDevLeadID,
			"to_type": "user", "to_id": fixtures.OwnerID,
			"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339), "reason": "AC-14 临时桥",
		}, &bridge)
		if bridge.ID == "" || bridge.Status != "active" {
			t.Fatalf("bridge = %+v", bridge)
		}
		enqueue(postMention(dmConv, []string{fixtures.AgentDevLeadID}))
		waitIdle(dmConv)
		afterBridge := 0
		for _, m := range agentMessages(dmConv) {
			if m.SenderID == fixtures.AgentDevLeadID {
				afterBridge++
			}
		}
		if afterBridge != devLeadBefore+1 {
			t.Fatalf("bridged lead reply = %d, want %d", afterBridge, devLeadBefore+1)
		}
		var closed struct {
			Status string `json:"status"`
		}
		h.ExpectStatus(t, "DELETE", "/api/bridges/"+bridge.ID, nil, 200).Data(t, &closed)
		if closed.Status != "closed" {
			t.Fatalf("closed bridge = %+v", closed)
		}
		enqueue(postMention(dmConv, []string{fixtures.AgentDevLeadID}))
		waitIdle(dmConv)
		afterClose := 0
		for _, m := range agentMessages(dmConv) {
			if m.SenderID == fixtures.AgentDevLeadID {
				afterClose++
			}
		}
		if afterClose != afterBridge {
			t.Fatalf("closed bridge still allows messages: %d -> %d", afterBridge, afterClose)
		}

		// A bridge expires automatically via the scheduler and is audited.
		var expiring struct {
			ID string `json:"id"`
		}
		h.PostData(t, "/api/bridges", map[string]any{
			"scope": "cross_dept", "from_type": "department", "from_id": fixtures.DeptDevID,
			"to_type": "department", "to_id": fixtures.DeptTestID,
			"expires_at": time.Now().UTC().Add(time.Second).Format(time.RFC3339), "reason": "AC-14 到期桥",
		}, &expiring)
		time.Sleep(2500 * time.Millisecond)
		if _, err := h.Platform.RunSchedulesOnce(ctx); err != nil {
			t.Fatalf("run schedules: %v", err)
		}
		var expired struct {
			Items []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"items"`
		}
		h.GetData(t, "/api/bridges?status=expired", &expired)
		foundExpired := false
		for _, item := range expired.Items {
			if item.ID == expiring.ID && item.Status == "expired" {
				foundExpired = true
			}
		}
		if !foundExpired {
			t.Fatalf("expiring bridge %s not expired: %+v", expiring.ID, expired.Items)
		}
		audits, err := h.Store.QueryAudit(ctx, store.AuditFilter{Action: "bridge.expire", Limit: 10})
		if err != nil || len(audits) == 0 {
			t.Fatalf("bridge.expire audit = %d err=%v", len(audits), err)
		}
		if _, err := h.Store.VerifyAuditChain(ctx); err != nil {
			t.Fatalf("audit chain: %v", err)
		}
		t.Log("AC-14 mentions/bridge passed: order+limit+stop, DM isolation default-deny, bridge allow/close/expire, audit")
	})
}

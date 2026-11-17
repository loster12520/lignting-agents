//go:build acceptance

package ac

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// TestAC01FirstRunAndAssistant covers requirements.md §14 AC-01.
// R1 scope (real): health/bootstrap/setup, assistant + DM + greeting, no
// department before confirmation, audit chain, loopback-only default.
// R2 scope (still skipped): assistant clarification, intent card, single-shot Q&A.
func TestAC01FirstRunAndAssistant(t *testing.T) {
	started := time.Now()
	h := NewHarness(t, WithCoreServices())

	// 1) Fresh data dir: bootstrap asks for setup and exposes no session.
	data := h.Bootstrap()
	if got := data["setup_required"]; got != true {
		t.Fatalf("setup_required = %v, want true", got)
	}
	if got := data["authenticated"]; got != false {
		t.Fatalf("authenticated before setup = %v, want false", got)
	}
	if got, _ := data["csrf_token"].(string); got != "" {
		t.Fatalf("csrf before setup = %q, want empty", got)
	}

	// 2) Setup creates owner + assistant + assistant DM + greeting, one session.
	data = h.Setup("阿哲")
	if got := data["authenticated"]; got != true {
		t.Fatalf("authenticated after setup = %v, want true", got)
	}
	if csrf, _ := data["csrf_token"].(string); csrf == "" {
		t.Fatal("csrf_token missing after setup")
	}
	owner, _ := data["owner"].(map[string]any)
	if owner == nil || owner["name"] != "阿哲" {
		t.Fatalf("owner = %v, want name 阿哲", data["owner"])
	}
	if got := h.Client.csrf; got == "" {
		t.Fatal("client CSRF token not captured from setup/bootstrap")
	}

	// 3) Org has exactly one assistant and no departments yet (FR-ONB-03/AC-01).
	var org struct {
		Owner       map[string]any   `json:"owner"`
		Assistant   map[string]any   `json:"assistant"`
		Departments []map[string]any `json:"departments"`
		Agents      []map[string]any `json:"agents"`
	}
	h.GetData(t, "/api/org", &org)
	if org.Assistant == nil {
		t.Fatal("org has no assistant")
	}
	if len(org.Departments) != 0 {
		t.Fatalf("departments before G1 = %d, want 0", len(org.Departments))
	}
	for _, a := range org.Agents {
		if a["kind"] == "assistant" {
			t.Fatalf("assistant duplicated in agents list: %v", a["id"])
		}
	}

	// 4) Assistant DM exists; greeting states abilities, boundaries, pause and
	//    behavior audit trail (FR-ASST-09 / FR-ONB-03).
	var convs struct {
		Items []struct {
			ID          string           `json:"id"`
			Type        string           `json:"type"`
			Title       string           `json:"title"`
			Members     []map[string]any `json:"members"`
			LastMessage *struct {
				Content    string `json:"content"`
				SenderType string `json:"sender_type"`
				SenderID   string `json:"sender_id"`
			} `json:"last_message"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/conversations?type=dm", &convs)
	if len(convs.Items) != 1 || convs.Total != 1 {
		t.Fatalf("assistant DM count = %d/%d, want exactly 1", len(convs.Items), convs.Total)
	}
	dm := convs.Items[0]
	if dm.Title != "助理" {
		t.Fatalf("DM title = %q, want 助理", dm.Title)
	}
	if len(dm.Members) != 2 {
		t.Fatalf("DM members = %d, want owner+assistant", len(dm.Members))
	}
	if dm.LastMessage == nil || dm.LastMessage.SenderType != "agent" {
		t.Fatalf("DM last message = %+v, want assistant greeting", dm.LastMessage)
	}
	greeting := dm.LastMessage.Content
	for _, want := range []string{"我能做什么", "我不做什么", "如何暂停我", "如何查看我的行为"} {
		if !strings.Contains(greeting, want) {
			t.Errorf("greeting missing %q", want)
		}
	}

	// 5) Message list exposes the greeting with stable seq and agent sender.
	var msgs struct {
		Items []struct {
			Seq        int64  `json:"seq"`
			ID         string `json:"id"`
			SenderType string `json:"sender_type"`
			SenderID   string `json:"sender_id"`
			Content    string `json:"content"`
		} `json:"items"`
		Total  int64 `json:"total"`
		MaxSeq int64 `json:"max_seq"`
	}
	h.GetData(t, "/api/conversations/"+dm.ID+"/messages", &msgs)
	if msgs.Total != 1 || len(msgs.Items) != 1 {
		t.Fatalf("DM message count = %d/%d, want 1", len(msgs.Items), msgs.Total)
	}
	msg := msgs.Items[0]
	if msg.SenderType != "agent" || msg.SenderID != org.Assistant["id"] {
		t.Fatalf("greeting sender = %s/%s, want agent/%v", msg.SenderType, msg.SenderID, org.Assistant["id"])
	}
	if msg.Seq <= 0 || msgs.MaxSeq != msg.Seq {
		t.Fatalf("greeting seq = %d, max_seq = %d", msg.Seq, msgs.MaxSeq)
	}

	// 6) Setup + greeting are audited; audit hash chain verifies.
	audits, err := h.Store.QueryAudit(context.Background(), store.AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]bool{}
	for _, a := range audits {
		actions[a.Action] = true
	}
	for _, want := range []string{"setup", "message.create"} {
		if !actions[want] {
			t.Errorf("audit missing action %q (got %v)", want, actions)
		}
	}
	if n, err := h.Store.VerifyAuditChain(context.Background()); err != nil || n == 0 {
		t.Fatalf("verify audit chain: n=%d err=%v", n, err)
	}

	// 7) Write without CSRF is rejected even for the local owner.
	saved := h.Client.csrf
	h.Client.csrf = ""
	h.ExpectStatus(t, "POST", "/api/conversations", map[string]any{"type": "project", "title": "x"}, 403)
	h.Client.csrf = saved

	// 8) Restart is idempotent: no duplicate assistant/DM, still authenticated.
	h.Restart()
	data = h.EnsureSession()
	if got := data["setup_required"]; got != false {
		t.Fatalf("setup_required after restart = %v, want false", got)
	}
	var convsAfter struct {
		Items []map[string]any `json:"items"`
	}
	h.GetData(t, "/api/conversations?type=dm", &convsAfter)
	if len(convsAfter.Items) != 1 {
		t.Fatalf("DM after restart = %d, want 1", len(convsAfter.Items))
	}
	var agentsAfter struct {
		Items []map[string]any `json:"items"`
	}
	h.GetData(t, "/api/agents?kind=assistant", &agentsAfter)
	if len(agentsAfter.Items) != 1 {
		t.Fatalf("assistant after restart = %d, want 1", len(agentsAfter.Items))
	}

	t.Logf("T1 (install -> assistant DM) in-process elapsed: %s (manual browser timing required for release)", time.Since(started))

	// R2: intent card + one-shot Q&A do not create departments/tasks.
	t.Run("intent_card_and_single_question_R2", func(t *testing.T) {
		ctx := context.Background()
		var intent struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
			Content string `json:"content"`
			Status  string `json:"status"`
		}
		h.PostData(t, "/api/intents", map[string]any{
			"content": map[string]any{
				"goal":      "AC-01 把 CLI 工具做成 Web 产品",
				"scope":     []string{"登录", "导出"},
				"non_goals": []string{"移动端"},
			},
		}, &intent)
		if intent.ID == "" || intent.Version < 1 || !strings.Contains(intent.Content, "AC-01") {
			t.Fatalf("intent = %+v", intent)
		}
		var latest struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
			Content string `json:"content"`
			Status  string `json:"status"`
		}
		h.GetData(t, "/api/intents/latest", &latest)
		if latest.ID != intent.ID || latest.Version != intent.Version || !strings.Contains(latest.Content, "登录") {
			t.Fatalf("latest intent = %+v, want %s v%d", latest, intent.ID, intent.Version)
		}

		// A single question gets an assistant reply, but creates no task/department.
		before, err := h.Store.CountMessages(ctx, dm.ID)
		if err != nil {
			t.Fatalf("count messages: %v", err)
		}
		var posted struct {
			Message domain.Message `json:"message"`
		}
		h.PostData(t, "/api/conversations/"+dm.ID+"/messages", map[string]any{
			"content": "这个功能大概需要几天？只回答，不要建部门或任务。",
		}, &posted)
		if err := h.Runtime.EnqueueUserMessage(ctx, posted.Message); err != nil {
			t.Fatalf("enqueue single question: %v", err)
		}
		WaitFor(t, 10*time.Second, 50*time.Millisecond, func() error {
			n, err := h.Store.CountMessages(ctx, dm.ID)
			if err != nil {
				return err
			}
			if n < before+2 {
				return fmt.Errorf("assistant reply not persisted yet: messages=%d want>=%d", n, before+2)
			}
			return nil
		})
		var after struct {
			Items []struct {
				SenderType string `json:"sender_type"`
				SenderID   string `json:"sender_id"`
				Content    string `json:"content"`
			} `json:"items"`
			Total int64 `json:"total"`
		}
		h.GetData(t, "/api/conversations/"+dm.ID+"/messages?limit=100", &after)
		if after.Total < before+2 || len(after.Items) < 2 {
			t.Fatalf("DM messages after Q&A = %d/%d", after.Total, len(after.Items))
		}
		last := after.Items[len(after.Items)-1]
		if last.SenderType != "agent" || last.SenderID != org.Assistant["id"] || strings.TrimSpace(last.Content) == "" {
			t.Fatalf("single-question reply = %+v, want non-empty assistant answer", last)
		}
		// 只断言结构：真实模型模式下不校验措辞（见 docs/testing/ac-guide.md 断言原则）。
		if h.Mock && !strings.Contains(last.Content, "（mock）") {
			t.Fatalf("mock 模式回复缺（mock）标记 = %+v", last)
		}
		var tasks struct {
			Total int64 `json:"total"`
		}
		h.GetData(t, "/api/tasks", &tasks)
		if tasks.Total != 0 {
			t.Fatalf("single question created tasks: total=%d", tasks.Total)
		}
		var orgAfter struct {
			Departments []map[string]any `json:"departments"`
		}
		h.GetData(t, "/api/org", &orgAfter)
		if len(orgAfter.Departments) != 0 {
			t.Fatalf("single question created departments: %v", orgAfter.Departments)
		}
		t.Log("AC-01 R2 passed: intent card persisted; single question answered without creating task/department")
	})
}

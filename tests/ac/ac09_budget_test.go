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
	"github.com/lignting/lignting-agents/tests/fixtures"
)

// TestAC09BudgetAndPause covers AC-09 with the real platform budget/usage API
// plus the core Runtime budget guard: seeded usage -> 80% warning -> 100%
// budget_blocked turn -> added budget resumes -> global emergency stop gates
// new turns. Cost attribution is read back from /api/usage.
func TestAC09BudgetAndPause(t *testing.T) {
	h := NewHarness(t, WithSeed(), WithCoreServices())
	h.EnsureSession()
	ctx := context.Background()

	// Seeded usage_ledger: 12k+4k prompt, 3k+0.8k completion, CNY 0.10.
	var totals struct {
		PromptTokens     int64   `json:"PromptTokens"`
		CompletionTokens int64   `json:"CompletionTokens"`
		CachedTokens     int64   `json:"CachedTokens"`
		Cost             float64 `json:"Cost"`
	}
	h.GetData(t, "/api/usage", &totals)
	if totals.PromptTokens != 16000 || totals.CompletionTokens != 3800 || totals.CachedTokens != 1000 || totals.Cost < 0.099 {
		t.Fatalf("usage totals = %+v, want seeded 16000/3800/1000/0.10", totals)
	}
	var byDept struct {
		Items []struct {
			DepartmentID string  `json:"department_id"`
			Cost         float64 `json:"cost"`
		} `json:"items"`
	}
	h.GetData(t, "/api/usage?group_by=department", &byDept)
	var deptCost float64
	var devCost float64
	for _, item := range byDept.Items {
		deptCost += item.Cost
		if item.DepartmentID == fixtures.DeptDevID {
			devCost += item.Cost
		}
	}
	if deptCost < 0.099 || devCost < 0.079 {
		t.Fatalf("department usage = %+v, want total 0.10 / dev 0.08", byDept.Items)
	}
	for _, group := range []string{"task", "agent", "model", "day"} {
		var out struct {
			Total int64 `json:"total"`
		}
		h.GetData(t, "/api/usage?group_by="+group, &out)
		if out.Total == 0 {
			t.Fatalf("usage group_by=%s returned no rows", group)
		}
	}

	putBudgets := func(daily float64) {
		t.Helper()
		var out struct {
			DailyCNY float64 `json:"daily_cny"`
		}
		resp, err := h.Client.Do(ctx, "PUT", "/api/settings/budgets", map[string]any{
			"daily_cny": daily, "monthly_cny": 100,
		})
		if err != nil {
			t.Fatalf("put budgets: %v", err)
		}
		resp.MustStatus(t, 200).Data(t, &out)
		if out.DailyCNY != daily {
			t.Fatalf("daily budget = %v, want %v", out.DailyCNY, daily)
		}
	}
	lastSeq := func() int64 {
		events, err := h.Store.ListEventsSince(ctx, 0, "", 1000)
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		var max int64
		for _, ev := range events {
			if ev.Seq > max {
				max = ev.Seq
			}
		}
		return max
	}
	waitEvent := func(typ string, since int64) domain.Event {
		t.Helper()
		var found domain.Event
		WaitFor(t, 10*time.Second, 50*time.Millisecond, func() error {
			events, err := h.Store.ListEventsSince(ctx, since, "", 2000)
			if err != nil {
				return err
			}
			for _, ev := range events {
				if ev.Type == typ {
					found = ev
					return nil
				}
			}
			return fmt.Errorf("%s event not seen yet", typ)
		})
		return found
	}
	enqueue := func(content string) {
		t.Helper()
		var posted struct {
			Message domain.Message `json:"message"`
		}
		h.PostData(t, "/api/conversations/"+fixtures.ConversationDMID+"/messages", map[string]any{"content": content}, &posted)
		if err := h.Runtime.EnqueueUserMessage(ctx, posted.Message); err != nil {
			t.Fatalf("enqueue %q: %v", content, err)
		}
	}
	bumpNotifications := func() int {
		t.Helper()
		if _, err := h.Platform.RunSchedulesOnce(ctx); err != nil {
			t.Fatalf("run schedules: %v", err)
		}
		var list struct {
			Items []struct {
				Level string `json:"level"`
				Title string `json:"title"`
			} `json:"items"`
		}
		h.GetData(t, "/api/notifications", &list)
		n := 0
		for _, it := range list.Items {
			if strings.Contains(it.Title, "预算") {
				n++
			}
		}
		return n
	}

	// 80%: usage 0.10 vs daily 0.12 -> warning event + need_know notification.
	putBudgets(0.12)
	before := lastSeq()
	enqueue("预算 80% 提醒测试")
	warning := waitEvent("budget.warning", before)
	if !strings.Contains(warning.Payload, "global_day") || !strings.Contains(warning.Payload, "0.12") {
		t.Fatalf("budget.warning payload = %s", warning.Payload)
	}
	if n := bumpNotifications(); n != 1 {
		t.Fatalf("budget notifications after 80%% = %d, want 1", n)
	}
	if n := bumpNotifications(); n != 1 {
		t.Fatalf("duplicate budget notification after rescan = %d, want 1", n)
	}

	// 100%: daily 0.05 < used 0.10 -> new turn fails with budget_blocked.
	putBudgets(0.05)
	before = lastSeq()
	enqueue("预算 100% 拒绝测试")
	blocked := waitEvent("budget.blocked", before)
	if !strings.Contains(blocked.Payload, "global_day") {
		t.Fatalf("budget.blocked payload = %s", blocked.Payload)
	}
	waitEvent("turn.failed", before)
	if n := bumpNotifications(); n != 2 {
		t.Fatalf("budget notifications after 100%% = %d, want 2", n)
	}
	var blockedAgent struct {
		Status string `json:"status"`
	}
	h.GetData(t, "/api/agents/"+fixtures.AssistantID, &blockedAgent)
	if blockedAgent.Status != "budget_blocked" {
		t.Fatalf("agent status after budget block = %q, want budget_blocked", blockedAgent.Status)
	}

	// Added budget: the same Agent resumes and completes a turn.
	putBudgets(10)
	before = lastSeq()
	enqueue("追加预算后恢复")
	waitEvent("message.completed", before)
	var resumed struct {
		Status string `json:"status"`
	}
	h.GetData(t, "/api/agents/"+fixtures.AssistantID, &resumed)
	if resumed.Status == "budget_blocked" {
		t.Fatalf("agent still budget_blocked after added budget: %+v", resumed)
	}

	// Emergency stop: the switch stored in settings immediately rejects new turns.
	var stopOn struct {
		EmergencyStop bool `json:"emergency_stop"`
	}
	h.PostData(t, "/api/admin/emergency-stop", map[string]any{"enabled": true}, &stopOn)
	if !stopOn.EmergencyStop {
		t.Fatalf("emergency stop = %+v", stopOn)
	}
	err := h.Runtime.EnqueueUserMessage(ctx, domain.Message{
		ID: "msg_ac09_stop", ConversationID: fixtures.ConversationDMID,
		SenderType: domain.SenderUser, SenderID: fixtures.OwnerID, Content: "紧急停止期间",
	})
	if err == nil || !strings.Contains(err.Error(), "紧急停止") {
		t.Fatalf("enqueue during emergency stop = %v", err)
	}
	var stopOff struct {
		EmergencyStop bool `json:"emergency_stop"`
	}
	h.PostData(t, "/api/admin/emergency-stop", map[string]any{"enabled": false}, &stopOff)
	if stopOff.EmergencyStop {
		t.Fatalf("emergency resume = %+v", stopOff)
	}
	if v, ok, _ := h.Store.Setting(ctx, "emergency_stop"); !ok || v != "false" {
		t.Fatalf("emergency_stop setting = %q ok=%v", v, ok)
	}

	// Audit: budget update/block projection + emergency stop, chain intact.
	for _, action := range []string{"budget.update", "budget.blocked", "admin.emergency_stop"} {
		list, err := h.Store.QueryAudit(ctx, store.AuditFilter{Action: action, Limit: 20})
		if err != nil || len(list) == 0 {
			t.Errorf("audit %s missing: n=%d err=%v", action, len(list), err)
		}
	}
	if _, err := h.Store.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	t.Logf("AC-09 passed: usage attribution, 80%% warning, 100%% block, added-budget resume, emergency stop, audit chain")
}

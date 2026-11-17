//go:build acceptance

package ac

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// TestAC07CrashRestartOffline covers the R1-available part of AC-07:
// write -> SSE event -> idempotent retry -> in-process restart (SQLite reopen +
// new HTTP listener) -> messages/events/search survive, running turn becomes
// interrupted, deleted message stays tombstoned, no duplicate replay.
// Still R2/R8: real process kill, DeepSeek timeout/degradation, 1h disconnect rehearsal.
func TestAC07CrashRestartOffline(t *testing.T) {
	h := NewHarness(t)
	h.Setup("阿哲")

	var conv struct {
		ID string `json:"id"`
	}
	h.PostData(t, "/api/conversations", map[string]any{
		"type": "project", "title": "AC-07 恢复验收",
	}, &conv)

	post := func(content, clientID string) (map[string]any, bool) {
		t.Helper()
		var out struct {
			Message map[string]any `json:"message"`
			Created bool           `json:"created"`
		}
		h.PostData(t, "/api/conversations/"+conv.ID+"/messages", map[string]any{
			"content": content, "client_id": clientID,
		}, &out)
		if out.Message == nil {
			t.Fatalf("post %q: nil message", content)
		}
		return out.Message, out.Created
	}

	m1, created1 := post("限流与部署：先写库再推送，断线后按 seq 补齐。", "ac07-1")
	if !created1 {
		t.Fatal("first message should be created")
	}
	m2, _ := post("回滚步骤：停止写入、恢复备份、重建索引。", "ac07-2")
	m3, _ := post("幂等测试：同一个 client_id 重复发送不应产生重复消息。", "ac07-3")

	// Idempotency: retrying the same client_id returns the same message and
	// does not insert a duplicate (at-least-once delivery contract).
	again, createdAgain := post("幂等测试：同一个 client_id 重复发送不应产生重复消息。", "ac07-3")
	if createdAgain {
		t.Fatal("duplicate client_id was inserted again")
	}
	if again["id"] != m3["id"] {
		t.Fatalf("idempotent retry id = %v, want %v", again["id"], m3["id"])
	}

	var listBefore struct {
		Items []struct {
			ID  string `json:"id"`
			Seq int64  `json:"seq"`
		} `json:"items"`
		Total  int64 `json:"total"`
		MaxSeq int64 `json:"max_seq"`
	}
	h.GetData(t, "/api/conversations/"+conv.ID+"/messages", &listBefore)
	if listBefore.Total != 3 {
		t.Fatalf("messages before restart = %d, want 3", listBefore.Total)
	}

	// SSE replay: history is served from DB, so "disconnect for 3 days" and
	// "disconnect for 3 seconds" use the same since=<seq> path.
	readSSE := func(since int64, want int) []SSEEvent {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		events, err := h.Client.ReadSSE(ctx, since, want)
		if err != nil && len(events) < want {
			t.Fatalf("read SSE since=%d want=%d got=%d err=%v", since, want, len(events), err)
		}
		if len(events) < want {
			t.Fatalf("read SSE since=%d got=%d events, want %d", since, len(events), want)
		}
		return events
	}
	// Setup creates conversation.created + greeting message.created; the three
	// posted messages add message.created. Read until the stream is idle: the
	// exact number of conversation events is not part of this AC, but every
	// message must be replayable from seq 0.
	events := readSSE(0, 0)
	var lastEventSeq int64
	messageCreated := 0
	for _, ev := range events {
		if ev.ID > lastEventSeq {
			lastEventSeq = ev.ID
		}
		if ev.Type == "message.created" {
			messageCreated++
		}
	}
	if messageCreated < 4 {
		t.Fatalf("message.created events = %d, want >=4 (greeting + 3 posts); SSE events=%d", messageCreated, len(events))
	}
	if lastEventSeq == 0 {
		t.Fatal("SSE replay returned no events")
	}

	// A new message after the cursor is delivered exactly once by SSE.
	m4, _ := post("断线恢复后的新消息用于增量推送验证。", "ac07-4")
	next := readSSE(lastEventSeq, 1)
	if next[0].Type != "message.created" || next[0].ID <= lastEventSeq {
		t.Fatalf("incremental event = %+v, want message.created after %d", next[0], lastEventSeq)
	}
	var payload struct {
		ObjectID string `json:"object_id"`
	}
	if err := jsonUnmarshal(next[0].Data, &payload); err != nil {
		t.Fatalf("decode event payload: %v", err)
	}
	if payload.ObjectID != m4["id"] {
		t.Fatalf("incremental event object id = %q, want %q", payload.ObjectID, m4["id"])
	}
	lastEventSeq = next[0].ID

	// Simulate a crashed turn by inserting a running turn; Platform.New must
	// mark it interrupted on restart (FR-OPS-06 / AC-07).
	var assistants struct {
		Items []map[string]any `json:"items"`
	}
	h.GetData(t, "/api/agents?kind=assistant", &assistants)
	if len(assistants.Items) != 1 {
		t.Fatalf("assistant count = %d, want 1", len(assistants.Items))
	}
	assistantID, _ := assistants.Items[0]["id"].(string)
	turn := domain.Turn{
		ID: "turn_ac07", AgentID: assistantID, ConversationID: conv.ID,
		Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := h.Store.CreateTurn(context.Background(), turn); err != nil {
		t.Fatalf("create running turn: %v", err)
	}

	// Delete m2 before restart: deletion must survive restart and not reappear
	// in search/list (tombstone).
	if _, ok := m2["id"].(string); !ok {
		t.Fatalf("m2 id missing: %v", m2)
	}
	h.ExpectStatus(t, http.MethodDelete, "/api/messages/"+m2["id"].(string), nil, http.StatusOK)
	deleteEvent := readSSE(lastEventSeq, 1)
	if deleteEvent[0].Type != "message.deleted" {
		t.Fatalf("deletion event type = %q, want message.deleted", deleteEvent[0].Type)
	}
	lastEventSeq = deleteEvent[0].ID

	// Restart: stop HTTP + close SQLite, reopen/migrate, new listener. This is
	// the in-process equivalent of the process-kill half of AC-07; the real
	// OS-kill path is still a release rehearsal (see ac-matrix §6).
	restartElapsed := h.Restart()

	// Session survives restart (cookie + stable session secret in settings).
	data := h.EnsureSession()
	if got := data["setup_required"]; got != false {
		t.Fatalf("setup_required after restart = %v", got)
	}

	// Messages: no loss, no duplicates, deleted stays deleted.
	var listAfter struct {
		Items []struct {
			ID  string `json:"id"`
			Seq int64  `json:"seq"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/conversations/"+conv.ID+"/messages", &listAfter)
	if listAfter.Total != 3 {
		t.Fatalf("messages after restart = %d, want 3 (m2 deleted)", listAfter.Total)
	}
	seen := map[string]bool{}
	for _, m := range listAfter.Items {
		if seen[m.ID] {
			t.Fatalf("duplicate message after restart: %s", m.ID)
		}
		seen[m.ID] = true
	}
	if seen[m2["id"].(string)] {
		t.Fatal("deleted message reappeared after restart")
	}
	for _, want := range []string{m1["id"].(string), m3["id"].(string), m4["id"].(string)} {
		if !seen[want] {
			t.Fatalf("message %s lost after restart", want)
		}
	}

	// Deleted row remains as a tombstone in storage (audit/debug visible).
	deleted, err := h.Store.GetMessage(context.Background(), m2["id"].(string))
	if err != nil {
		t.Fatalf("get deleted message: %v", err)
	}
	if deleted.DeletedAt == "" {
		t.Fatal("deleted message has empty deleted_at after restart")
	}

	// Running turn was marked interrupted by startup reconciliation.
	gotTurn, err := h.Store.GetTurn(context.Background(), "turn_ac07")
	if err != nil {
		t.Fatalf("get turn: %v", err)
	}
	if gotTurn.Status != "interrupted" {
		t.Fatalf("crashed turn status = %q, want interrupted", gotTurn.Status)
	}
	if gotTurn.Error == "" {
		t.Fatal("interrupted turn should carry a readable error")
	}

	// Search index survived restart (it is in SQLite, not memory).
	var hits struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	h.GetData(t, "/api/search?"+url.Values{"q": {"限流"}}.Encode(), &hits)
	if hits.Total < 1 {
		t.Fatalf("search after restart total=%d, want >=1", hits.Total)
	}
	// Deleted message must not be searchable after restart.
	h.GetData(t, "/api/search?"+url.Values{"q": {"回滚"}}.Encode(), &hits)
	if hits.Total != 0 {
		t.Fatalf("deleted message searchable after restart: total=%d", hits.Total)
	}

	// Event log is monotonic and replayable: no new event was invented by
	// restart, and replaying from the last seen seq yields nothing.
	eventsAfter, err := h.Store.ListEventsSince(context.Background(), lastEventSeq, "", 100)
	if err != nil {
		t.Fatalf("list events since: %v", err)
	}
	if len(eventsAfter) != 0 {
		t.Fatalf("restart replayed %d events after cursor %d", len(eventsAfter), lastEventSeq)
	}
	for _, ev := range events {
		if ev.ID <= 0 {
			t.Fatalf("SSE event missing id: %+v", ev)
		}
	}

	t.Logf("restart elapsed=%s; messages=%d; SSE events replayed=%d; interrupted turn ok", restartElapsed, listAfter.Total, len(events))

	// R2/R8: real OS kill, DeepSeek timeout/no-key degradation, and the 1h
	// disconnect rehearsal remain release-gate manual/e2e checks.
	t.Run("real_process_kill_and_model_timeout_R2_R8", func(t *testing.T) {
		SkipSkeleton(t, "AC-07 real process kill / model timeout / 1h disconnect", "R2/R8", "需要 e2e 启动器与故障注入接线")
	})
}

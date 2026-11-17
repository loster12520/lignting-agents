//go:build acceptance

package ac

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/deepseek"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/internal/write"
)

// TestAC07CrashRestartOffline covers the R1-available part of AC-07:
// write -> SSE event -> idempotent retry -> in-process restart (SQLite reopen +
// new HTTP listener) -> messages/events/search survive, running turn becomes
// interrupted, deleted message stays tombstoned, no duplicate replay.
// Still R2/R8: real process kill, DeepSeek timeout/degradation, 1h disconnect rehearsal.
func TestAC07CrashRestartOffline(t *testing.T) {
	h := NewHarness(t, WithCoreServices())
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

	// R2/R8: a real model failure must degrade to a readable message and leave
	// the runtime usable; a real OS process kill must not lose committed data.
	// The 1h disconnect rehearsal is documented as a P1 manual gate in ac-matrix.
	t.Run("real_process_kill_and_model_timeout_R2_R8", func(t *testing.T) {
		ctx := context.Background()
		t.Run("model_timeout_degrades", func(t *testing.T) {
			// 用 scripted mock 确定性注入一次超时：真实 Key 模式下无法可靠制造真实超时，
			// 而这里验证的是「模型失败时的降级 + 不重复计费 + 失败后仍可用」，与 Provider 类型无关。
			mock := deepseek.NewMock()
			mock.Script(deepseek.Script{Err: context.DeadlineExceeded})
			ht := NewHarness(t, WithCoreServices(), WithProvider(mock))
			ht.Setup("阿哲")
			ctxT := context.Background()
			var convsT struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			ht.GetData(t, "/api/conversations?type=dm", &convsT)
			if len(convsT.Items) != 1 {
				t.Fatalf("DM conversations = %d, want 1", len(convsT.Items))
			}
			dmT := convsT.Items[0].ID
			lastSeqT := func() int64 {
				events, err := ht.Store.ListEventsSince(ctxT, 0, "", 3000)
				if err != nil {
					t.Fatalf("events: %v", err)
				}
				var max int64
				for _, ev := range events {
					if ev.Seq > max {
						max = ev.Seq
					}
				}
				return max
			}
			usageRows := func() int {
				var n int
				if err := ht.Store.DB.QueryRow("SELECT COUNT(*) FROM usage_ledger").Scan(&n); err != nil {
					t.Fatalf("usage rows: %v", err)
				}
				return n
			}
			runTurnT := func(content string) {
				t.Helper()
				var posted struct {
					Message domain.Message `json:"message"`
				}
				ht.PostData(t, "/api/conversations/"+dmT+"/messages", map[string]any{"content": content}, &posted)
				if err := ht.Runtime.EnqueueUserMessage(ctxT, posted.Message); err != nil {
					t.Fatalf("enqueue: %v", err)
				}
			}
			waitEventT := func(typ string, since int64) domain.Event {
				t.Helper()
				var found domain.Event
				WaitFor(t, 10*time.Second, 50*time.Millisecond, func() error {
					events, err := ht.Store.ListEventsSince(ctxT, since, dmT, 1000)
					if err != nil {
						return err
					}
					for _, ev := range events {
						if ev.Type == typ {
							found = ev
							return nil
						}
					}
					return fmt.Errorf("%s not seen", typ)
				})
				return found
			}

			usageBefore := usageRows()
			before := lastSeqT()
			runTurnT("AC-07 模型超时降级")
			waitEventT("turn.failed", before)
			var msgsT struct {
				Items []struct {
					SenderType string `json:"sender_type"`
					Content    string `json:"content"`
					Meta       string `json:"meta"`
				} `json:"items"`
			}
			ht.GetData(t, "/api/conversations/"+dmT+"/messages?limit=100", &msgsT)
			if len(msgsT.Items) == 0 {
				t.Fatal("no messages after model timeout")
			}
			last := msgsT.Items[len(msgsT.Items)-1]
			if last.SenderType != "agent" || !strings.Contains(last.Meta, "assistant.error") || !strings.Contains(last.Content, "模型调用失败") {
				t.Fatalf("timeout degradation message = %+v", last)
			}
			if got := usageRows(); got != usageBefore {
				t.Fatalf("failed turn wrote usage rows: %d -> %d", usageBefore, got)
			}
			// Runtime is still usable after the failure（scripted 错误已消费，下一次回落到默认回复）。
			before = lastSeqT()
			runTurnT("AC-07 超时后重试成功")
			waitEventT("message.completed", before)
			t.Log("AC-07 model timeout degraded to a readable assistant message; retry succeeded")
		})

		t.Run("model_no_key_degrades", func(t *testing.T) {
			h2 := NewHarness(t, WithCoreServices(), WithNoKeyProvider())
			h2.Setup("阿哲")
			var convs2 struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			h2.GetData(t, "/api/conversations?type=dm", &convs2)
			if len(convs2.Items) != 1 {
				t.Fatalf("no-key DM = %d, want 1", len(convs2.Items))
			}
			var posted struct {
				Message domain.Message `json:"message"`
			}
			h2.PostData(t, "/api/conversations/"+convs2.Items[0].ID+"/messages", map[string]any{"content": "AC-07 无 Key 降级"}, &posted)
			if err := h2.Runtime.EnqueueUserMessage(context.Background(), posted.Message); err != nil {
				t.Fatalf("enqueue no-key: %v", err)
			}
			WaitFor(t, 10*time.Second, 50*time.Millisecond, func() error {
				events, err := h2.Store.ListEventsSince(context.Background(), 0, convs2.Items[0].ID, 1000)
				if err != nil {
					return err
				}
				for _, ev := range events {
					if ev.Type == "turn.failed" {
						return nil
					}
				}
				return fmt.Errorf("no-key turn.failed not seen")
			})
			var msgs2 struct {
				Items []struct {
					SenderType string `json:"sender_type"`
					Content    string `json:"content"`
					Meta       string `json:"meta"`
				} `json:"items"`
			}
			h2.GetData(t, "/api/conversations/"+convs2.Items[0].ID+"/messages?limit=100", &msgs2)
			last := msgs2.Items[len(msgs2.Items)-1]
			if last.SenderType != "agent" || !strings.Contains(last.Meta, "assistant.error") || !strings.Contains(last.Content, "还没有配置 DeepSeek API Key") {
				t.Fatalf("no-key degradation message = %+v", last)
			}
			health, err := h2.Client.Get("/api/health")
			if err != nil || health.Status != http.StatusOK {
				t.Fatalf("health after no-key failure = %v/%v", health, err)
			}
			t.Log("AC-07 no-key degradation passed: readable assistant error, service healthy")
		})

		t.Run("sse_short_disconnect_replay", func(t *testing.T) {
			disconnectAt := func() int64 {
				events, err := h.Store.ListEventsSince(ctx, 0, "", 5000)
				if err != nil {
					t.Fatalf("events: %v", err)
				}
				var max int64
				for _, ev := range events {
					if ev.Seq > max {
						max = ev.Seq
					}
				}
				return max
			}()
			ids := make([]string, 0, 3)
			for _, content := range []string{"断线期间消息 1", "断线期间消息 2", "断线期间消息 3"} {
				var out struct {
					Message map[string]any `json:"message"`
				}
				h.PostData(t, "/api/conversations/"+conv.ID+"/messages", map[string]any{"content": content}, &out)
				id, _ := out.Message["id"].(string)
				ids = append(ids, id)
			}
			replayed := readSSE(disconnectAt, 3)
			if len(replayed) != 3 {
				t.Fatalf("replayed events = %d, want 3", len(replayed))
			}
			seen := map[int64]bool{}
			gotIDs := map[string]bool{}
			var prev int64
			for _, ev := range replayed {
				if ev.Type != "message.created" || ev.ID <= prev || seen[ev.ID] {
					t.Fatalf("bad replay event order/type: prev=%d ev=%+v", prev, ev)
				}
				seen[ev.ID] = true
				prev = ev.ID
				var payload struct {
					ObjectID string `json:"object_id"`
				}
				if err := jsonUnmarshal(ev.Data, &payload); err != nil {
					t.Fatalf("replay payload: %v", err)
				}
				gotIDs[payload.ObjectID] = true
			}
			for _, id := range ids {
				if !gotIDs[id] {
					t.Fatalf("replay missed message %s: got %v", id, gotIDs)
				}
			}
			t.Log("AC-07 1h-disconnect equivalent passed: since-cursor replay delivered exactly the 3 missed events in order, no duplicates")
		})

		t.Run("side_effect_tool_not_retried", func(t *testing.T) {
			rel := "ac07/recovered.txt"
			content := "recovered exactly once\n"
			h.WriteWorkspaceFile(rel, content)
			pending := domain.WriteEvent{
				ID: "write_ac07_pending", AgentID: assistantID, TaskID: "", Path: rel,
				BaseHash: write.HashString(""), Content: content, Status: "pending",
				CreatedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z"),
			}
			if err := h.Store.InsertWriteEvent(context.Background(), pending); err != nil {
				t.Fatalf("insert pending write: %v", err)
			}
			h.Restart()
			h.EnsureSession()
			svc, err := write.New(write.Options{Root: h.Cfg.WorkspaceDir, Repository: h.Store, Audit: h.Store})
			if err != nil {
				t.Fatalf("write service: %v", err)
			}
			report, err := svc.Recover(context.Background())
			if err != nil || report.Applied != 1 {
				t.Fatalf("recover = %+v err=%v, want 1 applied", report, err)
			}
			got, err := h.Store.GetWriteEvent(context.Background(), pending.ID)
			if err != nil || got.Status != "applied" {
				t.Fatalf("recovered write = %+v err=%v", got, err)
			}
			data, err := os.ReadFile(h.WorkspacePath(rel))
			if err != nil || string(data) != content {
				t.Fatalf("side-effect file after recovery = %q err=%v, want exactly once", data, err)
			}
			report2, err := svc.Recover(context.Background())
			if err != nil || report2 != (write.RecoveryReport{}) {
				t.Fatalf("second recover = %+v err=%v, want no-op", report2, err)
			}
			data2, _ := os.ReadFile(h.WorkspacePath(rel))
			if string(data2) != content {
				t.Fatal("recovery re-applied the side effect")
			}
			t.Log("AC-07 side-effect recovery passed: pending write classified applied once, second recover no-op")
		})

		t.Run("real_process_kill", func(t *testing.T) {
			runRealBinaryKill(t)
		})
	})
}

// runRealBinaryKill builds and starts the real lignting binary, commits a
// message, kills the OS process, restarts on the same data directory/address,
// and verifies the committed message and session survived. It is the real
// process-kill half of AC-07 (the in-process restart path is covered above).
func runRealBinaryKill(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "lignting-ac07.exe")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lignting")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}

	dataDir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve addr: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	base := "http://" + addr

	logs := &bytes.Buffer{}
	start := func() *exec.Cmd {
		cmd := exec.Command(bin, "--data-dir", dataDir, "--addr", addr, "--mock-model")
		cmd.Stdout, cmd.Stderr = logs, logs
		if err := cmd.Start(); err != nil {
			t.Fatalf("start binary: %v", err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := http.Get(base + "/api/health")
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return cmd
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("binary not ready; logs:\n%s", logs.String())
		return nil
	}
	cmd := start()
	defer func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	hc := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	csrf := ""
	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var rdr io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			rdr = bytes.NewReader(raw)
		}
		req, err := http.NewRequest(method, base+path, rdr)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Origin", base)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if method != http.MethodGet && csrf != "" {
			req.Header.Set("X-Lignting-CSRF", csrf)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}
	dataOf := func(out map[string]any) map[string]any {
		if d, ok := out["data"].(map[string]any); ok {
			return d
		}
		return out
	}

	status, out := do(http.MethodGet, "/api/bootstrap", nil)
	if status != http.StatusOK {
		t.Fatalf("bootstrap = %d %v", status, out)
	}
	csrf, _ = dataOf(out)["csrf_token"].(string)
	status, out = do(http.MethodPost, "/api/setup", map[string]any{"owner_name": "阿哲"})
	if status != http.StatusOK {
		t.Fatalf("setup = %d %v", status, out)
	}
	if token, _ := dataOf(out)["csrf_token"].(string); token != "" {
		csrf = token
	}
	status, out = do(http.MethodPost, "/api/conversations", map[string]any{"type": "project", "title": "AC-07 真实进程杀"})
	if status != http.StatusOK {
		t.Fatalf("create conversation = %d %v", status, out)
	}
	conv := dataOf(out)
	convID, _ := conv["id"].(string)
	if convID == "" {
		t.Fatalf("conversation id missing: %v", conv)
	}
	status, out = do(http.MethodGet, "/api/org", nil)
	assistant, _ := dataOf(out)["assistant"].(map[string]any)
	assistantID, _ := assistant["id"].(string)
	if status != http.StatusOK || assistantID == "" {
		t.Fatalf("org/assistant = %d %v", status, out)
	}
	posted := map[string]string{
		"ac07-real-kill-1": "真实进程 kill 前已提交的消息 1",
		"ac07-real-kill-2": "真实进程 kill 前已提交的消息 2",
	}
	for clientID, content := range posted {
		status, out = do(http.MethodPost, "/api/conversations/"+convID+"/messages", map[string]any{
			"content": content, "client_id": clientID,
		})
		if status != http.StatusOK {
			t.Fatalf("post message %s = %d %v", clientID, status, out)
		}
	}

	// Simulate the crash instant: a running turn committed just before the
	// process is killed (second SQLite connection; WAL permits it).
	st, err := store.Open(filepath.Join(dataDir, "lignting.db"))
	if err != nil {
		t.Fatalf("open db before kill: %v", err)
	}
	if err := st.CreateTurn(context.Background(), domain.Turn{
		ID: "turn_ac07_kill", AgentID: assistantID, ConversationID: convID,
		Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		_ = st.Close()
		t.Fatalf("create running turn: %v", err)
	}
	_ = st.Close()

	// Real OS kill: no graceful shutdown, no flush hook.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill process: %v", err)
	}
	_, _ = cmd.Process.Wait()
	cmd = start()

	status, out = do(http.MethodGet, "/api/bootstrap", nil)
	if status != http.StatusOK {
		t.Fatalf("bootstrap after kill = %d %v", status, out)
	}
	if authed, _ := dataOf(out)["authenticated"].(bool); !authed {
		t.Fatalf("session lost after real kill: %v", dataOf(out))
	}
	status, out = do(http.MethodGet, "/api/health", nil)
	if status != http.StatusOK {
		t.Fatalf("health after restart = %d", status)
	}
	status, out = do(http.MethodGet, "/api/conversations/"+convID+"/messages?limit=100", nil)
	if status != http.StatusOK {
		t.Fatalf("messages after kill = %d %v", status, out)
	}
	items, _ := dataOf(out)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("messages after real kill = %d, want 2; payload=%v", len(items), dataOf(out))
	}
	seen := map[string]bool{}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		content, _ := item["content"].(string)
		seen[content] = true
	}
	for _, content := range posted {
		if !seen[content] {
			t.Fatalf("message lost after kill: %q (got %v)", content, seen)
		}
	}
	status, out = do(http.MethodGet, "/api/turns/turn_ac07_kill", nil)
	if status != http.StatusOK {
		t.Fatalf("turn after kill = %d %v", status, out)
	}
	turn, _ := dataOf(out)["turn"].(map[string]any)
	if turn["status"] != "interrupted" {
		t.Fatalf("crashed turn status = %v, want interrupted", turn["status"])
	}
	if errText, _ := turn["error"].(string); errText == "" {
		t.Fatal("interrupted turn has no readable error")
	}

	// Audit chain and committed rows survive the real kill.
	st, err = store.Open(filepath.Join(dataDir, "lignting.db"))
	if err != nil {
		t.Fatalf("open db after kill: %v", err)
	}
	defer st.Close()
	if _, err := st.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("audit chain after kill: %v", err)
	}
	status, out = do(http.MethodPost, "/api/conversations/"+convID+"/messages", map[string]any{"content": "重启后新增的消息"})
	if status != http.StatusOK {
		t.Fatalf("post after restart = %d %v", status, out)
	}
	status, out = do(http.MethodGet, "/api/conversations/"+convID+"/messages?limit=100", nil)
	if status != http.StatusOK {
		t.Fatalf("messages after restart post = %d %v", status, out)
	}
	items, _ = dataOf(out)["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("messages after restart = %d, want 3", len(items))
	}
	t.Logf("AC-07 real process kill passed: 2 messages + interrupted turn survived SIGKILL; audit chain ok; restart %s", addr)
}

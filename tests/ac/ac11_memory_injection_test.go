//go:build acceptance

package ac

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/tests/fixtures"
)

// TestAC11MemoryInjection covers AC-11: create a preference memory through the
// real memory API, run a turn, then read the context.request snapshot and
// assert memory_entries/source/token; patch and delete take effect on the next
// turn; a disabled memory layer degrades with an explicit reason.
func TestAC11MemoryInjection(t *testing.T) {
	h := NewHarness(t, WithSeed(), WithCoreServices())
	h.EnsureSession()
	ctx := context.Background()

	var memory struct {
		ID         string `json:"id"`
		Version    int    `json:"version"`
		Summary    string `json:"summary"`
		Visibility string `json:"visibility"`
	}
	h.PostData(t, "/api/memories", map[string]any{
		"direct": true, "scope": "assistant", "agent_id": fixtures.AssistantID,
		"entity_type": "preference", "entity_key": "reply_format",
		"content": "回答时先给结论，再列来源与证据。",
		"summary": "先结论后来源", "tags": []string{"偏好", "ac11"},
		"confidence": "high", "visibility": "agent-private",
	}, &memory)
	if memory.ID == "" || memory.Version != 1 || memory.Visibility != "agent-private" {
		t.Fatalf("created memory = %+v", memory)
	}

	waitTurn := func(since int64) string {
		t.Helper()
		var turnID string
		WaitFor(t, 10*time.Second, 50*time.Millisecond, func() error {
			events, err := h.Store.ListEventsSince(ctx, since, fixtures.ConversationDMID, 1000)
			if err != nil {
				return err
			}
			for _, ev := range events {
				if ev.Type != "turn.completed" {
					continue
				}
				var payload struct {
					TurnID string `json:"turn_id"`
				}
				if json.Unmarshal([]byte(ev.Payload), &payload) == nil && payload.TurnID != "" {
					turnID = payload.TurnID
					return nil
				}
			}
			return fmt.Errorf("turn.completed not seen yet")
		})
		return turnID
	}
	lastSeq := func() int64 {
		events, err := h.Store.ListEventsSince(ctx, 0, "", 2000)
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
	runTurn := func(content string) string {
		t.Helper()
		var posted struct {
			Message domain.Message `json:"message"`
		}
		h.PostData(t, "/api/conversations/"+fixtures.ConversationDMID+"/messages", map[string]any{"content": content}, &posted)
		before := lastSeq()
		if err := h.Runtime.EnqueueUserMessage(ctx, posted.Message); err != nil {
			t.Fatalf("enqueue turn: %v", err)
		}
		return waitTurn(before)
	}
	type snapshot struct {
		TurnID       string   `json:"turn_id"`
		Payload      string   `json:"payload"`
		MemoryIDs    []string `json:"memory_ids"`
		MemoryTokens int64    `json:"memory_tokens"`
	}
	readSnapshot := func(turnID string) (memoryEntries []struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
		Source  string `json:"source"`
		Tokens  int64  `json:"tokens"`
	}, memoryTokens int64, degraded string) {
		t.Helper()
		var list struct {
			Items []snapshot `json:"items"`
		}
		h.GetData(t, "/api/context-snapshots?turn_id="+turnID, &list)
		if len(list.Items) != 1 {
			t.Fatalf("snapshots for turn %s = %d, want 1", turnID, len(list.Items))
		}
		var payload struct {
			MemoryEntries []struct {
				ID      string `json:"id"`
				Summary string `json:"summary"`
				Source  string `json:"source"`
				Tokens  int64  `json:"tokens"`
			} `json:"memory_entries"`
			MemoryTokens         int64    `json:"memory_tokens"`
			MemoryDegradedReason string   `json:"memory_degraded_reason"`
			MemoryIDs            []string `json:"memory_ids"`
		}
		if err := json.Unmarshal([]byte(list.Items[0].Payload), &payload); err != nil {
			t.Fatalf("snapshot payload: %v", err)
		}
		return payload.MemoryEntries, payload.MemoryTokens, payload.MemoryDegradedReason
	}
	findEntry := func(entries []struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
		Source  string `json:"source"`
		Tokens  int64  `json:"tokens"`
	}, id string) (bool, string, int64) {
		for _, e := range entries {
			if e.ID == id {
				return true, e.Source, e.Tokens
			}
		}
		return false, "", 0
	}

	// 1) New turn injects the preference and records source/tokens.
	turn1 := runTurn("AC-11 第一轮：请总结导出方案")
	entries, tokens, degraded := readSnapshot(turn1)
	if degraded != "" {
		t.Fatalf("unexpected memory degradation: %q", degraded)
	}
	if ok, source, entryTokens := findEntry(entries, memory.ID); !ok || source == "" || entryTokens <= 0 {
		t.Fatalf("memory %s not injected with source/tokens: entries=%+v", memory.ID, entries)
	}
	if tokens <= 0 {
		t.Fatalf("memory_tokens = %d, want >0", tokens)
	}

	// 2) PATCH takes effect on the next turn.
	var patched struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
		Summary string `json:"summary"`
	}
	resp, err := h.Client.Do(ctx, "PATCH", "/api/memories/"+memory.ID, map[string]any{
		"summary": "先结论后来源（v2）", "expected_version": memory.Version,
	})
	if err != nil {
		t.Fatalf("patch memory: %v", err)
	}
	resp.MustStatus(t, 200).Data(t, &patched)
	if patched.Version != 2 || !strings.Contains(patched.Summary, "v2") {
		t.Fatalf("patched memory = %+v", patched)
	}
	turn2 := runTurn("AC-11 第二轮：修订偏好是否生效")
	entries, _, _ = readSnapshot(turn2)
	foundV2 := false
	for _, e := range entries {
		if e.ID == memory.ID {
			foundV2 = strings.Contains(e.Summary, "v2")
		}
	}
	if !foundV2 {
		t.Fatalf("patched summary not injected: %+v", entries)
	}

	// 3) DELETE removes the memory from subsequent injection (tombstone).
	h.ExpectStatus(t, "DELETE", "/api/memories/"+memory.ID, map[string]any{"reason": "AC-11 删除测试"}, 200)
	turn3 := runTurn("AC-11 第三轮：删除后不应再注入")
	entries, _, _ = readSnapshot(turn3)
	if ok, _, _ := findEntry(entries, memory.ID); ok {
		t.Fatalf("deleted memory still injected: %+v", entries)
	}

	// 4) A disabled memory layer degrades explicitly (manual + task only).
	h2 := NewHarness(t, WithSeed(), WithCoreServices(), WithMemoryDisabled())
	h2.EnsureSession()
	var posted struct {
		Message domain.Message `json:"message"`
	}
	h2.PostData(t, "/api/conversations/"+fixtures.ConversationDMID+"/messages", map[string]any{"content": "AC-11 降级测试"}, &posted)
	before := int64(0)
	if err := h2.Runtime.EnqueueUserMessage(ctx, posted.Message); err != nil {
		t.Fatalf("enqueue degraded turn: %v", err)
	}
	var turnID string
	WaitFor(t, 10*time.Second, 50*time.Millisecond, func() error {
		events, err := h2.Store.ListEventsSince(ctx, before, fixtures.ConversationDMID, 1000)
		if err != nil {
			return err
		}
		for _, ev := range events {
			if ev.Type == "turn.completed" {
				var payload struct {
					TurnID string `json:"turn_id"`
				}
				if json.Unmarshal([]byte(ev.Payload), &payload) == nil {
					turnID = payload.TurnID
					return nil
				}
			}
		}
		return fmt.Errorf("degraded turn.completed not seen yet")
	})
	var list struct {
		Items []snapshot `json:"items"`
	}
	h2.GetData(t, "/api/context-snapshots?turn_id="+turnID, &list)
	if len(list.Items) != 1 {
		t.Fatalf("degraded snapshots = %d, want 1", len(list.Items))
	}
	var degradedPayload struct {
		MemoryEntries []json.RawMessage `json:"memory_entries"`
		Reason        string            `json:"memory_degraded_reason"`
	}
	if err := json.Unmarshal([]byte(list.Items[0].Payload), &degradedPayload); err != nil {
		t.Fatalf("degraded payload: %v", err)
	}
	if degradedPayload.Reason != "memory_disabled" || len(degradedPayload.MemoryEntries) != 0 {
		t.Fatalf("degraded snapshot = reason=%q entries=%d, want memory_disabled/0", degradedPayload.Reason, len(degradedPayload.MemoryEntries))
	}

	t.Logf("AC-11 passed: injected memory source/tokens, patch/delete effective, disabled layer degrades explicitly")
}

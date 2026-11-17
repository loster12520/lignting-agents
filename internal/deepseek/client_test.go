package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func sseWrite(t *testing.T, w http.ResponseWriter, payloads ...string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, p := range payloads {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", p); err != nil {
			return
		}
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func TestCompleteNonStream(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(mustJSON(t, map[string]any{
			"id":      "chatcmpl-1",
			"model":   "deepseek-flash",
			"created": 123,
			"choices": []any{map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "你好",
					"tool_calls": []any{map[string]any{
						"id":       "call_1",
						"type":     "function",
						"function": map[string]any{"name": "read_file", "arguments": "{}"},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{
				"prompt_tokens": 10, "completion_tokens": 5,
				"total_tokens": 15, "prompt_cache_hit_tokens": 4,
			},
		}))
	}))
	defer srv.Close()

	c := New(Config{APIKey: "sk-test", BaseURL: srv.URL, Model: "deepseek-flash"})
	resp, err := c.Complete(context.Background(), Request{Messages: []Message{UserMessage("hi")}})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if gotPath != PathChatCompletions {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotBody["model"] != "deepseek-flash" {
		t.Fatalf("model = %v", gotBody["model"])
	}
	if gotBody["stream"] != nil {
		t.Fatalf("非流式请求不应带 stream: %v", gotBody["stream"])
	}
	msg := resp.FirstMessage()
	if msg.Content != "你好" || len(msg.ToolCalls) != 1 {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if msg.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("tool name = %q", msg.ToolCalls[0].Function.Name)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 || resp.Usage.CachedTokens != 4 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish = %q", resp.Choices[0].FinishReason)
	}
}

func TestServerToolsEncoding(t *testing.T) {
	var gotTools []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &body)
		gotTools, _ = body["tools"].([]any)
		_, _ = w.Write(mustJSON(t, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}},
		}))
	}))
	defer srv.Close()

	c := New(Config{APIKey: "sk-test", BaseURL: srv.URL})
	_, err := c.Complete(context.Background(), Request{
		Messages: []Message{UserMessage("hi")},
		Tools:    []Tool{FunctionTool("read_file", "read", json.RawMessage(`{"type":"object"}`)), WebSearchTool(), ReadURLTool()},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(gotTools) != 3 {
		t.Fatalf("tools = %v", gotTools)
	}
	types := make([]string, 0, len(gotTools))
	for _, item := range gotTools {
		m, _ := item.(map[string]any)
		types = append(types, fmt.Sprint(m["type"]))
	}
	want := []string{"function", ServerToolWebSearch, ServerToolReadURL}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("tools[%d] = %q, want %q", i, types[i], want[i])
		}
	}
}

func TestCompleteRetriesOn429(t *testing.T) {
	calls := 0
	var delays []time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write(mustJSON(t, map[string]any{"error": map[string]any{"message": "rate limited", "type": "rate_limit"}}))
			return
		}
		_, _ = w.Write(mustJSON(t, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}},
		}))
	}))
	defer srv.Close()

	c := New(Config{
		APIKey: "sk-test", BaseURL: srv.URL, MaxRetries: 2,
		Sleep: func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil },
	})
	resp, err := c.Complete(context.Background(), Request{Messages: []Message{UserMessage("hi")}})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if resp.FirstMessage().Content != "ok" {
		t.Fatalf("content = %q", resp.FirstMessage().Content)
	}
	if len(delays) != 1 {
		t.Fatalf("delays = %v", delays)
	}
}

func TestCompleteNoRetryOn400(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(mustJSON(t, map[string]any{"error": map[string]any{"message": "bad request", "code": "invalid_request"}}))
	}))
	defer srv.Close()

	c := New(Config{APIKey: "sk-test", BaseURL: srv.URL, MaxRetries: 3, Sleep: func(context.Context, time.Duration) error { return nil }})
	_, err := c.Complete(context.Background(), Request{Messages: []Message{UserMessage("hi")}})
	if err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("err = %v", err)
	}
	if IsRetryable(err) {
		t.Fatal("400 不应可重试")
	}
}

func TestMissingAPIKey(t *testing.T) {
	c := New(Config{APIKey: "", BaseURL: "http://127.0.0.1:1"})
	_, err := c.Complete(context.Background(), Request{Messages: []Message{UserMessage("hi")}})
	if !IsNoAPIKey(err) {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
	if IsRetryable(err) {
		t.Fatal("缺少 Key 不应重试")
	}
}

func TestStreamAggregates(t *testing.T) {
	args := string(mustJSON(t, map[string]any{"path": "a.txt"}))
	mid := len(args) / 2
	var deltas []Delta
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &body)
		if body["stream"] != true {
			t.Errorf("stream = %v", body["stream"])
		}
		sseWrite(t, w,
			string(mustJSON(t, map[string]any{
				"id": "c1", "model": "deepseek-flash",
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "你"}}},
			})),
			string(mustJSON(t, map[string]any{
				"id": "c1",
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
					"content": "好",
					"tool_calls": []any{map[string]any{
						"index": 0, "id": "call_1", "type": "function",
						"function": map[string]any{"name": "read_file", "arguments": args[:mid]},
					}},
				}}},
			})),
			string(mustJSON(t, map[string]any{
				"id": "c1",
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
					"tool_calls": []any{map[string]any{
						"index": 0, "function": map[string]any{"arguments": args[mid:]},
					}},
				}, "finish_reason": "tool_calls"}},
			})),
			string(mustJSON(t, map[string]any{
				"id": "c1", "usage": map[string]any{
					"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13, "prompt_cache_hit_tokens": 2,
				},
			})),
		)
	}))
	defer srv.Close()

	c := New(Config{APIKey: "sk-test", BaseURL: srv.URL})
	resp, err := c.Stream(context.Background(), Request{Messages: []Message{UserMessage("hi")}}, func(d Delta) error {
		deltas = append(deltas, d)
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(deltas) < 4 {
		t.Fatalf("deltas = %d", len(deltas))
	}
	msg := resp.FirstMessage()
	if msg.Content != "你好" {
		t.Fatalf("content = %q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("tool calls = %+v", msg.ToolCalls)
	}
	if msg.ToolCalls[0].Function.Arguments != args {
		t.Fatalf("arguments = %q, want %q", msg.ToolCalls[0].Function.Arguments, args)
	}
	if resp.Usage.CompletionTokens != 3 || resp.Usage.CachedTokens != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish = %q", resp.Choices[0].FinishReason)
	}
}

func TestStreamRetriesBeforeFirstDelta(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write(mustJSON(t, map[string]any{"error": map[string]any{"message": "boom"}}))
			return
		}
		sseWrite(t, w, string(mustJSON(t, map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "hi"}, "finish_reason": "stop"}},
		})))
	}))
	defer srv.Close()

	c := New(Config{APIKey: "sk-test", BaseURL: srv.URL, MaxRetries: 1, Sleep: func(context.Context, time.Duration) error { return nil }})
	resp, err := c.Stream(context.Background(), Request{Messages: []Message{UserMessage("hi")}}, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if resp.FirstMessage().Content != "hi" {
		t.Fatalf("content = %q", resp.FirstMessage().Content)
	}
}

func TestStreamNoRetryAfterFirstDelta(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		sseWrite(t, w,
			string(mustJSON(t, map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "先输出"}}},
			})),
			"{bad json",
		)
	}))
	defer srv.Close()

	c := New(Config{APIKey: "sk-test", BaseURL: srv.URL, MaxRetries: 2, Sleep: func(context.Context, time.Duration) error { return nil }})
	got := ""
	_, err := c.Stream(context.Background(), Request{Messages: []Message{UserMessage("hi")}}, func(d Delta) error {
		got += d.Content
		return nil
	})
	if err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1（已输出后不得重试）", calls)
	}
	if got != "先输出" {
		t.Fatalf("got = %q", got)
	}
}

func TestStreamNoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := New(Config{APIKey: "sk-test", BaseURL: srv.URL, MaxRetries: 0})
	if _, err := c.Stream(context.Background(), Request{Messages: []Message{UserMessage("hi")}}, nil); err == nil {
		t.Fatal("空流应返回错误")
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	c := New(Config{APIKey: "sk-test", BaseURL: srv.URL, Timeout: 20 * time.Millisecond, MaxRetries: 0})
	start := time.Now()
	_, err := c.Complete(context.Background(), Request{Messages: []Message{UserMessage("hi")}})
	if err == nil {
		t.Fatal("want timeout error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("耗时 %v，超时未生效", time.Since(start))
	}
	if !strings.Contains(err.Error(), "deepseek:") {
		t.Fatalf("err = %v", err)
	}
}

func TestIsRateLimitedAndContextWindow(t *testing.T) {
	rateErr := &APIError{StatusCode: http.StatusTooManyRequests, Message: "slow down"}
	if !IsRateLimited(rateErr) || !IsRetryable(rateErr) {
		t.Fatal("429 应识别为限流且可重试")
	}
	ctxErr := &APIError{StatusCode: http.StatusBadRequest, Message: "This model's maximum context length is 65536 tokens"}
	if !IsContextWindowExceeded(ctxErr) {
		t.Fatal("应识别上下文超限")
	}
	if IsContextWindowExceeded(&APIError{StatusCode: http.StatusBadRequest, Message: "bad"}) {
		t.Fatal("普通 400 不应识别为上下文超限")
	}
}

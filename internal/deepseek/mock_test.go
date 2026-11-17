package deepseek

import (
	"context"
	"strings"
	"testing"
)

func TestMockDeterministicFallback(t *testing.T) {
	m := NewMock()
	req := Request{Model: "deepseek-flash", Messages: []Message{UserMessage("帮我梳理需求")}}
	resp, err := m.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	content := resp.FirstMessage().Content
	if !strings.Contains(content, "帮我梳理需求") {
		t.Fatalf("content = %q", content)
	}
	// 同一请求重复调用结果一致。
	resp2, _ := m.Complete(context.Background(), req)
	if resp2.FirstMessage().Content != content {
		t.Fatalf("mock 不确定：%q vs %q", content, resp2.FirstMessage().Content)
	}
	if m.CallCount() != 2 {
		t.Fatalf("calls = %d", m.CallCount())
	}
}

func TestMockScriptedToolCall(t *testing.T) {
	m := NewMock()
	m.Script(Script{
		Match: func(req Request) bool {
			return len(req.Messages) > 0 && strings.Contains(req.Messages[len(req.Messages)-1].Content, "读文件")
		},
		ToolCalls: []ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: FunctionCall{Name: "read_file", Arguments: "{}"},
		}},
	})
	resp, err := m.Complete(context.Background(), Request{Messages: []Message{UserMessage("读文件")}})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	msg := resp.FirstMessage()
	if resp.Choices[0].FinishReason != "tool_calls" || len(msg.ToolCalls) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	if msg.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("tool = %+v", msg.ToolCalls[0])
	}
	// 脚本只消费一次，第二次回落到 deterministic fallback。
	resp2, _ := m.Complete(context.Background(), Request{Messages: []Message{UserMessage("读文件")}})
	if len(resp2.FirstMessage().ToolCalls) != 0 {
		t.Fatal("脚本应只消费一次")
	}
}

func TestMockStreamChunks(t *testing.T) {
	m := NewMock()
	m.Script(Script{Content: "你好世界"})
	var b strings.Builder
	var sawFinish bool
	_, err := m.Stream(context.Background(), Request{Messages: []Message{UserMessage("hi")}}, func(d Delta) error {
		b.WriteString(d.Content)
		if d.FinishReason != "" {
			sawFinish = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if b.String() != "你好世界" {
		t.Fatalf("content = %q", b.String())
	}
	if !sawFinish {
		t.Fatal("缺少 finish delta")
	}
}

func TestMockErrorScript(t *testing.T) {
	m := NewMock()
	m.Script(Script{Err: ErrNoAPIKey})
	if _, err := m.Complete(context.Background(), Request{Messages: []Message{UserMessage("hi")}}); !IsNoAPIKey(err) {
		t.Fatalf("err = %v", err)
	}
}

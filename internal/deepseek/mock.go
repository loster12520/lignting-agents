package deepseek

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Script 是 mock 的一条脚本化回复。Match 为空表示下一条任意请求都命中。
type Script struct {
	Match        func(Request) bool
	Content      string
	Reasoning    string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
	Err          error
}

// Mock 是确定性 Provider：按队列消费 Script，未命中时返回固定格式回声。
// 用于 model.mode=mock 与 CI/AC（implementation-plan 6.3）。
type Mock struct {
	mu      sync.Mutex
	scripts []Script
	calls   []Request
}

// NewMock 构造 mock provider。
func NewMock() *Mock { return &Mock{} }

// Script 压入一条脚本（FIFO，仅消费一次）。
func (m *Mock) Script(s Script) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scripts = append(m.scripts, s)
}

// Reset 清空脚本与调用记录。
func (m *Mock) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scripts = nil
	m.calls = nil
}

// Name 实现 Provider。
func (m *Mock) Name() string { return "mock" }

// Requests 返回已接收请求的副本。
func (m *Mock) Requests() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Request, len(m.calls))
	copy(out, m.calls)
	return out
}

// CallCount 返回调用次数。
func (m *Mock) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// Complete 实现 Provider。
func (m *Mock) Complete(_ context.Context, req Request) (Response, error) {
	m.mu.Lock()
	m.calls = append(m.calls, req)
	script, ok := m.pickLocked(req)
	m.mu.Unlock()
	if ok && script.Err != nil {
		return Response{}, script.Err
	}
	if !ok {
		script = Script{Content: fallbackContent(req)}
	}
	return buildMockResponse(req, script), nil
}

// Stream 实现 Provider：按固定块大小切片输出 content，tool call 一次给全。
func (m *Mock) Stream(ctx context.Context, req Request, sink Sink) (Response, error) {
	resp, err := m.Complete(ctx, req)
	if err != nil {
		return Response{}, err
	}
	if sink != nil {
		msg := resp.FirstMessage()
		runes := []rune(msg.Content)
		const chunk = 8
		for i := 0; i < len(runes); i += chunk {
			end := i + chunk
			if end > len(runes) {
				end = len(runes)
			}
			if err := sink(Delta{Content: string(runes[i:end])}); err != nil {
				return Response{}, err
			}
		}
		if msg.ReasoningContent != "" {
			if err := sink(Delta{ReasoningContent: msg.ReasoningContent}); err != nil {
				return Response{}, err
			}
		}
		for i, tc := range msg.ToolCalls {
			if err := sink(Delta{ToolCalls: []ToolCallDelta{{
				Index:     i,
				ID:        tc.ID,
				Type:      tc.Type,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			}}}); err != nil {
				return Response{}, err
			}
		}
		if len(resp.Choices) > 0 && resp.Choices[0].FinishReason != "" {
			if err := sink(Delta{FinishReason: resp.Choices[0].FinishReason}); err != nil {
				return Response{}, err
			}
		}
		if resp.Usage != (Usage{}) {
			u := resp.Usage
			if err := sink(Delta{Usage: &u}); err != nil {
				return Response{}, err
			}
		}
	}
	return resp, nil
}

func (m *Mock) pickLocked(req Request) (Script, bool) {
	for i, s := range m.scripts {
		if s.Match == nil || s.Match(req) {
			m.scripts = append(m.scripts[:i], m.scripts[i+1:]...)
			return s, true
		}
	}
	return Script{}, false
}

func buildMockResponse(req Request, s Script) Response {
	content := s.Content
	if content == "" && len(s.ToolCalls) == 0 {
		content = fallbackContent(req)
	}
	msg := Message{Role: RoleAssistant, Content: content, ReasoningContent: s.Reasoning, ToolCalls: s.ToolCalls}
	finish := s.FinishReason
	if finish == "" {
		if len(s.ToolCalls) > 0 {
			finish = "tool_calls"
		} else {
			finish = "stop"
		}
	}
	return Response{
		ID:      "mock-chatcmpl",
		Model:   req.Model,
		Created: 0,
		Choices: []Choice{{Index: 0, Message: msg, FinishReason: finish}},
		Usage:   s.Usage,
	}
}

func fallbackContent(req Request) string {
	last := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == RoleUser {
			last = req.Messages[i].Content
			break
		}
	}
	last = strings.TrimSpace(last)
	if last == "" {
		return "（mock）收到请求。"
	}
	return fmt.Sprintf("（mock）收到：%s", truncate(last, 200))
}

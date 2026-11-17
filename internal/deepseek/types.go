// Package deepseek 是 DeepSeek 官方 Chat Completions API 的最小客户端。
//
// v0.0.1 只接 DeepSeek 官方 API（默认 deepseek-flash），不做多供应商抽象。
// 本包只负责协议、传输、重试与错误分类，不包含任何业务规则。
package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	// DefaultBaseURL 是 DeepSeek 官方 API 根地址。
	DefaultBaseURL = "https://api.deepseek.com"
	// PathChatCompletions 是 Chat Completions 端点。
	PathChatCompletions = "/chat/completions"
	// DefaultModel 是 v0.0.1 默认模型。
	DefaultModel = "deepseek-flash"

	// ServerToolWebSearch / ServerToolReadURL 是 DeepSeek 服务端工具名。
	// v0.0.1 只用这两个联网能力，模型侧不需要客户端执行。
	ServerToolWebSearch = "deepseek_web_search"
	ServerToolReadURL   = "deepseek_read_url"
)

// Role 是 Chat Completions 的消息角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是一条对话消息。Content 统一用字符串：
// v0.0.1 不支持图片/多模态输入；工具调用参数放在 ToolCalls 中。
type Message struct {
	Role             Role       `json:"role"`
	Content          string     `json:"content"`
	Name             string     `json:"name,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
}

// ToolCall 是模型请求执行的一次函数调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall 是工具名与 JSON 参数（字符串形式，由调用方解析）。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool 是请求中的工具声明：type=function 时带 Function；
// type=deepseek_web_search/deepseek_read_url 时是服务端工具。
type Tool struct {
	Type     string       `json:"type"`
	Function *FunctionDef `json:"function,omitempty"`
}

// FunctionDef 是 function 工具的声明。
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Request 是一次 Chat Completions 请求。零值字段会被省略。
type Request struct {
	Model         string         `json:"model,omitempty"`
	Messages      []Message      `json:"messages"`
	Tools         []Tool         `json:"tools,omitempty"`
	ToolChoice    any            `json:"tool_choice,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	TopP          *float64       `json:"top_p,omitempty"`
	Stop          []string       `json:"stop,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`
}

// StreamOptions 控制流式返回的附加信息。v0.0.1 固定要求 usage。
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// Usage 是一次调用的 token 用量。
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	CachedTokens     int64
}

// Choice 是一条候选回复。
type Choice struct {
	Index        int
	Message      Message
	FinishReason string
}

// Response 是一次完整调用（含流式聚合后）的结果。
type Response struct {
	ID      string
	Model   string
	Created int64
	Choices []Choice
	Usage   Usage
	Raw     json.RawMessage
}

// FirstMessage 返回第一条候选的 assistant 消息。
func (r Response) FirstMessage() Message {
	if len(r.Choices) == 0 {
		return Message{}
	}
	return r.Choices[0].Message
}

// Delta 是流式返回的一段增量。
type Delta struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCallDelta
	FinishReason     string
	Usage            *Usage
}

// ToolCallDelta 是工具调用的增量片段。
type ToolCallDelta struct {
	Index     int
	ID        string
	Type      string
	Name      string
	Arguments string
}

// Sink 接收流式增量；返回 error 会中止本次流。
type Sink func(Delta) error

// Provider 是模型提供方抽象：真 DeepSeek 与 mock 都实现它。
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
	Stream(ctx context.Context, req Request, sink Sink) (Response, error)
}

// FunctionTool 构造一个客户端函数工具声明。
func FunctionTool(name, description string, parameters json.RawMessage) Tool {
	return Tool{
		Type: "function",
		Function: &FunctionDef{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		},
	}
}

// WebSearchTool 构造 DeepSeek 服务端联网搜索工具声明。
func WebSearchTool() Tool { return Tool{Type: ServerToolWebSearch} }

// ReadURLTool 构造 DeepSeek 服务端读取网页工具声明。
func ReadURLTool() Tool { return Tool{Type: ServerToolReadURL} }

// SystemMessage / UserMessage / AssistantMessage / ToolMessage 是构造助手。
func SystemMessage(content string) Message {
	return Message{Role: RoleSystem, Content: content}
}

func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

func AssistantMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: content}
}

func ToolMessage(callID, content string) Message {
	return Message{Role: RoleTool, ToolCallID: callID, Content: content}
}

// String 便于日志输出，不参与协议。
func (m Message) String() string {
	return fmt.Sprintf("%s:%s", m.Role, truncate(m.Content, 80))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

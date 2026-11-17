package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	maxErrorBodyBytes    = 1 << 20 // 1 MiB
	maxResponseBodyBytes = 8 << 20 // 8 MiB
)

// Config 是 Client 的配置。
type Config struct {
	APIKey         string
	BaseURL        string
	Model          string
	HTTPClient     *http.Client
	Timeout        time.Duration
	MaxRetries     int
	RetryBaseDelay time.Duration
	MaxRetryDelay  time.Duration
	// Sleep 供测试注入；nil 时使用真实 timer。
	Sleep     func(context.Context, time.Duration) error
	UserAgent string
}

// Client 实现 Provider（DeepSeek 官方 Chat Completions）。
type Client struct {
	cfg  Config
	http *http.Client
}

// New 构造客户端并补齐默认值。
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 180 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = 500 * time.Millisecond
	}
	if cfg.MaxRetryDelay <= 0 {
		cfg.MaxRetryDelay = 8 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "lignting-agents/0.0.1"
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{cfg: cfg, http: hc}
}

// Name 实现 Provider。
func (c *Client) Name() string { return "deepseek" }

// Model 返回默认模型。
func (c *Client) Model() string { return c.cfg.Model }

// Complete 发起非流式请求。
func (c *Client) Complete(ctx context.Context, req Request) (Response, error) {
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return Response{}, ErrNoAPIKey
	}
	req.Stream = false
	req.StreamOptions = nil
	body, err := c.buildBody(req)
	if err != nil {
		return Response{}, err
	}
	for attempt := 0; ; attempt++ {
		resp, err := c.completeOnce(ctx, body)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		if !IsRetryable(err) || attempt >= c.cfg.MaxRetries {
			return Response{}, err
		}
		if waitErr := c.waitRetry(ctx, attempt, err); waitErr != nil {
			return Response{}, waitErr
		}
	}
}

// Stream 发起流式请求，delta 通过 sink 实时交付。
// 若已经向下游交付过增量，则不会重试（避免重复输出）。
func (c *Client) Stream(ctx context.Context, req Request, sink Sink) (Response, error) {
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return Response{}, ErrNoAPIKey
	}
	req.Stream = true
	if req.StreamOptions == nil {
		req.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	body, err := c.buildBody(req)
	if err != nil {
		return Response{}, err
	}
	started := false
	wrapped := func(d Delta) error {
		started = true
		if sink != nil {
			return sink(d)
		}
		return nil
	}
	for attempt := 0; ; attempt++ {
		resp, err := c.streamOnce(ctx, body, wrapped)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		if started || !IsRetryable(err) || attempt >= c.cfg.MaxRetries {
			return Response{}, err
		}
		if waitErr := c.waitRetry(ctx, attempt, err); waitErr != nil {
			return Response{}, waitErr
		}
	}
}

func (c *Client) buildBody(req Request) ([]byte, error) {
	if req.Model == "" {
		req.Model = c.cfg.Model
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("deepseek: messages 不能为空")
	}
	return json.Marshal(req)
}

func (c *Client) completeOnce(ctx context.Context, body []byte) (Response, error) {
	rctx, cancel := c.requestContext(ctx)
	defer cancel()
	httpResp, err := c.do(rctx, body, false)
	if err != nil {
		return Response{}, &TransportError{Op: "complete", Err: err}
	}
	defer httpResp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseBodyBytes))
	if err != nil {
		return Response{}, &TransportError{Op: "read", Err: err}
	}
	if httpResp.StatusCode != http.StatusOK {
		return Response{}, parseAPIError(httpResp, data)
	}
	return decodeResponse(data)
}

func (c *Client) streamOnce(ctx context.Context, body []byte, sink Sink) (Response, error) {
	rctx, cancel := c.requestContext(ctx)
	defer cancel()
	httpResp, err := c.do(rctx, body, true)
	if err != nil {
		return Response{}, &TransportError{Op: "stream", Err: err}
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(httpResp.Body, maxErrorBodyBytes))
		return Response{}, parseAPIError(httpResp, data)
	}

	scanner := newSSEScanner(httpResp.Body)
	var (
		content   strings.Builder
		reasoning strings.Builder
		finish    string
		usage     Usage
		haveUsage bool
		model     string
		id        string
		created   int64
	)
	acc := newToolCallAccumulator()

	for {
		data, ok, err := scanner.next()
		if err != nil {
			return Response{}, &TransportError{Op: "stream-read", Err: err}
		}
		if !ok {
			break
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return Response{}, &TransportError{Op: "stream-decode", Err: err}
		}
		if chunk.ID != "" {
			id = chunk.ID
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Created != 0 {
			created = chunk.Created
		}
		var delta Delta
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				content.WriteString(ch.Delta.Content)
				delta.Content += ch.Delta.Content
			}
			if ch.Delta.ReasoningContent != "" {
				reasoning.WriteString(ch.Delta.ReasoningContent)
				delta.ReasoningContent += ch.Delta.ReasoningContent
			}
			for _, tc := range ch.Delta.ToolCalls {
				acc.add(tc)
				delta.ToolCalls = append(delta.ToolCalls, ToolCallDelta{
					Index:     tc.Index,
					ID:        tc.ID,
					Type:      tc.Type,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
			if ch.FinishReason != "" {
				finish = ch.FinishReason
				delta.FinishReason = ch.FinishReason
			}
		}
		if chunk.Usage != nil {
			usage = chunk.Usage.toUsage()
			haveUsage = true
			u := usage
			delta.Usage = &u
		}
		if sink != nil {
			if err := sink(delta); err != nil {
				return Response{}, err
			}
		}
	}

	if content.Len() == 0 && reasoning.Len() == 0 && acc.len() == 0 && finish == "" {
		return Response{}, &TransportError{Op: "stream", Err: fmt.Errorf("流在返回任何内容前结束")}
	}

	msg := Message{Role: RoleAssistant, Content: content.String(), ReasoningContent: reasoning.String()}
	msg.ToolCalls = acc.calls()
	resp := Response{
		ID:      id,
		Model:   model,
		Created: created,
		Choices: []Choice{{Index: 0, Message: msg, FinishReason: finish}},
	}
	if haveUsage {
		resp.Usage = usage
	}
	return resp, nil
}

func (c *Client) do(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
	url := c.cfg.BaseURL + PathChatCompletions
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	httpReq.Header.Set("User-Agent", c.cfg.UserAgent)
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}
	return c.http.Do(httpReq)
}

func (c *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.cfg.Timeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.cfg.Timeout)
}

func (c *Client) waitRetry(ctx context.Context, attempt int, err error) error {
	delay := c.cfg.RetryBaseDelay << attempt
	if delay <= 0 || delay > c.cfg.MaxRetryDelay {
		delay = c.cfg.MaxRetryDelay
	}
	var apiErr *APIError
	if asAPIError(err, &apiErr) && apiErr.RetryAfter > 0 {
		delay = apiErr.RetryAfter
	}
	if c.cfg.Sleep != nil {
		return c.cfg.Sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ---------- 线格式 ----------

type usageWire struct {
	PromptTokens         int64 `json:"prompt_tokens"`
	CompletionTokens     int64 `json:"completion_tokens"`
	TotalTokens          int64 `json:"total_tokens"`
	PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
}

func (u usageWire) toUsage() Usage {
	cached := u.PromptCacheHitTokens
	return Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CachedTokens:     cached,
	}
}

type responseWire struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Created int64  `json:"created"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageWire `json:"usage"`
}

func decodeResponse(data []byte) (Response, error) {
	var wire responseWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return Response{}, &TransportError{Op: "decode", Err: err}
	}
	resp := Response{ID: wire.ID, Model: wire.Model, Created: wire.Created, Raw: append(json.RawMessage(nil), data...)}
	for _, ch := range wire.Choices {
		msg := Message{Role: Role(ch.Message.Role), Content: ch.Message.Content, ReasoningContent: ch.Message.ReasoningContent}
		for _, tc := range ch.Message.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:       tc.ID,
				Type:     tc.Type,
				Function: FunctionCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
			})
		}
		if msg.Role == "" {
			msg.Role = RoleAssistant
		}
		resp.Choices = append(resp.Choices, Choice{Index: ch.Index, Message: msg, FinishReason: ch.FinishReason})
	}
	if wire.Usage != nil {
		resp.Usage = wire.Usage.toUsage()
	}
	return resp, nil
}

type streamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Created int64  `json:"created"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageWire `json:"usage"`
}

type toolCallAccumulator struct {
	order []int
	items map[int]*toolCallAccum
}

type toolCallAccum struct {
	id   string
	typ  string
	name string
	args strings.Builder
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{items: map[int]*toolCallAccum{}}
}

func (a *toolCallAccumulator) add(tc struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}) {
	item, ok := a.items[tc.Index]
	if !ok {
		item = &toolCallAccum{}
		a.items[tc.Index] = item
		a.order = append(a.order, tc.Index)
	}
	if tc.ID != "" {
		item.id = tc.ID
	}
	if tc.Type != "" {
		item.typ = tc.Type
	}
	if tc.Function.Name != "" {
		item.name = tc.Function.Name
	}
	if tc.Function.Arguments != "" {
		item.args.WriteString(tc.Function.Arguments)
	}
}

func (a *toolCallAccumulator) len() int { return len(a.order) }

func (a *toolCallAccumulator) calls() []ToolCall {
	out := make([]ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		item := a.items[idx]
		typ := item.typ
		if typ == "" {
			typ = "function"
		}
		out = append(out, ToolCall{
			ID:       item.id,
			Type:     typ,
			Function: FunctionCall{Name: item.name, Arguments: item.args.String()},
		})
	}
	return out
}

// ---------- HTTP 错误 ----------

type errorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
	Message string `json:"message"`
}

func parseAPIError(resp *http.Response, data []byte) error {
	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		Body:       truncate(string(data), 2048),
	}
	var env errorEnvelope
	if err := json.Unmarshal(data, &env); err == nil {
		if env.Error.Message != "" || env.Error.Code != "" {
			apiErr.Message = env.Error.Message
			apiErr.Type = env.Error.Type
			apiErr.Code = env.Error.Code
		} else if env.Message != "" {
			apiErr.Message = env.Message
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs >= 0 {
			apiErr.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	return apiErr
}

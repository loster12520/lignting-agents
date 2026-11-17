package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/write"
)

var ErrUnavailable = errors.New("tools: capability unavailable")

// Actor 是工具调用者。
type Actor struct {
	Type string
	ID   string
}

// Request 是一次工具调用。
type Request struct {
	CallID string
	Actor  Actor
	TaskID string
	Args   json.RawMessage
}

// Result 是工具返回给 Agent 的内容。IsError=true 表示可读的业务错误。
type Result struct {
	CallID  string         `json:"call_id"`
	Name    string         `json:"name"`
	Content string         `json:"content"`
	IsError bool           `json:"is_error"`
	Meta    map[string]any `json:"meta,omitempty"`
}

type Definition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Tool 是 5 个内置工具的统一接口。
type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Invoke(ctx context.Context, req Request) (Result, error)
}

// History 供 view_change_history 只读查询；*write.Service 满足。
type History interface {
	Timeline(ctx context.Context, f write.TimelineFilter) ([]domain.FileTimelineEntry, error)
}

// SearchProvider 由 core-dev 的 DeepSeek 联网工具或 Search API 适配器实现。
type SearchProvider interface {
	Search(ctx context.Context, query string, limit int) ([]SearchHit, error)
}

type SearchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Options 是工具注册表的构造参数。
type Options struct {
	Root           string
	Writer         *write.Service
	History        History
	Search         SearchProvider
	Audit          write.AuditSink
	ReadMaxBytes   int64
	WriteMaxBytes  int64
	OutputMaxBytes int
	BashTimeout    time.Duration
	BashMaxTimeout time.Duration
	Shell          string
	ShellArgs      []string
}

func (o *Options) applyDefaults() error {
	if o.Root == "" {
		return errors.New("tools: root is required")
	}
	abs, err := filepath.Abs(o.Root)
	if err != nil {
		return err
	}
	o.Root = abs
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	if o.Audit == nil {
		return errors.New("tools: audit sink is required")
	}
	if o.ReadMaxBytes <= 0 {
		o.ReadMaxBytes = 1 << 20
	}
	if o.WriteMaxBytes <= 0 {
		o.WriteMaxBytes = 2 << 20
	}
	if o.OutputMaxBytes <= 0 {
		o.OutputMaxBytes = 64 << 10
	}
	if o.BashTimeout <= 0 {
		o.BashTimeout = 30 * time.Second
	}
	if o.BashMaxTimeout <= 0 {
		o.BashMaxTimeout = 5 * time.Minute
	}
	return nil
}

type Registry struct {
	opts  Options
	tools map[string]Tool
	order []string
}

func NewRegistry(opts Options) (*Registry, error) {
	if err := opts.applyDefaults(); err != nil {
		return nil, err
	}
	r := &Registry{opts: opts, tools: make(map[string]Tool)}
	r.add(&readFileTool{opts: opts})
	r.add(&writeFileTool{opts: opts})
	r.add(&bashTool{opts: opts})
	r.add(&searchTool{opts: opts})
	r.add(&historyTool{opts: opts})
	return r, nil
}

func (r *Registry) add(t Tool) {
	r.tools[t.Name()] = t
	r.order = append(r.order, t.Name())
}

func (r *Registry) Definitions() []Definition {
	out := make([]Definition, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		out = append(out, Definition{Name: t.Name(), Description: t.Description(), Parameters: t.Parameters()})
	}
	return out
}

func (r *Registry) Invoke(ctx context.Context, name string, req Request) (Result, error) {
	t, ok := r.tools[name]
	if !ok {
		res := Result{CallID: req.CallID, Name: name, Content: "unknown tool: " + name, IsError: true}
		_ = r.audit(ctx, req, name, res, "error")
		return res, nil
	}
	if req.CallID == "" {
		req.CallID = domain.NewID("call")
	}
	start := time.Now()
	res, err := t.Invoke(ctx, req)
	if err != nil {
		res = Result{CallID: req.CallID, Name: name, Content: err.Error(), IsError: true}
	}
	res.CallID = req.CallID
	res.Name = name
	if res.Meta == nil {
		res.Meta = map[string]any{}
	}
	res.Meta["duration_ms"] = time.Since(start).Milliseconds()
	result := "ok"
	if res.IsError {
		result = "error"
	}
	if auditErr := r.audit(ctx, req, name, res, result); auditErr != nil {
		return res, fmt.Errorf("%w: %v", write.ErrAudit, auditErr)
	}
	return res, nil
}

func (r *Registry) audit(ctx context.Context, req Request, name string, res Result, result string) error {
	actorType := req.Actor.Type
	if actorType == "" {
		actorType = domain.SenderAgent
	}
	actorID := req.Actor.ID
	if actorID == "" {
		actorID = "unknown"
	}
	detail := map[string]any{
		"args":     summarizeArgs(name, req.Args),
		"is_error": res.IsError,
		"task_id":  req.TaskID,
	}
	if res.IsError {
		detail["error"] = truncate(res.Content, 500)
	}
	b, _ := json.Marshal(detail)
	return r.opts.Audit.RecordAudit(ctx, actorType, actorID, "tool."+name, "tool_call", req.CallID, result, string(b))
}

func summarizeArgs(name string, raw json.RawMessage) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	switch name {
	case "write_file":
		delete(m, "content")
		if c, ok := m["content"]; ok {
			_ = c
		}
	case "bash":
		if cmd, ok := m["command"].(string); ok {
			m["command"] = truncate(cmd, 500)
		}
	case "read_file":
		delete(m, "content")
	}
	return m
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func errorResult(callID, name, msg string) Result {
	return Result{CallID: callID, Name: name, Content: msg, IsError: true}
}

func isBinary(s string) bool { return strings.ContainsRune(s, 0) }

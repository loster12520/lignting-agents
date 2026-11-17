package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lignting/lignting-agents/internal/deepseek"
	"github.com/lignting/lignting-agents/internal/domain"
)

// 5 个工具名（requirements 9.2 / implementation-plan 0.1）。
const (
	ToolReadFile          = "read_file"
	ToolWriteFile         = "write_file"
	ToolBash              = "bash"
	ToolWebSearch         = "web_search"
	ToolViewChangeHistory = "view_change_history"
)

// ToolSpec 是面向模型的工具声明 + 参数校验元数据。
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Required    []string
	Allowed     []string
}

var toolSpecs = []ToolSpec{
	{
		Name:        ToolReadFile,
		Description: "读取工作区内的文本文件；返回带行号的内容。路径必须在工作区内。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "工作区相对路径"},
    "offset": {"type": "integer", "minimum": 1, "description": "起始行（1-based，可选）"},
    "limit": {"type": "integer", "minimum": 1, "description": "最多读取行数（可选）"}
  },
  "required": ["path"]
}`),
		Required: []string{"path"},
		Allowed:  []string{"path", "offset", "limit"},
	},
	{
		Name:        ToolWriteFile,
		Description: "通过共享写工具提交一次文件写入（带 base_hash、冲突检测与变更时间线）。禁止直接用 shell 改文件。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "工作区相对路径"},
    "content": {"type": "string", "description": "新内容"},
    "base_hash": {"type": "string", "description": "读取时拿到的内容哈希，用于冲突检测"},
    "reason": {"type": "string", "description": "为什么改这个文件"}
  },
  "required": ["path", "content"]
}`),
		Required: []string{"path", "content"},
		Allowed:  []string{"path", "content", "base_hash", "reason"},
	},
	{
		Name:        ToolBash,
		Description: "在工作区内执行 shell 命令并返回输出；有超时与输出裁剪。会绕过共享写工具的变更会被标记为不受管变更。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "要执行的命令"},
    "cwd": {"type": "string", "description": "工作区相对目录（可选）"},
    "timeout_seconds": {"type": "integer", "minimum": 1, "maximum": 300, "description": "超时秒数（可选，默认 60）"}
  },
  "required": ["command"]
}`),
		Required: []string{"command"},
		Allowed:  []string{"command", "cwd", "timeout_seconds"},
	},
	{
		Name:        ToolWebSearch,
		Description: "搜索公开网络信息并返回摘要与链接（由 DeepSeek 联网能力提供）。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "搜索词"},
    "max_results": {"type": "integer", "minimum": 1, "maximum": 10, "description": "返回条数（可选）"}
  },
  "required": ["query"]
}`),
		Required: []string{"query"},
		Allowed:  []string{"query", "max_results"},
	},
	{
		Name:        ToolViewChangeHistory,
		Description: "查看文件最近的变更时间线（谁、为什么、diff、任务、分支/commit）。默认每文件保留 30 条。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "工作区相对路径（可选；不传则看最近全局变更）"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "返回条数（可选，默认 30）"}
  }
}`),
		Allowed: []string{"path", "limit"},
	},
}

// ToolSpecs 返回 5 个工具的副本。
func ToolSpecs() []ToolSpec {
	out := make([]ToolSpec, len(toolSpecs))
	copy(out, toolSpecs)
	return out
}

// ToolRegistry 注册 5 个工具并校验参数；执行委托给注入的 ToolExecutor。
type ToolRegistry struct {
	executor ToolExecutor
	specs    map[string]ToolSpec
	order    []string
}

// NewToolRegistry 构造注册表。executor 可为 nil（R2 未接入工具实现时）。
func NewToolRegistry(executor ToolExecutor) *ToolRegistry {
	r := &ToolRegistry{executor: executor, specs: make(map[string]ToolSpec, len(toolSpecs))}
	for _, spec := range toolSpecs {
		r.specs[spec.Name] = spec
		r.order = append(r.order, spec.Name)
	}
	return r
}

// Names 返回工具名（稳定顺序）。
func (r *ToolRegistry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// ToolDefinitions 转为 DeepSeek function tools 声明。
func (r *ToolRegistry) ToolDefinitions() []deepseek.Tool {
	out := make([]deepseek.Tool, 0, len(r.order))
	for _, name := range r.order {
		spec := r.specs[name]
		out = append(out, deepseek.FunctionTool(spec.Name, spec.Description, spec.Parameters))
	}
	return out
}

// Validate 校验工具名与参数（未知工具、非对象、缺必填、未知字段都拒绝）。
func (r *ToolRegistry) Validate(call ToolCall) error {
	spec, ok := r.specs[call.Name]
	if !ok {
		return fmt.Errorf("unknown tool %q", call.Name)
	}
	var args map[string]json.RawMessage
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return fmt.Errorf("tool %s: arguments must be a JSON object: %w", call.Name, err)
		}
	}
	for _, key := range spec.Required {
		if _, ok := args[key]; !ok {
			return fmt.Errorf("tool %s: missing required argument %q", call.Name, key)
		}
	}
	if len(spec.Allowed) > 0 {
		allowed := make(map[string]bool, len(spec.Allowed))
		for _, name := range spec.Allowed {
			allowed[name] = true
		}
		var unknown []string
		for key := range args {
			if !allowed[key] {
				unknown = append(unknown, key)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return fmt.Errorf("tool %s: unknown argument(s) %s", call.Name, strings.Join(unknown, ", "))
		}
	}
	return nil
}

// Execute 校验后执行；未注入 executor 时返回可读错误而不是崩溃。
func (r *ToolRegistry) Execute(ctx context.Context, agent domain.Agent, call ToolCall) (ToolResult, error) {
	if err := r.Validate(call); err != nil {
		return ToolResult{CallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}, err
	}
	if r.executor == nil {
		msg := fmt.Sprintf("工具 %s 尚未接入（R5 实现）；请先用只读方式描述计划。", call.Name)
		return ToolResult{CallID: call.ID, Name: call.Name, Content: msg, IsError: true}, fmt.Errorf("%s", msg)
	}
	return r.executor.Execute(ctx, agent, call)
}

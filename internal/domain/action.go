package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Action 是 Agent 回复中的结构化平台动作（不是 LLM tool call）。
// 只有 core 的动作执行器可以把它落库；文本层只负责解析。
type Action struct {
	Action string          `json:"action"`
	Args   json.RawMessage `json:"args"`
}

// ActionSpec 描述白名单动作的最小参数要求（执行器还要做权限/状态机校验）。
type ActionSpec struct {
	Name        string
	Description string
	Required    []string
}

var ActionSpecs = map[string]ActionSpec{
	"save_intent":             {Name: "save_intent", Description: "保存/更新项目意图卡", Required: []string{"content"}},
	"propose_blueprint":       {Name: "propose_blueprint", Description: "提出项目/部门蓝图并生成 G1", Required: []string{"content"}},
	"provision_departments":   {Name: "provision_departments", Description: "按已批准蓝图创建部门", Required: []string{"blueprint_id"}},
	"create_task":             {Name: "create_task", Description: "创建任务", Required: []string{"title", "department_id", "perspective", "acceptance"}},
	"transition_task":         {Name: "transition_task", Description: "推进任务状态", Required: []string{"task_id", "to"}},
	"submit_artifact":         {Name: "submit_artifact", Description: "提交产出物版本", Required: []string{"task_id", "type", "title", "content"}},
	"request_review":          {Name: "request_review", Description: "发起挑战单", Required: []string{"artifact_id", "severity", "claim", "evidence"}},
	"submit_verdict":          {Name: "submit_verdict", Description: "提交评审裁决", Required: []string{"review_id", "verdict", "rationale"}},
	"create_decision":         {Name: "create_decision", Description: "创建决策记录", Required: []string{"title", "decision", "rationale"}},
	"request_approval":        {Name: "request_approval", Description: "请求人类闸门/不可逆审批", Required: []string{"kind", "title", "payload"}},
	"submit_memory_candidate": {Name: "submit_memory_candidate", Description: "提交候选记忆", Required: []string{"scope", "content", "evidence"}},
}

const actionFence = "lignting-action"

// ParseActions 从正文中提取 lignting-action fenced JSON 块。
// 返回去掉动作块后的可展示正文；非法块会从正文移除并作为 error 返回，
// 由调用方决定“整块拒绝并提示”，不能半执行。
func ParseActions(text string) (string, []Action, []error) {
	var clean strings.Builder
	var actions []Action
	var errs []error
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !isActionFenceStart(trimmed) {
			clean.WriteString(lines[i])
			if i < len(lines)-1 {
				clean.WriteByte('\n')
			}
			continue
		}
		// 找到闭合 fence。
		var body []string
		j := i + 1
		closed := false
		for ; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				closed = true
				break
			}
			body = append(body, lines[j])
		}
		if !closed {
			errs = append(errs, fmt.Errorf("action block at line %d: missing closing fence", i+1))
			clean.WriteString(strings.Join(body, "\n"))
			break
		}
		raw := strings.TrimSpace(strings.Join(body, "\n"))
		var a Action
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			errs = append(errs, fmt.Errorf("action block at line %d: invalid JSON: %w", i+1, err))
			i = j
			continue
		}
		if _, ok := ActionSpecs[a.Action]; !ok {
			errs = append(errs, fmt.Errorf("action block at line %d: unknown action %q", i+1, a.Action))
			i = j
			continue
		}
		actions = append(actions, a)
		i = j
	}
	return strings.TrimSpace(clean.String()), actions, errs
}

func isActionFenceStart(s string) bool {
	if !strings.HasPrefix(s, "```") {
		return false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(s, "```"))
	return strings.EqualFold(rest, actionFence)
}

// Validate 做动作白名单与必填参数的最小校验；权限/状态机由 core 执行器负责。
func (a Action) Validate() error {
	spec, ok := ActionSpecs[a.Action]
	if !ok {
		return fmt.Errorf("unknown action %q", a.Action)
	}
	var args map[string]json.RawMessage
	if len(a.Args) == 0 || string(a.Args) == "null" {
		return fmt.Errorf("action %s: args is empty", a.Action)
	}
	if err := json.Unmarshal(a.Args, &args); err != nil {
		return fmt.Errorf("action %s: args must be an object: %w", a.Action, err)
	}
	for _, key := range spec.Required {
		if _, ok := args[key]; !ok {
			return fmt.Errorf("action %s: missing required arg %q", a.Action, key)
		}
	}
	return nil
}

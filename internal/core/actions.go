package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lignting/lignting-agents/internal/domain"
)

// ErrActionNotImplemented 表示动作尚未在当前轮次接入（后续轮次实现）。
var ErrActionNotImplemented = fmt.Errorf("动作尚未接入")

// ActionHandler 执行一个已通过 Policy 校验的动作，返回给用户看的一句话结果。
type ActionHandler func(ctx context.Context, agent domain.Agent, action domain.Action) (string, error)

// ActionExecutor 是动作执行骨架：白名单/参数已在 domain.Action.Validate 校验，
// 权限与状态机由 Policy 校验，这里只做「取参数 + 落库 + 返回卡片文案」。
type ActionExecutor struct {
	store    Store
	handlers map[string]ActionHandler
}

// NewActionExecutor 构造执行器，并注册 R2 可用的处理器（save_intent）。
func NewActionExecutor(store Store) *ActionExecutor {
	e := &ActionExecutor{store: store, handlers: make(map[string]ActionHandler, len(domain.ActionSpecs))}
	e.handlers["save_intent"] = e.handleSaveIntent
	return e
}

// Register 注册/覆盖动作处理器。
func (e *ActionExecutor) Register(name string, handler ActionHandler) {
	e.handlers[name] = handler
}

// Execute 执行动作。未注册处理器时返回 ErrActionNotImplemented，
// 调用方应把动作整块拒绝并回消息说明（不能半执行）。
func (e *ActionExecutor) Execute(ctx context.Context, agent domain.Agent, action domain.Action) (string, error) {
	if err := action.Validate(); err != nil {
		return "", err
	}
	handler, ok := e.handlers[action.Action]
	if !ok {
		return "", fmt.Errorf("%w：%s（当前轮次未接入）", ErrActionNotImplemented, action.Action)
	}
	return handler(ctx, agent, action)
}

func (e *ActionExecutor) handleSaveIntent(ctx context.Context, agent domain.Agent, action domain.Action) (string, error) {
	if e.store == nil {
		return "", fmt.Errorf("意图卡存储未接入")
	}
	content := argRaw(action.Args, "content")
	if len(content) == 0 {
		return "", fmt.Errorf("save_intent 缺少 content")
	}
	// content 允许是对象或字符串；统一存成 JSON 文本。
	payload := string(content)
	var asString string
	if err := json.Unmarshal(content, &asString); err == nil {
		payload = asString
	}
	id, version, err := e.store.SaveIntent(ctx, payload)
	if err != nil {
		return "", fmt.Errorf("保存意图卡失败：%w", err)
	}
	return fmt.Sprintf("已保存项目意图卡 v%d（%s）", version, id), nil
}

func argRaw(raw json.RawMessage, key string) json.RawMessage {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil
	}
	return args[key]
}

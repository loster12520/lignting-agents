package main

import (
	"context"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/tools"
)

// toolExecutor 把 core 的 5 工具调用适配到 internal/tools.Registry。
type toolExecutor struct{ reg *tools.Registry }

func (e toolExecutor) Execute(ctx context.Context, agent domain.Agent, call core.ToolCall) (core.ToolResult, error) {
	if e.reg == nil {
		return core.ToolResult{CallID: call.ID, Name: call.Name, Content: "工具执行器未初始化", IsError: true}, nil
	}
	res, err := e.reg.Invoke(ctx, call.Name, tools.Request{
		CallID: call.ID,
		Actor:  tools.Actor{Type: domain.SenderAgent, ID: agent.ID},
		TaskID: agent.CurrentTaskID,
		Args:   call.Arguments,
	})
	if err != nil {
		return core.ToolResult{CallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}, err
	}
	return core.ToolResult{CallID: res.CallID, Name: res.Name, Content: res.Content, IsError: res.IsError}, nil
}

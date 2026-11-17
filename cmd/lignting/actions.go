package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// wireActions 把 R3/R4 的平台动作接到 core 的 ActionExecutor。
// 助理通过 lignting-action 提议蓝图/发起 provisioning；权限/状态机已在 core Policy 校验。
func wireActions(rt *core.Runtime, org *core.OrgService, st *store.Store) {
	actions := rt.Options().Actions
	if actions == nil {
		return
	}
	actions.Register("propose_blueprint", func(ctx context.Context, agent domain.Agent, action domain.Action) (string, error) {
		content := argString(action.Args, "content")
		if strings.TrimSpace(content) == "" {
			return "", fmt.Errorf("propose_blueprint 缺少 content")
		}
		intentID, version, err := st.SaveIntent(ctx, content)
		if err != nil {
			return "", fmt.Errorf("保存意图卡失败: %w", err)
		}
		option := argString(action.Args, "option")
		if option == "" {
			option = "full"
		}
		bp, appr, err := org.ProposeBlueprint(ctx, agent, intentID, content, option)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已提出蓝图 %s（方案 %s，意图卡 v%d），G1 审批 %s 等待项目主管确认。", bp.ID, option, version, appr.ID), nil
	})
	actions.Register("provision_departments", func(ctx context.Context, agent domain.Agent, action domain.Action) (string, error) {
		blueprintID := argString(action.Args, "blueprint_id")
		if blueprintID == "" {
			return "", fmt.Errorf("provision_departments 缺少 blueprint_id")
		}
		res, err := org.Provision(ctx, blueprintID, agent.ID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Provisioning 完成：%s（创建部门 %d，Agent %d，会话 %d）", res.Provisioning.Status, len(res.Orphans.Departments), len(res.Orphans.Agents), len(res.Orphans.Conversations)), nil
	})
}

func argString(raw json.RawMessage, key string) string {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(v))
}

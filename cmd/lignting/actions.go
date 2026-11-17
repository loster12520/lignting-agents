package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// wireActions 把 R3/R4 的平台动作接到 core 的 ActionExecutor。
// 权限/状态机已在 core Policy 校验；这里只做参数解析与调用。
func wireActions(rt *core.Runtime, org *core.OrgService, work *core.WorkService, st *store.Store) {
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
		return fmt.Sprintf("Provisioning 完成：%s", res.Provisioning.Status), nil
	})
	actions.Register("create_task", func(ctx context.Context, agent domain.Agent, action domain.Action) (string, error) {
		task, err := work.CreateTask(ctx, agent, core.CreateTaskInput{
			Title:           argString(action.Args, "title"),
			Description:     argString(action.Args, "description"),
			DepartmentID:    argString(action.Args, "department_id"),
			OwnerID:         argString(action.Args, "owner_id"),
			ReviewerID:      argString(action.Args, "reviewer_id"),
			ApproverID:      argString(action.Args, "approver_id"),
			Perspective:     argString(action.Args, "perspective"),
			Priority:        argString(action.Args, "priority"),
			Deadline:        argString(action.Args, "deadline"),
			BudgetTokens:    argInt64(action.Args, "budget_tokens"),
			BudgetCost:      argFloat(action.Args, "budget_cost"),
			Acceptance:      argStrings(action.Args, "acceptance"),
			DependsOn:       argStrings(action.Args, "depends_on"),
			SourceMessageID: argString(action.Args, "source_message_id"),
			ParentID:        argString(action.Args, "parent_id"),
			HighRisk:        argBool(action.Args, "high_risk"),
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已创建任务 %s：%s", task.ID, task.Title), nil
	})
	actions.Register("transition_task", func(ctx context.Context, agent domain.Agent, action domain.Action) (string, error) {
		task, err := work.TransitionTask(ctx, agent, argString(action.Args, "task_id"), argString(action.Args, "to"), argString(action.Args, "reason"), argEvidence(action.Args))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("任务 %s 已转换到 %s", task.ID, task.Status), nil
	})
	actions.Register("submit_artifact", func(ctx context.Context, agent domain.Agent, action domain.Action) (string, error) {
		art, ver, err := work.CreateArtifact(ctx, agent, core.ArtifactInput{
			TaskID:       argString(action.Args, "task_id"),
			Type:         argString(action.Args, "type"),
			Title:        argString(action.Args, "title"),
			Content:      argString(action.Args, "content"),
			DepartmentID: argString(action.Args, "department_id"),
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已登记产出物 %s v%d（%s）", art.ID, ver.Version, ver.ContentHash[:12]), nil
	})
	actions.Register("request_review", func(ctx context.Context, agent domain.Agent, action domain.Action) (string, error) {
		rv, err := work.CreateReview(ctx, agent, core.ReviewInput{
			ArtifactID:         argString(action.Args, "artifact_id"),
			ArtifactVersionID:  argString(action.Args, "artifact_version_id"),
			FromDepartment:     argString(action.Args, "from_department"),
			ToDepartment:       argString(action.Args, "to_department"),
			Severity:           argString(action.Args, "severity"),
			Stance:             argString(action.Args, "stance"),
			Claim:              argString(action.Args, "claim"),
			Evidence:           argEvidence(action.Args),
			Impact:             argString(action.Args, "impact"),
			ProposedResolution: argString(action.Args, "proposed_resolution"),
			DueAt:              argString(action.Args, "due_at"),
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已提交挑战单 %s（%s）", rv.ID, rv.Severity), nil
	})
	actions.Register("submit_verdict", func(ctx context.Context, agent domain.Agent, action domain.Action) (string, error) {
		v, err := work.SubmitVerdict(ctx, agent, argString(action.Args, "review_id"), argString(action.Args, "verdict"), argString(action.Args, "checked"), argString(action.Args, "rationale"), argString(action.Args, "conditions"), argString(action.Args, "residual_risk"))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已提交裁决 %s（%s）", v.ID, v.Verdict), nil
	})
	actions.Register("create_decision", func(ctx context.Context, agent domain.Agent, action domain.Action) (string, error) {
		d, err := work.CreateDecision(ctx, agent, domain.Decision{
			Title:      argString(action.Args, "title"),
			Context:    argString(action.Args, "context"),
			Decision:   argString(action.Args, "decision"),
			Rationale:  argString(action.Args, "rationale"),
			Evidence:   argEvidence(action.Args),
			Dissent:    argString(action.Args, "dissent"),
			Supersedes: argString(action.Args, "supersedes"),
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已创建决策记录 %s：%s", d.ID, d.Title), nil
	})
}

func argMap(raw json.RawMessage) map[string]json.RawMessage {
	var args map[string]json.RawMessage
	_ = json.Unmarshal(raw, &args)
	return args
}

func argString(raw json.RawMessage, key string) string {
	v, ok := argMap(raw)[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(v))
}

func argStrings(raw json.RawMessage, key string) []string {
	v, ok := argMap(raw)[key]
	if !ok {
		return nil
	}
	var out []string
	if err := json.Unmarshal(v, &out); err == nil {
		return out
	}
	if s := argString(raw, key); s != "" {
		return []string{s}
	}
	return nil
}

func argEvidence(raw json.RawMessage) string {
	if s := argString(raw, "evidence"); s != "" {
		return s
	}
	b, _ := json.Marshal(argStrings(raw, "evidence"))
	return string(b)
}

func argInt64(raw json.RawMessage, key string) int64 {
	if s := argString(raw, key); s != "" {
		n, _ := strconv.ParseInt(s, 10, 64)
		return n
	}
	return 0
}

func argFloat(raw json.RawMessage, key string) float64 {
	if s := argString(raw, key); s != "" {
		n, _ := strconv.ParseFloat(s, 64)
		return n
	}
	return 0
}

func argBool(raw json.RawMessage, key string) bool {
	if s := argString(raw, key); s != "" {
		return s == "true" || s == "1"
	}
	return false
}

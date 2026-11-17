package core

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// DefaultWIPLimit 是每 Agent 同时 in_progress 任务上限（可配）。
const DefaultWIPLimit = 2

// MissingDoR 返回 DoR 缺失项；空切片表示满足。
func MissingDoR(t domain.Task, criteria []domain.Criterion) []string {
	var missing []string
	if strings.TrimSpace(t.Title) == "" {
		missing = append(missing, "标题")
	}
	if strings.TrimSpace(t.DepartmentID) == "" {
		missing = append(missing, "部门")
	}
	if strings.TrimSpace(t.OwnerID) == "" {
		missing = append(missing, "owner")
	}
	if strings.TrimSpace(t.ReviewerID) == "" {
		missing = append(missing, "reviewer")
	}
	if strings.TrimSpace(t.ApproverID) == "" {
		missing = append(missing, "approver")
	}
	if t.OwnerID != "" && t.OwnerID == t.ReviewerID {
		missing = append(missing, "reviewer 不能是 owner")
	}
	if strings.TrimSpace(t.Perspective) == "" {
		missing = append(missing, "分工视角")
	}
	if t.BudgetTokens <= 0 && t.BudgetCost <= 0 {
		missing = append(missing, "预算")
	}
	if strings.TrimSpace(t.Deadline) == "" {
		missing = append(missing, "时限")
	}
	if len(criteria) == 0 {
		missing = append(missing, "验收标准")
	}
	return missing
}

// MissingDoD 返回 DoD 缺失项（证据/结论/产出物/blocker）。
func MissingDoD(t domain.Task, criteria []domain.Criterion, artifacts []ArtifactRef) []string {
	var missing []string
	for _, c := range criteria {
		if strings.TrimSpace(c.Evidence) == "" || strings.TrimSpace(c.Evidence) == "[]" {
			missing = append(missing, "证据缺失："+c.Text)
		}
		if c.SupervisorVerdict == "" || c.SupervisorVerdict == "pending" {
			missing = append(missing, "主管未核对："+c.Text)
		}
	}
	if len(criteria) == 0 {
		missing = append(missing, "没有验收标准")
	}
	if len(artifacts) == 0 {
		missing = append(missing, "没有登记产出物")
	}
	if strings.Contains(t.Flags, `"blocker":true`) {
		missing = append(missing, "存在未关闭 blocker")
	}
	return missing
}

// TaskHighRisk 判断任务是否标记为高风险（需要 G2/G3）。
func TaskHighRisk(t domain.Task) bool {
	var flags map[string]any
	if err := json.Unmarshal([]byte(t.Flags), &flags); err != nil {
		return false
	}
	if v, ok := flags["high_risk"].(bool); ok && v {
		return true
	}
	if v, ok := flags["risk"].(string); ok && (v == "high" || v == "critical") {
		return true
	}
	return false
}

// DependencyBlockers 返回仍未完成的依赖任务 ID（空表示可开工）。
func DependencyBlockers(deps []domain.Task) []string {
	var blockers []string
	for _, d := range deps {
		if d.Status != "done" {
			blockers = append(blockers, d.ID+"("+d.Status+")")
		}
	}
	sort.Strings(blockers)
	return blockers
}

// WouldCreateCycle 判断给 taskID 增加依赖 dependsOn 是否成环。
// deps 是依赖图：task -> 它依赖的 tasks。
func WouldCreateCycle(deps map[string][]string, taskID, dependsOn string) []string {
	if taskID == dependsOn {
		return []string{taskID, taskID}
	}
	visited := map[string]bool{}
	var path []string
	var dfs func(cur string) bool
	dfs = func(cur string) bool {
		if cur == taskID {
			path = append(path, cur)
			return true
		}
		if visited[cur] {
			return false
		}
		visited[cur] = true
		path = append(path, cur)
		for _, next := range deps[cur] {
			if dfs(next) {
				return true
			}
		}
		path = path[:len(path)-1]
		return false
	}
	if dfs(dependsOn) {
		return path
	}
	return nil
}

func criterionPassed(c domain.Criterion) bool {
	return strings.TrimSpace(c.Evidence) != "" && strings.TrimSpace(c.Evidence) != "[]" &&
		c.SupervisorVerdict != "" && c.SupervisorVerdict != "pending"
}

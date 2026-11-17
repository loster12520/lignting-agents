package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// ErrEmergencyStop 表示系统处于紧急停止状态。
var ErrEmergencyStop = errors.New("core: emergency stop 生效中")

// isBudgetBlocked 判断错误是否 budget_blocked（用于可读降级文案）。
func isBudgetBlocked(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Code == domain.CodeBudgetBlocked
}

// SettingEmergencyStop 是 platform 写入、core 只读的紧急停止开关。
const SettingEmergencyStop = "emergency_stop"

// SettingReader 读取 platform settings（store.Setting 已满足）。
type SettingReader interface {
	Setting(ctx context.Context, key string) (string, bool, error)
}

// UsageSumer 由 store 实现（domain.UsageQuery/Totals 避免 store→core 反依赖）。
type UsageSumer interface {
	SumUsage(ctx context.Context, q domain.UsageQuery) (domain.UsageTotals, error)
}

// BudgetPolicy 是 core 侧预算上限（0 表示不限制该维度）。
type BudgetPolicy struct {
	DailyCNY      float64 // 全局按天（用户预算）
	MonthlyCNY    float64 // 全局按月（用户预算）
	TaskCNY       float64 // 单任务累计
	DepartmentCNY float64 // 部门按月
	WarnRatio     float64 // 默认 0.8
}

// BudgetWarning 是一条 80% 告警（每条 scope+周期只发一次）。
type BudgetWarning struct {
	Scope string  `json:"scope"`
	Used  float64 `json:"used"`
	Limit float64 `json:"limit"`
	Key   string  `json:"key"`
}

// BudgetCheck 是模型调用前的预算结论。
type BudgetCheck struct {
	Blocked  bool            `json:"blocked"`
	Scope    string          `json:"scope,omitempty"`
	Used     float64         `json:"used,omitempty"`
	Limit    float64         `json:"limit,omitempty"`
	Message  string          `json:"message,omitempty"`
	Warnings []BudgetWarning `json:"warnings,omitempty"`
}

// BudgetGuard 检查每日/每月/任务/部门预算；线程安全，告警去重。
type BudgetGuard struct {
	usage    UsageSumer
	mu       sync.Mutex
	policy   BudgetPolicy
	settings SettingReader // 可动态覆盖预算（设置页追加后无需重启）
	warned   map[string]bool
	now      func() time.Time
}

// NewBudgetGuard 构造 guard；usage 为 nil 时不检查（降级）。
func NewBudgetGuard(usage UsageSumer, policy BudgetPolicy, settings ...SettingReader) *BudgetGuard {
	if policy.WarnRatio <= 0 {
		policy.WarnRatio = 0.8
	}
	guard := &BudgetGuard{usage: usage, policy: policy, warned: map[string]bool{}, now: func() time.Time { return time.Now().UTC() }}
	if len(settings) > 0 {
		guard.settings = settings[0]
	}
	return guard
}

// AttachSettings 绑定设置读取器（NewRuntime 会用 Options.Settings 自动绑定）。
func (g *BudgetGuard) AttachSettings(settings SettingReader) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.settings = settings
	g.mu.Unlock()
}

// SetPolicy 更新预算（用户/主管追加预算后调用）。
func (g *BudgetGuard) SetPolicy(policy BudgetPolicy) {
	if policy.WarnRatio <= 0 {
		policy.WarnRatio = 0.8
	}
	g.mu.Lock()
	g.policy = policy
	g.mu.Unlock()
}

// Policy 返回当前策略副本。
func (g *BudgetGuard) Policy() BudgetPolicy {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.policy
}

// Evaluate 检查各维度预算；100% 直接 Blocked（调用方拒绝新 turn）。
func (g *BudgetGuard) Evaluate(ctx context.Context, agent domain.Agent, taskID string) (BudgetCheck, error) {
	if g == nil || g.usage == nil {
		return BudgetCheck{}, nil
	}
	g.mu.Lock()
	policy := g.policy
	settings := g.settings
	g.mu.Unlock()
	g.applySettings(ctx, settings, agent, taskID, &policy)
	now := g.now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)

	type check struct {
		scope string
		limit float64
		query domain.UsageQuery
		key   string
	}
	checks := []check{}
	if policy.DailyCNY > 0 {
		checks = append(checks, check{"global_day", policy.DailyCNY, domain.UsageQuery{From: dayStart}, "global_day:" + dayStart[:10]})
	}
	if policy.MonthlyCNY > 0 {
		checks = append(checks, check{"global_month", policy.MonthlyCNY, domain.UsageQuery{From: monthStart}, "global_month:" + monthStart[:7]})
	}
	if policy.TaskCNY > 0 && taskID != "" {
		checks = append(checks, check{"task", policy.TaskCNY, domain.UsageQuery{TaskID: taskID}, "task:" + taskID})
	}
	if policy.DepartmentCNY > 0 && agent.DepartmentID != "" {
		checks = append(checks, check{"department_month", policy.DepartmentCNY, domain.UsageQuery{DepartmentID: agent.DepartmentID, From: monthStart}, "department:" + agent.DepartmentID + ":" + monthStart[:7]})
	}
	checkResult := BudgetCheck{}
	for _, item := range checks {
		totals, err := g.usage.SumUsage(ctx, item.query)
		if err != nil {
			continue // 用量查询失败不阻塞模型（降级），但会缺告警/暂停
		}
		used := totals.Cost
		if used >= item.limit {
			checkResult.Blocked = true
			checkResult.Scope = item.scope
			checkResult.Used = used
			checkResult.Limit = item.limit
			checkResult.Message = fmt.Sprintf("预算已用尽（%s）：已用 %.2f / 限额 %.2f CNY。请追加预算或等待周期重置后再试。", scopeLabel(item.scope), used, item.limit)
			break
		}
		if used >= item.limit*policy.WarnRatio {
			g.mu.Lock()
			already := g.warned[item.key]
			if !already {
				g.warned[item.key] = true
			}
			g.mu.Unlock()
			if !already {
				checkResult.Warnings = append(checkResult.Warnings, BudgetWarning{Scope: item.scope, Used: used, Limit: item.limit, Key: item.key})
			}
		}
	}
	return checkResult, nil
}

// applySettings 用 settings 覆盖固定 Policy（缺失/解析失败保留构造值）。
// key：budget.daily_cny / budget.monthly_cny / budget.department.<dept> / budget.task.<task>。
func (g *BudgetGuard) applySettings(ctx context.Context, settings SettingReader, agent domain.Agent, taskID string, policy *BudgetPolicy) {
	if settings == nil {
		return
	}
	if v, ok := settingFloat(ctx, settings, "budget.daily_cny"); ok {
		policy.DailyCNY = v
	}
	if v, ok := settingFloat(ctx, settings, "budget.monthly_cny"); ok {
		policy.MonthlyCNY = v
	}
	if agent.DepartmentID != "" {
		if v, ok := settingFloat(ctx, settings, "budget.department."+agent.DepartmentID); ok {
			policy.DepartmentCNY = v
		}
	}
	if taskID != "" {
		if v, ok := settingFloat(ctx, settings, "budget.task."+taskID); ok {
			policy.TaskCNY = v
		}
	}
}

func settingFloat(ctx context.Context, settings SettingReader, key string) (float64, bool) {
	raw, ok, err := settings.Setting(ctx, key)
	if err != nil || !ok || strings.TrimSpace(raw) == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

func scopeLabel(scope string) string {
	switch scope {
	case "global_day":
		return "每日总预算"
	case "global_month":
		return "每月总预算"
	case "task":
		return "任务预算"
	case "department_month":
		return "部门月度预算"
	default:
		return scope
	}
}

// emergencyStop 读取 platform 的紧急停止开关（读失败按未开启处理，避免误停）。
func (rt *Runtime) emergencyStop(ctx context.Context) bool {
	if rt.opts.Settings == nil {
		return false
	}
	value, ok, err := rt.opts.Settings.Setting(ctx, SettingEmergencyStop)
	if err != nil || !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// checkBudget 在每次模型调用前检查预算并发射 80% 告警（每条 scope 一次）。
func (rt *Runtime) checkBudget(ctx context.Context, agent domain.Agent, conv domain.Conversation) (BudgetCheck, bool) {
	if rt.opts.Budget == nil {
		return BudgetCheck{}, false
	}
	check, err := rt.opts.Budget.Evaluate(ctx, agent, agent.CurrentTaskID)
	if err != nil {
		return BudgetCheck{}, false
	}
	if check.Blocked {
		payload, _ := json.Marshal(map[string]any{
			"agent_id": agent.ID, "task_id": agent.CurrentTaskID,
			"conversation_id": conv.ID, "scope": check.Scope,
			"used": check.Used, "limit": check.Limit, "message": check.Message,
		})
		_, _ = rt.opts.Store.AppendEvent(ctx, domain.Event{
			Type: "budget.blocked", ConversationID: conv.ID, ObjectType: "agent", ObjectID: agent.ID,
			ActorType: "system", ActorID: "core", Payload: string(payload), CreatedAt: rt.now().Format(time.RFC3339Nano),
		})
	}
	for _, warning := range check.Warnings {
		payload, _ := json.Marshal(warning)
		_, _ = rt.opts.Store.AppendEvent(ctx, domain.Event{
			Type: "budget.warning", ConversationID: conv.ID, ObjectType: "agent", ObjectID: agent.ID,
			ActorType: "system", ActorID: "core", Payload: string(payload), CreatedAt: rt.now().Format(time.RFC3339Nano),
		})
		detail, _ := json.Marshal(warning)
		_, _ = rt.opts.Store.AppendAudit(ctx, domain.AuditEntry{
			ActorType: "system", ActorID: "core", Action: "budget.warning",
			ObjectType: "agent", ObjectID: agent.ID, Result: "ok", Detail: string(detail),
			CreatedAt: rt.now().Format(time.RFC3339Nano),
		})
	}
	return check, check.Blocked
}

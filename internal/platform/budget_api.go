package platform

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// R8 预算平台侧：settings keys + 80%/100% 告警投影（判定在 core.BudgetGuard）。

const (
	budgetDailyKey      = "budget.daily_cny"
	budgetMonthlyKey    = "budget.monthly_cny"
	budgetDeptPrefix    = "budget.department."
	budgetTaskPrefix    = "budget.task."
	budgetNotifyLastSeq = "budget.notify_last_seq"
)

type budgetSettingsDTO struct {
	DailyCNY    float64            `json:"daily_cny"`
	MonthlyCNY  float64            `json:"monthly_cny"`
	Departments map[string]float64 `json:"departments"`
	Tasks       map[string]float64 `json:"tasks"`
}

func (s *Server) budgetSettings(ctx context.Context) (budgetSettingsDTO, error) {
	settings, err := s.store.SettingsWithPrefix(ctx, "")
	if err != nil {
		return budgetSettingsDTO{}, err
	}
	out := budgetSettingsDTO{Departments: map[string]float64{}, Tasks: map[string]float64{}}
	out.DailyCNY = settingsFloat(settings, budgetDailyKey, settingsFloat(settings, "daily_budget_cny", s.cfg.DailyBudgetCNY))
	out.MonthlyCNY = settingsFloat(settings, budgetMonthlyKey, settingsFloat(settings, "monthly_budget_cny", s.cfg.MonthlyBudgetCNY))
	for k, v := range settings {
		if !strings.HasPrefix(k, budgetDeptPrefix) {
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			out.Departments[strings.TrimPrefix(k, budgetDeptPrefix)] = f
		}
	}
	for k, v := range settings {
		if !strings.HasPrefix(k, budgetTaskPrefix) {
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			out.Tasks[strings.TrimPrefix(k, budgetTaskPrefix)] = f
		}
	}
	return out, nil
}

func (s *Server) handleGetBudgets(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	out, err := s.budgetSettings(r.Context())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, out)
}

type budgetUpdateRequest struct {
	DailyCNY    *float64           `json:"daily_cny"`
	MonthlyCNY  *float64           `json:"monthly_cny"`
	Departments map[string]float64 `json:"departments"`
	Tasks       map[string]float64 `json:"tasks"`
}

func validBudgetNumber(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1e9
}

func (s *Server) handlePutBudgets(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req budgetUpdateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	updates := map[string]string{}
	changed := []string{}
	if req.DailyCNY != nil {
		if !validBudgetNumber(*req.DailyCNY) {
			s.writeErr(w, errInvalid("daily_cny 不合法"))
			return
		}
		updates[budgetDailyKey] = strconv.FormatFloat(*req.DailyCNY, 'f', 2, 64)
		updates["daily_budget_cny"] = updates[budgetDailyKey] // 兼容旧设置键
		changed = append(changed, "global_day")
	}
	if req.MonthlyCNY != nil {
		if !validBudgetNumber(*req.MonthlyCNY) {
			s.writeErr(w, errInvalid("monthly_cny 不合法"))
			return
		}
		updates[budgetMonthlyKey] = strconv.FormatFloat(*req.MonthlyCNY, 'f', 2, 64)
		updates["monthly_budget_cny"] = updates[budgetMonthlyKey]
		changed = append(changed, "global_month")
	}
	for deptID, amount := range req.Departments {
		deptID = strings.TrimSpace(deptID)
		if deptID == "" {
			continue
		}
		if !validBudgetNumber(amount) {
			s.writeErr(w, errInvalid("部门预算不合法："+deptID))
			return
		}
		if _, err := s.store.GetDepartment(ctx, deptID); err != nil {
			s.writeErr(w, err)
			return
		}
		updates[budgetDeptPrefix+deptID] = strconv.FormatFloat(amount, 'f', 2, 64)
		changed = append(changed, "department:"+deptID)
	}
	for taskID, amount := range req.Tasks {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		if !validBudgetNumber(amount) {
			s.writeErr(w, errInvalid("任务预算不合法："+taskID))
			return
		}
		if _, err := s.store.GetTask(ctx, taskID); err != nil {
			s.writeErr(w, err)
			return
		}
		updates[budgetTaskPrefix+taskID] = strconv.FormatFloat(amount, 'f', 2, 64)
		changed = append(changed, "task:"+taskID)
	}
	if len(updates) > 0 {
		if err := s.store.SetSettings(ctx, updates); err != nil {
			s.writeErr(w, err)
			return
		}
		s.audit(ctx, domain.SenderUser, owner.ID, "budget.update", "settings", "budget", "ok",
			jsonDetail(map[string]any{"changed": changed}))
	}
	out, err := s.budgetSettings(ctx)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// scanBudgetEvents 把 core 写入的 budget.warning / budget.blocked 事件投影为收件箱通知。
// 判定（80%/100%）在 core；平台只通知，去重由 notifications 唯一约束 + 周期 key 保证。
func (s *Server) scanBudgetEvents(ctx context.Context) (int, error) {
	last, _, err := s.store.Setting(ctx, budgetNotifyLastSeq)
	if err != nil {
		return 0, err
	}
	lastSeq, _ := strconv.ParseInt(strings.TrimSpace(last), 10, 64)
	events, err := s.store.ListEventsSince(ctx, lastSeq, "", 500)
	if err != nil {
		return 0, err
	}
	created := 0
	maxSeq := lastSeq
	for _, ev := range events {
		if ev.Seq > maxSeq {
			maxSeq = ev.Seq
		}
		if ev.Type != "budget.warning" && ev.Type != "budget.blocked" {
			continue
		}
		var payload map[string]any
		_ = json.Unmarshal([]byte(ev.Payload), &payload)
		scope, _ := payload["scope"].(string)
		key, _ := payload["key"].(string)
		agentID, _ := payload["agent_id"].(string)
		taskID, _ := payload["task_id"].(string)
		msg, _ := payload["message"].(string)
		used, _ := payload["used"].(float64)
		limit, _ := payload["limit"].(float64)
		// 周期键：warning 用 key（scope+周期）；blocked 无 key，用 日/月 粒度去重，
		// 避免每次被拒都刷一条通知（core 每次拒绝都会写 blocked 事件）。
		period := ev.CreatedAt
		if len(period) >= 10 {
			period = period[:10]
			if strings.Contains(scope, "month") && len(ev.CreatedAt) >= 7 {
				period = ev.CreatedAt[:7]
			}
		}
		level := "need_know"
		title := "预算使用达到 80%"
		objectType := "budget"
		objectID := scope + ":" + key
		if objectID == ":" {
			objectID = "budget:" + scope + ":" + period
		}
		body := "预算范围：" + scope
		if key != "" {
			body += "（周期 " + key + "）"
		}
		if ev.Type == "budget.blocked" || (limit > 0 && used >= limit) {
			level = "blocking"
			title = "预算已用尽，相关 Agent/任务已被暂停"
			objectType = "budget_blocked"
			objectID = "blocked:" + scope + ":" + taskID + ":" + agentID + ":" + period
			if msg != "" {
				body = msg
			}
		}
		if limit > 0 {
			body += "；已用 " + strconv.FormatFloat(used, 'f', 2, 64) + " / " + strconv.FormatFloat(limit, 'f', 2, 64)
		}
		if _, err := s.notify(ctx, level, title, body, objectType, objectID, ""); err == nil {
			created++
		}
		if ev.Type == "budget.blocked" {
			s.audit(ctx, domain.SenderSystem, "system", "budget.blocked", "budget", objectID, "ok",
				jsonDetail(map[string]any{"scope": scope, "key": key, "used": used, "limit": limit}))
		}
	}
	if maxSeq > lastSeq {
		if err := s.store.SetSetting(ctx, budgetNotifyLastSeq, strconv.FormatInt(maxSeq, 10)); err != nil {
			return created, err
		}
	}
	return created, nil
}

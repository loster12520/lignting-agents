package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// R3/R8：通知收件箱、审批 TTL、调度与本地密码（task-26）。

const (
	scheduleInterval  = 30 * time.Second
	loginFailWindow   = 10 * time.Minute
	loginFailMax      = 5
	defaultInterruptN = 3
	settingInterrupt  = "notify.interrupt_budget"
	settingPauseDept  = "approval.timeout_pause_departments"
)

func todayStart() string { return time.Now().UTC().Format("2006-01-02") + "T00:00:00.000000000Z" }

// startRunner 启动调度执行器：启动时补跑一次到期任务，之后每 30s 扫描。
func (s *Server) startRunner() {
	ctx, cancel := context.WithCancel(context.Background())
	s.runnerMu.Lock()
	s.runnerCancel = cancel
	s.runnerDone = make(chan struct{})
	done := s.runnerDone
	s.runnerMu.Unlock()
	if n, err := s.RunSchedulesOnce(ctx); err != nil {
		s.logf("补跑到期任务失败: %v", err)
	} else if n > 0 {
		s.logf("已补跑 %d 个到期任务", n)
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(scheduleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.RunSchedulesOnce(ctx); err != nil && ctx.Err() == nil {
					s.logf("调度扫描失败: %v", err)
				}
			}
		}
	}()
}

// Close 停止后台调度器（不改动 store；store 由 cmd 关闭）。
func (s *Server) Close() {
	s.runnerMu.Lock()
	cancel, done := s.runnerCancel, s.runnerDone
	s.runnerCancel, s.runnerDone = nil, nil
	s.runnerMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// RunSchedulesOnce 处理所有已到期 pending 任务；返回处理条数（可测试直接调用）。
func (s *Server) RunSchedulesOnce(ctx context.Context) (int, error) {
	due, err := s.store.ListDueSchedules(ctx, store.Now(), 100)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sc := range due {
		status := "done"
		if err := s.runSchedule(ctx, sc); err != nil {
			status = "failed"
			s.logf("任务 %s(%s) 执行失败: %v", sc.ID, sc.Kind, err)
		}
		if err := s.store.SetScheduleStatus(ctx, sc.ID, status); err != nil {
			s.logf("更新任务状态失败 %s: %v", sc.ID, err)
		}
		n++
	}
	// R8 预算告警投影（判定在 core；这里只把事件变成收件箱通知）。
	if created, err := s.scanBudgetEvents(ctx); err != nil && ctx.Err() == nil {
		s.logf("预算告警扫描失败: %v", err)
	} else {
		n += created
	}
	return n, nil
}

func schedulePayload(sc store.Schedule) map[string]string {
	out := map[string]string{}
	var raw map[string]any
	if json.Unmarshal([]byte(sc.Payload), &raw) == nil {
		for k, v := range raw {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}

func (s *Server) runSchedule(ctx context.Context, sc store.Schedule) error {
	payload := schedulePayload(sc)
	switch sc.Kind {
	case "approval.expire":
		approvalID := payload["approval_id"]
		if approvalID == "" {
			s.audit(ctx, domain.SenderSystem, "system", "schedule.invalid", "schedule", sc.ID, "denied", jsonDetail(map[string]any{"kind": sc.Kind}))
			return nil
		}
		approval, changed, err := s.store.ExpireApproval(ctx, approvalID)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		_, err = s.notify(ctx, "blocking", "审批已到期未处理", "审批「"+approval.Title+"」已超过 TTL，标记为过期并阻塞；系统不会自动执行，请人工处理。",
			"approval", approval.ID, "")
		s.audit(ctx, domain.SenderSystem, "system", "approval.expire", "approval", approval.ID, "ok", jsonDetail(map[string]any{"ttl": approval.ExpiresAt}))
		return err
	case "department.timeout":
		deptID := payload["department_id"]
		if deptID == "" {
			return nil
		}
		dept, err := s.store.GetDepartment(ctx, deptID)
		if err != nil {
			return nil // 部门已删除，跳过
		}
		if dept.Status != domain.DeptActive {
			return nil
		}
		pause := true
		if v, ok, _ := s.store.Setting(ctx, settingPauseDept); ok {
			pause = v != "false" && v != "0"
		}
		status := "degraded"
		if pause {
			if err := s.store.UpdateDepartmentStatus(ctx, deptID, domain.DeptPaused); err != nil {
				return err
			}
			status = domain.DeptPaused
		}
		_, err = s.notify(ctx, "blocking", "部门 24h 无响应已降级", "部门「"+dept.Name+"」超过时限未响应，已降级为 "+status+"；恢复需人工确认。",
			"department", deptID, "")
		s.audit(ctx, domain.SenderSystem, "system", "department.degraded", "department", deptID, "ok", jsonDetail(map[string]any{"status": status}))
		return err
	case "snooze.expire":
		key := payload["setting_key"]
		if key == "" {
			return nil
		}
		if err := s.store.DeleteSetting(ctx, key); err != nil {
			return err
		}
		if payload["object_type"] == "notification" {
			if _, err := s.store.SetNotificationStatus(ctx, payload["object_id"], "unread"); err != nil {
				s.logf("snooze restore notification failed: %v", err)
			}
		}
		s.audit(ctx, domain.SenderSystem, "system", "snooze.restore", payload["object_type"], payload["object_id"], "ok",
			jsonDetail(map[string]any{"key": key}))
		return nil
	case "delegate.expire":
		key := payload["setting_key"]
		if key == "" {
			return nil
		}
		if err := s.store.DeleteSetting(ctx, key); err != nil {
			return err
		}
		s.audit(ctx, domain.SenderSystem, "system", "delegate.expire", "approval", payload["approval_id"], "ok", "{}")
		return nil
	case "bridge.expire":
		bridgeID := payload["bridge_id"]
		if bridgeID == "" {
			return nil
		}
		bridge, changed, err := s.store.ExpireBridge(ctx, bridgeID)
		if err != nil || !changed {
			return err
		}
		_, err = s.notify(ctx, "need_know", "临时桥已到期关闭", "临时桥「"+bridge.Scope+"」("+bridge.FromType+":"+bridge.FromID+" → "+bridge.ToType+":"+bridge.ToID+") 已到期自动关闭。",
			"bridge", bridge.ID, "")
		_, _ = s.store.AppendEvent(ctx, domain.Event{
			Type: "bridge.expired", ObjectType: "bridge", ObjectID: bridge.ID,
			ActorType: domain.SenderSystem, ActorID: "system",
			Payload: jsonDetail(map[string]any{"scope": bridge.Scope, "from": bridge.FromType + ":" + bridge.FromID, "to": bridge.ToType + ":" + bridge.ToID}),
		})
		s.audit(ctx, domain.SenderSystem, "system", "bridge.expire", "bridge", bridge.ID, "ok", jsonDetail(map[string]any{"expires_at": bridge.ExpiresAt}))
		return err
	default:
		s.audit(ctx, domain.SenderSystem, "system", "schedule.skipped", "schedule", sc.ID, "ok", jsonDetail(map[string]any{"kind": sc.Kind}))
		return nil
	}
}

// notify 写收件箱 + 审计 + notification.created 事件；实时打扰预算内降级为 need_know。
func (s *Server) notify(ctx context.Context, level, title, body, objectType, objectID, conversationID string) (store.Notification, error) {
	if level == "need_decide" || level == "blocking" {
		budget := defaultInterruptN
		if v, ok, _ := s.store.Setting(ctx, settingInterrupt); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
				budget = n
			}
		}
		if cnt, err := s.store.CountNotificationsSince(ctx, []string{"need_decide", "blocking"}, todayStart()); err == nil && cnt >= int64(budget) {
			level = "need_know"
			body += "（今日实时打扰预算已用完，本条降级为稍后处理）"
		}
	}
	n, created, err := s.store.CreateNotification(ctx, store.Notification{
		Level: level, Title: title, Body: body,
		ConversationID: conversationID, ObjectType: objectType, ObjectID: objectID,
	})
	if err != nil || !created {
		return n, err
	}
	_, _ = s.store.AppendEvent(ctx, domain.Event{
		Type: "notification.created", ConversationID: conversationID, ObjectType: "notification", ObjectID: n.ID,
		ActorType: domain.SenderSystem, ActorID: "system", Payload: jsonDetail(n),
	})
	s.audit(ctx, domain.SenderSystem, "system", "notification.create", "notification", n.ID, "ok",
		jsonDetail(map[string]any{"level": n.Level, "object_type": objectType, "object_id": objectID}))
	return n, nil
}

// ---- HTTP handlers ----

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	status := r.URL.Query().Get("status")
	limit, err := parseInt64Param(r.URL.Query().Get("limit"), 100)
	if err != nil || limit <= 0 || limit > 500 {
		s.writeErr(w, errInvalid("limit 必须在 1..500"))
		return
	}
	items, err := s.store.ListNotifications(r.Context(), status, int(limit))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

type notificationStatusRequest struct {
	Status string `json:"status"`
}

func (s *Server) handleNotificationStatus(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req notificationStatusRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	n, err := s.store.SetNotificationStatus(ctx, r.PathValue("id"), strings.TrimSpace(req.Status))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "notification.status", "notification", n.ID, "ok", jsonDetail(map[string]any{"status": n.Status}))
	writeOK(w, n)
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	q := r.URL.Query()
	items, err := s.store.ListSchedules(r.Context(), q.Get("status"), q.Get("kind"), 200)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

func (s *Server) handleCancelSchedule(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	sc, err := s.store.CancelSchedule(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "schedule.cancel", "schedule", sc.ID, "ok", jsonDetail(map[string]any{"kind": sc.Kind}))
	writeOK(w, sc)
}

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req passwordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if len(req.NewPassword) < 8 || len(req.NewPassword) > 128 {
		s.writeErr(w, errInvalid("新密码长度必须在 8..128"))
		return
	}
	if owner.HasPassword() && !store.VerifyPassword(owner.PasswordHash, req.CurrentPassword) {
		s.audit(ctx, domain.SenderUser, owner.ID, "settings.password.set", "owner", owner.ID, "denied", jsonDetail(map[string]any{"reason": "bad_current_password"}))
		s.writeErr(w, errUnauthorized("当前密码不正确"))
		return
	}
	if err := s.store.SetOwnerPassword(ctx, req.NewPassword); err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "settings.password.set", "owner", owner.ID, "ok", "{}")
	writeOK(w, map[string]any{"has_password": true})
}

// recentLoginFailures 统计窗口内登录失败次数（登录限流）。
func (s *Server) recentLoginFailures(ctx context.Context, ownerID string) int64 {
	since := time.Now().Add(-loginFailWindow).UTC().Format(fixedTimeLayout)
	n, err := s.store.CountAudit(ctx, store.AuditFilter{
		ActorType: domain.SenderUser, ActorID: ownerID, Action: "login", Result: "denied", SinceCreatedAt: since,
	})
	if err != nil {
		return 0
	}
	return n
}

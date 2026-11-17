package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

func jsonDetail(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// audit 写审计；失败只记日志，不回滚已提交的业务数据。
func (s *Server) audit(ctx context.Context, actorType, actorID, action, objectType, objectID, result, detail string) {
	if _, err := s.store.AppendAudit(ctx, domain.AuditEntry{
		ActorType: actorType, ActorID: actorID, Action: action,
		ObjectType: objectType, ObjectID: objectID, Result: result, Detail: detail,
	}); err != nil {
		s.logf("写审计失败 action=%s object=%s/%s: %v", action, objectType, objectID, err)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	if r.Body == nil {
		return errInvalid("请求体为空")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		return errInvalid("请求体不是合法 JSON")
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"version":            config.Version,
		"model":              s.cfg.Model,
		"model_mode":         s.cfg.ModelMode,
		"schema_version":     store.SchemaVersion,
		"search_index_state": s.indexStateValue(),
		"uptime_seconds":     int(time.Since(s.start).Seconds()),
		"now":                store.Now(),
	})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	cur, err := s.currentOwner(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	data, err := s.bootstrapData(r, cur)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, data)
}

// bootstrapData 组装 /api/bootstrap 与 setup/login 的同构响应。
func (s *Server) bootstrapData(r *http.Request, cur *store.Owner) (map[string]any, error) {
	ctx := r.Context()
	owner, err := s.store.GetOwner(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"setup_required":     owner == nil,
		"authenticated":      false,
		"version":            config.Version,
		"schema_version":     store.SchemaVersion,
		"model":              s.cfg.Model,
		"model_mode":         s.cfg.ModelMode,
		"search_index_state": s.indexStateValue(),
		"csrf_token":         "",
		"owner":              nil,
	}
	if owner == nil {
		return out, nil
	}
	out["owner"] = toOwnerDTO(owner)
	if cur == nil || cur.ID != owner.ID {
		return out, nil
	}
	token, err := s.csrfToken(cur.ID)
	if err != nil {
		return nil, err
	}
	agents, err := s.store.ListAgents(ctx, store.AgentFilter{})
	if err != nil {
		return nil, err
	}
	departments, err := s.store.ListDepartments(ctx, store.DepartmentFilter{})
	if err != nil {
		return nil, err
	}
	convs, err := s.store.ListConversations(ctx, store.ConversationFilter{})
	if err != nil {
		return nil, err
	}
	dtos, err := s.conversationDTOs(ctx, convs)
	if err != nil {
		return nil, err
	}
	settings, err := s.settingsData(ctx, cur)
	if err != nil {
		return nil, err
	}
	out["authenticated"] = true
	out["csrf_token"] = token
	out["settings"] = settings
	out["agents"] = agents
	out["departments"] = departments
	out["conversations"] = dtos
	return out, nil
}

type setupRequest struct {
	OwnerName        string  `json:"owner_name"`
	DeepSeekAPIKey   string  `json:"deepseek_api_key"`
	DailyBudgetCNY   float64 `json:"daily_budget_cny"`
	MonthlyBudgetCNY float64 `json:"monthly_budget_cny"`
	Locale           string  `json:"locale"`
}

func validBudget(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1e9
}

// assistantGreeting 是 setup 时写入的静态欢迎辞（AC-01：能力/边界/暂停/查看行为/邀请目标）。
// 不依赖模型，离线可用；R2 之后可由 core 的模型 greeting 替换。
const assistantGreeting = `你好，我是你的助理。这个工作台由你（项目主管）指挥，我负责把想法变成可执行的组织与任务。

我能做什么：
- 澄清目标与范围，整理项目意图卡，提出项目/部门蓝图
- 在你批准 G1 后协调部门主管组建产品/研发/测试部门
- 汇总进展、阻塞与风险，帮你过滤不重要的消息
- 在你需要时检索历史消息、决策与产出物

我不做什么：
- 不代替你批准预算/章程，不批准高风险/不可逆动作
- 不修改自己的身份与权限，不删除审计
- 不绕过部门主管直接给员工派活（紧急情况除外）

如何暂停我：在「Agent 管理」里暂停/恢复我；全局紧急停止会立即停止所有 Agent，恢复后从暂停点继续。
如何查看我的行为：所有消息、事件与工具动作都在平台留档，可在会话历史与审计里查看。

下一步：告诉我你想做的第一件事（一句话即可）。我会先和你确认目标，不会擅自创建部门。`

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	existing, err := s.store.GetOwner(ctx)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if existing != nil {
		s.writeErr(w, errConflict("已完成初始化，无需重复 setup"))
		return
	}
	var req setupRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	name := strings.TrimSpace(req.OwnerName)
	if name == "" {
		s.writeErr(w, errInvalid("owner_name 必填"))
		return
	}
	if utf8.RuneCountInString(name) > 50 {
		s.writeErr(w, errInvalid("owner_name 过长（上限 50 字）"))
		return
	}
	if !validBudget(req.DailyBudgetCNY) || !validBudget(req.MonthlyBudgetCNY) {
		s.writeErr(w, errInvalid("预算必须是非负数"))
		return
	}
	locale := strings.TrimSpace(req.Locale)
	if locale == "" {
		locale = "zh-CN"
	}
	if locale != "zh-CN" && locale != "en-US" {
		s.writeErr(w, errInvalid("不支持的 locale"))
		return
	}
	if key := strings.TrimSpace(req.DeepSeekAPIKey); key != "" {
		if len(key) < 8 {
			s.writeErr(w, errInvalid("deepseek_api_key 格式不正确"))
			return
		}
		if err := s.saveSecrets(config.Secrets{DeepSeekAPIKey: key}); err != nil {
			s.logf("保存密钥失败: %v", err)
			s.writeErr(w, errInvalid("保存密钥失败，请检查数据目录权限"))
			return
		}
	}

	keyBytes, keyHex, err := generateSecret()
	if err != nil {
		s.writeErr(w, err)
		return
	}
	now := store.Now()
	owner := store.Owner{ID: store.OwnerID, Name: name, CreatedAt: now}
	assistant := domain.Agent{
		ID:          domain.NewID("agent"),
		Name:        "助理",
		Kind:        domain.AgentKindAssistant,
		Status:      domain.AgentIdle,
		Model:       s.cfg.Model,
		Perspective: "全局幕僚",
		RoleCard:    jsonDetail(map[string]any{"mission": "澄清目标、划分部门、协调节奏、过滤注意力"}),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	conv := domain.Conversation{
		ID:         domain.NewID("conv"),
		Type:       domain.ConvDM,
		Title:      "助理",
		Visibility: domain.VisibilityPrivate,
		Status:     "active",
		CreatedBy:  store.OwnerID,
	}
	members := []domain.ConversationMember{
		{PrincipalType: domain.SenderUser, PrincipalID: store.OwnerID, Role: "owner", JoinedAt: now},
		{PrincipalType: domain.SenderAgent, PrincipalID: assistant.ID, Role: "member", JoinedAt: now},
	}
	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := store.CreateOwnerTx(tx, owner); err != nil {
			return err
		}
		if err := store.CreateAgentTx(tx, assistant); err != nil {
			return err
		}
		if _, err := store.CreateConversationTx(ctx, tx, conv, members, store.Actor{Type: domain.SenderUser, ID: owner.ID}); err != nil {
			return err
		}
		settings := map[string]string{
			"owner_name":         name,
			"locale":             locale,
			"daily_budget_cny":   formatBudget(req.DailyBudgetCNY),
			"monthly_budget_cny": formatBudget(req.MonthlyBudgetCNY),
			"model":              s.cfg.Model,
			"setup.completed_at": now,
			sessionSecretKey:     keyHex,
		}
		if err := store.SetSettingsTx(tx, settings); err != nil {
			return err
		}
		if _, err := s.store.AppendAuditTx(ctx, tx, domain.AuditEntry{
			ActorType: domain.SenderUser, ActorID: owner.ID, Action: "setup",
			ObjectType: "owner", ObjectID: owner.ID, Result: "ok",
			Detail: jsonDetail(map[string]any{
				"assistant_id":    assistant.ID,
				"conversation_id": conv.ID,
				"has_api_key":     s.hasAPIKey(),
			}),
		}); err != nil {
			return err
		}
		// 助理主动出现：静态欢迎辞，消息 + 事件 + 审计同一事务落库。
		greeting, err := s.store.AppendMessageTx(ctx, tx, domain.Message{
			ConversationID: conv.ID,
			SenderType:     domain.SenderAgent,
			SenderID:       assistant.ID,
			SenderName:     assistant.Name,
			Type:           domain.MsgText,
			Content:        assistantGreeting,
			Meta:           `{"kind":"greeting"}`,
			CreatedAt:      now,
		})
		if err != nil {
			return err
		}
		if _, err := s.store.AppendEventTx(ctx, tx, domain.Event{
			Type: "message.created", ConversationID: conv.ID, ObjectType: "message", ObjectID: greeting.ID,
			ActorType: domain.SenderAgent, ActorID: assistant.ID, Payload: jsonDetail(greeting), CreatedAt: now,
		}); err != nil {
			return err
		}
		if _, err := s.store.AppendAuditTx(ctx, tx, domain.AuditEntry{
			ActorType: domain.SenderAgent, ActorID: assistant.ID, Action: "message.create",
			ObjectType: "message", ObjectID: greeting.ID, Result: "ok",
			Detail: jsonDetail(map[string]any{"conversation_id": conv.ID, "seq": greeting.Seq, "kind": "greeting"}),
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		s.logf("setup 失败: %v", err)
		s.writeErr(w, err)
		return
	}
	s.useSecret(keyBytes)
	if err := s.issueSession(w, owner.ID); err != nil {
		s.writeErr(w, err)
		return
	}
	data, err := s.bootstrapData(r, &owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, data)
}

type loginRequest struct {
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	owner, err := s.store.GetOwner(ctx)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if owner == nil {
		s.writeErr(w, errConflict("尚未初始化，请先完成 setup"))
		return
	}
	var req loginRequest
	_ = decodeJSON(w, r, &req)
	if owner.HasPassword() {
		if req.Password == "" || !store.VerifyPassword(owner.PasswordHash, req.Password) {
			s.audit(ctx, domain.SenderUser, owner.ID, "login", "owner", owner.ID, "denied", jsonDetail(map[string]any{"reason": "bad_password"}))
			s.writeErr(w, errUnauthorized("密码不正确"))
			return
		}
	} else if !requireLoopback(r) {
		s.audit(ctx, domain.SenderUser, owner.ID, "login", "owner", owner.ID, "denied", jsonDetail(map[string]any{"reason": "non_loopback_without_password"}))
		s.writeErr(w, errForbidden("未设置密码时仅允许本机自动登录"))
		return
	}
	if err := s.issueSession(w, owner.ID); err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "login", "owner", owner.ID, "ok", "{}")
	data, err := s.bootstrapData(r, owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	owner, err := s.currentOwner(r)
	if err == nil && owner != nil {
		s.audit(ctx, domain.SenderUser, owner.ID, "logout", "owner", owner.ID, "ok", "{}")
	}
	s.clearSession(w)
	writeOK(w, map[string]any{"logged_out": true})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	data, err := s.settingsData(r.Context(), owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, data)
}

type settingsUpdateRequest struct {
	OwnerName        *string  `json:"owner_name"`
	Locale           *string  `json:"locale"`
	DailyBudgetCNY   *float64 `json:"daily_budget_cny"`
	MonthlyBudgetCNY *float64 `json:"monthly_budget_cny"`
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req settingsUpdateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	updates := map[string]string{}
	changed := []string{}
	if req.OwnerName != nil {
		name := strings.TrimSpace(*req.OwnerName)
		if name == "" || utf8.RuneCountInString(name) > 50 {
			s.writeErr(w, errInvalid("owner_name 不合法"))
			return
		}
		if err := s.store.UpdateOwnerName(ctx, name); err != nil {
			s.writeErr(w, err)
			return
		}
		updates["owner_name"] = name
		changed = append(changed, "owner_name")
	}
	if req.Locale != nil {
		locale := strings.TrimSpace(*req.Locale)
		if locale != "zh-CN" && locale != "en-US" {
			s.writeErr(w, errInvalid("不支持的 locale"))
			return
		}
		updates["locale"] = locale
		changed = append(changed, "locale")
	}
	if req.DailyBudgetCNY != nil {
		if !validBudget(*req.DailyBudgetCNY) {
			s.writeErr(w, errInvalid("daily_budget_cny 不合法"))
			return
		}
		updates["daily_budget_cny"] = formatBudget(*req.DailyBudgetCNY)
		changed = append(changed, "daily_budget_cny")
	}
	if req.MonthlyBudgetCNY != nil {
		if !validBudget(*req.MonthlyBudgetCNY) {
			s.writeErr(w, errInvalid("monthly_budget_cny 不合法"))
			return
		}
		updates["monthly_budget_cny"] = formatBudget(*req.MonthlyBudgetCNY)
		changed = append(changed, "monthly_budget_cny")
	}
	if len(updates) > 0 {
		if err := s.store.SetSettings(ctx, updates); err != nil {
			s.writeErr(w, err)
			return
		}
		s.audit(ctx, domain.SenderUser, owner.ID, "settings.update", "settings", "", "ok", jsonDetail(map[string]any{"keys": changed}))
	}
	data, err := s.settingsData(ctx, owner)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, data)
}

type apiKeyRequest struct {
	APIKey string `json:"api_key"`
}

func (s *Server) handleSetAPIKey(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req apiKeyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	key := strings.TrimSpace(req.APIKey)
	if len(key) < 8 {
		s.writeErr(w, errInvalid("api_key 格式不正确"))
		return
	}
	if err := s.saveSecrets(config.Secrets{DeepSeekAPIKey: key}); err != nil {
		s.logf("保存密钥失败: %v", err)
		s.writeErr(w, errInvalid("保存密钥失败，请检查数据目录权限"))
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "settings.api_key.set", "settings", "api_key", "ok", jsonDetail(map[string]any{"has_api_key": true}))
	writeOK(w, map[string]any{"has_api_key": true})
}

func (s *Server) handleClearAPIKey(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	if err := s.saveSecrets(config.Secrets{}); err != nil {
		s.logf("清除密钥失败: %v", err)
		s.writeErr(w, errInvalid("清除密钥失败"))
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "settings.api_key.clear", "settings", "api_key", "ok", jsonDetail(map[string]any{"has_api_key": false}))
	writeOK(w, map[string]any{"has_api_key": false})
}

var _ = strconv.Itoa

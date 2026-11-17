package platform

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/memory"
	"github.com/lignting/lignting-agents/internal/search"
	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/web"
)

// MessageSink 是平台 -> core 的用户消息投递口。
// 平台在「消息 + 事件 + 索引 + 审计」事务提交后调用；未注入时 no-op。
// 实现方必须快速返回（只做入队），不得阻塞 HTTP 请求。
type MessageSink interface {
	OnUserMessage(ctx context.Context, msg domain.Message)
}

// Server 是本地工作台 HTTP 服务：路由、鉴权、SSE、静态资源。
type Server struct {
	cfg    config.Config
	store  *store.Store
	mux    *http.ServeMux
	start  time.Time
	static fs.FS
	broker *eventBroker
	logger *log.Logger

	sinkMu sync.RWMutex
	sink   MessageSink

	secretMu sync.Mutex
	secret   []byte

	secretsMu sync.RWMutex
	secrets   config.Secrets

	indexMu    sync.RWMutex
	indexState string
	reindexMu  sync.Mutex

	runnerMu     sync.Mutex
	runnerCancel context.CancelFunc
	runnerDone   chan struct{}

	orgMu sync.RWMutex
	org   OrgService

	workMu sync.RWMutex
	work   WorkService

	memMu sync.RWMutex
	mem   *memory.Service

	debateMu sync.RWMutex
	debate   DebateService
}

// New 构造 HTTP 服务并完成 store 侧的索引/事件钩子接线。
// cmd 负责 Migrate 之后调用；core 通过 SetMessageSink 注入消息投递。
func New(cfg config.Config, st *store.Store) *Server {
	s := &Server{
		cfg:        cfg,
		store:      st,
		mux:        http.NewServeMux(),
		start:      time.Now(),
		broker:     newEventBroker(),
		logger:     log.New(os.Stderr, "[platform] ", log.LstdFlags),
		indexState: "ready",
	}
	sub, err := fs.Sub(web.FS, ".")
	if err != nil {
		panic(err)
	}
	s.static = sub
	if secrets, err := config.LoadSecrets(cfg.DataDir); err != nil {
		s.logf("读取本地密钥失败: %v", err)
	} else {
		s.secrets = secrets
	}
	// 检索索引在消息写事务内同步更新；事件提交后立即唤醒 SSE。
	st.SetIndexer(search.NewIndexer())
	st.SetEventHook(s.broker.Notify)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if n, err := st.MarkInterruptedTurns(ctx); err != nil {
		s.logf("标记中断 turn 失败: %v", err)
	} else if n > 0 {
		s.logf("已把 %d 个未完成 turn 标记为 interrupted", n)
	}
	s.routes()
	s.startRunner()
	return s
}

// SetMessageSink 注入 core 的用户消息处理器（R2 接线）。
func (s *Server) SetMessageSink(sink MessageSink) {
	s.sinkMu.Lock()
	defer s.sinkMu.Unlock()
	s.sink = sink
}

func (s *Server) routes() {
	m := s.mux
	// 路径注册后由 mount 分发方法；未知方法返回 JSON 405（不是 text/plain）。
	mount := func(pattern string, handlers map[string]http.HandlerFunc) {
		m.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			h, ok := handlers[r.Method]
			if !ok {
				allow := make([]string, 0, len(handlers))
				for method := range handlers {
					allow = append(allow, method)
				}
				sort.Strings(allow)
				w.Header().Set("Allow", strings.Join(allow, ", "))
				s.writeErr(w, domain.NewError("method_not_allowed", "不支持的 HTTP 方法", http.StatusMethodNotAllowed))
				return
			}
			h(w, r)
		})
	}

	mount("/api/health", map[string]http.HandlerFunc{http.MethodGet: s.handleHealth})
	mount("/api/bootstrap", map[string]http.HandlerFunc{http.MethodGet: s.handleBootstrap})
	mount("/api/setup", map[string]http.HandlerFunc{http.MethodPost: s.handleSetup})
	mount("/api/login", map[string]http.HandlerFunc{http.MethodPost: s.handleLogin})
	mount("/api/logout", map[string]http.HandlerFunc{http.MethodPost: s.handleLogout})

	mount("/api/settings", map[string]http.HandlerFunc{
		http.MethodGet: s.authed(s.handleGetSettings),
		http.MethodPut: s.authed(s.handlePutSettings),
	})
	mount("/api/settings/api-key", map[string]http.HandlerFunc{
		http.MethodPut:    s.authed(s.handleSetAPIKey),
		http.MethodPost:   s.authed(s.handleSetAPIKey),
		http.MethodDelete: s.authed(s.handleClearAPIKey),
	})

	mount("/api/conversations", map[string]http.HandlerFunc{
		http.MethodGet:  s.authed(s.handleListConversations),
		http.MethodPost: s.authed(s.handleCreateConversation),
	})
	mount("/api/conversations/{id}", map[string]http.HandlerFunc{
		http.MethodGet:   s.authed(s.handleGetConversation),
		http.MethodPatch: s.authed(s.handlePatchConversation),
	})
	mount("/api/conversations/{id}/messages", map[string]http.HandlerFunc{
		http.MethodGet:  s.authed(s.handleListMessages),
		http.MethodPost: s.authed(s.handlePostMessage),
	})
	mount("/api/conversations/{id}/read", map[string]http.HandlerFunc{
		http.MethodPost: s.authed(s.handleMarkRead),
	})
	mount("/api/conversations/{id}/join", map[string]http.HandlerFunc{
		http.MethodPost: s.authed(s.handleJoinConversation),
	})
	mount("/api/conversations/{id}/leave", map[string]http.HandlerFunc{
		http.MethodPost: s.authed(s.handleLeaveConversation),
	})
	mount("/api/messages/{id}", map[string]http.HandlerFunc{
		http.MethodPatch:  s.authed(s.handleEditMessage),
		http.MethodDelete: s.authed(s.handleDeleteMessage),
	})
	mount("/api/search", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleSearch)})
	mount("/api/admin/reindex", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleReindex)})
	mount("/api/events", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleEvents)})

	mount("/api/org", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleOrg)})
	mount("/api/departments", map[string]http.HandlerFunc{
		http.MethodGet:  s.authed(s.handleListDepartments),
		http.MethodPost: s.authed(s.handleCreateDepartment),
	})
	mount("/api/departments/{id}", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleGetDepartment)})
	mount("/api/agents", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleListAgents)})
	mount("/api/agents/{id}", map[string]http.HandlerFunc{
		http.MethodGet:   s.authed(s.handleGetAgent),
		http.MethodPatch: s.authed(s.handlePatchAgent),
	})

	// R3/R8：通知收件箱、调度、本地密码。
	mount("/api/notifications", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleListNotifications)})
	mount("/api/notifications/{id}/status", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleNotificationStatus)})
	mount("/api/schedules", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleListSchedules)})
	mount("/api/schedules/{id}/cancel", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleCancelSchedule)})
	mount("/api/settings/password", map[string]http.HandlerFunc{
		http.MethodPut:  s.authed(s.handleSetPassword),
		http.MethodPost: s.authed(s.handleSetPassword),
	})

	// R4 只读投影（写路径等 core R4 service）。
	mount("/api/tasks", map[string]http.HandlerFunc{
		http.MethodGet:  s.authed(s.handleListTasks),
		http.MethodPost: s.authed(s.handleCreateTask),
	})
	mount("/api/tasks/{id}", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleGetTask)})
	mount("/api/artifacts", map[string]http.HandlerFunc{
		http.MethodGet:  s.authed(s.handleListArtifacts),
		http.MethodPost: s.authed(s.handleCreateArtifact),
	})
	mount("/api/artifacts/{id}/versions", map[string]http.HandlerFunc{
		http.MethodGet:  s.authed(s.handleListArtifactVersions),
		http.MethodPost: s.authed(s.handleAddArtifactVersion),
	})
	mount("/api/artifacts/{id}/versions/{v}", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleGetArtifactVersion)})
	mount("/api/reviews", map[string]http.HandlerFunc{
		http.MethodGet:  s.authed(s.handleListReviews),
		http.MethodPost: s.authed(s.handleCreateReview),
	})
	mount("/api/decisions", map[string]http.HandlerFunc{
		http.MethodGet:  s.authed(s.handleListDecisions),
		http.MethodPost: s.authed(s.handleCreateDecision),
	})
	mount("/api/decisions/{id}", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleGetDecision)})
	mount("/api/decisions/{id}/detail", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleDecisionDetail)})
	mount("/api/decisions/{id}/dissent", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleDecisionDissent)})
	mount("/api/conversations/{id}/discussions", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handlePostDiscussion)})
	mount("/api/conversations/{id}/stances", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleSubmitStance)})
	mount("/api/debates/{id}/escalate", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleEscalateDebate)})
	mount("/api/debates/{id}/close", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleCloseDebate)})
	mount("/api/debates/{id}/reopen", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleReopenDebate)})

	// R4/R5 写工具只读投影。
	mount("/api/writes/timeline", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleWriteTimeline)})
	mount("/api/writes", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleListWriteEvents)})
	mount("/api/writes/{id}", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleGetWriteEvent)})

	// R8 成本/用量只读投影。
	mount("/api/usage", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleUsage)})
	mount("/api/settings/budgets", map[string]http.HandlerFunc{
		http.MethodGet: s.authed(s.handleGetBudgets),
		http.MethodPut: s.authed(s.handlePutBudgets),
	})

	// R8 运维：备份/导出/导入/恢复/紧急停止/审计。
	mount("/api/admin/backup", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleBackup)})
	mount("/api/admin/backups", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleListBackups)})
	mount("/api/admin/restore", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleRestore)})
	mount("/api/admin/export", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleExport)})
	mount("/api/admin/import", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleImport)})
	mount("/api/admin/emergency-stop", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleEmergencyStop)})
	mount("/api/audit", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleAudit)})
	mount("/api/audit/export", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleAuditExport)})
	mount("/api/approvals", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleListApprovals)})
	mount("/api/tasks/{id}/transition", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleTransitionTask)})
	mount("/api/tasks/{id}/criteria/{cid}/evidence", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleCriterionEvidence)})
	mount("/api/tasks/{id}/criteria/{cid}/verdict", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleCriterionVerdict)})
	mount("/api/tasks/{id}/g3", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleSubmitG3)})
	mount("/api/reviews/{id}/verdicts", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleSubmitVerdict)})
	mount("/api/reviews/{id}/rework", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleRework)})
	mount("/api/decisions/{id}/supersede", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleSupersedeDecision)})
	mount("/api/approvals/{id}/decide", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleDecideApproval)})

	// R7 记忆检查器 / 手册 / 上下文回放。
	mount("/api/memories", map[string]http.HandlerFunc{
		http.MethodGet:  s.authed(s.handleListMemories),
		http.MethodPost: s.authed(s.handleCreateMemory),
	})
	mount("/api/memories/{id}", map[string]http.HandlerFunc{
		http.MethodPatch:  s.authed(s.handlePatchMemory),
		http.MethodDelete: s.authed(s.handleDeleteMemory),
	})
	mount("/api/memories/{id}/approve", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleApproveMemory)})
	mount("/api/memories/{id}/reject", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleRejectMemory)})
	mount("/api/manuals", map[string]http.HandlerFunc{
		http.MethodGet:  s.authed(s.handleListManuals),
		http.MethodPost: s.authed(s.handleProposeManual),
	})
	mount("/api/manuals/{id}/rollback", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleRollbackManual)})
	mount("/api/context-snapshots", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleListContextSnapshots)})
	mount("/api/turns/{id}", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleGetTurn)})

	// R4 attachments（v2 表）。
	mount("/api/conversations/{id}/attachments", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleCreateAttachment)})
	mount("/api/attachments/{id}", map[string]http.HandlerFunc{
		http.MethodGet:    s.handleDownloadAttachment,
		http.MethodDelete: s.authed(s.handleDeleteAttachment),
	})

	// R3 §7：意图 / 蓝图+G1 / provisioning / 部门与 Agent 生命周期。
	mount("/api/intents", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleSaveIntent)})
	mount("/api/intents/latest", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleLatestIntent)})
	mount("/api/blueprints", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleProposeBlueprint)})
	mount("/api/blueprints/{id}", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleGetBlueprint)})
	mount("/api/blueprints/{id}/approve", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleApproveBlueprint)})
	mount("/api/blueprints/{id}/reject", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleRejectBlueprint)})
	mount("/api/provisioning", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleProvision)})
	mount("/api/provisioning/{id}", map[string]http.HandlerFunc{http.MethodGet: s.authed(s.handleProvisionStatus)})
	mount("/api/departments/{id}/pause", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleDepartmentLifecycle("pause"))})
	mount("/api/departments/{id}/resume", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleDepartmentLifecycle("resume"))})
	mount("/api/departments/{id}/archive", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleDepartmentLifecycle("archive"))})
	mount("/api/agents/{id}/token", map[string]http.HandlerFunc{
		http.MethodPost:   s.authed(s.handleIssueAgentToken),
		http.MethodDelete: s.authed(s.handleRevokeAgentToken),
	})
	mount("/api/agents/{id}/token/rotate", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleRotateAgentToken)})
	mount("/api/agents/{id}/pause", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleAgentLifecycle("pause"))})
	mount("/api/agents/{id}/resume", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleAgentLifecycle("resume"))})
	mount("/api/agents/{id}/stop", map[string]http.HandlerFunc{http.MethodPost: s.authed(s.handleAgentLifecycle("stop"))})

	// /api 兜底：未知路径返回 JSON 404（不是 text/plain）。
	m.HandleFunc("/api/", s.handleAPINotFound)

	m.HandleFunc("/assets/", s.handleAssets)
	m.HandleFunc("/", s.handleIndex)
}

func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	s.writeErr(w, errNotFound("接口不存在: "+r.URL.Path))
}

// opCtx 返回一次内部短操作（session key 读写等）的超时上下文。
func (s *Server) opCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// logf 记录日志前用本地密钥做脱敏（API Key 不进日志）。
func (s *Server) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.secretsMu.RLock()
	secrets := s.secrets
	s.secretsMu.RUnlock()
	s.logger.Printf("%s", secrets.Redact(msg))
}

func (s *Server) hasAPIKey() bool {
	s.secretsMu.RLock()
	defer s.secretsMu.RUnlock()
	return strings.TrimSpace(s.secrets.DeepSeekAPIKey) != ""
}

func (s *Server) saveSecrets(secrets config.Secrets) error {
	if err := config.SaveSecrets(s.cfg.DataDir, secrets); err != nil {
		return err
	}
	s.secretsMu.Lock()
	s.secrets = secrets
	s.secretsMu.Unlock()
	return nil
}

func (s *Server) indexStateValue() string {
	s.indexMu.RLock()
	defer s.indexMu.RUnlock()
	return s.indexState
}

func (s *Server) setIndexState(state string) {
	s.indexMu.Lock()
	s.indexState = state
	s.indexMu.Unlock()
}

// onUserMessage 在事务提交后把用户消息投递给 core；panic 不影响已提交数据。
func (s *Server) onUserMessage(msg domain.Message) {
	s.sinkMu.RLock()
	sink := s.sink
	s.sinkMu.RUnlock()
	if sink == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer func() {
		if rec := recover(); rec != nil {
			s.logf("MessageSink panic: %v", rec)
		}
	}()
	sink.OnUserMessage(ctx, msg)
}

// ---- 静态资源 ----

func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	if !assetAllowed(name) {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(s.static, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".js"), strings.HasSuffix(name, ".mjs"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(name, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(name, ".json"):
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	_, _ = w.Write(data)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(s.static, "index.html")
	if err != nil {
		http.Error(w, "index missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

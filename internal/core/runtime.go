package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lignting/lignting-agents/internal/deepseek"
	"github.com/lignting/lignting-agents/internal/domain"
)

// ErrInterrupted 表示 turn 被停止（用户点停止或进程/更新中断）。
var ErrInterrupted = errors.New("core: turn interrupted")

// Options 是 Runtime 的依赖与策略。除 Store/Provider 外都有默认值。
type Options struct {
	Store    Store
	Provider deepseek.Provider
	Tools    *ToolRegistry
	Policy   *Policy
	Memory   MemoryInjector
	Actions  *ActionExecutor

	Assembler     *Assembler
	Model         string
	TurnTimeout   time.Duration
	MaxToolRounds int

	// ServerWebTools 为 true 时额外声明 DeepSeek 服务端工具
	// deepseek_web_search / deepseek_read_url（不计入 5 个客户端工具）。
	ServerWebTools bool

	OwnerName string
	Now       func() time.Time

	// R8：预算 guard（nil 表示不检查）与紧急停止开关读取（platform settings）。
	Budget   *BudgetGuard
	Settings SettingReader

	// AC-14：单轮 @ 触发 Agent 数上限（<=0 取默认 2）。
	MentionBatchLimit int

	// 成本估算（每 1K token，人民币）；默认 0 表示只记 token 不估算金额。
	PromptPricePer1K     float64
	CompletionPricePer1K float64
}

// Runtime 管理所有 Agent 的 mailbox/turn，并负责回复落库与事件投影。
type Runtime struct {
	opts Options

	mu      sync.Mutex
	runners map[string]*runner
	closed  bool

	mentionMu     sync.Mutex
	mentionQueues map[string]*mentionQueue
}

// NewRuntime 构造运行时并补齐默认值。
func NewRuntime(opts Options) (*Runtime, error) {
	if opts.Store == nil {
		return nil, errors.New("core: Store 不能为空")
	}
	if opts.Provider == nil {
		return nil, errors.New("core: Provider 不能为空")
	}
	if opts.Tools == nil {
		opts.Tools = NewToolRegistry(nil)
	}
	if opts.Policy == nil {
		opts.Policy = NewPolicy(opts.Store)
	}
	if opts.Actions == nil {
		opts.Actions = NewActionExecutor(opts.Store)
	}
	if opts.Assembler == nil {
		opts.Assembler = NewAssembler(0, 0)
	}
	if opts.Model == "" {
		opts.Model = deepseek.DefaultModel
	}
	if opts.TurnTimeout <= 0 {
		opts.TurnTimeout = 180 * time.Second
	}
	if opts.MaxToolRounds <= 0 {
		opts.MaxToolRounds = 4
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Budget != nil && opts.Settings != nil {
		opts.Budget.AttachSettings(opts.Settings)
	}
	if opts.MentionBatchLimit <= 0 {
		opts.MentionBatchLimit = DefaultMentionBatchLimit
	}
	return &Runtime{opts: opts, runners: map[string]*runner{}, mentionQueues: map[string]*mentionQueue{}}, nil
}

// Options 返回运行时的只读配置副本（测试/诊断用）。
func (rt *Runtime) Options() Options { return rt.opts }

func (rt *Runtime) now() time.Time { return rt.opts.Now().UTC() }

// Close 停止所有 Agent 的 worker（已接受的 turn 会继续跑完）。
func (rt *Runtime) Close() {
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return
	}
	rt.closed = true
	runners := make([]*runner, 0, len(rt.runners))
	for _, r := range rt.runners {
		runners = append(runners, r)
	}
	rt.mu.Unlock()
	for _, r := range runners {
		r.close()
	}
}

// EnqueueUserMessage 是 platform MessageSink 的入口：先落库的消息进入 core 后，
// 由 core 决定该会话里哪些 Agent 被触发（platform 不决定路由）。
func (rt *Runtime) EnqueueUserMessage(ctx context.Context, msg domain.Message) error {
	rt.mu.Lock()
	closed := rt.closed
	rt.mu.Unlock()
	if closed {
		return errors.New("core: runtime 已关闭")
	}
	if strings.TrimSpace(msg.ConversationID) == "" {
		return errors.New("core: 消息缺少 conversation_id")
	}
	if rt.emergencyStop(ctx) {
		return domain.NewError(domain.CodeUnavailable, "系统处于紧急停止状态，已拒绝新 turn；恢复后可重试。", 503)
	}
	conv, err := rt.opts.Store.GetConversation(ctx, msg.ConversationID)
	if err != nil {
		return fmt.Errorf("core: 读取会话失败: %w", err)
	}
	members, err := rt.opts.Store.ListConversationMembers(ctx, msg.ConversationID)
	if err != nil {
		return fmt.Errorf("core: 读取会话成员失败: %w", err)
	}
	if mentions := parseMentions(msg.Meta); len(mentions) > 0 {
		agents, merr := rt.resolveMentionAgents(ctx, conv, members, mentions)
		if merr != nil {
			return merr
		}
		if len(agents) == 0 {
			return nil
		}
		rt.enqueueMention(conv.ID, mentionJob{message: msg, agents: agents})
		return nil
	}
	agents, err := rt.resolveTriggeredAgents(ctx, conv, members)
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		return nil
	}
	var failures []string
	for _, agent := range agents {
		if err := rt.runnerFor(agent).submit(inbound{message: msg}); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) == len(agents) {
		return fmt.Errorf("core: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (rt *Runtime) resolveTriggeredAgents(ctx context.Context, conv domain.Conversation, members []domain.ConversationMember) ([]domain.Agent, error) {
	var candidates []domain.Agent
	seen := map[string]bool{}
	for _, m := range members {
		if m.PrincipalType != "agent" || m.PrincipalID == "" || seen[m.PrincipalID] {
			continue
		}
		seen[m.PrincipalID] = true
		agent, err := rt.opts.Store.GetAgent(ctx, m.PrincipalID)
		if err != nil {
			continue
		}
		candidates = append(candidates, agent)
	}
	if len(candidates) == 0 && conv.Type == domain.ConvDM {
		assistants, err := rt.opts.Store.ListAgentsByKind(ctx, domain.AgentKindAssistant)
		if err == nil && len(assistants) > 0 {
			candidates = append(candidates, assistants[0])
		}
	}
	var triggered []domain.Agent
	var rejected []string
	for _, agent := range candidates {
		if agent.Status == domain.AgentPaused || agent.Status == domain.AgentOffline {
			rejected = append(rejected, agent.Name+" 已暂停/离线")
			continue
		}
		if agent.DepartmentID != "" {
			if dept, err := rt.opts.Store.GetDepartment(ctx, agent.DepartmentID); err == nil && (dept.Status == domain.DeptPaused || dept.Status == domain.DeptArchived) {
				rejected = append(rejected, agent.Name+" 所在部门已暂停/归档")
				continue
			}
		}
		if err := rt.opts.Policy.AuthorizeParticipation(ctx, agent, conv, members); err != nil {
			rejected = append(rejected, err.Error())
			continue
		}
		triggered = append(triggered, agent)
	}
	if len(triggered) == 0 && len(candidates) > 0 {
		return nil, fmt.Errorf("core: 没有 Agent 可响应该会话：%s", strings.Join(rejected, "; "))
	}
	return triggered, nil
}

func (rt *Runtime) runnerFor(agent domain.Agent) *runner {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if r, ok := rt.runners[agent.ID]; ok {
		r.agent = agent
		return r
	}
	r := newRunner(rt, agent)
	rt.runners[agent.ID] = r
	return r
}

func (rt *Runtime) getRunner(agentID string) *runner {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.runners[agentID]
}

// WaitIdle 等待某 Agent 的 mailbox 清空（测试、setup 与停止/重启后用）。
func (rt *Runtime) WaitIdle(ctx context.Context, agentID string) error {
	r := rt.getRunner(agentID)
	if r == nil {
		return nil
	}
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		if r.idle() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Interrupt 停止某 Agent 当前 turn；已排队的消息保留。
func (rt *Runtime) Interrupt(agentID string) bool {
	r := rt.getRunner(agentID)
	if r == nil {
		return false
	}
	return r.interrupt()
}

// RetryLast 重新投递该 Agent 最近一次输入（模型失败后的「重试」）。
func (rt *Runtime) RetryLast(agentID string) error {
	r := rt.getRunner(agentID)
	if r == nil {
		return fmt.Errorf("core: agent %s 尚未运行", agentID)
	}
	return r.retryLast()
}

// TurnResult 是一个 turn 的完整结果（测试/诊断/后续 UI 用）。
type TurnResult struct {
	TurnID   string
	Message  domain.Message
	Text     string
	Actions  []ActionOutcome
	Rejected []ActionRejection
	Usage    deepseek.Usage
	Err      error
}

// ActionOutcome 是成功执行的动作。
type ActionOutcome struct {
	Action  string `json:"action"`
	Message string `json:"message"`
}

// ActionRejection 是被拒绝的动作（整块拒绝，不半执行）。
type ActionRejection struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// SendAssistantGreeting 直接写入助理首启问候（不调用模型；mock/无 Key 同样可用）。
func (rt *Runtime) SendAssistantGreeting(ctx context.Context, agentID, conversationID string) (domain.Message, error) {
	agent, err := rt.opts.Store.GetAgent(ctx, agentID)
	if err != nil {
		return domain.Message{}, err
	}
	if agent.Kind != domain.AgentKindAssistant {
		return domain.Message{}, NewForbidden("只有助理可以发送首启问候", "请由助理 Agent 发送。")
	}
	conv, err := rt.opts.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return domain.Message{}, err
	}
	members, err := rt.opts.Store.ListConversationMembers(ctx, conversationID)
	if err != nil {
		return domain.Message{}, err
	}
	if err := rt.opts.Policy.AuthorizeParticipation(ctx, agent, conv, members); err != nil {
		return domain.Message{}, err
	}
	msg := domain.Message{
		ID:             domain.NewID("msg"),
		ConversationID: conversationID,
		SenderType:     domain.SenderAgent,
		SenderID:       agent.ID,
		SenderName:     agent.Name,
		Type:           domain.MsgText,
		Content:        AssistantGreeting(agent.Name, rt.opts.OwnerName),
		Meta:           `{"kind":"assistant.greeting"}`,
	}
	events := []domain.Event{rt.messageEvent("message.completed", msg, agent.ID, map[string]any{
		"message_id": msg.ID, "conversation_id": conversationID, "kind": "assistant.greeting",
	})}
	audit := domain.AuditEntry{
		ActorType: "agent", ActorID: agent.ID, Action: "assistant.greeting",
		ObjectType: "message", ObjectID: msg.ID, Result: "ok",
		Detail: `{"conversation_id":"` + conversationID + `"}`,
	}
	saved, err := rt.persistReply(ctx, msg, events, audit)
	if err != nil {
		return domain.Message{}, fmt.Errorf("core: 写入助理问候失败: %w", err)
	}
	return saved, nil
}

// executeTurn 是一个 turn 的完整执行：装配 → 模型（含工具循环）→ 动作 → 落库。
func (rt *Runtime) executeTurn(ctx context.Context, agent domain.Agent, userMsg domain.Message) TurnResult {
	result := TurnResult{}
	conv, err := rt.opts.Store.GetConversation(ctx, userMsg.ConversationID)
	if err != nil {
		result.Err = fmt.Errorf("core: 读取会话失败: %w", err)
		return result
	}
	members, _ := rt.opts.Store.ListConversationMembers(ctx, userMsg.ConversationID)
	if err := rt.opts.Policy.AuthorizeParticipation(ctx, agent, conv, members); err != nil {
		result.Err = err
		return result
	}
	department := rt.loadDepartment(ctx, firstNonEmpty(conv.DepartmentID, agent.DepartmentID))
	task := rt.loadTask(ctx, agent.CurrentTaskID)
	recent := rt.loadRecent(ctx, conv.ID, userMsg)

	startedAt := rt.now()
	turn := domain.Turn{
		ID:             domain.NewID("turn"),
		AgentID:        agent.ID,
		ConversationID: conv.ID,
		Status:         "running",
		StartedAt:      startedAt.Format(time.RFC3339Nano),
	}
	result.TurnID = turn.ID
	if err := rt.opts.Store.CreateTurn(ctx, turn); err != nil {
		result.Err = fmt.Errorf("core: 创建 turn 失败: %w", err)
		return result
	}

	memories, memorySnapshot := rt.loadMemories(ctx, agent, conv, task, turn.ID, recent)

	assembled := rt.opts.Assembler.Assemble(ContextInput{
		Agent:          agent,
		Conversation:   conv,
		Department:     department,
		Task:           task,
		OwnerName:      rt.opts.OwnerName,
		CurrentMessage: userMsg,
		Recent:         recent,
		Memories:       memories,
		MemorySnapshot: memorySnapshot,
		Now:            rt.now(),
	})
	rt.saveContextSnapshot(ctx, turn, agent, assembled)

	messageID := domain.NewID("msg")
	emitter := newDeltaEmitter(rt.opts.Store, messageID, conv.ID, agent.ID)

	tools := rt.opts.Tools.ToolDefinitions()
	if rt.opts.ServerWebTools {
		tools = append(tools, deepseek.WebSearchTool(), deepseek.ReadURLTool())
	}
	messages := assembled.Messages
	var (
		text       strings.Builder
		usage      deepseek.Usage
		toolRounds int
	)
	model := firstNonEmpty(agent.Model, rt.opts.Model)

	for round := 0; round <= rt.opts.MaxToolRounds; round++ {
		if rt.emergencyStop(ctx) {
			res := rt.failTurn(ctx, agent, conv, turn, messageID, ErrEmergencyStop, toolRounds)
			rt.updateStatus(ctx, agent, domain.AgentIdle, "emergency_stop")
			return res
		}
		if check, blocked := rt.checkBudget(ctx, agent, conv); blocked {
			res := rt.failTurn(ctx, agent, conv, turn, messageID, domain.NewError(domain.CodeBudgetBlocked, check.Message, 409), toolRounds)
			rt.updateStatus(ctx, agent, domain.AgentBudgetBlocked, check.Message)
			return res
		}
		req := deepseek.Request{Model: model, Messages: messages, Tools: tools}
		resp, callErr := rt.opts.Provider.Stream(ctx, req, func(delta deepseek.Delta) error {
			if delta.Content == "" {
				return nil
			}
			text.WriteString(delta.Content)
			return emitter.add(delta.Content)
		})
		if callErr != nil {
			return rt.failTurn(ctx, agent, conv, turn, messageID, callErr, toolRounds)
		}
		addUsage(&usage, resp.Usage)
		msg := resp.FirstMessage()
		if len(msg.ToolCalls) == 0 {
			break
		}
		messages = append(messages, msg)
		for _, call := range msg.ToolCalls {
			tc := ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)}
			res, execErr := rt.opts.Tools.Execute(ctx, agent, tc)
			if execErr != nil {
				res.IsError = true
				if strings.TrimSpace(res.Content) == "" {
					res.Content = execErr.Error()
				}
			}
			messages = append(messages, deepseek.ToolMessage(call.ID, res.Content))
			toolRounds++
		}
	}
	_ = emitter.flush()
	result.Usage = usage

	clean, actions, parseErrs := domain.ParseActions(text.String())
	for _, perr := range parseErrs {
		result.Rejected = append(result.Rejected, ActionRejection{Action: "动作块", Reason: perr.Error()})
	}
	for _, action := range actions {
		if err := action.Validate(); err != nil {
			result.Rejected = append(result.Rejected, ActionRejection{Action: action.Action, Reason: err.Error()})
			continue
		}
		ac := ActionContext{Agent: agent, Conversation: conv, Task: task}
		if err := rt.opts.Policy.AuthorizeAction(ctx, ac, action); err != nil {
			result.Rejected = append(result.Rejected, ActionRejection{Action: action.Action, Reason: err.Error()})
			continue
		}
		msgOut, err := rt.opts.Actions.Execute(ctx, agent, action)
		if err != nil {
			result.Rejected = append(result.Rejected, ActionRejection{Action: action.Action, Reason: err.Error()})
			continue
		}
		result.Actions = append(result.Actions, ActionOutcome{Action: action.Action, Message: msgOut})
	}

	visible := strings.TrimSpace(clean)
	if len(result.Actions) > 0 {
		var cards []string
		for _, outcome := range result.Actions {
			cards = append(cards, "[动作:"+outcome.Action+"] "+outcome.Message)
		}
		visible = strings.TrimSpace(visible + "\n\n" + strings.Join(cards, "\n"))
	}
	if visible == "" {
		visible = "（本轮没有产生文本回复）"
	}
	reply := domain.Message{
		ID:             messageID,
		ConversationID: conv.ID,
		SenderType:     domain.SenderAgent,
		SenderID:       agent.ID,
		SenderName:     agent.Name,
		Type:           domain.MsgText,
		Content:        visible,
		Meta:           rt.replyMeta(result, toolRounds),
	}
	events := []domain.Event{rt.messageEvent("message.completed", reply, agent.ID, map[string]any{
		"message_id": messageID, "conversation_id": conv.ID, "actions": actionNames(result.Actions),
	})}
	audit := domain.AuditEntry{
		ActorType: "agent", ActorID: agent.ID, Action: "agent.reply",
		ObjectType: "message", ObjectID: messageID, Result: "ok",
		Detail: `{"conversation_id":"` + conv.ID + `","tool_rounds":"` + fmt.Sprint(toolRounds) + `"}`,
	}
	saved, err := rt.persistReply(ctx, reply, events, audit)
	if err != nil {
		return rt.failTurn(ctx, agent, conv, turn, messageID, fmt.Errorf("core: 写入回复失败: %w", err), toolRounds)
	}
	result.Message = saved
	result.Text = visible
	rt.writeRejections(ctx, agent, conv, result.Rejected)
	rt.recordUsage(ctx, agent, conv, model, usage, result.TurnID)
	interrupted := ctx.Err() != nil
	if err := rt.opts.Store.FinishTurnWithEvents(ctx, turn.ID, "completed", "", []domain.Event{
		rt.event("turn.completed", conv.ID, "turn", turn.ID, "agent", agent.ID, map[string]any{
			"turn_id": turn.ID, "status": "completed", "interrupted": interrupted,
		}),
	}, domain.AuditEntry{}); err != nil {
		result.Err = fmt.Errorf("core: 结束 turn 失败: %w", err)
	}
	rt.updateStatus(ctx, agent, domain.AgentIdle, "")
	return result
}

func (rt *Runtime) failTurn(ctx context.Context, agent domain.Agent, conv domain.Conversation, turn domain.Turn, messageID string, cause error, toolRounds int) TurnResult {
	result := TurnResult{TurnID: turn.ID, Err: cause}
	leaveCtx := context.WithoutCancel(ctx)
	if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		_ = rt.opts.Store.FinishTurnWithEvents(leaveCtx, turn.ID, "interrupted", "stopped", []domain.Event{
			rt.event("turn.interrupted", conv.ID, "turn", turn.ID, "agent", agent.ID, map[string]any{"turn_id": turn.ID}),
		}, domain.AuditEntry{})
		rt.updateStatus(leaveCtx, agent, domain.AgentIdle, "interrupted")
		result.Err = ErrInterrupted
		return result
	}
	errText := cause.Error()
	_ = rt.opts.Store.FinishTurnWithEvents(leaveCtx, turn.ID, "failed", errText, []domain.Event{
		rt.event("turn.failed", conv.ID, "turn", turn.ID, "agent", agent.ID, map[string]any{
			"turn_id": turn.ID, "error": truncateRunes(errText, 500),
		}),
	}, domain.AuditEntry{
		ActorType: "agent", ActorID: agent.ID, Action: "agent.turn.failed",
		ObjectType: "turn", ObjectID: turn.ID, Result: "failed", Detail: fmt.Sprintf(`{"error":%q}`, truncateRunes(errText, 500)),
	})
	// 契约：模型失败必须给会话发一条可读错误消息，不能静默。
	errMsg := domain.Message{
		ID:             domain.NewID("msg"),
		ConversationID: conv.ID,
		SenderType:     domain.SenderAgent,
		SenderID:       agent.ID,
		SenderName:     agent.Name,
		Type:           domain.MsgText,
		Content:        DegradedMessage(cause),
		Meta:           `{"kind":"assistant.error","retryable":true}`,
	}
	events := []domain.Event{rt.messageEvent("message.completed", errMsg, agent.ID, map[string]any{
		"message_id": errMsg.ID, "conversation_id": conv.ID, "error": true,
	})}
	audit := domain.AuditEntry{
		ActorType: "agent", ActorID: agent.ID, Action: "agent.reply.error",
		ObjectType: "message", ObjectID: errMsg.ID, Result: "failed",
		Detail: `{"conversation_id":"` + conv.ID + `","cause":"` + truncateRunes(errText, 500) + `"}`,
	}
	if saved, err := rt.persistReply(ctx, errMsg, events, audit); err == nil {
		result.Message = saved
	}
	rt.updateStatus(ctx, agent, domain.AgentFailed, truncateRunes(errText, 200))
	return result
}

func (rt *Runtime) writeRejections(ctx context.Context, agent domain.Agent, conv domain.Conversation, rejected []ActionRejection) {
	if len(rejected) == 0 {
		return
	}
	var lines []string
	for _, r := range rejected {
		lines = append(lines, "- "+r.Action+"："+r.Reason)
	}
	content := "以下动作被拒绝（整块未执行）：\n" + strings.Join(lines, "\n")
	msg := domain.Message{
		ID:             domain.NewID("msg"),
		ConversationID: conv.ID,
		SenderType:     domain.SenderSystem,
		SenderID:       "system",
		SenderName:     "系统",
		Type:           domain.MsgSystem,
		Content:        content,
		Meta:           `{"kind":"action.rejected"}`,
	}
	events := []domain.Event{rt.messageEvent("message.completed", msg, agent.ID, map[string]any{
		"message_id": msg.ID, "conversation_id": conv.ID, "rejected_actions": len(rejected),
	})}
	audit := domain.AuditEntry{
		ActorType: "system", ActorID: "core", Action: "action.rejected",
		ObjectType: "message", ObjectID: msg.ID, Result: "rejected",
		Detail: `{"conversation_id":"` + conv.ID + `"}`,
	}
	_, _ = rt.persistReply(ctx, msg, events, audit)
}

// persistReply 用 store 的 AppendReply 原子写「消息 + 事件 + 审计」（先写库再发事件）。
func (rt *Runtime) persistReply(ctx context.Context, msg domain.Message, events []domain.Event, audit domain.AuditEntry) (domain.Message, error) {
	return rt.opts.Store.AppendReply(ctx, msg, events, audit)
}

func (rt *Runtime) loadRecent(ctx context.Context, conversationID string, current domain.Message) []domain.Message {
	limit := rt.opts.Assembler.RecentLimit
	before := int64(0)
	if current.Seq > 0 {
		before = current.Seq
	}
	recent, err := rt.opts.Store.RecentMessages(ctx, conversationID, limit, before)
	if err != nil {
		return nil
	}
	if before == 0 {
		out := recent[:0]
		for _, m := range recent {
			if current.ID != "" && m.ID == current.ID {
				continue
			}
			out = append(out, m)
		}
		return out
	}
	return recent
}

func (rt *Runtime) loadDepartment(ctx context.Context, departmentID string) *domain.Department {
	if departmentID == "" {
		return nil
	}
	dept, err := rt.opts.Store.GetDepartment(ctx, departmentID)
	if err != nil {
		return nil
	}
	return &dept
}

func (rt *Runtime) loadTask(ctx context.Context, taskID string) *domain.Task {
	if taskID == "" {
		return nil
	}
	task, err := rt.opts.Store.GetTask(ctx, taskID)
	if err != nil {
		return nil
	}
	return &task
}

func (rt *Runtime) saveContextSnapshot(ctx context.Context, turn domain.Turn, agent domain.Agent, assembled Assembled) {
	payload, err := json.Marshal(map[string]any{
		"turn_id":                turn.ID,
		"agent_id":               agent.ID,
		"conversation_id":        turn.ConversationID,
		"report":                 assembled.Report,
		"message_count":          len(assembled.Messages),
		"memory_entries":         assembled.Report.MemoryEntries,
		"memory_ids":             assembled.Report.MemoryIDs,
		"memory_tokens":          assembled.Report.MemoryTokens,
		"memory_degraded_reason": assembled.Report.MemoryDegradedReason,
	})
	if err != nil {
		return
	}
	_ = rt.opts.Store.InsertContextSnapshot(ctx, domain.ContextSnapshot{
		ID:             domain.NewID("snap"),
		TurnID:         turn.ID,
		AgentID:        agent.ID,
		ConversationID: turn.ConversationID,
		Payload:        string(payload),
		CreatedAt:      rt.now().Format(time.RFC3339Nano),
	})
}

// loadMemories 优先使用 memory 的快照接口；失败时返回降级原因并让上下文继续（手册+任务）。
func (rt *Runtime) loadMemories(ctx context.Context, agent domain.Agent, conv domain.Conversation, task *domain.Task, turnID string, recent []domain.Message) ([]domain.Memory, *MemorySnapshot) {
	if rt.opts.Memory == nil {
		return nil, nil
	}
	q := MemoryQuery{
		Agent: agent, ConversationID: conv.ID, TaskID: agent.CurrentTaskID, Limit: 8,
		TurnID: turnID, Model: firstNonEmpty(agent.Model, rt.opts.Model),
		TaskBrief: taskBrief(task), EffectiveInputTokens: int64(rt.opts.Assembler.MaxPromptTokens),
		RecentMessages: len(recent), IncludeManual: true, IncludeArchives: true,
	}
	if snapshotter, ok := rt.opts.Memory.(MemorySnapshotter); ok {
		snap, err := snapshotter.InjectWithSnapshot(ctx, q)
		if err != nil {
			return nil, &MemorySnapshot{Degraded: true, DegradeReason: truncateRunes(err.Error(), 300)}
		}
		if snap == nil {
			return nil, nil
		}
		return memoriesFromSnapshot(snap), snap
	}
	memories, _ := rt.opts.Memory.Inject(ctx, q)
	return memories, nil
}

func taskBrief(task *domain.Task) string {
	if task == nil {
		return ""
	}
	return strings.TrimSpace(task.Title + "：" + task.Description)
}

func memoriesFromSnapshot(snap *MemorySnapshot) []domain.Memory {
	out := make([]domain.Memory, 0, len(snap.Entries))
	for _, e := range snap.Entries {
		out = append(out, domain.Memory{
			ID: e.ID, Content: e.Summary, Summary: e.Summary,
			Tags: "injected;src:" + e.Source, Visibility: e.Visibility,
			Confidence: e.Confidence, Version: e.Version,
		})
	}
	return out
}

func (rt *Runtime) recordUsage(ctx context.Context, agent domain.Agent, conv domain.Conversation, model string, usage deepseek.Usage, turnID string) {
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		return
	}
	cost := float64(usage.PromptTokens)/1000*rt.opts.PromptPricePer1K +
		float64(usage.CompletionTokens)/1000*rt.opts.CompletionPricePer1K
	_ = rt.opts.Store.InsertUsage(ctx, domain.Usage{
		ID:               domain.NewID("usage"),
		AgentID:          agent.ID,
		TaskID:           agent.CurrentTaskID,
		ConversationID:   conv.ID,
		Model:            model,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CachedTokens:     usage.CachedTokens,
		Cost:             cost,
		CreatedAt:        rt.now().Format(time.RFC3339Nano),
	})
}

func (rt *Runtime) updateStatus(ctx context.Context, agent domain.Agent, status, reason string) {
	if err := rt.opts.Store.UpdateAgentStatus(ctx, agent.ID, status); err != nil {
		return
	}
	_, _ = rt.opts.Store.AppendEvent(ctx, rt.event("agent.status", "", "agent", agent.ID, "agent", agent.ID, map[string]any{
		"agent_id": agent.ID, "status": status, "reason": reason,
	}))
}

func (rt *Runtime) replyMeta(result TurnResult, toolRounds int) string {
	payload := map[string]any{
		"actions":     actionNames(result.Actions),
		"tool_rounds": toolRounds,
		"usage": map[string]any{
			"prompt_tokens":     result.Usage.PromptTokens,
			"completion_tokens": result.Usage.CompletionTokens,
			"cached_tokens":     result.Usage.CachedTokens,
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func (rt *Runtime) messageEvent(eventType string, msg domain.Message, actorID string, payload map[string]any) domain.Event {
	return rt.event(eventType, msg.ConversationID, "message", msg.ID, "agent", actorID, payload)
}

func (rt *Runtime) event(eventType, conversationID, objectType, objectID, actorType, actorID string, payload map[string]any) domain.Event {
	b, _ := json.Marshal(payload)
	return domain.Event{
		Type:           eventType,
		ConversationID: conversationID,
		ObjectType:     objectType,
		ObjectID:       objectID,
		ActorType:      actorType,
		ActorID:        actorID,
		Payload:        string(b),
		CreatedAt:      rt.now().Format(time.RFC3339Nano),
	}
}

func actionNames(outcomes []ActionOutcome) []string {
	out := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		out = append(out, o.Action)
	}
	return out
}

func addUsage(total *deepseek.Usage, u deepseek.Usage) {
	total.PromptTokens += u.PromptTokens
	total.CompletionTokens += u.CompletionTokens
	total.TotalTokens += u.TotalTokens
	total.CachedTokens += u.CachedTokens
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// deltaEmitter 把流式内容批量投影成 message.delta 事件（默认攒够 32 个字符发一次）。
type deltaEmitter struct {
	store   EventStore
	msgID   string
	convID  string
	agentID string
	index   int
	buf     strings.Builder
	runes   int
}

func newDeltaEmitter(store EventStore, msgID, convID, agentID string) *deltaEmitter {
	return &deltaEmitter{store: store, msgID: msgID, convID: convID, agentID: agentID}
}

func (e *deltaEmitter) add(text string) error {
	e.buf.WriteString(text)
	e.runes += len([]rune(text))
	if e.runes < 32 {
		return nil
	}
	return e.flush()
}

func (e *deltaEmitter) flush() error {
	if e.buf.Len() == 0 {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{
		"message_id": e.msgID, "conversation_id": e.convID, "delta": e.buf.String(), "index": e.index,
	})
	_, err := e.store.AppendEvent(context.Background(), domain.Event{
		Type:           "message.delta",
		ConversationID: e.convID,
		ObjectType:     "message",
		ObjectID:       e.msgID,
		ActorType:      "agent",
		ActorID:        e.agentID,
		Payload:        string(payload),
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
	})
	e.index++
	e.buf.Reset()
	e.runes = 0
	return err
}

package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// 默认值：core 未提供有效输入预算时的保守假设。
const (
	defaultEffectiveInputTokens = 32000
	// 每条注入的固定开销（标签、来源、分隔符）。
	injectOverheadTokens = 12
)

// EntityRef 是显式指定的检索目标（如当前任务、当前项目）。
type EntityRef struct {
	Type string
	Key  string
}

// InjectionRequest 是上下文装配请求。短期上下文（近期消息）由 core 提供计数，
// 本包只负责按预算裁剪并产出可解释快照。
type InjectionRequest struct {
	TurnID         string
	AgentID        string
	AgentKind      string
	DepartmentID   string
	TaskID         string
	ConversationID string
	Model          string

	TaskBrief            string
	EffectiveInputTokens int64
	RecentMessages       int
	RecentTokens         int64

	ManualScope     string
	IncludeManual   bool
	IncludeArchives bool
	ExtraRefs       []EntityRef
}

// Budgets 记录本次装配的预算口径，便于用户核对“为什么只注入了这些”。
type Budgets struct {
	EffectiveInput int64
	SafetyReserve  int64
	Usable         int64
	MemoryBudget   int64
	Manual         int64
	Task           int64
	Archive        int64
	Recent         int64
}

// ManualInjection 是本次注入的管理手册版本。
type ManualInjection struct {
	Version   int    `json:"version"`
	Content   string `json:"content"`
	Tokens    int64  `json:"tokens"`
	Truncated bool   `json:"truncated"`
}

// TaskInjection 是当前任务说明（最后防线，超预算也不整段丢弃）。
type TaskInjection struct {
	Text      string `json:"text"`
	Tokens    int64  `json:"tokens"`
	Truncated bool   `json:"truncated"`
}

// InjectedMemory 是一条被注入的档案（只给摘要，全文走 memory.read）。
type InjectedMemory struct {
	ID         string `json:"id"`
	EntityType string `json:"entity_type"`
	EntityKey  string `json:"entity_key"`
	Version    int    `json:"version"`
	Summary    string `json:"summary"`
	Source     string `json:"source"`
	Visibility string `json:"visibility"`
	Confidence string `json:"confidence"`
	Pinned     bool   `json:"pinned"`
	Tokens     int64  `json:"tokens"`
	Reason     string `json:"reason"`
}

// DroppedMemory 是本次因权限/预算被裁掉的档案（可解释性）。
type DroppedMemory struct {
	ID         string `json:"id"`
	EntityType string `json:"entity_type"`
	EntityKey  string `json:"entity_key"`
	Reason     string `json:"reason"`
	Tokens     int64  `json:"tokens"`
}

// ContextRequest 是 FR-MEM-08 要求的 context.request 快照载荷。
type ContextRequest struct {
	ID             string           `json:"id"`
	TurnID         string           `json:"turn_id"`
	AgentID        string           `json:"agent_id"`
	AgentKind      string           `json:"agent_kind"`
	ConversationID string           `json:"conversation_id"`
	TaskID         string           `json:"task_id"`
	Model          string           `json:"model"`
	Budgets        Budgets          `json:"budgets"`
	Manual         *ManualInjection `json:"manual,omitempty"`
	Task           *TaskInjection   `json:"task,omitempty"`
	Memories       []InjectedMemory `json:"memories"`
	Dropped        []DroppedMemory  `json:"dropped"`
	RecentMessages int              `json:"recent_messages"`
	RecentTokens   int64            `json:"recent_tokens"`
	Estimated      int64            `json:"estimated_tokens"`
	Degraded       bool             `json:"degraded"`
	DegradeReason  string           `json:"degrade_reason,omitempty"`
	CreatedAt      string           `json:"created_at"`
}

// Payload 序列化为 context_snapshots.payload。
func (c *ContextRequest) Payload() (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// EstimateTokens 估算 token：ASCII 约 4 字符/token，非 ASCII 约 1.5 字符/token。
// 宁可略高估（安全边际由 Budgets 单独留）。
func EstimateTokens(s string) int64 {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	ascii, wide := 0, 0
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			wide++
		}
	}
	t := float64(ascii)/4 + float64(wide)/1.5
	if t < 1 {
		return 1
	}
	return int64(math.Ceil(t))
}

func partitionTokens(budget int64, ratio float64) int64 {
	if budget <= 0 || ratio <= 0 {
		return 0
	}
	return int64(math.Floor(float64(budget) * ratio))
}

// Assemble 按 agent/部门/可见性/任务检索记忆，按分区预算裁剪，并产出可解释的
// context.request 快照。任何仓储错误都降级为「手册 + 当前任务」，不阻塞主流程。
func (s *Service) Assemble(ctx context.Context, req InjectionRequest) (*ContextRequest, error) {
	out := &ContextRequest{
		ID:             domain.NewID("ctx"),
		TurnID:         req.TurnID,
		AgentID:        req.AgentID,
		AgentKind:      req.AgentKind,
		ConversationID: req.ConversationID,
		TaskID:         req.TaskID,
		Model:          req.Model,
		Memories:       []InjectedMemory{},
		Dropped:        []DroppedMemory{},
		CreatedAt:      s.stamp(),
	}
	if !s.cfg.Enabled {
		out.Degraded = true
		out.DegradeReason = "memory_disabled"
		return out, nil
	}
	eff := req.EffectiveInputTokens
	if eff <= 0 {
		eff = defaultEffectiveInputTokens
	}
	margin := s.cfg.SafetyMargin()
	reserve := int64(float64(eff) * margin)
	usable := eff - reserve
	if usable < 0 {
		usable = 0
	}
	memBudget := int64(math.Min(float64(usable)*s.cfg.InjectPercent, float64(s.cfg.InjectAbsCapTokens)))
	if memBudget < 0 {
		memBudget = 0
	}
	out.Budgets = Budgets{
		EffectiveInput: eff,
		SafetyReserve:  reserve,
		Usable:         usable,
		MemoryBudget:   memBudget,
		Manual:         partitionTokens(memBudget, s.cfg.PartitionManual),
		Task:           partitionTokens(memBudget, s.cfg.PartitionTask),
		Archive:        partitionTokens(memBudget, s.cfg.PartitionArchive),
		Recent:         partitionTokens(memBudget, s.cfg.PartitionRecent),
	}

	actor := Access{Type: req.AgentKind, ID: req.AgentID, DepartmentID: req.DepartmentID}
	if actor.Type == "" {
		actor.Type = ActorAssistant
	}

	// 1) 管理手册（system 稳定前缀）。
	if req.IncludeManual {
		scope := req.ManualScope
		if scope == "" {
			switch req.AgentKind {
			case ActorSupervisor:
				scope = domain.MemoryScopeDepartment
			case ActorEmployee:
				scope = domain.MemoryScopeEmployee
			default:
				scope = domain.MemoryScopeAssistant
			}
		}
		key := ManualKey{Scope: scope, DepartmentID: req.DepartmentID}
		// 部门手册是部门共享的（不带 agent_id）；助理/员工手册才按 agent 归属。
		if scope == domain.MemoryScopeAssistant || scope == domain.MemoryScopeEmployee {
			key.AgentID = req.AgentID
		}
		m, ok, err := s.CurrentApprovedManual(ctx, key)
		if err != nil {
			out.Degraded = true
			out.DegradeReason = "manual_read_failed: " + err.Error()
		} else if ok && strings.TrimSpace(m.Content) != "" {
			out.Manual = s.fitManual(m, out.Budgets.Manual)
		}
	}

	// 2) 当前任务（最后防线：即使超预算也保留目标与验收标准的前半部分）。
	if brief := strings.TrimSpace(req.TaskBrief); brief != "" {
		tokens := EstimateTokens(brief)
		text := brief
		truncated := false
		if out.Budgets.Task > 0 && tokens > out.Budgets.Task {
			text, truncated = truncateToTokens(brief, out.Budgets.Task)
			tokens = EstimateTokens(text)
		}
		out.Task = &TaskInjection{Text: text, Tokens: tokens, Truncated: truncated}
	}

	// 3) 档案：结构化优先 + 全文不可用时用摘要；按可见性与预算裁剪。
	if req.IncludeArchives {
		rows, err := s.retrieveForInjection(ctx, req, actor)
		if err != nil {
			out.Degraded = true
			out.DegradeReason = "archive_read_failed: " + err.Error()
			s.log.Warn("memory: assemble degrade", "agent", req.AgentID, "error", err)
		} else {
			out.Memories, out.Dropped = s.trimByBudget(rows, req, actor, out.Budgets.Archive)
		}
	}

	// 4) 近期会话只计预算（消息本身由 core 装配，记忆层不持有短期上下文）。
	if req.RecentMessages > 0 {
		out.RecentMessages = req.RecentMessages
		out.RecentTokens = req.RecentTokens
		if out.RecentTokens == 0 {
			out.RecentTokens = int64(req.RecentMessages) * 48
		}
	}

	out.Estimated = estimateContext(out)
	s.recordReads(ctx, out)
	return out, nil
}

// retrieveForInjection 先按部门 + 任务键做结构化检索，再做可见性过滤。
func (s *Service) retrieveForInjection(ctx context.Context, req InjectionRequest, actor Access) ([]domain.Memory, error) {
	rows, err := s.repo.ListMemories(ctx, Filter{
		DepartmentID: req.DepartmentID,
		Status:       []string{domain.MemoryStatusActive},
		Limit:        200,
	})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(rows))
	uniq := make([]domain.Memory, 0, len(rows))
	add := func(m domain.Memory) {
		if seen[m.ID] {
			return
		}
		seen[m.ID] = true
		uniq = append(uniq, m)
	}
	for _, m := range rows {
		add(m)
	}
	refs := append([]EntityRef{}, req.ExtraRefs...)
	if req.TaskID != "" {
		refs = append(refs, EntityRef{Type: EntityTask, Key: req.TaskID})
	}
	for _, ref := range refs {
		if ref.Type == "" || ref.Key == "" {
			continue
		}
		extra, eerr := s.repo.ListMemories(ctx, Filter{
			EntityType: ref.Type,
			EntityKey:  ref.Key,
			Status:     []string{domain.MemoryStatusActive},
			Limit:      10,
		})
		if eerr != nil {
			continue // 单条检索失败不影响整体注入（降级而非失败）。
		}
		for _, m := range extra {
			add(m)
		}
	}
	_ = actor
	return uniq, nil
}

type scoredMemory struct {
	memory domain.Memory
	score  float64
	reason string
}

// trimByBudget 打分排序后按预算注入，返回注入项与裁剪项（含原因）。
func (s *Service) trimByBudget(rows []domain.Memory, req InjectionRequest, actor Access, budget int64) ([]InjectedMemory, []DroppedMemory) {
	now := s.now()
	scored := make([]scoredMemory, 0, len(rows))
	dropped := make([]DroppedMemory, 0, 4)
	for _, m := range rows {
		if err := CheckAutoInject(actor, m, now); err != nil {
			reason := "not_visible"
			if m.Visibility == domain.MemoryHumanOnly {
				reason = "human_only_no_auto_inject"
			} else if m.Sensitivity == SensitivitySecret {
				reason = "secret"
			}
			dropped = append(dropped, DroppedMemory{ID: m.ID, EntityType: m.EntityType, EntityKey: m.EntityKey, Reason: reason, Tokens: EstimateTokens(m.Summary)})
			continue
		}
		scored = append(scored, scoredMemory{memory: m, score: s.scoreMemory(m, req, now), reason: scoreReason(m, req)})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].memory.ID < scored[j].memory.ID
	})

	used := int64(0)
	injected := make([]InjectedMemory, 0, len(scored))
	for _, sc := range scored {
		m := sc.memory
		summary := m.Summary
		if strings.TrimSpace(summary) == "" {
			summary, _ = TruncateRunes(m.Content, s.cfg.RenderSummaryMaxChars)
		} else {
			summary, _ = TruncateRunes(summary, s.cfg.RenderSummaryMaxChars)
		}
		if actor.IsAgent() && s.cfg.RedactSensitiveForAgents {
			summary = Redact(summary).Cleaned
		}
		tokens := EstimateTokens(summary) + injectOverheadTokens
		if used+tokens > budget {
			dropped = append(dropped, DroppedMemory{ID: m.ID, EntityType: m.EntityType, EntityKey: m.EntityKey, Reason: "budget", Tokens: tokens})
			continue
		}
		used += tokens
		injected = append(injected, InjectedMemory{
			ID:         m.ID,
			EntityType: m.EntityType,
			EntityKey:  m.EntityKey,
			Version:    m.Version,
			Summary:    summary,
			Source:     sourceLabel(m),
			Visibility: m.Visibility,
			Confidence: m.Confidence,
			Pinned:     TagsContain(m.Tags, TagPinned),
			Tokens:     tokens,
			Reason:     sc.reason,
		})
	}
	return injected, dropped
}

// scoreMemory 是可解释的打分：钉住 > 决策/偏好 > 任务命中 > 关键词 > 新近 > 置信度。
func (s *Service) scoreMemory(m domain.Memory, req InjectionRequest, now time.Time) float64 {
	score := 0.0
	if TagsContain(m.Tags, TagPinned) {
		score += 1000
	}
	priority := map[string]float64{
		EntityDecision: 200, EntityPreference: 190, EntityDispute: 180,
		EntityCapability: 120, EntityProject: 100, EntityDepartment: 90,
		EntityTask: 80, EntityEmployee: 70, EntityPerson: 60,
	}
	score += priority[m.EntityType]
	if req.TaskID != "" && TagsContain(m.Tags, TaskTag(req.TaskID)) {
		score += 150
	}
	if req.TaskBrief != "" {
		score += keywordOverlap(req.TaskBrief, m.Content, 50)
	}
	if t := ParseTime(m.UpdatedAt); !t.IsZero() {
		age := now.Sub(t)
		switch {
		case age < 24*time.Hour:
			score += 40
		case age < 7*24*time.Hour:
			score += 25
		case age < 30*24*time.Hour:
			score += 10
		}
	}
	switch m.Confidence {
	case ConfidenceHigh:
		score += 15
	case ConfidenceMedium:
		score += 5
	}
	return score
}

func scoreReason(m domain.Memory, req InjectionRequest) string {
	var parts []string
	if TagsContain(m.Tags, TagPinned) {
		parts = append(parts, "pinned")
	}
	parts = append(parts, "entity:"+m.EntityType)
	if req.TaskID != "" && TagsContain(m.Tags, TaskTag(req.TaskID)) {
		parts = append(parts, "task_match")
	}
	if req.TaskBrief != "" && keywordOverlap(req.TaskBrief, m.Content, 1) > 0 {
		parts = append(parts, "keyword")
	}
	return strings.Join(parts, "+")
}

// keywordOverlap 用字符二元组重叠度做廉价相关性（中文友好，无需分词）。
func keywordOverlap(query, text string, cap float64) float64 {
	q := []rune(strings.ToLower(query))
	if len(q) < 2 {
		return 0
	}
	set := map[string]bool{}
	for i := 0; i+1 < len(q); i++ {
		set[string(q[i:i+2])] = true
	}
	if len(set) == 0 {
		return 0
	}
	t := []rune(strings.ToLower(text))
	hits := 0
	for i := 0; i+1 < len(t); i++ {
		if set[string(t[i:i+2])] {
			hits++
		}
	}
	v := float64(hits) * 8
	if v > cap {
		v = cap
	}
	return v
}

// fitManual 把手册裁到分区预算；手册优先级高于普通档案，超预算也只截尾不丢弃。
func (s *Service) fitManual(m domain.PromptManual, budget int64) *ManualInjection {
	content := m.Content
	tokens := EstimateTokens(content)
	truncated := false
	if budget > 0 && tokens > budget {
		content, truncated = truncateToTokens(content, budget)
		tokens = EstimateTokens(content)
	}
	return &ManualInjection{Version: m.Version, Content: content, Tokens: tokens, Truncated: truncated}
}

// truncateToTokens 按估算 token 截断（保守取 1.5 字符/token）。
func truncateToTokens(s string, tokens int64) (string, bool) {
	if tokens <= 0 {
		return "", s != ""
	}
	runes := int(float64(tokens) * 1.5)
	return TruncateRunes(s, runes)
}

func sourceLabel(m domain.Memory) string {
	return fmt.Sprintf("memory://%s/%s/%s#v%d", m.Scope, m.EntityType, m.EntityKey, m.Version)
}

func estimateContext(c *ContextRequest) int64 {
	var n int64
	if c.Manual != nil {
		n += c.Manual.Tokens
	}
	if c.Task != nil {
		n += c.Task.Tokens
	}
	for _, m := range c.Memories {
		n += m.Tokens
	}
	if c.RecentTokens > 0 {
		n += c.RecentTokens
	}
	return n
}

// recordReads 为每条注入写读取留痕（FR-MEM-10）；失败只告警，不影响装配。
func (s *Service) recordReads(ctx context.Context, c *ContextRequest) {
	if !s.cfg.ReadTraceEnabled {
		return
	}
	for _, m := range c.Memories {
		rec := MemoryRead{
			MemoryID:   m.ID,
			ReaderType: orDefault(c.AgentKind, ActorAssistant),
			ReaderID:   c.AgentID,
			TurnID:     c.TurnID,
			CreatedAt:  s.stamp(),
		}
		if err := s.repo.AddMemoryRead(ctx, rec); err != nil {
			s.log.Warn("memory: read trace failed", "memory_id", m.ID, "error", err)
		}
	}
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

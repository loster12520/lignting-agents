package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Patch 是档案编辑请求（FR-MEM-09）。空字段表示不修改。
type Patch struct {
	Summary         string
	Content         string
	Tags            []string
	Visibility      string
	Confidence      string
	ExpectedVersion int
}

// SubmitCandidate 提交候选记忆（员工/主管/助理）。候选必须经 Approve 才成为生效档案。
func (s *Service) SubmitCandidate(ctx context.Context, c Candidate, actor Access) (SubmitResult, error) {
	if !s.cfg.Enabled {
		return SubmitResult{Disabled: true}, nil
	}
	c = normalizeCandidate(c, actor)
	if err := s.validateCandidate(c, actor, true, true); err != nil {
		return SubmitResult{}, err
	}
	red := Redact(c.Content)
	if red.SecretRemaining {
		s.log.Warn("memory: secret remained after redaction", "entity_type", c.EntityType, "entity_key", c.EntityKey)
		return SubmitResult{}, domain.NewError(CodeSensitive, "内容含密钥/令牌，已拒绝写入", 422)
	}
	if len(red.Kinds) > 0 {
		c.Content = red.Cleaned
	}
	if strings.TrimSpace(c.Summary) != "" {
		sred := Redact(c.Summary)
		if sred.SecretRemaining {
			return SubmitResult{}, domain.NewError(CodeSensitive, "摘要含密钥/令牌，已拒绝写入", 422)
		}
		c.Summary = sred.Cleaned
	}
	if inj := DetectInjection(c.Content); len(inj) > 0 {
		c.Tags = append(c.Tags, TagUntrusted)
	}
	if err := s.validateAfterRedact(c, actor); err != nil {
		return SubmitResult{}, err
	}
	if err := s.checkQuota(ctx, c); err != nil {
		s.log.Warn("memory: quota rejected", "agent", c.AgentID, "task", c.SourceTaskID)
		return SubmitResult{}, err
	}

	warnings := make([]string, 0, 4)
	if len(red.Kinds) > 0 {
		warnings = append(warnings, fmt.Sprintf("已脱敏敏感字段: %s", strings.Join(red.Kinds, ",")))
	}
	if len(DetectInjection(c.Content)) > 0 {
		warnings = append(warnings, "检测到指令注入特征，已标记 untrusted，禁止晋升为管理手册")
	}
	if c.Confidence == ConfidenceLow {
		warnings = append(warnings, "低置信度：需人工验证后再批准")
	}
	if !c.Reusable {
		warnings = append(warnings, "未声明可复用性（gate: reusable）")
	}
	if !c.DecisionRelevant {
		warnings = append(warnings, "未声明决策相关性（gate: decision-relevant）")
	}

	existing, found, err := s.findExisting(ctx, c)
	if err != nil {
		return SubmitResult{}, err
	}
	res := SubmitResult{Warnings: warnings}
	m := s.buildMemory(c, existing, found, domain.MemoryStatusCandidate)

	if found {
		res.ConflictsWith = existing.ID
		if HashContent(existing.Content) == HashContent(c.Content) {
			res.Duplicate = true
			res.Memory = existing
			s.event(ctx, existing.ID, ActionDuplicate, actor, map[string]any{"entity_key": c.EntityKey})
			return res, nil
		}
		res.Conflict = true
		warnings = append(warnings, "同键已有生效版本，批准后旧版本将失效（决策/争议内容不可覆盖）")
		res.Warnings = warnings
	}

	if err := s.repo.InsertMemory(ctx, m); err != nil {
		return SubmitResult{}, err
	}
	s.event(ctx, m.ID, ActionCandidateCreated, actor, map[string]any{
		"entity_type":    m.EntityType,
		"entity_key":     m.EntityKey,
		"confidence":     m.Confidence,
		"evidence_count": len(c.Evidence),
		"conflict_with":  res.ConflictsWith,
		"warnings":       warnings,
		"task_id":        c.SourceTaskID,
	})
	res.Memory = m
	return res, nil
}

// CreateDirect 由人类直接创建生效档案（POST /api/memories 的“直接创建”分支）。
func (s *Service) CreateDirect(ctx context.Context, c Candidate, actor Access) (domain.Memory, error) {
	if !s.cfg.Enabled {
		return domain.Memory{}, domain.NewError(CodeMemoryDisabled, "记忆层已关闭", 503)
	}
	if !actor.IsHuman() {
		return domain.Memory{}, forbiddenError("仅人类可直接创建生效档案；Agent 必须提交候选", nil)
	}
	c = normalizeCandidate(c, actor)
	c.CreatedBy = actor.ID
	if err := s.validateCandidate(c, actor, false, false); err != nil {
		return domain.Memory{}, err
	}
	red := Redact(c.Content)
	if red.SecretRemaining {
		return domain.Memory{}, domain.NewError(CodeSensitive, "内容含密钥/令牌，已拒绝写入", 422)
	}
	c.Content = red.Cleaned
	if strings.TrimSpace(c.Summary) != "" {
		sred := Redact(c.Summary)
		if sred.SecretRemaining {
			return domain.Memory{}, domain.NewError(CodeSensitive, "摘要含密钥/令牌，已拒绝写入", 422)
		}
		c.Summary = sred.Cleaned
	}
	if inj := DetectInjection(c.Content); len(inj) > 0 {
		c.Tags = append(c.Tags, TagUntrusted)
	}
	existing, found, err := s.findEffective(ctx, c)
	if err != nil {
		return domain.Memory{}, err
	}
	m := s.buildMemory(c, existing, found, domain.MemoryStatusActive)
	if err := s.repo.InsertMemory(ctx, m); err != nil {
		return domain.Memory{}, err
	}
	if found {
		s.supersede(ctx, existing, m.ID, actor)
	}
	s.event(ctx, m.ID, ActionApproved, actor, map[string]any{"direct": true})
	return m, nil
}

// Approve 批准候选：容量校验 → 旧版本失效（supersede）→ 生效 → 必要时排队压缩。
func (s *Service) Approve(ctx context.Context, id string, actor Access) (ApproveResult, error) {
	if !s.cfg.Enabled {
		return ApproveResult{}, domain.NewError(CodeMemoryDisabled, "记忆层已关闭", 503)
	}
	if !CanReview(actor) {
		return ApproveResult{}, forbiddenError("无权审核候选记忆", nil)
	}
	m, err := s.getRaw(ctx, id)
	if err != nil {
		return ApproveResult{}, err
	}
	if m.Status != domain.MemoryStatusCandidate {
		return ApproveResult{}, conflictError("只有候选记忆可以批准", map[string]any{"status": m.Status})
	}
	if IsTombstone(m) {
		return ApproveResult{}, notFoundError("记忆不存在")
	}
	over, err := s.capacityCheck(m.EntityType, m.Content)
	if err != nil {
		return ApproveResult{}, err
	}

	now := s.stamp()
	supersededID := ""
	if m.Supersedes != "" {
		if old, gerr := s.getRaw(ctx, m.Supersedes); gerr == nil && Effective(old, s.now()) {
			s.supersede(ctx, old, m.ID, actor)
			supersededID = old.ID
		}
	} else {
		// 防御：批准时再查一次同键生效版本，避免提交后到批准前出现的竞态。
		if old, found, ferr := s.findEffective(ctx, Candidate{
			Scope: m.Scope, DepartmentID: m.DepartmentID, AgentID: m.AgentID,
			EntityType: m.EntityType, EntityKey: m.EntityKey, Content: m.Content,
		}); ferr == nil && found && old.ID != m.ID {
			s.supersede(ctx, old, m.ID, actor)
			supersededID = old.ID
		}
	}

	m.Status = domain.MemoryStatusActive
	if strings.TrimSpace(m.ValidFrom) == "" {
		m.ValidFrom = now
	}
	m.UpdatedAt = now
	if err := s.repo.UpdateMemory(ctx, m, m.Version); err != nil {
		if errors.Is(err, ErrConflict) {
			return ApproveResult{}, conflictError("记忆已被其他人修改，请刷新后重试", map[string]any{"memory_id": m.ID})
		}
		return ApproveResult{}, err
	}
	s.event(ctx, m.ID, ActionApproved, actor, map[string]any{"superseded": supersededID, "over_limit": over})
	res := ApproveResult{Memory: m, SupersededID: supersededID, OverLimit: over}
	if over {
		res.QueuedCompress = s.enqueueCompression(m.ID, "auto", s.cfg.CompressTargetChars)
	}
	return res, nil
}

// Reject 拒绝候选；必须给出理由（AC-05）。拒绝不删除，保留供审计与查询。
func (s *Service) Reject(ctx context.Context, id, reason string, actor Access) (domain.Memory, error) {
	if !CanReview(actor) {
		return domain.Memory{}, forbiddenError("无权拒绝候选记忆", nil)
	}
	if strings.TrimSpace(reason) == "" {
		return domain.Memory{}, invalidError("拒绝理由不能为空", map[string]any{"field": "reason"})
	}
	m, err := s.getRaw(ctx, id)
	if err != nil {
		return domain.Memory{}, err
	}
	if m.Status != domain.MemoryStatusCandidate {
		return domain.Memory{}, conflictError("只有候选记忆可以拒绝", map[string]any{"status": m.Status})
	}
	m.Status = domain.MemoryStatusRejected
	m.UpdatedAt = s.stamp()
	if err := s.repo.UpdateMemory(ctx, m, m.Version); err != nil {
		return domain.Memory{}, err
	}
	s.event(ctx, m.ID, ActionRejected, actor, map[string]any{"reason": reason})
	return m, nil
}

// Update 编辑档案（元数据/摘要/可见性；决策/争议/偏好内容不可原地修改）。
func (s *Service) Update(ctx context.Context, id string, p Patch, actor Access) (domain.Memory, error) {
	if !CanReview(actor) {
		return domain.Memory{}, forbiddenError("无权编辑档案", nil)
	}
	m, err := s.getRaw(ctx, id)
	if err != nil {
		return domain.Memory{}, err
	}
	if IsTombstone(m) {
		return domain.Memory{}, notFoundError("记忆不存在")
	}
	if p.ExpectedVersion > 0 && p.ExpectedVersion != m.Version {
		return domain.Memory{}, conflictError("版本不一致，请刷新后重试", map[string]any{
			"expected": p.ExpectedVersion, "actual": m.Version,
		})
	}
	changed := map[string]any{}
	if p.Summary != "" && p.Summary != m.Summary {
		sred := Redact(p.Summary)
		if sred.SecretRemaining {
			return domain.Memory{}, domain.NewError(CodeSensitive, "摘要含密钥/令牌，已拒绝写入", 422)
		}
		m.Summary, _ = TruncateRunes(sred.Cleaned, s.cfg.RenderContentMaxChars)
		changed["summary"] = true
	}
	if strings.TrimSpace(p.Content) != "" && p.Content != m.Content {
		if ImmutableEntity(m.EntityType) && !(actor.IsHuman() && m.Status == domain.MemoryStatusCandidate) {
			return domain.Memory{}, conflictError("决策/争议/偏好内容不可覆盖，请追加新版本并让其 supersede 旧版本", map[string]any{"entity_type": m.EntityType})
		}
		red := Redact(p.Content)
		if red.SecretRemaining {
			return domain.Memory{}, domain.NewError(CodeSensitive, "内容含密钥/令牌，已拒绝写入", 422)
		}
		if RuneLen(red.Cleaned) > s.cfg.StorageMaxChars {
			return domain.Memory{}, domain.NewError(domain.CodeInvalid, "内容超过存储上限", 422)
		}
		m.Content = red.Cleaned
		if m.Summary == "" {
			m.Summary, _ = TruncateRunes(m.Content, s.cfg.RenderSummaryMaxChars)
		}
		changed["content"] = true
	}
	if len(p.Tags) > 0 {
		m.Tags = JoinTags(p.Tags)
		changed["tags"] = true
	}
	if p.Visibility != "" && p.Visibility != m.Visibility {
		if !validVisibility(p.Visibility) {
			return domain.Memory{}, invalidError("未知可见性", map[string]any{"visibility": p.Visibility})
		}
		m.Visibility = p.Visibility
		changed["visibility"] = true
	}
	if p.Confidence != "" && p.Confidence != m.Confidence {
		if !validConfidence(p.Confidence) {
			return domain.Memory{}, invalidError("未知置信度", map[string]any{"confidence": p.Confidence})
		}
		m.Confidence = p.Confidence
		changed["confidence"] = true
	}
	if len(changed) == 0 {
		return m, nil
	}
	m.Version++
	m.UpdatedAt = s.stamp()
	if err := s.repo.UpdateMemory(ctx, m, m.Version-1); err != nil {
		if errors.Is(err, ErrConflict) {
			return domain.Memory{}, conflictError("记忆已被其他人修改，请刷新后重试", map[string]any{"memory_id": m.ID})
		}
		return domain.Memory{}, err
	}
	s.event(ctx, m.ID, ActionEdited, actor, changed)
	return m, nil
}

// Delete 墓碑删除：保留行与内容用于审计，但任何读取/注入路径都不可见（FR-MEM-09/10）。
func (s *Service) Delete(ctx context.Context, id, reason string, actor Access) error {
	if !actor.IsHuman() && actor.Type != ActorAssistant {
		return forbiddenError("仅人类或助理可删除记忆", nil)
	}
	m, err := s.getRaw(ctx, id)
	if err != nil {
		return err
	}
	if IsTombstone(m) {
		return notFoundError("记忆不存在")
	}
	now := s.stamp()
	m.Status = domain.MemoryStatusDeleted
	m.DeletedAt = now
	m.ValidTo = now
	m.UpdatedAt = now
	if err := s.repo.UpdateMemory(ctx, m, m.Version); err != nil {
		if errors.Is(err, ErrConflict) {
			return conflictError("记忆已被其他人修改，请刷新后重试", map[string]any{"memory_id": m.ID})
		}
		return err
	}
	s.event(ctx, m.ID, ActionDeleted, actor, map[string]any{"reason": reason, "tombstone": true})
	return nil
}

// Pin 钉住/取消钉住（注入排序优先，无独立列，复用 tags）。
func (s *Service) Pin(ctx context.Context, id string, pinned bool, actor Access) (domain.Memory, error) {
	if !CanReview(actor) {
		return domain.Memory{}, forbiddenError("无权钉住档案", nil)
	}
	m, err := s.getRaw(ctx, id)
	if err != nil {
		return domain.Memory{}, err
	}
	if IsTombstone(m) {
		return domain.Memory{}, notFoundError("记忆不存在")
	}
	tags := SplitTags(m.Tags)
	out := make([]string, 0, len(tags)+1)
	for _, t := range tags {
		if t != TagPinned {
			out = append(out, t)
		}
	}
	if pinned {
		out = append(out, TagPinned)
	}
	if JoinTags(out) == JoinTags(tags) {
		return m, nil
	}
	m.Tags = JoinTags(out)
	m.Version++
	m.UpdatedAt = s.stamp()
	if err := s.repo.UpdateMemory(ctx, m, m.Version-1); err != nil {
		return domain.Memory{}, err
	}
	action := ActionUnpinned
	if pinned {
		action = ActionPinned
	}
	s.event(ctx, m.ID, action, actor, nil)
	return m, nil
}

// Get 读取单条档案。墓碑对所有人不可见（不泄露存在性）；人类读取由上层写审计。
func (s *Service) Get(ctx context.Context, id string, actor Access) (domain.Memory, error) {
	m, err := s.getRaw(ctx, id)
	if err != nil {
		return domain.Memory{}, err
	}
	if IsTombstone(m) {
		return domain.Memory{}, notFoundError("记忆不存在")
	}
	if actor.IsHuman() {
		return m, nil
	}
	if !s.agentCanSee(m, actor) {
		return domain.Memory{}, forbiddenError("无权读取该记忆", map[string]any{"memory_id": m.ID})
	}
	return m, nil
}

// List 列出档案（检查器/检索）。人类看全部；Agent 按可见性 + 部门收敛。
func (s *Service) List(ctx context.Context, f Filter, actor Access) ([]domain.Memory, error) {
	if !actor.IsHuman() && !actor.IsAgent() && actor.Type != ActorSystem {
		return nil, forbiddenError("未知访问主体", nil)
	}
	if !actor.IsHuman() {
		// Agent 永不看墓碑；human-only / secret 在行级过滤。
		f.IncludeDeleted = false
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	f.Limit = limit
	rows, err := s.repo.ListMemories(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Memory, 0, len(rows))
	for _, m := range rows {
		if IsTombstone(m) && !(f.IncludeDeleted && actor.IsHuman()) {
			continue
		}
		if actor.IsHuman() {
			out = append(out, m)
			continue
		}
		if !s.agentCanSee(m, actor) {
			continue
		}
		if !f.IncludeExpired && m.Status == domain.MemoryStatusActive && !Effective(m, s.now()) {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// agentCanSee 是 Agent 侧的粗粒度可见性：候选只对同部门管理者/本人可见。
func (s *Service) agentCanSee(m domain.Memory, actor Access) bool {
	if m.Visibility == domain.MemoryHumanOnly {
		return false
	}
	if m.Sensitivity == SensitivitySecret {
		return false
	}
	if IsTombstone(m) {
		return false
	}
	if m.Status == domain.MemoryStatusCandidate {
		return m.AgentID == actor.ID || (actor.IsManager() && (actor.OrgWide || actor.DepartmentID == m.DepartmentID))
	}
	return VisibleTo(actor, m, s.now())
}

// ---- 内部工具 ----

// normalizeCandidate 补齐默认值：归属、范围、置信度、敏感度、可见性。
func normalizeCandidate(c Candidate, actor Access) Candidate {
	if c.CreatedBy == "" {
		c.CreatedBy = actor.ID
	}
	if c.AgentID == "" && actor.IsAgent() {
		c.AgentID = actor.ID
	}
	if c.DepartmentID == "" {
		c.DepartmentID = actor.DepartmentID
	}
	if c.Scope == "" {
		switch actor.Type {
		case ActorEmployee:
			c.Scope = domain.MemoryScopeEmployee
		case ActorSupervisor:
			c.Scope = domain.MemoryScopeDepartment
		default:
			c.Scope = domain.MemoryScopeAssistant
		}
	}
	if c.Confidence == "" {
		c.Confidence = ConfidenceMedium
	}
	if c.Sensitivity == "" {
		c.Sensitivity = SensitivityNormal
	}
	if c.Visibility == "" {
		c.Visibility = domain.MemoryTeam
	}
	return c
}

func (s *Service) validateCandidate(c Candidate, actor Access, requireEvidence, isCandidate bool) error {
	if !KnownEntityType(c.EntityType) {
		return invalidError("未知实体类型", map[string]any{"entity_type": c.EntityType})
	}
	if strings.TrimSpace(c.EntityKey) == "" {
		return invalidError("缺少实体键", map[string]any{"field": "entity_key"})
	}
	if strings.TrimSpace(c.Content) == "" {
		return invalidError("内容不能为空", map[string]any{"field": "content"})
	}
	limit := s.cfg.StorageMaxChars
	if isCandidate {
		limit = s.cfg.CandidateMaxChars
	}
	if limit > 0 && RuneLen(c.Content) > limit {
		return invalidError("内容超长", map[string]any{"limit": limit, "actual": RuneLen(c.Content)})
	}
	if !validScope(c.Scope) {
		return invalidError("未知记忆范围", map[string]any{"scope": c.Scope})
	}
	if !validVisibility(c.Visibility) {
		return invalidError("未知可见性", map[string]any{"visibility": c.Visibility})
	}
	if !validConfidence(c.Confidence) {
		return invalidError("未知置信度", map[string]any{"confidence": c.Confidence})
	}
	if !validSensitivity(c.Sensitivity) {
		return invalidError("未知敏感度", map[string]any{"sensitivity": c.Sensitivity})
	}
	if requireEvidence && s.cfg.RequireEvidence && !HasEvidence(c.Evidence) {
		return invalidError("缺少可核查证据（至少 1 条带 ref 的 evidence）", map[string]any{"field": "evidence"})
	}
	if actor.Type == ActorEmployee {
		if c.Visibility == domain.MemoryHumanOnly {
			return forbiddenError("员工不得写入 human-only 记忆", nil)
		}
		switch c.EntityType {
		case EntityDecision, EntityDispute, EntityPreference:
			return forbiddenError("决策/争议/人类偏好只能由主管、助理或人类写入", nil)
		}
	}
	return nil
}

func (s *Service) validateAfterRedact(c Candidate, actor Access) error {
	if RuneLen(c.Content) == 0 {
		return invalidError("脱敏后内容为空", nil)
	}
	return nil
}

func validScope(s string) bool {
	switch s {
	case domain.MemoryScopeAssistant, domain.MemoryScopeDepartment, domain.MemoryScopeEmployee:
		return true
	}
	return false
}

func validVisibility(v string) bool {
	switch v {
	case domain.MemoryAgentPrivate, domain.MemoryTeam, domain.MemoryOrg, domain.MemoryHumanOnly:
		return true
	}
	return false
}

func validConfidence(c string) bool {
	switch c {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
		return true
	}
	return false
}

func validSensitivity(s string) bool {
	switch s {
	case SensitivityNormal, SensitivitySensitive, SensitivitySecret:
		return true
	}
	return false
}

// findExisting 查同 scope/部门/agent/实体类型/实体键 的既有条目（生效版本或待审候选），
// 用于去重与冲突判定：候选之间也要去重，否则同一事实会反复入队。
func (s *Service) findExisting(ctx context.Context, c Candidate) (domain.Memory, bool, error) {
	rows, err := s.repo.ListMemories(ctx, Filter{
		Scope:        c.Scope,
		DepartmentID: c.DepartmentID,
		AgentID:      c.AgentID,
		EntityType:   c.EntityType,
		EntityKey:    c.EntityKey,
		Status:       []string{domain.MemoryStatusActive, domain.MemoryStatusCandidate},
		Limit:        10,
	})
	if err != nil {
		return domain.Memory{}, false, err
	}
	now := s.now()
	var best domain.Memory
	found := false
	for _, m := range rows {
		if IsTombstone(m) {
			continue
		}
		usable := Effective(m, now) || m.Status == domain.MemoryStatusCandidate
		if !usable {
			continue
		}
		if !found || m.Version > best.Version {
			best = m
			found = true
		}
	}
	return best, found, nil
}

// findEffective 查同 scope/部门/agent/实体类型/实体键 的生效版本。
func (s *Service) findEffective(ctx context.Context, c Candidate) (domain.Memory, bool, error) {
	rows, err := s.repo.ListMemories(ctx, Filter{
		Scope:        c.Scope,
		DepartmentID: c.DepartmentID,
		AgentID:      c.AgentID,
		EntityType:   c.EntityType,
		EntityKey:    c.EntityKey,
		Status:       []string{domain.MemoryStatusActive},
	})
	if err != nil {
		return domain.Memory{}, false, err
	}
	now := s.now()
	for _, m := range rows {
		if Effective(m, now) {
			return m, true, nil
		}
	}
	return domain.Memory{}, false, nil
}

func (s *Service) buildMemory(c Candidate, existing domain.Memory, found bool, status string) domain.Memory {
	now := s.stamp()
	version := 1
	supersedes := c.Supersedes
	if found {
		version = existing.Version + 1
		supersedes = existing.ID
	}
	summary := strings.TrimSpace(c.Summary)
	if summary == "" {
		summary, _ = TruncateRunes(c.Content, s.cfg.RenderSummaryMaxChars)
	} else if RuneLen(summary) > s.cfg.RenderSummaryMaxChars {
		summary, _ = TruncateRunes(summary, s.cfg.RenderSummaryMaxChars)
	}
	tags := append([]string{}, c.Tags...)
	if c.SourceTaskID != "" {
		tags = append(tags, TaskTag(c.SourceTaskID))
	}
	if c.SourceEvent != "" {
		tags = append(tags, TagSourcePrefix+c.SourceEvent)
	}
	validFrom := strings.TrimSpace(c.ValidFrom)
	if validFrom == "" {
		validFrom = now
	}
	return domain.Memory{
		ID:           domain.NewID("mem"),
		Scope:        c.Scope,
		DepartmentID: c.DepartmentID,
		AgentID:      c.AgentID,
		EntityType:   c.EntityType,
		EntityKey:    c.EntityKey,
		Content:      c.Content,
		Summary:      summary,
		Tags:         JoinTags(tags),
		Confidence:   c.Confidence,
		Evidence:     EncodeEvidence(c.Evidence),
		Sensitivity:  c.Sensitivity,
		Visibility:   c.Visibility,
		ValidFrom:    validFrom,
		ValidTo:      "",
		Supersedes:   supersedes,
		Version:      version,
		Status:       status,
		CreatedBy:    c.CreatedBy,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// supersede 让旧版本失效（双时间：ValidTo=now，内容与 ID 不变，不物理删除）。
func (s *Service) supersede(ctx context.Context, old domain.Memory, byID string, actor Access) {
	if IsTombstone(old) {
		return
	}
	now := s.stamp()
	old.UpdatedAt = now
	reason := "expired"
	if old.Status == domain.MemoryStatusCandidate {
		// 未生效的候选被更新版本取代：直接置 rejected，避免之后被单独批准产生双生效。
		old.Status = domain.MemoryStatusRejected
		reason = "replaced_by_candidate"
	} else {
		old.ValidTo = now
	}
	if err := s.repo.UpdateMemory(ctx, old, old.Version); err != nil {
		s.log.Warn("memory: supersede failed", "memory_id", old.ID, "error", err)
		return
	}
	s.event(ctx, old.ID, ActionSuperseded, actor, map[string]any{"superseded_by": byID, "valid_to": now, "kind": reason})
}

// capacityCheck 判定内容是否超存储上限；reject 模式直接返回错误并给压缩指引。
func (s *Service) capacityCheck(entityType, content string) (bool, error) {
	if s.cfg.IsExemptEntity(entityType) {
		return false, nil
	}
	n := RuneLen(content)
	if n <= s.cfg.StorageMaxChars {
		return false, nil
	}
	if s.cfg.OverflowAction == OverflowReject {
		return false, domain.NewError(domain.CodeInvalid, "档案超过存储上限，请先压缩或拆分", 422)
	}
	return true, nil
}

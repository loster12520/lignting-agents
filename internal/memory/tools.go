package memory

import (
	"context"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// ReadArgs 是 memory.read 工具入参。只允许读取单条：要么给 id，要么给 entity_type+entity_key
// （命中多版本时要求用 id 精确读取），不支持批量/预取（lead 裁决，2026-11-17）。
type ReadArgs struct {
	ID         string
	EntityType string
	EntityKey  string
	TurnID     string
	Reason     string
}

// MemoryView 是返回给 Agent 的单条记忆视图（默认只给摘要 + 受权限约束的正文）。
type MemoryView struct {
	ID          string
	EntityType  string
	EntityKey   string
	Version     int
	Summary     string
	Content     string
	Visibility  string
	Confidence  string
	Sensitivity string
	Source      string
	Redacted    bool
	Truncated   bool
}

// ReadResult 是 memory.read 的结果；Denied 时同时返回可解释原因（不泄露存在性时用 notFound）。
type ReadResult struct {
	Memory       *MemoryView
	Denied       bool
	DeniedReason string
}

// ReadTool 是供 core 注册的「记忆读取」工具（FR-MEM-08 必须提供，否则不得声称按需读取）。
type ReadTool struct {
	svc *Service
}

// NewReadTool 构造工具；svc 为空时 Invoke 返回错误。
func NewReadTool(svc *Service) *ReadTool { return &ReadTool{svc: svc} }

// ToolDefinition 是给 core 注册用的元数据（不含 JSON Schema 之外的实现细节）。
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// Definition 返回工具定义。描述里显式写明 human-only 策略，避免模型自行脑补。
func (t *ReadTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "memory.read",
		Description: "按 id 或 (entity_type, entity_key) 读取一条长期记忆（档案）。" +
			"只读单条、不支持批量。human-only 记忆仅助理可显式读取且会被脱敏；" +
			"越权读取会被拒绝并记录审计。已删除记忆不可读。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          map[string]any{"type": "string", "description": "记忆 ID（二选一）"},
				"entity_type": map[string]any{"type": "string", "description": "实体类型，如 decision/team/project"},
				"entity_key":  map[string]any{"type": "string", "description": "实体键，与 entity_type 配对"},
				"turn_id":     map[string]any{"type": "string", "description": "当前 turn id（审计必需）"},
			},
			"oneOf": []any{
				map[string]any{"required": []string{"id", "turn_id"}},
				map[string]any{"required": []string{"entity_type", "entity_key", "turn_id"}},
			},
		},
	}
}

// Invoke 执行读取：权限裁决 → 脱敏/截断 → 写 memory_reads 与 memory_events。
func (t *ReadTool) Invoke(ctx context.Context, actor Access, args ReadArgs) (ReadResult, error) {
	if t.svc == nil {
		return ReadResult{}, invalidError("记忆服务未初始化", nil)
	}
	if strings.TrimSpace(args.TurnID) == "" {
		return ReadResult{}, invalidError("memory.read 必须带 turn_id 以便审计", map[string]any{"field": "turn_id"})
	}
	hasID := strings.TrimSpace(args.ID) != ""
	hasKey := strings.TrimSpace(args.EntityType) != "" || strings.TrimSpace(args.EntityKey) != ""
	if hasID == hasKey {
		return ReadResult{}, invalidError("必须且只能指定 id 或 (entity_type+entity_key) 之一", nil)
	}

	var m domain.Memory
	if hasID {
		got, err := t.svc.getRaw(ctx, args.ID)
		if err != nil {
			return ReadResult{}, err
		}
		m = got
	} else {
		if strings.TrimSpace(args.EntityType) == "" || strings.TrimSpace(args.EntityKey) == "" {
			return ReadResult{}, invalidError("entity_type 与 entity_key 必须同时提供", nil)
		}
		rows, err := t.svc.repo.ListMemories(ctx, Filter{
			EntityType: args.EntityType,
			EntityKey:  args.EntityKey,
			Status:     []string{domain.MemoryStatusActive},
			Limit:      10,
		})
		if err != nil {
			return ReadResult{}, err
		}
		now := t.svc.now()
		matches := make([]domain.Memory, 0, 2)
		for _, r := range rows {
			if !IsTombstone(r) && Effective(r, now) {
				matches = append(matches, r)
			}
		}
		if len(matches) == 0 {
			return ReadResult{}, notFoundError("记忆不存在")
		}
		if len(matches) > 1 {
			return ReadResult{}, conflictError("同键命中多条记忆，请用 id 精确读取", map[string]any{"count": len(matches)})
		}
		m = matches[0]
	}

	now := t.svc.now()
	decision := DecideRead(actor, m, true, args.TurnID, now)
	if !decision.Allowed {
		t.svc.event(ctx, m.ID, ActionReadDenied, actor, map[string]any{
			"turn_id": args.TurnID, "reason": decision.Reason,
		})
		if decision.Reason == "deleted" {
			return ReadResult{}, notFoundError("记忆不存在")
		}
		return ReadResult{Denied: true, DeniedReason: decision.Reason},
			forbiddenError("无权读取该记忆", map[string]any{"reason": decision.Reason})
	}

	view := MemoryView{
		ID:          m.ID,
		EntityType:  m.EntityType,
		EntityKey:   m.EntityKey,
		Version:     m.Version,
		Summary:     m.Summary,
		Content:     m.Content,
		Visibility:  m.Visibility,
		Confidence:  m.Confidence,
		Sensitivity: m.Sensitivity,
		Source:      sourceLabel(m),
	}
	if decision.Redact || (actor.IsAgent() && t.svc.cfg.RedactSensitiveForAgents) {
		view.Content = Redact(view.Content).Cleaned
		view.Summary = Redact(view.Summary).Cleaned
		view.Redacted = true
	}
	if RuneLen(view.Content) > t.svc.cfg.RenderContentMaxChars {
		view.Content, view.Truncated = TruncateRunes(view.Content, t.svc.cfg.RenderContentMaxChars)
	}

	if t.svc.cfg.ReadTraceEnabled {
		if err := t.svc.repo.AddMemoryRead(ctx, MemoryRead{
			MemoryID:   m.ID,
			ReaderType: actor.Type,
			ReaderID:   actor.ID,
			TurnID:     args.TurnID,
			CreatedAt:  t.svc.stamp(),
		}); err != nil {
			t.svc.log.Warn("memory: read trace failed", "memory_id", m.ID, "error", err)
		}
	}
	t.svc.event(ctx, m.ID, ActionRead, actor, map[string]any{
		"turn_id": args.TurnID, "reason": decision.Reason,
		"redacted": view.Redacted, "truncated": view.Truncated,
		"via": "tool", "why": args.Reason,
	})
	return ReadResult{Memory: &view}, nil
}

package main

import (
	"context"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/memory"
)

// coreMemoryInjector 把 memory.Service 适配成 core.MemoryInjector。
// core 优先走 InjectWithSnapshot（记忆来源/token/降级原因进 context_snapshots）；
// 旧 Inject 保留给不需要快照的调用方。
type coreMemoryInjector struct{ svc *memory.Service }

func (m coreMemoryInjector) Inject(ctx context.Context, q core.MemoryQuery) ([]domain.Memory, error) {
	return m.svc.Inject(ctx, q.Agent, q.ConversationID, q.TaskID, q.Limit)
}

// InjectWithSnapshot 把 memory.ContextRequest 映射为 core 侧 DTO（core 不 import memory）。
func (m coreMemoryInjector) InjectWithSnapshot(ctx context.Context, q core.MemoryQuery) (*core.MemorySnapshot, error) {
	snap, err := m.svc.InjectWithSnapshot(ctx, memory.InjectionRequest{
		TurnID:               q.TurnID,
		AgentID:              q.Agent.ID,
		AgentKind:            q.Agent.Kind,
		DepartmentID:         q.Agent.DepartmentID,
		TaskID:               q.TaskID,
		ConversationID:       q.ConversationID,
		Model:                q.Model,
		TaskBrief:            q.TaskBrief,
		EffectiveInputTokens: q.EffectiveInputTokens,
		RecentMessages:       q.RecentMessages,
		RecentTokens:         q.RecentTokens,
		ManualScope:          q.ManualScope,
		IncludeManual:        q.IncludeManual,
		IncludeArchives:      q.IncludeArchives,
	})
	if err != nil {
		return nil, err
	}
	out := &core.MemorySnapshot{
		Estimated:     snap.Estimated,
		Degraded:      snap.Degraded,
		DegradeReason: snap.DegradeReason,
		CreatedAt:     snap.CreatedAt,
	}
	for _, entry := range snap.Memories {
		out.Entries = append(out.Entries, core.MemoryEntrySnapshot{
			ID: entry.ID, EntityType: entry.EntityType, EntityKey: entry.EntityKey,
			Version: entry.Version, Summary: entry.Summary, Source: entry.Source,
			Visibility: entry.Visibility, Confidence: entry.Confidence,
			Pinned: entry.Pinned, Tokens: entry.Tokens, Reason: entry.Reason,
		})
		out.Tokens += entry.Tokens
	}
	for _, dropped := range snap.Dropped {
		out.Dropped = append(out.Dropped, core.MemoryDroppedSnapshot{
			ID: dropped.ID, EntityType: dropped.EntityType, EntityKey: dropped.EntityKey,
			Reason: dropped.Reason, Tokens: dropped.Tokens,
		})
	}
	if snap.Manual != nil {
		out.ManualVersion = snap.Manual.Version
		out.ManualTokens = snap.Manual.Tokens
	}
	if snap.Task != nil {
		out.TaskTokens = snap.Task.Tokens
	}
	return out, nil
}

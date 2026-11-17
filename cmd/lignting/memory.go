package main

import (
	"context"

	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/memory"
)

// coreMemoryInjector 把 memory.Service 适配成 core.MemoryInjector。
// core 目前只取 []domain.Memory；context.request 快照由 memory.InjectWithSnapshot 提供，
// R7 再把 core 的注入接口升级为快照版本。
type coreMemoryInjector struct{ svc *memory.Service }

func (m coreMemoryInjector) Inject(ctx context.Context, q core.MemoryQuery) ([]domain.Memory, error) {
	return m.svc.Inject(ctx, q.Agent, q.ConversationID, q.TaskID, q.Limit)
}

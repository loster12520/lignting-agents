package memory

import (
	"context"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Inject 是给 core 现有窄接口 core.MemoryInjector 准备的适配方法。
//
// core 侧只需 3 行（core 已 import memory，方向是 core→memory，不会成环）：
//
//	type injectAdapter struct{ svc *memory.Service }
//
//	func (a injectAdapter) Inject(ctx context.Context, q core.MemoryQuery) ([]domain.Memory, error) {
//		return a.svc.Inject(ctx, q.Agent, q.ConversationID, q.TaskID, q.Limit)
//	}
//
// 语义：
//   - 只返回本次允许注入的档案；content 被替换为「本次可注入摘要」（已脱敏 + 按渲染上限截断），
//     完整档案必须走 memory.read 工具（带权限校验与读痕），避免 core 直接把全文塞进提示词。
//   - 权限、可见性、预算、降级全部复用 Assemble；human-only 永不出现。
//   - limit > 0 时最多返回 limit 条（core 的旧接口字段）。
//
// 需要 context.request 快照 / 「本次用了哪些记忆」时，请改用 InjectWithSnapshot
// （core 的接口应升级为返回 *memory.ContextRequest，R7 联调时由 core-dev 改 ports.go）。
func (s *Service) Inject(ctx context.Context, agent domain.Agent, conversationID, taskID string, limit int) ([]domain.Memory, error) {
	req := InjectionRequest{
		AgentID:         agent.ID,
		AgentKind:       agent.Kind,
		DepartmentID:    agent.DepartmentID,
		TaskID:          taskID,
		ConversationID:  conversationID,
		IncludeArchives: true,
	}
	snap, err := s.Assemble(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Memory, 0, len(snap.Memories))
	for _, injected := range snap.Memories {
		m, gerr := s.repo.GetMemory(ctx, injected.ID)
		if gerr != nil {
			continue // 注入期间被删除/不可读：跳过而不是让整次装配失败。
		}
		// 只把摘要交给 core；同时把来源贴到 tags，便于前端展示「为什么注入这条」。
		m.Content = injected.Summary
		m.Summary = injected.Summary
		m.Tags = JoinTags(append(SplitTags(m.Tags), "injected", "src:"+injected.Source))
		out = append(out, m)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// InjectWithSnapshot 是推荐给 core 的新接口：返回完整 ContextRequest（含来源、token 估算、
// 预算口径、降级原因），core 负责把它写进 context_snapshots 并在前端展示「本次回答用了哪些记忆」。
func (s *Service) InjectWithSnapshot(ctx context.Context, req InjectionRequest) (*ContextRequest, error) {
	return s.Assemble(ctx, req)
}

// ManualLayers 返回当前已批准的管理手册分层（L4）。core 用 layers.Render() 拼进 system 前缀，
// 并按 version 写进 context.request 以便追溯。没有已批准版本时 found=false，core 只放角色卡。
func (s *Service) ManualLayers(ctx context.Context, agent domain.Agent, roleCard string) (layer SystemLayers, version int, found bool, err error) {
	scope := domain.MemoryScopeAssistant
	switch agent.Kind {
	case ActorSupervisor:
		scope = domain.MemoryScopeDepartment
	case ActorEmployee:
		scope = domain.MemoryScopeEmployee
	}
	key := ManualKey{Scope: scope, DepartmentID: agent.DepartmentID}
	if scope == domain.MemoryScopeAssistant || scope == domain.MemoryScopeEmployee {
		key.AgentID = agent.ID
	}
	m, ok, qerr := s.CurrentApprovedManual(ctx, key)
	if qerr != nil {
		return SystemLayers{}, 0, false, qerr
	}
	if !ok {
		return ComposeSystem(roleCard, ""), 0, false, nil
	}
	return ComposeSystem(roleCard, m.Content), m.Version, true, nil
}

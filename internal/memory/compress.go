package memory

import (
	"context"
	"errors"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// compressionJob 是待压缩条目（内存队列，进程重启丢失；持久化由 store 的“超限枚举”兜底）。
type compressionJob struct {
	MemoryID    string
	Reason      string
	TargetChars int
	QueuedAt    string
	Attempts    int
}

// 压缩相关的最小/最大目标字数（FR-MEM-06：低于 200 必然丢关键事实）。
const (
	minCompressTarget = 200
	// 正文超过该长度时只提供首尾，要求实现按 outline 再分段读回，避免编造中间内容。
	pagingThresholdChars = 60000
	pagingHeadChars      = 30000
	pagingTailChars      = 20000
)

// enqueueCompression 把档案放入待压缩队列；同 ID 去重，队列满则丢弃并告警。
func (s *Service) enqueueCompression(id, reason string, target int) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.pending {
		if j.MemoryID == id {
			return false
		}
	}
	if len(s.pending) >= s.cfg.CompressMaxPending {
		s.log.Warn("memory: compression queue full", "memory_id", id, "limit", s.cfg.CompressMaxPending)
		return false
	}
	s.pending = append(s.pending, compressionJob{
		MemoryID:    id,
		Reason:      reason,
		TargetChars: target,
		QueuedAt:    s.stamp(),
	})
	return true
}

// PendingCompressions 返回待压缩队列快照（观测/管理界面用）。
func (s *Service) PendingCompressions() []compressionJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]compressionJob, len(s.pending))
	copy(out, s.pending)
	return out
}

func (s *Service) popCompression() (compressionJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return compressionJob{}, false
	}
	j := s.pending[0]
	s.pending = s.pending[1:]
	return j, true
}

// RunPendingCompressions 串行处理待压缩队列，返回成功/失败数；模型不可用时立即停止，
// 队列保留到下次运行（FR-MEM-11：失败不丢、不阻塞对话）。
func (s *Service) RunPendingCompressions(ctx context.Context, max int) (done, failed int) {
	if max <= 0 {
		max = s.cfg.CompressMaxPending
	}
	actor := Access{Type: ActorSystem, ID: "memory"}
	for i := 0; i < max; i++ {
		job, ok := s.popCompression()
		if !ok {
			break
		}
		if _, err := s.Compress(ctx, job.MemoryID, job.TargetChars, job.Reason, actor); err != nil {
			failed++
			if errors.Is(err, ErrModelUnavailable) {
				// 模型不可用：把当前任务放回队列尾部，停止本轮。
				s.enqueueCompression(job.MemoryID, job.Reason, job.TargetChars)
				break
			}
			continue
		}
		done++
	}
	return done, failed
}

// Compress 把一条档案压到目标字数。语义（FR-MEM-06）：
//   - 单飞 + 冷却；压缩前写快照事件；只接受完整新正文；
//   - 结果为空或仍超目标 → 不落库（原文不变），记失败退避；
//   - 成功才写回，写回走乐观锁；失败绝不静默留半成品。
func (s *Service) Compress(ctx context.Context, id string, target int, reason string, actor Access) (domain.Memory, error) {
	if !s.cfg.Enabled {
		return domain.Memory{}, domain.NewError(CodeMemoryDisabled, "记忆层已关闭", 503)
	}
	if s.compressor == nil {
		return domain.Memory{}, ErrModelUnavailable
	}
	key := "compress:" + id
	if !s.gate.TryLock(key) {
		return domain.Memory{}, ErrInFlight
	}
	defer s.gate.Unlock(key)
	if s.gate.Cooling(key) {
		return domain.Memory{}, ErrCooldown
	}

	m, err := s.getRaw(ctx, id)
	if err != nil {
		return domain.Memory{}, err
	}
	if IsTombstone(m) {
		return domain.Memory{}, notFoundError("记忆不存在")
	}
	if target <= 0 {
		target = s.cfg.CompressTargetChars
	}
	if target < minCompressTarget {
		target = minCompressTarget
	}
	if target > s.cfg.StorageMaxChars && s.cfg.StorageMaxChars > 0 {
		target = s.cfg.StorageMaxChars
	}
	before := RuneLen(m.Content)
	if before <= target {
		s.gate.Succeed(key)
		return m, nil // no-op：零 token
	}
	if reason == "" {
		reason = "manual"
	}

	// 压缩前快照：原文、版本、字数、原因全部进 memory_events.detail（不新增表）。
	s.event(ctx, m.ID, ActionSnapshot, actor, map[string]any{
		"reason":       reason,
		"prev_version": m.Version,
		"prev_chars":   before,
		"target_chars": target,
		"prev_content": m.Content,
	})

	req := CompressionRequest{
		MemoryID:    m.ID,
		EntityType:  m.EntityType,
		EntityKey:   m.EntityKey,
		Content:     m.Content,
		TargetChars: target,
	}
	if before > pagingThresholdChars {
		r := []rune(m.Content)
		req.RequiresPaging = true
		req.Head = string(r[:pagingHeadChars])
		req.Tail = string(r[len(r)-pagingTailChars:])
		req.Content = req.Head + "\n...(中间省略，需分段读回)...\n" + req.Tail
	}

	res, cerr := s.compressor.Compress(ctx, req)
	if cerr != nil {
		s.gate.Fail(key)
		s.event(ctx, m.ID, ActionCompressFailed, actor, map[string]any{
			"reason": reason, "error": cerr.Error(), "prev_chars": before,
		})
		if errors.Is(cerr, ErrModelUnavailable) {
			return domain.Memory{}, ErrModelUnavailable
		}
		return domain.Memory{}, cerr
	}
	clean := strings.TrimSpace(res.Content)
	after := RuneLen(clean)
	if clean == "" || after == 0 {
		s.gate.Fail(key)
		s.event(ctx, m.ID, ActionCompressRejected, actor, map[string]any{
			"reason": "empty_result", "prev_chars": before, "target_chars": target,
		})
		return domain.Memory{}, domain.NewError(domain.CodeInvalid, "压缩结果为空，已保留原文", 502)
	}
	if after > target {
		s.gate.Fail(key)
		s.event(ctx, m.ID, ActionCompressRejected, actor, map[string]any{
			"reason": "over_target", "got_chars": after, "target_chars": target, "prev_chars": before,
		})
		return domain.Memory{}, domain.NewError(domain.CodeInvalid, "压缩后仍超目标字数，已保留原文", 502)
	}

	updated := m
	updated.Content = clean
	updated.UpdatedAt = s.stamp()
	if s.cfg.IsExemptEntity(updated.EntityType) {
		// 内部表不参与容量治理。
	} else if RuneLen(updated.Summary) > s.cfg.RenderSummaryMaxChars {
		updated.Summary, _ = TruncateRunes(updated.Summary, s.cfg.RenderSummaryMaxChars)
	}
	if err := s.repo.UpdateMemory(ctx, updated, m.Version); err != nil {
		if errors.Is(err, ErrConflict) {
			s.event(ctx, m.ID, ActionCompressFailed, actor, map[string]any{"reason": "version_conflict"})
			return domain.Memory{}, conflictError("压缩期间档案被修改，已放弃本次压缩", map[string]any{"memory_id": m.ID})
		}
		s.event(ctx, m.ID, ActionCompressFailed, actor, map[string]any{"reason": "write_failed", "error": err.Error()})
		return domain.Memory{}, err
	}
	s.gate.Succeed(key)
	s.event(ctx, m.ID, ActionCompressed, actor, map[string]any{
		"reason": reason, "before_chars": before, "after_chars": after,
		"target_chars": target, "model": res.Model, "notes": res.Notes,
	})
	return updated, nil
}

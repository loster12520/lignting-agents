package memory

import (
	"context"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// 额度窗口（FR-MEM-04：每任务/每日写入额度可配置）。
const (
	WindowTask  = "per_task"
	WindowDay   = "per_day"
	WindowAgent = "per_agent"
)

func quotaError(window string, limit, used int) *domain.Error {
	e := domain.NewError(CodeQuotaExceeded, "记忆写入超出额度", 429)
	e.Details = map[string]any{"window": window, "limit": limit, "used": used}
	return e
}

// dayStart 返回 UTC 当日零点的 RFC3339 字符串，供仓储做时间下界过滤。
func dayStart(t time.Time) string {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
}

// checkQuota 检查候选是否超额度。口径：
//   - 每任务：同 task 标签的候选（含被拒，防刷）≤ CandidateMaxPerTask；
//   - 每日：同一提交者当日新建 ≤ CandidateMaxPerDay；
//   - 每 Agent 累计：同一 agent 的候选总量 ≤ CandidateMaxPerAgent。
//
// 统计失败按“放行 + 告警”处理：额度是治理手段，不能因计数故障阻塞主流程。
func (s *Service) checkQuota(ctx context.Context, c Candidate) error {
	if c.SourceTaskID != "" && s.cfg.CandidateMaxPerTask > 0 {
		used, err := s.repo.CountMemories(ctx, Filter{
			Tags: []string{TaskTag(c.SourceTaskID)},
		})
		if err != nil {
			s.log.Warn("memory: quota count failed", "window", WindowTask, "error", err)
		} else if used >= s.cfg.CandidateMaxPerTask {
			return quotaError(WindowTask, s.cfg.CandidateMaxPerTask, used)
		}
	}
	if s.cfg.CandidateMaxPerDay > 0 && c.CreatedBy != "" {
		used, err := s.repo.CountMemories(ctx, Filter{
			CreatedBy:    c.CreatedBy,
			CreatedSince: dayStart(s.now()),
		})
		if err != nil {
			s.log.Warn("memory: quota count failed", "window", WindowDay, "error", err)
		} else if used >= s.cfg.CandidateMaxPerDay {
			return quotaError(WindowDay, s.cfg.CandidateMaxPerDay, used)
		}
	}
	if s.cfg.CandidateMaxPerAgent > 0 && c.AgentID != "" {
		used, err := s.repo.CountMemories(ctx, Filter{AgentID: c.AgentID})
		if err != nil {
			s.log.Warn("memory: quota count failed", "window", WindowAgent, "error", err)
		} else if used >= s.cfg.CandidateMaxPerAgent {
			return quotaError(WindowAgent, s.cfg.CandidateMaxPerAgent, used)
		}
	}
	return nil
}

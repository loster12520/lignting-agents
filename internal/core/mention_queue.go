package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// DefaultMentionBatchLimit 是单轮 @ 触发 Agent 数上限（避免刷屏）。
const DefaultMentionBatchLimit = 2

// MentionQueueStatus 是多 Agent @ 发言队列的可观测状态（platform DTO）。
type MentionQueueStatus struct {
	ConversationID string `json:"conversation_id"`
	Running        bool   `json:"running"`
	PendingJobs    int    `json:"pending_jobs"`
	CurrentAgentID string `json:"current_agent_id,omitempty"`
	Limit          int    `json:"limit"`
	Stopped        bool   `json:"stopped"`
}

type mentionJob struct {
	message domain.Message
	agents  []domain.Agent
}

type mentionQueue struct {
	mu      sync.Mutex
	jobs    []mentionJob
	running bool
	stopped bool
	current string
	cancel  context.CancelFunc
}

// parseMentions 解析用户消息 meta 中的 mentions（agent ID 数组，保序去重）。
func parseMentions(meta string) []string {
	var parsed struct {
		Mentions   []string `json:"mentions"`
		MentionIDs []string `json:"mention_agent_ids"`
	}
	if err := json.Unmarshal([]byte(meta), &parsed); err != nil {
		return nil
	}
	raw := parsed.Mentions
	if len(raw) == 0 {
		raw = parsed.MentionIDs
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// resolveMentionAgents 按 @ 顺序解析可触发的 Agent（保留顺序，过滤 paused/部门暂停/无权限）。
func (rt *Runtime) resolveMentionAgents(ctx context.Context, conv domain.Conversation, members []domain.ConversationMember, mentionIDs []string) ([]domain.Agent, error) {
	limit := rt.opts.MentionBatchLimit
	if limit <= 0 {
		limit = DefaultMentionBatchLimit
	}
	var out []domain.Agent
	for _, id := range mentionIDs {
		if len(out) >= limit {
			break
		}
		agent, err := rt.opts.Store.GetAgent(ctx, id)
		if err != nil {
			continue
		}
		if agent.Status == domain.AgentPaused || agent.Status == domain.AgentOffline {
			continue
		}
		if agent.DepartmentID != "" {
			if dept, derr := rt.opts.Store.GetDepartment(ctx, agent.DepartmentID); derr == nil && (dept.Status == domain.DeptPaused || dept.Status == domain.DeptArchived) {
				continue
			}
		}
		if err := rt.opts.Policy.AuthorizeParticipation(ctx, agent, conv, members); err != nil {
			continue
		}
		out = append(out, agent)
	}
	return out, nil
}

// enqueueMention 把一条消息的 @ 任务放入会话队列；同一会话串行，多 Agent 顺序执行。
func (rt *Runtime) enqueueMention(conversationID string, job mentionJob) {
	rt.mentionMu.Lock()
	q := rt.mentionQueues[conversationID]
	if q == nil || q.stopped {
		q = &mentionQueue{}
		rt.mentionQueues[conversationID] = q
	}
	q.mu.Lock()
	q.jobs = append(q.jobs, job)
	start := !q.running
	q.running = true
	q.mu.Unlock()
	rt.mentionMu.Unlock()
	if start {
		go rt.runMentionQueue(conversationID, q)
	}
}

func (rt *Runtime) runMentionQueue(conversationID string, q *mentionQueue) {
	for {
		q.mu.Lock()
		if q.stopped || len(q.jobs) == 0 {
			q.running = false
			q.current = ""
			q.mu.Unlock()
			rt.mentionMu.Lock()
			if rt.mentionQueues[conversationID] == q {
				delete(rt.mentionQueues, conversationID)
			}
			rt.mentionMu.Unlock()
			return
		}
		job := q.jobs[0]
		q.jobs = q.jobs[1:]
		ctx, cancel := context.WithCancel(context.Background())
		q.cancel = cancel
		if len(job.agents) > 0 {
			q.current = job.agents[0].ID
		}
		q.mu.Unlock()

		for _, agent := range job.agents {
			if q.isStopped() {
				break
			}
			q.setCurrent(agent.ID)
			if err := rt.runnerFor(agent).submitAndWait(ctx, inbound{message: job.message}); err != nil {
				break
			}
		}
		cancel()
		q.mu.Lock()
		q.cancel = nil
		q.current = ""
		q.mu.Unlock()
	}
}

func (q *mentionQueue) isStopped() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.stopped
}

func (q *mentionQueue) setCurrent(agentID string) {
	q.mu.Lock()
	q.current = agentID
	q.mu.Unlock()
}

// MentionQueueStatusFor 返回某会话 @ 队列状态（platform 可见/可停止）。
func (rt *Runtime) MentionQueueStatusFor(conversationID string) MentionQueueStatus {
	limit := rt.opts.MentionBatchLimit
	if limit <= 0 {
		limit = DefaultMentionBatchLimit
	}
	status := MentionQueueStatus{ConversationID: conversationID, Limit: limit}
	rt.mentionMu.Lock()
	q := rt.mentionQueues[conversationID]
	rt.mentionMu.Unlock()
	if q == nil {
		return status
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	status.Running = q.running
	status.PendingJobs = len(q.jobs)
	status.CurrentAgentID = q.current
	status.Stopped = q.stopped
	return status
}

// StopMentionQueue 停止某会话 @ 队列：丢弃未开始的任务，并取消当前等待；
// 已在执行的 Agent turn 不强制中断（需要时可再调 Runtime.Interrupt）。
func (rt *Runtime) StopMentionQueue(conversationID string) int {
	rt.mentionMu.Lock()
	q := rt.mentionQueues[conversationID]
	rt.mentionMu.Unlock()
	if q == nil {
		return 0
	}
	q.mu.Lock()
	dropped := len(q.jobs)
	q.jobs = nil
	q.stopped = true
	cancel := q.cancel
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return dropped
}

// WaitMentionQueueIdle 等待某会话 @ 队列清空（测试/关闭时用）。
func (rt *Runtime) WaitMentionQueueIdle(ctx context.Context, conversationID string) error {
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		status := rt.MentionQueueStatusFor(conversationID)
		if !status.Running && status.PendingJobs == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// mentionLimitError 是 @ 超限时的可读提示（不阻塞消息，只截断触发）。
func mentionLimitError(limit int) error {
	return fmt.Errorf("core: @ 触发超过单轮上限 %d，已截断", limit)
}

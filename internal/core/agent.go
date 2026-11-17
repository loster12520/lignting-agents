package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// inbound 是一条进入 mailbox 的用户消息。done 非空时在 turn 结束后关闭（@ 队列串行等待用）。
type inbound struct {
	message domain.Message
	done    chan struct{}
}

// runner 是单个 Agent 的串行 mailbox：一次只跑一个 turn，其余排队。
type runner struct {
	rt    *Runtime
	agent domain.Agent

	in   chan inbound
	stop chan struct{}
	done chan struct{}

	pending int32

	mu     sync.Mutex
	cancel context.CancelFunc
	last   *inbound
}

func newRunner(rt *Runtime, agent domain.Agent) *runner {
	r := &runner{
		rt:    rt,
		agent: agent,
		in:    make(chan inbound, 16),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go r.loop()
	return r
}

func (r *runner) loop() {
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return
		case in := <-r.in:
			r.runOne(in)
		}
	}
}

func (r *runner) submit(in inbound) error {
	atomic.AddInt32(&r.pending, 1)
	select {
	case <-r.stop:
		atomic.AddInt32(&r.pending, -1)
		return fmt.Errorf("agent %s 已停止", r.agent.Name)
	default:
	}
	select {
	case r.in <- in:
		return nil
	default:
		atomic.AddInt32(&r.pending, -1)
		return fmt.Errorf("agent %s 的 mailbox 已满（16），请稍后重试", r.agent.Name)
	}
}

func (r *runner) runOne(in inbound) {
	if in.done != nil {
		defer close(in.done)
	}
	defer atomic.AddInt32(&r.pending, -1)
	ctx, cancel := context.WithTimeout(context.Background(), r.rt.opts.TurnTimeout)
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()

	r.rt.updateStatus(ctx, r.agent, domain.AgentRunning, "turn started")
	result := r.rt.executeTurn(ctx, r.agent, in.message)
	cancel()

	r.mu.Lock()
	r.cancel = nil
	if result.Err != nil {
		copied := in
		r.last = &copied
	}
	r.mu.Unlock()
}

// submitAndWait 提交一条消息并等待该 turn 结束（@ 队列串行执行用）。
func (r *runner) submitAndWait(ctx context.Context, in inbound) error {
	done := make(chan struct{})
	in.done = done
	if err := r.submit(in); err != nil {
		return err
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return fmt.Errorf("agent %s 已停止", r.agent.Name)
	}
}

func (r *runner) close() {
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
	}
}

func (r *runner) idle() bool { return atomic.LoadInt32(&r.pending) == 0 }

func (r *runner) interrupt() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel == nil {
		return false
	}
	r.cancel()
	return true
}

func (r *runner) retryLast() error {
	r.mu.Lock()
	last := r.last
	r.mu.Unlock()
	if last == nil {
		return errors.New("core: 没有可重试的输入")
	}
	return r.submit(*last)
}

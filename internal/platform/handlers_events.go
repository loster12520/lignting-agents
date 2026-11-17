package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// eventBroker 唤醒所有 SSE 连接。Notify 关闭当前 channel 并换新，实现广播。
type eventBroker struct {
	mu sync.Mutex
	ch chan struct{}
}

func newEventBroker() *eventBroker {
	return &eventBroker{ch: make(chan struct{})}
}

func (b *eventBroker) Notify() {
	b.mu.Lock()
	close(b.ch)
	b.ch = make(chan struct{})
	b.mu.Unlock()
}

func (b *eventBroker) Wait() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ch
}

func parseSince(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("since"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid since")
	}
	return v, nil
}

// handleEvents 是 SSE /api/events?since=<seq>：
// 先补发 seq > since 的历史事件（断线补齐），之后实时广播；20s 心跳。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	since, err := parseSince(r)
	if err != nil {
		s.writeErr(w, errInvalid("since 必须是 >=0 的整数"))
		return
	}
	conversationID := r.URL.Query().Get("conversation_id")
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeErr(w, domain.NewError(domain.CodeUnavailable, "当前连接不支持流式推送", http.StatusServiceUnavailable))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	last := since
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	for {
		newLast, err := s.flushEvents(w, r, conversationID, last)
		if err != nil {
			return
		}
		if newLast != last {
			last = newLast
			flusher.Flush()
		}
		wait := s.broker.Wait()
		select {
		case <-r.Context().Done():
			return
		case <-wait:
		case <-poll.C:
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) flushEvents(w http.ResponseWriter, r *http.Request, conversationID string, after int64) (int64, error) {
	last := after
	for {
		events, err := s.store.ListEventsSince(r.Context(), last, conversationID, 500)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return last, err // 客户端断开，不记错误日志
			}
			s.logf("读取事件失败: %v", err)
			return last, err
		}
		if len(events) == 0 {
			return last, nil
		}
		for _, ev := range events {
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, data); err != nil {
				return last, err
			}
			last = ev.Seq
		}
		if len(events) < 500 {
			return last, nil
		}
	}
}

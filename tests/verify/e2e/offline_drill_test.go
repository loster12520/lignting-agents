package verify_e2e

// R9 断线补齐演练（真实二进制 + 真实 HTTP/SSE 黑盒）。
//
// 目的：发布清单里的「1 小时断线演练」等价验证——客户端在断线窗口内完全不读 SSE，
// 服务端持续产生事件；重连时带 since=baseline 补齐，断言补齐的事件「不重、不漏」。
//
// 为什么不等真 1 小时：补发逻辑只按 events.seq 游标切片，与墙上时间无关（没有按时间的清理/TTL），
// 决定补发正确性的是断线期间的事件条数。演练默认灌 3600 条事件 ≈ 1 小时 @1 条/秒的写入量，
// 几十秒内跑完；真实浏览器断网 1 小时的人工项仍需发布前人工做（见 release-checklist）。
//
// 用法：
//   go test ./tests/verify/e2e -run TestR9OfflineCatchUpDrill -v
//   DRILL_EVENTS=7200 go test ./tests/verify/e2e -run TestR9OfflineCatchUpDrill -v   # 加倍压力

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type drillEvent struct {
	ID   int64
	Type string
	Data json.RawMessage
}

func drillEventCount(t *testing.T) int {
	t.Helper()
	raw := os.Getenv("DRILL_EVENTS")
	if raw == "" {
		return 3600
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("DRILL_EVENTS 必须是正整数，得到 %q", raw)
	}
	return n
}

// readDrillSSE 连接 /api/events?since=<since>，逐条回调事件，直到 fn 返回 true 或 ctx 取消。
func readDrillSSE(ctx context.Context, a *api, since int64, fn func(drillEvent) bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		a.base+fmt.Sprintf("/api/events?since=%d", since), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Origin", a.base)
	req.Header.Set("Accept", "text/event-stream")
	// SSE 是长连接，不能复用 api 客户端 30s 超时；共享 cookie jar 保持会话。
	client := &http.Client{Jar: a.hc.Jar}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("events status=%d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var (
		cur  drillEvent
		data []byte
	)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.Type != "" || len(data) > 0 {
				cur.Data = append(json.RawMessage(nil), data...)
				if fn(cur) {
					return nil
				}
			}
			cur, data = drillEvent{}, data[:0]
		case strings.HasPrefix(line, "id: "):
			if n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id: ")), 10, 64); err == nil {
				cur.ID = n
			}
		case strings.HasPrefix(line, "event: "):
			cur.Type = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			data = append(data, []byte(strings.TrimPrefix(line, "data: "))...)
		}
	}
	return sc.Err()
}

type drillResult struct {
	ids        []int64
	contiguous bool
	dup        bool
	drillMsgs  int
	lastID     int64
	err        error
}

// TestR9OfflineCatchUpDrill 演练：断线期间产生大量事件 → 重连 since 补齐 → 不重不漏。
func TestR9OfflineCatchUpDrill(t *testing.T) {
	s := startServer(t)
	a := s.client(t)
	s.setup(a, "阿哲")

	conv := a.ok(http.MethodPost, "/api/conversations", map[string]any{
		"type": "project", "title": "断线补齐演练", "visibility": "project",
	}, nil)
	convID := str(conv, "id")
	if convID == "" {
		t.Fatalf("建会话失败: %v", conv)
	}
	post := func(content, clientID string) {
		a.ok(http.MethodPost, "/api/conversations/"+convID+"/messages",
			map[string]any{"content": content, "client_id": clientID}, nil)
	}

	// 1) 在线：读到第一条事件即当作 baseline，然后主动断开（模拟浏览器关页/断网）。
	post("演练开始", "drill-start")
	baselineCh := make(chan int64, 1)
	baselineCtx, baselineCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer baselineCancel()
	go func() {
		var first int64
		err := readDrillSSE(baselineCtx, a, 0, func(ev drillEvent) bool {
			first = ev.ID
			return true
		})
		if err != nil && first == 0 {
			t.Logf("基线 SSE 连接: %v", err)
		}
		baselineCh <- first
	}()
	var baseline int64
	select {
	case baseline = <-baselineCh:
	case <-time.After(20 * time.Second):
		t.Fatal("20s 内没从 SSE 读到基线事件")
	}
	if baseline <= 0 {
		t.Fatalf("baseline 游标无效: %d", baseline)
	}
	t.Logf("基线事件 id=%d；进入断线窗口（此后不再读 SSE）", baseline)

	// 2) 断线窗口：客户端不读 SSE，服务端持续写事件（等价 1h@1/s 的量级）。
	n := drillEventCount(t)
	drillStart := time.Now()
	for i := 0; i < n; i++ {
		post(fmt.Sprintf("断线期间消息 #%05d", i), fmt.Sprintf("drill-%05d", i))
	}
	post("演练结束", "drill-end")
	t.Logf("断线窗口写入 %d 条消息（另有起止标记），耗时 %s", n, time.Since(drillStart))

	// 3) 重连并补齐：since=baseline，读到「演练结束」标记为止。
	replayStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	done := make(chan drillResult, 1)
	go func() {
		res := drillResult{contiguous: true, lastID: baseline}
		got := make(map[int64]bool, n+2)
		res.err = readDrillSSE(ctx, a, baseline, func(ev drillEvent) bool {
			if got[ev.ID] {
				res.dup = true
			}
			got[ev.ID] = true
			res.ids = append(res.ids, ev.ID)
			// SSE id 即 events.seq，全局单调；补齐必须严格 +1，缺一条就是漏。
			if ev.ID != res.lastID+1 {
				res.contiguous = false
			}
			res.lastID = ev.ID
			if isDrillMsg(ev, "断线期间消息") {
				res.drillMsgs++
			}
			return isDrillMsg(ev, "演练结束")
		})
		done <- res
	}()

	var res drillResult
	select {
	case res = <-done:
		if res.err != nil {
			t.Fatalf("补齐 SSE 读取失败: %v", res.err)
		}
	case <-ctx.Done():
		t.Fatalf("120s 内没补齐到结束标记")
	}
	t.Logf("补齐 %d 条事件，最后 id=%d（baseline=%d），耗时 %s；断线期间消息命中 %d/%d",
		len(res.ids), res.lastID, baseline, time.Since(replayStart), res.drillMsgs, n)

	if res.dup {
		t.Errorf("补齐出现重复事件 id")
	}
	if !res.contiguous {
		t.Errorf("补齐的事件 id 不连续（有漏事件）：共 %d 条，首=%d 末=%d", len(res.ids), res.ids[0], res.lastID)
	}
	if res.drillMsgs != n {
		t.Errorf("断线期间消息补齐 %d 条，want %d", res.drillMsgs, n)
	}
	// 断线期间产生的事件 = n 条断线消息 + 1 条结束标记，全部要在补齐里。
	if len(res.ids) < n+1 {
		t.Errorf("补齐事件总数 %d < %d", len(res.ids), n+1)
	}
}

func isDrillMsg(ev drillEvent, needle string) bool {
	return ev.Type == "message.created" && strings.Contains(string(ev.Data), needle)
}

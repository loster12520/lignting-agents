package memory

import (
	"sync"
	"time"
)

// gate 提供两件事：键级单飞锁与失败冷却（FR-MEM-11）。
//   - 单飞：同一实体/键同时只允许一个写入或压缩；拿不到立刻返回，不排队（避免请求堆积）。
//   - 冷却：失败后指数退避，冷却期内拒绝重复尝试；成功即清零。
type gate struct {
	mu      sync.Mutex
	busy    map[string]bool
	fails   map[string]*failureState
	now     Clock
	base    time.Duration
	maxWait time.Duration
}

type failureState struct {
	count int
	until time.Time
}

func newGate(clock Clock, base, maxWait time.Duration) *gate {
	if clock == nil {
		clock = SystemClock{}
	}
	if base <= 0 {
		base = 15 * time.Second
	}
	if maxWait <= 0 {
		maxWait = 10 * time.Minute
	}
	return &gate{busy: map[string]bool{}, fails: map[string]*failureState{}, now: clock, base: base, maxWait: maxWait}
}

// TryLock 立即尝试占用 key；已占用返回 false（调用方应返回 ErrInFlight）。
func (g *gate) TryLock(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.busy[key] {
		return false
	}
	g.busy[key] = true
	return true
}

// Unlock 释放 key。
func (g *gate) Unlock(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.busy, key)
}

// Cooling 报告 key 是否在冷却期内。
func (g *gate) Cooling(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	st, ok := g.fails[key]
	if !ok {
		return false
	}
	return g.now.Now().Before(st.until)
}

// Fail 记录一次失败并按指数退避设置冷却，返回冷却截止时间。
func (g *gate) Fail(key string) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	st, ok := g.fails[key]
	if !ok {
		st = &failureState{}
		g.fails[key] = st
	}
	st.count++
	d := g.base
	for i := 1; i < st.count && d < g.maxWait; i++ {
		d *= 2
	}
	if d > g.maxWait {
		d = g.maxWait
	}
	st.until = g.now.Now().Add(d)
	return st.until
}

// Succeed 清除冷却与失败计数。
func (g *gate) Succeed(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.fails, key)
}

// Failures 返回累计失败次数（观测用）。
func (g *gate) Failures(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if st, ok := g.fails[key]; ok {
		return st.count
	}
	return 0
}

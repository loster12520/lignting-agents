//go:build perf

package bench

import (
	"context"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/search"
	"github.com/lignting/lignting-agents/internal/store"
)

const totalMessages = 100000

type querySpec struct {
	name   string
	q      string
	filter search.Filter
	limit  time.Duration // p95 阈值
}

func openBenchStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/bench.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func measure(t *testing.T, st *store.Store, spec querySpec, warmRuns int) (p50, p95 time.Duration) {
	t.Helper()
	ctx := context.Background()
	// warm
	for i := 0; i < 5; i++ {
		if _, err := search.Search(ctx, st.DB, spec.q, spec.filter); err != nil {
			t.Fatalf("%s warm: %v", spec.name, err)
		}
	}
	ds := make([]time.Duration, 0, warmRuns)
	for i := 0; i < warmRuns; i++ {
		start := time.Now()
		rs, err := search.Search(ctx, st.DB, spec.q, spec.filter)
		if err != nil {
			t.Fatalf("%s: %v", spec.name, err)
		}
		ds = append(ds, time.Since(start))
		if i == 0 {
			t.Logf("%s: total=%d first-page=%d", spec.name, rs.Total, len(rs.Items))
		}
	}
	return percentile(ds, 0.50), percentile(ds, 0.95)
}

func TestSearchPerf100k(t *testing.T) {
	if testing.Short() {
		t.Skip("perf test")
	}
	ctx := context.Background()
	st := openBenchStore(t)
	seedStart := time.Now()
	stats, err := Seed(ctx, st, totalMessages)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Logf("seed: messages=%d terms=%d conversations=%d elapsed=%s", stats.Messages, stats.Terms, len(stats.Conversations), time.Since(seedStart))

	specs := []querySpec{
		{name: "中文2字(限流)", q: "限流", filter: search.Filter{}, limit: 150 * time.Millisecond},
		{name: "中文2字(登录)", q: "登录", filter: search.Filter{}, limit: 150 * time.Millisecond},
		{name: "中文3字(限流策略)", q: "限流策略", filter: search.Filter{}, limit: 150 * time.Millisecond},
		{name: "中文长词(部署流水线)", q: "部署流水线", filter: search.Filter{}, limit: 300 * time.Millisecond},
		{name: "英文(login)", q: "login", filter: search.Filter{}, limit: 300 * time.Millisecond},
		{name: "英文大写(LOGIN)", q: "LOGIN", filter: search.Filter{}, limit: 300 * time.Millisecond},
		{name: "代码标识符(getUserById)", q: "getUserById", filter: search.Filter{}, limit: 300 * time.Millisecond},
		{name: "多词(限流 login)", q: "限流 login", filter: search.Filter{}, limit: 300 * time.Millisecond},
		{name: "过滤会话", q: "登录", filter: search.Filter{ConversationID: stats.Conversations[3]}, limit: 150 * time.Millisecond},
		{name: "过滤部门", q: "登录", filter: search.Filter{DepartmentID: "dept_bench_1"}, limit: 150 * time.Millisecond},
		{name: "过滤作者", q: "登录", filter: search.Filter{SenderID: store.OwnerID}, limit: 150 * time.Millisecond},
		{name: "过滤时间窗", q: "登录", filter: search.Filter{From: "2020-01-01T00:00:00.000000000Z", To: "2030-01-01T00:00:00.000000000Z"}, limit: 150 * time.Millisecond},
		{name: "分页 offset=100", q: "限流", filter: search.Filter{Offset: 100, Limit: 20}, limit: 150 * time.Millisecond},
	}
	type row struct {
		name            string
		p50, p95, limit time.Duration
	}
	rows := make([]row, 0, len(specs))
	failed := false
	for _, spec := range specs {
		p50, p95 := measure(t, st, spec, 60)
		rows = append(rows, row{spec.name, p50, p95, spec.limit})
		if p95 > spec.limit {
			failed = true
			t.Errorf("SLOW %s p95=%s > limit=%s", spec.name, p95, spec.limit)
		}
		t.Logf("PERF %-24s p50=%-9s p95=%-9s limit=%s", spec.name, p50, p95, spec.limit)
	}

	// 写入后 ≤100ms 可搜
	conv := stats.Conversations[0]
	writeStart := time.Now()
	msg, _, created, err := st.InsertMessage(ctx, store.NewMessage{
		ConversationID: conv, SenderType: "user", SenderID: "owner", SenderName: "阿哲",
		Type: "text", Content: "刚刚写入的限流指标需要立即可搜", ClientID: "bench-write-1",
	})
	if err != nil || !created {
		t.Fatalf("write: %v created=%v", err, created)
	}
	searchStart := time.Now()
	rs, err := search.Search(ctx, st.DB, "刚刚写入", search.Filter{})
	writeVisible := time.Since(searchStart)
	if err != nil || rs.Total == 0 {
		t.Fatalf("write->search failed: total=%d err=%v", rs.Total, err)
	}
	if writeVisible > 100*time.Millisecond {
		failed = true
		t.Errorf("WRITE->SEARCH %s > 100ms", writeVisible)
	}
	t.Logf("WRITE->SEARCH msg=%s visible_after=%s", msg.ID, writeVisible)
	_ = writeStart

	// Recall@10 金标准（20 query）
	hits := 0
	queries := 0
	for token, wantID := range stats.GoldQueries {
		if queries >= 20 {
			break
		}
		queries++
		rs, err := search.Search(ctx, st.DB, token, search.Filter{Limit: 10})
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		for _, it := range rs.Items {
			if it.RefID == wantID {
				hits++
				break
			}
		}
	}
	recall := float64(hits) / float64(queries)
	t.Logf("RECALL@10 = %d/%d = %.2f", hits, queries, recall)
	if recall < 0.8 {
		t.Errorf("Recall@10 = %.2f < 0.8", recall)
	}

	// 重建索引
	rebuildStart := time.Now()
	rebuild, err := search.Rebuild(ctx, st.DB)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	t.Logf("REBUILD docs=%d terms=%d elapsed=%s (-=search during rebuild)", rebuild.Documents, rebuild.Terms, time.Since(rebuildStart))
	if rebuild.Documents != int64(stats.Messages) {
		t.Errorf("rebuild documents=%d want %d", rebuild.Documents, stats.Messages)
	}
	// 重建后仍有结果
	if rs, err := search.Search(ctx, st.DB, "限流", search.Filter{}); err != nil || rs.Total == 0 {
		t.Errorf("post-rebuild search failed: total=%d err=%v", rs.Total, err)
	}

	_ = http.StatusOK
	if failed {
		t.Fatalf("search perf gate failed")
	}
}

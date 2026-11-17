package platform_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
)

func TestWriteTimelineReadAPI(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()

	base := "2026-01-01T00:00:00."
	w1 := domain.WriteEvent{ID: "write_1", AgentID: "agent_1", Path: "a.go", BaseHash: "h1", Content: "v2", Diff: "-old\n+new", Reason: "implement", Branch: "main", Status: domain.WriteApplied, CreatedAt: base + "000000001Z"}
	w2 := domain.WriteEvent{ID: "write_2", AgentID: "agent_2", Path: "a.go", BaseHash: "stale", Content: "v3", Diff: "-new\n+v3", Reason: "conflict retry", Branch: "main", Status: domain.WriteRejected, CreatedAt: base + "000000002Z"}
	if err := e.st.InsertWriteEvent(ctx, w1); err != nil {
		t.Fatalf("write1: %v", err)
	}
	if err := e.st.InsertWriteEvent(ctx, w2); err != nil {
		t.Fatalf("write2: %v", err)
	}
	// 35 次写只留最近 30
	for i := 0; i < 35; i++ {
		ev := domain.FileTimelineEntry{
			Path: "a.go", WriteEventID: "tl_" + itoa10(i), AgentID: "agent_1", TaskID: "task_1",
			Diff: "-a\n+b" + itoa10(i), Reason: "change " + itoa10(i), Branch: "main", Status: domain.WriteApplied,
			CreatedAt: "2026-01-01T00:00:" + twoDigit(i) + ".000000000Z",
		}
		if err := e.st.AppendTimeline(ctx, ev); err != nil {
			t.Fatalf("timeline %d: %v", i, err)
		}
	}
	if n, err := e.st.TrimTimeline(ctx, "a.go", 30); err != nil || n != 5 {
		t.Fatalf("trim = %d err=%v, want 5", n, err)
	}

	status, out := e.do("GET", "/api/writes/timeline?path=a.go&limit=100", nil)
	d := e.mustOK(status, out)
	items, _ := d["items"].([]any)
	if len(items) != 30 {
		t.Fatalf("timeline = %d, want 30", len(items))
	}
	first := items[0].(map[string]any)
	if first["agent_id"] != "agent_1" || first["task_id"] != "task_1" || first["diff"] == "" {
		t.Fatalf("timeline item = %v", first)
	}
	if items[0].(map[string]any)["created_at"].(string) < items[29].(map[string]any)["created_at"].(string) {
		t.Fatalf("timeline not desc")
	}

	// 冲突：同文件两次尝试可查（applied + rejected）
	status, out = e.do("GET", "/api/writes?status=rejected&path=a.go", nil)
	items, _ = e.mustOK(status, out)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "write_2" {
		t.Fatalf("rejected writes = %v", items)
	}
	// 详情
	status, out = e.do("GET", "/api/writes/write_1", nil)
	detail := e.mustOK(status, out)
	if detail["status"] != domain.WriteApplied || detail["diff"] == "" || detail["base_hash"] != "h1" {
		t.Fatalf("write detail = %v", detail)
	}
	// commit 组字段
	if err := e.st.CreateCommitGroup(ctx, "cg_1", "sha_1", []string{"write_1"}, "2026-01-01T00:01:00.000000000Z"); err != nil {
		t.Fatalf("commit group: %v", err)
	}
	if err := e.st.MarkWriteEventsCommitted(ctx, []string{"write_1"}, "sha_1"); err != nil {
		t.Fatalf("mark committed: %v", err)
	}
	if err := e.st.MarkTimelineCommitted(ctx, []string{"tl_34"}, "sha_1"); err != nil {
		t.Fatalf("mark timeline: %v", err)
	}
	status, out = e.do("GET", "/api/writes/write_1", nil)
	if e.mustOK(status, out)["commit_sha"] != "sha_1" {
		t.Fatalf("commit_sha not projected: %v", out)
	}
	// 过滤 agent_2
	status, out = e.do("GET", "/api/writes?agent_id=agent_2", nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) != 1 {
		t.Fatalf("agent filter = %v", items)
	}
	// 未认证
	if status, _ := (&http.Client{}).Get(e.ts.URL + "/api/writes/timeline"); status != nil && status.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth timeline = %d, want 401", status.StatusCode)
	}
}

func itoa10(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func twoDigit(v int) string {
	if v < 10 {
		return "0" + itoa10(v)
	}
	return itoa10(v)
}

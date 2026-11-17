//go:build acceptance

package ac

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lignting/lignting-agents/internal/write"
	"github.com/lignting/lignting-agents/tests/fixtures"
)

// TestAC15ConcurrentWriteTimeline covers AC-15 through the real shared write
// service + /api/writes read API: same-file base_hash conflict (only one
// winner), rebase retry, different files, 35 writes trimming the per-file
// timeline to 30, commit-group projection, unmanaged (bash) changes and audit.
func TestAC15ConcurrentWriteTimeline(t *testing.T) {
	h := NewHarness(t, WithSeed(), WithCoreServices())
	h.EnsureSession()
	ctx := context.Background()
	svc, err := write.New(write.Options{
		Root: h.Cfg.WorkspaceDir, TimelineLimit: 30, Repository: h.Store, Audit: h.Store,
	})
	if err != nil {
		t.Fatalf("write service: %v", err)
	}
	apply := func(req write.Request) *write.Result {
		t.Helper()
		if req.Branch == "" {
			req.Branch = "main"
		}
		res, err := svc.Apply(ctx, req)
		if err != nil {
			t.Fatalf("apply %s: %v", req.Path, err)
		}
		return res
	}
	fileHash := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(h.Cfg.WorkspaceDir, rel))
		if errors.Is(err, os.ErrNotExist) {
			return write.HashString("")
		}
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return write.HashString(string(b))
	}

	// 1) Two agents race on the same base_hash: exactly one write wins.
	init := apply(write.Request{ActorID: fixtures.AgentDevAID, TaskID: fixtures.TaskID, Path: "ac15/concurrent.txt", Content: "v1\n", Reason: "初始化"})
	if init.Hash != write.HashString("v1\n") {
		t.Fatalf("init hash = %s", init.Hash)
	}
	type outcome struct {
		res *write.Result
		err error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for _, c := range []struct{ actor, content string }{
		{fixtures.AgentDevAID, "v2-a\n"},
		{fixtures.AgentDevBID, "v2-b\n"},
	} {
		c := c
		go func() {
			<-start
			res, err := svc.Apply(ctx, write.Request{
				ActorID: c.actor, TaskID: fixtures.TaskID, Path: "ac15/concurrent.txt",
				Content: c.content, BaseHash: init.Hash, Reason: "并发写同一文件",
			})
			results <- outcome{res: res, err: err}
		}()
	}
	close(start)
	var wins, conflicts int
	for i := 0; i < 2; i++ {
		out := <-results
		switch {
		case out.err == nil:
			wins++
		case errors.Is(out.err, write.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent write error: %v", out.err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("concurrent same-file writes: wins=%d conflicts=%d, want 1/1", wins, conflicts)
	}
	var rejected struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"items"`
	}
	h.GetData(t, "/api/writes?status=rejected&path=ac15/concurrent.txt", &rejected)
	if len(rejected.Items) != 1 || rejected.Items[0].Error == "" {
		t.Fatalf("rejected write events = %+v, want one conflict record", rejected.Items)
	}

	// 2) The loser re-reads the file, rebases and succeeds.
	current := fileHash("ac15/concurrent.txt")
	rebased := apply(write.Request{ActorID: fixtures.AgentDevBID, TaskID: fixtures.TaskID, Path: "ac15/concurrent.txt", Content: "v3-rebased\n", BaseHash: current, Reason: "重新读取后变基"})
	if rebased.Hash != write.HashString("v3-rebased\n") {
		t.Fatalf("rebased hash = %s", rebased.Hash)
	}
	if got := fileHash("ac15/concurrent.txt"); got != rebased.Hash {
		t.Fatalf("file hash = %s, want %s", got, rebased.Hash)
	}

	// 3) Different files: both writes succeed.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, rel := range []string{"ac15/a.txt", "ac15/b.txt"} {
		rel := rel
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Apply(ctx, write.Request{ActorID: fixtures.AgentDevAID, Path: rel, Content: rel + "\n", Reason: "不同文件并行"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("different-file write: %v", err)
		}
	}

	// 4) 35 consecutive writes leave only the newest 30 timeline entries.
	content := "line-0\n"
	base := ""
	for i := 0; i < 35; i++ {
		content = fmt.Sprintf("line-%d\n", i+1)
		res := apply(write.Request{ActorID: fixtures.AgentDevAID, TaskID: fixtures.TaskID, Path: "ac15/history.txt", Content: content, BaseHash: base, Reason: fmt.Sprintf("历史修改 %d", i)})
		base = res.Hash
	}
	var timeline struct {
		Items []struct {
			ID        string `json:"id"`
			Path      string `json:"path"`
			AgentID   string `json:"agent_id"`
			TaskID    string `json:"task_id"`
			Diff      string `json:"diff"`
			Reason    string `json:"reason"`
			Branch    string `json:"branch"`
			Status    string `json:"status"`
			CreatedAt string `json:"created_at"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/writes/timeline?path=ac15/history.txt&limit=100", &timeline)
	if timeline.Total != 30 || len(timeline.Items) != 30 {
		t.Fatalf("timeline after 35 writes = %d/%d, want 30", timeline.Total, len(timeline.Items))
	}
	for _, it := range timeline.Items {
		if it.Path != "ac15/history.txt" || it.AgentID == "" || it.TaskID == "" || it.Diff == "" || it.Reason == "" || it.Branch == "" {
			t.Fatalf("timeline item incomplete: %+v", it)
		}
	}
	if timeline.Items[0].CreatedAt < timeline.Items[29].CreatedAt {
		t.Fatalf("timeline is not newest-first")
	}
	var writes struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	h.GetData(t, "/api/writes?path=ac15/history.txt&status=applied&limit=500", &writes)
	if len(writes.Items) != 35 {
		t.Fatalf("applied write events = %d, want 35 (events are not trimmed)", len(writes.Items))
	}

	// 5) Commit groups + seeded rejected attempt are projected by the read API.
	var seeded struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		BaseHash  string `json:"base_hash"`
		Diff      string `json:"diff"`
		CommitSHA string `json:"commit_sha"`
	}
	h.GetData(t, "/api/writes/"+h.Seed.WriteAppliedID, &seeded)
	if seeded.Status != "applied" || seeded.CommitSHA != h.Seed.GitCommitSHA || seeded.BaseHash == "" || seeded.Diff == "" {
		t.Fatalf("seeded write detail = %+v", seeded)
	}
	var committed struct {
		Items []struct {
			CommitSHA string `json:"commit_sha"`
		} `json:"items"`
	}
	h.GetData(t, "/api/writes/timeline?path="+fixtures.TimelinePath+"&commit="+h.Seed.GitCommitSHA+"&limit=100", &committed)
	if len(committed.Items) != 1 {
		t.Fatalf("commit-group timeline = %d, want the seeded main.go entry", len(committed.Items))
	}
	for _, it := range committed.Items {
		if it.CommitSHA != h.Seed.GitCommitSHA {
			t.Fatalf("commit filter leaked %q", it.CommitSHA)
		}
	}

	// 6) bash bypass is recorded as an unmanaged change, never silently applied.
	unmanaged, err := svc.RecordUnmanaged(ctx, write.UnmanagedRequest{
		ActorType: "tool", ActorID: "bash", TaskID: fixtures.TaskID, Path: "ac15/unmanaged.txt",
		Reason: "bash 直接改文件", Before: "", BeforeExists: false, After: "bash wrote this\n", AfterExists: true,
	})
	if err != nil {
		t.Fatalf("record unmanaged: %v", err)
	}
	if unmanaged.Event.Status != "unknown" || unmanaged.Event.Error == "" {
		t.Fatalf("unmanaged event = %+v", unmanaged.Event)
	}
	var unmanagedTimeline struct {
		Items []struct {
			Status string `json:"status"`
		} `json:"items"`
	}
	h.GetData(t, "/api/writes/timeline?path=ac15/unmanaged.txt", &unmanagedTimeline)
	if len(unmanagedTimeline.Items) != 1 || unmanagedTimeline.Items[0].Status != "unmanaged" {
		t.Fatalf("unmanaged timeline = %+v", unmanagedTimeline.Items)
	}

	// 7) Audit chain: every write path leaves a verifiable audit record.
	if _, err := h.Store.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain: %v", err)
	}
	var audit struct {
		Items []struct {
			Action string `json:"action"`
			Result string `json:"result"`
		} `json:"items"`
	}
	h.GetData(t, "/api/audit?action=write.apply&limit=1000", &audit)
	if len(audit.Items) == 0 {
		t.Fatal("no write.apply audit records")
	}
	h.GetData(t, "/api/audit?action=write.unmanaged&limit=10", &audit)
	if len(audit.Items) == 0 {
		t.Fatal("no write.unmanaged audit record")
	}

	t.Logf("AC-15 passed: 1/1 concurrent winner, rebase, 35->30 timeline, commit groups, unmanaged bypass audited")
}

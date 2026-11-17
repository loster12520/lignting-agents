package write

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lignting/lignting-agents/internal/store"
)

var _ Repository = (*store.Store)(nil)
var _ AuditSink = (*store.Store)(nil)

func TestStoreRepositoryIntegration(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	svc, err := New(Options{
		Root: filepath.Join(dir, "workspace"), Repository: st, Audit: st,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Apply(ctx, Request{
		ActorType: "agent", ActorID: "agent_a", TaskID: "task_1",
		Path: "docs/a.txt", Content: "hello\n", Reason: "integration",
	})
	if err != nil {
		t.Fatalf("apply new: %v", err)
	}
	var events, timeline int
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM write_events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM file_timeline").Scan(&timeline); err != nil {
		t.Fatal(err)
	}
	if events != 1 || timeline != 1 {
		t.Fatalf("events=%d timeline=%d", events, timeline)
	}
	updated, err := svc.Apply(ctx, Request{
		ActorType: "agent", ActorID: "agent_a", TaskID: "task_1",
		Path: "docs/a.txt", Content: "hello world\n", BaseHash: res.Hash, Reason: "update",
	})
	if err != nil {
		t.Fatalf("apply update: %v", err)
	}
	entries, err := svc.Timeline(ctx, TimelineFilter{Path: "docs/a.txt", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].WriteEventID != updated.Event.ID {
		t.Fatalf("timeline = %+v", entries)
	}
}

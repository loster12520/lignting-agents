package fixtures_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/tests/fixtures"
)

func TestSeedDefault(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "lignting.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	res, err := fixtures.SeedDefault(ctx, st.DB)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if res.TaskID != fixtures.TaskID || len(res.CriteriaIDs) != 3 {
		t.Fatalf("unexpected seed result: task=%q criteria=%v", res.TaskID, res.CriteriaIDs)
	}
	if res.DecisionAcceptedID != fixtures.DecisionAcceptedID {
		t.Fatalf("decision id = %q", res.DecisionAcceptedID)
	}
	counts := map[string]int{
		"departments":       3,
		"agents":            8,
		"conversations":     8,
		"messages":          8,
		"tasks":             2,
		"task_criteria":     3,
		"artifacts":         2,
		"artifact_versions": 2,
		"reviews":           2,
		"review_verdicts":   1,
		"decisions":         2,
		"approvals":         3,
		"memories":          5,
		"prompt_manuals":    1,
		"write_events":      3,
		"file_timeline":     2 + 35,
		"audit_log":         4,
		"notifications":     4,
		"turns":             2,
		"context_snapshots": 1,
		"bridges":           1,
	}
	for table, want := range counts {
		var got int
		if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Errorf("%s count = %d, want %d", table, got, want)
		}
	}
	if len(res.AuditSeqs) != 4 {
		t.Errorf("audit seqs = %v", res.AuditSeqs)
	}
	if res.TimelinePath != fixtures.TimelinePath {
		t.Errorf("timeline path = %q", res.TimelinePath)
	}
}

func TestFaultsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, found, err := fixtures.ReadFaults(dir); err != nil || found {
		t.Fatalf("missing faults: found=%v err=%v", found, err)
	}
	want := fixtures.Faults{ModelTimeoutMS: 1500, BudgetForcePercent: 100, DenyTool: "bash"}
	if _, err := fixtures.WriteFaults(dir, want); err != nil {
		t.Fatalf("write faults: %v", err)
	}
	got, found, err := fixtures.ReadFaults(dir)
	if err != nil || !found {
		t.Fatalf("read faults: found=%v err=%v", found, err)
	}
	if got != want {
		t.Fatalf("faults = %+v, want %+v", got, want)
	}
	env := want.EnvMap()
	if env[fixtures.FaultEnv("budget_force_percent")] != "100" {
		t.Errorf("env map = %v", env)
	}
}

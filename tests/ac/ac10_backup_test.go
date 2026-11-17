//go:build acceptance

package ac

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/tests/fixtures"
)

// TestAC10BackupExportRestore covers AC-10: consistent backup -> restore into
// a second data directory -> open/verify -> redacted JSON+Markdown export ->
// import into a third directory. It exercises the real /api/admin/* HTTP
// surface against a seeded SQLite store; the restored database is reopened and
// its message rows + audit hash chain are verified (FR-OPS-01/02, NFR-REL-05).
func TestAC10BackupExportRestore(t *testing.T) {
	h := NewHarness(t, WithSeed(), WithCoreServices())
	h.EnsureSession()
	ctx := context.Background()

	// A secret in the data directory must never leak into backup/export.
	const secret = "sk-ac10-super-secret"
	if err := config.SaveSecrets(h.Cfg.DataDir, config.Secrets{DeepSeekAPIKey: secret}); err != nil {
		t.Fatalf("save secrets: %v", err)
	}

	var backup struct {
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		SHA256   string `json:"sha256"`
		Manifest struct {
			Format        string `json:"format"`
			BackupID      string `json:"backup_id"`
			SchemaVersion int    `json:"schema_version"`
			Redaction     struct {
				ConfigRedacted  bool `json:"config_redacted"`
				SecretsExcluded bool `json:"secrets_excluded"`
			} `json:"redaction"`
		} `json:"manifest"`
	}
	h.PostData(t, "/api/admin/backup", map[string]any{}, &backup)
	if backup.Path == "" || backup.Size <= 0 || len(backup.SHA256) != 64 {
		t.Fatalf("backup result = %+v", backup)
	}
	if backup.Manifest.SchemaVersion != store.SchemaVersion || backup.Manifest.BackupID == "" {
		t.Fatalf("backup manifest = %+v", backup.Manifest)
	}
	if !backup.Manifest.Redaction.SecretsExcluded || !backup.Manifest.Redaction.ConfigRedacted {
		t.Fatalf("backup redaction = %+v, want secrets excluded + config redacted", backup.Manifest.Redaction)
	}
	if _, err := os.Stat(backup.Path); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if !strings.HasPrefix(backup.Path, filepath.Join(h.Cfg.DataDir, "backups")) {
		t.Fatalf("backup path %s not under data dir backups", backup.Path)
	}

	var backups struct {
		Items []struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Size int64  `json:"size"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/admin/backups", &backups)
	if backups.Total != 1 || len(backups.Items) != 1 || backups.Items[0].Path != backup.Path {
		t.Fatalf("backups list = %+v, want one entry for %s", backups, backup.Path)
	}

	// Redacted export: JSON + Markdown, secret not present.
	var exported struct {
		Dir           string         `json:"dir"`
		Files         []string       `json:"files"`
		Counts        map[string]int `json:"counts"`
		Redacted      bool           `json:"redacted"`
		SchemaVersion int            `json:"schema_version"`
	}
	h.PostData(t, "/api/admin/export", map[string]any{}, &exported)
	if exported.Dir == "" || !exported.Redacted || exported.SchemaVersion != store.SchemaVersion {
		t.Fatalf("export result = %+v", exported)
	}
	if exported.Counts["messages"] == 0 || exported.Counts["tasks"] == 0 {
		t.Fatalf("export counts = %+v, want seeded messages/tasks", exported.Counts)
	}
	raw, err := os.ReadFile(filepath.Join(exported.Dir, "export.json"))
	if err != nil {
		t.Fatalf("read export.json: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("export.json leaked the DeepSeek API key")
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("export.json not valid JSON: %v", err)
	}
	if parsed["format"] == nil || parsed["tables"] == nil {
		t.Fatalf("export.json missing format/tables: %v", parsed)
	}
	readme, err := os.ReadFile(filepath.Join(exported.Dir, "README.md"))
	if err != nil || !strings.Contains(string(readme), "脱敏") {
		t.Fatalf("export README missing/redaction note: err=%v", err)
	}

	// Restore requires explicit confirm and a non-live target.
	h.ExpectStatus(t, "POST", "/api/admin/restore", map[string]any{
		"backup_path": backup.Path, "target_dir": filepath.Join(t.TempDir(), "x"),
	}, 409)
	h.ExpectStatus(t, "POST", "/api/admin/restore", map[string]any{
		"backup_path": backup.Path, "target_dir": h.Cfg.DataDir, "confirm": true,
	}, 409)

	restoreTarget := filepath.Join(t.TempDir(), "restored")
	var restored struct {
		BackupID      string `json:"backup_id"`
		SchemaVersion int    `json:"schema_version"`
	}
	h.PostData(t, "/api/admin/restore", map[string]any{
		"backup_path": backup.Path, "target_dir": restoreTarget, "confirm": true,
	}, &restored)
	if restored.BackupID != backup.Manifest.BackupID || restored.SchemaVersion != store.SchemaVersion {
		t.Fatalf("restore manifest = %+v", restored)
	}
	verifyRestoredDB(t, filepath.Join(restoreTarget, "lignting.db"), "restore")

	// Import the export into a third directory; duplicate import is idempotent.
	importTarget := filepath.Join(t.TempDir(), "imported")
	var imported struct {
		DataDir            string         `json:"data_dir"`
		Tables             map[string]any `json:"tables"`
		TotalInserted      int            `json:"total_inserted"`
		AuditChainVerified bool           `json:"audit_chain_verified"`
		Redacted           bool           `json:"redacted"`
	}
	h.PostData(t, "/api/admin/import", map[string]any{
		"export_path": exported.Dir, "target_dir": importTarget, "mode": "skip", "confirm": true,
	}, &imported)
	if imported.DataDir == "" || !imported.AuditChainVerified || !imported.Redacted || imported.TotalInserted == 0 {
		t.Fatalf("import result = %+v", imported)
	}
	verifyRestoredDB(t, filepath.Join(importTarget, "lignting.db"), "import")
	h.ExpectStatus(t, "POST", "/api/admin/import", map[string]any{
		"export_path": exported.Dir, "target_dir": importTarget, "mode": "skip",
	}, 409)
	var reimported struct {
		TotalInserted      int  `json:"total_inserted"`
		TotalSkipped       int  `json:"total_skipped"`
		AuditChainVerified bool `json:"audit_chain_verified"`
	}
	h.PostData(t, "/api/admin/import", map[string]any{
		"export_path": exported.Dir, "target_dir": importTarget, "mode": "skip", "confirm": true,
	}, &reimported)
	if reimported.TotalInserted != 0 || reimported.TotalSkipped == 0 || !reimported.AuditChainVerified {
		t.Fatalf("duplicate import = %+v, want 0 inserted and skipped>0", reimported)
	}

	// Index rebuild on the live store keeps the seeded Chinese text searchable.
	var stats struct {
		Documents int `json:"documents"`
	}
	h.PostData(t, "/api/admin/reindex", map[string]any{}, &stats)
	if stats.Documents == 0 {
		t.Fatalf("reindex stats = %+v", stats)
	}
	var hits struct {
		Total int64 `json:"total"`
	}
	h.GetData(t, "/api/search?q=%E9%99%90%E6%B5%81", &hits)
	if hits.Total == 0 {
		t.Fatalf("seeded message is not searchable after reindex")
	}

	// Every sensitive operation is audited (append-only hash chain intact).
	for _, action := range []string{"admin.backup", "admin.export", "admin.restore", "admin.import", "search.reindex"} {
		audits, err := h.Store.QueryAudit(ctx, store.AuditFilter{Action: action, Limit: 10})
		if err != nil || len(audits) == 0 {
			t.Errorf("audit %s missing: n=%d err=%v", action, len(audits), err)
		}
	}
	if _, err := h.Store.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain after admin ops: %v", err)
	}

	t.Logf("AC-10 passed: backup=%s export=%s restore+import verified; audit chain intact", filepath.Base(backup.Path), filepath.Base(exported.Dir))
}

// verifyRestoredDB opens a restored/imported database and checks that seeded
// messages and the audit hash chain survived the round trip.
func verifyRestoredDB(t *testing.T, dbPath, label string) {
	t.Helper()
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("%s db missing: %v", label, err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("%s open db: %v", label, err)
	}
	defer st.Close()
	ctx := context.Background()
	n, err := st.CountMessages(ctx, fixtures.ConversationDMID)
	if err != nil || n == 0 {
		t.Fatalf("%s messages = %d err=%v, want seeded DM messages", label, n, err)
	}
	if _, err := st.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("%s audit chain: %v", label, err)
	}
}

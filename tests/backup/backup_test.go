package backup_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/backup"
	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

const testSecret = "sk-test-SECRET-abcdefghijklmnop"

func newFixture(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default(dir)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := config.SaveSecrets(dir, config.Secrets{DeepSeekAPIKey: testSecret}); err != nil {
		t.Fatalf("save secrets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "artifacts", "a1.md"), []byte("# artifact one\n"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}
	s, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec(`INSERT INTO messages(id, conversation_id, sender_type, sender_id, sender_name, type, content, created_at)
	      VALUES(?,?,?,?,?,?,?,?)`, "m1", "c1", "human", "owner", "Owner", "text", "hello "+testSecret, store.Now())
	exec(`INSERT INTO tasks(id, title, department_id, created_at, updated_at) VALUES(?,?,?,?,?)`, "t1", "Task one", "d1", store.Now(), store.Now())
	exec(`INSERT INTO artifacts(id, type, title, created_at) VALUES(?,?,?,?)`, "a1", "doc", "Artifact one", store.Now())
	exec(`INSERT INTO artifact_versions(id, artifact_id, version, content, content_hash, author_id, created_at)
	      VALUES(?,?,?,?,?,?,?)`, "av1", "a1", 1, "# content", "hash1", "dev1", store.Now())
	exec(`INSERT INTO reviews(id, claim, created_at) VALUES(?,?,?)`, "r1", "claim one", store.Now())
	exec(`INSERT INTO decisions(id, title, created_at) VALUES(?,?,?)`, "dec1", "Decision one", store.Now())
	exec(`INSERT INTO memories(id, scope, content, created_at, updated_at) VALUES(?,?,?,?,?)`,
		"mem1", "project", "remember this", store.Now(), store.Now())
	exec(`INSERT INTO task_events(task_id, type, actor_type, actor_id, created_at) VALUES(?,?,?,?,?)`,
		"t1", "created", "agent", "lead1", store.Now())

	for i, action := range []string{"fixture.one", "fixture.two"} {
		detail := fmt.Sprintf(`{"api_key":%q,"note":"audit %d %s"}`, testSecret, i, testSecret)
		if _, err := s.AppendAudit(ctx, domain.AuditEntry{
			ActorType: "human", ActorID: "owner", Action: action,
			ObjectType: "fixture", ObjectID: fmt.Sprintf("o%d", i), Result: "ok", Detail: detail,
		}); err != nil {
			t.Fatalf("append audit: %v", err)
		}
	}
	return dir, s
}

func readZip(t *testing.T, p string) (*zip.ReadCloser, map[string]*zip.File) {
	t.Helper()
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	out := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		out[f.Name] = f
	}
	return zr, out
}

func readEntry(t *testing.T, f *zip.File) []byte {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatalf("open entry %s: %v", f.Name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read entry %s: %v", f.Name, err)
	}
	return b
}

func writeZip(t *testing.T, p string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create entry: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("write entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}

func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	dir, _ := newFixture(t)
	res, err := backup.Backup(ctx, dir)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if res.Manifest.Format != backup.FormatName || res.Manifest.SchemaVersion != store.SchemaVersion {
		t.Fatalf("bad manifest: %+v", res.Manifest)
	}
	if res.Size <= 0 || len(res.SHA256) != 64 {
		t.Fatalf("bad archive metadata: size=%d sha=%q", res.Size, res.SHA256)
	}
	if !strings.HasPrefix(res.Path, filepath.Join(dir, "backups")) {
		t.Fatalf("backup not under backups: %s", res.Path)
	}
	zr, entries := readZip(t, res.Path)
	defer zr.Close()
	for _, want := range []string{backup.ManifestFile, backup.DBFile, backup.ConfigFile, "artifacts/a1.md"} {
		if entries[want] == nil {
			t.Fatalf("missing zip entry %s", want)
		}
	}
	for _, banned := range []string{"secrets.json", "workspace/main.go"} {
		if entries[banned] != nil {
			t.Fatalf("unexpected zip entry %s", banned)
		}
	}
	manifestRaw := readEntry(t, entries[backup.ManifestFile])
	var manifest backup.Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatalf("manifest json: %v", err)
	}
	if manifest.Database.Path != backup.DBFile || manifest.TotalSize <= 0 {
		t.Fatalf("manifest database/total: %+v", manifest)
	}
	if len(manifest.Excluded) == 0 || !manifest.Redaction.SecretsExcluded {
		t.Fatalf("manifest redaction/excluded missing: %+v", manifest)
	}
	configRaw := string(readEntry(t, entries[backup.ConfigFile]))
	if strings.Contains(configRaw, testSecret) {
		t.Fatalf("secret leaked into backup config")
	}

	target := filepath.Join(t.TempDir(), "restored")
	restored, err := backup.Restore(ctx, res.Path, target)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.SchemaVersion != store.SchemaVersion {
		t.Fatalf("restored manifest: %+v", restored)
	}
	if _, err := os.Stat(filepath.Join(target, "secrets.json")); !os.IsNotExist(err) {
		t.Fatalf("secrets.json should not be restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "workspace", "main.go")); !os.IsNotExist(err) {
		t.Fatalf("workspace should not be restored: %v", err)
	}
	art, err := os.ReadFile(filepath.Join(target, "artifacts", "a1.md"))
	if err != nil || string(art) != "# artifact one\n" {
		t.Fatalf("artifact restored wrong: %q %v", art, err)
	}
	var cfg map[string]any
	cfgRaw, err := os.ReadFile(filepath.Join(target, backup.ConfigFile))
	if err != nil {
		t.Fatalf("restored config: %v", err)
	}
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		t.Fatalf("restored config json: %v", err)
	}
	if cfg["data_dir"] != target {
		t.Fatalf("config data_dir not rewritten: %v", cfg["data_dir"])
	}

	st, err := store.Open(filepath.Join(target, backup.DBFile))
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	var msgCount, auditCount int
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages").Scan(&msgCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if msgCount != 1 {
		t.Fatalf("message count = %d", msgCount)
	}
	if _, err := st.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("restored audit chain: %v", err)
	}
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log").Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 2 {
		t.Fatalf("audit count = %d", auditCount)
	}
	_ = st.Close()

	if _, err := backup.Restore(ctx, res.Path, target); !errors.Is(err, backup.ErrTargetNotEmpty) {
		t.Fatalf("restore to non-empty target: got %v", err)
	}
}

func TestBackupRejectsBadArchives(t *testing.T) {
	ctx := context.Background()
	dir, _ := newFixture(t)
	_ = dir
	bad := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(bad, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Restore(ctx, bad, filepath.Join(t.TempDir(), "t1")); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("bad zip: got %v", err)
	}

	manifest := backup.Manifest{
		Format: backup.FormatName, FormatVersion: backup.FormatVersion,
		SchemaVersion: store.SchemaVersion + 1,
		Files:         []backup.FileInfo{{Path: backup.DBFile, Size: 1, SHA256: strings.Repeat("0", 64)}},
	}
	mb, _ := json.Marshal(manifest)
	ver := filepath.Join(t.TempDir(), "version.zip")
	writeZip(t, ver, map[string][]byte{backup.ManifestFile: mb, backup.DBFile: []byte("x")})
	if _, err := backup.Restore(ctx, ver, filepath.Join(t.TempDir(), "t2")); !errors.Is(err, backup.ErrVersionMismatch) {
		t.Fatalf("version mismatch: got %v", err)
	}

	noManifest := filepath.Join(t.TempDir(), "nomani.zip")
	writeZip(t, noManifest, map[string][]byte{backup.DBFile: []byte("x")})
	if _, err := backup.Restore(ctx, noManifest, filepath.Join(t.TempDir(), "t3")); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("missing manifest: got %v", err)
	}
}

func TestExportImport(t *testing.T) {
	ctx := context.Background()
	dir, _ := newFixture(t)
	res, err := backup.Export(ctx, dir)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	for _, name := range []string{backup.ExportFileName, backup.ExportReadme, "messages.md", "audit_log.md"} {
		if _, err := os.Stat(filepath.Join(res.Dir, name)); err != nil {
			t.Fatalf("missing export file %s: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(res.Dir, backup.ExportFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), testSecret) {
		t.Fatalf("secret leaked into export.json")
	}
	var ed backup.ExportData
	if err := json.Unmarshal(raw, &ed); err != nil {
		t.Fatalf("export json: %v", err)
	}
	if ed.Format != backup.ExportFormatName || !ed.Redacted || ed.Counts["messages"] != 1 || ed.Counts["audit_log"] != 2 {
		t.Fatalf("export data: format=%s redacted=%v counts=%v", ed.Format, ed.Redacted, ed.Counts)
	}
	md, err := os.ReadFile(filepath.Join(res.Dir, "messages.md"))
	if err != nil || strings.Contains(string(md), testSecret) {
		t.Fatalf("messages.md redaction: %v", err)
	}

	target := filepath.Join(t.TempDir(), "imported")
	imp, err := backup.Import(ctx, target, res.Dir, backup.ImportError)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if imp.TotalInserted <= 0 || !imp.AuditChainVerified || len(imp.Warnings) == 0 {
		t.Fatalf("import result: %+v", imp)
	}
	if imp.Tables["audit_log"].Skipped != 2 {
		t.Fatalf("redacted audit should be skipped: %+v", imp.Tables["audit_log"])
	}
	st, err := store.Open(filepath.Join(target, backup.DBFile))
	if err != nil {
		t.Fatalf("open imported db: %v", err)
	}
	var n int
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages").Scan(&n); err != nil || n != 1 {
		t.Fatalf("messages after import: n=%d err=%v", n, err)
	}
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log").Scan(&n); err != nil || n != 0 {
		t.Fatalf("audit after redacted import: n=%d err=%v", n, err)
	}
	if _, err := st.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain after redacted import: %v", err)
	}
	_ = st.Close()

	if _, err := backup.Import(ctx, target, res.Dir, backup.ImportError); !errors.Is(err, backup.ErrConfirmRequired) {
		t.Fatalf("non-empty import without confirm: got %v", err)
	}
	imp2, err := backup.Import(ctx, target, res.Dir, backup.ImportSkip, true)
	if err != nil {
		t.Fatalf("skip import: %v", err)
	}
	if imp2.TotalInserted != 0 || imp2.TotalSkipped == 0 {
		t.Fatalf("skip import result: %+v", imp2)
	}
	if _, err := backup.Import(ctx, target, res.Dir, backup.ImportError, true); !errors.Is(err, backup.ErrConflict) {
		t.Fatalf("error import conflict: got %v", err)
	}

	sensitive := filepath.Join(t.TempDir(), "sensitive")
	sres, err := backup.ExportTo(ctx, dir, sensitive, backup.ExportOptions{IncludeSensitive: true})
	if err != nil {
		t.Fatalf("sensitive export: %v", err)
	}
	sraw, _ := os.ReadFile(filepath.Join(sres.Dir, backup.ExportFileName))
	if !strings.Contains(string(sraw), testSecret) {
		t.Fatalf("sensitive export should contain secret for migration")
	}
	target2 := filepath.Join(t.TempDir(), "imported-sensitive")
	imp3, err := backup.Import(ctx, target2, sres.Dir, backup.ImportError)
	if err != nil {
		t.Fatalf("sensitive import: %v", err)
	}
	if !imp3.AuditChainVerified || imp3.Tables["audit_log"].Inserted != 2 {
		t.Fatalf("sensitive import audit: %+v", imp3)
	}

	var badEd backup.ExportData
	if err := json.Unmarshal(sraw, &badEd); err != nil {
		t.Fatal(err)
	}
	badEd.SchemaVersion = store.SchemaVersion + 1
	badRaw, _ := json.Marshal(badEd)
	badDir := filepath.Join(t.TempDir(), "badexport")
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, backup.ExportFileName), badRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Import(ctx, filepath.Join(t.TempDir(), "badimport"), badDir, backup.ImportError); !errors.Is(err, backup.ErrVersionMismatch) {
		t.Fatalf("version mismatch import: got %v", err)
	}
}

func TestRetentionPlanReadOnly(t *testing.T) {
	dir, _ := newFixture(t)
	backupsDir := filepath.Join(dir, "backups")
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		p := filepath.Join(backupsDir, fmt.Sprintf("lignting-backup-%d.zip", i))
		if err := os.WriteFile(p, []byte("zip"), 0o600); err != nil {
			t.Fatal(err)
		}
		ts := now.Add(-time.Duration(3-i) * time.Hour)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := backup.PlanRetention(dir, backup.RetentionOptions{Keep: 1, Now: now})
	if err != nil {
		t.Fatalf("PlanRetention: %v", err)
	}
	if len(plan) != 3 {
		t.Fatalf("plan len = %d", len(plan))
	}
	deleteCandidates := 0
	for _, e := range plan {
		if _, err := os.Stat(e.Path); err != nil {
			t.Fatalf("retention plan modified/deleted file: %v", err)
		}
		if e.Action == "delete-candidate" {
			deleteCandidates++
		}
	}
	if deleteCandidates != 2 {
		t.Fatalf("delete candidates = %d, plan=%+v", deleteCandidates, plan)
	}
}

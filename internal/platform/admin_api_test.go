package platform_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/lignting/lignting-agents/internal/store"
)

func TestAdminBackupExportRestoreImportEmergencyStopAudit(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()

	// 备份
	status, out := e.doCSRF("POST", "/api/admin/backup", nil)
	d := e.mustOK(status, out)
	backupPath, _ := d["path"].(string)
	if backupPath == "" {
		t.Fatalf("backup result = %v", d)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if got := d["manifest"].(map[string]any)["schema_version"].(float64); got != float64(store.SchemaVersion) {
		t.Fatalf("backup schema version = %v, want %d", d["manifest"], store.SchemaVersion)
	}
	status, out = e.do("GET", "/api/admin/backups", nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) != 1 {
		t.Fatalf("backups list = %v", out)
	}

	// 导出（JSON+Markdown，默认脱敏）
	status, out = e.doCSRF("POST", "/api/admin/export", nil)
	d = e.mustOK(status, out)
	exportDir, _ := d["dir"].(string)
	if exportDir == "" || d["redacted"] != true {
		t.Fatalf("export = %v", d)
	}
	if _, err := os.Stat(filepath.Join(exportDir, "export.json")); err != nil {
		t.Fatalf("export.json missing: %v", err)
	}

	// 恢复：未 confirm → 409；恢复到新目录 → 成功
	restoreTarget := filepath.Join(t.TempDir(), "restored")
	if status, _ := e.doCSRF("POST", "/api/admin/restore", map[string]any{"backup_path": backupPath, "target_dir": restoreTarget}); status != http.StatusConflict {
		t.Fatalf("restore without confirm = %d, want 409", status)
	}
	status, out = e.doCSRF("POST", "/api/admin/restore", map[string]any{
		"backup_path": backupPath, "target_dir": restoreTarget, "confirm": true,
	})
	d = e.mustOK(status, out)
	if d["backup_id"] == "" {
		t.Fatalf("restore manifest = %v", d)
	}
	if _, err := os.Stat(filepath.Join(restoreTarget, "lignting.db")); err != nil {
		t.Fatalf("restored db missing: %v", err)
	}
	// 不能恢复到当前运行目录
	if status, _ := e.doCSRF("POST", "/api/admin/restore", map[string]any{
		"backup_path": backupPath, "target_dir": e.cfg.DataDir, "confirm": true,
	}); status != http.StatusConflict {
		t.Fatalf("restore into live dir = %d, want 409", status)
	}

	// 导入到新目录；非空需 confirm
	importTarget := filepath.Join(t.TempDir(), "imported")
	status, out = e.doCSRF("POST", "/api/admin/import", map[string]any{
		"export_path": exportDir, "target_dir": importTarget, "mode": "skip", "confirm": true,
	})
	d = e.mustOK(status, out)
	if d["data_dir"] == "" || d["audit_chain_verified"] != true {
		t.Fatalf("import result = %v", d)
	}

	// 紧急停止（写 setting + event + audit）
	status, out = e.doCSRF("POST", "/api/admin/emergency-stop", map[string]any{"enabled": true})
	if e.mustOK(status, out)["emergency_stop"] != true {
		t.Fatalf("emergency stop = %v", out)
	}
	if v, ok, _ := e.st.Setting(ctx, "emergency_stop"); !ok || v != "true" {
		t.Fatalf("emergency_stop setting = %q ok=%v", v, ok)
	}
	status, out = e.doCSRF("POST", "/api/admin/emergency-stop", map[string]any{"enabled": false})
	if e.mustOK(status, out)["emergency_stop"] != false {
		t.Fatalf("emergency resume = %v", out)
	}
	if v, _, _ := e.st.Setting(ctx, "emergency_stop"); v != "false" {
		t.Fatalf("emergency_stop setting after resume = %q", v)
	}

	// 审计查询
	status, out = e.do("GET", "/api/audit?action=admin.backup&limit=10", nil)
	items, _ := e.mustOK(status, out)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("admin.backup audits = %v", out)
	}
	status, out = e.do("GET", "/api/audit?action=admin.emergency_stop", nil)
	if items, _ := e.mustOK(status, out)["items"].([]any); len(items) != 2 {
		t.Fatalf("emergency audits = %v", out)
	}
	if status, _ := e.do("GET", "/api/audit?since=not-a-time", nil); status != http.StatusBadRequest {
		t.Fatalf("bad since status = %d, want 400", status)
	}
	// 无 CSRF 的写操作
	if status, _ := e.do("POST", "/api/admin/backup", nil); status != http.StatusForbidden {
		t.Fatalf("backup without csrf = %d, want 403", status)
	}

	// 敏感操作全部有审计
	for _, action := range []string{"admin.backup", "admin.export", "admin.restore", "admin.import", "admin.emergency_stop"} {
		audits, err := e.st.QueryAudit(ctx, store.AuditFilter{Action: action, Limit: 10})
		if err != nil || len(audits) == 0 {
			t.Fatalf("audit %s missing: %d err=%v", action, len(audits), err)
		}
	}
}

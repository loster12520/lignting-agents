package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/lignting/lignting-agents/internal/store"
)

// Restore unpacks a backup into targetDataDir and verifies the database and
// audit chain. targetDataDir must be absent or empty; restore never overwrites
// an existing data directory. Callers must stop the platform before restoring
// so no process has the target database open.
func Restore(ctx context.Context, backupPath, targetDataDir string) (*Manifest, error) {
	if strings.TrimSpace(backupPath) == "" {
		return nil, fmt.Errorf("%w: empty backup path", ErrInvalidDataDir)
	}
	absBackup, err := filepath.Abs(backupPath)
	if err != nil {
		return nil, fmt.Errorf("backup path: %w", err)
	}
	zr, err := zip.OpenReader(absBackup)
	if err != nil {
		return nil, fmt.Errorf("%w: open archive: %v", ErrCorrupt, err)
	}
	defer zr.Close()

	entries := make(map[string]*zip.File, len(zr.File))
	var manifestFile *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if name == ManifestFile {
			manifestFile = f
			continue
		}
		entries[name] = f
	}
	if manifestFile == nil {
		return nil, fmt.Errorf("%w: manifest.json missing", ErrCorrupt)
	}
	manifest, err := readManifest(manifestFile)
	if err != nil {
		return nil, err
	}

	absTarget, err := filepath.Abs(targetDataDir)
	if err != nil || strings.TrimSpace(targetDataDir) == "" {
		return nil, fmt.Errorf("%w: target path", ErrInvalidDataDir)
	}
	if st, statErr := os.Stat(absTarget); statErr == nil {
		if !st.IsDir() {
			return nil, fmt.Errorf("%w: %s is not a directory", ErrTargetNotEmpty, absTarget)
		}
		list, readErr := os.ReadDir(absTarget)
		if readErr != nil {
			return nil, fmt.Errorf("read target: %w", readErr)
		}
		if len(list) != 0 {
			return nil, fmt.Errorf("%w: %s", ErrTargetNotEmpty, absTarget)
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("stat target: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(absTarget), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir target parent: %w", err)
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(absTarget), ".restore-"+filepath.Base(absTarget)+"-")
	if err != nil {
		return nil, fmt.Errorf("temp restore dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	seen := make(map[string]bool, len(manifest.Files))
	for _, fi := range manifest.Files {
		name := fi.Path
		if !validArchivePath(name) {
			return nil, fmt.Errorf("%w: unsafe path %q", ErrCorrupt, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("%w: duplicate path %q", ErrCorrupt, name)
		}
		seen[name] = true
		zf := entries[name]
		if zf == nil {
			return nil, fmt.Errorf("%w: missing %s", ErrCorrupt, name)
		}
		if err := extractFile(zf, fi, filepath.Join(tmpDir, filepath.FromSlash(name))); err != nil {
			return nil, err
		}
	}
	for name := range entries {
		if !seen[name] {
			return nil, fmt.Errorf("%w: unlisted archive entry %s", ErrCorrupt, name)
		}
	}
	seen[ManifestFile] = true

	if err := rewriteConfig(tmpDir, absTarget); err != nil {
		return nil, err
	}
	if err := validateRestoredDatabase(ctx, filepath.Join(tmpDir, DBFile)); err != nil {
		return nil, err
	}
	for _, d := range []string{"workspace", "artifacts", "backups", "logs"} {
		if err := os.MkdirAll(filepath.Join(tmpDir, d), 0o700); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	if st, statErr := os.Stat(absTarget); statErr == nil && st.IsDir() {
		if err := os.Remove(absTarget); err != nil {
			return nil, fmt.Errorf("remove empty target: %w", err)
		}
	}
	if err := os.Rename(tmpDir, absTarget); err != nil {
		return nil, fmt.Errorf("publish restored data: %w", err)
	}
	return &manifest, nil
}

func readManifest(f *zip.File) (Manifest, error) {
	var m Manifest
	rc, err := f.Open()
	if err != nil {
		return m, fmt.Errorf("%w: open manifest: %v", ErrCorrupt, err)
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, 8<<20))
	if err != nil {
		return m, fmt.Errorf("%w: read manifest: %v", ErrCorrupt, err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("%w: parse manifest: %v", ErrCorrupt, err)
	}
	if m.Format != FormatName {
		return m, fmt.Errorf("%w: format %q", ErrCorrupt, m.Format)
	}
	if m.FormatVersion != FormatVersion {
		return m, fmt.Errorf("%w: format version %d", ErrVersionMismatch, m.FormatVersion)
	}
	if m.SchemaVersion <= 0 {
		return m, fmt.Errorf("%w: missing schema version", ErrCorrupt)
	}
	if m.SchemaVersion > store.SchemaVersion {
		return m, fmt.Errorf("%w: backup schema %d > supported %d", ErrVersionMismatch, m.SchemaVersion, store.SchemaVersion)
	}
	dbFound := false
	for _, fi := range m.Files {
		if fi.Path == DBFile {
			dbFound = true
			if fi.Size <= 0 || fi.SHA256 == "" {
				return m, fmt.Errorf("%w: invalid database entry", ErrCorrupt)
			}
		}
	}
	if !dbFound {
		return m, fmt.Errorf("%w: database entry missing", ErrCorrupt)
	}
	return m, nil
}

func extractFile(zf *zip.File, fi FileInfo, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("mkdir extract: %w", err)
	}
	mode := os.FileMode(0o600)
	if fi.Kind == "artifact" {
		mode = 0o640
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	rc, err := zf.Open()
	if err != nil {
		out.Close()
		return fmt.Errorf("%w: open %s: %v", ErrCorrupt, fi.Path, err)
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), rc)
	rc.Close()
	if syncErr := out.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return fmt.Errorf("extract %s: %w", fi.Path, copyErr)
	}
	if n != fi.Size || hex.EncodeToString(h.Sum(nil)) != fi.SHA256 {
		return fmt.Errorf("%w: checksum mismatch for %s", ErrCorrupt, fi.Path)
	}
	return nil
}

func validArchivePath(name string) bool {
	if name == "" || strings.Contains(name, ":") || strings.Contains(name, "\\") {
		return false
	}
	if path.IsAbs(name) || strings.HasPrefix(name, "../") || name == ".." {
		return false
	}
	return path.Clean(name) == name
}

func rewriteConfig(dir, target string) error {
	p := filepath.Join(dir, ConfigFile)
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read restored config: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parse restored config: %w", err)
	}
	m["data_dir"] = target
	m["workspace_dir"] = filepath.Join(target, "workspace")
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal restored config: %w", err)
	}
	if err := os.WriteFile(p, out, 0o600); err != nil {
		return fmt.Errorf("write restored config: %w", err)
	}
	return nil
}

func validateRestoredDatabase(ctx context.Context, dbPath string) error {
	s, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("%w: open restored database: %v", ErrCorrupt, err)
	}
	defer s.Close()
	var integrity string
	if err := s.DB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("%w: integrity_check: %v", ErrCorrupt, err)
	}
	if integrity != "ok" {
		return fmt.Errorf("%w: integrity_check=%s", ErrCorrupt, integrity)
	}
	var version int
	if err := s.DB.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err != nil {
		return fmt.Errorf("%w: schema version: %v", ErrCorrupt, err)
	}
	if version > store.SchemaVersion {
		return fmt.Errorf("%w: restored schema %d > supported %d", ErrVersionMismatch, version, store.SchemaVersion)
	}
	if _, err := s.VerifyAuditChain(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	_ = s.Close()
	removeSQLiteAux(dbPath)
	return nil
}

func removeSQLiteAux(dbPath string) {
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}
}

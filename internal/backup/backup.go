// Package backup implements local backup, restore, export, import and
// retention planning for a Lignting data directory.
//
// The package is deliberately independent from the platform HTTP layer: the
// platform can call Backup/Restore/Export/Import and render its own UI. It
// imports internal/store only to read SchemaVersion and to use store.Open for
// snapshotting/verification (one-way dependency).
package backup

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/store"
)

const (
	// FormatName is the backup manifest format identifier.
	FormatName = "lignting-backup"
	// FormatVersion is the current manifest format version.
	FormatVersion = 1
	// DBFile is the database filename inside a data directory and a backup.
	DBFile = "lignting.db"
	// ConfigFile is the sanitised config filename inside a backup.
	ConfigFile = "config.json"
	// ManifestFile is the manifest filename inside a backup archive.
	ManifestFile = "manifest.json"
	// DefaultBackupDir is the directory below a data directory where backups are written.
	DefaultBackupDir = "backups"
)

var (
	// ErrInvalidDataDir means the data directory does not exist or is not a directory.
	ErrInvalidDataDir = errors.New("backup: invalid data directory")
	// ErrNotFound means a required file was not found.
	ErrNotFound = errors.New("backup: not found")
	// ErrCorrupt means an archive/export failed validation.
	ErrCorrupt = errors.New("backup: corrupt archive")
	// ErrVersionMismatch means the archive/export was produced by an incompatible version.
	ErrVersionMismatch = errors.New("backup: incompatible version")
	// ErrTargetNotEmpty means a restore/export target already contains data.
	ErrTargetNotEmpty = errors.New("backup: target is not empty")
	// ErrConfirmRequired means an import into a non-empty destination needs explicit confirmation.
	ErrConfirmRequired = errors.New("backup: confirmation required for non-empty destination")
	// ErrConflict means an import encountered a conflicting row (error mode).
	ErrConflict = errors.New("backup: import conflict")
	// ErrInvalidExport means an export directory/file is malformed.
	ErrInvalidExport = errors.New("backup: invalid export")
)

// FileInfo describes one file stored in a backup archive.
type FileInfo struct {
	// Path is the archive-relative path using forward slashes.
	Path string `json:"path"`
	// Kind is database, config or artifact.
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ExcludedEntry documents something intentionally not backed up.
type ExcludedEntry struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// RedactionInfo records how sensitive data was handled.
type RedactionInfo struct {
	ConfigRedacted          bool `json:"config_redacted"`
	SecretsExcluded         bool `json:"secrets_excluded"`
	SensitiveValuesRedacted bool `json:"sensitive_values_redacted"`
}

// Manifest is the machine-readable index stored as manifest.json in a backup.
type Manifest struct {
	Format        string          `json:"format"`
	FormatVersion int             `json:"format_version"`
	BackupID      string          `json:"backup_id"`
	AppVersion    string          `json:"app_version"`
	SchemaVersion int             `json:"schema_version"`
	CreatedAt     string          `json:"created_at"`
	Database      FileInfo        `json:"database"`
	Files         []FileInfo      `json:"files"`
	TotalSize     int64           `json:"total_size"`
	Excluded      []ExcludedEntry `json:"excluded"`
	Redaction     RedactionInfo   `json:"redaction"`
	Notes         []string        `json:"notes,omitempty"`
}

// BackupResult describes a completed backup.
type BackupResult struct {
	Path     string   `json:"path"`
	Size     int64    `json:"size"`
	SHA256   string   `json:"sha256"`
	Manifest Manifest `json:"manifest"`
}

// Backup creates a consistent zip backup of dataDir under <dataDir>/backups and
// returns its manifest, archive size and sha256. It does not need the platform
// to be stopped: the SQLite snapshot is produced with VACUUM INTO.
func Backup(ctx context.Context, dataDir string) (*BackupResult, error) {
	dir, err := resolveDir(dataDir)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, DBFile)
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, dbPath)
		}
		return nil, fmt.Errorf("stat database: %w", err)
	}
	backupsDir := filepath.Join(dir, DefaultBackupDir)
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir backups: %w", err)
	}

	now := time.Now().UTC()
	id := newID(now)
	createdAt := store.Now()
	secrets := loadSecrets(dir)

	tmpDir, err := os.MkdirTemp(backupsDir, ".tmp-backup-")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	snapshotPath := filepath.Join(tmpDir, DBFile)
	if err := snapshotDatabase(ctx, dbPath, snapshotPath); err != nil {
		return nil, err
	}

	configBytes, err := sanitizedConfig(dir, secrets)
	if err != nil {
		return nil, err
	}

	tmpZip, err := os.CreateTemp(backupsDir, ".tmp-backup-*.zip")
	if err != nil {
		return nil, fmt.Errorf("temp archive: %w", err)
	}
	tmpZipPath := tmpZip.Name()
	defer os.Remove(tmpZipPath)
	zw := zip.NewWriter(tmpZip)
	files := make([]FileInfo, 0, 8)
	addFile := func(src, archivePath, kind string) error {
		fi, err := addFile(zw, src, archivePath, kind)
		if err != nil {
			return err
		}
		files = append(files, fi)
		return nil
	}
	addBytes := func(name string, data []byte, kind string, mod time.Time) (FileInfo, error) {
		fi := FileInfo{Path: name, Kind: kind, Size: int64(len(data))}
		sum := sha256.Sum256(data)
		fi.SHA256 = hex.EncodeToString(sum[:])
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.Modified = mod
		hdr.SetMode(0o600)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return fi, err
		}
		if _, err := w.Write(data); err != nil {
			return fi, err
		}
		return fi, nil
	}

	var dbInfo FileInfo
	if err := addFile(snapshotPath, DBFile, "database"); err != nil {
		return nil, err
	}
	dbInfo = files[len(files)-1]

	if configBytes != nil {
		fi, err := addBytes(ConfigFile, configBytes, "config", now)
		if err != nil {
			return nil, fmt.Errorf("add config: %w", err)
		}
		files = append(files, fi)
	}

	excluded := []ExcludedEntry{
		{Path: "secrets.json", Reason: "凭据不入备份；恢复后需重新输入 DeepSeek API Key"},
		{Path: "workspace/", Reason: "用户项目文件不纳入 v0.0.1 备份，请用 Git/外部备份"},
		{Path: "logs/", Reason: "运行日志可重建，且可能含敏感信息"},
		{Path: "backups/", Reason: "避免备份嵌套备份"},
		{Path: "exports/", Reason: "导出目录不纳入备份"},
		{Path: "chrome-profile*/", Reason: "浏览器临时配置，不属于产品数据"},
		{Path: "lignting.db-wal", Reason: "SQLite WAL 不属于独立可恢复文件"},
		{Path: "lignting.db-shm", Reason: "SQLite 共享内存不属于独立可恢复文件"},
	}
	artifactDir := filepath.Join(dir, "artifacts")
	if _, err := os.Stat(artifactDir); err == nil {
		err := filepath.WalkDir(artifactDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				rel, _ := filepath.Rel(artifactDir, path)
				excluded = append(excluded, ExcludedEntry{Path: filepath.ToSlash(filepath.Join("artifacts", rel)), Reason: "符号链接不纳入备份"})
				return nil
			}
			rel, err := filepath.Rel(artifactDir, path)
			if err != nil {
				return err
			}
			return addFile(path, "artifacts/"+filepath.ToSlash(rel), "artifact")
		})
		if err != nil {
			return nil, fmt.Errorf("walk artifacts: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat artifacts: %w", err)
	}

	m := Manifest{
		Format:        FormatName,
		FormatVersion: FormatVersion,
		BackupID:      id,
		AppVersion:    config.Version,
		SchemaVersion: store.SchemaVersion,
		CreatedAt:     createdAt,
		Database:      dbInfo,
		Files:         files,
		Excluded:      excluded,
		Redaction: RedactionInfo{
			ConfigRedacted:          true,
			SecretsExcluded:         true,
			SensitiveValuesRedacted: true,
		},
	}
	for _, f := range files {
		m.TotalSize += f.Size
	}
	if configBytes == nil {
		m.Notes = append(m.Notes, "config.json 不存在，备份中未包含配置")
	}
	m.Notes = append(m.Notes,
		"恢复时目标目录必须为空；失败不会覆盖已有数据。",
		"workspace/、logs/、secrets.json 不在备份范围内（见 excluded）。",
		"恢复后 DeepSeek API Key 需重新输入；审计链在 Restore 时校验。",
	)
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	if _, err := addBytes(ManifestFile, mb, "manifest", now); err != nil {
		return nil, fmt.Errorf("add manifest: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close archive: %w", err)
	}
	if err := tmpZip.Sync(); err != nil {
		_ = tmpZip.Close()
		return nil, fmt.Errorf("sync archive: %w", err)
	}
	if err := tmpZip.Close(); err != nil {
		return nil, fmt.Errorf("close archive file: %w", err)
	}
	final := filepath.Join(backupsDir, "lignting-backup-"+id+".zip")
	if err := os.Rename(tmpZipPath, final); err != nil {
		return nil, fmt.Errorf("publish archive: %w", err)
	}
	size, sum, err := hashFile(final)
	if err != nil {
		return nil, err
	}
	return &BackupResult{Path: final, Size: size, SHA256: sum, Manifest: m}, nil
}

func resolveDir(dataDir string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", ErrInvalidDataDir
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidDataDir, err)
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrInvalidDataDir, abs)
	}
	return abs, nil
}

func snapshotDatabase(ctx context.Context, dbPath, snapshotPath string) error {
	s, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database for snapshot: %w", err)
	}
	defer s.Close()
	q := "VACUUM INTO '" + strings.ReplaceAll(filepath.ToSlash(snapshotPath), "'", "''") + "'"
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := s.DB.ExecContext(ctx, q); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
			if !isBusyErr(err) {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
			}
		}
	}
	if lastErr != nil {
		return fmt.Errorf("vacuum snapshot: %w", lastErr)
	}
	st, err := os.Stat(snapshotPath)
	if err != nil || st.Size() == 0 {
		if err == nil {
			err = errors.New("empty snapshot")
		}
		return fmt.Errorf("verify snapshot: %w", err)
	}
	return nil
}

func isBusyErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "locked") || strings.Contains(s, "busy")
}

func addFile(zw *zip.Writer, src, archivePath, kind string) (FileInfo, error) {
	f, err := os.Open(src)
	if err != nil {
		return FileInfo{}, fmt.Errorf("open %s: %w", src, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return FileInfo{}, err
	}
	hdr := &zip.FileHeader{Name: archivePath, Method: zip.Deflate}
	hdr.Modified = st.ModTime()
	hdr.SetMode(st.Mode().Perm())
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return FileInfo{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), f)
	if err != nil {
		return FileInfo{}, fmt.Errorf("write %s: %w", archivePath, err)
	}
	return FileInfo{Path: archivePath, Kind: kind, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func hashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func newID(now time.Time) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return now.Format("20060102-150405")
	}
	return now.Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

func loadSecrets(dataDir string) []string {
	sec, err := config.LoadSecrets(dataDir)
	if err != nil {
		return nil
	}
	var out []string
	if sec.DeepSeekAPIKey != "" {
		out = append(out, sec.DeepSeekAPIKey)
	}
	return out
}

func sanitizedConfig(dataDir string, secrets []string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, ConfigFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	v = redactAny(v, secrets)
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return out, nil
}

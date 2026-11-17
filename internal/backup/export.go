package backup

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/store"
)

const (
	// ExportFormatName is the JSON export format identifier.
	ExportFormatName = "lignting-export"
	// ExportFormatVersion is the current JSON export format version.
	ExportFormatVersion = 1
	// ExportFileName is the structured export document.
	ExportFileName = "export.json"
	// ExportReadme is the human-readable narrative summary.
	ExportReadme = "README.md"
)

// exportTables is the open-format export surface: messages, tasks, artifacts,
// reviews, decisions, memories and audit, plus their immutable child rows.
var exportTables = []string{
	"messages",
	"tasks",
	"task_criteria",
	"task_events",
	"artifacts",
	"artifact_versions",
	"reviews",
	"review_verdicts",
	"decisions",
	"memories",
	"memory_events",
	"audit_log",
}

// ExportTable is one table in the structured export.
type ExportTable struct {
	Columns []string `json:"columns"`
	Rows []map[string]any `json:"rows"`
}

// ExportData is the export.json document.
type ExportData struct {
	Format string `json:"format"`
	FormatVersion int `json:"format_version"`
	AppVersion string `json:"app_version"`
	SchemaVersion int `json:"schema_version"`
	ExportedAt string `json:"exported_at"`
	Redacted bool `json:"redacted"`
	Tables map[string]ExportTable `json:"tables"`
	Counts map[string]int `json:"counts"`
}

// ExportProgress is emitted once per table when a progress callback is set.
type ExportProgress struct {
	Phase string
	Table string
	Rows int
	Tables int
}

// ExportOptions controls an export.
type ExportOptions struct {
	// IncludeSensitive disables redaction. Default is redacted.
	IncludeSensitive bool
	// Progress is optional; called once per exported table.
	Progress func(ExportProgress)
}

// ExportResult describes a completed export directory.
type ExportResult struct {
	Dir string `json:"dir"`
	Files []string `json:"files"`
	Counts map[string]int `json:"counts"`
	Redacted bool `json:"redacted"`
	SchemaVersion int `json:"schema_version"`
	ExportedAt string `json:"exported_at"`
}

// Export writes a redacted JSON+Markdown export below <dataDir>/exports.
func Export(ctx context.Context, dataDir string) (*ExportResult, error) {
	dir, err := resolveDir(dataDir)
	if err != nil {
		return nil, err
	}
	exportsDir := filepath.Join(dir, "exports")
	out := filepath.Join(exportsDir, "export-"+newID(time.Now().UTC()))
	return exportTo(ctx, dir, out, ExportOptions{})
}

// ExportTo writes a JSON+Markdown export into outDir. outDir must be absent or
// empty. The export is assembled in a temporary sibling directory and renamed
// into place, so a failure does not leave a partial export.
func ExportTo(ctx context.Context, dataDir, outDir string, opts ExportOptions) (*ExportResult, error) {
	dir, err := resolveDir(dataDir)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(outDir) == "" {
		return nil, fmt.Errorf("%w: empty export directory", ErrInvalidDataDir)
	}
	return exportTo(ctx, dir, outDir, opts)
}

func exportTo(ctx context.Context, dataDir, outDir string, opts ExportOptions) (*ExportResult, error) {
	dbPath := filepath.Join(dataDir, DBFile)
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, dbPath)
		}
		return nil, fmt.Errorf("stat database: %w", err)
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return nil, fmt.Errorf("export path: %w", err)
	}
	if st, err := os.Stat(absOut); err == nil {
		if !st.IsDir() {
			return nil, fmt.Errorf("%w: %s", ErrTargetNotEmpty, absOut)
		}
		list, err := os.ReadDir(absOut)
		if err != nil {
			return nil, fmt.Errorf("read export target: %w", err)
		}
		if len(list) != 0 {
			return nil, fmt.Errorf("%w: %s", ErrTargetNotEmpty, absOut)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat export target: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absOut), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir export parent: %w", err)
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(absOut), ".export-"+filepath.Base(absOut)+"-")
	if err != nil {
		return nil, fmt.Errorf("temp export dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer s.Close()
	secrets := loadSecrets(dataDir)
	exportedAt := store.Now()
	ed := ExportData{
		Format: ExportFormatName,
		FormatVersion: ExportFormatVersion,
		AppVersion: config.Version,
		SchemaVersion: store.SchemaVersion,
		ExportedAt: exportedAt,
		Redacted: !opts.IncludeSensitive,
		Tables: make(map[string]ExportTable, len(exportTables)),
		Counts: make(map[string]int, len(exportTables)),
	}

	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin export read tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for i, table := range exportTables {
		et, err := readTable(ctx, tx, table, secrets, !opts.IncludeSensitive)
		if err != nil {
			return nil, err
		}
		ed.Tables[table] = et
		ed.Counts[table] = len(et.Rows)
		if opts.Progress != nil {
			opts.Progress(ExportProgress{Phase: "export", Table: table, Rows: len(et.Rows), Tables: i + 1})
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit export read tx: %w", err)
	}

	payload, err := json.MarshalIndent(ed, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal export: %w", err)
	}
	files := []string{ExportFileName, ExportReadme}
	if err := os.WriteFile(filepath.Join(tmpDir, ExportFileName), payload, 0o600); err != nil {
		return nil, fmt.Errorf("write export.json: %w", err)
	}
	if err := writeExportMarkdown(tmpDir, ed); err != nil {
		return nil, err
	}
	for _, table := range exportTables {
		files = append(files, table+".md")
	}
	if st, err := os.Stat(absOut); err == nil && st.IsDir() {
		if err := os.Remove(absOut); err != nil {
			return nil, fmt.Errorf("remove empty export target: %w", err)
		}
	}
	if err := os.Rename(tmpDir, absOut); err != nil {
		return nil, fmt.Errorf("publish export: %w", err)
	}
	return &ExportResult{
		Dir: absOut,
		Files: files,
		Counts: ed.Counts,
		Redacted: ed.Redacted,
		SchemaVersion: ed.SchemaVersion,
		ExportedAt: exportedAt,
	}, nil
}

func readTable(ctx context.Context, tx *sql.Tx, table string, secrets []string, redact bool) (ExportTable, error) {
	rows, err := tx.QueryContext(ctx, "SELECT * FROM \""+table+"\" ORDER BY rowid")
	if err != nil {
		return ExportTable{}, fmt.Errorf("query %s: %w", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return ExportTable{}, fmt.Errorf("columns %s: %w", table, err)
	}
	out := ExportTable{Columns: cols, Rows: []map[string]any{}}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return ExportTable{}, fmt.Errorf("scan %s: %w", table, err)
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			if redact {
				v = redactValue(c, v, secrets)
			}
			row[c] = v
		}
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return ExportTable{}, fmt.Errorf("read %s: %w", table, err)
	}
	return out, nil
}

var jsonExportCols = map[string]bool{
	"detail": true, "payload": true, "meta": true, "evidence": true, "dissent": true,
	"options": true, "deciders": true, "consulted": true, "informed": true,
	"affected_tasks": true, "affected_artifacts": true, "conditions": true,
	"dor": true, "dod": true, "flags": true, "role_card": true, "charter": true,
}

func redactValue(col string, v any, secrets []string) any {
	if sensitiveKeyRe.MatchString(col) {
		return "[REDACTED]"
	}
	s, ok := v.(string)
	if !ok {
		return v
	}
	if jsonExportCols[col] {
		return redactJSONText(s, secrets)
	}
	return redactText(s, secrets)
}

func writeExportMarkdown(dir string, ed ExportData) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Lignting 工作区导出\n\n")
	fmt.Fprintf(&b, "- 应用版本：%s\n", ed.AppVersion)
	fmt.Fprintf(&b, "- schema 版本：%d\n", ed.SchemaVersion)
	fmt.Fprintf(&b, "- 导出时间：%s\n", ed.ExportedAt)
	fmt.Fprintf(&b, "- 脱敏：%s\n", yesNo(ed.Redacted))
	fmt.Fprintf(&b, "- 结构化数据：%s（JSON，表 > 行 > 列）\n\n", ExportFileName)
	fmt.Fprintf(&b, "## 数量\n\n")
	tables := make([]string, 0, len(ed.Counts))
	for t := range ed.Counts {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		fmt.Fprintf(&b, "- %s：%d 行\n", t, ed.Counts[t])
	}
	if err := os.WriteFile(filepath.Join(dir, ExportReadme), []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write export README: %w", err)
	}
	for _, table := range exportTables {
		et := ed.Tables[table]
		var mb strings.Builder
		fmt.Fprintf(&mb, "# %s\n\n", table)
		fmt.Fprintf(&mb, "- 行数：%d\n", len(et.Rows))
		if ed.Redacted {
			fmt.Fprintf(&mb, "- 已脱敏：是\n")
		}
		fmt.Fprintf(&mb, "\n")
		for _, row := range et.Rows {
			fmt.Fprintf(&mb, "- ")
			for i, c := range et.Columns {
				if i > 0 {
					fmt.Fprintf(&mb, " | ")
				}
				fmt.Fprintf(&mb, "%s=%s", c, markdownValue(row[c]))
			}
			fmt.Fprintf(&mb, "\n")
		}
		if err := os.WriteFile(filepath.Join(dir, table+".md"), []byte(mb.String()), 0o600); err != nil {
			return fmt.Errorf("write %s.md: %w", table, err)
		}
	}
	return nil
}

func markdownValue(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
		s = ""
	case string:
		s = x
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case int64:
		s = strconv.FormatInt(x, 10)
	default:
		if b, err := json.Marshal(x); err == nil {
			s = string(b)
		} else {
			s = fmt.Sprint(x)
		}
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

// randomID is shared by export naming.
func randomID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b[:])
}

var _ = io.Discard

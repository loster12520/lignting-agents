package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lignting/lignting-agents/internal/store"
)

// ImportMode controls conflict handling when imported rows already exist.
type ImportMode string

const (
	// ImportSkip skips duplicate rows and reports the number skipped.
	ImportSkip ImportMode = "skip"
	// ImportError aborts on the first duplicate row.
	ImportError ImportMode = "error"
)

// TableImportStat is per-table import accounting.
type TableImportStat struct {
	Rows     int `json:"rows"`
	Inserted int `json:"inserted"`
	Skipped  int `json:"skipped"`
}

// ImportResult summarizes an import.
type ImportResult struct {
	DataDir            string                     `json:"data_dir"`
	Mode               ImportMode                 `json:"mode"`
	Tables             map[string]TableImportStat `json:"tables"`
	TotalInserted      int                        `json:"total_inserted"`
	TotalSkipped       int                        `json:"total_skipped"`
	AuditEntries       int64                      `json:"audit_entries"`
	AuditChainVerified bool                       `json:"audit_chain_verified"`
	Redacted           bool                       `json:"redacted"`
	Warnings           []string                   `json:"warnings,omitempty"`
}

// Import validates and imports an export directory (or export.json) into
// dataDir. Import never deletes or replaces existing rows; duplicate rows are
// skipped or rejected according to mode. A non-empty destination requires the
// optional confirm argument to be true.
func Import(ctx context.Context, dataDir, exportPath string, mode ImportMode, confirm ...bool) (*ImportResult, error) {
	if mode != ImportSkip && mode != ImportError {
		return nil, fmt.Errorf("%w: unknown import mode %q", ErrInvalidExport, mode)
	}
	dir, err := filepath.Abs(dataDir)
	if err != nil || strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("%w: target data dir", ErrInvalidDataDir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir target data dir: %w", err)
	}
	exportFile := exportPath
	if st, statErr := os.Stat(exportPath); statErr == nil && st.IsDir() {
		exportFile = filepath.Join(exportPath, ExportFileName)
	}
	raw, err := os.ReadFile(exportFile)
	if err != nil {
		return nil, fmt.Errorf("%w: read export.json: %v", ErrInvalidExport, err)
	}
	var ed ExportData
	if err := json.Unmarshal(raw, &ed); err != nil {
		return nil, fmt.Errorf("%w: parse export.json: %v", ErrInvalidExport, err)
	}
	if ed.Format != ExportFormatName {
		return nil, fmt.Errorf("%w: format %q", ErrInvalidExport, ed.Format)
	}
	if ed.FormatVersion != ExportFormatVersion {
		return nil, fmt.Errorf("%w: export format version %d", ErrVersionMismatch, ed.FormatVersion)
	}
	if ed.SchemaVersion <= 0 {
		return nil, fmt.Errorf("%w: missing schema version", ErrInvalidExport)
	}
	if ed.SchemaVersion > store.SchemaVersion {
		return nil, fmt.Errorf("%w: export schema %d > supported %d", ErrVersionMismatch, ed.SchemaVersion, store.SchemaVersion)
	}
	allowed := make(map[string]bool, len(exportTables))
	for _, t := range exportTables {
		allowed[t] = true
	}
	for t := range ed.Tables {
		if !allowed[t] {
			return nil, fmt.Errorf("%w: unsupported table %q", ErrInvalidExport, t)
		}
	}
	confirmed := len(confirm) > 0 && confirm[0]

	s, err := store.Open(filepath.Join(dir, DBFile))
	if err != nil {
		return nil, fmt.Errorf("open target database: %w", err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate target database: %w", err)
	}
	var version int
	if err := s.DB.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err != nil {
		return nil, fmt.Errorf("read target schema version: %w", err)
	}
	if version > store.SchemaVersion {
		return nil, fmt.Errorf("%w: target schema %d > supported %d", ErrVersionMismatch, version, store.SchemaVersion)
	}
	hasData, err := targetHasData(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	if hasData && !confirmed {
		return nil, ErrConfirmRequired
	}

	result := &ImportResult{
		DataDir:  dir,
		Mode:     mode,
		Tables:   make(map[string]TableImportStat, len(ed.Tables)),
		Redacted: ed.Redacted,
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin import tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, table := range exportTables {
		et, ok := ed.Tables[table]
		if !ok {
			continue
		}
		if table == "audit_log" && ed.Redacted {
			stat := TableImportStat{Rows: len(et.Rows), Skipped: len(et.Rows)}
			result.Tables[table] = stat
			result.TotalSkipped += stat.Skipped
			result.Warnings = append(result.Warnings, "redacted audit_log rows are not imported because their hash chain cannot be re-verified; use an unredacted export for migration")
			continue
		}
		known, err := tableColumns(ctx, tx, table)
		if err != nil {
			return nil, err
		}
		if len(et.Columns) == 0 {
			return nil, fmt.Errorf("%w: %s has no columns", ErrInvalidExport, table)
		}
		for _, c := range et.Columns {
			if !known[c] {
				return nil, fmt.Errorf("%w: %s unknown column %q", ErrInvalidExport, table, c)
			}
		}
		stat := TableImportStat{Rows: len(et.Rows)}
		for i, row := range et.Rows {
			for k := range row {
				if !known[k] {
					return nil, fmt.Errorf("%w: %s row %d unknown column %q", ErrInvalidExport, table, i+1, k)
				}
			}
			inserted, err := insertRow(ctx, tx, table, et.Columns, row, mode)
			if err != nil {
				return nil, err
			}
			if inserted {
				stat.Inserted++
			} else {
				stat.Skipped++
			}
		}
		result.Tables[table] = stat
		result.TotalInserted += stat.Inserted
		result.TotalSkipped += stat.Skipped
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit import: %w", err)
	}
	auditCount, auditErr := s.VerifyAuditChain(ctx)
	result.AuditEntries = auditCount
	if auditErr == nil {
		result.AuditChainVerified = true
	} else if !ed.Redacted {
		return result, fmt.Errorf("%w: audit chain after import: %v", ErrCorrupt, auditErr)
	} else {
		result.Warnings = append(result.Warnings, "target audit chain is not valid; redacted audit rows were not imported")
	}
	return result, nil
}

func targetHasData(ctx context.Context, db *sql.DB) (bool, error) {
	for _, table := range exportTables {
		var n int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM \""+table+"\"").Scan(&n); err != nil {
			return false, fmt.Errorf("count %s: %w", table, err)
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}

func tableColumns(ctx context.Context, tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info(\""+table+"\")")
	if err != nil {
		return nil, fmt.Errorf("table_info %s: %w", table, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notNull int
			dflt    any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan table_info %s: %w", table, err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: table %s missing", ErrInvalidExport, table)
	}
	return out, nil
}

func insertRow(ctx context.Context, tx *sql.Tx, table string, columns []string, row map[string]any, mode ImportMode) (bool, error) {
	quoted := make([]string, len(columns))
	marks := make([]string, len(columns))
	args := make([]any, len(columns))
	for i, c := range columns {
		quoted[i] = quoteIdent(c)
		marks[i] = "?"
		args[i] = row[c]
	}
	verb := "INSERT"
	if mode == ImportSkip {
		verb = "INSERT OR IGNORE"
	}
	q := verb + " INTO " + quoteIdent(table) + "(" + strings.Join(quoted, ",") + ") VALUES(" + strings.Join(marks, ",") + ")"
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		if isConflictErr(err) {
			return false, fmt.Errorf("%w: %s: %v", ErrConflict, table, err)
		}
		return false, fmt.Errorf("insert %s: %w", table, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected %s: %w", table, err)
	}
	return n > 0, nil
}

func isConflictErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique constraint") || strings.Contains(s, "primary key") || strings.Contains(s, "constraint failed")
}

func quoteIdent(s string) string { return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\"" }

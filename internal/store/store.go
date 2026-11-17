package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const SchemaVersion = 1

type Store struct {
	DB   *sql.DB
	Path string
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store dir: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return &Store{DB: db, Path: path}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}
	var version int
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version >= SchemaVersion {
		return nil
	}
	if _, err := s.DB.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema v%d: %w", SchemaVersion, err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?,?)`, SchemaVersion, Now()); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	return nil
}

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (s *Store) Begin(ctx context.Context) (*sql.Tx, error) { return s.DB.BeginTx(ctx, nil) }

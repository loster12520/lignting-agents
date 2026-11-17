package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFiles embed.FS

// SchemaVersion 是内嵌 schema 的迁移版本，保存在 schema_migrations。
const SchemaVersion = 1

// 仓储层错误。平台层负责把它们映射为 HTTP 错误码；store 不做业务规则。
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: conflict")
	ErrInvalid  = errors.New("store: invalid argument")
)

// Store 是 SQLite 的唯一入口。DB 只在 store 内部和 core 的事务内使用；
// platform 不直接拼业务 SQL。
type Store struct {
	DB   *sql.DB
	Path string

	mu        sync.RWMutex
	indexer   DocIndexer
	eventHook func()
}

// Open 打开数据库。v0.0.1 单用户本地应用，所有事务使用 BEGIN IMMEDIATE，
// 由 SQLite 写锁串行化，保证消息 seq、事件 seq、审计 hash 链单调一致。
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store dir: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
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
	return &Store{DB: db, Path: path}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// SchemaSQL 返回内嵌的 schema（供测试/诊断只读使用）。
func SchemaSQL() (string, error) {
	b, err := schemaFiles.ReadFile("schema.sql")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Migrate 幂等应用内嵌 schema。schema.sql 本身就是 v1 迁移；
// 后续版本以 migration 文件追加，不修改已冻结的 schema.sql。
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}
	var version int
	if err := s.DB.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version >= SchemaVersion {
		return nil
	}
	schemaSQL, err := SchemaSQL()
	if err != nil {
		return fmt.Errorf("read embedded schema: %w", err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema v%d: %w", SchemaVersion, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(?,?)", SchemaVersion, Now()); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

// Now 返回统一时间格式：RFC3339 UTC + 固定 9 位小数秒，
// 保证字符串排序 = 时间排序（RFC3339Nano 会裁掉尾零导致字典序错乱）。
func Now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z07:00") }

func (s *Store) Begin(ctx context.Context) (*sql.Tx, error) { return s.DB.BeginTx(ctx, nil) }

// WithTx 在写事务中执行 fn；fn 返回错误则回滚。core 可用它组合跨表写入。
// 事务为 IMMEDIATE，写操作全局串行，满足「先写库再发事件」与单调 seq。
func (s *Store) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// DocIndexer 由 internal/search 实现并在启动时注入；在写入事务内同步更新索引。
// 未注入时（纯 store 单测）索引调用为 no-op。
type DocIndexer interface {
	IndexTx(tx *sql.Tx, doc IndexDoc) error
	DeleteTx(tx *sql.Tx, docID string) error
}

// IndexDoc 是可检索文档的最小投影。
type IndexDoc struct {
	DocID          string
	Kind           string
	RefID          string
	ConversationID string
	DepartmentID   string
	Title          string
	Body           string
	Visibility     string
	CreatedAt      string
}

// MessageDocID 返回消息文档的稳定 doc_id。
func MessageDocID(messageID string) string { return "message:" + messageID }

// SetIndexer 注入索引实现（启动时一次）。
func (s *Store) SetIndexer(idx DocIndexer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.indexer = idx
}

func (s *Store) indexDocTx(tx *sql.Tx, doc IndexDoc) error {
	s.mu.RLock()
	idx := s.indexer
	s.mu.RUnlock()
	if idx == nil {
		return nil
	}
	return idx.IndexTx(tx, doc)
}

func (s *Store) deleteDocTx(tx *sql.Tx, docID string) error {
	s.mu.RLock()
	idx := s.indexer
	s.mu.RUnlock()
	if idx == nil {
		return nil
	}
	return idx.DeleteTx(tx, docID)
}

// SetEventHook 注入事件通知器：事务提交后唤醒 SSE 连接。
// 回调必须非阻塞，不能反向调用 store。
func (s *Store) SetEventHook(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventHook = fn
}

func (s *Store) notifyEvent() {
	s.mu.RLock()
	fn := s.eventHook
	s.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

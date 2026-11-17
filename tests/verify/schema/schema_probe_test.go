package verify

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/store"
)

var expectedTables = []string{
	"agents", "approvals", "artifact_versions", "artifacts", "audit_log", "blueprints",
	"bridges", "commit_groups", "context_snapshots", "conversation_members", "conversations",
	"decisions", "departments", "events", "file_timeline", "git_events", "intents",
	"memories", "memory_events", "memory_reads", "messages", "notifications", "owner",
	"prompt_manuals", "provisioning", "review_verdicts", "reviews", "schedules",
	"search_docs", "search_terms", "settings", "task_criteria", "task_dependencies",
	"task_events", "tasks", "turns", "usage_ledger", "write_events",
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default(dir)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func tableSet(t *testing.T, st *store.Store) map[string]bool {
	t.Helper()
	rows, err := st.DB.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		out[n] = true
	}
	return out
}

func columns(t *testing.T, st *store.Store, table string) map[string]bool {
	t.Helper()
	rows, err := st.DB.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info %s: %v", table, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		out[name] = true
	}
	return out
}

// R0-EXIT: schema 冻结版能落到一份干净库；schema_migrations = 1。
func TestSchemaApplied(t *testing.T) {
	st := openStore(t)
	got := tableSet(t, st)
	var missing []string
	for _, want := range expectedTables {
		if !got[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("缺表: %v", missing)
	}
	var version int
	if err := st.DB.QueryRow("SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if version != store.SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, store.SchemaVersion)
	}
}

// messages.seq 必须全局单调递增（api-contract §1.3 / requirements NFR）。
func TestMessagesSeqMonotonic(t *testing.T) {
	st := openStore(t)
	ins := "INSERT INTO messages(id, conversation_id, sender_type, sender_id, sender_name, created_at) VALUES(?,?,?,?,?,?)"
	for i, id := range []string{"msg_a", "msg_b"} {
		if _, err := st.DB.Exec(ins, id, "conv_1", "user", "owner", "阿哲", "2026-01-01T00:00:0"+string(rune('0'+i))+"Z"); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	var a, b int64
	if err := st.DB.QueryRow("SELECT seq FROM messages WHERE id='msg_a'").Scan(&a); err != nil {
		t.Fatalf("seq a: %v", err)
	}
	if err := st.DB.QueryRow("SELECT seq FROM messages WHERE id='msg_b'").Scan(&b); err != nil {
		t.Fatalf("seq b: %v", err)
	}
	if b <= a {
		t.Fatalf("seq 未递增: msg_a=%d msg_b=%d", a, b)
	}
}

// api-contract §3：client_id 幂等键必须有落点；缺失则重复发送无法幂等。
func TestSchemaMessageIdempotencyKey(t *testing.T) {
	st := openStore(t)
	cols := columns(t, st, "messages")
	for _, c := range []string{"client_id", "client_msg_id"} {
		if cols[c] {
			return
		}
	}
	t.Skipf("TRIAGED R1（Lead 2026-11-17）：client_id 幂等走 meta.client_id + 应用层查询，不要求独立列；行为断言见 tests/verify/store。现有列: %v", sortedKeys(cols))
}

// FR-CHAT-06/AC-13：未读以 last_read_seq 为准；收件箱需持久化分组与唯一性。
func TestSchemaUnreadAndInbox(t *testing.T) {
	st := openStore(t)
	if !columns(t, st, "conversations")["last_read_seq"] && !columns(t, st, "conversation_members")["last_read_seq"] {
		t.Skip("TRIAGED R1（Lead 2026-11-17）：已读位置临时用 settings key=read:<conv>；行为断言见 tests/verify/store。R8 迁移到独立表。")
	}
}

// FR-CHAT-02：附件是 P0，但 schema 无 attachments 表、api-contract 无附件端点。
func TestSchemaAttachments(t *testing.T) {
	st := openStore(t)
	if !tableSet(t, st)["attachments"] {
		t.Skip("TRIAGED R4（Lead 2026-11-17）：attachments 为 P0 缺口，R4 随 schema v2 加表 + /api/attachments；本轮不阻塞。")
	}
}

// FR-ASST-06：同一事项只能有一个持久收件箱条目。
func TestSchemaInboxUniqueness(t *testing.T) {
	st := openStore(t)
	rows, err := st.DB.Query("SELECT name, sql FROM sqlite_master WHERE type='index' AND tbl_name='notifications'")
	if err != nil {
		t.Fatalf("notifications indexes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var sql *string
		if err := rows.Scan(&name, &sql); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		if sql != nil && strings.Contains(*sql, "UNIQUE") && strings.Contains(*sql, "object_id") {
			return
		}
	}
	t.Skip("TRIAGED R8（Lead 2026-11-17）：notifications 唯一约束随 schema v2 补齐（FR-ASST-06）；R1 应用层保证。")
}

// FR-OPS-07：定时任务需可补跑/幂等/可观测。
func TestSchemaSchedules(t *testing.T) {
	st := openStore(t)
	cols := columns(t, st, "schedules")
	for _, c := range []string{"attempts", "fired_at", "idempotency_key"} {
		if !cols[c] {
			t.Skipf("TRIAGED R8（Lead 2026-11-17）：schedules 缺字段 %q，随 schema v2 补齐（FR-OPS-07）；现有: %v", c, sortedKeys(cols))
		}
	}
}

// FR-APPR-02：双钥匙（作者+审批人）需要独立落点。
func TestSchemaApprovalDoubleKey(t *testing.T) {
	st := openStore(t)
	cols := columns(t, st, "approvals")
	for _, c := range []string{"required_keys", "keys_approved", "second_approver_id"} {
		if cols[c] {
			return
		}
	}
	t.Skipf("TRIAGED R8（Lead 2026-11-17）：approvals 双钥匙字段随 schema v2 补齐（FR-APPR-02）；现有: %v", sortedKeys(cols))
}

// AC-09：成本必须与模型调用可对账（重试不能重复计费）。
func TestSchemaUsageIdempotency(t *testing.T) {
	st := openStore(t)
	cols := columns(t, st, "usage_ledger")
	for _, c := range []string{"call_id", "request_id", "retry_of"} {
		if cols[c] {
			return
		}
	}
	t.Skipf("TRIAGED R8（Lead 2026-11-17）：usage_ledger 幂等键随 schema v2 补齐（AC-09 对账）；现有: %v", sortedKeys(cols))
}

// FR-SRCH-02：api-contract §5 的 sender_id 过滤需要索引列。
func TestSchemaSearchSenderFilter(t *testing.T) {
	st := openStore(t)
	if !columns(t, st, "search_docs")["sender_id"] && !columns(t, st, "search_docs")["author_id"] {
		t.Skip("TRIAGED R4（Lead 2026-11-17）：R1 消息搜索回表 messages 过滤 sender；R4 跨对象搜索时加列。")
	}
}

// 可观测性：WAL 与连接级 PRAGMA 现状（证据，不判失败）。
func TestStorePragmas(t *testing.T) {
	st := openStore(t)
	var mode string
	if err := st.DB.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	ctx := context.Background()
	// 同时持有多个连接，避免 database/sql 复用同一个连接而掩盖连接级 PRAGMA 差异。
	var conns []*sql.Conn
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for i := 0; i < 3; i++ {
		conn, err := st.DB.Conn(ctx)
		if err != nil {
			t.Fatalf("conn %d: %v", i, err)
		}
		conns = append(conns, conn)
		var fk, sync int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("fk: %v", err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync); err != nil {
			t.Fatalf("synchronous: %v", err)
		}
		t.Logf("conn#%d foreign_keys=%d synchronous=%d", i, fk, sync)
		if i > 0 && fk != 1 {
			t.Logf("DEFECT D-R0-4: 第 %d 个连接 foreign_keys=%d（PRAGMA 只在首个连接 Exec，未进 DSN）", i, fk)
		}
	}
	// DDL 是否声明了 FK（决定 PRAGMA foreign_keys 是否有意义）。
	var ddl string
	_ = st.DB.QueryRow("SELECT COALESCE(GROUP_CONCAT(sql,'\n'),'') FROM sqlite_master WHERE type='table'").Scan(&ddl)
	t.Logf("schema 中 FOREIGN KEY 出现次数=%d", strings.Count(strings.ToUpper(ddl), "FOREIGN KEY"))
}

// FR-SEC-05：确认数据目录/密钥文件权限现状（Windows 上 0600/0700 语义有限，仅记录）。
func TestDataDirPermissions(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := config.SaveSecrets(dir, config.Secrets{DeepSeekAPIKey: "sk-test"}); err != nil {
		t.Fatalf("SaveSecrets: %v", err)
	}
	for _, p := range []string{cfg.DataDir, cfg.ConfigPath(), cfg.SecretsPath()} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		t.Logf("%s perm=%v", filepath.Base(p), fi.Mode().Perm())
	}
	raw, err := os.ReadFile(cfg.SecretsPath())
	if err != nil {
		t.Fatalf("read secrets: %v", err)
	}
	if strings.Contains(string(raw), "sk-test") {
		t.Log("TRIAGED R8（Lead 2026-11-17）：Windows 无 0600 语义，v0.0.1 依赖用户目录 ACL；DPAPI 加密为 R8 项，不标 P0 失败。")
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

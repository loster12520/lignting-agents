//go:build integration

package memory

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/store"
)

// 编译期契约：platform-dev 在 *store.Store 上的 12 个方法必须满足 memory.Repo
// （参数/返回值全部 domain 类型，store 不需要 import memory）。
var _ Repo = (*store.Store)(nil)

func newSQLiteMemory(t *testing.T, mutate func(*Config)) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := DefaultConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	svc, err := NewService(Options{Repo: NewStoreRepo(st), Clock: newFakeClock(), Config: cfg, Compressor: &fakeCompressor{}})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return svc, st
}

func TestIntegrationLifecycle(t *testing.T) {
	svc, st := newSQLiteMemory(t, nil)
	sup := supervisor("sup_1", "dept_a")

	m1 := mustActive(t, svc, baseCandidate("t1"), sup)

	got, err := svc.Assemble(ctx, InjectionRequest{
		TurnID: "turn_1", AgentID: "sup_1", AgentKind: ActorSupervisor,
		DepartmentID: "dept_a", IncludeArchives: true, EffectiveInputTokens: 8000,
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if !containsID(got.Memories, m1.ID) {
		t.Fatalf("真实仓储下应注入: %+v", got.Memories)
	}

	// 读痕落库。
	var reads int
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM memory_reads WHERE memory_id = ?", m1.ID).Scan(&reads); err != nil {
		t.Fatalf("query reads: %v", err)
	}
	if reads != 1 {
		t.Fatalf("memory_reads 应有 1 条，得到 %d", reads)
	}

	// 双时间：新版本批准后旧版本 valid_to 落库。
	c2 := baseCandidate("t1")
	c2.Content = "后端能力升级：Go + SQLite + 分布式"
	sub, err := svc.SubmitCandidate(ctx, c2, sup)
	if err != nil {
		t.Fatalf("二次提交: %v", err)
	}
	m2 := mustApprove(t, svc, sub.Memory.ID, sup)
	old, err := svc.repo.GetMemory(ctx, m1.ID)
	if err != nil {
		t.Fatalf("读取旧版: %v", err)
	}
	if strings.TrimSpace(old.ValidTo) == "" {
		t.Fatal("旧版本 valid_to 应落库")
	}
	if old.Content != m1.Content {
		t.Fatal("旧版本内容不可被覆盖")
	}

	// 墓碑：任何读取路径不可见。
	if err := svc.Delete(ctx, m2.ID, "用户删除", human("user_1")); err != nil {
		t.Fatalf("删除: %v", err)
	}
	if _, err := svc.Get(ctx, m2.ID, human("user_1")); err == nil {
		t.Fatal("墓碑不可读")
	}
	got, _ = svc.Assemble(ctx, InjectionRequest{
		TurnID: "turn_2", AgentID: "sup_1", AgentKind: ActorSupervisor,
		DepartmentID: "dept_a", IncludeArchives: true, EffectiveInputTokens: 8000,
	})
	if containsID(got.Memories, m2.ID) {
		t.Fatal("删除后不得注入")
	}
	var tomb int
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories WHERE id = ? AND status = 'deleted' AND deleted_at <> ''", m2.ID).Scan(&tomb); err != nil {
		t.Fatalf("query tombstone: %v", err)
	}
	if tomb != 1 {
		t.Fatal("墓碑行应保留")
	}
}

func TestIntegrationErrorMapping(t *testing.T) {
	svc, _ := newSQLiteMemory(t, nil)
	if _, err := svc.repo.GetMemory(ctx, "mem_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMemory 未命中应映射为 memory.ErrNotFound，得到 %v", err)
	}
	sup := supervisor("sup_1", "dept_a")
	m := mustActive(t, svc, baseCandidate("t1"), sup)
	if err := svc.repo.UpdateMemory(ctx, m, 999); !errors.Is(err, ErrConflict) {
		t.Fatalf("版本不符应映射为 memory.ErrConflict，得到 %v", err)
	}
	if err := svc.repo.InsertMemory(ctx, m); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复主键应映射为 memory.ErrConflict，得到 %v", err)
	}
}

func TestIntegrationManualLifecycle(t *testing.T) {
	svc, _ := newSQLiteMemory(t, nil)
	key := deptKey()
	v1, err := svc.ProposeManual(ctx, key, manualV1, nil, human("user_1"))
	if err != nil {
		t.Fatalf("propose v1: %v", err)
	}
	if _, err := svc.ApproveManual(ctx, v1.ID, human("user_1")); err != nil {
		t.Fatalf("approve v1: %v", err)
	}
	v2, err := svc.ProposeManual(ctx, key, manualV2, nil, human("user_1"))
	if err != nil {
		t.Fatalf("propose v2: %v", err)
	}
	if _, err := svc.ApproveManual(ctx, v2.ID, human("user_1")); err != nil {
		t.Fatalf("approve v2: %v", err)
	}
	versions, err := svc.ListManualVersions(ctx, key)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("应有 2 个版本，得到 %d", len(versions))
	}
	// ListManuals 不保证顺序（store 返回 version desc，fake 返回 asc）；调用方必须按 Version 处理。
	byVersion := map[int]string{}
	for _, v := range versions {
		byVersion[v.Version] = v.Status
	}
	if byVersion[1] != ManualStatusSuperseded || byVersion[2] != ManualStatusApproved {
		t.Fatalf("版本状态不正确: %+v", byVersion)
	}
	rolled, err := svc.RollbackManual(ctx, key, 1, human("user_1"))
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rolled.Version != 3 || rolled.Content != manualV1 || rolled.Status != ManualStatusApproved {
		t.Fatalf("回滚结果不正确: %+v", rolled)
	}
	cur, ok, err := svc.CurrentApprovedManual(ctx, key)
	if err != nil || !ok || cur.Version != 3 {
		t.Fatalf("生效版本应为 v3: %+v err=%v", cur, err)
	}
}

func TestIntegrationCompressionSnapshot(t *testing.T) {
	svc, st := newSQLiteMemory(t, func(c *Config) {
		c.StorageMaxChars = 1000
		c.CompressTargetChars = 300
		c.CandidateMaxChars = 2000
	})
	sup := supervisor("sup_1", "dept_a")
	c := baseCandidate("")
	c.Content = strings.Repeat("能", 1200)
	c.Summary = "长档案"
	sub, err := svc.SubmitCandidate(ctx, c, sup)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	res, err := svc.Approve(ctx, sub.Memory.ID, sup)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !res.OverLimit {
		t.Fatal("应识别超限")
	}
	updated, err := svc.Compress(ctx, sub.Memory.ID, 300, "auto", Access{Type: ActorSystem, ID: "memory"})
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if RuneLen(updated.Content) > 300 {
		t.Fatalf("压缩后超目标: %d", RuneLen(updated.Content))
	}
	// 快照必须逐字落库（detail 含 prev_content 全文与 prev_chars）。
	var detail string
	if err := st.DB.QueryRowContext(ctx,
		"SELECT detail FROM memory_events WHERE memory_id = ? AND action = 'snapshot'", sub.Memory.ID).Scan(&detail); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	for _, want := range []string{"prev_content", "prev_chars", "prev_version", "target_chars", "auto"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("快照 detail 缺字段 %q", want)
		}
	}
	if !strings.Contains(detail, strings.Repeat("能", 100)) {
		t.Fatal("快照必须保存压缩前全文（不能被截断）")
	}
}

func TestIntegrationReadToolHumanOnly(t *testing.T) {
	svc, st := newSQLiteMemory(t, nil)
	m := humanOnlyMemory(t, svc)
	tool := NewReadTool(svc)
	res, err := tool.Invoke(ctx, assistant("asst_1"), ReadArgs{ID: m.ID, TurnID: "turn_h"})
	if err != nil {
		t.Fatalf("助理单条读取: %v", err)
	}
	if !res.Memory.Redacted {
		t.Fatal("human-only 必须脱敏")
	}
	var reads int
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM memory_reads WHERE memory_id = ? AND reader_type = 'assistant'", m.ID).Scan(&reads); err != nil {
		t.Fatalf("query reads: %v", err)
	}
	if reads != 1 {
		t.Fatalf("读取留痕应落库，得到 %d", reads)
	}
	var denied int
	if _, err := tool.Invoke(ctx, employee("emp_1", "dept_a"), ReadArgs{ID: m.ID, TurnID: "turn_e"}); err == nil {
		t.Fatal("员工不得读 human-only")
	}
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM memory_events WHERE memory_id = ? AND action = 'read_denied'", m.ID).Scan(&denied); err != nil {
		t.Fatalf("query denied: %v", err)
	}
	if denied == 0 {
		t.Fatal("越权拒绝必须留痕")
	}
}

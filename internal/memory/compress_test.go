package memory

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func compressTestService(t *testing.T) (*Service, *fakeRepo, *fakeClock, *fakeCompressor) {
	t.Helper()
	return newTestService(t, func(c *Config) {
		c.StorageMaxChars = 1000
		c.CompressTargetChars = 300
		c.CandidateMaxChars = 2000
	})
}

func longCandidate(content string) Candidate {
	c := baseCandidate("")
	c.Content = content
	c.Summary = "长档案"
	return c
}

func TestApproveOverLimitQueuesCompression(t *testing.T) {
	svc, _, _, _ := compressTestService(t)
	sup := supervisor("sup_1", "dept_a")
	c := longCandidate(strings.Repeat("能", 1200))
	sub, err := svc.SubmitCandidate(ctx, c, sup)
	if err != nil {
		t.Fatalf("提交: %v", err)
	}
	res, err := svc.Approve(ctx, sub.Memory.ID, sup)
	if err != nil {
		t.Fatalf("批准: %v", err)
	}
	if !res.OverLimit || !res.QueuedCompress {
		t.Fatalf("超限应标记并入队压缩: %+v", res)
	}
	if got := svc.PendingCompressions(); len(got) != 1 {
		t.Fatalf("待压缩队列应为 1，得到 %d", len(got))
	}
}

func TestCompressSnapshotAndSuccess(t *testing.T) {
	svc, repo, _, comp := compressTestService(t)
	sup := supervisor("sup_1", "dept_a")
	content := strings.Repeat("能", 1200)
	sub, _ := svc.SubmitCandidate(ctx, longCandidate(content), sup)
	if _, err := svc.Approve(ctx, sub.Memory.ID, sup); err != nil {
		t.Fatalf("批准: %v", err)
	}
	updated, err := svc.Compress(ctx, sub.Memory.ID, 300, "auto", Access{Type: ActorSystem, ID: "memory"})
	if err != nil {
		t.Fatalf("压缩: %v", err)
	}
	if RuneLen(updated.Content) > 300 {
		t.Fatalf("压缩后仍超目标: %d", RuneLen(updated.Content))
	}
	if comp.callCount() != 1 {
		t.Fatalf("应调用一次模型，得到 %d", comp.callCount())
	}
	ev, ok := repo.lastEvent(ActionSnapshot)
	if !ok {
		t.Fatal("压缩前必须写快照事件")
	}
	detail := eventDetailMap(t, ev)
	if detail["reason"] != "auto" {
		t.Fatalf("快照 reason 字段错误: %v", detail["reason"])
	}
	if int(detail["prev_chars"].(float64)) != 1200 {
		t.Fatalf("快照 prev_chars 应为 1200: %v", detail["prev_chars"])
	}
	if int(detail["prev_version"].(float64)) != 1 {
		t.Fatalf("快照 prev_version 应为 1: %v", detail["prev_version"])
	}
	if int(detail["target_chars"].(float64)) != 300 {
		t.Fatalf("快照 target_chars 应为 300: %v", detail["target_chars"])
	}
	if s, _ := detail["prev_content"].(string); s != content {
		t.Fatalf("快照必须保存压缩前全文（长度 %d）", len(s))
	}
	if _, ok := repo.lastEvent(ActionCompressed); !ok {
		t.Fatal("成功应记录 compressed 事件")
	}
}

func TestCompressFailureKeepsOriginalAndCoolsDown(t *testing.T) {
	svc, repo, clock, comp := compressTestService(t)
	sup := supervisor("sup_1", "dept_a")
	content := strings.Repeat("能", 1200)
	sub, _ := svc.SubmitCandidate(ctx, longCandidate(content), sup)
	if _, err := svc.Approve(ctx, sub.Memory.ID, sup); err != nil {
		t.Fatalf("批准: %v", err)
	}
	comp.fn = func(req CompressionRequest) (CompressionResult, error) {
		return CompressionResult{}, errors.New("model 502")
	}
	if _, err := svc.Compress(ctx, sub.Memory.ID, 300, "auto", Access{Type: ActorSystem, ID: "memory"}); err == nil {
		t.Fatal("模型失败应返回错误")
	}
	cur, _ := repo.GetMemory(ctx, sub.Memory.ID)
	if cur.Content != content {
		t.Fatal("失败必须保留原文")
	}
	if _, ok := repo.lastEvent(ActionCompressFailed); !ok {
		t.Fatal("应记录 compress_failed")
	}
	if _, err := svc.Compress(ctx, sub.Memory.ID, 300, "auto", Access{Type: ActorSystem, ID: "memory"}); !errors.Is(err, ErrCooldown) {
		t.Fatalf("冷却期内应拒绝，得到 %v", err)
	}
	clock.Advance(20 * time.Minute)
	comp.fn = nil
	if _, err := svc.Compress(ctx, sub.Memory.ID, 300, "auto", Access{Type: ActorSystem, ID: "memory"}); err != nil {
		t.Fatalf("冷却结束后应可重试: %v", err)
	}
}

func TestCompressOverTargetRejected(t *testing.T) {
	svc, repo, _, comp := compressTestService(t)
	sup := supervisor("sup_1", "dept_a")
	content := strings.Repeat("能", 1200)
	sub, _ := svc.SubmitCandidate(ctx, longCandidate(content), sup)
	if _, err := svc.Approve(ctx, sub.Memory.ID, sup); err != nil {
		t.Fatalf("批准: %v", err)
	}
	comp.fn = func(req CompressionRequest) (CompressionResult, error) {
		return CompressionResult{Content: strings.Repeat("压", 400)}, nil
	}
	if _, err := svc.Compress(ctx, sub.Memory.ID, 300, "auto", Access{Type: ActorSystem, ID: "memory"}); err == nil {
		t.Fatal("压缩后仍超目标应失败")
	}
	cur, _ := repo.GetMemory(ctx, sub.Memory.ID)
	if cur.Content != content {
		t.Fatal("未达标必须保留原文")
	}
	ev, ok := repo.lastEvent(ActionCompressRejected)
	if !ok {
		t.Fatal("应记录 compress_rejected")
	}
	detail := eventDetailMap(t, ev)
	if detail["reason"] != "over_target" {
		t.Fatalf("原因应为 over_target: %v", detail["reason"])
	}
	if int(detail["got_chars"].(float64)) != 400 {
		t.Fatalf("应记录实际字数 400: %v", detail["got_chars"])
	}
}

func TestCompressNoopDoesNotCallModel(t *testing.T) {
	svc, _, _, comp := compressTestService(t)
	sup := supervisor("sup_1", "dept_a")
	m := mustActive(t, svc, longCandidate(strings.Repeat("短", 100)), sup)
	got, err := svc.Compress(ctx, m.ID, 300, "manual", Access{Type: ActorSystem, ID: "memory"})
	if err != nil {
		t.Fatalf("no-op 压缩不应报错: %v", err)
	}
	if got.ID != m.ID || comp.callCount() != 0 {
		t.Fatalf("目标不小于当前字数时不应调用模型（calls=%d）", comp.callCount())
	}
}

func TestCompressWithoutModelDegrades(t *testing.T) {
	svc, _, _, _ := compressTestService(t)
	svc.compressor = nil
	if _, err := svc.Compress(ctx, "mem_x", 300, "auto", Access{Type: ActorSystem, ID: "memory"}); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("无模型应返回 ErrModelUnavailable，得到 %v", err)
	}
}

func TestCompressInFlightRejected(t *testing.T) {
	svc, _, _, _ := compressTestService(t)
	sup := supervisor("sup_1", "dept_a")
	c := longCandidate(strings.Repeat("能", 1200))
	sub, _ := svc.SubmitCandidate(ctx, c, sup)
	if !svc.gate.TryLock("compress:" + sub.Memory.ID) {
		t.Fatal("测试前置：应能占用锁")
	}
	if _, err := svc.Compress(ctx, sub.Memory.ID, 300, "auto", Access{Type: ActorSystem, ID: "memory"}); !errors.Is(err, ErrInFlight) {
		t.Fatalf("并发压缩应返回 ErrInFlight，得到 %v", err)
	}
	svc.gate.Unlock("compress:" + sub.Memory.ID)
}

func TestRunPendingCompressions(t *testing.T) {
	svc, _, _, _ := compressTestService(t)
	sup := supervisor("sup_1", "dept_a")
	for i := 0; i < 2; i++ {
		c := longCandidate(strings.Repeat("能", 1200))
		c.EntityKey = "k" + string(rune('a'+i))
		sub, _ := svc.SubmitCandidate(ctx, c, sup)
		if _, err := svc.Approve(ctx, sub.Memory.ID, sup); err != nil {
			t.Fatalf("批准: %v", err)
		}
	}
	done, failed := svc.RunPendingCompressions(ctx, 10)
	if done != 2 || failed != 0 {
		t.Fatalf("应处理 2 条: done=%d failed=%d", done, failed)
	}
	if len(svc.PendingCompressions()) != 0 {
		t.Fatal("处理完队列应为空")
	}
}

func TestCompressRejectsTombstone(t *testing.T) {
	svc, _, _, _ := compressTestService(t)
	sup := supervisor("sup_1", "dept_a")
	c := longCandidate(strings.Repeat("能", 1200))
	sub, _ := svc.SubmitCandidate(ctx, c, sup)
	if err := svc.Delete(ctx, sub.Memory.ID, "测试删除", human("user_1")); err != nil {
		t.Fatalf("删除: %v", err)
	}
	if _, err := svc.Compress(ctx, sub.Memory.ID, 300, "auto", Access{Type: ActorSystem, ID: "memory"}); err == nil {
		t.Fatal("墓碑不可压缩")
	}
}

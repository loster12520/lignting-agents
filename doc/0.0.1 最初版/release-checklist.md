# v0.0.1 发布检查清单

> 勾选前必须有证据（命令输出/报告路径/日期）。
> 最近一次复跑：**2026-11-18**（WSL2/Linux，Go 1.27.4；自动项全绿）。
> 状态与后续动作见 [release-status.md](release-status.md)。

## 构建与静态检查

- [x] `gofmt -l .` 为空 — 2026-11-18，无输出
- [x] `go vet ./...` 通过 — 2026-11-18，exit 0
- [x] `go build ./...` 通过 — 2026-11-18，exit 0
- [x] `go test ./...` 全绿 — 2026-11-18，8.2s，internal/*、tests/verify/* 全 ok
- [x] `go test -tags=acceptance ./tests/ac` 全绿且无 SKIP — 2026-11-18，12.3s，15 个 `TestAC*` PASS，0 Skip
- [x] `go test ./tests/verify/e2e` 真实二进制 E2E 全绿 — 2026-11-18，7.8s（含 R9 断线演练），exit 0
- [x] 单二进制可启动：`--version` 输出 `lignting 0.0.1`；`/api/health` 返回 `schema_version` — 2026-11-18，
  `--version` = `lignting 0.0.1`，health 200（schema_version=3）
- [x] 发布二进制手工冒烟：`setup → bootstrap → 建会话 → 发消息 → 列表` — 2026-11-18，真实二进制（mock 模式，
  127.0.0.1:18891）+ `scripts/smoke.sh`：health 200（schema_version=3）→ setup 200 → bootstrap `authenticated=true`
  → 建项目会话 200 → 发消息 200（seq=2）→ 列表 total≥1 → 检索 total=1 → 审计 5 条 → SSE 200 `text/event-stream`

## 验收

- [x] AC-01..15 全部 PASS（记录命令、模式、耗时） — `go test -tags=acceptance ./tests/ac`，mock 模式，12.3s，0 Skip
- [x] 真实 Key 模式：AC-01/03/07 人工复跑 PASS — 2026-11-18，`AC_MODEL_MODE=deepseek` +
  `DEEPSEEK_API_KEY`（owner 提供，仅环境变量注入，未落盘/未入库）：`go test -tags=acceptance ./tests/ac
  -run 'TestAC01|TestAC03|TestAC07' -v` → PASS 8.9s（AC-01 2.78s 为真实网络调用、AC-03 0.04s、AC-07 6.06s）。
  **同时发现并修复假通过**：此前 `AC_MODEL_MODE=deepseek` 并没有接线真实客户端（`Start()` 里 Provider 为 nil 时
  一律回落 `deepseek.NewMock()`），所以「真实 Key 验收」跑的是 mock；已修 harness 并让 AC-01 不再断言 mock 措辞、
  AC-07 超时用 scripted provider 确定性注入。
- [x] 崩溃恢复：真实 SIGKILL + 重启；running turn → interrupted；消息 0 丢失；写事件 unknown 不自动重试 — AC-07 自动化（`tests/ac/ac07_recovery_test.go` 的 `real_process_kill_and_model_timeout_R2_R8`）
- [x] 断线补齐自动化：SSE `since` 重放不重不漏 — AC-07 + 新增 `TestR9OfflineCatchUpDrill`：断线窗口灌 3600 条事件（≈1h@1/s），
  重连 16.9ms 补齐 3604 条，id 连续、0 重复、3600/3600 命中
- [ ] 断线补齐人工项：真实浏览器断网 1 小时演练记录 — **未执行**（需人工操作浏览器断网 1h）；
  自动化等价演练已通过（见上一条），步骤见 [docs/testing/ac-guide.md](../../docs/testing/ac-guide.md) 第 7 节
- [x] 安全：CSRF/Origin/Agent Token/临时桥/审计链/紧急停止 均有断言 — AC-08 自动化
- [x] 预算：80%/100% 暂停生效；usage 无重复计费 — AC-09 自动化
- [x] 备份/导出/导入：恢复到新 data-dir 成功，manifest/hash 校验通过 — AC-10 自动化（schema_version 断言改为跟随 `store.SchemaVersion`，本次 3）

## 性能

- [x] 10 万消息：中文 2 字 p95 ≤150ms、普通 ≤300ms、写入 ≤100ms 可见、Recall@10 ≥0.8、重建耗时可接受 —
  2026-11-18 `TestSearchPerf100k` PASS，76.5s：中文 2 字 p95 72.8/99.9ms；普通查询最大 p95 138.5ms；
  写入后可见 0.32ms；Recall@10 = 20/20 = 1.00；重建 100,001 文档 27.1s
- [x] 结果写入 [docs/ops/search-benchmark.md](../../docs/ops/search-benchmark.md) — 2026-11-18，含逐项 p50/p95 与历史 O(N²) 修复说明

## 文档

- [x] README 快速启动可用 — 已更新开发/二进制两种启动方式与验收命令（2026-11-18 补 Linux 冒烟脚本）
- [x] docs/user：quickstart/concepts/faq/troubleshooting/demo — 5 篇均在
- [x] docs/ops：backup-restore/export-import/search-benchmark — 均在（search-benchmark 已填 2026-11-18 实数）
- [x] docs/testing：ac-guide/fault-injection — 均在（ac-guide 补 R9 断线演练与 Linux 冒烟）
- [x] release-notes / p1-backlog / CHANGELOG — 均在；CHANGELOG 补「修复」段（O(N²)、基准假通过、断线演练）
- [x] 无未记录审批；审计链可校验；P1 清单与规避方案已列 — 2026-11-18 整体复核：审批/双钥匙/批量均有审计断言（AC-08/AC-13），
  审计链由 `store.VerifyAuditChain` 在备份恢复流程与单测（`internal/store/store_test.go`、AC-01）中校验，
  P1 清单见 [p1-backlog.md](p1-backlog.md)

## 仓库

- [x] 构建产物不入库 — `bin/`、`tests/verify/bin/`、`*.log` 均在 `.gitignore`
- [x] 工作区干净（`git status` 无未提交改动） — 2026-11-18 收尾提交后确认
- [x] 版本号 `internal/config.Version = 0.0.1`；tag `v0.0.1` — 版本号已确认；tag 已在收尾提交上创建（见 release-status）

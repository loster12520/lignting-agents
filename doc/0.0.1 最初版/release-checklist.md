# v0.0.1 发布检查清单

> 勾选前必须有证据（命令输出/报告路径/日期）。
> 最近一次复跑：**2026-11-17**（自动项全绿；人工/手工项仍为 `[ ]`，属发布阻塞项）。
> 状态与后续动作见 [release-status.md](release-status.md)。

## 构建与静态检查

- [x] `gofmt -l .` 为空 — 2026-11-17，无输出
- [x] `go vet ./...` 通过 — 2026-11-17，exit 0
- [x] `go build ./...` 通过 — 2026-11-17，exit 0
- [x] `go test ./...` 全绿 — 2026-11-17，exit 0（internal/*、tests/verify/* 全 ok）
- [x] `go test -tags=acceptance ./tests/ac` 全绿且无 SKIP — 2026-11-17，15.0s，15 个 `TestAC*` PASS，0 Skip
- [x] `go test ./tests/verify/e2e` 真实二进制 E2E 全绿 — 2026-11-17，1.9s，exit 0
- [x] 单二进制可启动：`lignting --version` 输出 `lignting 0.0.1`；`/api/health` 返回 `schema_version` — `--version` 已验证；health 由 AC-01/AC-07 与 E2E 断言
- [x] 发布二进制手工冒烟：`setup → bootstrap → 建会话 → 发消息` — 2026-11-17，真实二进制（mock 模式，127.0.0.1:18891）：`/api/health` 200（schema_version=2）→ `POST /api/setup` 200 → bootstrap `authenticated=true` → 建项目会话 200 → 发消息 200（seq=2）→ 列表 total=1。上一轮 `setup` 400 确认为 PowerShell 向原生程序传 JSON 时引号被剥离，非产品缺陷

## 验收

- [x] AC-01..15 全部 PASS（记录命令、模式、耗时） — `go test -tags=acceptance ./tests/ac`，mock 模式，15.0s
- [ ] 真实 Key 模式：AC-01/03/07 人工复跑 PASS — **未执行**：开发机无 `DEEPSEEK_API_KEY`；需在有 Key 环境按 `AC_MODEL_MODE=deepseek` 复跑
- [x] 崩溃恢复：真实 SIGKILL + 重启；running turn → interrupted；消息 0 丢失；写事件 unknown 不自动重试 — AC-07 自动化（`tests/ac/ac07_recovery_test.go` 的 `real_process_kill_and_model_timeout_R2_R8`）
- [ ] 断线补齐：SSE `since` 重放不重不漏（已自动化）+ 1h 断线人工演练记录 — **人工演练未做**，步骤见 [docs/testing/ac-guide.md](../../docs/testing/ac-guide.md)
- [x] 安全：CSRF/Origin/Agent Token/临时桥/审计链/紧急停止 均有断言 — AC-08 自动化
- [x] 预算：80%/100% 暂停生效；usage 无重复计费 — AC-09 自动化
- [x] 备份/导出/导入：恢复到新 data-dir 成功，manifest/hash 校验通过 — AC-10 自动化

## 性能

- [ ] 10 万消息：中文 2 字 p95 ≤150ms、普通 ≤300ms、写入 ≤100ms 可见、Recall@10 ≥0.8、重建耗时可接受 — **未完成**：2026-11-17 本机跑 >20 分钟未结束（CPU 满载，临时库 ~67MB 已 seed，疑似卡在查询/Recall/Rebuild），已终止；需先定位热点
- [ ] 结果写入 [docs/ops/search-benchmark.md](../../docs/ops/search-benchmark.md) — **待填**（当前仅有方法，无数字）

## 文档

- [x] README 快速启动可用 — 已更新开发/二进制两种启动方式与验收命令
- [x] docs/user：quickstart/concepts/faq/troubleshooting/demo — 5 篇均在
- [x] docs/ops：backup-restore/export-import/search-benchmark — 均在（search-benchmark 待填数字）
- [x] docs/testing：ac-guide/fault-injection — 均在
- [x] release-notes / p1-backlog / CHANGELOG — 均已新增
- [ ] 无未记录审批；审计链可校验；P1 清单与规避方案已列 — 审计链由 AC-08 断言，P1 已列；**发布前整体复核未做**

## 仓库

- [x] 构建产物不入库 — 本次从跟踪中移除 `tests/verify/bin/*.exe`（60MB）并加入 `.gitignore`
- [ ] 工作区干净（`git status` 无未提交改动） — 本次临时提交后复查
- [ ] 版本号 `internal/config.Version = 0.0.1`；tag `v0.0.1` — 版本号已确认；**tag 未创建**

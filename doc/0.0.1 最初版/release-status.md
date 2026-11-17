# v0.0.1 发布收尾 — 工作状态快照

- 日期：2026-11-18（上一轮中断于 2026-11-17 发布冒烟；本轮在 WSL2/Linux 上收尾）
- 仓库：`/home/lignting/project/github/lignting-agents`，分支 `dev`，remote `origin`
  （`https://github.com/loster12520/lignting-agents`）
- 工具链：Go 1.27.4（mise 安装）、modernc.org/sqlite（纯 Go）、前端零构建内嵌
- 本次提交性质：发布收尾提交，打 tag `v0.0.1`

---

## 1. 已完成并验证（2026-11-18 复跑证据）

R1–R8 按 [implementation-plan.md](implementation-plan.md) 的 R 表全部完成；R9 发布收尾在本轮完成。

| 验证项 | 命令 | 结果 |
|---|---|---|
| 格式 | `gofmt -l .` | 无输出 |
| 静态检查 | `go vet ./...` | exit 0 |
| 构建 | `go build ./...` | exit 0 |
| 单元/集成 | `go test ./...` | 8.2s，全 ok |
| 验收 | `go test -tags=acceptance ./tests/ac` | 12.3s，15 个 `TestAC*` PASS，0 Skip |
| 真实二进制 E2E | `go test ./tests/verify/e2e` | 7.8s，全 PASS（含新增 R9 断线演练） |
| 发布二进制冒烟 | `./scripts/smoke.sh` | PASS：schema_version=3、setup/bootstrap 通过、建会话、发消息 seq=2、检索 total=1、审计 5 条、SSE 200 |
| 10 万检索基准 | `go test -tags=perf ./tests/bench -run TestSearchPerf100k` | **PASS 76.5s**（详见 docs/ops/search-benchmark.md） |
| 断线补齐演练 | `go test ./tests/verify/e2e -run TestR9OfflineCatchUpDrill` | PASS：断线灌 3600 条事件，重连 16.9ms 补齐 3604 条，不重不漏 |
| 版本 | `./bin/lignting --version` | `lignting 0.0.1` |

本轮修复/新增：

- **检索写入 O(N²) 修复（本轮最大项）**：`search_terms` 只有 `(term,doc_id)` 主键 + `term` 索引，
  写入路径按 `doc_id` 删除倒排词只能全表 `SCAN`，每写一条消息扫一遍全表。
  实测 1 万条 seed 50s、2 万条 186s（翻倍 3.7 倍），外推 10 万条约 80 分钟。
  修复：迁移 v3 加 `idx_search_terms_doc`；10 万条 seed **24s**、重建 **27s**。
- **schema v3**：`SchemaVersion=3`；备份/导出/AC-10 的 schema 断言改为跟随常量，不再硬编码。
- **基准自身假通过修复**：基准库未注入生产 indexer（「写入后立即可搜」没走生产路径）、
  「过滤会话」用例选中数学上不含目标词的会话（0 条也很快）；已修正并加「至少命中 1 条」断言。
- **断线演练自动化**：新增 `tests/verify/e2e/offline_drill_test.go`（`DRILL_EVENTS` 可调规模）。
- **v2→v3 升级用例**：`TestMigrateUpgradeAddsSearchTermsDocIndex` 断言升级后按 `doc_id` 删除命中索引。
- **Linux 发布冒烟脚本**：`scripts/smoke.sh`（真实二进制 + mock 模型，无需 API Key）。
- **文档**：search-benchmark 填实数与 O(N²) 复盘；ac-guide 补 R9 演练；release-notes 修正「审计链 HTTP 校验端点」
  的不实描述（实际由 `store.VerifyAuditChain` 在备份恢复与测试中校验）。

## 2. 未完成（下次继续的入口）

只剩两项，都依赖外部条件/人工，非代码缺陷：

1. **真实 Key 模式验收**：在有 `DEEPSEEK_API_KEY` 的环境按 `AC_MODEL_MODE=deepseek` 复跑 AC-01/03/07 并记录证据。
   - 本机 shell 无该环境变量；`~/.dsh/.credentials.yaml` 里有 Key，但真实模型跑 AC 会产生 API 费用，
     需 owner 同意后执行。
2. **真实浏览器断网 1 小时人工演练**：自动化等价演练已通过（见上表），人工项仍需发布前做一次，
   步骤见 [docs/testing/ac-guide.md](../../docs/testing/ac-guide.md) 第 7 节。

## 3. 如何继续

~~~bash
# 0) 工具链（本机没有 Go 时用 mise 装；dl.google.com 慢，走阿里云镜像 + 官方 sha256 校验）
mise use -g go@1.27.4
export GOPROXY=https://goproxy.cn,direct

# 1) 起一个干净的实例（mock 模型，无需 Key）
go run ./cmd/lignting --data-dir ./.lignting-data --addr 127.0.0.1:8787 --mock-model

# 2) 全量门禁
gofmt -l .; go vet ./...; go build ./...
go test ./...
go test -tags=acceptance ./tests/ac
go test ./tests/verify/e2e

# 3) 发布冒烟（真实二进制）
go build -o bin/lignting ./cmd/lignting && ./scripts/smoke.sh

# 4) 性能基准（分钟级）与断线演练
go test -tags=perf ./tests/bench -run TestSearchPerf100k -v
go test ./tests/verify/e2e -run TestR9OfflineCatchUpDrill -v
~~~

关键文件定位：

- 发布清单：[release-checklist.md](release-checklist.md)；P1：[p1-backlog.md](p1-backlog.md)
- 验收用例：`tests/ac`（AC-01..15）；E2E/演练：`tests/verify/e2e`
- 检索基准：`tests/bench` + [docs/ops/search-benchmark.md](../../docs/ops/search-benchmark.md)
- 使用文档：[docs/user/quickstart.md](../../docs/user/quickstart.md)；运维：[docs/ops/README.md](../../docs/ops/README.md)

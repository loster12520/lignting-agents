# Lignting Agents v0.0.1 发布说明

日期：2026-11-18 · 版本：`0.0.1` · 形态：单二进制（Windows/Linux/macOS，Go 1.27.4 构建）

## 1. 交付内容

- `lignting(.exe)`：Web 工作台 + HTTP API + Agent 运行时；数据全部落在 `--data-dir`（默认 `.lignting-data`）。
- 启动：`./lignting --data-dir <dir> --addr 127.0.0.1:8787`，浏览器打开 `http://127.0.0.1:8787`。
- 模型：DeepSeek 官方 API（`deepseek-flash` 默认）；无 Key 时平台可用，Agent turn 返回可读降级错误；测试可用 `--mock-model`。
- 工具面固定 5 个：`read_file` / `write_file` / `bash` / `web_search` / `view_change_history`。

## 2. 验收口径

- 自动化：`go build ./...`、`go vet ./...`、`go test ./...` 全绿；`go test -tags=acceptance ./tests/ac` 15 AC 全绿（mock 模式，无 SKIP）；`tests/verify/e2e` 真实二进制 E2E 全绿。
- 性能：10 万消息检索 p50/p95、写入可见延迟、Recall@10、索引重建耗时见 [docs/ops/search-benchmark.md](../docs/ops/search-benchmark.md)。
- 真实模型：无真实 `DEEPSEEK_API_KEY` 的机器上，AC 以 mock 模式执行；发布前需在有 Key 的环境按 `AC_MODEL_MODE=deepseek` 人工复跑 AC-01/03/07。
- 1 小时断线：以 SSE `since` 游标重放等价演练；发布前建议按 [docs/testing/ac-guide.md](../docs/testing/ac-guide.md) 做一次真实断网演练。

## 3. 升级与数据

- 首次启动自动建库/迁移；schema 版本见 `GET /api/health` 的 `schema_version`（当前 `3`）。
- 备份：`POST /api/backup` 生成包；恢复只允许空/新数据目录（`confirm=true`），不会就地覆盖运行中的库。
- 导出/导入：`/api/export`、`/api/import`（见 [docs/ops/export-import.md](../docs/ops/export-import.md)）。

## 4. 已知限制与 P1

- 见 [p1-backlog.md](p1-backlog.md)：schema v2 增补项（通知唯一性、调度补跑字段、usage 幂等键、search 作者列、独立已读表、Windows DPAPI）、若干 P1 FR、真实 Key 验收、1h 断线人工演练。
- 单用户 v0.0.1 不含 DSH/ACP/Node/MCP/多 Provider；不接非 DeepSeek 模型。

## 5. 安全提示

- 仅监听 loopback；不要把 `--addr` 暴露到公网。
- `secrets.json` 与 `--data-dir` 需由操作系统 ACL 保护（v0.0.1 未做 DPAPI/Keychain 封装）。
- 审计 hash chain 由存储层 `VerifyAuditChain` 校验（备份恢复流程与测试自动执行）；发现断裂按事故流程处理。HTTP 侧提供 `GET /api/audit` 查询与 `/api/audit/export` 导出（不含校验端点）。

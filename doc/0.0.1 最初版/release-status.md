# v0.0.1 发布收尾 — 工作状态快照

- 日期：2026-11-17（上一轮会话中断于发布冒烟验证）
- 仓库：`D:\project\github\lignting-agents`，分支 `main`，无 remote
- 当前形态：Go 单二进制 + 内嵌前端（零构建），只接 DeepSeek 官方 API（默认 `deepseek-flash`）
- 本次提交性质：**临时提交（WIP）**，用于保存中断现场；不是发布提交，未打 tag

---

## 1. 已完成并验证（2026-11-17 复跑证据）

R1–R8 按 [implementation-plan.md](implementation-plan.md) 的 R 表全部标记完成；R9 为发布收尾。

| 验证项 | 命令 | 结果 |
|---|---|---|
| 格式 | `gofmt -l .` | 无输出（exit 0） |
| 静态检查 | `go vet ./...` | exit 0 |
| 构建 | `go build ./...` | exit 0 |
| 单元/集成 | `go test ./...` | exit 0（internal/*、tests/verify/* 全 ok） |
| 验收 | `go test -tags=acceptance ./tests/ac` | exit 0，15.0s，15 个 `TestAC*` 全 PASS，0 Skip |
| 真实二进制 E2E | `go test ./tests/verify/e2e` | exit 0，1.9s |
| 版本 | `./bin/lignting.exe --version` | `lignting 0.0.1` |

上一轮（中断前）完成的改动：

- **AC-07 补全**：真实 OS 进程 kill + 重启、真实模型失败（无 API Key）降级为可读错误且 runtime 仍可用、SSE `since` 重放；移除 SkipSkeleton。
- **AC-13 补全**：批量审批（低风险）、snooze、授权代审、双钥匙二次确认；调整用例顺序避免密码/限流段提前登出影响后续断言。
- **AC harness**：新增 `WithNoKeyProvider()`，可注入空 Key 的真实 DeepSeek 客户端。
- **检索基准脚本**：种子批量化（500→1000）、预热/计时收敛（5→3、60→20）、单条内容瘦身，目标是把 10 万条基准压到分钟级；移除 offset 分页场景。（实测仍未达预期，见 P0-4）
- **web/dev/serve.mjs**：修复 `web/web` 路径拼接导致 `/assets/*` 404。
- **发布文档**：新增 [release-notes.md](release-notes.md)、[release-checklist.md](release-checklist.md)、[p1-backlog.md](p1-backlog.md)、[CHANGELOG.md](../../CHANGELOG.md)；README 补快速开始与验收命令；implementation-plan 的 R4–R9 状态更新。

## 2. 未完成（按优先级，**这些是下次继续的入口**）

### P0 — 发布前必须做完

1. ~~**发布二进制手工冒烟**~~ **已完成（2026-11-17）**：真实二进制 + mock 模式跑通 `health → setup → bootstrap → 建项目会话 → 发消息 → 列表`；上一轮 `POST /api/setup` 400 的原因是 PowerShell 传 JSON 时引号被剥离，非产品缺陷（详见 [release-checklist.md](release-checklist.md)）。
2. **真实 Key 模式验收**：在有 `DEEPSEEK_API_KEY` 的环境按 `AC_MODEL_MODE=deepseek` 复跑至少 AC-01 / AC-03 / AC-07，并记录证据。
3. **1 小时断线演练**：SSE `since` 重放已自动化验证，真实断网 1h 的人工演练未做（步骤见 [docs/testing/ac-guide.md](../../docs/testing/ac-guide.md)）。
4. **100k 检索基准结果落盘**：`go test -tags=perf ./tests/bench -run TestSearchPerf100k -v` 的结果需填进 [docs/ops/search-benchmark.md](../../docs/ops/search-benchmark.md)（当前该文档只有方法、没有数字）。
   - 本次尝试：在本机跑了 **>20 分钟未完成**（CPU 满载；临时库已增长到 ~67MB，说明 seed 基本完成，疑似慢在后续查询/Recall/Rebuild 阶段），已手动终止。下次先分段计时定位热点，再决定是否放宽阈值或优化 `search.Rebuild`。
5. **发布收口**：打 tag `v0.0.1`；确认 `internal/config.Version` 仍为 `0.0.1`；按 [release-checklist.md](release-checklist.md) 把剩余 `[ ]` 全部勾掉。

### P1 — 不阻塞发布，见 [p1-backlog.md](p1-backlog.md)

- schema v2 增补（通知唯一约束、schedules 补跑字段、usage 幂等键、search 作者列、独立已读表、Windows DPAPI）；
- P1 功能项（引导增强、会话归档、模板市场、产出物预览、评审分配、回放/成本预测、定时策略/配额）；
- 前端为原生 JS 单页，大列表性能与组件复用有限；
- `tests/verify/bin/*.exe` 曾是构建产物入库（60MB），本次已从 git 跟踪移除并加入 `.gitignore`。

## 3. 如何继续

~~~powershell
# 1) 起一个干净的实例（mock 模型，无需 Key）
go run ./cmd/lignting --data-dir ./.lignting-data --addr 127.0.0.1:8787 --mock-model

# 2) 全量门禁
go vet ./...; go build ./...; go test ./...
go test -tags=acceptance ./tests/ac
go test ./tests/verify/e2e

# 3) 性能基准（慢，分钟级）
go test -tags=perf ./tests/bench -run TestSearchPerf100k -v
~~~

关键文件定位：

- 发布清单：[release-checklist.md](release-checklist.md)；P1：[p1-backlog.md](p1-backlog.md)
- 验收用例：`tests/ac`（AC-01..15）；E2E：`tests/verify/e2e`
- 使用文档：[docs/user/quickstart.md](../../docs/user/quickstart.md)；运维：[docs/ops/README.md](../../docs/ops/README.md)

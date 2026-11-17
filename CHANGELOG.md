# Changelog

## v0.0.1 — 2026-11-17（初版）

本地优先、单用户、单二进制的 AI 组织工作台；只接 DeepSeek 官方 API（默认 `deepseek-flash`）。

### 新增

- **启动与工作台**：首次启动向导（bootstrap/setup/密码）、助理私聊、项目/部门/跨部门群聊、收件箱、Agent 管理、设置页；前端零构建、内嵌进二进制。
- **组织**：意图澄清（单次一个关键问题）→ 蓝图 → G1 人类闸门 → Provisioning → 部门/主管/员工；Agent 生命周期（暂停/恢复/停止）、角色卡、部门内讨论、跨部门辩论（5/15 限）、决策与升级。
- **任务闭环**：任务状态机、DoR/DoD、依赖、WIP、时间线、核对表；产出物注册/版本；评审/挑战/裁决；**G2**（高风险进入 `in_progress` 前）与 **G3**（所有任务 `done` 前）人类闸门；主管不能自审。
- **共享写工具**：5 个工具（read_file / write_file / bash / web_search / view_change_history）；全局串行、按文件时间线（默认 30，裁剪保留）、冲突检测、Git 分支/提交监听、提交组。
- **检索**：中文 2 字、全文、类型/部门/作者过滤、权限 ACL、软删除不可搜、索引重建、10 万条性能基准（`tests/bench`）。
- **记忆**：四层记忆、人工确认、注入快照（来源/token/降级）、检查器、上下文查询、导出。
- **注意力与审批**：四类通知（FYI/需要知道/需要决定/阻塞）、收件箱持久对象、审批 TTL、批量（仅低风险）、稍后（snooze）、授权代审（范围+到期自动回收）、双钥匙（不可逆动作二次确认）、人类沉默策略、抽检与异常通过率（FR-REV-05）。
- **安全**：本地单用户会话、CSRF/Origin 校验、Agent Bearer Token（仅存 sha256，可签发/轮换/撤销）、临时桥（scope+到期+审计+自动关闭）、审计 hash chain、紧急停止。
- **成本**：usage 账本、日/月预算、80%/100% 告警与暂停、按 Agent/任务聚合。
- **运维**：SQLite WAL + schema 迁移（v1→v2）；备份（`VACUUM INTO`）/导出/导入/恢复到新数据目录；SSE `since`/Last-Event-ID 断线补齐；崩溃恢复（running turn 标记中断、写事件 applied/rejected/unknown）；定时任务与补跑；故障注入契约（`faults.json` + `LIGNT_FAULT_*`，仅测试构建读取）。

### 测试与验收

- 单元/集成：`go test ./...`（含 store/platform/core/memory/search/write/gitwatch/backup/verify）。
- 端到端：`tests/verify/e2e` 真实二进制 + 临时数据目录。
- 验收：`tests/ac` 15 个 AC（`-tags=acceptance`，mock 模式；真实 Key 模式为发布前人工项）。
- 性能：`go test -tags=perf ./tests/bench -run TestSearchPerf100k`。

### 已知限制

见 [doc/0.0.1 最初版/release-notes.md](doc/0.0.1 最初版/release-notes.md) 与 [doc/0.0.1 最初版/p1-backlog.md](doc/0.0.1 最初版/p1-backlog.md)。

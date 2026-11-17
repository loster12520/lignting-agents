# v0.0.1 P1 清单与规避方案

> 来源：task-14 独立验证、task-22 schema 增补、R9 收尾自查。
> 原则：P1 不阻塞 v0.0.1 发布，但必须有替代方案/规避说明，并在 v0.0.2 补齐。

## 1. Schema v2 增补（task-22）

| # | 项 | 现状/规避 | v0.0.2 计划 |
|---|---|---|---|
| 1 | `notifications` 唯一约束 | 应用层按 `(level, object_type, object_id)` 去重（store.CreateNotification 事务内查询），DB 无 UNIQUE 约束 | 迁移加唯一索引；并发写冲突改为 insert-or-ignore |
| 2 | `schedules` 补 `attempts/fired_at/last_error/idempotency_key` | 现有 `status+due_at` 与审计可追溯；启动补跑按到期扫描 | 迁移加列 + 补跑幂等键 |
| 3 | 双钥匙落点 `required_keys/keys_approved` | 单用户 v0.0.1 用「人类二次确认 `second_confirm` + 审计 `dual_key_*`」代替第二人类；批量审批对不可逆整批 403 | 多用户/多钥匙时落表 |
| 4 | `usage_ledger` 幂等键（call_id/request_id/retry_of） | 一次 turn 只记一次 usage；客户端内部重试不重复记账；无请求级幂等键 | 加 call_id + retry_of，重试不重复计费 |
| 5 | `search_docs` 作者列 | 消息搜索可回表 `messages` 过滤；跨对象作者过滤暂不支持 | 加 `sender_id/author_id` 列 |
| 6 | `conversation_reads` 独立表 | 已读用 `settings` key `read:<conv>`（R1 临时方案，已获批） | 独立表 + 迁移 |
| 7 | Windows 密钥/数据目录 ACL | 依赖用户目录 ACL；`secrets.json` 0600 语义 | DPAPI/Keychain 封装 |

## 2. 验收与运维人工项

- **真实 DeepSeek Key 验收**：开发环境无 Key，AC 以 `--mock-model` 执行；发布前需在有 Key 环境跑 `AC_MODEL_MODE=deepseek ./scripts/ac.ps1 -Strict`（至少 AC-01/03/07）。
- **1 小时浏览器断线演练**：已用 SSE `since` 游标重放等价验证（不重不漏）；发布前按 `docs/testing/ac-guide.md` 做一次真实断网 1h 演练并记录。
- **Windows 沙箱/杀软**：`bash` 工具在受限环境可能被拦截，属环境限制，不是平台缺陷。

## 3. P1 功能项（requirements 已标 P1）

- FR-ONB-05 引导增强、FR-CHAT-10 会话归档/置顶、FR-TEAM-07 组织模板市场、FR-ART-04 产出物预览、FR-REV-04 评审分配算法、FR-OBS-02/05 回放与成本预测、FR-OPS-03/04 定时策略与配额。
- 工作台视觉/交互打磨（当前为可用优先的零构建单页）。

## 4. 已知技术债

- 前端为原生 JS 单页（无构建链），大列表性能与组件复用有限；v0.0.2 视需要引入轻量构建。
- 辩论/抽检的统计为应用层聚合（reviewer×author），非物化视图；数据量大时需预聚合。
- `tests/verify/bin/*.exe`（e2e 现场构建产物，约 60MB）已于 2026-11-17 从 git 跟踪移除并加入 `.gitignore`；`tests/verify/e2e` 的 `TestMain` 仍会按需现场构建。

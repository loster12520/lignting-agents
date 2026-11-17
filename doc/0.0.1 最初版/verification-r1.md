# v0.0.1 独立验证报告 R1（HTTP 平台黑盒）

> **验证者**：verifier（独立验证/红队，不写生产代码）
> **任务**：task-14《独立验证：R0/R1 基线、契约一致性与缺陷跟踪》
> **基线**：R0 = git `f7a2566`；R1 = 工作树（尚未提交，platform/store/search 已落地）。契约基线 = `api-contract.md` v1.1（§12 裁决后）。
> **验证时间**：2026-11-17 15:29–16:05（Asia/Shanghai）
> **方法**：`tests/verify/http` 独立夹具（httptest + 临时数据目录 + cookiejar），HTTP 黑盒驱动；只写测试与报告，未改生产代码/schema/契约。

## 0. 结论

| 范围 | 结论 |
|---|---|
| **R1 HTTP 平台** | **通过（无已知 P0）**：AC-01 启动/setup 主路径、AC-06 中文 2 字检索+删除、AC-07 重启恢复、AC-08 的会话/CSRF/Origin/无 CORS 基线全部有独立 PASS 证据；契约 v1.1 的响应包例外、JSON 404/405、settings allowlist 通过 |
| **D-R1-2（已关闭）** | 消息分页 next 方向错误（before 模式翻页重复上一页）→ task-24 已由 platform-dev 修复（handlers_chat.go 按 after/before 分支取页尾/页首 seq），verifier 复跑 `TestR1MessagePaginationNoOverlap` **PASS** |
| **P0 语义缺口** | 检索权限过滤未接线（`Filter.AllowedVisibilities` 从未赋值）、本地密码无设置入口（远程访问不可用）→ **task-25**（R2/R8，不阻塞单用户 R1 演示，但阻塞 AC-08） |
| **真实二进制 E2E** | **20/20 通过**（`tests/verify/bin/lignting-r1.exe` + 临时数据目录 + curl）：setup→中文消息→同 client_id 幂等→中文 2 字检索→删除→检索为空→重启→再查；Cookie HttpOnly/SameSite=Strict、CSRF 403、Origin 403、JSON 404/405、SSE since 补齐逐项有证据（详见 §1.1） |
| **15 AC** | 仍无 AC 可整体关闭：AC-01/06/07 仅"主路径证据"，AC-08 缺 agent 身份；AC-13 等未开始（详见 §4） |

## 1. 通过项（`go test ./tests/verify/http -v`，15/15 PASS，1 项 Skip=缺陷）

| 用例 | 断言 | 结果 |
|---|---|---|
| TestR1SetupBootstrapAndDuplicates | bootstrap `setup_required=true`；setup 创建 1 助理 + 1 DM；重复 setup=409；认证后下发 csrf/settings；settings 不含 `session.secret`/`read:`/`sk-` | ✅ |
| TestR1AuthCSRFOrigin | 未登录 401 JSON(`code=unauthorized`)；缺 CSRF 403；带 CSRF 200；恶意 Origin 的 GET/POST 均 403；无 `Access-Control-Allow-Origin` | ✅ |
| TestR1MessageIdempotencyHTTP | 同 `client_id` 两次 POST → 第二次 `created=false`、同一 message.id、列表只 1 条（Lead 分诊 #1 行为口径） | ✅ |
| TestR1MessagePaginationNoOverlap | 5 条消息 limit=2 连续翻页无重复无遗漏 | ✅（D-R1-2 修复后复验） |
| TestR1SearchChineseAndDelete | 中文 2 字「限流」命中且片段含 `<em>`；`graphql` 大小写不敏感命中；删除后同词 0 命中（AC-06） | ✅ |
| TestR1SSEReplay | `GET /api/events?since=0&conversation_id=` 返回 `text/event-stream`，补发 `event: message.created` 且带数字 `id:`（AC-07 断线补齐基础） | ✅ |
| TestR1RestartPersistence | 重启进程+DB 后 loopback 重新登录：会话/消息完整、中文检索仍命中、审计链 `VerifyAuditChain` 通过（AC-07） | ✅ |
| TestR1JSONErrors | 未知 `/api/*` → JSON 404；方法不允许 → JSON 405；非法 JSON → 400（契约 §12.1） | ✅ |
| TestContractSuccessEnvelope | health 扁平（§12.1 唯一例外）；bootstrap 包 `data` | ✅ |
| TestContractJSONErrors | 同上（契约探针口径） | ✅ |
| TestContractOriginHostCSRF | 恶意 Origin+Host 被拒 | ✅ |
| TestContractBootstrap / AuthRequired / SSE | bootstrap 可用；受保护端点 401；SSE 存在 | ✅ |
| TestR0Health / Static / SecurityHeaders / AssetTraversalBlocked | R0 退出标准回归全绿（traversal 已按正确 Host 断言 404） | ✅ |


## 1.1 真实二进制端到端（Lead 要求；证据命令见 §5）

**环境**：`go build -o tests/verify/bin/lignting-r1.exe ./cmd/lignting`（15:57，18.7MB）；临时数据目录 `tests/verify/.tmp-r1`；`--addr 127.0.0.1:18807`；curl 驱动（cookie jar + 文件体 UTF-8，避免 PowerShell 引号陷阱）。**不依赖 httptest，未修改生产代码。**

| # | 步骤 | 通过标准 | 实测 |
|---|---|---|---|
| 1 | GET /api/bootstrap（未初始化） | 200，`setup_required=true` | ✅ |
| 2 | POST /api/setup（阿哲 + 预算） | 200，authenticated，csrf_len=43，agents=1，convs=1 | ✅ |
| 3 | 重复 POST /api/setup | 409 `conflict` | ✅ |
| 4 | POST /api/conversations（带 CSRF） | 200，返回 conv_id | ✅ |
| 5 | 同 POST 不带 CSRF | 403 `forbidden` | ✅ |
| 6 | POST 中文消息「登录限流策略需要调整」 | 200，created=true，返回 id/seq | ✅ |
| 7 | 同 client_id 重发（内容不同） | 200，created=false，**同 message.id** | ✅ |
| 8 | GET /api/search?q=限流 | total=1，片段含 `<em>` 高亮 | ✅ |
| 9 | 发第二条「重启后仍在的消息」 | 200 | ✅ |
| 10 | DELETE /api/messages/{id} | 200，deleted=true | ✅ |
| 11 | 删除后再搜「限流」 | 200，total=0（AC-06 删除不可搜） | ✅ |
| 12 | GET /api/events?since=0（SSE，--max-time 3） | `event: message.created` + 数字 `id:`（2.7KB） | ✅ |
| 13 | GET /api/health 带 `Origin: evil` | 403 | ✅ |
| 14 | GET /api/does-not-exist | 404 JSON `not_found`（契约 §12.1） | ✅ |
| 15 | POST /api/health | 405 JSON | ✅ |
| 16 | 强杀进程 → 同数据目录重启 → 复用同一 cookie 与 CSRF | 200；消息 1 条、检索「重启」total=1 | ✅ |
| 17 | 重启后用旧 CSRF 再发消息 | 200（session.secret 持久化，R1 重启语义） | ✅ |
| 18 | /api/setup 响应头 Set-Cookie | `HttpOnly; SameSite=Strict; Path=/; Max-Age=2592000` | ✅（首次脚本从写请求抓头导致误报，复核 setup 响应头通过） |
| 19 | setup 后 DM 首条消息 | 助理欢迎消息（能做/不做/暂停/查看行为四段，`meta.kind=greeting`）→ AC-01 助理主动介绍证据 | ✅ |
| 20 | GET /api/org | 200，assistant + owner，agents=[]（无部门时） | ✅ |

**结论**：真实二进制的 setup → 中文消息 → 幂等重发 → 中文 2 字检索 → 删除 → 检索为空 → 重启 → 再查，**全链路通过**；Cookie/CSRF/Origin/404/405/SSE since 补齐逐项有证据。唯一失败是 Lead 已分诊的 **D-R1-2 分页 next**（由 httptest 用例稳定复现，task-24）。

## 2. 缺陷 D-R1-2（已关闭）：消息分页 next 方向错误

> **状态**：platform-dev 已修（task-24 completed）。修复实现：`handlers_chat.go:310-320` 按 `after>0` 分别取 `msgs[len-1].Seq`（向前同步）与 `msgs[0].Seq`（默认/before 往前翻页）。verifier 复跑 `go test ./tests/verify/http -run TestR1MessagePaginationNoOverlap -v` → **PASS**；`tests/verify/...` 三个套件全绿。以下保留缺陷记录作为回归依据。

**复现**（独立 HTTP 用例，非推测）：
1. 创建会话，连发 5 条消息（seq 1..5 递增）；
2. `GET /api/conversations/{id}/messages?limit=2` → `items=[4,5]`，`next=5`；
3. `GET ...&before=5&limit=2` → `items=[2,3]`，`next=3`（4 已看过，被跳过没关系）；
4. `GET ...&before=3&limit=2` → `items=[1,2]` → **2 重复显示**。

**根因**：`internal/platform/handlers_chat.go:310-313` 无条件使用 `msgs[len(msgs)-1].Seq` 作为 `next`。`ListMessages` 在 before/default 模式返回升序页，语义是"取 seq < before"；因此 `next` 必须取**本页首页序**（`msgs[0].Seq`）作为下一轮 before；只有 `after` 模式（向前补齐）才应取末页序。

**影响**：桌面端"加载更早消息"会在第 2–3 页出现重复消息；用户感知为"聊天记录错乱/重复"，属 AC-01/AC-07 日常可用性问题。

**修复建议**：
```go
if len(msgs) == int(limit) {
    switch {
    case after > 0: next = strconv.FormatInt(msgs[len(msgs)-1].Seq, 10)
    default:        next = strconv.FormatInt(msgs[0].Seq, 10)
    }
}
```
并补测试：5 条消息 limit=2 连续翻页，集合恰好 5 条、无重复；`next==""` 表示到底。

**验收**：`tests/verify/http::TestR1MessagePaginationNoOverlap` 由 Skip 转 PASS。

## 3. 其他 R1 观察（不阻塞本轮，已记录）

| # | 位置 | 观察 | 处置 |
|---|---|---|---|
| o1 | handlers_search.go（Filter 构造） | `search.Filter.AllowedVisibilities` **从未赋值**（grep=0）；单 owner 现状可接受，R2 接入 Agent 身份后即跨部门泄漏 | task-25（R2） |
| o2 | platform 路由 | 无设置/修改本地密码端点，但 `store.SetOwnerPassword` 已存在；非 loopback 且无密码时登录一律 403 → 远程访问（Tailscale/反代）无可用路径 | task-25（R2/R8） |
| o3 | handlers_search.go / search.Rebuild | 重建索引先清空 docs，期间搜索返回空且响应无"索引更新中"标记（FR-SRCH-05 要求可降级/明示） | R8 验收点 |
| o4 | handlers_chat.go:316 | `total` 是会话可见消息总数，不是本次结果数；前端需按 `items.length`/next 判断（建议契约写清） | 文档 |
| o5 | auth.go:203 | `handleLogout` 直接注册、未走 CSRF（契约 §1.2 要求写操作带 CSRF；强制登出属低危） | 契约注记 |
| o6 | handlers_bootstrap.go:279 | 登录无失败限流（FR-SEC-01「失败登录有记录与限流」只做了审计记录） | R8 |
| o7 | handlers_bootstrap.go:182-192 | setup 先写 secrets.json 再事务建 owner；事务失败会留 API Key（无 owner 的半初始化，重试可恢复） | 记录 |
| o8 | handlers_search.go:15 | 时间过滤把入参规范成固定 9 位小数；store.Now 为 RFC3339Nano（同秒可能无小数）→ 同秒边界可能漏/多一条 | R8 测试覆盖 |
| o9 | handlers_events.go:63 | SSE 支持 conversation_id 过滤但无会话 ACL（owner 视角 OK；R2 加） | R2 |
| o10 | handlers_bootstrap.go:48 | health 在 §12.1 字段清单外多返回 `model`/`search_index_state`（无害；契约建议标注"附加字段允许"） | 契约 |

## 4. 15 AC 覆盖状态（R1 视角）

| AC | R1 可给证据 | 缺口 / 后续轮次 |
|---|---|---|
| AC-01 | setup/bootstrap/助理创建/DM/会话列表/单次问答所需数据面 | 助理问候/意图卡/引导（R2）；30 分钟口径未测 |
| AC-06 | 消息中文 2 字、大小写、删除不可搜、过滤 conversation/kind | 任务/决策/产出物跨对象检索（R4/R8）；Recall@10/10 万条基准（R9） |
| AC-07 | 进程内重启后消息/索引/审计链一致 | kill -9 强制终止、1 小时断线、SSE 全量补齐（R8） |
| AC-08 | 人类会话：401/CSRF/Origin/无 CORS/JSON 错误 | Agent Token/跨部门越权/注入（R2）；检索 ACL（task-25） |
| AC-02/03/04/05/09/10/11/12/13/14/15 | 未开始或仅数据面 | R3–R9 |

## 5. 测试资产与复现

~~~powershell
# R1 HTTP 黑盒（本次 15 用例，1 项 D-R1-2 Skip）
go test ./tests/verify/http -v -count=1

# R1 存储层行为（task-20 口径；5/5）
go test ./tests/verify/store -v

# 契约/数据探针（已分诊缺口 Skip）
go test ./tests/verify/schema -v

# 全量（当前工作树 core/tools 正在写：build red；其余平台包绿）
go build ./... ; go test ./internal/platform ./internal/store ./internal/search -count=1
~~~

本次全量快照：`internal/platform`、`store`、`search`、`memory`、`gitwatch`、`deepseek` 绿；`internal/core`、`internal/tools` 处于并行写入（编译中），不计入 R1 结论。verifier 三个套件全绿（http/store/schema）。

## 6. 下一步

1. 等 platform-dev 修 task-24 → 我把该用例转硬断言并复跑，确认聊天分页无重复。
2. R2 接入 Agent 身份后，先补 AC-08 的"越权搜索/跨部门读取"用例（task-25）。
3. R8/R9 前补：kill -9 断电类恢复、1 小时断线补齐、10 万条中文检索基准与 Recall@10 qrels（AC-06/07 正式关闭）。

# v0.0.1 独立验证报告（R0 基线 + R1 存储层）

> **验证者**：verifier（独立验证/红队，不写生产代码）
> **后续**：R1 HTTP 黑盒结果见 [verification-r1.md](verification-r1.md)（含 D-R1-2 分页缺陷）。
> **任务**：task-14《独立验证：R0/R1 基线、契约一致性与缺陷跟踪》
> **基线**：git `f7a2566`（R0 scaffold 已提交）；工作树另有 37 个未提交文件，属 R1 并行开发中。
> **验证时间**：2026-11-17 15:29–15:52（Asia/Shanghai）
> **方法**：只读源码/文档 + 独立写 `tests/verify/**` 探针 + 预编译二进制黑盒 HTTP + 临时数据目录；**未修改任何生产代码、schema 或契约文档**。
> **参考**：`doc/0.0.1 最初版/requirements.md` 第 14 章 15 AC 与 14.2 FR→AC 矩阵、`doc/0.0.1 最初版/api-contract.md`（v1 冻结草案）、`doc/0.0.1 最初版/implementation-plan.md` §0.1/§4/§6（测试策略、R9）。

## 0. 结论

| 范围 | 结论 | 依据 |
|---|---|---|
| **R0 交付（commit f7a2566）** | **通过** | `go build ./...`、`go vet ./...`、`go test ./...` 全绿（15:33 快照）；预编译二进制黑盒：`/api/health` 200、静态页/资源 200、安全头齐全、默认无 CORS、路径穿越 404；运行时 DB 40 表 + WAL + schema_version=1 |
| **R1 存储层** | **通过（独立抽检）** | 5/5 行为用例通过：client_id 幂等（同 ID 两次 = 1 消息 + 1 事件）、已读游标单调、编辑/删除墓碑+索引移除、seq 全局递增+分页、审计链篡改可检出（详见 §3） |
| **R1 HTTP 层** | **阻塞（进行中，非缺陷）** | platform 包正在落地 R1 路由/鉴权：当前工作树 `internal/platform/auth.go`/`respond.go` 与 `server.go` 尚未合并完成（`writeJSON` redeclared、`Server` 缺 secret/opCtx 字段），`go build ./...` 红。待 platform-dev 合入后立即跑 §6 的 HTTP 清单 |
| **15 AC 发布门** | **尚无 AC 可关闭** | R0/R1 只覆盖 AC-01/06/07 的基础设施；AC-01 依赖 bootstrap/setup（R1）、AC-06 依赖跨对象搜索与审计（R1/R8）、AC-07 依赖重启恢复（存储层已具备，HTTP 未验证） |

**一句话**：R0 骨架与 R1 持久化底座质量达到预期（幂等/墓碑/审计链都有真实测试且通过）；当前唯一的红色是并行开发中的编译瞬时态，不是产品缺陷。契约侧有 1 个 P0 缺口（附件）和一批需要 Lead 决策的端点/枚举问题，已按分诊开任务（task-20..23）。

---

## 1. R0 退出标准核对

R0 退出标准（implementation-plan §4 R0）：`go build ./...`、`go test ./...` 通过；`lignting --help` 与启动后 `/api/health` 返回 200；所有 owner 明确写作用域；任务板可领取。

| 项 | 结果 | 证据 |
|---|---|---|
| go build ./... | ✅ | 15:33 快照 exit=0 |
| go vet ./... | ✅ | 15:33 快照 exit=0 |
| go test ./... | ✅（覆盖薄） | domain 4 个用例 pass；store/platform/config/search 当时无测试文件 |
| lignting --help | ✅ | 打印 `-addr/-data-dir/-mock-model/-version` 用法 |
| /api/health 200 | ✅ | HTTP/1.1 200 + JSON（见 §2） |
| 静态页 | ✅ | `/` 200 text/html（含 "Lignting Agents"）；`/assets/app.js`、`/assets/style.css` 200 且 MIME 正确 |
| 工作区/任务板 | ✅ | implementation-plan §5.1 写作用域；task-8..19 已建 |

**覆盖薄的风险**：R0 当时只有 `internal/domain/state_test.go`（4 用例）。R1 已补 `internal/store/store_test.go`（11 用例，全绿）。建议 R1 退出沿用 implementation-plan §4 R1 的 `go test ./internal/store ./internal/platform ./internal/search` 门槛，且 platform/search 必须有实体用例，不接受 `[no test files]`。

## 2. 黑盒抽检（预编译二进制，R0 状态）

命令：`bin/lignting.exe --data-dir <tmp> --addr 127.0.0.1:18791`（二进制构建于 15:33:05，对应 R0 代码）；curl 逐项探测。

| # | 请求 | 结果 | 判定 |
|---|---|---|---|
| 1 | GET /api/health | 200，`{"model":"deepseek-flash","model_mode":"deepseek","now":...,"ok":true,"schema_version":1,"uptime_seconds":0,"version":"0.0.1"}` | ✅ 但**扁平包**，与 api-contract §1.1 `{"ok":true,"data":{...}}` 不一致（task-23 A1） |
| 2 | 安全响应头 | CSP、`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy: no-referrer` 均在 | ✅ |
| 3 | 默认 CORS | 无 `Access-Control-Allow-Origin` | ✅（AC-08 要求） |
| 4 | GET / | 200 text/html 656B | ✅ |
| 5 | GET /assets/app.js、style.css | 200，`text/javascript` / `text/css` | ✅ |
| 6 | GET /assets/..%2Fgo.mod（--path-as-is） | 404 | ✅ 穿越被拒 |
| 7 | GET /api/nope | 404 **text/plain** | ❌ 契约 §1.1 要求 JSON 错误包（task-23 A2） |
| 8 | POST /api/health | 405 **text/plain** | ❌ 同上（task-23 A2） |
| 9 | GET /api/health + `Origin: https://evil.example`、`Host: evil.example` | **200** | ❌ Origin/Host 校验未实现（R1 已知范围；AC-08 发布门） |
| 10 | GET /api/bootstrap | 404（R0 预期） | ⏳ R1 |
| 11 | 运行时数据目录 | `config.json`（473B）+ `lignting.db` + WAL/SHM + artifacts/backups/logs/workspace | ✅ 结构符合 implementation-plan §1.1 |
| 12 | 运行时 DB（独立 dbcheck 工具） | 40 张表（38 业务表 + schema_migrations + sqlite_sequence）、schema_version=1、journal_mode=wal | ✅ |
| 13 | 强制 kill 后 DB | 主文件 4096B，schema 在 WAL 中；重开后 40 表完整 | ✅ 但也说明：**备份不能只拷 lignting.db**（FR-OPS-01 必须在线备份/导出，R8 验收点） |

## 3. R1 存储层独立验证（已通过）

测试资产：`tests/verify/store/r1_store_test.go`（`go test ./tests/verify/store -v`，5/5 PASS）。只断言**行为**，不断言内部列名（按 Lead 分诊 #1/#2）。

| 用例 | 断言 | 结果 |
|---|---|---|
| TestR1ClientIDIdempotency | 同 `client_id` 连续两次 InsertMessage：第 2 次 `inserted=false`、同一 `msg.ID`、消息数=1、`message.created` 事件数=1、审计链可校验 | ✅ |
| TestR1UnreadAndReadCursor | 未读=非本人消息数；`SetReadSeq` 后未读=0；已读位置单调（回退不生效） | ✅ |
| TestR1EditAndTombstone | 编辑写 `edited_at`；删除写 `deleted_at`、列表不可见、`search_docs` 移除、审计链可校验 | ✅ |
| TestR1SeqAndPagination | 跨会话 seq 全局递增；`ListMessages` before/after 均返回升序 | ✅ |
| TestR1AuditChainDetectsTamper | 审计链可校验；直接 UPDATE 篡改 hash 后 `VerifyAuditChain` 必失败 | ✅ |

对照 Lead 分诊结论：
1. **client_id**：R1 用 `meta.client_id` + `json_extract` 应用层幂等，行为已满足「同 client_id 两次 → 1 消息 + 1 事件」，**不需要 schema 列**。当前 `internal/store/messages.go` 已实现，未发现「client_id 未写入 meta」的残留（`canonicalMeta` 会合并；`InsertMessage` 走它）。若 platform-dev 修的是 HTTP 层传参路径，合入后由 `tests/verify/http` 用例复验。
2. **last_read_seq**：`settings` key=`read:<conv>` 已生效（`internal/store/settings.go`），语义正确（只算非本人、单调推进）。R8 迁移独立表（task-22）。
3. 其余 schema 缺口按分诊归 R4/R8（task-21/22），本轮不判 R0/R1 失败。

## 4. 契约/数据一致性发现（按 Blocker/Major/Minor）

### Blocker（流程，非产品缺陷）

**B1 工作树在并行开发中多次不可整体构建，R0/R1 退出标准依赖"某一瞬间的绿"。**
证据（同一工作树，20 分钟内三次快照）：
- 15:33：`go build/vet/test` 全绿（R0 交付）；
- 15:40：`web/embed.go:7:44: pattern views: no matching files found`（web-dev 加 `views` 引用时目录未建）；
- 15:47：`internal/memory/compress.go:166:29: newline in string`（memory-dev 写入中）；
- 15:50：`internal/platform/auth.go` `writeJSON redeclared` / `Server` 缺字段（platform-dev 合入 R1 中）；
- 15:55：`internal/platform/server.go` 引用尚未提交的 `eventBroker`/`handleSearch`/`handleEvents` 等（platform 路由合入中）；`internal/memory/archive_test.go:182` 字符串未闭合（memory-dev 写入中）。
- 同一时刻 **`go test ./...` 有一个真实失败（非编译态）**：`internal/gitwatch` `TestCommitGroup` —— `watch_test.go:169 status={Branch:main Commit:23eb9d5...}`、`groups=[]`、`poll error: git log --reverse ... exit status 1`。属 R5（task-11）在研功能，本报告只登记，不作为 R0/R1 结论。
**影响**：任何"每轮绿"的验收结论都可能被下一分钟推翻；verifier 无法稳定跑 HTTP/集成验证。
**建议**：Lead 在每轮合并点执行 `go build ./... && go vet ./... && go test ./...` 并把结果记入 implementation-plan 轮次状态表；各 owner 提交前自跑同一命令；在 CI/脚本落地前，验收只认"提交后的 commit"，不认"工作树"（本次 R0 即按 `f7a2566` 判定）。

### Major

**M1（P0 缺口，R4）附件没有表/没有端点，与 requirements FR-CHAT-02（P0）和 implementation-plan R1「附件元数据 API」冲突。**
证据：`schema.sql` 无 `attachments`；`api-contract.md` grep `attachment` = 0；但 FR-CHAT-02 要求大小/解压上限、类型限制、`attachment`+`nosniff`、SVG/HTML 不同源渲染。→ task-21（R4）。

**M2（R1）契约响应包与错误模型不统一。**
`/api/health` 扁平包 vs §1.1 `data` 包裹；`/api/*` 404/405 返回 text/plain vs §1.1 JSON 错误包。→ task-23 A1/A2。建议 Lead 现在就定：health 是否例外（若例外，写进契约）。

**M3（R1/R8）契约缺 5 类端点，P0 行为无落点。**
- notifications/收件箱：只有事件类型 `notification.created`，无 GET/POST（FR-CHAT-06/FR-ASST-06/AC-13 的持久分组、稍后、批量、授权）；
- schedules：无端点（FR-OPS-07 要求 UI 显示等待中/已到期/已处理/已跳过）；
- restore/import：只有 backup/export（FR-OPS-01/02、AC-10）；
- `api_key`：「单独端点」被提及但无端点行（§2）；
- FR-ART-03「评论转挑战单」、FR-MEM-07 手册回滚无端点（可注明用 POST 新版本表达）。
→ task-23 B。

**M4（R8）schema v2 增补清单**：notifications 唯一约束、schedules attempts/fired_at/idempotency、approvals 双钥匙、usage_ledger call_id、search_docs sender_id、conversation_reads 独立表、Windows 密钥 DPAPI/ACL。→ task-22（按 Lead 分诊 #4/#5/#6）。

### Major（R5 观察，非本轮阻塞）

**M5 `internal/gitwatch` 的 `TestCommitGroup` 当前失败**（`go test ./internal/gitwatch -run TestCommitGroup -v`）：`watch_test.go:169` 打印 `Branch:main Commit:23eb9d5...` 但 `groups=[]`，`poll error: git log --reverse --format=... <range>: exit status 1`。说明提交轮询的区间/父提交处理在测试仓库形态下不成立（或测试仓库初始化未产生可比较区间）。归 R5/task-11，本轮不阻塞；要求 R5 退出前 `go test ./internal/gitwatch` 必须绿。

### Minor

| # | 位置 | 问题 | 处置 |
|---|---|---|---|
| m1 | config.json vs settings 表 | `config.Save` 持久化 budgets/owner_name；`api-contract §2` 又用 GET/PUT /api/settings（schema settings 表），存在双源 | Lead 定运行时权威源（task-23 C10） |
| m2 | domain vs requirements | §11.3 资源可见性 `private/department/project/org/human-only`；FR-MEM-10 记忆可见性 `agent-private/team/org/human-only`；domain 只有一套 `Visibility*` 且 `human_only` 下划线；`MemoryScope*` 只有 assistant/department/employee，与附录 E `employee|department|project|task|decision|dispute` 不一致 | task-23 C11（映射表） |
| m3 | 事件类型 | 契约 §4 列 8 个 SSE 类型；附录 D 是消息类型（含 group_event/write_event）；domain 无事件类型常量 | task-23 C12 |
| m4 | server.go | 无 JSON 404/405、无 panic recover（500 会断连接）；静态 `/assets/` 只白名单后缀设置 Content-Type（未知后缀按默认，可接受） | 并入 task-23 A2 |
| m5 | 安全 | `POST /api/writes`「内部/测试创建写事件」若在生产暴露需 owner 权限+审计或仅在测试模式启用（契约未写） | Lead 确认（task-23 B） |
| m6 | 数据目录权限 | Windows 实测 data dir `0777`、config.json/secrets.json `0666`；FR-SEC-05 的 0600/0700 在 Windows 无意义；secrets 明文 | task-22 #7（DPAPI/ACL，R8） |
| m7 | 持久性口径 | `store.Open` 用 `synchronous=NORMAL`（WAL）：进程崩溃不丢，但 OS 断电可能丢最近提交；requirements NFR-REL-01/AC-07 未区分「进程崩溃」与「断电」 | Lead 在需求侧明确口径（建议 AC-07 只承诺进程崩溃零丢失） |
| m8 | schema 完整性 | DDL 中 `FOREIGN KEY` 出现 0 次（`PRAGMA foreign_keys=ON` 只是装饰），引用完整性全靠应用层 | Lead 决策是否加 FK；若不加，删除该 PRAGMA 或注释说明 |
| m9 | 迁移机制 | v1 是"整份 schema.sql 在事务内执行 + schema_migrations 版本"；注释说"后续版本以 migration 文件追加"，但 runner 尚未实现 per-version 文件机制，R1 之后改表前必须定（否则冻结版无法演进） | 建议 R4（schema v2）前补 migration runner |
| m10 | 删除后幂等语义 | 消息删除后同 `client_id` 重放会返回"墓碑消息"（`findMessageByClientIDTx` 不筛 `deleted_at`），前端可能收到已删除消息；不违反 Lead 分诊的行为断言，但需明确"删除后重试应新建还是返回墓碑" | Lead 确认；当前行为先记录 |
| m11 | 测试资产 | `tests/verify/schema` 8 个 Skip 为**已分诊缺口**（R1/R4/R8），不是失败；R1 后把 R1 项改为硬断言，R4/R8 保持 Skip 直到对应轮次 | 随轮次推进更新 |

## 5. 缺陷任务（已建，task-14 追踪）

| 任务 | 内容 | 建议 owner | 轮次 |
|---|---|---|---|
| **task-20** | R1：client_id 幂等行为落地与验证（同 client_id 两次 = 1 消息 + 1 事件；store 已过，待 HTTP 复验） | platform-dev | R1 |
| **task-21** | R4：attachments P0 缺口（schema v2 + /api/attachments + 头/权限/上限验收） | platform-dev（core-dev 协作） | R4 |
| **task-22** | R8：schema v2 增补（notifications 唯一/schedules 补跑/approvals 双钥匙/usage 幂等/search sender_id/conversation_reads/Windows DPAPI） | platform-dev | R8 |
| **task-23** | 契约缺口与决策（health 包、JSON 错误包、5 类缺失端点、双源配置、可见性/事件枚举） | Lead（契约）+ platform-dev | R1 起 |

## 6. R1 HTTP 黑盒验证清单（platform 包编译通过后执行）

测试文件计划：`tests/verify/http/r1_platform_test.go`（用 `httptest` + 临时数据目录，复用 store 探针的 seed 手法）。按 AC-01/06/07 与 api-contract 顺序：

1. **setup/引导**：GET /api/bootstrap `setup_required=true` 且含 `csrf_token`；POST /api/setup 创建 owner+助理+DM、Set-Cookie（HttpOnly/SameSite=Strict）；二次 setup 拒绝；重复启动不重复建助理。
2. **鉴权**：无 Cookie 访问 /api/conversations → 401 JSON；错误 Cookie 401；logout 后失效。
3. **CSRF/Origin（AC-08）**：POST 缺 `X-Lignting-CSRF` → 403；带正确值 → 201；`Origin: evil` 的 GET/SSE/POST 全部拒绝；无 CORS 头。
4. **消息（AC-01/07）**：POST 带 `client_id` 重放 → 同 seq、1 消息 1 事件；分页 before/after 升序；PATCH 编辑可见、DELETE 后列表不可见且搜索不可见；未读/已读推进与 §3 行为一致。
5. **SSE**：`GET /api/events?since=0` 按 seq 升序补发；`Last-Event-ID` 等价；心跳 20s（长跑用例标记 slow）；事件 payload 与 DTO 一致。
6. **搜索（AC-06）**：中文 2 字（限流）、3 字、短语、中英混排、代码标识符、路径；过滤 department/conversation/sender/time/kind；片段高亮；写入后 ≤100ms 可搜（轮询断言）；删除不可搜；无权限内容不出现且不泄漏存在性（不同身份对比 total）。
7. **重启（AC-07）**：kill -9 → 重启 → 消息/事件/已读位置/审计链完整；`VerifyAuditChain` 通过；WAL 恢复。
8. **错误模型**：/api/* 404、405、非法 JSON、超大 body（1MB 上限）均返回 JSON 错误包且不泄露堆栈。

## 7. 复现命令

~~~powershell
# R0 基线（commit f7a2566 的绿快照，工作树干净时）
go version              # go1.27.4 windows/amd64
go build ./...
go vet ./...
go test ./...

# R1 存储层独立验证（当前工作树可用）
go test ./tests/verify/store -v
go test ./internal/store -count=1

# 契约/数据探针（已分诊的 Skip 是已知缺口，不是失败）
go test ./tests/verify/schema -v

# 黑盒（R0 预编译二进制；R1 后改为自己构建）
bin/lignting.exe --data-dir tests/verify/.tmp --addr 127.0.0.1:18791
curl -i http://127.0.0.1:18791/api/health
go run ./tests/verify/dbcheck tests/verify/.tmp/lignting.db
~~~

## 8. 测试资产（我的写作用域）

~~~text
tests/verify/
  doc.go                      约定：R0 硬断言；缺口 Skip(TRIAGED) 可追踪
  http/r0_baseline_test.go    R0 退出标准 + 安全头/CORS/穿越（当前不依赖 R1 路由）
  http/contract_probe_test.go 契约探测（health 包、JSON 错误、Origin/CSRF、bootstrap、SSE；R1 后转硬断言）
  schema/schema_probe_test.go schema/表/字段/约束/PRAGMA/权限现状（8 个 Skip = 已分诊 R1/R4/R8 缺口）
  store/r1_store_test.go      R1 存储层行为（5/5 PASS）
  dbcheck/main.go             只读 DB 检查工具（表清单/schema 版本/WAL）
~~~

> 说明：`tests/verify/http` 当前因 platform 包编译失败无法运行；platform 合入后我会把它跑成硬断言，并在 R1 报告里给出 AC-01/06/07 的逐条证据。`tests/verify/schema` 的 Skip 不是"测试失败"，而是已分诊缺口清单的机器可读版本（每轮把对应项改成硬断言）。

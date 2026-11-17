# DSH 运行时研究备忘录

> 任务：task-1《研究 DSH 运行时：session/记忆/集成接口》，服务于 `doc/target.md` 与 `doc/0.0.1 最初版/requirements.md`。
> 研究对象：本机 `@deepseek-ai/dsh` **0.2.1-rc.5**（MIT，包内 package.json 声明仓库 deepseek-ai/deepseek-harness）。
> 本机无外网，以下结论以本地包 README（中文版）与 `lib/types/*.d.ts` 为依据；README 中链接的 `docs/subsystems/*.md`、`.agents/notes/*` 未随 npm 包分发，无法核对。
> 路径缩写：`$DSH` = `C:\Users\cihu0\AppData\Local\nvm\v24.14.1\node_modules\@deepseek-ai\dsh`，`$PKG` = `$DSH\node_modules\@deepseek-ai`。

## 0. 结论先行

1. DSH 的本质是**事件溯源会话日志 + Cordis 插件树运行时**：模型可见即已记录，模型历史、标题、检索、持久化都从只追加的 `SessionEvent` 流派生（`$PKG\dsh-session\README.zh.md`）。
2. 它的"记忆"其实是**会话记忆**：一条会话一个 JSONL（默认 Zstd）日志，靠 compaction 在活跃对话中"遮蔽"旧历史；**没有跨会话长期记忆、档案、触发式记忆**（全局 README 检索只找到第三方 MCP 记忆指南的引用）。
3. 会话检索是"**派生索引**"：`ctx.sessionQuery` 提供精确读/过滤/血缘追踪，SQLite FTS5 后端提供带排序的 token 短语搜索；它索引的是 DSH 会话日志，不是通用消息库（`dsh-session-query`、`dsh-session-query-sqlite`）。
4. 外部集成有三条真实通道：**headless CLI**（一次性 + 行 JSON 事件流）、**SDK stdio JSON-RPC**（私有协议，按 sessionId 多路复用）、**ACP stdio**（标准 Agent Client Protocol v1，带 cancel/close/permission）。另有 Web/Typert RPC，但那是浏览器内部接口，不适合外部程序。
5. 多 Agent 能力只到"**单进程内**"：subagent 的驻留、agent-team 的 roster/mailbox、goal 的续行都是进程内状态；README 明确写"不支持跨进程协调""无持久 parent mailbox""无持久邮箱与租约"。
6. 因此对本项目：**对话平台、消息检索、四层记忆、跨团队编排必须自研**；DSH 最值得复用/借鉴的是 session 事件模型、压缩事务、检查点策略、提示词组装与 skill/MCP 扩展点，以及在 0.0.1 阶段把 DSH 当作"第一个 Agent 运行时后端"。
7. 许可 MIT，可放心借鉴设计甚至复制协议结构；但 0.2.1-rc.5 是**预发布且无兼容承诺**，集成必须固定版本 + 适配层 + 金丝雀升级。
8. 0.0.1 的推荐形态：Go 平台（消息/任务/记忆/审计的唯一真源）+ 每团队（或每成员）一个 `dsh --profile acp` 子进程，通过 ACP 驱动，平台侧镜像关键事件。

## 1. 证据基础与包地图

- 安装布局：`dsh` 是 CLI 壳（`lib/bin.js`），全部实现以约 300 个 `@deepseek-ai/dsh-*` 包平铺在 `$PKG` 下；没有源码，只有 `lib/`（编译产物）与 `lib/types/*.d.ts`（类型声明，可直接读）。
- 运行时骨架是 Cordis v4：插件 = 服务 + 事件 + `ctx` 作用域；profile = bundle patch 叠加（`dsh-app-boot`），随包 `web/headless/sdk/acp/sdk-minimal` 五个 profile 均在 `$DSH\node_modules\@deepseek-ai\*`。
- 本报告最关键的 10 个本地依据：
  `$PKG\dsh-session\README.zh.md`、`$PKG\dsh-session-persistence-jsonl\README.zh.md`、`$PKG\dsh-session-query\README.zh.md`、`$PKG\dsh-session-query-sqlite\README.zh.md`、`$PKG\dsh-compaction-basic\README.zh.md`、`$PKG\dsh-session-checkpoint-policy\README.zh.md`、`$PKG\dsh-headless\README.zh.md`、`$PKG\dsh-sdk-protocol\README.zh.md`、`$PKG\dsh-sdk-jsonrpc-server\README.zh.md`、`$PKG\dsh-acp\README.zh.md`。
- 版本事实：`$PKG\dsh-session\package.json` → `0.2.1-rc.5`、`license: MIT`、peer `cordis ~4.0.5`；当前会话格式版本在 `$PKG\dsh-session\lib\types\types.d.ts` 中为 `SESSION_FORMAT_VERSION = 4`。

## 2. (a) 会话生命周期、事件模型、持久化、恢复与压缩

### 2.1 生命周期（create → append → derive → fork → flush → dispose）

- `ctx.sessions.create(id, {meta})` 在内存建会话；`get/list` 查活会话；`fork(source, boundary?)` 从活源会话精确前缀复制子会话。
- `session.append(type, data, opts)`：先深度快照/冻结载荷、校验无损 JSON，再通知观察者；拒绝的追加不改变日志、派生状态与事件流。
- `deriveMessages()` 把日志投影成模型历史（增量、缓存、返回深冻结新数组）。
- `ctx.sessions.flush(session)` 是**持久性屏障**：等待每个持久化监听器刷新后返回；生产方应等它而不是假设批处理已排空。
- 生命周期事实源在 agent-loop：`create` 时 `persistence.create(header)` 取得写所有权，`resume` 时 `open(id,'write')` 排他恢复；teardown 先提交收尾事件再 close 句柄（`$PKG\dsh-agent-loop\README.zh.md`）。

### 2.2 事件模型

- 事件词汇（`$PKG\dsh-session\lib\types\types.d.ts`）：`turn/start|turn/end`、`step/start|step/end`、`user/message`、`developer/message`、`system/message`、`assistant/message`、`assistant/attempt`、`tool/call`、`tool/result`、`request/header`、`request/context`、`session/end-seed`，以及 `agent/inbox/*`、`compaction/*`、`goal/change`、`team/member`、`team/task`、`subagent/descriptor`、`plugin:*` 等。
- **surface（表层）**是核心抽象：surface 事件（system/developer/user/assistant/tool-result）带 `surfaceOp`，支持 `{op:'replace', startSeq, endSeq}` 遮蔽旧节点但不删日志；模型历史 = 折叠 surface + 消息投影。
- **日志即事实，surface 即可见**：`assistant/message` 内嵌紧凑 stream，`assistant/attempt` 保留失败/重试/取消且不进历史；`request/header` 记录每次请求的完整规范快照（工具 schema、调用配置、适配器默认值），使"每个请求都是日志的纯函数"。
- 系统提示词也是 surface 第 0 号节点（`system/message`），不是 request header 的字段；替换第 0 号节点受保护，压缩不能遮蔽它。

### 2.3 持久化（JSONL + Zstd generations）

- `dsh-session-persistence-jsonl` 是唯一第一方后端：`root` 必填；磁盘布局 `<root>/--<normalized-cwd>--/<encoded-id>/session.vN.jsonl[.zstd]`；默认 `compression: zstd`（带校验和帧），`none` 时为可外部逐行读取的纯文本。
- 延迟实体化：`create` 不写盘；首次 `append` 才以无覆盖发布 + fsync 写入 header 与首批；从未 append 的会话不留文件。
- 崩溃语义：已提交前缀全部保留；撕裂的原始尾行丢弃；撕裂的 Zstd 帧只取其中完整记录，写句柄截断并重写；**恢复时读方通过写句柄追加合成 closer**（不重写已提交事件）。
- 并发：**每会话一个活动写入方**；进程内认领 + 内核锁（POSIX `flock`，Windows 命名信号量）；崩溃持有者锁随进程消亡。
- 明确限制：**没有删除会话的接口**，日志在 root 下累积；格式迁移保留编码、不改压缩；POSIX 实体化需硬链接与预编译 addon。
- 格式迁移：`dsh-session-format` + `dsh-session-format-catalog` 提供相邻版本（v0→v1→v2→v3→v4）编解码与单遍迁移；历史写打开会"发布后继 generation"并保持源文件字节不变。

### 2.4 恢复与检查点

- `dsh-session-checkpoint-policy` 定义三道屏障：模型请求前 flush、顶层工具正文执行前 flush、每个 `agent/pre-step` 前 flush；**fail-closed**——持久写入成功前不调用适配器、不执行工具正文。
- 崩溃后的工具语义：无 `tool/call` 记录的调用补 `TOOL_NOT_STARTED`；有调用无结果的补 `TOOL_OUTCOME_UNKNOWN`，并要求模型"只重试只读/幂等操作，否则先验证外部状态或询问用户"。
- 关键取舍：记录的是"已分派意图"而非"恰好一次副作用"；有副作用工具应把 `exec.callId` 当幂等键转发。

### 2.5 压缩（compaction）

- `dsh-compaction-basic` 的设计很值得抄：触发阈值 `floor(min(W×0.8, W−O−65536))`（W=上下文窗口，O=输出预留），逐字保留近期 `W−O` 的 16%。
- 事务是"日志记录标记对"：`compaction/start` → 摘要请求 → 重新校验稳定性 → `compaction/summary` + 一条替换 `user/message` → `compaction/end`；未闭合的 start 是持久锁（`busy`）。
- 摘要请求复用**同一会话前缀**（节点 0 的系统提示词 + 上次已路由工具 + 被遮蔽消息），因此只放弃尾部指令与摘要输出的缓存；摘要指令是固定的 Markdown 结构（Primary Request / Key Concepts / Files / Errors / Pending Jobs / Current Work / Next Step / Critical Context）。
- 边界：压缩不能缩减系统提示词、工具 schema 或不可分单元（一次超大工具调用）；摘要失败默认"携带超预算历史继续"。
- 对本项目：这套"阈值—保留—标记事务—前缀复用—失败不回滚"的策略，可直接映射为 Go 平台的会话压缩设计。

## 3. (b) 会话检索/搜索能力

- 分两层：**服务** `dsh-session-query`（实时优先、克隆返回、精确读/过滤/血缘/事件追踪）+ **可选搜索后端** `dsh-session-query-sqlite`。
- 过滤器的 `text` 是**字面、不区分大小写、空白灵活的正则扫描**，无排名；需要排名时用后端 `searchSessions/searchEvents`。
- SQLite 后端：独立派生库（不是 session-persistence 库）、FTS5、`unicode61` tokenizer，**匹配 token/短语，不匹配任意子串**（"AI" 不命中 "BRAID"）；默认 limit 20、max 100、snippet 240 码点。
- 排序确定性：命中 span 多者优先 → 文档更短者优先 → 时间/sessionId/seq 打破平局；分页用**世代绑定游标**，语料变化即 `SESSION_QUERY_STALE_CURSOR`，宁可失败不给错页。
- 索引生命周期：每次搜索做一次串行化观察——列持久快照、比修订、只读新增/变更日志、在内存补齐中断轮次、一个事务提交对账；实时会话用 TEMP 表遮蔽持久基库。
- 明确限制：**无调用方授权**（"模型工具或 UI 必须自行限制可检查的会话"）；同步执行阻塞 JS 线程；单进程独占索引路径；schema v8 + application id 防误开。
- `dsh-experimental-session-search` 只是把这套能力包装成 5 个只读全局工具，索引 `:memory:`、首次搜索打开、重启重建；Web/Desktop 默认关闭。
- **对本项目**：不要指望 DSH 检索承担"对话平台消息检索"。但它的三个设计可复用：(1) 源日志与派生索引分离、索引可丢弃可重建；(2) 对账式增量索引 + 修订比较；(3) 游标绑定世代，避免分页错位。平台需要自建消息级索引（作者/团队/会话/时间/类型）+ 中文分词策略。

## 4. (c) headless / SDK / ACP 三种外部集成

### 4.1 headless（一次性任务）

- 形态：`dsh --profile headless "task"`，单任务、无端口、跑完退出；任务也可来自 stdin。
- 机器接口：`--json` 输出**行 JSON 事件流** `session → status/text/thinking/tool_call/tool_result → final`；除 `final` 外字符串/对象键限 8 KiB、单行 32 KiB、嵌套 64 层截断；`--session-id` 可续同一会话（要求组合 sessionPersistence + sessionQuery，且 cwd/preset/归属必须匹配；拒绝子 agent 与 fork 会话）。
- 失败信号：退出码 0/1；轮次内失败仍可能以空的 `final` 结束，**必须同时看 exit code 与 `turn_end` 原因**；runner 级失败写 `error` 事件且无 `final`。
- 适用：CI、脚本、批处理、一次性问答。不适用：多轮交互、取消、并发会话。

### 4.2 SDK stdio JSON-RPC（私有协议）

- 传输：`\n` 分帧的 JSON-RPC 2.0，stdout 专属协议、诊断走 stderr；7 个 client→server 请求（initialize、session/prompt、session/resume、session/wait、working-directory get/set、shutdown）+ 4 个 server→client 通知（session.event、session.status、subagent.started/finished）。
- 语义：每 `sessionId` 首次使用时建 agent；`session/prompt` 只返回入队 `messageId`，随后所有事件按会话流式推送；`session/wait` 等根 agent 及其后代完全停稳；`shutdown` 排空并以 0 退出。
- 限制：**无协议版本协商**（握手只带 `serverInfo.version='0.0.1'` 且客户端不校验）；**无取消、无逐会话关闭**（agent 活到进程退出）；**无逐提示词结果**（客户端要自己定义并观察区间）；stdout 纯净性由部署保证；自动挂载适配器只支持 DeepSeek。

### 4.3 ACP（标准协议，仅自动化）

- 标准 ACP v1 over stdio：`initialize/authenticate/session/new|list|resume|close/set_config_option/prompt/cancel`，通知 `session/update`，反向请求 `session/request_permission`；支持挂载 stdio 或 Streamable HTTP MCP、图片提示词、按会话选择 model/reasoning_effort。
- 特意只给标准语义：不提供 DSH 私有展示数据、plan、todo、terminal、elicitation；**不支持 session/load、fork、删除、附加目录、transcript 回放**；一个连接可并发多会话。
- 结算语义适合编排：一次 prompt 要等根 agent + 受管后代 + 有序更新都完成才返回；cancel 会等调用时已有的活动与其排队输出；`session/close` 停稳式释放且只释放指定 agent 作用域。
- 适用：进程外 subagent、测试运行器、脚本化控制器、**另一个 harness 驱动 DSH**——正是 lignting 的角色。

### 4.4 Web / Typert RPC（不建议作为外部接口）

- `dsh-api-gateway` 是 Host↔Client 的 Typert RPC（`/api` unary + `/api/remote.mux` WebSocket multiplex + peer principal + remoteAccess 判定），`dsh-api-session-controller` 在其上暴露 session/skills/fileReferences 命名空间。
- 它是**浏览器内部接口**：依赖启动 token + 签名 cookie、生成式 descriptor、Client 侧代码投影；无公开协议文档与版本承诺。外部 Go 程序走这条路会把耦合面从"协议"扩大到"前端运行时"。
- 结论：读它可以理解 DSH 的会话控制能力，但不要据此对接。

### 4.5 三通道对比（面向 lignting-agents）

| 维度 | headless | SDK JSON-RPC | ACP v1 |
|---|---|---|---|
| 交互形态 | 一次性任务 | 多轮、按 sessionId 多路复用 | 多轮、一连接多会话 |
| 取消 | 无（只能杀进程） | 无 | 有（prompt 所属取消路径） |
| 会话关闭 | 进程退出 | 无（活到进程退出） | `session/close` |
| 恢复 | `--session-id`（受限） | `session/resume` | `session/list` + `session/resume` |
| 权限/审批 | 无交互 | 无专门通道 | `session/request_permission` |
| 事件粒度 | 投影事件流（限长） | 全量 session.event | 标准语义更新 |
| 标准性 | DSH 私有 | DSH 私有、预发布、无版本协商 | 公开标准、v1 |
| 适合 | 批处理/CI | 需要 DSH 特有能力的深度集成 | **平台编排、外部 Agent 统一契约** |

## 5. (d) 提示词组装、instructions、persona、skills、tools 的扩展点

- **系统提示词注册表**（`dsh-system-prompt`）：段（section）带 `name/order/text/interpolate`，按 order 升序、同名按名称排序拼接；`complete:true` 段可独占整份提示词（多个即组装失败）；变量 `{{name}}` 严格解析（未知引用直接报错）；`toolOrder` 控制模型可见工具顺序；作用域注册（`agent.ctx`）可遮蔽全局同名项。
- 顺序值：harness 身份 −1000、persona 前缀 0、第一方指导、harness 源码 10000、Web 表层 10100、persona 后缀 10200；`refreshContext()` 在 pre-step 后刷新运行时事实。
- **persona**（`dsh-persona`）只能在 agent scope 挂载：让单个 preset 拥有自己的前缀/后缀，可 `complete:true` 独占提示词、可 `includeRuntimeContext:false` 抑制可选上下文（必需上下文仍保留）。
- **agent instructions**（`dsh-agent-instructions`）：与 `AGENTS.md`/`CLAUDE.md` 兼容，用户全局（`$DSH_HOME/AGENTS.md`、`~/.agents/AGENTS.md`）+ 项目链（项目根 → cwd 逐级）；默认预算 **65,536 字节**，宽泛文件先省略、最具体文件最后截断；注入是一条带来源的持久 user 消息（`<system-reminder>` 框架），**没有 watcher**，靠成功的 read/write/edit 或恢复对账触发刷新。
- **skills**（`dsh-skill` + `dsh-skill-filesystem` + `dsh-tool-skill`）：多提供方合并目录、按需加载正文、`modelInvocable/userInvocable` 两种调用策略；无 TTL，失效由提供方 `invalidate()` 驱动。
- **tools**：`ctx.tools` 注册表 + `ToolRuntime` 自动贡献 schema；工具可按 agent scope 注册；MCP 工具以 `mcp__<server>__<tool>` 命名（stdio / streamable-http，指令上限 32 KiB，自动重连退避）。
- **运行时上下文**：工作目录、沙箱策略、委派范围声明等以"带来源的 user 角色快照"注入，不是系统提示词。
- 对本项目：若托管 DSH，"自适应提示词"可用 preset patch（persona + 段 + 工具 + skill）表达；四层记忆中的档案/触发式记忆则需要平台侧在每次 prompt 前拼装成上下文（可借 MCP resource 或 user 上下文注入），DSH 本身不提供。

## 6. (e) 多 Agent / subagent / agent team / goal 的现状与限制

- **subagent seam**（`dsh-subagent`）：一个服务 + 多具名提供方（spawn/fork/ACP/DSH SDK/Codex/Claude Code）；`startActivation` 返回 `childId/result/dispose`；本地子级可续、外部子级一次性；`maxActiveSubagents` 默认 8、`maxDepth` 默认 1；`delivery:'parent'|'caller'` 决定完成通知还是工作流收集结果。
- **单进程是硬边界**：README 明确"驻留仅限进程内，Activation inbox 与所有权图不跨 harness 进程协调""没有持久化 parent mailbox""接受标识、不保证恰好一次投递""child→parent 要求直接 parent 在线"。
- **agent team**（`dsh-experimental-agent-team`，实验性）：每个 root 会话隐式是一支 Team 的 Lead（TeamId=SessionId）；roster 持久在 Lead 日志（`team/member`），任务板也持久在 Lead 日志（`team/task`，CAS revision、DAG 依赖、`writeScopes` 仅提示重叠）；消息走目标 agent 的 inbox（`Team message from <name>:` 前缀），不写新的 mailbox 记录。
- 上限与限制：`maxMembers` 16、`maxTasks` 256、单条消息 64 KiB；**扁平不可变 roster**（只有 Lead 能建直接 teammate、不能嵌套/改名/删除/复用名字）；**不自动释放 task owner**；**单进程 + 共享 checkout**（无 worktree/远端/merge/文件锁）；write scope 只是提示，Bash 可绕过；**完整 roster/task 广播给所有浏览器**。
- **goal**（`dsh-goal`）：每会话一个持久完成目标，phase `active/paused/blocked/complete`，CAS `{id,revision}`，`defaultMaxGoalRounds` 256；只存状态不调度（调度在 `dsh-goal-round-driver`）；续行启用（armed/disarmed）是**进程本地**、不持久；只有 Round 预算，无 token/时间/评估器。
- **ralph**（`dsh-tool-ralph`）给出另一个值得抄的模式：每 Round 一个全新子 agent，只接收不可变目标 + 上一份 ≤16 KiB 结构化交接，"**共享工作区是长期记忆**"，父对话与旧会话绝不作为种子。
- 对本项目：DSH 的 team 机制可以当"单进程内的最小原型"和 API 设计参考（CAS 任务板、writeScopes 提示、显式权限），但**跨进程、跨团队、可恢复的编排必须自研**。

## 7. (f) Go 程序"托管/驱动 DSH agent"的可行路径对比

### 路径 A：子进程 headless（每次任务一个进程）

- 做法：Go 拼 `dsh --profile headless --json --session-id ... "task"`，解析行 JSON。
- 优点：最简单、无长连接、退出码清晰；无端口、无认证面；适合 CI/批处理。
- 缺点：**无取消（只能杀进程）、无多轮交互（同一对话靠多次拉起 + --session-id，且受 cwd/preset/归属限制）**、事件流是投影且限长（8 KiB/32 KiB）、每次冷启动成本高。
- 稳定性/许可：CLI 参数是 DSH 私有约定；MIT 无许可障碍；运维简单但吞吐差。
- 结论：只用于"离线/批量/报告"类任务，不作为平台主通道。

### 路径 B：SDK stdio JSON-RPC（每 Agent 或每团队一个常驻进程）

- 做法：Go 实现 JSON-RPC 行分帧客户端，`dsh --profile sdk` 拉起；`initialize` → `session/prompt` → 消费 `session.event`；`session/wait` 等停稳；`shutdown` 收尾。
- 优点：多轮、低延迟、按 sessionId 多路复用、能拿到全量 `SessionEvent`（便于平台落库/回放）、有 `session/resume` 与 working-directory。
- 缺点：**无取消、无逐会话关闭**（agent 活到进程退出，长跑会累积内存）；无版本协商；stdout 纯净性需自己保证；DeepSeek 之外的适配器要预注册；事件语义要自己归因到 prompt。
- 稳定性/许可：私有预发布协议，**无兼容承诺**；需 pin 版本 + 契约测试；MIT。
- 结论：适合需要 DSH 特有能力的"深度适配器"，不建议作为平台唯一契约。

### 路径 C：ACP stdio（推荐作为主契约）

- 做法：Go 实现 ACP v1 客户端（或复用社区 SDK），`dsh --profile acp` 拉起；`session/new|prompt|cancel|close`，处理 `session/update` 与 `session/request_permission`。
- 优点：**公开标准 + 有版本（v1）+ 有取消 + 有会话关闭 + 有审批通道 + 可挂 MCP**；结算语义天然适合"主管等成员完成"；一个连接多会话；未来可接 Codex/Claude Code 等其他 ACP agent（DSH 自己的 subagent 就同时支持 ACP/SDK/Codex/Claude Code，说明这套抽象可行）。
- 缺点：不提供 transcript 回放、fork、删除、附加目录、DSH 私有展示；stdout 必须纯净；仍是"每进程一组会话"，进程级故障会同时影响该进程所有会话。
- 稳定性/许可：标准公开（agentclientprotocol.com），DSH 侧仍是 rc 实现；MIT。
- 结论：**0.0.1 主通道**；平台侧自己落库 session/update 即可补齐"不可回放"的缺口。

### 路径 D：直接复用 Web/Typert HTTP API

- 做法：Go 模拟浏览器，拿启动 token → cookie → 调 `/api` RPC / `/api/remote.mux`。
- 优点：能用到 GUI 同款能力（会话列表、队列、审批、文件引用）。
- 缺点：**内部接口、无契约、生成式 descriptor、peer/principal/remoteAccess 语义复杂、认证是浏览器 token+cookie**；升级即破坏；等于把前端当协议。
- 结论：不采用。

### 路径 E：只借鉴设计、Go 自研运行时

- 做法：Go 实现 agent loop（LLM 适配、工具执行、事件日志、compaction、权限），DSH 只作为设计参考。
- 优点：单语言、单进程模型清晰、记忆/编排/检索完全可控、无 Node 运行时与 npm 供应链。
- 缺点：工作量最大（模型适配、流式、工具沙箱、压缩、重试、MCP 客户端、skill/instructions、诊断）；0.0.1 很难同时做平台 + 运行时。
- 结论：作为 target.md 的**长期形态**与接口设计的"第二个实现"，不作为 0.0.1 交付路径。

### 建议（0.0.1）

1. 在 target.md 里定义 `AgentBackend` 抽象：`spawn(team, role, preset) / send(prompt) / cancel / close / events()`，把"DSH 进程"作为第一个实现；这样未来换 Go 原生运行时或接其他 ACP agent 不改上层。
2. 0.0.1 实现顺序：**ACP 客户端 → 平台消息/事件镜像 → 团队编排**；SDK 适配器作为"需要 session/wait、working-directory、DSH 特有事件"的补充，按需增量。
3. 进程拓扑：先做"**每团队一个 dsh 进程 + 团队内多会话**"（减少进程数、共享 MCP 连接），但要接受"进程崩溃=整队失联"；若要强隔离再退化为每成员一进程。两者都要有 supervisor（重启、退避、日志）。
4. 版本策略：pin `0.2.1-rc.5`（或升级时逐条跑契约测试），Node 版本固定，profile 目录与 `DSH_HOME` 收归平台管理；启动前 `dsh --dump-config` 自检插件树。
5. 观测：平台侧把 ACP `session/update` 与 DSH session 日志双向对照（镜像 + 定期重放校验），否则协议变化时无法定位"是 DSH 变了还是我们理解错了"。

## 8. (g) 可复用 / 必须自研的边界

| 能力 | DSH 现状 | 建议 |
|---|---|---|
| 会话事件模型（turn/step/message/tool/request-header/surface） | 成熟、语义完整 | **直接借鉴**，作为平台 MemberSession 的规范；必要时做 DSH 事件 ↔ 平台事件的映射层 |
| 只追加日志 + JSONL/Zstd + 代际 + 崩溃恢复 | 成熟 | 借鉴文件布局与恢复语义；Go 平台可用 SQLite/自研 WAL，但保留"日志为真源、索引可重建" |
| 压缩（阈值/保留/标记事务/前缀复用） | 成熟 | 借鉴策略与不变量；摘要 prompt 结构可直接复用 |
| 检查点策略（请求前/工具前/pre-step） | 成熟 | 借鉴 fail-closed 三屏障 |
| 检索（查询服务 + FTS5 派生索引 + 世代游标） | 可用但面向 DSH 日志 | **借鉴架构、平台自研**消息级索引与权限 |
| 提示词组装（段/变量/persona/instructions） | 成熟 | 托管 DSH 时直接用 preset；Go 侧自研等价物 |
| skills / MCP / 工具扩展 | 成熟 | 托管 DSH 时复用；平台只要定义"工具/技能来源"抽象 |
| 单进程内 subagent / team / goal | 实验性、进程内 | 只作原型参考；**跨进程编排自研** |
| 四层记忆（短期/触发/档案/自适应） | **不存在**（README 把记忆引向第三方 MCP） | **必须自研**：存储、触发规则、召回、提示词拼装、审计 |
| 对话平台（消息全量落库、检索、UI） | 无 | **必须自研** |
| 任务/产出/辩论/返工 | 仅有实验性 task board | **必须自研**（可借鉴 CAS + DAG + writeScopes） |
| 权限/审批/审计 | ACP 有 permission；DSH 有 sandbox/approval | 平台统一策略层，DSH/ACP 作为执行点 |
| 多机/多进程协作 | 明确不支持 | **必须自研**（或用消息队列/平台中枢） |

一句话：**DSH 是"单机单进程 Agent 运行时"的优秀参考实现，不是"多 Agent 协作平台"**。lignting 的平台层、记忆层、编排层都是新增价值，DSH 提供的是运行时内核与一套值得对齐的协议/事件语义。

## 9. 对两份文档的具体建议

### 对 `doc/target.md`

1. 把系统拆成三个平面并明确真源：**对话平面**（Go 平台：消息/任务/成员/审计的唯一真源）、**运行时平面**（可插拔 AgentBackend，第一实现为 DSH 子进程；长期可换 Go 原生）、**记忆平面**（平台侧四层记忆 + 成员 session 记忆）。
2. 明确"会话 ≠ 对话"：平台会话（人-Agent 沟通线程）与 Agent 工作 session（事件日志）是两个实体，用映射表关联；否则后期无法解释"同一条消息为何出现在两个 session"。
3. 写清 Agent 生命周期契约：spawn / ready / prompt / cancel / close / crash-restart，以及"崩溃后哪些事实一定不丢"（引用 DSH 的 TOOL_OUTCOME_UNKNOWN 语义作为参考）。
4. 写清记忆分层与归属：短期=session 历史+压缩；触发式=平台事件规则；档案=平台结构化存储；自适应提示词=拼装器（persona/instructions/skills/记忆摘要）。
5. 写清团队模型：Lead（主管 Agent）+ 成员 + 共享任务板 + 产出/评审/辩论记录；说明"团队是逻辑边界，不承诺进程隔离"（首版）。
6. 标注 DSH 为"第一个运行时后端"而非依赖：引用其 MIT 许可、预发布状态与协议限制，避免文档把内部实现写成永久契约。

### 对 `doc/0.0.1 最初版/requirements.md`（只写需求，不写方案）

- 用户故事：主管登录 → 自动获得助理 → 与助理商讨 → 创建产品/研发/测试团队 → 分派任务 → 成员产出 → 主管核对 → 不合格返工/跨团队辩论。
- 功能需求：消息全量落库（不可丢、可审计、可导出）；检索（关键词/全文/按团队/按 Agent/按时间）；会话与记忆（成员 session；主管/助理四层记忆的最小闭环：档案 CRUD + 触发规则 + 提示词拼装）；任务板（依赖、owner、状态）；产出核对与返工记录；审批/权限；崩溃恢复与重启续跑。
- 非功能：本地优先、单机可跑、离线可启动、数据可迁移；P95 检索延迟目标；日志可回放；**明确"不写具体实现方案"**（不出现 ACP/SDK/DSH 字样）。
- 非目标（首版）：多机/多租户、跨进程团队、worktree 隔离、无审批自治、任意工具沙箱逃逸防护、跨团队自动辩论的完全自治。
- 验收：重启后消息不丢、会话可恢复、档案可检索、任务 owner 可追溯、返工有记录、崩溃后工具副作用不重复执行（或显式标记未知）。

## 10. 仍需决策的问题

1. **运行时路线**：0.0.1 是"托管 DSH 子进程"还是"Go 原生最小 agent loop"？（决定 target.md 的依赖表述与 requirements 的验收口径）
2. **主契约**：ACP 为主 + SDK 补充，还是只做 DSH SDK？（ACP 标准但少 DSH 特有事件；SDK 全量但无取消/无版本）
3. **事件镜像策略**：平台是否双写 DSH `session.event`？还是只存用户可见消息 + 摘要（省存储但丢回放能力）？
4. **团队拓扑与隔离**：每团队一进程（省资源、共享 MCP、崩溃影响整队）vs 每成员一进程（隔离好、进程多、内存高）；以及首版是否接受"团队=单进程"的限制。

## 11. 来源清单

本地（可核查，按重要度）：
- `$PKG\dsh-session\README.zh.md`（事件溯源、surface、fork、flush、限制）
- `$PKG\dsh-session-persistence-jsonl\README.zh.md`（JSONL/Zstd、恢复、单写者、无删除）
- `$PKG\dsh-session-format\README.zh.md`、`$PKG\dsh-session-format-catalog\README.zh.md`（v0–v4 迁移）
- `$PKG\dsh-session-query\README.zh.md`、`$PKG\dsh-session-query-sqlite\README.zh.md`（查询与 FTS5）
- `$PKG\dsh-experimental-session-search\README.zh.md`（5 个只读工具、:memory: 索引）
- `$PKG\dsh-compaction-basic\README.zh.md`（阈值、区域事务、摘要指令）
- `$PKG\dsh-session-checkpoint-policy\README.zh.md`（三屏障、fail-closed）
- `$PKG\dsh-headless\README.zh.md`（--json、--session-id、退出码）
- `$PKG\dsh-sdk-protocol\README.zh.md`、`$PKG\dsh-sdk-jsonrpc-server\README.zh.md`（方法表、限制）
- `$PKG\dsh-acp\README.zh.md`、`$PKG\dsh-acp-app\README.zh.md`（ACP v1 契约）
- `$PKG\dsh-agent-loop\README.zh.md`（步骤、request/header、崩溃修复）
- `$PKG\dsh-system-prompt\README.zh.md`、`$PKG\dsh-persona\README.zh.md`；`$PKG\dsh-agent-instructions\README.zh.md`；`$PKG\dsh-skill\README.zh.md`；`$PKG\dsh-mcp-client\README.zh.md`
- `$PKG\dsh-subagent\README.zh.md`、`$PKG\dsh-experimental-agent-team\README.zh.md`（含 `tool-agent-team`）、`$PKG\dsh-goal\README.zh.md`、`$PKG\dsh-tool-ralph\README.zh.md`
- `$PKG\dsh-api-gateway\README.zh.md`、`$PKG\dsh-api-session-controller\README.zh.md`、`$PKG\dsh-web-app\README.zh.md`（内部 RPC，不采用）
- `$PKG\dsh-base\README.zh.md`、`$PKG\dsh-app-boot\README.zh.md`、`$PKG\dsh-session-projection\README.zh.md`、`$PKG\dsh-storage\README.zh.md`
- `$PKG\dsh-session\lib\types\types.d.ts`（`SESSION_FORMAT_VERSION = 4`、事件词汇）、`$PKG\dsh-session\package.json`（版本/许可）

外部（本机无外网，**未验证可访问性**，仅作线索）：
- 包内声明仓库：`https://github.com/deepseek-ai/deepseek-harness`（来自 `$PKG\dsh-session\package.json` 的 repository 字段）
- ACP 标准：`https://agentclientprotocol.com`（引用自 `$PKG\dsh-acp\README.zh.md`）
- MCP：`https://modelcontextprotocol.io`（引用自 `$PKG\dsh-mcp-client\README.zh.md` 的上游协议）

> 局限声明：本机无法访问外网（web_fetch 失败），未能核对上游仓库/文档站点；README 中引用的 `docs/subsystems/*.md`、`.agents/notes/*` 未随 npm 包分发，相关结论仅取自包内 README 与类型声明。

# 研究备忘录：Go 本地对话平台与消息检索方案

> 任务：task-4 / 服务于 `doc/target.md` 与 `doc/0.0.1 最初版/requirements.md`
> 范围：单机/本地优先、Go、单二进制、内嵌 Web UI 的对话平台；消息领域模型、检索链路、实时投递、进程边界、v0.0.1 选型。
> 结论先给：**SQLite(WAL) + FTS5 做唯一存储与检索；SSE 给 UI、游标长轮询给 Agent；Agent 通过 HTTP 契约接平台，进程可同可异；核心资产是 `seq` 全序 + 可重放，而不是 WebSocket 或搜索引擎。**

## 0. 一句话立场与总体判断

我们要做的不是"又一个 Slack"，而是 **一个本地的、可审计的、Agent 优先的对话内核（message log + search + delivery）**。这决定了三件事必须做对，其余都可以砍：

1. **每一条消息都有单调递增的 `seq`**（全序），这是排序、断线重放、去重的唯一权威；
2. **写入与索引在同一事务里完成**，检索结果不会"晚一拍"；
3. **投递语义是 at-least-once + 消费端幂等**，而不是"推送成功即送达"。

反之，下面的东西在"本地优先 + 单用户 + 单二进制"这个前提下**不带来收益，只带来成本**：多节点集群、消息中间件、独立搜索引擎、向量数据库、联邦协议、E2EE。参考系统之所以需要它们，是因为它们服务的是"成千上万用户的多租户云"，而我们的第一版服务的是"一个人 + 一支 Agent 团队"。

## 1. 参考系统：只取我们要的那一层

| 系统 | 语言/形态 | 对我们有用的一点 | 不该抄的部分 |
|---|---|---|---|
| **Mattermost** | Go 单二进制 + Webapp，业务全在服务端 | 证明"Go 单二进制做 IM 是可行的"；搜索演进路线是反面教材 | 引入 Elasticsearch/OpenSearch、多租户、插件体系 |
| **Zulip** | Python/Django + Tornado，stream/topic 强线程 | **事件队列 + `last_event_id` 确认**的投递语义；topic 作为一等公民 | 强制 topic、长轮询的复杂度（第一版可省） |
| **Matrix/Element** | 联邦 + 房间事件 DAG | `/sync` 用 `since` token 做增量同步；"limited timeline + gap" 诚实表达数据缺口 | 联邦、DAG、E2EE、homeserver 分层 |
| **Revolt / Stoat** | Rust + MongoDB + Redis | 事件 fanout 与"客户端只认事件流"的干净分层 | MongoDB/Redis 两套基础设施 |

三条可直接吸收的经验：

- **Mattermost 的搜索史就是我们该避开的坑**：默认数据库全文检索 → 实验性 Bleve（Lucene 风格）→ **v11 起 Bleve 被废弃**，推荐 Elasticsearch/OpenSearch（见参考资料）。一个 Go 项目宁愿维护两套外部搜索引擎也不要内嵌 Bleve，说明**嵌入式检索引擎的可运维性被高估了**。
- **中文检索在成熟 IM 里是"事故级"难题**：Mattermost 直到 v11.5 才用特性开关 `MM_FEATUREFLAGS_CJKSEARCH` 打开 CJK 搜索，且不同数据库要求不同配置（MySQL 要 ngram，Postgres 要装 SCWS 词典 + zhparser 扩展并重建索引）。**结论：中文检索不是"建个 FTS 表"就完事，它是 v0.0.1 里唯一需要专门设计 + 专门验收的技术点。**
- **Zulip 的 `last_event_id` 语义值得原样抄**：客户端上报"我已收到的最高事件 id"作为确认。这把"投递"降级成"游标推进"，天然支持多端、断线、重启。

## 2. (a) 最小领域模型

### 2.1 设计原则

- **人 与 Agent 是同一类主体（principal）**，只是 `kind` 不同。否则后面每一处"成员管理/提及/已读"都要写两遍。
- **消息表是唯一事实来源（source of truth）**，`event` 表是派生的 outbox，可重建。第一版**不要做事件溯源**——事件溯源在本地单机上换来的是复杂度，不是审计（审计用一张 append-only 表就够）。
- **"线程"用字段不用表**：两级（`reply_to_id` / `thread_root_id`）足以支撑"任务讨论→返工"的场景，第三级是评论区，不是对话平台。
- **结构化内容是"消息的一种 kind"，不是新表**：Agent 会产出卡片、工具调用、产物链接，用 `kind + body_json` 承载，`body_text` 只放人可读、可检索的投影。

### 2.2 表清单（设计级，非最终 DDL）

```
principal        id, kind(human|agent), handle, display_name, avatar_ref, status, created_at
agent_profile    principal_id, role(assistant|team_lead|member), team_id, model_ref,
                 capabilities_json, supervisor_id, session_ref          -- session_ref 指向框架侧记忆
conversation     id, kind(dm|channel|thread), name, topic, team_id, created_by, created_at, archived_at
conv_member      conversation_id, principal_id, role, joined_at, last_read_seq, muted
message          id, conversation_id, seq, sender_id, kind(text|card|tool|system),
                 body_text, body_json, reply_to_id, thread_root_id,
                 client_msg_id, created_at, edited_at, deleted_at
message_mention  message_id, principal_id, kind(mention|reply|assign)   -- 写入时算好，别查询时解析
attachment       id, message_id, sha256, filename, mime, bytes, w, h, alt
event            seq, type, actor_id, conversation_id, message_id, payload_json, created_at
agent_cursor     principal_id, conversation_id(NULL=全部), last_seq, updated_at
agent_inbox      principal_id, message_id, received_at, processed_at, attempt   -- 去重 + 重投
audit_log        id, at, actor_id, action, target, detail_json
meta             schema_version, fts_version, instance_id
```

要点：

- `seq`：**单写者生成的单调整数**（每 workspace 一条），不做时间戳排序。时间戳只用于展示和"按天分桶"。
- `client_msg_id`：客户端/Agent 生成，唯一索引，**这是幂等发送的唯一手段**——at-least-once 的另一半在这里。
- `thread_root_id`：根消息的 id；根消息自身为 NULL。查询线程 = `WHERE thread_root_id=? OR id=?`。
- `last_read_seq` 放在 `conv_member` 上：**不做逐条已读回执**。逐条回执在单机上是纯浪费（写放大 + 表爆炸）。
- `message_mention` 在写路径算好：将来"@我""分配给我"是高频查询，靠 `LIKE '%@x%'` 会死。
- `event` 表带独立 `seq`（可与 message.seq 共用一条序列），是 UI 与 Agent 的统一事件流；**删除事件、编辑事件也走这里**，客户端才能增量修正本地状态。
- 明确**不落库**的东西：输入中（typing）、在线状态（presence）、光标位置、未提交草稿（v0.0.1）。这些是"界面状态"，落库只会让"消息全量落库"这句话失去重点。

### 2.3 被忽略但会咬人的细节

- **Agent 的中间过程不该进主检索**。一次任务可能产生上百条 tool 调用记录，若全量进 FTS，人类搜"部署"会搜出 Agent 的 shell 输出。建议：`agent_trace` 独立表 + 默认不进 FTS，只在"显示详情"时按 message_id 拉取。这是"消息全量落库"与"检索质量"之间必须做的分离。
- **可见性（visibility）从第一天就要有**：v0.0.1 只有 owner 一个人类，但 Agent 之间有"团队隔离"。给 `conversation` 加 `visibility`（private/team/public），检索时**先过滤再排序**，避免"结果条数"泄漏不该看的内容。
- **附件内容寻址**：`sha256` 做文件名，天然去重（同一张图被多个 Agent 引用只存一份），且便于将来做 GC（孤儿扫描）。

## 3. (b) 存储与检索链路

### 3.1 存储选型：SQLite 是答案，不是妥协

- **单进程 + 单文件 + WAL**：读不阻塞写、写不阻塞读。注意官方同时指出 **WAL 要求所有进程在同一台机器上，不能走网络文件系统**——这恰好把"本地优先"钉死成一条硬约束，也是我们**拒绝把 data 目录放 NAS/OneDrive 同步盘**的技术理由（必须写进文档）。
- **驱动：`modernc.org/sqlite`（纯 Go）**，理由不是性能而是**交付形态**：单二进制、无 CGO、交叉编译零配置。代价可量化：在 `go-sqlite-bench` 里 modernc 插 100 万行约 2419ms vs mattn 1480ms；"大字段读"场景 modernc 401/629/1094ms vs mattn 122/207/376ms（约 2.5–3×）；"贴近真实"场景插入 1759 vs 1586、查询 130 vs 104。**对一个每天几千条消息的本地应用，这个差距无感；对"用户双击就跑"的 Windows 安装包，CGO 的差距是致命的。**
- 若将来必须上 CGO（例如要加载 C 版分词扩展），备选是 `github.com/ncruces/go-sqlite3`（WASM 版 SQLite，仍纯 Go）。
- 连接策略：**一个写连接（单 goroutine 串行提交）+ 一池读连接**。`PRAGMA journal_mode=WAL; synchronous=NORMAL; busy_timeout=5000; foreign_keys=ON`。所有写在应用层排队，从根上消灭 `SQLITE_BUSY`（官方也承认 WAL 下仍可能偶发 BUSY，见 WAL 文档第 9 节）。

### 3.2 索引形态：这是全文的核心决策

FTS5 的四种 tokenizer 决定了中文能不能搜：

- **unicode61（默认）**：把"连续的 L*/N* 类字符"当作一个 token。中文没有空格 → **一整句话是一个 token**。查"部署"永远匹配不到"我们下周部署第一版"。*这是最常见的踩坑点，Mattermost 的 CJK 难题是同一根源。*
- **trigram**：三个连续字符一个 token，支持真正的子串匹配，还顺带支持有索引的 `LIKE`/`GLOB`。**硬限制：MATCH 查询里少于 3 个字符的片段匹配不到任何行**；`LIKE '%xx%'` 会退化成全表扫描（官方原文："Substrings consisting of fewer than 3 unicode characters do not match any rows"）。
- **porter/ascii**：中文场景无用。
- **自定义 tokenizer**：需要 C 扩展（如 `wangfenjin/simple`，bigram + 拼音 + cppjieba）——**一旦用它就告别纯 Go 与单二进制**。

**中文的两个现实矛盾**：trigram 天然支持任意子串（对聊天检索体验最好），但 2 字词（"测试""部署""返工"——中文里最高频的长度）会失效；分词（jieba/gse）支持 2 字词，但依赖词典、对 OOV（人名、代码标识符、中英混排）不友好，且**查询必须用同一套分词器切**，否则索引和查询永远对不上。

**我的建议（v0.0.1）**：**trigram 为主 + 2 字以下回退 `LIKE` 扫描 + 语料上限保护**。
理由：聊天语料天然短（单条中位数 < 50 字），本地库规模可控；trigram 不需要任何词典、不需要 CGO、且顺带把"代码片段、URL、英文标识符的部分匹配"一起解决了（这对 Agent 协作场景是加分项：搜 `platform-sea` 能搜到 `platform-search.md`）。2 字查询走 `LIKE`，在 10 万条消息上的实测成本需要基准（建议 M0 就测）。如果基准不可接受，**升级路径是加一列 `body_seg`（应用层用 `gse` 分词后空格拼接）再建第二个 FTS 表**，属于可加的优化，不是返工。

**索引表形态：用"自包含 FTS5 表"（`CREATE VIRTUAL TABLE message_fts USING fts5(body, headers, tokenize='trigram')`），不用 external content。**
- external content + trigger 是官方推荐做法，但有明确的 pitfall：索引与内容表一旦不一致，带 MATCH 和不带 MATCH 的查询会返回互相矛盾的结果（FTS5 文档 4.4.4 节），而删除必须用 `INSERT INTO fts(fts, rowid, ...) VALUES('delete', ...)` 的特殊语法并携带旧值。
- 自包含表代价只是**正文多存一份**（聊天文本量级可接受，≈1.3×），换来：`DELETE by rowid` 直白、`snippet()`/`highlight()` 可用、迁移/重建简单。**在单机场景，"少一个能错的地方"比"省 30% 磁盘"值钱得多。**

### 3.3 写路径（一个事务，四件事）

```
BEGIN IMMEDIATE
  seq  = next_seq(meta)                        -- 单写者，无并发热点
  INSERT message(...)                          -- client_msg_id UNIQUE 提供幂等
  INSERT message_fts(rowid=message.id, body, headers)
  INSERT message_mention(...) / INSERT attachment(...)
  INSERT event(seq, 'message.created', ...)    -- outbox：投递的唯一依据
COMMIT
  -> 通知 in-process notify bus（SSE / 长轮询被唤醒）
```

- **通知放在 commit 之后**：先提交再广播，否则订阅者可能读到"还不存在"的消息（这是用事务边界解决"推送早于可见"的经典顺序问题）。
- 编辑/删除：更新 `message` + `message_fts`（`DELETE`+`INSERT`）+ `event('message.updated'|'deleted')`，全部同事务。**FTS 与主表同事务是 SQLite 最大的红利**，用它就别再引入异步索引。
- 若坚持异步索引（不推荐），必须有 `index_lag` 指标 + 对账任务，否则"刚发的消息搜不到"会被当成 bug 反复报。

### 3.4 检索链路

```
query string
  -> 规范化（NFKC、全半角、大小写、去零宽字符）
  -> 语言判定：含 CJK -> trigram 路径；否则同一条路径（trigram 对英文同样有效）
  -> 长度判定：有效片段 < 3 字符 -> LIKE fallback（严格 LIMIT，加超时）
  -> FTS5 MATCH（转义、引号包裹、多词 AND）
  -> JOIN message + conversation，先应用可见性/时间/发送者/会话过滤
  -> 排序：bm25 加权 + 时间衰减
  -> 截断 + snippet() + 命中的会话聚合
```

- **bm25 的两条硬知识**：FTS5 的 `bm25()` 越小越相关（内部乘了 -1），`ORDER BY bm25(message_fts)` 直接可用；k1=1.2、b=0.75 是硬编码，**唯一能调的是列权重**：`bm25(message_fts, 1.0, 3.0)`。所以排序质量靠"列权重 + 时间衰减 + 会话聚合"三件事，而不是调参。
- **列权重建议**：`body`=1.0，`headers`（发送者名/会话名/标签）≈2.5–3.0 —— 搜"测试组"时用户多半想找那个会话，而不是正文里随口提到的"测试组"。
- **时间衰减**：`score = bm25 + λ·(now - created_at)` 或简单的"近 30 天加权 1.2×"。**不要**用纯时间排序（会毁掉相关性），也**不要**只用 bm25（三天前和三个月前的同一句话价值不同）。
- **会话聚合（conversation-aware ranking）**：聊天检索的"单位"是会话不是消息。取每条会话命中消息的 top-3 bm25 求和作为会话得分，先给"会话列表"，点进去再给消息。这是 Slack/Zulip 类产品的实际体验，也是我们说"高效检索"时用户真正感知到的东西。
- **过度取数**：`LIMIT 200` 后再过滤到 20。权限过滤放在 SQL 里（不要事后过滤），否则分页会出现"这页只有 3 条"。

### 3.5 增量索引、重建与删除

- 增量：无——同事务写完就是最新的。这是选型红利，写进文档当作**验收标准**："发送后 <100ms 内可被自己搜到"。
- 重建：`meta.fts_version` 记录分词器配置指纹（如 `trigram/v1`）。升级导致索引不兼容时，启动检测到版本不符 → 后台 `INSERT INTO message_fts SELECT ... FROM message` 分批重建（每批 500 条，关同步），期间检索走"旧索引 + LIKE"或直接返回"索引重建中"状态。**这条能力要在 v0.0.1 就有，因为它同时是灾难恢复路径。**
- 删除：软删除（`deleted_at`）+ 物理清 FTS 行。注意"删除后仍能被搜到"是隐私事故，测试用例必须覆盖；`event('message.deleted')` 要让客户端真删而不是显示占位。
- 备份：单文件 + WAL，**不能直接 `cp`**。用 `VACUUM INTO 'backup.db'` 或 SQLite Online Backup API，退出时 `wal_checkpoint(TRUNCATE)`。

### 3.6 检索质量评估（必须有，且很便宜）

- 建一个 `eval/search_golden.jsonl`：40–60 条查询（覆盖：中文 2 字词、中文短语、中英混排、代码标识符、人名、时间/发送者过滤组合、空结果预期），每条标注理想结果的消息 id 列表。
- 指标：`Recall@10`、`nDCG@10`、`p95 延迟`、`空结果率`、`首条即命中率`。
- 每次改分词/tokenizer/权重都跑一遍。**没有这个，讨论"检索好不好"只能靠感觉**；有了它，trigram vs 分词的分歧可以用数据在一天内关闭。
- 工具无所谓（一个 Go test + NDCG 手算即可），不要引入 Python 评测栈。

## 4. (c) 实时通信与投递语义

### 4.1 传输选型：SSE（UI）+ 游标长轮询（Agent），不引入 WebSocket

| 方案 | 优 | 劣 | 结论 |
|---|---|---|---|
| WebSocket | 双向、低延迟、生态成熟（`coder/websocket`：零依赖、context 优先） | 需自建重连/心跳/背压；代理不友好；标签页多时连接数增长；**没有免费的断点续传** | v0.0.1 不用 |
| SSE | `EventSource` 内建自动重连 + `Last-Event-ID` 续传；纯 HTTP、可 curl 调试、Go 只需 `http.ResponseController` 刷写；服务端无状态 | 单向；HTTP/1.1 每域名 6 连接；`EventSource` 不能自定义 Header | **UI 首选** |
| 长轮询 | 最笨也最稳；任何 HTTP 客户端都能用；天然对应"游标" | 每次都要重建请求；并发连接占用 | **Agent 首选** |
| 内存广播 | 瞬时、简单 | 重启即丢、不能补发 | 只做"唤醒信号"，不做数据通道 |

**关键判断：本地部署下延迟差距是毫秒级，WebSocket 换来的"双向"我们并不需要（客户端上行用普通 POST 更简单、更好重试、更好审计）。真正需要的是"可重放"，而 SSE 的 `Last-Event-ID` 是唯一免费送这个能力的浏览器 API。**

两个必须写进设计的细节：
1. **回放来自数据库，不来自内存环形缓冲。** 客户端带 `Last-Event-ID: <seq>` 重连，服务端 `SELECT ... FROM event WHERE seq > ?` 补齐，再切到实时流。这样"断线 3 天"和"断线 3 秒"是同一条代码路径，也顺带解决进程重启。
2. **HTTP/1.1 的 6 连接上限**：明文 localhost 没有 h2，多标签页会打满。约束：**每个页面最多一条 SSE 流**，所有事件类型（消息、输入中、任务状态）复用这一条，前端用 `eventType` 分发。

### 4.2 投递语义（这是最容易写错的地方）

- **at-least-once，绝不承诺 exactly-once。** 承诺后者需要在消费端做分布式事务，而我们是单机——用"游标 + 幂等"两件便宜的工具就能得到"效果上恰好一次"。
- 每条消息有全局 `seq`；消费者（Agent）持 `agent_cursor.last_seq`；投递 = `seq > cursor` 的消息。
- 消费流程：`拉取 -> 处理 -> 在同一事务里推进 cursor + 写 agent_inbox.processed_at + 写副作用`。崩溃后重放 → 可能重复处理 → 靠 `(principal_id, message_id)` 唯一键 + 副作用幂等（发消息用 `client_msg_id`）保证安全。
- **不要用"推送成功"当作送达**：推送只是唤醒，真相在 `seq`。
- 多端/多 Agent 并发消费同一会话：各自独立游标，**不要会话级广播确认**（一个慢 Agent 会拖住所有人）。
- 事件流里要有 `event.seq` 与 `message.seq` 的对应关系（或者共用一条序列），否则客户端无法把"编辑/删除"精确地应用到本地视图。

## 5. (d) 本地部署与安全

### 5.1 部署形态

- **默认只绑 `127.0.0.1`**，随机端口 + 启动时打印 URL + 可选自动开浏览器。绝不默认 `0.0.0.0`。
- **单用户**（v0.0.1）：首次启动创建 owner 账号（本地用户名 + 密码，或干脆"本机即身份"）。**不做注册、不做邮箱验证、不做 OAuth/SSO。**
- 数据目录走平台惯例：`os.UserConfigDir()` / `os.UserCacheDir()`（Windows `%APPDATA%`、macOS `~/Library/Application Support`、Linux `~/.config`）。**数据目录必须支持 `--data-dir` 覆盖**，否则用户没法放到 D 盘或备份盘。
- 远程访问：**不自建 TLS + 公网暴露**，文档里直接推荐 SSH 端口转发或 Tailscale 之类的覆盖网络。这一条同时省掉证书管理、端口转发、公网鉴权三摊事。
- 局域网共享（多用户）作为**明确的非目标**写进 `requirements.md`，而不是"也许以后"。

### 5.2 威胁模型：localhost 不是安全边界

这是本地 Web 应用最常被忽略的一条：**任意网页都能向 `http://127.0.0.1:<port>` 发请求**（CSRF / DNS rebinding）。必做三件事：

1. 校验 `Origin` / `Host`（只接受配置的端口与 localhost 变体）；
2. Cookie 用 `SameSite=Strict` + `HttpOnly`；写操作要求自定义 Header（如 `X-Lignting-Client`），迫使跨站请求走 preflight；
3. 明文 HTTP 下 `Secure` 不可用——不要假装有 TLS，改为依赖"仅本机可达 + Origin 校验"两层。参考浏览器侧对本地网络的收紧趋势（PNA/Local Network Access），**越早按"跨站不可信"设计越省事**。

### 5.3 Agent 的凭证与权限

- **每个 Agent 一个独立 token（本地生成，哈希存库）**，不用人类会话。撤销 = 删一行。
- 模型 API Key 不是 Agent 的凭证，是**平台的配置**：放 `secrets.json`（0600）或 OS keychain（Go 可用 `zalando/go-keyring`），**绝不写进消息、绝不进检索、日志脱敏**。
- 能力声明（`capabilities_json`）要有实际约束力：
  - 会话级：能读哪些 conversation（默认只读自己所在团队 + 被 @ 的）；
  - 工具级：`fs.read` 的根目录白名单、`fs.write` 白名单、`shell` 是否允许、网络域名白名单；
  - 审批门：破坏性操作（删文件、force push、对外发消息）需要人类确认 → 以 `kind=card` 消息形式发到会话里，等人类点击，结果写 `event`。
- 审计：`audit_log` 记录"谁（人类还是哪个 Agent）在什么时候对什么做了什么"，至少覆盖：登录、创建/删除会话、发消息、工具调用、配置变更、密钥变更、索引重建。**审计日志不可 UI 删除**，只能整体导出。

### 5.4 数据保护

- 附件：MIME 白名单 + 大小上限 + 存盘用 sha256 路径（防路径穿越）+ 下载走 `Content-Disposition: attachment` + 独立的 `X-Content-Type-Options: nosniff`。
- 备份加密与"密钥在 keychain、数据在磁盘"的矛盾要写明：**v0.0.1 不做全库加密**（SQLCipher 会引入 CGO/新依赖），改为"数据目录权限 0700 + 明确告知用户磁盘未加密"。这是取舍，不是遗漏。

## 6. (e) 与 Go Agent 框架的进程边界

三种形态：

| 形态 | 优 | 劣 | 适用 |
|---|---|---|---|
| **同进程**（一个二进制，goroutine 跑 Agent runtime） | 零序列化、共享内存、调试最简单、部署最简 | 一个 panic/内存泄漏拖垮全平台；Agent 的 shell/文件工具与平台同权限；不能单独升级/重启 Agent；测试要起整机 | v0.0.1 的内置助理 |
| **子进程**（平台 spawn `lts-agent`，stdio + JSON-RPC） | 崩溃隔离、每 Agent 独立 env/secret、语言无关、流式天然（MCP stdio 就是这个形态：客户端启动 server 子进程，换行分隔 JSON-RPC） | 生命周期管理/僵尸进程/Windows 控制台窗口/日志归集/背压都要自己写 | 未来的"重量级 Agent"、需要独立工具链时 |
| **HTTP+流客户端**（Agent 作为普通客户端连平台） | 与人类客户端同一套契约；进程随意；重启不影响平台；天然多机；**强制把"投递语义"做对**（因为只能靠游标） | 多一跳鉴权；不能共享内存；需要 token 管理 | **对外契约的唯一形态** |

**推荐：契约统一为 HTTP + 流，实现上"同进程快路径"。**

- 定义一个 `PlatformClient` 接口（`Send` / `Stream(afterSeq)` / `Search` / `Ack`），提供两个实现：
  - `inproc`：直接调平台内部服务（内置助理、单测、benchmark 用）；
  - `http`：走 `/api/v1`（外部 Agent、跨进程、将来的多机用）。
- **只有 `http` 实现在 v0.0.1 就要完整可用**，`inproc` 只是它的加速版。这样"进程边界"从第一天就是**可选的**，而不是将来要重写的一次大手术。
- 反面模式（明确写进文档禁止）：Agent 进程直接打开 `data.db`。这会让 WAL 的"单写者"假设失效、绕过审计、让 schema 迁移变成多方协同。
- 记忆的归属要说清：**平台只存消息与事件；DSH 式 session 记忆、四层记忆、档案都在 Agent 框架侧**（文件/自有存储），平台通过"检索 API"作为记忆的一个召回源。两个进程各自拥有自己的数据，边界才稳。

### 6.1 内嵌 Web UI

- `go:embed` 把前端 `dist/` 打进二进制，`/` 走 SPA fallback（找不到静态文件就回 `index.html`），`/api/v1/*` 走 API。构建期依赖 Node，运行期零依赖——这是"单二进制"的完整含义。
- 开发期：Vite dev server 反代 `/api` 到 Go 进程（`--dev` 开关），避免把前端构建塞进 `go run`。
- 版本一致性：把 `git describe` / 构建时间注入 `meta`，前端 `/api/v1/meta` 校验，避免"前端缓存了旧包连新后端"。

## 7. (f) v0.0.1 推荐选型

| 决策点 | 选择 | 一句话理由 |
|---|---|---|
| 语言/依赖 | Go 1.2x + **标准库 `net/http`**（Go 1.22 路由模式）+ 极少量依赖 | 路由已够用，框架是负债 |
| 存储 | **SQLite（WAL）+ `modernc.org/sqlite`** | 单文件、纯 Go、可交叉编译 |
| 连接模型 | 1 写连接（串行）+ N 读连接 | 从根上消灭 `SQLITE_BUSY` |
| 全文检索 | **FTS5 + `tokenize='trigram'`** + `<3 字符` LIKE 回退 | 免词典、免 CGO、支持子串与中英混排 |
| 索引形态 | **自包含 FTS5 表**（非 external content） | 少一个能出错的地方；`snippet()` 可用 |
| 排序 | `bm25(col weights)` + 时间衰减 + 会话聚合 | 三个旋钮都在应用层，可评测 |
| 实时（UI） | **SSE + `Last-Event-ID` 回放** | 免费重连续传，无状态 |
| 实时（Agent） | **游标长轮询 `/messages?after_seq=`** | 任何 HTTP 客户端可用，可重放 |
| 投递语义 | **at-least-once + `client_msg_id` 幂等 + `agent_cursor`** | 不承诺 exactly-once，但效果等价 |
| 前端 | 内嵌 SPA（Vite 构建，`go:embed`） | 单二进制 |
| 认证 | 本地 owner + 会话 Cookie + Agent Bearer Token | 不引入 OAuth |
| 进程边界 | 统一 HTTP 契约，内置助理走 `inproc` 快路径 | 边界可选，不返工 |
| 文件 | 内容寻址（sha256） | 去重 + 防穿越 + 好备份 |
| 备份 | `VACUUM INTO` + 退出时 checkpoint | 单文件不等于能 `cp` |

**v0.0.1 必须达成的验收线（可直接写进 requirements）**：

1. 单条消息写入 → 可检索 < 100ms（同进程内）；
2. 断线 1 小时后带 `Last-Event-ID` 重连，**零丢失、零重复**补齐；
3. 中文 2 字/3 字/短语/中英混排各 10 条查询，`Recall@10 >= 0.8`；
4. 10 万条消息语料下，检索 p95 < 150ms，冷启动 < 2s；
5. 删除的消息不可被任何检索路径命中；
6. 单二进制在 Windows/macOS/Linux 三平台无 CGO 构建通过。

### 7.1 "不要做"清单（v0.0.1 明确不做）

**检索相关**
- ❌ 不引入 Bleve——Mattermost 都用废弃它换 Elasticsearch 了；BoltDB 系存储的独占文件锁还会挡住"平台 + CLI 同时访问"。
- ❌ 不引入 Meilisearch/Typesense——都是**独立进程/独立端口**的服务，破坏单二进制与"双击即用"；且中文质量依赖它们的内部 tokenizer，我们无法控制。
- ❌ 不引入向量检索（`sqlite-vec` / `pgvector`）——`sqlite-vec` 自己标注 pre-v1、会有 breaking change；语义检索是 v0.1 之后配合记忆层的事。
- ❌ 不引入 PostgreSQL/Elasticsearch/OpenSearch——单用户本地库不需要。
- ❌ 不自研 C 版 FTS5 分词扩展（`wangfenjin/simple` 这类）——引入 CGO，摧毁单二进制交付。
- ❌ 不做同义词/拼音/纠错/多语言——先把中文子串搜对。

**架构相关**
- ❌ 不做 Redis / 消息队列 / 独立 broker——单进程内 notify 足够。
- ❌ 不做微服务拆分、不做多租户、不做联邦（Matrix 式）、不做 E2EE。
- ❌ 不做事件溯源（event 表是派生 outbox，不是事实来源）。
- ❌ 不允许任何进程绕过平台直连 SQLite 写。
- ❌ 不做集群/多实例/负载均衡。

**产品相关**
- ❌ 不做逐条已读回执、不做 typing/presence 落库。
- ❌ 不做三级以上线程、不做频道嵌套、不做富文本编辑器（Markdown 足够）。
- ❌ 不做移动端、不做桌面端壳（浏览器即客户端）。
- ❌ 不做 OAuth/SSO/邮箱注册、不做公网 TLS 部署方案。
- ❌ 不做插件系统/机器人市场。
- ❌ 不做全库加密（SQLCipher）——写明"磁盘未加密"的取舍即可。

## 8. 对两份文档的具体建议

### 8.1 `doc/target.md`（最终形态）
- 用一节讲**"消息是唯一事实源 + seq 全序"**，把它上升为不变量（invariant），因为后面所有能力（检索、重放、审计、多端）都从它派生。
- 明确**三类数据的所有权边界**：平台（消息/事件/审计）、Agent 框架（session 记忆/档案/四层记忆）、模型供应商（推理）。图画出来，比文字清楚。
- 终态可以包含：多 Agent 并发消费、跨团队辩论事件流、检索 API 成为记忆召回源、向量检索作为可选召回通道（**标注为 v0.1+**）。
- 写一句"本地优先的硬约束"：数据文件不得位于网络/同步盘；平台要么单机跑，要么通过覆盖网络访问（WAL 官方限制）。

### 8.2 `doc/0.0.1 最初版/requirements.md`（不写实现）
按"能力 + 验收"写，不写 SQLite/FTS5 这些实现名词：
- 需求条目应该是："用户可以按关键词检索历史消息，支持中文 2 字及以上的片段匹配，10 万条消息下 95 分位响应 < 150ms"——**而不是**"使用 FTS5 trigram"。
- 需要一条**明确的非功能需求**："消息写入后 100ms 内可被检索"（这是"同事务索引"的产品化表达）。
- 需要一条**投递语义需求**："客户端断线重连后不丢消息、不重复显示"，以及"Agent 处理消息具备至少一次投递保证，重复消息不会产生重复副作用"。
- 需要一条**安全需求**："平台默认仅本机可访问；跨站请求被拒绝；Agent 工具调用全部留痕"。
- 建议加一节"非目标"（直接抄 7.1 的产品类条目），**这比多写十条功能更能约束范围**。

## 9. 仍需决策的问题（给 Lead / 架构讨论用）

1. **`seq` 的粒度**：全局单序列（简单、跨会话可统一排序/重放）vs 每会话序列（客户端只需关心本会话、但跨会话聚合事件流要二次排序）。我倾向全局单序列——本地写入量小，没必要优化。
2. **Agent 的"读"是否要计入已读/是否要有未读概念**：人类看未读是刚需；Agent 需要的是"游标"而不是"未读红点"。两套语义混在一张表上会互相污染，需要明确。
3. **内置助理是"同进程 goroutine"还是"同二进制里的子进程"**：前者快，后者能在助理崩溃时不拖垮平台。第一版可以先同进程，但要不要**一开始就上子进程**把隔离做死，需要拍板。
4. **"消息全量落库"的边界**：Agent 的 tool 调用/思考过程（trace）算不算"消息"？我建议算"事件"不算"消息"、且默认不进检索索引——但这会直接影响"可审计"的承诺强度，需要产品口径。
5. **中文 2 字词回退策略的成本**：`LIKE` 扫描在 10 万条语料上是否可接受，需要 M0 实测数据来定；若不可接受，就要提前接受"分词列 + 双索引"的复杂度。

## 10. 参考来源（可核查）

- SQLite FTS5 官方文档（tokenizer / trigram 限制 / bm25 / external content pitfall）：<https://sqlite.org/fts5.html>
- SQLite WAL（读写并发、同机限制、SQLITE_BUSY）：<https://sqlite.org/wal.html>
- SQLite Online Backup：<https://sqlite.org/backup.html>
- Go SQLite 驱动横评（modernc vs mattn 等，含具体耗时）：<https://pkg.go.dev/github.com/cvilsmeier/go-sqlite-bench>
- `modernc.org/sqlite`：<https://pkg.go.dev/modernc.org/sqlite>；仓库 <https://gitlab.com/cznic/sqlite>
- `coder/websocket`（前 nhooyr.io/websocket）：<https://github.com/coder/websocket>
- Go `embed`：<https://pkg.go.dev/embed>；`http.ResponseController`：<https://pkg.go.dev/net/http#ResponseController>
- MDN Server-Sent Events：<https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events>
- MCP 传输规范（stdio 子进程 / Streamable HTTP）：<https://modelcontextprotocol.io/specification/2025-06-18/basic/transports>
- Zulip 事件队列 + `last_event_id` 确认：<https://zulip.com/api/get-events>；narrow（窄化检索模型）：<https://zulip.com/api/construct-narrow>
- Matrix Client-Server `/sync`（`since` token、limited timeline）：<https://spec.matrix.org/latest/client-server-api/#syncing>
- Mattermost：Bleve 在 v11 废弃 <https://raw.githubusercontent.com/mattermost/mattermost/master/docs/main/administration-guide/configure/bleve-search.mdx>；CJK 搜索特性开关与各库配置 <https://raw.githubusercontent.com/mattermost/mattermost/master/docs/main/administration-guide/configure/enabling-chinese-japanese-korean-search.mdx>
- Revolt / Stoat 后端（Rust + MongoDB + Redis）：<https://github.com/revoltchat/backend>
- `wangfenjin/simple`（SQLite FTS5 中文/拼音扩展，bigram + cppjieba，需 CGO）：<https://github.com/wangfenjin/simple>
- `sqlite-vec`（pre-v1，有 breaking change）：<https://github.com/asg017/sqlite-vec>
- Transactional Outbox 模式：<https://microservices.io/patterns/data/transactional-outbox.html>
- Local-first 理念（数据主权与协作的张力）：<https://www.inkandswitch.com/local-first/>
- Private Network Access（本地 Web 服务的跨站访问收紧趋势）：<https://wicg.github.io/private-network-access/>

---
*备注：本备忘录只做选型论证与设计含义提炼，不含实现代码与最终 schema；具体 DDL 与接口定义留待 `doc/target.md` 与设计文档阶段。*

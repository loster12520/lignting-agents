# Lignting Agents 0.0.1 分轮次分模块实施方案

> 文档版本：v1.0  
> 日期：2026-11-17  
> 状态：执行中（Lead 维护）  
> 上游：[requirements.md](requirements.md) · [target.md](../target.md)  
> 本文是**实施方案**，不是需求变更；需求冲突时以 requirements.md 为准。

---

## 0. 实施目标

把 v0.0.1 做成一个**单机、单用户、单二进制、可每天使用的纵向切片**：

> 用户启动 lignting → 浏览器工作台 → 助理接待 → G1 组建产品/研发/测试部门 → 一个真实需求从任务、部门内讨论、实现、评审、G3 验收走完 → 记忆/检索/审计可复盘。

完成标准不是「代码量」，而是 **15 个 AC 全部有可执行证据**。本方案把 108 条 FR 收敛为 10 个工作包和 9 个交付轮次，每轮都有可运行产物。

### 0.1 硬约束（不可在实现中漂移）

| 约束 | 实现要求 |
|---|---|
| 语言 | 平台 + 核心全部 Go；单二进制 |
| 模型 | 只接 DeepSeek 官方 API；默认 deepseek-flash；不做多供应商 |
| 工具 | Agent 只暴露 5 个：read_file、write_file、bash、web_search、view_change_history |
| 运行时 | 不使用 DSH/ACP/Node/MCP/外部 Agent 运行时 |
| 组织 | 用户 → 助理（或 PM 部门）→ 部门主管 → 员工；无统管所有部门的主管 |
| 写入 | 所有 Agent 共用 WriteTool；默认全局串行；每文件时间线默认 30 条可配 |
| 用户 | v0.0.1 只支持一个 owner；不做多用户 |
| 部署 | loopback、SQLite、无外部数据库/搜索服务；DeepSeek 是唯一默认外联 |
| 人类闸门 | G1/G2/G3 + 不可逆审批；Agent 不能自批、不能自改权限 |

### 0.2 已冻结的实现决策

1. **同进程双模块**：internal/platform（消息/会话/检索/审计投影）与 internal/core（Agent/任务/产出/评审/记忆/写工具）在同一个 Go 进程内，通过明确的 service 接口调用，不做 RPC。
2. **SQLite 用纯 Go 驱动** modernc.org/sqlite，避免 Windows cgo；WAL 模式；迁移内嵌。
3. **检索用自建短词倒排索引**：中文按 bigram、英文按小写词、代码标识符按原词 + 子词；不依赖 FTS5 分词，保证中文 2 字可命中。
4. **Agent 平台动作走结构化消息协议**：Agent 的回复中可带 lignting-action JSON 块；核心解析、鉴权、执行并投影为卡片。这样 5 个工具不变，同时让 Agent 能建任务/交产出/提评审。动作块不直接写库，必须过 core 的 Policy + 状态机。
5. **模型失败可降级**：无 Key / API 不可用时，平台启动、浏览、检索、导出可用；Agent turn 标记失败并可重试。测试用 model.mode=mock 或 mock provider，生产默认 deepseek。
6. **写锁默认全局串行**：write_file 提交写事件，未消费前其他写请求排队/拒绝；配置 write.parallel_by_file=false 可切换按文件并行（v0.0.1 只做全局串行的完整验收，按文件并行作为配置项保留）。
7. **前端零构建**：web/ 下原生 HTML/CSS/ES Module，go:embed 进二进制；不引入 Node 构建链。
8. **测试优先**：每个包必须有单元测试；核心链路有集成测试；15 AC 用 tests/ac 下的 Go 测试 + HTTP API 驱动，故障注入用测试开关。

---

## 1. 技术架构

### 1.1 进程与端口

- 单进程 lignting，启动参数：--data-dir、--addr、--config、--mock-model。
- 默认 127.0.0.1:8787，只绑 loopback。
- 数据目录结构：

~~~text
<data-dir>/
  lignting.db          SQLite（WAL）
  workspace/           用户工作区（read/write/bash 的根）
  artifacts/           产出物内容（按 hash 存储）
  backups/             备份 zip
  logs/                结构化日志
  config.json          非敏感配置（Key 单独加密/存放）
~~~

### 1.2 包结构

~~~text
cmd/lignting/main.go              启动、wire、信号、优雅退出
internal/config/                 配置、数据目录、Key 安全存储（DPAPI/0600）
internal/domain/                 共享类型、状态机、错误码、权限枚举（Lead 所有）
internal/store/                  SQLite 打开/迁移/仓储/事务/审计哈希链
internal/platform/               HTTP、SSE、静态资源、鉴权、会话/消息/通知 API
internal/search/                 短词索引、查询解析、排序、片段、重建
internal/deepseek/               Chat Completions 客户端（流式/工具/联网搜索）
internal/core/                   Orchestrator、Policy、路由、Agent loop、任务/产出/评审/决策/审批
internal/memory/                 四层记忆、候选、注入、快照、手册
internal/tools/                  5 个工具的实现与权限
internal/gitwatch/               分支/提交轮询、commit 组
internal/write/                  共享 WriteTool、锁、diff、时间线
web/                             index.html / app.js / style.css / 组件
tests/                           单元外的集成、AC、夹具
scripts/                         开发/验收脚本（含 go 包装）
doc/                             需求、方案、用户文档
~~~

### 1.3 依赖方向（只允许向下）

~~~text
cmd → platform, core, config, store
platform → store, domain, search
core → store, domain, deepseek, memory, tools, write, gitwatch
memory → store, domain, deepseek
tools → write, gitwatch, domain
write → store, domain
search → store, domain
store → domain
domain → 标准库
~~~

禁止：store 反向依赖 platform/core；platform 直接改任务表；tools 直接改消息表；web 直接访问数据库。

### 1.4 单二进制与静态资源

- go:embed web/* 到 platform 包。
- 前端路由用 hash 路由（#/chat/...），服务端只提供 index.html 与 /assets/*。
- SSE：GET /api/events?since=<seq>，事件为平台 events 表的增量；断线用 Last-Event-ID/since 补齐。
- 写操作统一要求 X-Lignting-CSRF（值由 /api/bootstrap 下发）并校验 Origin/Host。

---

## 2. 数据契约

> 完整 DDL 在 internal/store/schema.sql，由 Lead 冻结；改动必须走 migration 并通知所有 owner。

### 2.1 核心表（首版）

| 域 | 表 | 说明 |
|---|---|---|
| 配置 | settings, owner | 单用户、Key 引用、预算、端口等 |
| 组织 | departments, agents | 角色卡 JSON、状态、部门、视角、token_hash |
| 会话 | conversations, conversation_members | 类型/可见性/成员/到期（临时组/桥） |
| 消息 | messages, events | 消息全量落库；events 为 SSE/审计投影 |
| 检索 | search_docs, search_terms | 短词倒排；删除同步 |
| 任务 | tasks, task_criteria, task_dependencies, task_events | 状态机事件 append-only + hash chain |
| 产出 | artifacts, artifact_versions | 版本不可变，内容 hash |
| 评审 | reviews, review_verdicts | 挑战单/裁决 |
| 决策 | decisions | 不可变，supersede 链 |
| 审批 | approvals | G1/G2/G3/不可逆；TTL/双钥匙字段 |
| 记忆 | memories, memory_events, prompt_manuals, memory_reads | 四层/候选/手册/读取留痕 |
| 写入 | write_events, file_timeline, git_events, commit_groups | 写事件/时间线/Git |
| 审计 | audit_log | 唯一 append-only hash chain |
| 成本 | usage_ledger | token/费用归因 |
| 调度 | schedules | TTL/SLA/时间盒/周期 |
| 通知 | notifications | 收件箱/未读/打扰分级 |
| 运行 | turns, context_snapshots, bridges | turn/上下文快照/临时桥 |

### 2.2 关键字段与不变量

- messages.seq 全局单调递增；消息先落库再推送。
- task_events 每行 prev_hash/hash，按 task_id 串链；禁止 UPDATE/DELETE。
- artifact_versions 主键 (artifact_id, version)，content_hash 唯一；只插不改。
- write_events.status：pending | applied | rejected | unknown；base_hash 必填。
- file_timeline 每文件超过 write_timeline_limit（默认 30）淘汰最旧，按 created_at 保留。
- memories.visibility：agent-private | team | org | human-only；删除写墓碑 deleted_at 并移除注入。
- audit_log 只能 INSERT；hash = sha256(prev_hash + canonical(payload))。

### 2.3 Agent 结构化动作协议

Agent 回复中允许出现：

~~~lignting-action
{"action":"create_task","args":{"title":"...","acceptance":["..."],"department_id":"...","perspective":"接口","priority":"P1"}}
~~~

首版动作白名单：

| action | 允许角色 | 核心校验 |
|---|---|---|
| save_intent | 助理 | 仅 DM；写 intent 版本 |
| propose_blueprint | 助理 | 生成 G1 approval；不直接建部门 |
| provision_departments | 助理 | 必须有已批准 G1 |
| create_task | 助理/部门主管 | 有部门、DoR 字段、perspective |
| transition_task | owner/reviewer/approver | 状态机 + 角色 |
| submit_artifact | 员工/主管 | 任务存在、版本 hash |
| request_review | 任意受影响者 | 产出物版本、证据 |
| submit_verdict | 评审人 | 非作者；blocker 需证据 |
| create_decision | 助理/主管 | 决策人/异议/影响 |
| request_approval | 助理/主管 | 闸门类型/风险/证据 |
| submit_memory_candidate | 员工/主管/助理 | 额度 + 证据 + 敏感过滤 |

执行器要求：JSON 校验 → Policy 校验 → 事务内写库 + 审计 + 事件 → 从正文移除动作块，替换为卡片投影。非法动作整块拒绝并回消息说明，不能半执行。

---

## 3. 模块与工作包

| 工作包 | 内容 | 主 owner | 主要文件 |
|---|---|---|---|
| P1 存储与迁移 | SQLite 打开、DDL、仓储、事务、审计链、崩溃恢复 | platform-dev | internal/store/** |
| P2 HTTP/SSE/鉴权 | 路由、CSRF/Origin、Cookie、SSE、静态资源、错误模型 | platform-dev | internal/platform/** |
| P3 消息与群聊 | 会话/成员/消息/线程/提及/未读/观察者/桥 | platform-dev | internal/platform/chat*.go |
| P4 检索 | 倒排索引、查询解析、权限过滤、片段、重建 | platform-dev | internal/search/** |
| C1 DeepSeek 客户端 | Chat Completions、流式、函数工具、联网工具、重试/超时 | core-dev | internal/deepseek/** |
| C2 Agent 运行时 | mailbox、turn、上下文装配、动作协议、降级 | core-dev | internal/core/agent*.go |
| C3 组织与 Provisioning | 模板、部门、角色卡、幂等状态机、G1 | core-dev | internal/core/org*.go |
| C4 任务/产出/评审/审批 | 状态机、DoR/DoD、版本、裁决、G2/G3 | core-dev | internal/core/work*.go |
| C5 讨论/辩论/决策 | 5/15 上限、决策包、stall 检测、决策记录 | core-dev | internal/core/debate*.go |
| M1 四层记忆 | 候选/档案/手册/注入/快照/检查器 | core-dev + memory-dev | internal/memory/** |
| T1 工具层 | read/write/bash/web_search/view_history | tools-dev | internal/tools/** |
| T2 共享写工具 | 全局锁、base_hash、diff、时间线、Git | tools-dev | internal/write/**, internal/gitwatch/** |
| W1 工作台前端 | 聊天/组织/任务/产出/审批/记忆/搜索/审计 | web-dev | web/** |
| Q1 验收与文档 | 单元/集成/AC 夹具、性能、Quickstart/FAQ | qa-dev | tests/**, scripts/**, docs/** |

---

## 4. 轮次计划

> 每轮结束必须：go build ./... 通过、go test ./... 通过、有可演示入口、更新本文件「轮次状态」、Lead 验收 diff + 跑测试。轮次可在同一 goal round 内合并，也可拆成多轮。

### R0 环境、骨架、契约（Lead，当前轮）

**交付**
- Go 1.25 全局可用；git 仓库初始化；.gitignore。
- go.mod、cmd/lignting 最小可启动 HTTP 服务（/api/health、/）。
- internal/domain 共享类型 + 任务状态机 + 错误码。
- internal/store/schema.sql 冻结版 + migration runner 接口。
- internal/config 配置加载/保存。
- web/index.html 占位 + 静态资源嵌入。
- 本实施方案；共享任务板；Agent Team 组建。

**退出标准**
- go build ./...、go test ./... 通过。
- lignting --help 与启动后 /api/health 返回 200。
- 所有 owner 明确写作用域；任务板可领取。

### R1 平台底座：消息、持久化、检索、登录（platform-dev + web-dev）

**交付**
- 全部 schema/migration/仓储；audit_log hash chain；owner/session/CSRF/Origin。
- 会话/成员/消息/线程/未读/附件元数据 API；先写库后 SSE。
- SSE /api/events + 断线 since 补齐；消息编辑/删除墓碑。
- 中文 2 字/英文/代码检索（索引在写入事务内同步更新）、过滤、片段、重建。
- 前端外壳：登录/向导、左侧会话列表、消息流、输入框、搜索页。
- 单元 + 集成测试：消息重启不丢、SSE 补齐、中文 2 字搜索。

**退出标准**：M0「能说话」的持久化/检索/重启部分可演示；go test ./internal/store ./internal/platform ./internal/search 绿。

### R2 助理与 Agent 运行时（core-dev + web-dev）

**交付**
- DeepSeek 客户端：非流式/流式、函数工具、deepseek_web_search/deepseek_read_url、超时重试、usage 记账。
- Agent loop：turn 状态、mailbox、上下文装配（角色卡/手册/任务/近期消息/记忆）、5 工具、动作协议执行器。
- 助理：首启问候、澄清、意图卡、单次问答、边界提示、失败降级。
- 路由与隔离：部门内/跨部门/主管-用户/用户观察者；拒绝路径提示。
- model.mode=mock 确定性 provider + deepseek 真 provider。
- 前端：流式消息、Agent 状态、停止/重试。

**退出标准**：AC-01 可在 mock 与真实 Key 两种模式跑；无 Key 时平台可启动并解释。

### R3 组织与 Provisioning（core-dev + web-dev + qa-dev）

**交付**
- 部门模板（产品/研发/测试，可选平台/PM）、角色卡、分工视角、部门群聊/任务板/记忆空间。
- 蓝图 → G1 approval → provisioning 状态机（幂等键、重试、回滚/孤儿报告）。
- 部门 pause/resume/archive；Agent 生命周期接口；组织图。
- 前端：蓝图审批、组织图、角色卡、部门群聊。
- 测试：重复 provisioning 不重复、失败回滚、暂停不接新任务。

**退出标准**：AC-02 通过；M1「能组部门」可演示。

### R4 任务、产出、评审、人类闸门（core-dev + web-dev + qa-dev）

**交付**
- 任务状态机（含 discussing）、DoR/DoD、依赖/环检测、WIP、事件时间线 hash chain、验收核对表。
- 产出物注册表/不可变版本/hash/关联/评论；评审挑战单/裁决/返工/作者≠评审者。
- G2/G3 approval；G3 缺证据不可提交；审计链。
- 前端：任务板/详情/时间线/产出物/评审卡/审批中心。
- 测试：非法转换、作者自批拒绝、G3 缺证据拒绝、返工版本。

**退出标准**：AC-03（不含写工具）与 AC-12 主路径通过；M2 核心可演示。

### R5 共享写工具、时间线、Git（tools-dev + core-dev + web-dev）

**交付**
- write_file：写事件、全局锁、base_hash、冲突拒绝/排队、diff、审计。
- file_timeline 30 条可配；view_change_history 工具 + API/UI。
- Git 分支/提交轮询、commit 组、分支切换保护。
- read_file/bash 权限/路径/超时/输出裁剪；bash 绕过标记「不受管变更」。
- 并发测试：同文件只有一个成功、不同文件默认串行、35 次裁剪、分支/commit 组。

**退出标准**：AC-15 通过；M2 完整。

### R6 部门内讨论、跨部门辩论、决策（core-dev + web-dev + qa-dev）

**交付**
- 部门内讨论：perspective、每人≤5/总≤15、主管裁决、记录进任务线程。
- 跨部门挑战/决策包/轮次/时间盒/stall/升级 L0–L4；5/15；新证据重开最多 1 次。
- 决策记录不可变、异议保留、supersede、检索。
- 前端：讨论面板、辩论时间线、决策卡。
- 测试：超限拒绝、stall 检测、异议保留、AC-04。

**退出标准**：AC-04 通过；M3「能讨论」可演示。

### R7 四层记忆（core-dev + memory-dev + web-dev）

**交付**
- 短期/触发/档案/自适应提示词；候选→审核→档案；员工候选额度；敏感过滤。
- 注入预算/分区/context.request 快照/「用了哪些记忆」；记忆读取工具。
- 检查器：浏览/搜索/编辑/删除/钉住/待审队列/手册 diff+回滚。
- 触发：任务关闭/返工/裁决/决策/人类拍板；单飞/冷却/失败队列。
- 测试：AC-05/AC-11；删除后不再注入；跨部门越权拒绝。

**退出标准**：AC-05/AC-11 通过；M4「能记住」可演示。

### R8 安全、成本、备份、运维（platform-dev + core-dev + qa-dev）

**交付**
- 审计查询/导出；Key 加密存储/脱敏；工具白名单；紧急停止；预算 80%/100%。
- 备份/恢复/导出/导入；索引重建；定时/到期；崩溃恢复。
- 前端：审计/成本/设置/诊断。
- 测试：AC-07/08/09/10/13。

**退出标准**：安全基线 + 备份演练 + 预算/暂停通过。

### R9 15 AC 总验收与发布（qa-dev 主责，全员修缺陷）

**交付**
- tests/ac/AC01...AC15 可独立运行；夹具/seed；真实模型与 mock 双模式。
- 15 AC 报告（通过/失败/耗时/成本/证据）；性能基准（10 万消息）；Recall 金标准集。
- Quickstart/概念/FAQ/故障排查/演示脚本；发布检查清单。
- 修完 P0 缺陷；P1 清单与规避方案。

**退出标准**
- go test ./... 全绿；go test ./tests/ac -tags=acceptance 全绿（真实 Key 模式人工确认）。
- 单二进制可启动；数据重启恢复；无未记录审批。
- 文档齐全；发布检查清单勾选。

---

## 5. Agent Team 编制与协作

### 5.1 角色

| Agent | 职责 | 写作用域（只写这里） | 不写 |
|---|---|---|---|
| lead | 架构/契约/集成/最终验收 | cmd/**, internal/domain/**, internal/config/**, go.mod, go.sum, doc/0.0.1 最初版/implementation-plan.md | 各业务包内部实现 |
| platform-dev | 存储、HTTP、SSE、消息、检索 | internal/store/**, internal/platform/**, internal/search/** | core/**, tools/**, web/** |
| core-dev | DeepSeek、Agent loop、组织、任务、评审、辩论 | internal/core/**, internal/deepseek/** | store/**, web/**, tools/** |
| memory-dev | 四层记忆、注入、检查器后端 | internal/memory/** | core/** 业务状态机 |
| tools-dev | 5 工具、共享写、时间线、Git | internal/tools/**, internal/write/**, internal/gitwatch/** | store/**, core/** |
| web-dev | 工作台前端 | web/** | Go 业务包 |
| qa-dev | 测试/AC/文档/脚本 | tests/**, scripts/**, docs/** | 生产代码（发现缺陷开任务给 owner） |

### 5.2 协作规则

1. 每个任务必须写清：目标、验收、写作用域、依赖任务。写作用域重叠时先找 Lead 拆分。
2. 共享契约（domain、schema、HTTP 错误码、动作协议）由 Lead 冻结；需要改先发消息给 Lead，由 Lead 改。
3. 所有跨包调用先定义接口（在 domain 或本包 ports.go），再实现；不允许 store 里写业务规则。
4. 每完成一个包：go vet ./...、go test ./<pkg> 通过，提交前 gofmt。
5. 不直接改别人文件；需要别人改时开任务或发消息。Lead 负责最终 diff 审核与集成测试。
6. 所有用户可见行为必须落库 + 审计；不允许「只在前端状态里」。
7. 每个 PR/批次更新 doc/0.0.1 最初版/implementation-plan.md 的轮次状态表。

### 5.3 轮次状态表

| 轮次 | 状态 | owner | 备注 |
|---|---|---|---|
| R0 | 进行中 | lead | 环境/骨架/契约 |
| R1 | 待开始 | platform-dev | |
| R2 | 待开始 | core-dev | |
| R3 | 待开始 | core-dev | |
| R4 | 待开始 | core-dev | |
| R5 | 待开始 | tools-dev | |
| R6 | 待开始 | core-dev | |
| R7 | 待开始 | memory-dev | |
| R8 | 待开始 | platform-dev | |
| R9 | 待开始 | qa-dev | |

---

## 6. 测试与验收策略

### 6.1 测试金字塔

| 层 | 范围 | 工具 |
|---|---|---|
| 单元 | 状态机、hash chain、索引、diff、权限、预算 | go test |
| 集成 | store+HTTP+SSE+core 的临时数据目录 | httptest + t.TempDir() |
| 端到端 | 启动真实二进制，HTTP 驱动完整流程 | tests/e2e + os/exec |
| AC | 15 个场景，独立夹具 | tests/ac，-tags=acceptance |
| 性能 | 10 万消息检索、1 万条会话 | go test -bench |
| 安全 | Origin/CSRF/越权/注入/密钥脱敏 | 集成 + 手工清单 |

### 6.2 15 AC 对应轮次

| AC | 内容 | 主要轮次 |
|---|---|---|
| AC-01 | 首次启动与助理 | R1/R2 |
| AC-02 | 部门组建 | R3 |
| AC-03 | 需求交付闭环 | R4/R5 |
| AC-04 | 跨部门挑战与决策 | R6 |
| AC-05 | 记忆回忆与纠正 | R7 |
| AC-06 | 检索与审计 | R1/R8 |
| AC-07 | 崩溃、重启与断线 | R1/R8 |
| AC-08 | 安全与权限 | R2/R8 |
| AC-09 | 预算与暂停 | R8 |
| AC-10 | 备份、导出与恢复 | R8 |
| AC-11 | 记忆注入与跨会话复用 | R7 |
| AC-12 | 部门主管核对与人类抽检 | R4 |
| AC-13 | 注意力、收件箱与审批 | R3/R8 |
| AC-14 | 群聊与 Agent 管理 | R3 |
| AC-15 | 并发写入与变更时间线 | R5 |

### 6.3 Mock 与真实模型

- model.mode=deepseek：真实 DeepSeek；无 Key 时启动仍可用，Agent turn 返回可读错误。
- model.mode=mock：确定性回复 + 可脚本化工具调用，供 CI/AC 使用；UI 明确标注「测试模式」。
- AC 验收优先真实模型；自动化回归用 mock，故障/成本/越权用测试开关注入。

---

## 7. 风险与应对

| 风险 | 影响 | 应对 |
|---|---|---|
| Go 依赖（modernc sqlite）首次编译慢/Windows 兼容 | 阻塞 R0/R1 | R0 先做最小 import 编译验证；失败切 ncruces/go-sqlite3 |
| 无 DeepSeek Key | 无法跑真实 AC | mock provider 先通；Key 由用户设置页注入；AC 报告标注模式 |
| 5 个工具限制与平台动作冲突 | 功能不可用 | 动作协议走消息层，不算工具；核心鉴权 |
| 多 Agent 并发写代码冲突 | 集成失败 | 严格写作用域 + Lead 集成；每轮先拉齐契约 |
| 前端工作量过大 | 体验不达标 | 零构建、单页、优先闭环页面；P1 视觉后补 |
| 15 AC 规模大 | 发布延期 | 按 R1–R8 逐 AC 点亮；每 AC 有独立夹具 |
| 真实模型非确定性 | AC 不稳定 | 断言状态/审计/证据，不断言措辞；质量判断人工 |
| 上下文/成本 | 费用不可控 | 预算、截断、快照、按需注入；默认 8K 输出 |

---

## 8. 完成定义（v0.0.1）

- [ ] 15 个 AC 全部通过（真实模型 + 干净数据目录；故障注入有记录）。
- [ ] go build ./...、go test ./...、go vet ./... 全绿。
- [ ] 单二进制启动；重启后消息/任务/产出/评审/决策/记忆/时间线恢复。
- [ ] 中文 2 字检索、10 万条 p95 ≤300ms、写入 ≤100ms 可搜。
- [ ] 共享写工具串行/冲突/时间线/Git 提交组通过。
- [ ] 无未记录审批；审计 hash chain 可校验。
- [ ] Quickstart/概念/FAQ/故障排查/演示脚本齐全。
- [ ] 代码与文档提交到 git，工作区干净。

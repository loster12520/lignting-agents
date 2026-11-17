# 多 Agent 团队协作、辩论与治理模式研究备忘录

> 任务：task-3（多 Agent 团队协作、辩论与治理模式）｜作者：researcher-collab｜日期：2026-11-16
> 服务对象：doc/target.md 与 doc/0.0.1 最初版/requirements.md
> 方法：阅读官方文档、论文与工程博客（链接见第 6 节），做设计取向对比；未复现实验，结论用于需求与架构取舍，不作为性能承诺。
> 本地路径：D:\project\github\lignting-agents\.research\multi-agent-patterns.md

## 0. 结论摘要

1. 协作框架大致两派：SOP／流水线（MetaGPT、ChatDev）可预测、易审计但僵化；动态编排（AutoGen/AG2、CrewAI、LangGraph、OpenAI Agents SDK）灵活但易漂移、成本高。v0.0.1 应“固定 SOP 骨架 + 有限动态”。
2. 任务必须是独立对象（ID、状态、owner、验收标准、证据），不能是聊天消息的副产品。A2A 的 Task 生命周期可借鉴，但它缺“评审／返工”态，需在管理状态机中扩展。
3. 产出物是第一等公民。MetaGPT 用结构化中间产物降低级联幻觉；ChatDev 用 Git 与评审管理代码。本项目需要 artifact registry：不可变版本、内容哈希、来源、评审与决策关联。
4. 辩论有边界。Du 等证明多轮辩论提升事实性与推理；Khan 等证明非专家裁判可从辩论中受益；但 MT-Bench 与 sycophancy 研究表明裁判存在位置／冗长／自我偏好，Agent 也会迎合。必须“证据门槛 + 盲评 + 有限轮次 + 独立裁判 + 人类校准”。
5. 主管 Agent 的主要风险是系统设计，不是模型能力。MAST 的 14 种失效模式集中在规格、角色、协作与验证；Anthropic 多 Agent 系统 token 约为聊天 15 倍；Cognition 指出上下文不共享会造成隐性决策冲突。治理靠权限矩阵、独立验证、预算与升级，而非更长的提示词。
6. 串谋与虚假共识需要结构性防线：作者≠审批者、评审轮换、盲评、红队、人类抽检、审批率异常监控。提示词里写“请批判”不够。
7. v0.0.1 建议：人类主管 + 助理 + 2 支团队（产品、研发含测试），单项目单需求；9 个正常状态 + failed 异常终态的任务机、产物注册表、最多 2 轮的挑战—裁决协议、3 个人工闸门、决策记录、全量事件日志。不做自由群聊、动态组队、外部 A2A 互操作。
8. 协议分工要清楚：MCP 接工具与知识，A2A 做 Agent 间互操作（未来），AG-UI 做人机事件流；任务、产物、评审、升级、决策的治理层必须自建，不能被协议替代。

## 1. 研究范围与判断标准

覆盖：MetaGPT、ChatDev、AutoGen/AG2、CrewAI、AgentScope、CAMEL、OpenAI Agents SDK、LangGraph，以及 A2A、AG-UI、MCP 三个协议；另参考 supervisor／manager、shared artifact、debate、human-in-the-loop 的公开实践与失效研究。

判断一个体系是否适合本项目，看五件事：
- 是否有明确角色契约（输入、输出、权限、完成标准），而不只是“人设提示词”；
- 是否有任务生命周期与终止条件，而不只是对话轮次；
- 是否把共享产物作为一等对象，而不只是消息上下文；
- 是否有独立验证／评审，而不是作者自证；
- 是否有人类升级通道与决策留痕，而不是“全自动”。

## 2. 横向对比

### 2.1 框架层

| 体系 | 控制模型 | 角色与拓扑 | 任务／终止 | 产物与共享上下文 | 治理／HITL 及启示 |
|---|---|---|---|---|---|
| MetaGPT | SOP 流水线，Code = SOP(Team) | 产品经理、架构师、项目经理、工程师、QA；流水线 | 按 SOP 阶段推进，靠结构化交接；无通用状态机 | 结构化 PRD／设计／API；shared message pool + 发布订阅 | 弱人环、弱辩论；启示：SOP + 结构化产物能显著降低幻觉，但缺治理 |
| ChatDev | chat chain 阶段化多轮对话 | CEO／CPO／CTO／程序员／评审／测试；instructor–assistant 对话 | 设计→编码→测试，子任务链；靠阶段完成 | 代码、评审、测试；Git 模式；Human-Agent-Interaction 模式 | 人可扮演 reviewer；无正式升级／决策记录；启示：澄清式提问（communicative dehallucination）值得借鉴 |
| AutoGen／AG2 | 事件驱动 Actor（Core）；AgentChat 团队预设 | RoundRobin、Selector、Swarm、Magentic-One、GraphFlow | 终止条件（文本触发、外部终止等）；Magentic-One 用任务台账 + 进度台账 | 共享消息上下文；台账是结构化短期记忆；状态可保存 | 有 user proxy／intervention handler；启示：事件日志、终止条件、双台账、可观测性 |
| CrewAI | sequential／hierarchical；Flows 做确定性编排 | role／goal／backstory；manager LLM 或 manager agent | 任务有描述与 expected_output；迭代上限；默认 sequential | 任务输出串联；控制上下文窗口 | manager 负责分派与结果验证，但本质是提示词角色，无硬权限；启示：角色卡 + manager 直观，需补权限与证据 |
| AgentScope | 消息交换为核心；pipeline + MsgHub 广播；Actor 分布式 | 开发者自定义；MsgHub 会议式广播 | 由开发者编排；内建容错 | 消息与多模态数据；无内建 artifact 版本 | 弱治理；启示：消息总线、广播、分布式运行时适合做平台底座参考 |
| CAMEL | role-playing + inception prompting；Agent Society | 双角色对话、社会、critic agents | 对话驱动，靠轮数／目标终止 | 对话轨迹与合成数据 | 弱治理；启示：角色扮演与批评者适合模拟／红队，不适合交付治理 |
| OpenAI Agents SDK | Agents + handoffs（工具化）+ agents-as-tools；LLM 驱动或代码驱动 | triage／handoff；manager 用 Agent.as_tool 保留最终答复权 | run／turn；RunState 可序列化恢复；sessions | 消息、工具结果、tracing | guardrails（输入／输出／工具，tripwire）、needs_approval、HITL 审批；启示：最接近“权限 + 人环”的原语，但无团队／产物／评审模型 |
| LangGraph | 图状态机；节点／边／Command／Send；checkpointer | supervisor、swarm、层级；官方已建议用 tools 实现 supervisor | 显式图状态、中断、恢复；durable execution | state／channels、checkpoints、store | interrupt／time travel 支持人环与回放；启示：“状态机 + 检查点 + 可重放”最适合任务与审批审计，但不要照搬自由图 |

补充：
- AutoGen 与 AG2 已分叉：AutoGen 主线是 Microsoft 的 Core／AgentChat／Extensions；AG2 v1.0 转向“AgentOS”，经典 autogen 命名空间迁到 ag2-classic。选型时要意识到生态碎片化。
- LangGraph 官方对 supervisor 库的说明已改为“优先用 tool-based supervisor”，原因是工具调用方式对上下文工程更可控。这与本项目“主管 Agent 用显式工具操作任务板”一致。

### 2.2 协议层

| 协议 | 解决什么 | 关键对象／状态 | 交互模式 | 与本项目的关系 |
|---|---|---|---|---|
| A2A | 跨框架、跨组织的 Agent 间互操作 | AgentCard、Task、Message、Part、Artifact、contextId；状态 SUBMITTED／WORKING／INPUT_REQUIRED／AUTH_REQUIRED／COMPLETED／FAILED／CANCELED／REJECTED | 发消息、流式 SSE、推送、查询／取消；支持多轮与异步 | 可作为未来对外互操作与任务状态映射参考；它没有评审、返工、决策记录，不能直接当内部工作流 |
| AG-UI | Agent 后端与前端的人机交互事件协议 | Run／Thread、16 类标准事件、State Snapshot／Delta、Interrupt | SSE、Webhook、WebSocket；transport agnostic | 本地对话平台的 UI、审批中断、状态同步可借鉴；不承担任务管理 |
| MCP | Agent 接工具与上下文 | Host／Client／Server；tools、resources、prompts；JSON-RPC | 请求／响应／通知；只负责上下文交换 | 工具与知识接入标准；不要拿它做 Agent 协作或任务状态机 |

一句话：MCP 是“手”，A2A 是“同事间通话”，AG-UI 是“对用户的界面”；三者都不提供治理。治理层是本项目真正的差异化。

## 3. 设计含义与建议

### 3.1 (a) 团队组建与角色定义

推荐最小闭环为“产品—研发—测试”三段式，但把“团队主管”作为独立角色，而不是让产品经理兼任：

| 角色 | 核心职责 | 决策权 | 关键产物 | 完成标准 |
|---|---|---|---|---|
| 人类主管 | 目标、预算、优先级、风险接受、最终裁决 | 最终决定权 | 目标、决策记录、风险确认 | 目标达成且风险可接受 |
| 助理 Agent | 需求澄清、议程、选项准备、记录、检索、一致性检查 | 无投票权 | 需求简报、会议议程、决策记录草稿 | 信息完整、可追溯、无遗漏 |
| 团队主管 Agent | 拆解、分派、排序、解阻、组织质量门、跨团队接口 | 团队内调度权 | 任务计划、评审安排、状态报告 | 团队任务闭环 |
| 产品 | 问题、用户、范围、用户故事、验收标准 | what／why | PRD、用户故事、验收标准 | 验收标准可测试 |
| 研发 | 方案、实现、估算、技术风险 | how／可行性 | 技术方案、代码、测试 | 可运行、可评审 |
| 测试 | 验证策略、证据、质量门 | 质量判定 | 测试计划、测试报告 | 证据可复现 |

角色契约建议用结构化 role card，而不是一段人设：
- id、name、mission、inputs、outputs、tools、authority（can／cannot）、done_criteria、escalation_triggers、memory_policy。
- 关键约束写进契约：作者不能审批自己的产物；研发不能豁免测试红线；产品不能自行扩大范围；助理不投票；团队主管不能修改人类目标与预算。

分工原则是“谁承担后果谁决策，谁专业谁提方案”：产品拥有 what／why，研发拥有 how／estimate，测试拥有 verification／quality gate；跨域冲突进入评审或升级，而不是由主管一人拍板。

取舍：动态组队／Agent 市场很吸引人，但没有稳定的角色契约与权限模型时，动态性只会放大漂移。v0.0.1 用固定模板（产品团队、研发团队含测试），把“动态”限定在任务分派与评审人选择上。

### 3.2 (b) 任务拆解、分派、追踪、验收与状态机

任务对象至少包含：id、project_id、team_id、title、goal、acceptance_criteria、owner、reviewer、priority、estimate、budget、artifact_refs、dependency_ids、state、version、created_by、created_at。任务不是消息；消息可以引用任务，任务不能只存在于聊天里。

推荐 9 个正常状态 + 1 个异常终态（failed）+ 1 个正交标记（escalated）：

    proposed --accept--> ready --claim--> in_progress --submit--> in_review
    in_progress --block--> blocked --unblock--> in_progress
    in_review --approve--> approved --verify--> done
    in_review --request_changes--> changes_requested --resubmit--> in_review
    any non-terminal --cancel--> canceled
    in_progress／in_review／approved --unrecoverable--> failed
    escalated 是正交标记：blocked／in_review／approved 可升级，处理后回到原状态或终止

| 状态 | 进入条件 | 允许转换 | 谁可操作 | A2A 映射 |
|---|---|---|---|---|
| proposed | 主管创建，写清目标与初步验收 | accept→ready；decline→canceled | 主管、owner | SUBMITTED |
| ready | DoR 满足：owner、输入、AC、预算、依赖 | claim→in_progress；cancel | owner | SUBMITTED |
| in_progress | owner 已认领 | submit→in_review；block→blocked；cancel | owner | WORKING |
| blocked | 缺依赖／信息／工具／人类决策 | unblock→in_progress；escalate；cancel | owner、主管 | INPUT_REQUIRED／AUTH_REQUIRED |
| in_review | 提交了不可变产物版本 | approve→approved；request_changes→changes_requested；escalate | reviewer／verifier（≠作者） | WORKING |
| changes_requested | 评审发现必须修改 | resubmit→in_review；cancel | owner | WORKING |
| approved | 评审通过，待验收 | verify→done；escalate | 验收 owner | WORKING |
| done | DoD 证据齐全 | 终态；重开需新任务并关联 | 验收 owner／人类 | COMPLETED |
| canceled | 范围取消／重复／超预算 | 终态 | 主管、人类 | CANCELED |
| failed | 不可恢复错误／放弃 | 终态或重试新任务 | owner、主管 | FAILED |

规则：
- DoR（Definition of Ready）：目标、验收标准、依赖、owner、预算、需要的产物类型；不满足不许开工。
- DoD（Definition of Done）：验收标准逐条有证据；产物已登记且版本固定；评审通过；无未决 blocker；人类闸门（如需要）已签。
- 门禁：只有 owner 能 claim／submit；只有 reviewer 能 approve；只有验收 owner 能 done；主管能 cancel／reassign；人类可覆盖一切但必须留痕。
- 超时：in_progress 超过估算 50% 提醒；in_review 超 SLA 升级；blocked 超时自动升级。
- 并发：任务带 version，状态转换用乐观锁；每次转换写事件（TaskCreated、TaskClaimed、ArtifactSubmitted、ReviewRequested、VerdictRecorded、TaskBlocked、Escalated、DecisionRecorded、TaskDone），状态是事件投影，可重放。

含义：A2A 的状态机面向“传输与执行”，不包含“评审／返工／验收”；本项目内部必须有自己的管理状态机，再在对外接口做映射。不要把 A2A 的 INPUT_REQUIRED 当成唯一的“等人”机制，它无法表达“等评审”“等验收”。

### 3.3 (c) 产出物版本化与关联

核心原则：聊天是过程，产物是契约。所有验收、评审、返工都针对某个不可变版本，而不是“刚才那段话”。

| 产物 | 最小内容 | 版本与关联 | 验收证据 |
|---|---|---|---|
| PRD | 问题、用户、范围、用户故事、验收标准、非目标 | 版本 + 需求 ID + 相关决策 | 产品确认 + 人类闸门 |
| 技术方案 | 架构、接口、数据、风险、回滚 | 版本 + 关联 PRD 版本 | 研发评审 + 可测试性确认 |
| 代码 | commit SHA、diff、测试 | Git 不可变提交 + 任务／PRD 关联 | CI 日志 + 评审通过 |
| 测试报告 | 用例、环境、结果、缺陷 | 版本 + 代码 SHA + 环境 | 可复现命令／日志 + 测试签字 |
| 评审记录 | 挑战、证据、裁决 | 不可变 + 产物版本 | 评审人签字 |
| 决策记录 | 背景、选项、决定、理由、异议 | 不可变 + 关联任务／产物 | 决策人 + 人类确认（如需要） |

Artifact 对象建议字段：id、type、title、version、content_hash、storage_uri、author_agent、task_id、team_id、parent_versions、depends_on、review_ids、decision_ids、status（draft／in_review／approved／superseded／deprecated）、provenance（session、model、prompt、tool）、created_at。版本只增不改；修正产生新版本并用 supersedes 关联。

存储取舍：代码天然放 Git；文档可以放 Git 或数据库，但注册表必须持内容哈希并指向不可变 URI。不要只存“最新文件路径”，否则评审与回放会失真。检索索引按 task／team／artifact／decision／时间建，消息全文检索是辅助，不是事实来源。

安全含义：产物内容是不可信输入，可能含提示注入；Agent 读取产物时应作为数据而非指令，工具调用走 allowlist，对外发布／合并／付款等动作必须人类审批。

### 3.4 (d) 跨团队评审、辩论、升级与决策记录协议

#### 3.4.1 触发与角色

强制评审：PRD、技术方案、代码、测试报告、范围变更、质量豁免。
可发起挑战：任何团队或人类，只要其工作受该产物影响；纯偏好、无证据、已决且无新证据的，不进入正式挑战。
角色：作者（回应）、评审人（质疑，不能是作者）、主持人（团队主管或助理，管流程不管对错）、裁决人（按领域：产品／研发／测试；跨域升级）、人类（最终）。

#### 3.4.2 挑战单与裁决格式

挑战单（Challenge Ticket）：
- id、from_team、to_team、artifact_id、artifact_version；
- severity：blocker（阻断验收）／major（需解决或豁免）／minor（记录不阻断）／question（澄清）；
- claim（一句话）、evidence（产物版本 + 章节／行号／commit／测试运行 + 可复现命令与观察）、impact、proposed_resolution、owner、due_at、status、thread。

裁决（Review Verdict）：
- review_id、artifact_version、verdict（approve／approve_with_comments／request_changes／escalate）；
- checked（实际验证了什么）、rationale、conditions、residual_risk、reviewer（≠作者）、decided_at、decision_record_id（如触发）。

证据标准：blocker 必须指向被违反的验收标准、回归或可复现缺陷；没有证据的质疑自动降级为 question。豁免的举证责任在申请方：必须给出证据、风险、补偿措施与人类确认。

#### 3.4.3 轮次、时限与终止

- R1：挑战 → 作者回应（接受／拒绝／降级），在 SLA 内完成。
- R2：各方一次反驳 → 主持人总结“共识／分歧／新证据”。
- 硬停止：2 个实质轮次，或时间盒（异步 24h、同步 15min，可配置），或预算耗尽；未决即升级。R2 后不接受新论点，只接受新证据；新证据重开需决策记录。
- 默认规则：可逆决策用“异议可记录、决定后共同执行”（disagree and commit）；不可逆／高风险（范围、质量豁免、架构、对外承诺）用“双钥匙”：作者 + 独立审批人，必要时人类。
- 死锁检测：立场不变且无新证据 → 主持人直接升级，不再消耗轮次。

#### 3.4.4 反无限争吵、反虚假共识、反串谋

- 反无限：轮次上限、SLA、预算、死锁检测、升级阶梯；主持人有权强制收敛。
- 反虚假共识：评审人先独立盲评再对表；要求显式填写 dissent；高风险变更设“魔鬼代言人”；裁决人不能是作者；用 rubric 而非“感觉不错”。
- 反串谋：评审人轮换、作者与审批人分离、同一对 Agent 的互审需要第三人、随机人类抽检、审批通过率异常告警、红队 Agent 专门找漏洞、验证者使用不同模型／提示词／会话。
- 注意模型偏差：MT-Bench 指出 LLM 裁判有位置、冗长、自我增强偏好；sycophancy 研究表明助手会迎合用户，人类与偏好模型也常偏好“说得漂亮”的错误答案。因此裁判要盲评、结构化 rubric、多裁判或人类校准。

#### 3.4.5 升级阶梯

| 级别 | 触发 | 处理者 | 建议 SLA | 输出 |
|---|---|---|---|---|
| L0 | 自检、单测、DoD 清单 | owner | 即时 | 证据 |
| L1 | 团队内冲突、优先级、返工 | 团队主管 | 4h | 任务更新 |
| L2 | 跨团队接口／范围／质量争议 | 双方主管 + 助理主持 | 24h | 选项 + 建议 |
| L3 | 预算、范围、不可逆、质量豁免、伦理 | 人类主管 | 24h | 决策记录 |
| L4 | 死锁、无决策、重大风险 | 人类主管 | 尽快 | 决策记录 + 风险确认 |

升级包必须包含：争议点、双方证据、选项、推荐、不决策的后果、需要谁在什么时间前决定。人类不应该是“默认收件箱”，而是异常通道。

人环机制的统一抽象：可中断（interrupt）、可序列化（保存 RunState／checkpoint）、可恢复（带 resume 载荷）、可审计（记录谁在何时批准了什么）。OpenAI Agents SDK 用 needs_approval + RunState，AG-UI 用 RunFinished 的 interrupt outcome + resume，A2A 用 INPUT_REQUIRED，LangGraph 用 interrupt + checkpointer；本项目在需求层应统一为“审批请求对象”，不要为每个协议各写一套。

#### 3.4.6 决策记录

建议采用 ADR 思路，每个决策一条不可变记录：

    id、title、status（proposed／accepted／superseded／rejected）
    context、options[]、decision、rationale、evidence[]
    dissent[]、deciders[]、consulted[]、informed[]
    affected_tasks[]、affected_artifacts[]
    revisit_trigger、supersedes、superseded_by
    human_ack、created_at

规则：范围变更、质量豁免、架构选择、跨团队裁决必须写决策记录；异议必须保留；修正用新记录 supersede，不能改旧记录；决策记录可检索、在任务与产物页面双向链接。

#### 3.4.7 辩论适用性

适合：事实争议、可测试的设计取舍、风险排序、根因分析。
不适合：价值与优先级判断（人类）、目标不清（先澄清）、无证据（先研究）、时间紧迫（先决定并留痕）。
证据：Du 等发现多轮辩论提升数学与策略推理、降低幻觉；Khan 等发现辩论能让弱裁判更准确识别真相（非专家模型 76%、人类 88%，朴素基线 48%、60%），但“更有说服力”不等于“更正确”；因此辩论必须配独立裁判与证据。

### 3.5 (e) 主管 Agent 的职责边界与失效模式

职责边界：
- 主管 owns：拆解、排序、分派、解阻、组织质量门、跨团队接口、状态汇报、预算内执行。
- 主管 does not：代替成员做专业判断、批准自己的产出、豁免测试红线、修改人类目标／预算、删除审计记录、直接对外发布或合并。
- 助理 owns：澄清、议程、选项、记录、检索、提醒、一致性检查；不投票、不裁决。
- 人类 owns：目标、优先级、预算、风险接受、Agent 启停、最终裁决。

| 失效模式 | 典型表现 | 机制／证据 | 缓解 |
|---|---|---|---|
| 微观管理 | 主管重写每个产物、所有事排队 | 角色无权限边界，主管被当成万能执行者 | 权限矩阵；“评审不重写”；只对产物版本评论；设分派预算 |
| 上下文爆炸 | 把全部消息与产物塞进上下文，质量下降 | Anthropic：多 Agent 约为聊天 15 倍 token；上下文越长越易失焦 | 结构化交接包、按任务检索、摘要、token 预算、压缩与分层记忆 |
| 虚假共识 | 一轮就全票通过，无人提异议 | sycophancy；裁判偏好“漂亮答案”；从众 | 盲评、dissent 字段、魔鬼代言人、rubric、独立裁判、人类抽检 |
| 串谋／互捧 | 两个 Agent 互相批准低质产物 | 多 Agent 风险报告把 collusion 列为三大失效之一 | 作者≠审批者、轮换、第三人复核、异常审批率监控、红队 |
| 越权／过度代理 | 自行改范围、合并代码、花钱、对外承诺 | OWASP LLM06 Excessive Agency | 最小权限、能力令牌、双钥匙、不可逆动作人类审批、工具 allowlist |
| 单点瓶颈 | 所有决策都等主管，延迟高 | 集中式 supervisor 的固有成本 | 例外升级、异步队列、有限 fan-out、助理做路由、团队内自治 |
| 规格／角色漂移 | 忽略约束、跑偏、重复劳动 | MAST：disobey task／role specification、step repetition | 任务契约、DoR、role card、固定产物版本、验收测试 |
| 验证缺失／表演 | “测试通过”但无日志、无复现 | MAST：no／incomplete／incorrect verification | 独立 verifier 带工具、证据产物、禁止自报通过、测试团队签字 |
| 死锁／无终止 | 反复争论、永不结束 | MAST：unaware of stopping conditions | 轮次上限、时间盒、死锁检测、外部终止、DoD |
| 信息囤积／断链 | 关键上下文不共享，接口对不上 | MAST：information withholding、ignored input；Cognition：上下文不共享导致隐性决策冲突 | 共享产物注册表、强制交接包、消息总线、接口契约 |
| 评估偏差 | 裁判偏好冗长／自己的输出 | MT-Bench：位置、冗长、自我增强偏差 | 盲评、多裁判、位置随机化、人类校准 |
| 成本／延迟失控 | 任务越做越贵，收益不足 | Anthropic：15 倍 token；多 Agent 只适合高价值、可并行任务 | 任务价值门槛、模型路由、预算上限、停止条件 |

一个关键结构性建议：把“提议者／执行者／验证者／裁决者”分开。主管可以组织流程，但不能同时是作者、审批者和最终裁判；助理可以准备选项，但不能投票；人类保留不可逆决策与风险接受权。

### 3.6 (f) v0.0.1 最小可用子集

目标不是“自动公司”，而是证明一条可审计的闭环：人类提出目标 → 助理澄清 → 团队产出 → 跨团队挑战 → 人类闸门 → 可回放验收。

| 能力 | v0.0.1 做 | 理由 | 延后 |
|---|---|---|---|
| 组织 | 人类 + 助理 + 2 团队（产品、研发含测试），固定模板 | 能演示跨团队争议，复杂度可控 | 动态组队、Agent 市场、嵌套团队 |
| 任务 | 10 态状态机（含 failed）+ 事件日志 + 每 Agent WIP=1 | 先保证可追踪、可审计 | 复杂依赖图、自动重规划 |
| 产物 | 注册表 + 不可变版本 + 哈希 + provenance | 验收与回放的基础 | 自动合并、并行多版本树 |
| 评审 | 挑战单 + 最多 2 轮 + 盲评 + 裁决 | 覆盖核心治理，避免无限争吵 | 自由群聊辩论、自动仲裁 |
| 升级 | L0–L3 + 决策记录 | 人类保留最终权 | 多轮谈判、自动博弈 |
| 人环 | 3 个闸门（需求／开工前方案／最终验收）+ 不可逆动作审批 | 关键风险点可控 | 细粒度逐工具审批 |
| 记忆 | session 记忆 + 档案写入策略 | 先区分“过程”与“长期事实” | 四层记忆完整实现 |
| 协议 | 内部 schema 对齐 A2A／AG-UI 概念，不实现外部互操作 | 保留演进空间 | A2A server、AgentCard、跨组织调用 |
| 观测 | 全量消息、事件、决策可检索、可重放 | 调试与信任 | 高级分析、成本归因 |

v0.0.1 需求编号建议（供 requirements.md 使用）：
- FR-01 主管登录与助理自动创建；FR-02 团队模板实例化；FR-03 任务创建／拆解／分派／状态机；FR-04 产物登记／版本／关联；FR-05 评审挑战／裁决／返工；FR-06 升级与决策记录；FR-07 人工闸门与不可逆审批；FR-08 消息全量落库与检索；FR-09 事件日志／审计／重放；FR-10 记忆边界（session 与 archive）；FR-11 轮次／预算／超时限制；FR-12 基础看板与状态视图。

验收场景（写进需求文档）：一个需求从提出到验收，至少发生一次跨团队挑战、一次升级、一条决策记录；所有产物版本与评审记录可追溯；断点后可重放；无未记录的人工批准。
成功指标：周期时间、人工干预次数、返工率、挑战解决时长、升级率、证据覆盖率、单任务成本、重放成功率。

明确不做：自主自由群聊、动态团队组建、外部 A2A 互操作、多用户、自修改提示词、自动部署、跨项目长期学习。理由：MAST 表明系统设计与验证失败占主导；Anthropic 表明多 Agent 成本高且不适合强耦合任务；Cognition 表明上下文不共享会造成隐性冲突。先把“契约、证据、升级、留痕”做对。

## 4. 对两份文档的具体建议

### 4.1 doc/target.md

- 增加“协作与治理”一章：对象模型（Task／Artifact／Review／Decision／Escalation）、角色与权限矩阵、状态机、挑战与升级协议、失效模式与缓解。
- 增加“平台 vs 框架”边界图：对话平台负责消息、落库、检索、通知；Agent 框架负责任务、产物、评审、决策、记忆；MCP 接工具，A2A 做未来互操作，AG-UI 做人机事件流。
- 写清原则：local-first、人类最终权、artifact-first、证据优先、有限自治、append-only 审计、职责分离。
- 写清非目标：不做全自动公司、不做无限辩论、不替代人类判断、不依赖云。
- 写演进路线：v0.0.1 固定团队与单项目 → v0.1 多团队与跨团队辩论 → v0.2 外部互操作与动态组队。

### 4.2 doc/0.0.1 最初版/requirements.md

- 结构：背景与目标、角色与术语、功能需求（带 ID 与验收标准）、非功能需求、边界与非目标、验收场景、风险。
- 行为写“shall”，不写实现方案：例如写“系统应记录每次状态转换并可重放”，不写“用事件溯源 + PostgreSQL”。
- 把状态机、挑战单、裁决、决策记录的字段作为行为契约写进需求附录；它们是需求，不是实现细节。
- 明确权限矩阵：谁能创建／认领／提交／评审／验收／豁免／取消；作者不能自审；人类保留不可逆决策。
- 明确数据保留与隐私：本地存储、消息全文、审计不可删、敏感字段脱敏。
- 明确非功能：单机部署、单用户、中文检索、断点恢复、成本上限、可观测性、故障降级。
- 把“辩论”限定为“挑战—回应—裁决”，v0.0.1 不承诺多轮自由辩论。

## 5. 待决策问题

1. v0.0.1 团队规模：2 团队（产品 vs 研发含测试）够不够演示跨团队辩论？还是直接上 3 团队（产品／研发／测试）以贴合 target？需要权衡演示效果与成本。
2. 人类闸门设几个、在哪：只在 PRD 与最终验收，还是加“开工前技术方案”？是否允许人类一键豁免测试 blocker（需决策记录 + 风险确认）？
3. 跨团队争议的默认裁决权归谁：领域 owner、团队主管、独立 verifier Agent，还是人类？是否引入不同模型的“独立裁判”？
4. v0.0.1 是否必须演示“多轮辩论 + 裁判”，还是只做“单轮挑战 + 裁决”？前者更有亮点，后者更稳、更便宜。

## 6. 参考来源

框架与论文：
- MetaGPT 论文：https://arxiv.org/abs/2308.00352 ｜代码：https://github.com/geekan/MetaGPT
- ChatDev 论文：https://arxiv.org/abs/2307.07924 ｜代码：https://github.com/OpenBMB/ChatDev
- Magentic-One：https://arxiv.org/abs/2411.04468
- AutoGen Core：https://microsoft.github.io/autogen/stable/user-guide/core-user-guide/index.html
- AutoGen Teams：https://microsoft.github.io/autogen/stable/user-guide/agentchat-user-guide/tutorial/teams.html
- AG2 文档：https://docs.ag2.ai/latest/ ｜README：https://github.com/ag2ai/ag2
- CrewAI 层级流程：https://docs.crewai.com/en/learn/hierarchical-process
- AgentScope 论文：https://arxiv.org/abs/2402.14034 ｜MsgHub：https://doc.agentscope.io/tutorial/task_pipeline.html
- CAMEL 论文：https://arxiv.org/abs/2303.17760
- OpenAI Agents SDK handoffs：https://openai.github.io/openai-agents-python/handoffs/
- OpenAI Agents SDK HITL：https://openai.github.io/openai-agents-python/human_in_the_loop/
- OpenAI Agents SDK guardrails：https://openai.github.io/openai-agents-python/guardrails/
- OpenAI Agents SDK 多 Agent 编排：https://openai.github.io/openai-agents-python/multi_agent/
- LangGraph supervisor：https://github.com/langchain-ai/langgraph-supervisor-py
- LangGraph interrupts：https://docs.langchain.com/oss/python/langgraph/interrupts

协议：
- A2A 规范：https://a2a-protocol.org/latest/specification/
- A2A 是什么：https://a2a-protocol.org/latest/topics/what-is-a2a/
- AG-UI 架构：https://docs.ag-ui.com/concepts/architecture
- AG-UI interrupts：https://docs.ag-ui.com/concepts/interrupts
- MCP 架构：https://modelcontextprotocol.io/docs/learn/architecture

治理、失效与辩论：
- MAST《Why Do Multi-Agent LLM Systems Fail?》：https://arxiv.org/abs/2503.13657
- Anthropic《How we built our multi-agent research system》：https://www.anthropic.com/engineering/multi-agent-research-system
- Cognition《Don't Build Multi-Agents》：https://cognition.ai/blog/dont-build-multi-agents
- Anthropic《Building effective agents》：https://www.anthropic.com/engineering/building-effective-agents
- Du 等《Improving Factuality and Reasoning in Language Models through Multiagent Debate》：https://arxiv.org/abs/2305.14325
- Khan 等《Debating with More Persuasive LLMs Leads to More Truthful Answers》：https://arxiv.org/abs/2402.06782
- MT-Bench／LLM-as-a-Judge 偏差：https://arxiv.org/abs/2306.05685
- Sycophancy：https://arxiv.org/abs/2310.13548
- 多 Agent 风险（miscoordination／conflict／collusion）：https://arxiv.org/abs/2502.14143
- ADR：https://adr.github.io/
- OWASP LLM06 Excessive Agency：https://genai.owasp.org/llmrisk/llm062025-excessive-agency/

本地路径：
- 本备忘录：D:\project\github\lignting-agents\.research\multi-agent-patterns.md
- 目标文档（待创建）：D:\project\github\lignting-agents\doc\target.md
- 最初版需求（待创建）：D:\project\github\lignting-agents\doc\0.0.1 最初版\requirements.md
- 研究目录：D:\project\github\lignting-agents\.research\

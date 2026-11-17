# internal/memory — 四层记忆

对应 requirements FR-MEM-01..12（R7）。本包只做**记忆域逻辑**：候选→审核→档案、双时间版本、容量治理、
注入预算、管理手册、记忆读取工具、读痕。**不做 SQL**（数据访问由 store 实现 Repo 端口），
**不做 HTTP**（platform 暴露 /api/memories 等），**不做 Agent loop**（core 装配上下文）。

## 1. 四层映射

| 层 | 位置 | 本包职责 |
|---|---|---|
| L1 短期上下文 | core 会话投影 + 压缩 | 只接收 InjectionRequest 里的 TaskBrief/RecentTokens 计预算；不持有消息 |
| L2 触发式记忆 | `Extractor` 端口 + SubmitCandidate | 事件→候选：额度、四道门槛、去重/冲突、敏感过滤、投毒标记 |
| L3 档案记忆 | memories 表 + Repo | 双时间/supersede/墓碑、容量治理、压缩与快照、检索注入、读痕 |
| L4 自适应提示词 | prompt_manuals 表 | 版本、diff、回滚、审批状态；默认 propose-only；红线类改动强制人类批准 |

## 2. Ports（最终版，platform-dev 按此实现）

`ports.go` 只有三个窄接口，方法参数只用 domain + 本包纯数据类型，无第三方依赖：

```go
type Repo interface {
    InsertMemory(ctx, m domain.Memory) error
    GetMemory(ctx, id string) (domain.Memory, error)
    UpdateMemory(ctx, m domain.Memory, expectedVersion int) error
    ListMemories(ctx, f Filter) ([]domain.Memory, error)
    CountMemories(ctx, f Filter) (int, error)
    AppendMemoryEvent(ctx, ev MemoryEvent) error
    AddMemoryRead(ctx, r MemoryRead) error
    MaxManualVersion(ctx, key ManualKey) (int, error)
    ListManuals(ctx, key ManualKey) ([]domain.PromptManual, error)
    GetManual(ctx, id string) (domain.PromptManual, error)
    InsertManual(ctx, m domain.PromptManual) error
    UpdateManualStatus(ctx, id, status string) error
}
type Clock interface { Now() time.Time }
type Extractor interface { Extract(ctx, ExtractionRequest) ([]Candidate, error) }
type Compressor interface { Compress(ctx, CompressionRequest) (CompressionResult, error) }
```

实现契约（必须）：
1. `UpdateMemory` 用 `WHERE id=? AND version=?`，影响行数 0 → 返回 `memory.ErrConflict`，并写入 `m.Version`。
2. `GetMemory` 未命中 → `memory.ErrNotFound`（Service 映射 404）。
3. `ListMemories`：`IncludeDeleted=false` 排除 `status='deleted' OR deleted_at<>''`；`IncludeExpired=false` 排除 `valid_to<>'' AND valid_to<=now`；
   `Tags` 为「包含任一」匹配（tags 逗号分隔）；`CreatedSince/Before` 按 RFC3339 比较。
4. `AppendMemoryEvent` **逐字保存 Detail**：压缩前快照（原文全文）就存在这里，禁止截断。
5. 表结构不改：用 schema.sql 的 `memories / memory_events / prompt_manuals / memory_reads`（330–391 行）。
6. 现成 fake：`fake_test.go`（同包测试用）；platform 集成测试可复制其语义或另写。

## 3. core 接线

core 现有窄接口（`internal/core/ports.go:133`）：

```go
type MemoryInjector interface { Inject(ctx, q MemoryQuery) ([]domain.Memory, error) }
```

**适配器放在 core**（core 已 import memory，方向 core→memory，不会成环）：

```go
type injectAdapter struct{ svc *memory.Service }

func (a injectAdapter) Inject(ctx context.Context, q core.MemoryQuery) ([]domain.Memory, error) {
    return a.svc.Inject(ctx, q.Agent, q.ConversationID, q.TaskID, q.Limit)
}
```

推荐升级（R7 联调时由 core-dev 改 ports.go）：
- 返回 `*memory.ContextRequest`（`Service.InjectWithSnapshot`）→ 才能落 `context_snapshots`、前端展示「本次回答用了哪些记忆」、给出 token 估算与降级原因（FR-MEM-08 硬要求）。
- 管理手册：`Service.ManualLayers(ctx, agent, roleCard)` → `layers.Render()` 作为 system 前缀，`version` 写进 context.request。
- 记忆读取工具：`memory.NewReadTool(svc)`，`Definition()` 注册到工具表，`Invoke(ctx, access, args)` 由 core 转调；必须传 `TurnID`。
- 事件触发：core 在任务关闭/返工/裁决/人类拍板时调 `Extractor` 产出候选后逐个 `SubmitCandidate`；消息量兜底由 core 自己计时。

## 4. 配置默认值（`DefaultConfig`）

| 项 | 默认 | 说明 |
|---|---|---|
| CandidateMaxPerTask / Chars / PerDay | 5 / 200 / 100 | FR-MEM-02/04 额度 |
| StorageMaxChars / RenderSummary / RenderContent | 15000 / 300 / 800 | 存储上限 ≠ 渲染上限（FR-MEM-06） |
| OverflowAction / CompressTarget / Cooldown | summarize / 10000 / 600s | 先落库再异步压；失败退避 |
| InjectPercent / AbsCap / SafetyMargin | 0.25 / 6000 / 8% | 预算 = min(百分比×有效输入, 绝对上限)，再留安全边际 |
| Partition | manual 8% / task 42% / archive 30% / recent 20% | 按和归一 |
| ManualMaxChars / Mode | 1500 / propose_only | 红线/权限/承诺类改动强制人类批准 |

## 5. 关键语义

- **双时间**：批准新版本时旧版 `valid_to=now`（内容不覆盖）；决策/争议/偏好禁止原地改内容（FR-MEM-05）。
- **墓碑**：删除只置 `status=deleted + deleted_at + valid_to`；Get/List/Inject/Read 全部不可见，行保留审计。
- **压缩**：单飞 + 冷却；压缩前把原文写 `memory_events(action=snapshot)`；结果为空或仍超目标 → **不落库、原文不变**；
  成功才写回并自校验字数（FR-MEM-06）。
- **human-only**（lead 裁决）：任何 Agent 都不得自动注入；助理可用 `memory.read` 显式读单条（脱敏 + `memory_reads` + 必填 turn_id）；
  员工/部门主管一律拒绝；墓碑后任何路径不可读。
- **投毒防护**：命中 `DetectInjection` 的候选打 `untrusted` 标签，禁止作为 L4 来源（除非人类亲自编辑，AC-11）。

## 6. 测试矩阵（`go test ./internal/memory`，覆盖 ~75%）

| 覆盖点 | 用例 |
|---|---|
| 候选→批准→supersede/双时间 | TestSubmitApproveSupersede |
| 重复去重（含候选之间） | TestDuplicateCandidateIsNoop |
| 删除后不可读/不注入、墓碑保留 | TestDeleteTombstoneNotReadableAndNotInjected |
| 部门隔离 / agent-private | TestInjectDepartmentIsolation、TestAgentPrivateNotVisibleToPeers |
| human-only 不自动注入 + 工具读取规则 | TestHumanOnlyNeverAutoInjected、TestReadToolHumanOnlyPolicy |
| 额度（每任务） | TestQuotaPerTask |
| 敏感过滤/投毒标记 | TestSensitiveRedactedBeforeWrite、TestInjectionPoisonMarkedUntrusted… |
| 决策内容不可覆盖 | TestDecisionContentImmutable |
| 容量 reject 模式 | TestCapacityRejectMode |
| 预算裁剪/降级/快照字段/读痕 | TestBudgetTrimming、TestAssembleDegradesOnRepoError、TestContextRequestSnapshotFields |
| 压缩快照/失败保留原文/超目标/单飞/队列 | compress_test.go 全部 |
| 手册 propose-only/批准/回滚/红线/优先级 | manual_test.go 全部 |
| core 适配器与 ManualLayers | injector_test.go |
| 单飞/冷却/脱敏/注入检测/可见性矩阵 | core_test.go |

## 7. 已知缺口（不阻塞 R7 逻辑，需要其他 owner）

1. **store 适配未实现**：Repo 12 个方法需 platform-dev 落到 *store.Store 或 store.MemoryRepo；本包只用 fake 验证。
2. **core 接口需升级**：现有 `MemoryInjector.Inject` 只返回 `[]domain.Memory`，拿不到 context.request 快照/手册版本/降级原因。
3. **事件触发管线**：`Extractor` 已定义端口，但「任务关闭/返工/裁决」到抽取的编排在 core（本包不订阅事件）。
4. **FR-MEM-12 导出/备份**：属于 store/platform 的备份路径，本包只保证档案可序列化（domain.Memory 全字段 JSON）。
5. **检查器 HTTP**：`/api/memories` 等由 platform 实现；本包提供 List/Get/Update/Delete/Pin/Approve/Reject 与手册 diff/回滚方法。
6. **批量审核**（P1）、**记忆导出 Markdown**（可由 Content+Summary 直接渲染，未提供专用 API）。

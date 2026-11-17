# v0.0.1 端到端验收矩阵（AC → FR → 测试 → 证据）

> 维护：qa-dev（Q1）。本文件只维护验收映射与当前状态；requirements.md / target.md / implementation-plan.md 正文不改。
> 上游唯一标准：[requirements.md](requirements.md) §14（AC-01..15）与 §14.2（P0 FR 组覆盖）。
> 当前阶段：R0（仅 /api/health）。15 个 AC 测试均为骨架，t.Skip；骨架可编译、可用 -Strict 防漏实现。

## 1. 运行方式

~~~powershell
# 全部 AC（mock 模式，默认；骨架阶段全部 SKIP）
.\scripts\ac.ps1

# 单个 AC
.\scripts\ac.ps1 -Run TestAC01

# 严格模式：骨架/端点未实现 = 失败（发布前必须）
.\scripts\ac.ps1 -Run TestAC01 -Strict

# 真实模型（发布验收）
$env:DEEPSEEK_API_KEY = "sk-..."
.\scripts\ac.ps1 -Mode deepseek -Strict -OutFile .\ac-report.log

# 直接 go
go test -tags=acceptance ./tests/ac -v -run TestAC01
~~~

- 环境变量：AC_MODEL_MODE=mock|deepseek；AC_STRICT=1。
- 普通 go test ./... 不包含 acceptance（build tag），不依赖 Key。
- 故障注入契约见 [docs/testing/fault-injection.md](../../docs/testing/fault-injection.md)。
- 编写指南见 [docs/testing/ac-guide.md](../../docs/testing/ac-guide.md)。

## 2. AC 矩阵

| AC | 主题 | 主要 FR（requirements §14 关联） | 测试文件 / 测试函数 | 证据命令 | 主轮次 | 当前状态 | 验证方式 |
|---|---|---|---|---|---|---|---|
| AC-01 | 首次启动与助理 | FR-ONB-01..05、FR-ASST-01/02、FR-CHAT-01、FR-SEC-01 | tests/ac/ac01_first_run_test.go / TestAC01FirstRunAndAssistant | ac.ps1 -Run TestAC01 -Strict | R1/R2 | 骨架 | 自动 + T1 人工计时 |
| AC-02 | 部门组建 | FR-ASST-03/04、FR-TEAM-01..06、FR-SEC-06 | tests/ac/ac02_provisioning_test.go / TestAC02DepartmentProvisioning | ac.ps1 -Run TestAC02 -Strict | R3 | 骨架 | 自动 + T2 人工计时 |
| AC-03 | 需求交付闭环 | FR-TASK-01..08、FR-ART-01..05、FR-REV-01..03、FR-APPR-01、FR-OPS-05、FR-WRITE-01..04 | tests/ac/ac03_delivery_test.go / TestAC03DeliveryLoop | ac.ps1 -Run TestAC03 -Strict | R4/R5 | 骨架 | 自动 + T3 人工计时 |
| AC-04 | 跨部门挑战与决策 | FR-REV-01/02、FR-DEB-01..04、FR-DEC-01/02、FR-ESC-01、FR-APPR-01/02 | tests/ac/ac04_debate_test.go / TestAC04CrossDepartmentDebate | ac.ps1 -Run TestAC04 -Strict | R6 | 骨架 | 自动（夹具注入触发） |
| AC-05 | 记忆回忆与纠正 | FR-MEM-01..12、FR-CHAT-10、FR-SEC-06 | tests/ac/ac05_memory_recall_test.go / TestAC05MemoryRecallAndCorrect | ac.ps1 -Run TestAC05 -Strict | R7 | 骨架 | 自动 + 质量人工 |
| AC-06 | 检索与审计 | FR-SRCH-01..07、FR-CHAT-11、FR-SEC-06、FR-OPS-02 | tests/ac/ac06_search_audit_test.go / TestAC06SearchAndAudit | ac.ps1 -Run TestAC06 -Strict | R1/R8 | 骨架 | 自动 + 性能基准 |
| AC-07 | 崩溃、重启与断线 | FR-CHAT-05、FR-TASK-06、FR-OPS-06、NFR-REL-01..07 | tests/ac/ac07_recovery_test.go / TestAC07CrashRestartOffline | ac.ps1 -Run TestAC07 -Strict | R1/R8 | 骨架 | 自动 + 真实进程/人工 |
| AC-08 | 安全与权限 | FR-SEC-01..07、NFR-SEC-01..08 | tests/ac/ac08_security_test.go / TestAC08SecurityAndPermissions | ac.ps1 -Run TestAC08 -Strict | R2/R8 | 骨架 | 自动 + 恶意网页人工 |
| AC-09 | 预算与暂停 | FR-OPS-05、FR-SEC-07、FR-OBS-03 | tests/ac/ac09_budget_test.go / TestAC09BudgetAndPause | ac.ps1 -Run TestAC09 -Strict | R8 | 骨架 | 自动（预算注入） |
| AC-10 | 备份、导出与恢复 | FR-OPS-01/02、FR-MEM-12、NFR-REL-05 | tests/ac/ac10_backup_test.go / TestAC10BackupExportRestore | ac.ps1 -Run TestAC10 -Strict | R8 | 骨架 | 自动 + 第二数据目录 e2e |
| AC-11 | 记忆注入与跨会话复用 | FR-MEM-07/08/09/11、FR-OBS-04、FR-SEC-04 | tests/ac/ac11_memory_injection_test.go / TestAC11MemoryInjection | ac.ps1 -Run TestAC11 -Strict | R7 | 骨架 | 自动 + 投毒人工复核 |
| AC-12 | 部门主管核对与人类抽检 | FR-TASK-07、FR-APPR-01、FR-REV-05、FR-OBS-02 | tests/ac/ac12_checklist_test.go / TestAC12SupervisorChecklistAndSampling | ac.ps1 -Run TestAC12 -Strict | R4 | 骨架 | 自动 + 抽检人工 |
| AC-13 | 注意力、收件箱与审批 | FR-CHAT-01/06、FR-ASST-06、FR-APPR-02、FR-OPS-07、FR-SEC-07 | tests/ac/ac13_inbox_test.go / TestAC13AttentionInboxApprovals | ac.ps1 -Run TestAC13 -Strict | R3/R8 | 骨架 | 自动（TTL/沉默注入） |
| AC-14 | 群聊与 Agent 管理 | FR-CHAT-14/15/16、FR-TEAM-06、FR-SEC-07 | tests/ac/ac14_group_chat_test.go / TestAC14GroupChatAndAgentLifecycle | ac.ps1 -Run TestAC14 -Strict | R3 | 骨架 | 自动 + 观察者人工 |
| AC-15 | 并发写入与变更时间线 | FR-WRITE-01..04、FR-OPS-06、FR-SEC-03 | tests/ac/ac15_write_test.go / TestAC15ConcurrentWriteTimeline | ac.ps1 -Run TestAC15 -Strict | R5 | 骨架 | 自动（并发/裁剪） |

## 3. 夹具与 AC 的对应

固定 ID 定义见 tests/fixtures/fixtures.go（常量）与 SeedResult；seed 通过 fixtures.SeedDefault 写入临时 SQLite。

| AC | 使用的夹具 |
|---|---|
| AC-01 | 空库 + harness（setup/bootstrap） |
| AC-02 | intent_1 / bp_1 / prov_1 / 三部门；G1 审批 |
| AC-03 | task_1 + crit_1..3、art_prd/art_code v1、review_open、verdict_1、write_applied |
| AC-04 | review_open、conv_cross_dept、dec_0/dec_1、appr_g1 |
| AC-05 | mem_pref / mem_decision / mem_candidate / mem_dept_private / mem_deleted、manual_1 |
| AC-06 | msg_search_limit、msg_deleted_secret、audit 链、usage_ledger |
| AC-07 | turn_interrupted（TOOL_OUTCOME_UNKNOWN）、faults kill/model_timeout |
| AC-08 | Agent token / 部门成员、appr_irreversible、faults deny_tool/force_forbidden_read |
| AC-09 | usage_1/2、faults budget_force_percent |
| AC-10 | 全量 seed；真实二进制 + 第二个 data-dir |
| AC-11 | mem_pref/dec_1、context_snapshots（ctx_1）、faults 投毒输入 |
| AC-12 | task_1 + crit_2 缺证据、verdict_1、review_* |
| AC-13 | notif_* 四类、appr_g3/appr_irreversible、bridge_1、faults approval_ttl/human_silence |
| AC-14 | conv_project/dept/cross、bridge_1、agents 生命周期 |
| AC-15 | write_applied/write_conflict/write_other、37 条 file_timeline、git_events/commit_groups |

搜索索引不预置：AC-06 必须走生产重建路径，避免夹具掩盖索引缺陷。

## 4. 证据记录模板

每个 AC 每次运行记录一行/一段，附在发布报告：

~~~text
AC-XX <名称> | 模式: mock|deepseek | strict: on|off | 日期/commit:
结果: PASS|FAIL|SKIP | 耗时: | 成本:
注入: <faults + 注入点> | 数据目录: <temp/第二个目录>
证据: <命令、审计 seq、文件 hash、截图/trace 路径>
缺陷: <task id 或 无> | 备注: <人工判定/跨平台>
~~~

- SKIP 不计入发布；发布前必须 0 SKIP（-Strict）。
- 自动断言只覆盖确定性内容；模型质量判断、T1/T2/T3 计时、跨平台、恶意网页、Beta 由人工记录。
- 每次发布前重跑：scripts/test.ps1 + scripts/ac.ps1 -Strict + benchmark.ps1。

## 5. 人工/半自动验收项（不计入 Go 测试）

| 项 | 原因 | 证据 |
|---|---|---|
| T1/T2/T3 时间 | 真人操作/网络/模型耗时 | 秒表 + 截图 + 审计时间 |
| 三平台 | Windows/macOS/Linux 各跑一次 | CI matrix 或人工记录 |
| 真实模型质量 | 非确定性 | 人工评分 + 金标准任务 |
| 恶意网页跨站 | 浏览器安全模型 | 手工清单 + 截图 |
| 断线 1 小时 | AC-07 发布前演练 | 日志/补齐计数 |
| Beta 3 用户 T2/2 用户 T3 | 用户验证计划 §12.5 | 访谈/记录模板 |
| 成本抽样 | 供应商账单对齐 | 账单 vs usage_ledger |

## 6. 当前已知缺口（Q1）

1. 15 个 AC 全部骨架 skip；R1 起按轮次点亮（建议顺序 AC-01/02 → AC-03/12 → AC-04 → AC-05/11 → AC-06/07/08 → AC-09/10/13 → AC-14/15）。
2. AC-07 的「kill 主进程」需要真实二进制启动器（os/exec + 临时 data-dir）；当前 harness 只覆盖 in-process 断线/重启存储恢复，杀进程步骤需 e2e helper。
3. AC-10 恢复需要第二个 data-dir 与真实二进制；in-process harness 只能覆盖备份产物与索引重建。
4. AC-06 的 10 万条 p95/Recall 需 tests/bench 语料与 benchmark 脚本（R1 search 落地后）。
5. AC-12 的「同一对 Agent 互审 ≥5 次 100% 通过」需要测试内批量创建裁决（或新增 fixture 选项），当前 seed 只有 1 条 verdict。
6. AC-14 临时桥到期需要可控时钟或短 TTL；seed bridge_1 为 1h，实现时用注入/短 TTL 验证。
7. 生产代码当前存在并行开发中的编译错误（internal/memory、internal/core）；tests/ac 可独立编译，但 scripts/test.ps1 需等对应 owner 修复后全绿。

## 7. 维护规则

- AC 测试点亮后：把「当前状态」改为「已实现/通过」，补充首次通过日期、模式、commit、证据路径；保留原 FR 关联。
- 发现验收缺口/FR 无 AC：在 §6 记录并开 task；不要改 requirements.md。
- 新增 P0 FR：同步 requirements §14.2 矩阵与本文；没有 AC 的 P0 不进入发布清单。
- 发布前：scripts/ac.ps1 -Strict 0 SKIP；scripts/test.ps1 全绿；benchmark 达标；人工项记录齐全。

# 验收测试编写指南（tests/ac）

> 状态：Q1 骨架已建立（AC-01..15 全部 t.Skip）；R1–R9 逐 AC 点亮。

## 1. 运行

~~~powershell
# 全部 15 个 AC（mock 模式，默认）
.\scripts\ac.ps1

# 单个 AC，严格模式（缺端点=失败而不是 skip）
.\scripts\ac.ps1 -Run TestAC01 -Strict

# 真实模型发布验收
$env:DEEPSEEK_API_KEY = "sk-..."
.\scripts\ac.ps1 -Mode deepseek -OutFile .\ac-report.log
~~~

直接使用 go：

~~~powershell
go test -tags=acceptance ./tests/ac -v -run TestAC01
~~~

- 不加 -tags=acceptance 时 tests/ac 只编译 doc.go，普通 go test ./... 不受影响。
- AC_STRICT=1：把「骨架/端点未实现」的 skip 变 failure；发布前必须开。
- AC_MODEL_MODE=deepseek|mock：选择模型模式；默认 mock。

## 2. 一个 AC 测试的标准结构

1. h := NewHarness(t, WithSeed())：临时数据目录 + SQLite + 内嵌 HTTP server + cookie jar。
2. h.Setup("阿哲")（AC-01/02）：bootstrap + setup；拿到 CSRF。
3. 用 h.Client.Get/PostJSON 驱动 API；Response.MustStatus/Envelope 断言冻结包。
4. 状态/审计/证据用 h.Store.DB 只读查询或 API 断言；**不要**断言真实模型固定措辞。
5. 故障场景用 WithFaults(fixtures.Faults{...})；报告记录注入点。
6. 长流程用 WaitFor；SSE 用 Client.ReadSSE(ctx, since, want)。
7. 删除 SkipSkeleton 行后才算点亮；CI 中 -Strict 防止漏实现。

## 3. 夹具

fixtures.SeedDefault 固定 ID（见 tests/fixtures/fixtures.go 常量与 SeedResult）：

- 部门：dept_product / dept_dev / dept_test；主管 agent_prod_lead / agent_dev_lead / agent_test_lead；员工 agent_dev_a / agent_dev_b / agent_test_emp。
- 会话：conv_dm_assistant / conv_project / conv_dept_* / conv_cross_dept / conv_task_task_1 / conv_decision_log。
- 任务：task_1（approved，3 条 criteria：有证据通过 / 缺证据 / 需人类判断）；task_2 进行中。
- 产出：art_prd v1、art_code v1；评审：review_open（blocker）、review_resolved + verdict_1（approve）。
- 决策：dec_1 accepted、dec_0 superseded；审批：appr_g1 approved、appr_g3 pending、appr_irreversible pending（dual_key）。
- 记忆：mem_pref（human-only）、mem_decision、mem_candidate、mem_dept_private、mem_deleted。
- 写：write_applied / write_conflict / write_other、37 条时间线（含 35 条历史）；审计 4 条 hash chain；四类通知；turn_interrupted（TOOL_OUTCOME_UNKNOWN）；bridge_1（1h 到期）。

搜索索引**不预置**：AC-06 必须走生产重建路径（POST /api/admin/reindex 或 search 包），避免夹具掩盖索引缺陷。

## 4. 断言原则

- 只断言**确定性**内容：状态码、状态机、审计、证据链接、消息数、版本 hash、权限拒绝。
- 不断言模型措辞、不依赖固定 token 数（可记录实际值）。
- 每个 AC 至少一个「负例」（缺证据 G3 拒绝、越权 403、删除不可搜、超限 409）。
- 证据命令必须可复制；报告包含：AC、模式、strict、通过/失败、耗时、成本、注入点、截图/trace 路径。
- 测试数据只落在 t.TempDir()；不得写真实数据目录/工作区。

## 5. 报告模板

~~~text
AC-XX <名称>
  模式: mock|deepseek   strict: on|off   数据目录: <temp>
  结果: PASS|FAIL|SKIP（SKIP 不计入发布）
  耗时:  成本:
  注入: <fault switches 与注入点>
  证据: <命令 / 审计 seq / 文件 hash / 截图路径>
  缺陷: <task id 或 无>
~~~

## 6. 当前状态（2026-11-18）

- AC-01..15 全部点亮并 PASS（mock 模式，`go test -tags=acceptance ./tests/ac`，约 12s，0 Skip）。
- 真实模型模式（`AC_MODEL_MODE=deepseek` + `DEEPSEEK_API_KEY`）需在有 Key 的环境人工复跑 AC-01/03/07。

## 7. 发布冒烟与断线演练（R9）

真实二进制冒烟（Linux/macOS，无需 Key）：

~~~bash
go build -o bin/lignting ./cmd/lignting
./scripts/smoke.sh          # health(schema_version) → setup → bootstrap → 建会话 → 发消息 → 列表 → 检索 → 审计 → SSE 头
~~~

断线补齐演练（自动化等价验证）：

~~~bash
go test ./tests/verify/e2e -run TestR9OfflineCatchUpDrill -v
DRILL_EVENTS=7200 go test ./tests/verify/e2e -run TestR9OfflineCatchUpDrill -v   # 加倍压力
~~~

- 做法：先连 SSE 拿到 baseline 游标后断开，断线窗口内灌 `DRILL_EVENTS`（默认 3600）条消息事件
  （≈1 小时 @1 条/秒的写入量），再带 `since=baseline` 重连，断言补齐的事件 id 连续、不重、不漏。
- 补发逻辑只按 `events.seq` 游标切片、没有按时间的清理，所以正确性与墙上时间无关，取决于断线期间事件条数；
  真实浏览器断网 1 小时的人工演练仍是发布前人工项（见 [release-checklist](../doc/0.0.1%20最初版/release-checklist.md)）。
- 2026-11-18 结果：3600 条断线消息 + 起止标记，重连 16.9ms 补齐 3604 条事件，id 连续、0 重复、3600/3600 命中。

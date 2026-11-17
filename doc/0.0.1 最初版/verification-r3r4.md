# 独立验证报告 R3/R4（真实二进制 E2E + ACL）

> **验证者**：verifier（独立验证/红队，不写生产代码）
> **任务**：task-44《独立验证：R3/R4 真实二进制 E2E 与 ACL》
> **基线**：R3/R4 合入后的工作树（未提交）；契约基线 `api-contract.md` v1.1（§12）。
> **验证时间**：2026-11-17 16:15–16:25（首轮）、16:30（task-42/43 修复后复验）（Asia/Shanghai）
> **方法**：`tests/verify/e2e` 用 **os/exec 启动真实二进制**（`go build ./cmd/lignting` → 临时数据目录 → `--addr 127.0.0.1:<随机端口>` → `--mock-model`），HTTP 黑盒驱动；agent token 仅作为**测试夹具**写入临时库（生产无签发端点，见观察项）；未修改生产代码、未 commit。
> **说明**：R3/R4 流程是平台/核心业务逻辑，不依赖模型输出，因此 mock 模式不影响结论；需要模型的 AC-01/03 质量判断在 R9 真实模型下复验。

## 0. 结论

| 范围 | 结论 |
|---|---|
| **R3 组织/蓝图/G1/Provisioning/生命周期** | **通过**：intent→blueprint→未 G1 403→G1 批准→provisioning `verified`→重复 provisioning 幂等→org 4 部门/10 Agent→部门 pause/resume→Agent pause/resume→空闲 Agent stop=409（语义正确） |
| **R4 任务/产出/评审/闸门** | **通过**：DoR→proposed/ready/discussing→**未 G2 开工 403**→G2 批准→in_progress→in_review→产出物 v1→挑战单→request_changes→返工 v2→G3 缺证据 400→证据+主管核对→G3 pending→人类批准→approved→**done 前 G3 门禁**→done；作者≠评审者、作者不自批、非法转换/终态回退均拒绝；时间线 ≥8 事件、产出物 2 版本 |
| **task-25 搜索 ACL** | **通过**：owner 全见；研发员工 token 只命中本部门、跨部门与私聊 total=0；无效 token 401 |
| **记忆 ACL** | **通过**（按实现词表）：owner 可见 human-only、Agent 0 泄漏；本部门 team 记忆员工可见、他部门员工 0 |
| **发布门修复复验** | **D-R3R4-2 已关闭**（task-42：G2 门禁 + done 统一 G3）；**D-R3R4-3 已关闭**（task-43：§12.4 别名归一化）。复验见 §4.1 |
| **报告修正项** | 早期 `TestR2AgentTokenSearchAndMemoryACL` 的 503 根因是 **cmd 传内存零值 Config**（`memory.Options{Repo:...}` → `Normalize()` 不补 `Enabled`），Lead 已修 `main.go:86` 为 `Config: memory.DefaultConfig()`；verifier 复验 `/api/memories` 正常（D-R3R4-1 关闭） |

## 1. R3 证据（`go test ./tests/verify/e2e -run TestR3 -v`）

| # | 步骤 | 实测 |
|---|---|---|
| 1 | POST /api/intents {goal} | 200，version=1；GET latest 含目标 |
| 2 | POST /api/blueprints {option:"full"} | 200，blueprint.status=proposed，approval.kind=G1/status=pending |
| 3 | 未 G1：POST /api/provisioning | **403 forbidden**（"蓝图尚未获得 G1 批准"） |
| 4 | 未 G1：POST /api/departments | **403 forbidden**（"尚无 G1 批准的蓝图"） |
| 5 | POST /api/blueprints/{id}/approve | 200，status=approved；GET /api/approvals?kind=G1 有 approved 记录 |
| 6 | POST /api/provisioning | 200，status=verified，steps 全部 done |
| 7 | org 投影 | departments=4（product/engineering/qa/platform），agents=10，assistant 存在 |
| 8 | 重复 provisioning | 200 verified；部门/Agent 数不变（幂等） |
| 9 | 部门 pause/resume | paused→active；部门内 Agent 同步 paused/idle |
| 10 | Agent pause/resume | paused→idle |
| 11 | 空闲 Agent stop | **409 conflict**，错误信息含 turn（不会 500/静默成功；符合"仅停止运行中 turn"） |
| 12 | 用未批准的 lean 蓝图 id 直接建部门 | 403（G1 绕过防护对具体蓝图同样生效） |

## 2. R4 证据（`go test ./tests/verify/e2e -run TestR4 -v`）

| # | 步骤 | 实测 |
|---|---|---|
| 1 | 员工（employee）建任务 | 403（只有助理/主管可拆解） |
| 2 | 主管建任务（DoR：验收 3 条/owner/reviewer/approver/视角/预算/时限/high_risk） | 200，status=proposed，criteria=3 |
| 3 | proposed→done 跳步 | **409**（状态机拒绝） |
| 4 | ready→discussing（owner） | 200；随后 in_progress → **403**（等待 G2） |
| 4b | 找到本任务 pending G2 → POST /api/approvals/{id}/decide approved | 200，G2 status=approved；之后 in_progress/in_review 均 200 |
| 5 | 产出物 v1（owner） | 200，version=1 |
| 6 | 挑战单（reviewer，blocker+证据） | 200，review.status=open |
| 7 | 作者提交裁决 | **403**（作者不能评审自己产出物） |
| 8 | 作者批准任务（approved 转换） | **403**（不能自批） |
| 9 | request_changes（reviewer，带修改项） | 200，review.status=rework |
| 10 | 任务转 changes_requested（reviewer） | 200 |
| 11 | 返工（owner） | 200，version=2 |
| 12 | 重新 in_review（owner） | 200 |
| 13 | 缺证据提交 G3 | **400**（核对表未就绪，列出缺失项） |
| 14 | 逐条补证据（owner）+ 主管核对 approved（supervisor） | 每项 200 |
| 15 | 员工填主管结论 | **403**（只有主管/助理） |
| 16 | 提交 G3 | 200，approval.status=pending |
| 17 | 人类 G3 批准 | 200，status=approved |
| 18 | approved（reviewer）→ done（approver） | 200/200；终态 done |
| 19 | done→in_progress | **409**（终态不可回退） |
| 20 | 任务详情 | status=done，events≥8，criteria 证据+主管结论齐全，artifacts=1，versions=2 |

## 3. ACL 证据（task-25）

**搜索 ACL**（真实二进制；owner 会话 + `X-Lignting-Agent-Token`）：
- 造数：研发部门群“研发限流词”、测试部门群“测试绝密词”、owner 私聊“私聊秘密词”。
- owner：三词 total≥1（全见）。
- 研发员工 token：「研发限流词」total≥1；「测试绝密词」「私聊秘密词」**total=0**。
- 无效 token → **401** `unauthorized`（搜索不泄漏存在性）。
- 实现路径：`handlers_search.go searchACL` 在 SQL 前置过滤 visibility+会话集合（owner 不受限）。

**记忆 ACL**（实现词表）：
- human-only：owner GET /api/memories 可见；研发员工 token 0 条。
- team（部门）：研发员工可见本部门约定；测试员工（他部门）0 条。
- 契约词表差异见 D-R3R4-3。

## 4. 缺陷与观察

### D-R3R4-1（已关闭，Lead 修复 + verifier 复验）
- 现象：早期真实二进制 `POST /api/memories` → 503 `memory_disabled`。
- 根因：`cmd/lignting/main.go` 用 `memory.NewService(memory.Options{Repo: ...})`，`Options{}.Config` 零值经 `Normalize()` 后 `Enabled=false`（`Normalize` 只补数值不补 Enabled；最小探针：zeroNormalizeEnabled=false / defaultConfigEnabled=true）。
- 修复：`main.go:86` 改为 `Config: memory.DefaultConfig()`；复验 /api/memories 200，ACL 用例全过。

### D-R3R4-2（已关闭，task-42）：人类闸门 G2 未实现 + 非高风险任务绕过 G3
- **修复前证据**：high_risk=true 任务 200 进入 in_progress 且 G2 审批为 0；普通任务 in_review→approved→done 全程 200 无 G3（`task_service.go` 仅 `to=="done" && TaskHighRisk` 校验）。
- **修复后实现**：CreateTask 对 high_risk 自动生成 pending G2（flags.g2_approval_id）；`transition→in_progress` 调 `ensureG2Approved`（pending/rejected → 403 并给审批 id）；`transition→done` 对所有任务要求 `g3_approval_id` 为 approved（403 + 升级路径）。
- **复验**：未批准 G2 时 in_progress=403；批准 G2 后 in_progress/in_review=200；普通任务缺 G3 时 done=403，补齐证据+主管核对+SubmitG3+批准后 done=200；verbose 输出无 `DEFECT D-R3R4-2`。

### D-R3R4-3（已关闭，task-43）：契约 §12.4 memory 词表未落地
- **修复前证据**：`POST /api/memories {visibility:"human_only"}` → 400 未知可见性；`entity_type:"ability"` → 400 未知实体类型；旧别名未归一化。
- **修复后实现**：`internal/platform/aliases.go` 统一归一化——记忆 visibility `human_only|human-only → human-only`、`agent-private→private` 等旧别名映射；entity_type `ability/abilities/capabilities→capability`（未知 400）；会话词表保持契约 canonical `human_only`。
- **复验**：同一 E2E 用契约值 `visibility:"human_only"` + `entity_type:"ability"` 创建成功（服务端归一化），记忆 ACL 断言全过；verbose 输出无 `DEFECT D-R3R4-3`。

### 4.1 修复复验记录（task-42/43）

~~~powershell
go vet ./tests/verify/...            # exit 0
go test ./tests/verify/... -count=1   # e2e 2.5s / http 0.8s / store 2.4s / schema 2.5s 全绿
go test ./tests/verify/e2e -v -count=1
# --- PASS: TestR3OrgBlueprintProvisioning
# --- PASS: TestR4TaskDeliveryFlow（G2 403→批准→放行；普通任务 G3 403→补齐→done）
# --- PASS: TestR2AgentTokenSearchAndMemoryACL
# --- PASS: TestR2HumanOnlyMemoryACL（human_only/ability 契约值直接可用）
# 无任何 DEFECT D-R3R4-x 日志
~~~

### 观察项（不判缺陷）
1. **Agent Token 无签发端点**：`SetAgentTokenHash` 只有 store 方法，platform 无路由；本验证用测试夹具写临时库。若 Agent 需通过 HTTP 调用平台，需在 R8 补"签发/轮换"端点（或明确 Agent 只在进程内调用）。
2. **Provisioning 失败回滚未注入**：`Provision` 有步骤化回滚与 orphan 报告，但无故障注入 hook；本轮只验证成功/重复幂等，失败回滚留 R9 故障注入。
3. **StopAgent 语义**：无运行 turn 返回 409 是正确行为；如产品希望"强制停止"需另设端点。
4. **G1 通过后直接建部门**允许（用已批准蓝图 id），符合"人类已批准蓝图"的设计；未批准蓝图 id 仍 403。

## 5. 命令与资产

~~~powershell
# R3/R4/ACL：真实二进制（每次 TestMain 现场 go build）
go test ./tests/verify/e2e -v -count=1

# 并发源码暂时不可构建时，用预构建二进制（仅应急）
$env:LIGINTING_E2E_BIN = (Resolve-Path 'tests/verify/bin/lignting-e2e.exe').Path
go test ./tests/verify/e2e -v -count=1

# 全套 verifier（当前全绿）
go vet ./tests/verify/...
go test ./tests/verify/... -count=1
~~~

测试资产：
- `tests/verify/e2e/r3r4_test.go`：R3（约 20 断言）、R4（约 30 断言）、搜索 ACL、记忆 ACL；缺陷以 `DEFECT D-R3R4-x` 日志 + task 追踪。
- `tests/verify/probe/main.go`：D-R3R4-1 根因最小探针（zero Normalize vs DefaultConfig）。
- 复用：`tests/verify/http|store|schema`（R0/R1 回归）。

## 6. 下一步

1. ~~task-42/43 修复复验~~ ✅ 已完成（D-R3R4-1/2/3 全部关闭，套件无 DEFECT 日志）。
2. R8/R9：provisioning 失败回滚注入、Agent Token 签发/轮换端点（若需 Agent HTTP 接入）、真实模型下的 AC-03/AC-12 质量判断、性能/Recall 基准。
3. 后续若改状态机/闸门/词表，先跑 `go test ./tests/verify/...` 作为回归门。

# 测试用故障注入契约（Q1 起草，R2/R8 接线）

> 状态：契约已定义；当前 R0 生产代码**不读取**该文件，所有开关默认关闭。
> 适用：tests/ac、集成测试、发布前故障演练。生产启动不得因为缺少 faults.json 报错。

## 1. 两个入口

1. **文件**：<data-dir>/faults.json（测试创建临时数据目录后写入，0600）。
2. **环境变量**：LIGNT_FAULT_*（单进程覆盖，适合启动真实二进制）。

两者同时存在时，约定环境变量优先；v0.0.1 只要求实现一种读取顺序并在测试报告里记注入点。

## 2. 默认与安全边界

- 默认关闭：缺失文件 = 无故障、无警告、无报错。
- 只允许测试/acceptance 构建或显式测试配置读取；生产启动不因它分支业务正确性。
- 文件不含密钥；写入 0600；导出诊断包时不含 faults.json。
- 每个 AC 报告必须记录：注入开关、注入点、期望行为、实际行为。

## 3. 开关清单

| JSON 字段 | 环境变量 | 用途 | 接线轮次 |
|---|---|---|---|
| model_timeout_ms | LIGNT_FAULT_MODEL_TIMEOUT_MS | 模型调用超时 | R2 |
| model_no_key | LIGNT_FAULT_MODEL_NO_KEY | 无 Key 降级 | R2 |
| model_error | LIGNT_FAULT_MODEL_ERROR | 供应商 5xx/错误 | R2 |
| model_rate_limit | LIGNT_FAULT_MODEL_RATE_LIMIT | 429 限流 | R2 |
| kill_before_commit | LIGNT_FAULT_KILL_BEFORE_COMMIT | 写库前崩溃 | R1/R8 |
| kill_after_tool_effect | LIGNT_FAULT_KILL_AFTER_TOOL_EFFECT | 工具有副作用后崩溃 | R8 |
| sse_disconnect_after | LIGNT_FAULT_SSE_DISCONNECT_AFTER | SSE 断线 N 个事件 | R1 |
| budget_force_percent | LIGNT_FAULT_BUDGET_FORCE_PERCENT | 强制预算到 80/100 | R8 |
| approval_ttl_seconds | LIGNT_FAULT_APPROVAL_TTL_SECONDS | 审批 TTL 缩短 | R8 |
| human_silence_hours | LIGNT_FAULT_HUMAN_SILENCE_HOURS | 人类静默降级 | R8 |
| deny_tool | LIGNT_FAULT_DENY_TOOL | 拒绝指定工具 | R8 |
| force_forbidden_read | LIGNT_FAULT_FORCE_FORBIDDEN_READ | 强制越权读取 | R8 |
| allow_bypass_permission | LIGNT_FAULT_ALLOW_BYPASS_PERMISSION | 验证「拒绝后不可绕过」 | R8 |
| index_stale | LIGNT_FAULT_INDEX_STALE | 索引滞后/失效 | R1 |
| write_conflict | LIGNT_FAULT_WRITE_CONFLICT | 并发写冲突 | R5 |
| git_branch_change | LIGNT_FAULT_GIT_BRANCH_CHANGE | 分支切换事件 | R5 |
| concurrency | LIGNT_FAULT_CONCURRENCY | 并发写/请求数 | R5 |

字段定义见 tests/fixtures/faults.go；测试通过 fixtures.Faults + fixtures.WriteFaults 写入。

## 4. 测试用法

~~~go
h := NewHarness(t,
    WithFaults(fixtures.Faults{ModelTimeoutMS: 50, BudgetForcePercent: 100}),
    WithSeed(),
)
// h.Faults 可在断言/报告里引用；env 已按 LIGNT_FAULT_* 注入当前测试进程。
~~~

启动真实二进制时：

~~~powershell
$env:LIGNT_FAULT_MODEL_NO_KEY = "1"
.\scripts\dev.ps1 -Port 8788 -DataDir .\tmp-ac07
~~~

## 5. 与 AC 的对应

| AC | 需要的注入 |
|---|---|
| AC-01/07 | model_no_key、model_timeout_ms、kill_after_tool_effect、sse_disconnect_after |
| AC-02 | kill_before_commit（provisioning 失败回滚） |
| AC-08 | deny_tool、force_forbidden_read、allow_bypass_permission |
| AC-09 | budget_force_percent |
| AC-10 | index_stale（重建前） |
| AC-13 | approval_ttl_seconds、human_silence_hours |
| AC-15 | write_conflict、git_branch_change、concurrency |

## 6. 当前缺口

- R0 不读取开关；R2/R8 接线时必须同时补「读取顺序 + 注入点日志 + 关闭时零行为差异」的测试。
- 故障注入不得改变正常路径的返回值/错误码；只改变注入条件下的行为。

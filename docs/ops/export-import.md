# 导出与导入（v0.0.1 / R8）

> 关联需求：FR-OPS-02、FR-MEM-12、AC-10。实现：`internal/backup`。
> 备份/恢复见 `docs/ops/backup-restore.md`。

## 1. 导出

```go
res, err := backup.Export(ctx, dataDir) // 默认脱敏，写到 <dataDir>/exports/export-...
res, err := backup.ExportTo(ctx, dataDir, outDir, backup.ExportOptions{IncludeSensitive: true})
```

产物（开放、可读）：

- `export.json`：结构化数据，表 → 行 → 列。包含 messages、tasks、task_criteria、task_events、artifacts、artifact_versions、reviews、review_verdicts、decisions、memories、memory_events、audit_log。
- `README.md`：数量与版本摘要（叙事）。
- `<table>.md`：每个表的人类可读片段（每条一行，内容截断 200 字符）。

默认 **脱敏**（`redacted: true`）：

- 敏感键（`api_key/token/password/secret/...`）值替换为 `[REDACTED]`；
- 已知 DeepSeek Key、`sk-...`、`Bearer ...` 等形态的文本被替换；
- JSON 字段（`detail/payload/meta/evidence/...`）按对象递归脱敏。

显式 `IncludeSensitive: true` 才导出原文——用于迁移/恢复场景，需由平台做权限校验并写审计。

进度：`ExportOptions.Progress` 每表回调一次；当前不做分片（大表导出在内存中组装 JSON，后续可加分片）。

## 2. 导入

```go
counts, err := backup.Import(ctx, dataDir, exportDir, backup.ImportError)
counts, err := backup.Import(ctx, dataDir, exportDir, backup.ImportSkip, true)
```

- 校验 `format/format_version/schema_version`；schema 高于当前程序拒绝（`ErrVersionMismatch`）。
- 只允许上述白名单表/列；未知表/列拒绝（`ErrInvalidExport`）。
- **默认不覆盖**：目标库已有数据时必须显式传 `confirm=true`（`ErrConfirmRequired`）。
- 冲突策略：
  - `ImportError`：遇到重复主键立即回滚并返回 `ErrConflict`；
  - `ImportSkip`：`INSERT OR IGNORE`，跳过重复行并计数。
- 导入在单事务中执行；失败回滚。**不删除、不替换现有行**（confirm 只允许向非空目标追加/跳过）。
- 导入后校验审计 hash 链。**脱敏导出中的 audit_log 不导入**（detail 被改写后 hash 无法重算，强插会破坏链），结果里会给出 warning；迁移请使用 `IncludeSensitive: true` 的导出。
- 返回每表 `Rows/Inserted/Skipped`、总插入/跳过、审计条数、是否校验通过。

## 3. 权限与审计（平台侧职责）

本包不写 HTTP，也不查询当前用户权限。平台在调用 Export/Import 前必须：

- 校验「导出/导入」权限（人类 owner / 授权范围）；
- 对导出/导入动作写审计（谁、何时、哪个目录、是否脱敏）；
- 导出目录放在用户可访问位置，导入前提示「会新增数据，不覆盖」。

## 4. 限制

- 导出只含 DB 中对象（附件文件的二进制不在 JSON 中；附件元数据/产出物版本内容在 DB）。
- 大导出未分片；导入会逐行插入，超大库建议先备份再用备份恢复。
- 导入不会重建搜索索引；平台启动后按现有索引重建流程处理。

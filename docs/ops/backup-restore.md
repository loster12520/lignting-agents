# 备份与恢复（v0.0.1 / R8）

> 适用：本机单用户数据目录（默认 `.lignting-data`）。实现：`internal/backup`。
> 关联需求：FR-OPS-01、FR-MEM-12、AC-10；关联里程碑：R8。

## 1. 备份范围

`backup.Backup(ctx, dataDir)` 生成一个 zip（写入 `<dataDir>/backups/`），包含：

| 内容 | 说明 |
|---|---|
| `lignting.db` | SQLite 一致性快照（`VACUUM INTO`），包含消息/任务/产出物/评审/决策/记忆/审计/文件变更时间线 |
| `artifacts/` | 产出物附件目录（递归，不含符号链接） |
| `config.json` | 脱敏后的配置（敏感键/凭据值替换为 `[REDACTED]`） |
| `manifest.json` | 版本、schema、创建时间、每个文件的大小/sha256、不可备份项 |

**不纳入备份（会写进 manifest.excluded 并告知用户）**：

- `secrets.json`：DeepSeek API Key 等凭据不入备份；恢复后需重新输入。
- `workspace/`：用户项目文件不属于产品数据，建议用 Git/外部备份。
- `logs/`、`backups/`、`exports/`、`chrome-profile*/`。
- `lignting.db-wal`、`lignting.db-shm`（快照已包含已提交数据，WAL 不是独立可恢复文件）。

这满足 AC-10 的「不可备份范围必须明确标注并告知用户」。

## 2. 一致性

- 备份通过 SQLite `VACUUM INTO` 生成快照，**不需要停止平台**；读取使用独立连接，不破坏运行中的数据库。
- 对 `busy/locked` 自动重试（最多 5 次）。
- zip 先写临时文件再原子改名；失败不会留下半成品。
- 返回 `BackupResult{Path, Size, SHA256, Manifest}`；每个文件也在 manifest 中有 `sha256`。

## 3. 恢复

```go
m, err := backup.Restore(ctx, backupPath, targetDataDir)
```

约束与步骤：

1. **先停止平台**，确保没有进程打开目标数据库（调用方负责；本包不覆盖运行中 DB）。
2. 目标目录必须不存在或为空；否则返回 `backup.ErrTargetNotEmpty`，绝不覆盖现有数据。
3. 先在目标目录同级的临时目录解压并校验：
   - manifest 格式/版本、schema 版本（`> store.SchemaVersion` 拒绝）；
   - 每个文件的 size + sha256；
   - zip 条目清单必须与 manifest 完全一致（防夹带）；
   - 路径必须是相对路径、无 `..`/盘符（防 zip-slip）；
   - 对恢复后的数据库执行 `PRAGMA integrity_check` 与 `VerifyAuditChain`。
4. 校验通过后才把临时目录 rename 到目标目录；失败删除临时目录，保留目标原状。
5. 恢复后自动把 `config.json` 的 `data_dir` 改写为目标目录、`workspace_dir` 改为 `<target>/workspace`。
6. 重新启动平台，按需重新输入 DeepSeek API Key（`secrets.json` 不在备份中）。
7. 索引可重建（见平台启动/维护流程）；恢复后建议先跑一次消息/任务/审计抽样。

返回的 manifest 可用于展示备份来源、schema 和校验信息。

## 4. 错误类型

- `ErrInvalidDataDir`：数据目录无效。
- `ErrNotFound`：数据库或备份不存在。
- `ErrCorrupt`：坏包、缺文件、sha256 不符、audit 链断、数据库 integrity 失败。
- `ErrVersionMismatch`：manifest/schema 版本高于当前程序支持版本。
- `ErrTargetNotEmpty`：恢复目标非空。

## 5. 保留策略

`backup.PlanRetention(dataDir, opts)` 只读：按 `Keep`（默认 7）保留最新备份，返回 `keep` / `delete-candidate` 计划；**不会删除任何文件**。清理动作由平台在人工确认后执行（v0.0.1 不自动删）。

## 6. 限制（v0.0.1）

- 备份未加密（明文 zip）；如需加密请放在磁盘/容器层解决，后续版本再评估。
- `workspace/` 不备份（见上），项目文件请用 Git。
- 恢复只能到空目录；不支持原地覆盖运行中的数据库。
- 大附件/大库会增加备份时间；当前未做增量/分片。

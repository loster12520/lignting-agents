# 运维与数据（R8）

| 文档 | 内容 |
|---|---|
| [备份与恢复](backup-restore.md) | 备份范围、manifest、VACUUM INTO 快照、恢复校验/回滚、不可备份项 |
| [导出与导入](export-import.md) | JSON/Markdown 导出、默认脱敏、导入冲突策略、版本校验 |

实现包：`internal/backup`；测试：`tests/backup`。备份/恢复/导出/导入均不写 HTTP，由 platform 接线并做权限/审计。

## 备份范围（摘要）

`backup.Backup(ctx, dataDir)` 写入 `<dataDir>/backups/*.zip`，包含：

- `lignting.db`：SQLite 一致性快照（`VACUUM INTO`，运行中可备份）；
- `artifacts/`：产出物附件（递归；跳过符号链接）；
- `config.json`：脱敏后的配置；
- `manifest.json`：版本/schema/时间/总大小/每个文件 sha256/排除项。

不包含：`secrets.json`（恢复后重输 DeepSeek Key）、`workspace/`（用户项目文件用 Git）、`logs/`、`backups/`、`exports/`、`chrome-profile*/`、`lignting.db-wal`、`lignting.db-shm`。这些会写入 manifest.excluded，满足 AC-10「明确标注不可备份范围」。

## 恢复步骤（摘要）

1. **停止平台**，确保没有进程打开目标数据目录的数据库。
2. 目标目录必须不存在或为空；否则返回 `ErrTargetNotEmpty`，不会覆盖现有数据。
3. 调用 `backup.Restore(ctx, backupPath, targetDataDir)`：解压到临时目录 → 校验 manifest/schema/逐文件 sha256/zip-slip/清单完整性 → `PRAGMA integrity_check` → 审计 hash 链校验 → 原子 rename 到目标目录；失败删除临时目录，不碰目标。
4. 恢复后 `config.json` 的 `data_dir`/`workspace_dir` 自动改写为目标路径；重新启动平台、重输 DeepSeek API Key（如需要）、重建索引。

## 关键限制

- 脱敏导出的 `audit_log` 导入时会被跳过（detail 改写后 hash 链无法重算）；迁移请用 `IncludeSensitive: true`。
- 保留策略只读计划（`PlanRetention`），不自动删除备份。
- 备份未加密；恢复只能到空目录，不支持原地覆盖运行中的数据库。

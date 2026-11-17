# 检索基准（10 万消息，AC-06/NFR-PERF-03）

## 环境与方法

- 命令：`go test -tags=perf ./tests/bench -run TestSearchPerf100k -v -timeout 60m`
- 种子：`tests/bench/seed.go` 批量写入 100,000 条消息（中文/英文/代码标识符混合，4 部门 × 20 会话），
  与生产同一 `search.Indexer` 在写入事务内同步倒排；为基准速度省略事件/审计（索引/消息表与生产一致）。
- 查询：`internal/search.Search`（与 HTTP /api/search 同一函数），每 query 预热 5 次后计时 60 次，
  报告 p50/p95；阈值：中文 2 字 p95 ≤150ms，普通 ≤300ms；写入后 ≤100ms 可搜；Recall@10 ≥0.8。
- 重建：`search.Rebuild` 全量重建消息索引，校验文档数与重建后可搜。

## 结果

运行后把控制台 `PERF ...` / `WRITE->SEARCH` / `RECALL@10` / `REBUILD` 行填到这里（由
`go test -tags=perf ...` 自动打印）。

## 已知限制

- 种子省略 audit_log/events 写入；生产写入还包括审计哈希链与 SSE 事件（单条消息额外 ~0.5–1ms）。
- 仅覆盖消息检索；任务/决策/产出物检索需后续索引更多 doc kind。
- SQLite 为本地文件，基准受磁盘影响；CI 机器与开发机数值可能不同。

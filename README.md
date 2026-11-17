# Lignting Agents

本地优先的 AI 组织工作台。v0.0.1：单用户、单二进制、DeepSeek-only。

## 快速开始

需要 Go 1.27+（或用发布二进制，无需 Go）。没有 Go 时：`mise use -g go@1.27.4`。

~~~powershell
# 开发模式：直接从源码启动
go run ./cmd/lignting --data-dir ./.lignting-data

# 或构建单二进制
go build -o bin/lignting.exe ./cmd/lignting
./bin/lignting.exe --data-dir ./.lignting-data --addr 127.0.0.1:8787
~~~

Linux/macOS：

~~~bash
go build -o bin/lignting ./cmd/lignting
./bin/lignting --data-dir ./.lignting-data --addr 127.0.0.1:8787
./scripts/smoke.sh   # 发布冒烟：health → setup → 建会话 → 发消息 → 检索 → 审计 → SSE
~~~

打开 http://127.0.0.1:8787 ，按向导完成 owner 初始化；在设置页填入 DeepSeek API Key（或使用 `--mock-model` 做演示）。

## 文档

- [项目目标](doc/target.md)
- [v0.0.1 需求](<doc/0.0.1 最初版/requirements.md>)
- [实施方案](<doc/0.0.1 最初版/implementation-plan.md>)
- [API 契约](<doc/0.0.1 最初版/api-contract.md>)
- [发布说明](<doc/0.0.1 最初版/release-notes.md>) · [发布检查清单](<doc/0.0.1 最初版/release-checklist.md>) · [P1 清单](<doc/0.0.1 最初版/p1-backlog.md>) · [收尾状态](<doc/0.0.1 最初版/release-status.md>)
- [用户文档](docs/user/quickstart.md) · [运维文档](docs/ops/README.md) · [测试文档](docs/testing/ac-guide.md)
- [CHANGELOG](CHANGELOG.md)

## 约束

- 只接 DeepSeek 官方 API，默认 deepseek-flash。
- Agent 只有 5 个工具：read_file / write_file / bash / web_search / view_change_history。
- 所有 Agent 共用 WriteTool；默认全局串行。
- v0.0.1 单用户、单项目、loopback。

## 验收

~~~bash
go test ./...
go test -tags=acceptance ./tests/ac
go test ./tests/verify/e2e
go test -tags=perf ./tests/bench -run TestSearchPerf100k
./scripts/smoke.sh
~~~

10 万消息检索基准的实测数据见 [docs/ops/search-benchmark.md](docs/ops/search-benchmark.md)。

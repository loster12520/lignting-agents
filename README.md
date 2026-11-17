# Lignting Agents

本地优先的 AI 组织工作台。v0.0.1：单用户、单二进制、DeepSeek-only。

## 开发

需要 Go 1.26+（本机已安装 Go 1.27.4，PATH 中可直接使用 go）。

~~~powershell
go run ./cmd/lignting --data-dir ./.lignting-data
~~~

打开 http://127.0.0.1:8787 。

## 文档

- [项目目标](doc/target.md)
- [v0.0.1 需求](<doc/0.0.1 最初版/requirements.md>)
- [实施方案](<doc/0.0.1 最初版/implementation-plan.md>)
- [API 契约](<doc/0.0.1 最初版/api-contract.md>)

## 约束

- 只接 DeepSeek 官方 API，默认 deepseek-flash。
- Agent 只有 5 个工具：read_file / write_file / bash / web_search / view_change_history。
- 所有 Agent 共用 WriteTool；默认全局串行。
- v0.0.1 单用户、单项目、loopback。

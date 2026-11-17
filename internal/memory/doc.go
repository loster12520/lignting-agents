// Package memory 实现 lignting-agents 的四层记忆（requirements FR-MEM-01..12）。
//
// 四层边界：
//   - 短期上下文：由 core 的会话投影与压缩负责，本包只接收注入请求中的“当前任务/近期消息”引用并计入预算。
//   - 触发式记忆：本包提供候选提交、额度、去重与单飞/冷却；事件到候选的抽取由 Extractor 端口完成。
//   - 档案记忆：memories 表的结构化实体，双时间 + 版本 + supersede + 墓碑删除 + 容量治理。
//   - 自适应提示词：prompt_manuals 表的分章节手册，版本/diff/回滚/审批状态。
//
// 设计约束（与 .research/memory-design.md 对应）：
//   - 只依赖 domain 与标准库；存储、模型、时钟全部通过窄接口注入（ports.go），便于纯逻辑单测。
//   - 写路径永远先校验后落库；压缩失败恢复原文；错误一律映射到 domain.Error 稳定 code。
//   - 注入默认稀疏：先摘要 + 取数路径（memory.read 工具），越权读取拒绝并留痕。
package memory

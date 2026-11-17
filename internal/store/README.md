# internal/store — 稳定接口（R1 冻结）

依赖方向：`platform/core/... -> store -> domain`。store 只做数据访问与事务，
业务规则（状态机、权限、DoR、路由）不在这里。

## 约定

- 时间：`store.Now()` = RFC3339 UTC + 固定 9 位小数秒，**字符串排序等于时间排序**。
- 事务：所有事务是 `BEGIN IMMEDIATE`，写操作全局串行；消息/事件 seq 单调。
- 先写库再发事件：消息、事件、索引、审计在同一事务提交；提交后 `SetEventHook` 唤醒 SSE。
- 幂等：`InsertMessage` 用 `messages.meta.client_id` 做幂等；重复提交返回同一消息。
- 审计：`sha256(prev_hash + canonical)`，`audit_log` 只有 INSERT。
- 错误：`ErrNotFound` / `ErrConflict` / `ErrInvalid`，由 platform 映射 HTTP 码。

## 稳定方法（改名必须先通知 core-dev / memory-dev / tools-dev / lead）

### Owner / Settings
- `GetOwner(ctx) (*Owner, error)`（未初始化返回 nil）
- `CreateOwner(ctx, name, password) (*Owner, error)`
- `UpdateOwnerName(ctx, name) error` / `SetOwnerPassword(ctx, password) error`
- `Setting(ctx, key) (string, bool, error)`
- `SettingsWithPrefix(ctx, prefix) (Settings, error)`
- `SetSetting / SetSettings(ctx, map) error`
- `GetReadSeq(ctx, conversationID) (int64, error)` / `SetReadSeq(ctx, conversationID, seq) error`
  - v0.0.1 临时：已读位置存 `settings["read:<conversation_id>"]`；后续迁移到 conversation_reads 表。

### Agent / Department / Task（core 只读为主）
- `CreateAgent(ctx, domain.Agent) (domain.Agent, error)`
- `GetAgent(ctx, id) (domain.Agent, error)`
- `ListAgents(ctx, AgentFilter) ([]domain.Agent, error)`
- `ListAgentsByDepartment(ctx, departmentID) ([]domain.Agent, error)`
- `UpdateAgent(ctx, domain.Agent) error`
- `UpdateAgentStatus(ctx, id, status) error`（reason 由 core 写进 agent.status 事件）
- `SetAgentTokenHash / FindAgentByTokenHash`
- `CreateDepartment / GetDepartment / ListDepartments / UpdateDepartment / UpdateDepartmentStatus`
- `GetTask(ctx, id) (domain.Task, error)`

### Conversation
- `CreateConversation(ctx, domain.Conversation, members, Actor) (domain.Conversation, error)`
- `CreateConversationTx(ctx, *sql.Tx, ...)`（setup/provisioning 原子写）
- `GetConversation(ctx, id)`
- `ListConversations(ctx, ConversationFilter) ([]domain.Conversation, error)`
- `UpdateConversation(ctx, domain.Conversation)`
- `ListConversationMembers / AddConversationMember / RemoveConversationMember / CountConversationMembers`

### Message / Event
- `InsertMessage(ctx, NewMessage) (*domain.Message, *domain.Event, bool, error)`
  - 平台写用户消息：client_id 幂等 + 消息 + 事件 + 索引 + 审计；返回 created=false 时 event 为 nil。
- `AppendMessage(ctx, domain.Message) (domain.Message, error)`（core 便捷入口，自动分配 id/seq/时间）
- `AppendMessageTx(ctx, *sql.Tx, domain.Message) (domain.Message, error)`
- `GetMessage / ListMessages / RecentMessages / MaxMessageSeq / CountMessages / CountUnread / LastMessage / MessageSeqsByIDs`
- `EditMessage / DeleteMessage(ctx, id, actor) (*domain.Message, *domain.Event, error)`
- `AppendEvent(ctx, domain.Event) (domain.Event, error)`（任意事件类型；写后唤醒 SSE）
- `AppendEventTx(ctx, *sql.Tx, domain.Event)`
- `ListEventsSince(ctx, since, conversationID, limit)` / `MaxEventSeq`

### Audit / Approval / Usage / Turn
- `AppendAudit(ctx, domain.AuditEntry) (domain.AuditEntry, error)`
- `AppendAuditTx(ctx, *sql.Tx, domain.AuditEntry)`
- `QueryAudit(ctx, AuditFilter) / VerifyAuditChain(ctx) (int64, error)`
- `RecordAudit(ctx, actorType, actorID, action, objectType, objectID, result, detail) error`
- `CreateApproval / GetApproval / ListApprovals / DecideApproval`
- `InsertUsage / QueryUsage`
- `CreateTurn / FinishTurn / GetTurn / MarkInterruptedTurns / InsertContextSnapshot`
- `FinishTurnWithEvents(ctx, turnID, status, errText string, events []domain.Event, audit domain.AuditEntry) error`（可选原子版：turn + 事件 + 审计同事务）
- `AppendReply(ctx, msg, events, audit) (domain.Message, error)`（core 原子回复：消息 + 索引 + 事件 + 审计）
- `ListAgentsByKind(ctx, kind)` / `SaveIntent(ctx, content)` / `GetIntent(ctx)`

### 写工具 / 时间线（tools-dev 端口）
- `InsertWriteEvent / UpdateWriteEvent / GetWriteEvent / ListWriteEventsByStatus`
- `AppendTimeline / TrimTimeline(ctx, path, keep) (int, error) / ListTimeline(...)`
- `TimelineHasEvent(ctx, writeEventID) (bool, error)`

### 事务
- `WithTx(ctx, func(tx *sql.Tx) error) error`：core 组合 `AppendMessageTx + AppendEventTx + AppendAuditTx`
  原子写模型回复；提交后自动唤醒 SSE。
- `Begin(ctx)`：需要手工控制事务时使用（仍受同一个 store 连接池约束）。

## 索引钩子
- `SetIndexer(DocIndexer)`：platform 启动时注入 `search.NewIndexer()`，消息写入事务内同步索引。
- `SetEventHook(func())`：platform 启动时注入 SSE broker 唤醒器。

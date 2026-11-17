package domain

// 记忆仓储的共享数据类型（R7）。
// 放在 domain 是为了让 internal/store 实现 memory.Repo 时不需要 import internal/memory，
// 同时 internal/memory 用类型别名保持原有 API 不变。

type MemoryFilter struct {
	Scope        string
	DepartmentID string
	AgentID      string
	EntityType   string
	EntityKey    string
	Status       []string
	Tags         []string
	IDs          []string

	// CreatedBy/CreatedSince/CreatedBefore 用于额度统计与按时间浏览（RFC3339）。
	CreatedBy     string
	CreatedSince  string
	CreatedBefore string

	IncludeDeleted bool
	IncludeExpired bool

	Limit  int
	Offset int
}

// MemoryEvent 是记忆审计事件（memory_events 表）。压缩快照也复用此表（action=snapshot）。
type MemoryEvent struct {
	MemoryID  string
	Action    string
	ActorType string
	ActorID   string
	Detail    string
	CreatedAt string
}

// MemoryRead 是读取留痕（memory_reads 表，FR-MEM-10）。
type MemoryRead struct {
	MemoryID   string
	ReaderType string
	ReaderID   string
	TurnID     string
	CreatedAt  string
}

// MemoryManualKey 定位某本手册（scope + 部门/Agent）。
// memory 包保留自己的 ManualKey（带 String/validate），Repo 边界用本类型，
// 这样 store 实现 memory.Repo 时不需要 import memory。
type MemoryManualKey struct {
	Scope        string
	DepartmentID string
	AgentID      string
}

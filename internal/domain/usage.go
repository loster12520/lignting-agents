package domain

// 用量聚合的共享类型（R8）。放在 domain 是为了让 store 实现 core.UsageSumer
// 时不需要 import internal/core（避免反向依赖）。

type UsageQuery struct {
	AgentID      string
	TaskID       string
	DepartmentID string
	From         string // RFC3339；空表示不限
	To           string
}

type UsageTotals struct {
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	Cost             float64
}

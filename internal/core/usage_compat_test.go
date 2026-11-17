//go:build usage_compat

// 编译断言：*store.Store 必须满足 core.UsageSumer（SumUsage 用 domain 类型）。
package core_test

import (
	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/store"
)

var _ core.UsageSumer = (*store.Store)(nil)

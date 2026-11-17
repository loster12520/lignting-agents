//go:build work_compat

// 编译断言：*store.Store 必须满足 R4 的 core.WorkStore。
// 运行：go test -tags=work_compat ./internal/core
package core_test

import (
	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/store"
)

var _ core.WorkStore = (*store.Store)(nil)

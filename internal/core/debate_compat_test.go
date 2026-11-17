//go:build debate_compat

// 编译断言：*store.Store 必须满足 R6 的 core.DebateStore。
// 运行：go test -tags=debate_compat ./internal/core
package core_test

import (
	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/store"
)

var _ core.DebateStore = (*store.Store)(nil)

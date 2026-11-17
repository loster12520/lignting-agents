//go:build store_compat

// 编译断言：*store.Store 必须满足 core 的全部窄接口。
// 默认不参与构建（store 方法补齐后运行 go test -tags=store_compat ./internal/core）。
package core_test

import (
	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/store"
)

var (
	_ core.Store            = (*store.Store)(nil)
	_ core.AtomicReplyStore = (*store.Store)(nil)
)

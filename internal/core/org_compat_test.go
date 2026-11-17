//go:build org_compat

// 编译断言：*store.Store 必须满足 R3 的 core.OrgStore。
// 待 platform-dev 加上 blueprint/provisioning 方法后运行：
//
//	go test -tags=org_compat ./internal/core
package core_test

import (
	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/store"
)

var _ core.OrgStore = (*store.Store)(nil)

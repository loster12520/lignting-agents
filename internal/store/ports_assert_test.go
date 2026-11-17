//go:build portsmoke

// 端口一致性编译断言：core / write 的窄接口必须由 *store.Store 结构化满足。
// 运行：go test -tags=portsmoke ./internal/store
package store_test

import (
	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/internal/write"
)

var _ core.Store = (*store.Store)(nil)
var _ core.AtomicReplyStore = (*store.Store)(nil)
var _ core.TurnStore = (*store.Store)(nil)
var _ write.Repository = (*store.Store)(nil)
var _ write.AuditSink = (*store.Store)(nil)

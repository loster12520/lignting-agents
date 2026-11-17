package main

import (
	"github.com/lignting/lignting-agents/internal/core"
	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/internal/write"
)

// 编译期检查：store 必须结构化满足 core / write 的端口。
// 这些断言故意放在 cmd，任何接口漂移都会在 go build 时立刻暴露。
var (
	_ core.Store       = (*store.Store)(nil)
	_ write.Repository = (*store.Store)(nil)
)

package main

import (
	"fmt"

	"github.com/lignting/lignting-agents/internal/memory"
)

func main() {
	zero := memory.Options{}.Config
	fmt.Printf("zeroNormalizeEnabled=%v\n", zero.Normalize().Enabled)
	fmt.Printf("defaultConfigEnabled=%v\n", memory.DefaultConfig().Enabled)
}

package domain

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// NewID 生成可读前缀 + 时间 + 随机数，便于日志与排序。
func NewID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + time.Now().UTC().Format("20060102150405") + "_" + hex.EncodeToString(b[:])
}

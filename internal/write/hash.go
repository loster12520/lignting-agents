package write

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
)

// HashBytes 返回小写十六进制 sha256。
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func HashString(s string) string { return HashBytes([]byte(s)) }

// HashFile 流式计算文件 hash。
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func NormalizeHash(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

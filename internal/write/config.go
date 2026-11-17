package write

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

// FormatTime 使用固定 9 位小数，保证 SQLite/字符串排序与时间顺序一致。
func FormatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

const (
	DefaultTimelineLimit = 30
	DefaultMaxFileBytes  = 2 << 20 // 2 MiB
	DefaultLockTimeout   = 30 * time.Second
	DefaultDiffMaxLines  = 2000

	// StatusUnmanaged 标记不是通过 write_file 产生的文件变更（例如 bash 绕过）。
	StatusUnmanaged = "unmanaged"
)

// Options 是共享写工具的构造参数。
type Options struct {
	Root           string
	TimelineLimit  int
	ParallelByFile bool
	MaxFileBytes   int64
	LockTimeout    time.Duration
	DiffMaxLines   int
	Now            func() time.Time
	NewID          func(prefix string) string
	Repository     Repository
	Audit          AuditSink
	Branch         BranchGate
}

func (o *Options) applyDefaults() error {
	if o.Root == "" {
		return errors.New("write: root is required")
	}
	abs, err := filepath.Abs(o.Root)
	if err != nil {
		return err
	}
	o.Root = abs
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	if o.TimelineLimit <= 0 {
		o.TimelineLimit = DefaultTimelineLimit
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = DefaultMaxFileBytes
	}
	if o.LockTimeout <= 0 {
		o.LockTimeout = DefaultLockTimeout
	}
	if o.DiffMaxLines <= 0 {
		o.DiffMaxLines = DefaultDiffMaxLines
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NewID == nil {
		o.NewID = domain.NewID
	}
	if o.Repository == nil {
		return errors.New("write: repository is required")
	}
	if o.Audit == nil {
		return errors.New("write: audit sink is required")
	}
	return nil
}

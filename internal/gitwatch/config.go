package gitwatch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
)

var (
	ErrNotRepository   = errors.New("gitwatch: not a git repository")
	ErrBranchSwitching = errors.New("gitwatch: git branch is switching")
)

const (
	DefaultPollInterval = 2 * time.Second
	DefaultGuardWindow  = 2 * time.Second
)

// Options 是 git 轮询器的构造参数。
type Options struct {
	Dir          string
	PollInterval time.Duration
	GuardWindow  time.Duration
	GitPath      string
	Now          func() time.Time
	NewID        func(prefix string) string
	Repository   Repository
	OnError      func(error)
	OnEvent      func(Event)
}

func (o *Options) applyDefaults() error {
	if o.Dir == "" {
		return errors.New("gitwatch: dir is required")
	}
	abs, err := filepath.Abs(o.Dir)
	if err != nil {
		return err
	}
	o.Dir = abs
	if o.PollInterval <= 0 {
		o.PollInterval = DefaultPollInterval
	}
	if o.PollInterval < 10*time.Millisecond {
		o.PollInterval = 10 * time.Millisecond
	}
	if o.GuardWindow <= 0 {
		o.GuardWindow = DefaultGuardWindow
	}
	if o.GitPath == "" {
		o.GitPath = "git"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NewID == nil {
		o.NewID = domain.NewID
	}
	if o.Repository == nil {
		return errors.New("gitwatch: repository is required")
	}
	return nil
}

// FormatTime 与 write 包一致：固定 9 位小数，保证字符串排序正确。
func FormatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func isRepository(ctx context.Context, git, dir string) bool {
	cmd := exec.CommandContext(ctx, git, "-C", dir, "rev-parse", "--is-inside-work-tree")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

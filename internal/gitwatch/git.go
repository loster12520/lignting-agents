package gitwatch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type commitInfo struct {
	SHA     string
	Unix    int64
	Subject string
}

func (w *Watcher) runGit(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"-C", w.dir, "--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, w.git, full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, text)
	}
	return text, nil
}

func (w *Watcher) currentBranch(ctx context.Context) (string, error) {
	out, err := w.runGit(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err == nil && out != "" {
		return out, nil
	}
	// 空仓库还没有 HEAD，用 symbolic-ref 拿到初始分支名。
	if sym, symErr := w.runGit(ctx, "symbolic-ref", "--short", "HEAD"); symErr == nil && sym != "" {
		return sym, nil
	}
	if err != nil {
		return "main", nil
	}
	if out == "" {
		out = "HEAD"
	}
	return out, nil
}

func (w *Watcher) headCommit(ctx context.Context) (commitInfo, error) {
	sha, err := w.runGit(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		// 空仓库（尚无 commit）不是致命错误；首次提交后下一轮会处理。
		return commitInfo{}, nil
	}
	if sha == "" {
		return commitInfo{}, nil
	}
	out, err := w.runGit(ctx, "log", "-1", "--format=%ct%x1f%s", "HEAD")
	if err != nil {
		return commitInfo{}, err
	}
	parts := strings.SplitN(out, "\x1f", 2)
	if len(parts) != 2 {
		return commitInfo{SHA: sha}, nil
	}
	unix, _ := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	return commitInfo{SHA: sha, Unix: unix, Subject: parts[1]}, nil
}

func (w *Watcher) commitsBetween(ctx context.Context, from, to string) ([]commitInfo, error) {
	if from == "" {
		info, err := w.headCommit(ctx)
		if err != nil {
			return nil, err
		}
		return []commitInfo{info}, nil
	}
	out, err := w.runGit(ctx, "log", "--reverse", "--format=%H%x1f%ct%x1f%s", from+".."+to)
	if err != nil && out == "" {
		// force push/rewrite 时旧 commit 可能不可达，退化为当前 HEAD。
		info, headErr := w.headCommit(ctx)
		if headErr != nil {
			return nil, err
		}
		if info.SHA == "" {
			return nil, err
		}
		return []commitInfo{info}, nil
	}
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	commits := make([]commitInfo, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\x1f", 3)
		if len(parts) < 3 {
			continue
		}
		unix, _ := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		commits = append(commits, commitInfo{SHA: parts[0], Unix: unix, Subject: parts[2]})
	}
	return commits, nil
}

func parseEventTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

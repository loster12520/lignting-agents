package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/write"
)

type bashTool struct{ opts Options }

func (t *bashTool) Name() string { return "bash" }
func (t *bashTool) Description() string {
	return "Run a shell command inside the workspace with timeout, output truncation and path policy. Writes that bypass write_file are marked as unmanaged."
}
func (t *bashTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "shell command"},
			"timeout_ms": {"type": "integer", "description": "timeout in milliseconds (default 30000, max 300000)"},
			"workdir": {"type": "string", "description": "workspace-relative working directory"}
		},
		"required": ["command"],
		"additionalProperties": false
	}`)
}

type bashArgs struct {
	Command   string `json:"command"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
	Workdir   string `json:"workdir,omitempty"`
}

func (t *bashTool) Invoke(ctx context.Context, req Request) (Result, error) {
	var args bashArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return errorResult(req.CallID, t.Name(), "invalid args: "+err.Error()), nil
	}
	if strings.TrimSpace(args.Command) == "" {
		return errorResult(req.CallID, t.Name(), "command is required"), nil
	}
	if len(args.Command) > 8192 {
		return errorResult(req.CallID, t.Name(), "command too long (max 8192)"), nil
	}
	workdir := t.opts.Root
	if strings.TrimSpace(args.Workdir) != "" {
		abs, _, err := write.ResolveDir(t.opts.Root, args.Workdir)
		if err != nil {
			return errorResult(req.CallID, t.Name(), err.Error()), nil
		}
		workdir = abs
	}
	if err := checkCommandSafety(args.Command, t.opts.Root, workdir); err != nil {
		return errorResult(req.CallID, t.Name(), err.Error()), nil
	}

	timeout := t.opts.BashTimeout
	if args.TimeoutMS > 0 {
		timeout = time.Duration(args.TimeoutMS) * time.Millisecond
	}
	if timeout > t.opts.BashMaxTimeout {
		timeout = t.opts.BashMaxTimeout
	}
	if timeout < 50*time.Millisecond {
		timeout = 50 * time.Millisecond
	}

	targets := writeTargets(args.Command, t.opts.Root, workdir)
	before := snapshotFiles(t.opts.Root, targets, t.opts.WriteMaxBytes)

	shell, shellArgs := t.shellArgs(args.Command)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, shell, shellArgs...)
	cmd.Dir = workdir
	cmd.WaitDelay = 2 * time.Second
	stdout := &limitedBuffer{max: t.opts.OutputMaxBytes}
	stderr := &limitedBuffer{max: t.opts.OutputMaxBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)

	exitCode := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else if timedOut {
			exitCode = -1
		} else {
			return errorResult(req.CallID, t.Name(), "start command: "+runErr.Error()), nil
		}
	}

	unmanaged, warnings := t.detectUnmanaged(ctx, req, targets, before, args.Command)
	var b strings.Builder
	fmt.Fprintf(&b, "exit_code=%d timed_out=%t duration_ms=%d\n", exitCode, timedOut, duration.Milliseconds())
	if s := stdout.String(); s != "" {
		fmt.Fprintf(&b, "stdout:\n%s\n", s)
	}
	if s := stderr.String(); s != "" {
		fmt.Fprintf(&b, "stderr:\n%s\n", s)
	}
	for _, w := range warnings {
		fmt.Fprintf(&b, "[warn] %s\n", w)
	}
	for _, u := range unmanaged {
		fmt.Fprintf(&b, "[unmanaged] %s event=%s status=%s\n", u["path"], u["event_id"], u["status"])
	}
	meta := map[string]any{
		"exit_code": exitCode, "timed_out": timedOut, "duration_ms": duration.Milliseconds(),
		"stdout": stdout.String(), "stderr": stderr.String(),
		"unmanaged": unmanaged, "warnings": warnings,
	}
	return Result{CallID: req.CallID, Name: t.Name(), Content: strings.TrimRight(b.String(), "\n"), Meta: meta}, nil
}

func (t *bashTool) shellArgs(command string) (string, []string) {
	if t.opts.Shell != "" {
		args := t.opts.ShellArgs
		if len(args) == 0 {
			args = []string{"-c"}
		}
		return t.opts.Shell, append(append([]string(nil), args...), command)
	}
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/C", command}
	}
	if p, err := exec.LookPath("bash"); err == nil {
		return p, []string{"-lc", command}
	}
	return "sh", []string{"-lc", command}
}

func (t *bashTool) detectUnmanaged(ctx context.Context, req Request, targets []string, before map[string]fileSnap, command string) ([]map[string]any, []string) {
	unmanaged := make([]map[string]any, 0)
	warnings := make([]string, 0)
	if len(targets) == 0 {
		return unmanaged, warnings
	}
	after := snapshotFiles(t.opts.Root, targets, t.opts.WriteMaxBytes)
	for _, rel := range targets {
		old, okOld := before[rel]
		now, okNow := after[rel]
		if !okOld || !okNow {
			continue
		}
		if old.Exists == now.Exists && old.Hash == now.Hash {
			continue
		}
		if old.TooLarge || now.TooLarge {
			warnings = append(warnings, "unmanaged change too large to diff: "+rel)
			continue
		}
		if t.opts.Writer == nil {
			warnings = append(warnings, "unmanaged change not recorded: writer unavailable: "+rel)
			continue
		}
		reason := "bash bypass: " + truncate(command, 300)
		res, err := t.opts.Writer.RecordUnmanaged(ctx, write.UnmanagedRequest{
			ActorType: "tool", ActorID: req.Actor.ID, TaskID: req.TaskID, Path: rel,
			Reason: reason, Before: old.Content, BeforeExists: old.Exists,
			After: now.Content, AfterExists: now.Exists,
		})
		if err != nil {
			warnings = append(warnings, "record unmanaged failed: "+err.Error())
			continue
		}
		unmanaged = append(unmanaged, map[string]any{
			"path": rel, "event_id": res.Event.ID, "status": res.Event.Status,
		})
	}
	return unmanaged, warnings
}

type fileSnap struct {
	Rel      string
	Exists   bool
	Content  string
	Hash     string
	TooLarge bool
}

func snapshotFiles(root string, targets []string, max int64) map[string]fileSnap {
	out := make(map[string]fileSnap, len(targets))
	for _, rel := range targets {
		abs, cleanRel, err := write.ResolvePath(root, rel)
		if err != nil {
			continue
		}
		snap := fileSnap{Rel: cleanRel}
		fi, err := os.Lstat(abs)
		if errors.Is(err, os.ErrNotExist) {
			out[cleanRel] = snap
			continue
		}
		if err != nil || fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		snap.Exists = true
		if fi.Size() > max {
			snap.TooLarge = true
			if h, err := write.HashFile(abs); err == nil {
				snap.Hash = h
			}
		} else {
			bytes, err := os.ReadFile(abs)
			if err == nil {
				snap.Content = string(bytes)
				snap.Hash = write.HashString(snap.Content)
				if isBinary(snap.Content) {
					snap.Content = ""
					snap.TooLarge = true
				}
			}
		}
		out[cleanRel] = snap
	}
	return out
}

func checkCommandSafety(command, root, workdir string) error {
	lower := strings.ToLower(command)
	denied := []string{
		"rm -rf /", "rm -fr /", "rm -rf /*", "format ", "diskpart", "mkfs",
		"shutdown", "reboot", "poweroff", "sudo ", "su -", "dd if=",
		":(){", "chmod -r 777 /", "> /dev/sda", "curl | sh", "wget | sh",
	}
	for _, p := range denied {
		if strings.Contains(lower, p) {
			return fmt.Errorf("command rejected by policy: %q", p)
		}
	}
	for _, token := range strings.Fields(command) {
		token = strings.Trim(token, "\"'")
		token = strings.TrimLeft(token, "<>123&")
		token = strings.TrimRight(token, ";|&")
		if token == "" || token == "NUL" || token == "/dev/null" || strings.Contains(token, "://") {
			continue
		}
		if strings.HasPrefix(token, "-") || strings.HasPrefix(token, "$") || strings.HasPrefix(token, "%") {
			continue
		}
		if !filepath.IsAbs(token) && !strings.Contains(token, "..") && !strings.ContainsAny(token, "/\\") {
			continue
		}
		candidate := token
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(workdir, candidate)
		}
		candidate = filepath.Clean(candidate)
		if !write.IsWithin(root, candidate) {
			return fmt.Errorf("path outside workspace rejected: %s", token)
		}
	}
	return nil
}

func writeTargets(command, root, workdir string) []string {
	tokens := strings.Fields(command)
	seen := make(map[string]bool)
	out := make([]string, 0)
	add := func(tok string) {
		tok = strings.Trim(tok, "\"'")
		tok = strings.TrimRight(tok, ";|&")
		if tok == "" || tok == "NUL" || tok == "/dev/null" || strings.HasPrefix(tok, "&") || strings.HasPrefix(tok, "$") || strings.HasPrefix(tok, "%") {
			return
		}
		candidate := tok
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(workdir, candidate)
		}
		abs, rel, err := write.ResolvePath(root, candidate)
		_ = abs
		if err != nil || seen[rel] {
			return
		}
		seen[rel] = true
		out = append(out, rel)
	}
	expectRedirect := false
	cmdMode := ""
	mvCp := false
	mvCpArgs := make([]string, 0, 2)
	for _, tok := range tokens {
		t := strings.Trim(tok, "\"'")
		if expectRedirect {
			add(t)
			expectRedirect = false
			continue
		}
		if t == ">" || t == ">>" || t == "1>" || t == "2>" || t == "&>" || t == ">|" {
			expectRedirect = true
			continue
		}
		if strings.HasPrefix(t, ">") && len(t) > 1 {
			add(t[1:])
			continue
		}
		if strings.HasSuffix(t, ">") && len(t) > 1 {
			expectRedirect = true
			continue
		}
		if i := strings.Index(t, ">"); i > 0 {
			target := t[i+1:]
			if target == "" {
				expectRedirect = true
			} else {
				add(target)
			}
			continue
		}
		switch t {
		case "tee", "touch", "rm", "mkdir", "rmdir", "truncate", "chmod", "chown":
			cmdMode = t
			continue
		case "mv", "cp":
			mvCp = true
			mvCpArgs = mvCpArgs[:0]
			continue
		case ";", "&&", "||", "|":
			cmdMode = ""
			mvCp = false
			continue
		}
		if strings.HasPrefix(t, "-") {
			continue
		}
		if mvCp {
			mvCpArgs = append(mvCpArgs, t)
			continue
		}
		if cmdMode != "" {
			add(t)
		}
	}
	if mvCp && len(mvCpArgs) > 0 {
		add(mvCpArgs[len(mvCpArgs)-1])
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

type limitedBuffer struct {
	max   int
	head  []byte
	tail  []byte
	total int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.total += n
	half := b.max / 2
	if half <= 0 {
		return n, nil
	}
	if len(b.head) < half {
		take := half - len(b.head)
		if take > len(p) {
			take = len(p)
		}
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		b.tail = append(b.tail, p...)
		if len(b.tail) > half {
			b.tail = b.tail[len(b.tail)-half:]
		}
	}
	return n, nil
}

func (b *limitedBuffer) String() string {
	if b.total <= b.max {
		return string(append(append([]byte(nil), b.head...), b.tail...))
	}
	return string(b.head) + fmt.Sprintf("\n... [output truncated, %d bytes total] ...\n", b.total) + string(b.tail)
}

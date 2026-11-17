package fixtures

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FaultEnvPrefix is the environment-variable namespace for acceptance fault
// injection. Both mechanisms are test-only:
//   - <data-dir>/faults.json: explicit, inspectable, per-run switches.
//   - LIGNT_FAULT_*: overrides for a single process.
//
// Contract (approved by Lead):
//   - Fault injection is OFF by default. A production start MUST NOT fail or
//     warn when faults.json is missing; it simply runs with no injected faults.
//   - Only test/acceptance builds (or a test-only config path) read these
//     switches. Production code must not branch on them for correctness.
//   - The file is written with 0600 permissions and contains no secrets.
const FaultEnvPrefix = "LIGNT_FAULT_"

// Faults is the serializable fault-injection switch set. Zero value means
// "no fault injected".
type Faults struct {
	// Model faults (R2): OpenAI-compatible mock/deepseek client behavior.
	ModelTimeoutMS int  `json:"model_timeout_ms,omitempty"`
	ModelNoKey     bool `json:"model_no_key,omitempty"`
	ModelError     bool `json:"model_error,omitempty"`
	ModelRateLimit bool `json:"model_rate_limit,omitempty"`

	// Crash/recovery faults (R1/R8).
	KillBeforeCommit    bool `json:"kill_before_commit,omitempty"`
	KillAfterToolEffect bool `json:"kill_after_tool_effect,omitempty"`
	SSEDisconnectAfter  int  `json:"sse_disconnect_after,omitempty"`

	// Budget/attention faults (R8).
	BudgetForcePercent int `json:"budget_force_percent,omitempty"` // 0=off, 80, 100
	ApprovalTTLSeconds int `json:"approval_ttl_seconds,omitempty"`
	HumanSilenceHours  int `json:"human_silence_hours,omitempty"`

	// Permission/security faults (R8).
	DenyTool              string `json:"deny_tool,omitempty"` // e.g. "bash"
	ForceForbiddenRead    bool   `json:"force_forbidden_read,omitempty"`
	AllowBypassPermission bool   `json:"allow_bypass_permission,omitempty"`

	// Search/write/git faults (R1/R5).
	IndexStale      bool `json:"index_stale,omitempty"`
	WriteConflict   bool `json:"write_conflict,omitempty"`
	GitBranchChange bool `json:"git_branch_change,omitempty"`
	Concurrency     int  `json:"concurrency,omitempty"`
}

// DefaultFaults is the disabled switch set.
func DefaultFaults() Faults { return Faults{} }

// Enabled reports whether any switch is set.
func (f Faults) Enabled() bool {
	return f != (Faults{})
}

// FaultsPath returns <data-dir>/faults.json.
func FaultsPath(dataDir string) string { return filepath.Join(dataDir, "faults.json") }

// WriteFaults writes the switch file atomically. Tests call this before
// starting a harness; production never writes it.
func WriteFaults(dataDir string, f Faults) (string, error) {
	if dataDir == "" {
		return "", errors.New("fixtures: empty data dir")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", fmt.Errorf("fixtures: mkdir data dir: %w", err)
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", fmt.Errorf("fixtures: marshal faults: %w", err)
	}
	path := FaultsPath(dataDir)
	tmp, err := os.CreateTemp(dataDir, ".faults-*.tmp")
	if err != nil {
		return "", fmt.Errorf("fixtures: create temp faults: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(name, path); err != nil {
		return "", fmt.Errorf("fixtures: rename faults: %w", err)
	}
	return path, nil
}

// ReadFaults reads <data-dir>/faults.json. A missing file is not an error:
// it returns the zero switch set and found=false. Production start may call
// this safely (per the contract above) or ignore it entirely.
func ReadFaults(dataDir string) (Faults, bool, error) {
	var f Faults
	b, err := os.ReadFile(FaultsPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return f, false, nil
	}
	if err != nil {
		return f, false, fmt.Errorf("fixtures: read faults: %w", err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, true, fmt.Errorf("fixtures: parse faults: %w", err)
	}
	return f, true, nil
}

// EnvMap renders set switches as LIGNT_FAULT_* environment variables. Tests can
// pass them to a child process with cmd.Env = append(os.Environ(), pairs...).
func (f Faults) EnvMap() map[string]string {
	out := map[string]string{}
	setInt := func(name string, v int) {
		if v != 0 {
			out[FaultEnvPrefix+name] = fmt.Sprintf("%d", v)
		}
	}
	setBool := func(name string, v bool) {
		if v {
			out[FaultEnvPrefix+name] = "1"
		}
	}
	setInt("MODEL_TIMEOUT_MS", f.ModelTimeoutMS)
	setBool("MODEL_NO_KEY", f.ModelNoKey)
	setBool("MODEL_ERROR", f.ModelError)
	setBool("MODEL_RATE_LIMIT", f.ModelRateLimit)
	setBool("KILL_BEFORE_COMMIT", f.KillBeforeCommit)
	setBool("KILL_AFTER_TOOL_EFFECT", f.KillAfterToolEffect)
	setInt("SSE_DISCONNECT_AFTER", f.SSEDisconnectAfter)
	setInt("BUDGET_FORCE_PERCENT", f.BudgetForcePercent)
	setInt("APPROVAL_TTL_SECONDS", f.ApprovalTTLSeconds)
	setInt("HUMAN_SILENCE_HOURS", f.HumanSilenceHours)
	if f.DenyTool != "" {
		out[FaultEnvPrefix+"DENY_TOOL"] = f.DenyTool
	}
	setBool("FORCE_FORBIDDEN_READ", f.ForceForbiddenRead)
	setBool("ALLOW_BYPASS_PERMISSION", f.AllowBypassPermission)
	setBool("INDEX_STALE", f.IndexStale)
	setBool("WRITE_CONFLICT", f.WriteConflict)
	setBool("GIT_BRANCH_CHANGE", f.GitBranchChange)
	setInt("CONCURRENCY", f.Concurrency)
	return out
}

// EnvPairs flattens EnvMap into "K=V" strings for os/exec.
func (f Faults) EnvPairs() []string {
	m := f.EnvMap()
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// FaultEnv returns the env var name for a switch, e.g. FaultEnv("model_timeout_ms")
// -> LIGNT_FAULT_MODEL_TIMEOUT_MS.
func FaultEnv(name string) string { return FaultEnvPrefix + strings.ToUpper(name) }

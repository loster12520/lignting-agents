package domain

// TaskTransitions 是 v0.0.1 任务状态机的唯一定义（requirements 15.1）。
// escalated/at_risk/waived 是正交标记，不是状态。
var TaskTransitions = map[string][]string{
	"proposed":          {"ready", "canceled"},
	"ready":             {"discussing", "canceled"},
	"discussing":        {"in_progress", "changes_requested"},
	"in_progress":       {"in_review", "blocked", "canceled", "failed"},
	"blocked":           {"in_progress", "canceled"},
	"in_review":         {"approved", "changes_requested"},
	"changes_requested": {"in_review", "canceled"},
	"approved":          {"done"},
	"done":              {},
	"canceled":          {},
	"failed":            {},
}

var taskTerminal = map[string]bool{"done": true, "canceled": true, "failed": true}

func CanTransition(from, to string) bool {
	for _, t := range TaskTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

func AllowedTransitions(from string) []string {
	out := make([]string, len(TaskTransitions[from]))
	copy(out, TaskTransitions[from])
	return out
}

func IsTerminalTaskStatus(s string) bool { return taskTerminal[s] }

func IsTaskStatus(s string) bool {
	_, ok := TaskTransitions[s]
	return ok
}

package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lignting/lignting-agents/internal/write"
)

type historyTool struct{ opts Options }

func (t *historyTool) Name() string { return "view_change_history" }
func (t *historyTool) Description() string {
	return "Read the shared write timeline (per-file changes, agent, diff, task, branch, commit) for a file or branch."
}
func (t *historyTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "workspace-relative file path"},
			"branch": {"type": "string"},
			"commit": {"type": "string"},
			"agent_id": {"type": "string"},
			"since": {"type": "string", "description": "RFC3339 lower bound"},
			"limit": {"type": "integer", "description": "max entries (default 20, max 100)", "minimum": 1, "maximum": 100}
		},
		"additionalProperties": false
	}`)
}

type historyArgs struct {
	Path    string `json:"path,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Commit  string `json:"commit,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
	Since   string `json:"since,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

func (t *historyTool) Invoke(ctx context.Context, req Request) (Result, error) {
	if t.opts.History == nil {
		return errorResult(req.CallID, t.Name(), ErrUnavailable.Error()+": history"), nil
	}
	var args historyArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return errorResult(req.CallID, t.Name(), "invalid args: "+err.Error()), nil
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	entries, err := t.opts.History.Timeline(ctx, write.TimelineFilter{
		Path: strings.TrimSpace(args.Path), Branch: args.Branch, CommitSHA: args.Commit,
		AgentID: args.AgentID, Since: args.Since, Limit: limit,
	})
	if err != nil {
		return errorResult(req.CallID, t.Name(), "history failed: "+err.Error()), nil
	}
	type item struct {
		ID           string `json:"id"`
		Path         string `json:"path"`
		WriteEventID string `json:"write_event_id"`
		AgentID      string `json:"agent_id"`
		TaskID       string `json:"task_id"`
		Diff         string `json:"diff"`
		Reason       string `json:"reason"`
		Branch       string `json:"branch"`
		CommitSHA    string `json:"commit_sha"`
		Status       string `json:"status"`
		CreatedAt    string `json:"created_at"`
	}
	items := make([]item, 0, len(entries))
	for _, e := range entries {
		items = append(items, item{
			ID: e.ID, Path: e.Path, WriteEventID: e.WriteEventID, AgentID: e.AgentID,
			TaskID: e.TaskID, Diff: e.Diff, Reason: e.Reason, Branch: e.Branch,
			CommitSHA: e.CommitSHA, Status: e.Status, CreatedAt: e.CreatedAt,
		})
	}
	out, _ := json.MarshalIndent(map[string]any{"items": items, "count": len(items)}, "", "  ")
	return Result{
		CallID: req.CallID, Name: t.Name(), Content: string(out),
		Meta: map[string]any{"count": len(items), "path": args.Path},
	}, nil
}

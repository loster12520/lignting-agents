package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/write"
)

type writeFileTool struct{ opts Options }

func (t *writeFileTool) Name() string { return "write_file" }
func (t *writeFileTool) Description() string {
	return "Write a UTF-8 text file through the shared WriteTool. Requires base_hash (sha256 of current content; empty for a new file)."
}
func (t *writeFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Workspace-relative path"},
			"content": {"type": "string", "description": "Full new UTF-8 content"},
			"base_hash": {"type": "string", "description": "sha256 of current file; empty string for a new file"},
			"reason": {"type": "string", "description": "why this write is needed"},
			"task_id": {"type": "string", "description": "optional task id"}
		},
		"required": ["path", "content", "base_hash"],
		"additionalProperties": false
	}`)
}

type writeFileArgs struct {
	Path     string  `json:"path"`
	Content  string  `json:"content"`
	BaseHash *string `json:"base_hash"`
	Reason   string  `json:"reason,omitempty"`
	TaskID   string  `json:"task_id,omitempty"`
}

func (t *writeFileTool) Invoke(ctx context.Context, req Request) (Result, error) {
	if t.opts.Writer == nil {
		return errorResult(req.CallID, t.Name(), ErrUnavailable.Error()+": writer"), nil
	}
	var args writeFileArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return errorResult(req.CallID, t.Name(), "invalid args: "+err.Error()), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return errorResult(req.CallID, t.Name(), "path is required"), nil
	}
	if args.BaseHash == nil {
		return errorResult(req.CallID, t.Name(), "base_hash is required (empty string for a new file)"), nil
	}
	if int64(len(args.Content)) > t.opts.WriteMaxBytes {
		return errorResult(req.CallID, t.Name(), write.ErrTooLarge.Error()), nil
	}
	actorID := req.Actor.ID
	if actorID == "" {
		actorID = "tool"
	}
	res, err := t.opts.Writer.Apply(ctx, write.Request{
		ActorType: domain.SenderAgent, ActorID: actorID, TaskID: args.TaskID,
		Path: args.Path, Content: args.Content, BaseHash: *args.BaseHash,
		Reason: args.Reason,
	})
	if err != nil {
		var ce *write.ConflictError
		if errors.As(err, &ce) {
			msg := fmt.Sprintf("write conflict: %s (base_hash=%s, current_hash=%s)", ce.Path, ce.BaseHash, ce.CurrentHash)
			return errorResult(req.CallID, t.Name(), msg), nil
		}
		return errorResult(req.CallID, t.Name(), err.Error()), nil
	}
	diff := res.Diff
	if len(diff) > 16<<10 {
		diff = diff[:16<<10] + "\n...(diff truncated)"
	}
	content := fmt.Sprintf("written %s\nhash=%s event=%s created=%t\n%s", res.Path, res.Hash, res.Event.ID, res.Created, diff)
	return Result{
		CallID: req.CallID, Name: t.Name(), Content: content,
		Meta: map[string]any{
			"path": res.Path, "hash": res.Hash, "event_id": res.Event.ID,
			"created": res.Created, "diff": res.Diff, "status": res.Event.Status,
		},
	}, nil
}

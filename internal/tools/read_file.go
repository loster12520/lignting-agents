package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lignting/lignting-agents/internal/write"
)

type readFileTool struct{ opts Options }

func (t *readFileTool) Name() string { return "read_file" }
func (t *readFileTool) Description() string {
	return "Read a UTF-8 text file inside the workspace. Returns content plus hash/size metadata."
}
func (t *readFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Workspace-relative path"},
			"offset": {"type": "integer", "description": "1-based start line", "minimum": 1},
			"limit": {"type": "integer", "description": "max lines (<=2000)", "minimum": 1, "maximum": 2000}
		},
		"required": ["path"],
		"additionalProperties": false
	}`)
}

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func (t *readFileTool) Invoke(ctx context.Context, req Request) (Result, error) {
	var args readFileArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return errorResult(req.CallID, t.Name(), "invalid args: "+err.Error()), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return errorResult(req.CallID, t.Name(), "path is required"), nil
	}
	abs, rel, err := write.ResolvePath(t.opts.Root, args.Path)
	if err != nil {
		return errorResult(req.CallID, t.Name(), err.Error()), nil
	}
	content, err := write.ReadFileLimited(abs, t.opts.ReadMaxBytes)
	if err != nil {
		return errorResult(req.CallID, t.Name(), err.Error()), nil
	}
	if isBinary(content) {
		return errorResult(req.CallID, t.Name(), write.ErrBinary.Error()), nil
	}
	if args.Offset < 0 || args.Limit < 0 {
		return errorResult(req.CallID, t.Name(), "offset/limit must be non-negative"), nil
	}
	totalLines := 0
	if content != "" {
		totalLines = len(strings.Split(strings.TrimSuffix(content, "\n"), "\n"))
	}
	offset := args.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	lines := strings.Split(content, "\n")
	start := offset - 1
	if start > len(lines) {
		start = len(lines)
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}
	selected := strings.Join(lines[start:end], "\n")
	truncated := end < len(lines)
	res := Result{
		CallID: req.CallID, Name: t.Name(), Content: selected,
		Meta: map[string]any{
			"path": rel, "hash": write.HashString(content), "bytes": len(content),
			"offset": offset, "lines": totalLines, "truncated": truncated,
		},
	}
	if fi, err := os.Stat(abs); err == nil {
		res.Meta["size_bytes"] = fi.Size()
	}
	if truncated {
		res.Meta["note"] = fmt.Sprintf("only lines %d-%d returned; file has %d lines", offset, end, len(lines))
	}
	return res, nil
}

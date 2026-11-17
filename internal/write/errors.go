package write

import "errors"

var (
	ErrInvalidPath    = errors.New("write: path outside workspace or invalid")
	ErrConflict       = errors.New("write: base_hash does not match current file")
	ErrLockTimeout    = errors.New("write: timed out waiting for write lock")
	ErrTooLarge       = errors.New("write: file too large")
	ErrBinary         = errors.New("write: binary content is not supported")
	ErrBranchChanging = errors.New("write: git branch is switching; retry later")
	ErrAudit          = errors.New("write: audit failed; write must not bypass audit")
)

// ConflictError 携带冲突双方的 hash，便于 Agent 重新读取/变基。
type ConflictError struct {
	Path        string
	BaseHash    string
	CurrentHash string
	FileExists  bool
}

func (e *ConflictError) Error() string {
	if !e.FileExists {
		return "write: file does not exist; base_hash must be empty for a new file"
	}
	return "write: base_hash does not match current file; re-read and retry"
}

func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

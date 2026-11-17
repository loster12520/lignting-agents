package gitwatch

import (
	"context"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Repository 是 gitwatch 需要的最小持久化端口，由 internal/store 实现。
type Repository interface {
	LastObservedGitCommit(ctx context.Context) (string, error)
	SetLastObservedGitCommit(ctx context.Context, commitSHA string) error
	RecordGitEvent(ctx context.Context, eventType, branch, commitSHA, message, createdAt string) error
	ListUncommittedWriteEvents(ctx context.Context, limit int) ([]domain.WriteEvent, error)
	CreateCommitGroup(ctx context.Context, id, commitSHA string, writeEventIDs []string, createdAt string) error
	MarkWriteEventsCommitted(ctx context.Context, writeEventIDs []string, commitSHA string) error
	MarkTimelineCommitted(ctx context.Context, writeEventIDs []string, commitSHA string) error
}

// Event 是回调/测试可见的 git 事件。
type Event struct {
	Type      string
	Branch    string
	CommitSHA string
	Message   string
	CreatedAt string
}

// Status 是当前监听状态。
type Status struct {
	Branch    string
	Commit    string
	Switching bool
	LastError string
}

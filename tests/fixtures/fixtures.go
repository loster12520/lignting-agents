// Package fixtures provides deterministic seed data and fault switches for
// integration and acceptance tests.
//
// Design notes:
//   - Seed writes directly to a migrated SQLite database. It deliberately does
//     not go through HTTP or core services so every AC can start from a known
//     state and run independently (requirements.md §14.1).
//   - IDs are fixed so assertions and evidence can reference them. The seed is
//     intended to run once per temporary data directory.
//   - Search indexes are NOT seeded. AC-06 must rebuild the index through the
//     production path (POST /api/admin/reindex or the search package) so this
//     fixture cannot mask an indexing bug.
package fixtures

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Fixed IDs shared by all seeds. Prefixes follow api-contract.md §1.3.
const (
	OwnerID                = "owner"
	AssistantID            = "agent_assistant"
	DeptProductID          = "dept_product"
	DeptDevID              = "dept_dev"
	DeptTestID             = "dept_test"
	AgentProdLeadID        = "agent_prod_lead"
	AgentProdEmpID         = "agent_prod_emp"
	AgentDevLeadID         = "agent_dev_lead"
	AgentDevAID            = "agent_dev_a"
	AgentDevBID            = "agent_dev_b"
	AgentTestLeadID        = "agent_test_lead"
	AgentTestEmpID         = "agent_test_emp"
	ConversationDMID       = "conv_dm_assistant"
	ConversationProjectID  = "conv_project"
	ConversationCrossID    = "conv_cross_dept"
	ConversationTaskID     = "conv_task_task_1"
	ConversationLogID      = "conv_decision_log"
	TaskID                 = "task_1"
	Task2ID                = "task_2"
	ArtifactPRDID          = "art_prd"
	ArtifactCodeID         = "art_code"
	ReviewOpenID           = "review_open"
	ReviewResolvedID       = "review_resolved"
	DecisionAcceptedID     = "dec_1"
	DecisionSupersededID   = "dec_0"
	ApprovalG1ID           = "appr_g1"
	ApprovalG3ID           = "appr_g3"
	ApprovalIrreversibleID = "appr_irreversible"
	TimelinePath           = "cmd/lignting/main.go"
)

// SeedOptions controls optional parts of the seed. The zero value is expanded
// by DefaultSeedOptions.
type SeedOptions struct {
	TimelineEntries int
	SkipWork        bool
	SkipMemory      bool
	SkipRuntime     bool
}

func DefaultSeedOptions() SeedOptions {
	return SeedOptions{TimelineEntries: 35}
}

// SeedResult returns every generated stable ID so tests do not duplicate
// fixture knowledge. Maps are keyed by semantic name.
type SeedResult struct {
	OwnerID       string
	AssistantID   string
	Departments   map[string]string
	Supervisors   map[string]string
	Employees     map[string]string
	Agents        []string
	Conversations map[string]string
	Messages      map[string]string

	TaskID                 string
	Task2ID                string
	CriteriaIDs            []string
	ReviewOpenID           string
	ReviewResolvedID       string
	VerdictApproveID       string
	DecisionAcceptedID     string
	DecisionSupersededID   string
	ApprovalG1ID           string
	ApprovalG3ID           string
	ApprovalIrreversibleID string

	ArtifactPRDID       string
	ArtifactCodeID      string
	ArtifactPRDVersion  string
	ArtifactCodeVersion string

	Memories          map[string]string
	ManualID          string
	WriteAppliedID    string
	WriteConflictID   string
	WriteOtherID      string
	TimelinePath      string
	TimelineEntryIDs  []string
	GitBranchEventID  int64
	GitCommitSHA      string
	UsageID           string
	TurnCompletedID   string
	TurnInterruptedID string
	NotificationIDs   map[string]string
	BridgeExpiringID  string
	AuditSeqs         []int64
}

// Seed inserts the default deterministic v0.0.1 fixture: one owner, one
// assistant, three departments (product/dev/test) with supervisors and
// employees, project/department/cross-department/task/decision-log
// conversations, a task with three acceptance criteria, artifacts, a review,
// decisions, approvals, four memory layers, write events + timeline, audit
// chain, usage, turns and notifications.
func Seed(ctx context.Context, db *sql.DB, opts SeedOptions) (*SeedResult, error) {
	if db == nil {
		return nil, fmt.Errorf("fixtures: nil db")
	}
	if opts.TimelineEntries == 0 {
		opts.TimelineEntries = 35
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("fixtures: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := Now()
	res := &SeedResult{
		OwnerID:         OwnerID,
		AssistantID:     AssistantID,
		Departments:     map[string]string{},
		Supervisors:     map[string]string{},
		Employees:       map[string]string{},
		Conversations:   map[string]string{},
		Messages:        map[string]string{},
		Memories:        map[string]string{},
		NotificationIDs: map[string]string{},
		TimelinePath:    TimelinePath,
	}

	if err := seedOrg(ctx, tx, now, res); err != nil {
		return nil, fmt.Errorf("fixtures: org: %w", err)
	}
	if !opts.SkipWork {
		if err := seedWork(ctx, tx, now, res); err != nil {
			return nil, fmt.Errorf("fixtures: work: %w", err)
		}
	}
	if !opts.SkipMemory {
		if err := seedMemory(ctx, tx, now, res); err != nil {
			return nil, fmt.Errorf("fixtures: memory: %w", err)
		}
	}
	if !opts.SkipRuntime {
		if err := seedRuntime(ctx, tx, now, opts.TimelineEntries, res); err != nil {
			return nil, fmt.Errorf("fixtures: runtime: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("fixtures: commit: %w", err)
	}
	return res, nil
}

// SeedDefault is Seed with DefaultSeedOptions.
func SeedDefault(ctx context.Context, db *sql.DB) (*SeedResult, error) {
	return Seed(ctx, db, DefaultSeedOptions())
}

// Now returns an RFC3339 UTC timestamp; matches internal/store.Now format.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// MustJSON marshals v for fixture text columns. It panics only on programmer
// error (unsupported Go value in a literal seed).
func MustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("fixtures: marshal: " + err.Error())
	}
	return string(b)
}

// ContentHash returns the sha256 hex of a fixture artifact body. It matches the
// content hashing contract the production code is expected to use.
func ContentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ChainHash returns sha256(prev + canonical json) hex for task event and audit
// hash chains (implementation-plan.md §2.2).
func ChainHash(prev string, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("fixtures: chain hash marshal: " + err.Error())
	}
	sum := sha256.Sum256(append([]byte(prev), b...))
	return hex.EncodeToString(sum[:])
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func exec(ctx context.Context, e sqlExecer, query string, args ...any) error {
	if _, err := e.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("%s: %w", query, err)
	}
	return nil
}

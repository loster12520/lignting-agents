package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Schedule 是持久化定时/到期任务（审批 TTL、部门超时、简报等）。
type Schedule struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	DueAt     string `json:"due_at"`
	Payload   string `json:"payload"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const scheduleColumns = "id, kind, due_at, payload, status, created_at, updated_at"

func scanSchedule(row interface{ Scan(...any) error }) (Schedule, error) {
	var s Schedule
	err := row.Scan(&s.ID, &s.Kind, &s.DueAt, &s.Payload, &s.Status, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return Schedule{}, err
	}
	return s, nil
}

func normalizeSchedule(sc *Schedule) {
	if sc.ID == "" {
		sc.ID = domain.NewID("sched")
	}
	if sc.Status == "" {
		sc.Status = "pending"
	}
	if sc.Payload == "" {
		sc.Payload = "{}"
	}
	now := Now()
	if sc.CreatedAt == "" {
		sc.CreatedAt = now
	}
	if sc.UpdatedAt == "" {
		sc.UpdatedAt = now
	}
}

func insertScheduleTx(tx *sql.Tx, sc Schedule) (Schedule, error) {
	normalizeSchedule(&sc)
	if strings.TrimSpace(sc.Kind) == "" || strings.TrimSpace(sc.DueAt) == "" {
		return Schedule{}, fmt.Errorf("%w: schedule kind/due_at required", ErrInvalid)
	}
	if _, err := tx.Exec("INSERT INTO schedules("+scheduleColumns+") VALUES(?,?,?,?,?,?,?)",
		sc.ID, sc.Kind, sc.DueAt, sc.Payload, sc.Status, sc.CreatedAt, sc.UpdatedAt); err != nil {
		return Schedule{}, fmt.Errorf("insert schedule: %w", err)
	}
	return sc, nil
}

// InsertSchedule 持久化一个定时任务。
func (s *Store) InsertSchedule(ctx context.Context, sc Schedule) (Schedule, error) {
	var out Schedule
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = insertScheduleTx(tx, sc)
		return err
	})
	return out, err
}

// GetSchedule 按 ID 读取定时任务。
func (s *Store) GetSchedule(ctx context.Context, id string) (Schedule, error) {
	sc, err := scanSchedule(s.DB.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, fmt.Errorf("%w: schedule %s", ErrNotFound, id)
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("get schedule: %w", err)
	}
	return sc, nil
}

// ListSchedules 按状态/kind 列出定时任务。
func (s *Store) ListSchedules(ctx context.Context, status, kind string, limit int) ([]Schedule, error) {
	if limit <= 0 {
		limit = 100
	}
	var (
		where []string
		args  []any
	)
	if status != "" {
		where = append(where, "status=?")
		args = append(args, status)
	}
	if kind != "" {
		where = append(where, "kind=?")
		args = append(args, kind)
	}
	q := "SELECT " + scheduleColumns + " FROM schedules"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY due_at ASC, id ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	out := make([]Schedule, 0, limit)
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan schedule: %w", err)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// ListDueSchedules 返回已到期且 pending 的任务（重启后由执行器补跑）。
func (s *Store) ListDueSchedules(ctx context.Context, now string, limit int) ([]Schedule, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE status='pending' AND due_at <= ? ORDER BY due_at ASC, id ASC LIMIT ?", now, limit)
	if err != nil {
		return nil, fmt.Errorf("list due schedules: %w", err)
	}
	defer rows.Close()
	out := make([]Schedule, 0, limit)
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan due schedule: %w", err)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// SetScheduleStatus 更新定时任务状态（pending -> done/skipped/canceled/failed）。
func (s *Store) SetScheduleStatus(ctx context.Context, id, status string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE schedules SET status=?, updated_at=? WHERE id=?", status, Now(), id)
	if err != nil {
		return fmt.Errorf("update schedule: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: schedule %s", ErrNotFound, id)
	}
	return nil
}

// CancelSchedule 取消 pending 定时任务（幂等：已取消直接返回）。
func (s *Store) CancelSchedule(ctx context.Context, id string) (Schedule, error) {
	var out Schedule
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		sc, err := scanSchedule(tx.QueryRow("SELECT "+scheduleColumns+" FROM schedules WHERE id=?", id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: schedule %s", ErrNotFound, id)
		}
		if err != nil {
			return fmt.Errorf("get schedule: %w", err)
		}
		switch sc.Status {
		case "pending":
			if _, err := tx.Exec("UPDATE schedules SET status='canceled', updated_at=? WHERE id=?", Now(), id); err != nil {
				return fmt.Errorf("cancel schedule: %w", err)
			}
			sc.Status = "canceled"
			sc.UpdatedAt = Now()
		case "canceled":
			// 幂等
		default:
			return fmt.Errorf("%w: schedule already %s", ErrConflict, sc.Status)
		}
		out = sc
		return nil
	})
	return out, err
}

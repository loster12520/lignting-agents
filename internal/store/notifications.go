package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// Notification 是持久化收件箱条目（FR-ASST-06/FR-CHAT-06）。
// 级别：fyi | need_know | need_decide | blocking；状态：unread | read | dismissed。
type Notification struct {
	ID             string `json:"id"`
	Level          string `json:"level"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	ConversationID string `json:"conversation_id"`
	ObjectType     string `json:"object_type"`
	ObjectID       string `json:"object_id"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
}

const notificationColumns = "id, level, title, body, conversation_id, object_type, object_id, status, created_at"

var notificationLevels = map[string]bool{
	"fyi": true, "need_know": true, "need_decide": true, "blocking": true,
}
var notificationStatuses = map[string]bool{"unread": true, "read": true, "dismissed": true, "snoozed": true}

// CreateNotification 写入收件箱条目；同一 (level, object_type, object_id) 应用层唯一。
// 返回 (条目, 是否新建)。object_id 为空表示不参与去重（纯广播条目）。
func (s *Store) CreateNotification(ctx context.Context, n Notification) (Notification, bool, error) {
	if n.Level == "" {
		n.Level = "fyi"
	}
	if !notificationLevels[n.Level] {
		return Notification{}, false, fmt.Errorf("%w: unknown notification level %s", ErrInvalid, n.Level)
	}
	if strings.TrimSpace(n.Title) == "" {
		return Notification{}, false, fmt.Errorf("%w: notification title required", ErrInvalid)
	}
	if n.Status == "" {
		n.Status = "unread"
	}
	if !notificationStatuses[n.Status] {
		return Notification{}, false, fmt.Errorf("%w: unknown notification status %s", ErrInvalid, n.Status)
	}
	var (
		out     Notification
		created bool
	)
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if n.ObjectID != "" {
			existing, err := scanNotification(tx.QueryRow("SELECT "+notificationColumns+" FROM notifications WHERE level=? AND object_type=? AND object_id=? ORDER BY created_at LIMIT 1",
				n.Level, n.ObjectType, n.ObjectID))
			if err == nil {
				out, created = existing, false
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("lookup notification: %w", err)
			}
		}
		if n.ID == "" {
			n.ID = newNotificationID()
		}
		if n.CreatedAt == "" {
			n.CreatedAt = Now()
		}
		if _, err := tx.Exec("INSERT INTO notifications("+notificationColumns+") VALUES(?,?,?,?,?,?,?,?,?)",
			n.ID, n.Level, n.Title, n.Body, n.ConversationID, n.ObjectType, n.ObjectID, n.Status, n.CreatedAt); err != nil {
			return fmt.Errorf("insert notification: %w", err)
		}
		out, created = n, true
		return nil
	})
	if err != nil {
		return Notification{}, false, err
	}
	return out, created, nil
}

func newNotificationID() string { return "notif_" + domain.NewID("n")[2:] }

func scanNotification(row interface{ Scan(...any) error }) (Notification, error) {
	var n Notification
	err := row.Scan(&n.ID, &n.Level, &n.Title, &n.Body, &n.ConversationID, &n.ObjectType, &n.ObjectID, &n.Status, &n.CreatedAt)
	if err != nil {
		return Notification{}, err
	}
	return n, nil
}

// GetNotification 按 ID 读取收件箱条目。
func (s *Store) GetNotification(ctx context.Context, id string) (Notification, error) {
	n, err := scanNotification(s.DB.QueryRowContext(ctx, "SELECT "+notificationColumns+" FROM notifications WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Notification{}, fmt.Errorf("%w: notification %s", ErrNotFound, id)
	}
	if err != nil {
		return Notification{}, fmt.Errorf("get notification: %w", err)
	}
	return n, nil
}

// ListNotifications 按状态列出收件箱（created_at 倒序；status 为空=全部）。
func (s *Store) ListNotifications(ctx context.Context, status string, limit int) ([]Notification, error) {
	if status != "" && !notificationStatuses[status] {
		return nil, fmt.Errorf("%w: unknown notification status %s", ErrInvalid, status)
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	q := "SELECT " + notificationColumns + " FROM notifications"
	args := []any{}
	if status != "" {
		q += " WHERE status=?"
		args = append(args, status)
	}
	q += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	out := make([]Notification, 0, limit)
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SetNotificationStatus 更新收件箱状态（unread/read/dismissed）。
func (s *Store) SetNotificationStatus(ctx context.Context, id, status string) (Notification, error) {
	if !notificationStatuses[status] {
		return Notification{}, fmt.Errorf("%w: unknown notification status %s", ErrInvalid, status)
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE notifications SET status=? WHERE id=?", status, id)
	if err != nil {
		return Notification{}, fmt.Errorf("update notification: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Notification{}, fmt.Errorf("%w: notification %s", ErrNotFound, id)
	}
	return s.GetNotification(ctx, id)
}

// CountNotificationsSince 统计某时间点后指定级别的通知数（实时打扰预算用）。
func (s *Store) CountNotificationsSince(ctx context.Context, levels []string, since string) (int64, error) {
	if len(levels) == 0 {
		return 0, nil
	}
	ph := make([]string, len(levels))
	args := make([]any, 0, len(levels)+1)
	for i, l := range levels {
		ph[i] = "?"
		args = append(args, l)
	}
	args = append(args, since)
	var n int64
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM notifications WHERE level IN ("+strings.Join(ph, ",")+") AND created_at >= ?", args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count notifications: %w", err)
	}
	return n, nil
}

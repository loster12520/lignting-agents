package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Attachment 是附件元数据（v2 表；文件字节由 platform 落数据目录，store 只存元数据）。
type Attachment struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	Filename       string `json:"filename"`
	Mime           string `json:"mime"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
	StoragePath    string `json:"storage_path,omitempty"`
	CreatedBy      string `json:"created_by"`
	CreatedAt      string `json:"created_at"`
	DeletedAt      string `json:"deleted_at"`
}

const attachmentColumns = "id, conversation_id, message_id, filename, mime, size, sha256, storage_path, created_by, created_at, deleted_at"

func scanAttachment(row interface{ Scan(...any) error }) (Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.ConversationID, &a.MessageID, &a.Filename, &a.Mime, &a.Size,
		&a.SHA256, &a.StoragePath, &a.CreatedBy, &a.CreatedAt, &a.DeletedAt)
	if err != nil {
		return Attachment{}, err
	}
	return a, nil
}

// CreateAttachment 写入附件元数据（文件已在磁盘落盘；失败由 platform 清理文件）。
func (s *Store) CreateAttachment(ctx context.Context, a Attachment) (Attachment, error) {
	if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.ConversationID) == "" || strings.TrimSpace(a.Filename) == "" {
		return Attachment{}, fmt.Errorf("%w: attachment requires id/conversation_id/filename", ErrInvalid)
	}
	if a.Mime == "" {
		a.Mime = "application/octet-stream"
	}
	if a.CreatedAt == "" {
		a.CreatedAt = Now()
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO attachments("+attachmentColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?)",
		a.ID, a.ConversationID, a.MessageID, a.Filename, a.Mime, a.Size, a.SHA256, a.StoragePath,
		a.CreatedBy, a.CreatedAt, a.DeletedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Attachment{}, fmt.Errorf("%w: attachment %s", ErrConflict, a.ID)
		}
		return Attachment{}, fmt.Errorf("create attachment: %w", err)
	}
	return a, nil
}

// GetAttachment 按 ID 读取附件元数据（含墓碑，调用方判断 DeletedAt）。
func (s *Store) GetAttachment(ctx context.Context, id string) (Attachment, error) {
	a, err := scanAttachment(s.DB.QueryRowContext(ctx, "SELECT "+attachmentColumns+" FROM attachments WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, fmt.Errorf("%w: attachment %s", ErrNotFound, id)
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("get attachment: %w", err)
	}
	return a, nil
}

// ListAttachmentsByConversation 列出会话附件（默认排除墓碑）。
func (s *Store) ListAttachmentsByConversation(ctx context.Context, conversationID string, includeDeleted bool, limit int) ([]Attachment, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := "SELECT " + attachmentColumns + " FROM attachments WHERE conversation_id=?"
	if !includeDeleted {
		q += " AND deleted_at=''"
	}
	q += " ORDER BY created_at DESC, id DESC LIMIT ?"
	rows, err := s.DB.QueryContext(ctx, q, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	defer rows.Close()
	out := make([]Attachment, 0, limit)
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SoftDeleteAttachment 软删除（墓碑）；不存在返回 ErrNotFound。
func (s *Store) SoftDeleteAttachment(ctx context.Context, id string) (Attachment, error) {
	a, err := s.GetAttachment(ctx, id)
	if err != nil {
		return Attachment{}, err
	}
	if a.DeletedAt != "" {
		return a, nil // 幂等
	}
	a.DeletedAt = Now()
	if _, err := s.DB.ExecContext(ctx, "UPDATE attachments SET deleted_at=? WHERE id=?", a.DeletedAt, id); err != nil {
		return Attachment{}, fmt.Errorf("soft delete attachment: %w", err)
	}
	return a, nil
}

var _ = sql.ErrNoRows

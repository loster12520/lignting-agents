package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lignting/lignting-agents/internal/domain"
)

// maxMessageBytes 是单条消息内容的硬上限（平台层还有更小的用户输入限制）。
const maxMessageBytes = 1 << 20

const messageColumns = "seq, id, conversation_id, thread_id, sender_type, sender_id, sender_name, type, content, meta, created_at, edited_at, deleted_at"

// NewMessage 是平台写入用户/系统消息的输入；seq/id/created_at 由 store 分配。
type NewMessage struct {
	ConversationID string
	ThreadID       string
	SenderType     string
	SenderID       string
	SenderName     string
	Type           string
	Content        string
	Meta           string
	ClientID       string
	// EventType 控制事务内事件类型，默认 message.created；
	// core 生成 Agent 完整回复时可传 message.completed。
	EventType string
}

func scanMessage(row interface{ Scan(...any) error }) (domain.Message, error) {
	var m domain.Message
	err := row.Scan(&m.Seq, &m.ID, &m.ConversationID, &m.ThreadID, &m.SenderType, &m.SenderID,
		&m.SenderName, &m.Type, &m.Content, &m.Meta, &m.CreatedAt, &m.EditedAt, &m.DeletedAt)
	if err != nil {
		return domain.Message{}, err
	}
	return m, nil
}

func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// canonicalMeta 校验 meta 为 JSON object，合并 client_id 后按 key 排序输出。
// 幂等查询依赖 json_extract(meta,'$.client_id')。
func canonicalMeta(meta, clientID string) (string, error) {
	m := map[string]any{}
	if strings.TrimSpace(meta) != "" {
		if err := json.Unmarshal([]byte(meta), &m); err != nil {
			return "", fmt.Errorf("%w: meta must be a JSON object", ErrInvalid)
		}
	}
	if clientID != "" {
		m["client_id"] = clientID
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("canonical meta: %w", err)
	}
	return string(b), nil
}

// AppendMessageTx 在调用方事务内插入消息并同步更新检索索引（不含事件/审计）。
// core 需要「消息 + 事件 + 审计」原子写时：WithTx + AppendMessageTx + AppendEventTx + AppendAuditTx。
func (s *Store) AppendMessageTx(ctx context.Context, tx *sql.Tx, msg domain.Message) (domain.Message, error) {
	return s.insertMessageTx(ctx, tx, msg)
}

func (s *Store) insertMessageTx(ctx context.Context, tx *sql.Tx, msg domain.Message) (domain.Message, error) {
	_ = ctx
	if strings.TrimSpace(msg.ConversationID) == "" {
		return domain.Message{}, fmt.Errorf("%w: conversation_id required", ErrInvalid)
	}
	deptID, visibility, _, err := conversationMetaTx(tx, msg.ConversationID)
	if err != nil {
		return domain.Message{}, err
	}
	if msg.ID == "" {
		msg.ID = domain.NewID("msg")
	}
	if msg.SenderType == "" {
		msg.SenderType = domain.SenderSystem
	}
	if msg.Type == "" {
		msg.Type = domain.MsgText
	}
	if msg.CreatedAt == "" {
		msg.CreatedAt = Now()
	}
	if len(msg.Content) > maxMessageBytes {
		return domain.Message{}, fmt.Errorf("%w: message too large", ErrInvalid)
	}
	meta, err := canonicalMeta(msg.Meta, "")
	if err != nil {
		return domain.Message{}, err
	}
	msg.Meta = meta
	res, err := tx.Exec("INSERT INTO messages(id, conversation_id, thread_id, sender_type, sender_id, sender_name, type, content, meta, created_at, edited_at, deleted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
		msg.ID, msg.ConversationID, msg.ThreadID, msg.SenderType, msg.SenderID, msg.SenderName,
		msg.Type, msg.Content, msg.Meta, msg.CreatedAt, msg.EditedAt, msg.DeletedAt)
	if err != nil {
		return domain.Message{}, fmt.Errorf("insert message: %w", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return domain.Message{}, fmt.Errorf("message seq: %w", err)
	}
	msg.Seq = seq
	doc := IndexDoc{
		DocID:          MessageDocID(msg.ID),
		Kind:           "message",
		RefID:          msg.ID,
		ConversationID: msg.ConversationID,
		DepartmentID:   deptID,
		Title:          "",
		Body:           msg.Content,
		Visibility:     visibility,
		CreatedAt:      msg.CreatedAt,
	}
	if err := s.indexDocTx(tx, doc); err != nil {
		return domain.Message{}, fmt.Errorf("index message: %w", err)
	}
	if _, err := tx.Exec("UPDATE conversations SET updated_at=? WHERE id=?", Now(), msg.ConversationID); err != nil {
		return domain.Message{}, fmt.Errorf("touch conversation: %w", err)
	}
	return msg, nil
}

// AppendMessage 是 core 的便捷入口：插入消息 + 索引，自行管理事务时用 AppendMessageTx。
func (s *Store) AppendMessage(ctx context.Context, msg domain.Message) (domain.Message, error) {
	var out domain.Message
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = s.insertMessageTx(ctx, tx, msg)
		return err
	})
	if err != nil {
		return domain.Message{}, err
	}
	return out, nil
}

// InsertMessage 是平台写消息的事务入口：client_id 幂等 + 消息 + 事件 + 审计 + 索引，
// 全部在一个事务内完成；提交后才唤醒 SSE（先写库再发事件）。
func (s *Store) InsertMessage(ctx context.Context, in NewMessage) (*domain.Message, *domain.Event, bool, error) {
	if strings.TrimSpace(in.ConversationID) == "" || strings.TrimSpace(in.SenderType) == "" || strings.TrimSpace(in.SenderID) == "" {
		return nil, nil, false, fmt.Errorf("%w: conversation_id/sender required", ErrInvalid)
	}
	meta, err := canonicalMeta(in.Meta, in.ClientID)
	if err != nil {
		return nil, nil, false, err
	}
	eventType := in.EventType
	if eventType == "" {
		eventType = "message.created"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if in.ClientID != "" {
		existing, err := findMessageByClientIDTx(tx, in.ConversationID, in.SenderID, in.ClientID)
		if err != nil {
			return nil, nil, false, err
		}
		if existing != nil {
			return existing, nil, false, nil
		}
	}
	msg, err := s.insertMessageTx(ctx, tx, domain.Message{
		ConversationID: in.ConversationID,
		ThreadID:       in.ThreadID,
		SenderType:     in.SenderType,
		SenderID:       in.SenderID,
		SenderName:     in.SenderName,
		Type:           in.Type,
		Content:        in.Content,
		Meta:           meta,
	})
	if err != nil {
		return nil, nil, false, err
	}
	ev, err := appendEventTxRaw(tx, domain.Event{
		Type:           eventType,
		ConversationID: msg.ConversationID,
		ObjectType:     "message",
		ObjectID:       msg.ID,
		ActorType:      msg.SenderType,
		ActorID:        msg.SenderID,
		Payload:        marshalJSON(msg),
		CreatedAt:      Now(),
	})
	if err != nil {
		return nil, nil, false, err
	}
	detail := map[string]any{"conversation_id": msg.ConversationID, "seq": msg.Seq, "event_type": eventType}
	if in.ClientID != "" {
		detail["client_id"] = in.ClientID
	}
	if _, err := s.AppendAuditTx(ctx, tx, domain.AuditEntry{
		ActorType: msg.SenderType, ActorID: msg.SenderID, Action: "message.create",
		ObjectType: "message", ObjectID: msg.ID, Result: "ok", Detail: marshalJSON(detail),
	}); err != nil {
		return nil, nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, false, fmt.Errorf("commit message: %w", err)
	}
	s.notifyEvent()
	return &msg, &ev, true, nil
}

func findMessageByClientIDTx(tx *sql.Tx, conversationID, senderID, clientID string) (*domain.Message, error) {
	m, err := scanMessage(tx.QueryRow("SELECT "+messageColumns+" FROM messages WHERE conversation_id=? AND sender_id=? AND json_extract(meta,'$.client_id')=? ORDER BY seq LIMIT 1",
		conversationID, senderID, clientID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find message by client_id: %w", err)
	}
	return &m, nil
}

// GetMessage 按 ID 读取消息（含已删除，调用方自行判断 deleted_at）。
func (s *Store) GetMessage(ctx context.Context, id string) (domain.Message, error) {
	m, err := scanMessage(s.DB.QueryRowContext(ctx, "SELECT "+messageColumns+" FROM messages WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Message{}, fmt.Errorf("%w: message %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Message{}, fmt.Errorf("get message: %w", err)
	}
	return m, nil
}

// ListMessages 按 before/after 分页返回消息（按 seq 升序，排除已删除）。
// before>0 向后翻页；after>0 向前补齐；都为 0 时返回最近 limit 条。
func (s *Store) ListMessages(ctx context.Context, conversationID string, before, after int64, limit int) ([]domain.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	var (
		q    string
		args = []any{conversationID}
	)
	switch {
	case after > 0:
		q = "SELECT " + messageColumns + " FROM messages WHERE conversation_id=? AND deleted_at='' AND seq > ? ORDER BY seq ASC LIMIT ?"
		args = append(args, after, limit)
	case before > 0:
		q = "SELECT " + messageColumns + " FROM messages WHERE conversation_id=? AND deleted_at='' AND seq < ? ORDER BY seq DESC LIMIT ?"
		args = append(args, before, limit)
	default:
		q = "SELECT " + messageColumns + " FROM messages WHERE conversation_id=? AND deleted_at='' ORDER BY seq DESC LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Message, 0, limit)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if after <= 0 {
		// DESC 查询结果反转为升序
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}

// RecentMessages 返回最近 limit 条消息（升序）；beforeSeq<=0 表示取最新。
func (s *Store) RecentMessages(ctx context.Context, conversationID string, limit int, beforeSeq int64) ([]domain.Message, error) {
	return s.ListMessages(ctx, conversationID, beforeSeq, 0, limit)
}

// MaxMessageSeq 返回会话内最新未删除消息 seq（空会话为 0）。
func (s *Store) MaxMessageSeq(ctx context.Context, conversationID string) (int64, error) {
	var seq int64
	if err := s.DB.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM messages WHERE conversation_id=? AND deleted_at=''", conversationID).Scan(&seq); err != nil {
		return 0, fmt.Errorf("max message seq: %w", err)
	}
	return seq, nil
}

// CountMessages 返回会话内未删除消息数。
func (s *Store) CountMessages(ctx context.Context, conversationID string) (int64, error) {
	var n int64
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE conversation_id=? AND deleted_at=''", conversationID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count messages: %w", err)
	}
	return n, nil
}

// CountUnread 返回 owner 未读数：seq > afterSeq 且非本人发送的未删除消息。
func (s *Store) CountUnread(ctx context.Context, conversationID string, afterSeq int64) (int64, error) {
	var n int64
	if err := s.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE conversation_id=? AND deleted_at='' AND seq > ? AND sender_type<>?",
		conversationID, afterSeq, domain.SenderUser).Scan(&n); err != nil {
		return 0, fmt.Errorf("count unread: %w", err)
	}
	return n, nil
}

// LastMessage 返回会话最新未删除消息；无消息返回 (nil, nil)。
func (s *Store) LastMessage(ctx context.Context, conversationID string) (*domain.Message, error) {
	m, err := scanMessage(s.DB.QueryRowContext(ctx, "SELECT "+messageColumns+" FROM messages WHERE conversation_id=? AND deleted_at='' ORDER BY seq DESC LIMIT 1", conversationID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last message: %w", err)
	}
	return &m, nil
}

// MessageSeqsByIDs 批量返回消息 seq（搜索命中后给前端跳转定位用）。
func (s *Store) MessageSeqsByIDs(ctx context.Context, ids []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT id, seq FROM messages WHERE id IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return nil, fmt.Errorf("message seqs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var seq int64
		if err := rows.Scan(&id, &seq); err != nil {
			return nil, err
		}
		out[id] = seq
	}
	return out, rows.Err()
}

// EditMessage 编辑消息内容：更新库 + 索引 + message.updated 事件 + 审计，单事务。
func (s *Store) EditMessage(ctx context.Context, id, content string, actor Actor) (*domain.Message, *domain.Event, error) {
	if strings.TrimSpace(content) == "" {
		return nil, nil, fmt.Errorf("%w: content required", ErrInvalid)
	}
	if len(content) > maxMessageBytes {
		return nil, nil, fmt.Errorf("%w: message too large", ErrInvalid)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	msg, err := scanMessage(tx.QueryRow("SELECT "+messageColumns+" FROM messages WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("%w: message %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get message: %w", err)
	}
	if msg.DeletedAt != "" {
		return nil, nil, fmt.Errorf("%w: message deleted", ErrConflict)
	}
	msg.Content = content
	msg.EditedAt = Now()
	if _, err := tx.Exec("UPDATE messages SET content=?, edited_at=? WHERE id=?", msg.Content, msg.EditedAt, id); err != nil {
		return nil, nil, fmt.Errorf("update message: %w", err)
	}
	deptID, visibility, _, err := conversationMetaTx(tx, msg.ConversationID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.indexDocTx(tx, IndexDoc{
		DocID: MessageDocID(msg.ID), Kind: "message", RefID: msg.ID,
		ConversationID: msg.ConversationID, DepartmentID: deptID,
		Body: msg.Content, Visibility: visibility, CreatedAt: msg.CreatedAt,
	}); err != nil {
		return nil, nil, fmt.Errorf("reindex message: %w", err)
	}
	ev, err := appendEventTxRaw(tx, domain.Event{
		Type: "message.updated", ConversationID: msg.ConversationID, ObjectType: "message", ObjectID: msg.ID,
		ActorType: actorOrSystem(actor).Type, ActorID: actorOrSystem(actor).ID,
		Payload: marshalJSON(msg), CreatedAt: Now(),
	})
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.AppendAuditTx(ctx, tx, domain.AuditEntry{
		ActorType: actorOrSystem(actor).Type, ActorID: actorOrSystem(actor).ID, Action: "message.update",
		ObjectType: "message", ObjectID: msg.ID, Result: "ok",
		Detail: marshalJSON(map[string]any{"conversation_id": msg.ConversationID, "seq": msg.Seq}),
	}); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit edit: %w", err)
	}
	s.notifyEvent()
	return &msg, &ev, nil
}

// DeleteMessage 软删除消息（墓碑）：保留行，索引同步移除，写 message.deleted + 审计。
func (s *Store) DeleteMessage(ctx context.Context, id string, actor Actor) (*domain.Message, *domain.Event, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	msg, err := scanMessage(tx.QueryRow("SELECT "+messageColumns+" FROM messages WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("%w: message %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get message: %w", err)
	}
	if msg.DeletedAt != "" {
		return nil, nil, fmt.Errorf("%w: message deleted", ErrConflict)
	}
	msg.DeletedAt = Now()
	if _, err := tx.Exec("UPDATE messages SET deleted_at=? WHERE id=?", msg.DeletedAt, id); err != nil {
		return nil, nil, fmt.Errorf("delete message: %w", err)
	}
	if err := s.deleteDocTx(tx, MessageDocID(msg.ID)); err != nil {
		return nil, nil, fmt.Errorf("remove message index: %w", err)
	}
	ev, err := appendEventTxRaw(tx, domain.Event{
		Type: "message.deleted", ConversationID: msg.ConversationID, ObjectType: "message", ObjectID: msg.ID,
		ActorType: actorOrSystem(actor).Type, ActorID: actorOrSystem(actor).ID,
		Payload: marshalJSON(msg), CreatedAt: Now(),
	})
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.AppendAuditTx(ctx, tx, domain.AuditEntry{
		ActorType: actorOrSystem(actor).Type, ActorID: actorOrSystem(actor).ID, Action: "message.delete",
		ObjectType: "message", ObjectID: msg.ID, Result: "ok",
		Detail: marshalJSON(map[string]any{"conversation_id": msg.ConversationID, "seq": msg.Seq, "tombstone": true}),
	}); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit delete: %w", err)
	}
	s.notifyEvent()
	return &msg, &ev, nil
}

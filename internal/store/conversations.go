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

// Actor 是审计/事件的执行者投影；Type 取 domain.SenderUser/SenderAgent/SenderSystem。
type Actor struct {
	Type string
	ID   string
}

// ConversationFilter 是会话列表过滤条件。
type ConversationFilter struct {
	Type            string
	DepartmentID    string
	Status          string
	IncludeArchived bool
	Limit           int
}

const conversationColumns = "id, type, title, department_id, visibility, topic, created_by, status, expires_at, created_at, updated_at"
const memberColumns = "conversation_id, principal_type, principal_id, role, joined_at"

func scanConversation(row interface{ Scan(...any) error }) (domain.Conversation, error) {
	var c domain.Conversation
	err := row.Scan(&c.ID, &c.Type, &c.Title, &c.DepartmentID, &c.Visibility, &c.Topic,
		&c.CreatedBy, &c.Status, &c.ExpiresAt, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return domain.Conversation{}, err
	}
	return c, nil
}

func scanMember(row interface{ Scan(...any) error }) (domain.ConversationMember, error) {
	var m domain.ConversationMember
	err := row.Scan(&m.ConversationID, &m.PrincipalType, &m.PrincipalID, &m.Role, &m.JoinedAt)
	if err != nil {
		return domain.ConversationMember{}, err
	}
	return m, nil
}

func normalizeConversation(c *domain.Conversation) {
	if c.ID == "" {
		c.ID = domain.NewID("conv")
	}
	if c.Status == "" {
		c.Status = "active"
	}
	if c.Visibility == "" {
		if c.Type == domain.ConvDM {
			c.Visibility = domain.VisibilityPrivate
		} else {
			c.Visibility = domain.VisibilityProject
		}
	}
	now := Now()
	if c.CreatedAt == "" {
		c.CreatedAt = now
	}
	if c.UpdatedAt == "" {
		c.UpdatedAt = now
	}
}

// CreateConversation 在单事务内创建会话 + 成员 + conversation.created 事件 + 审计。
func (s *Store) CreateConversation(ctx context.Context, c domain.Conversation, members []domain.ConversationMember, actor Actor) (domain.Conversation, error) {
	var out domain.Conversation
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = CreateConversationTx(ctx, tx, c, members, actor)
		return err
	})
	if err != nil {
		return domain.Conversation{}, err
	}
	s.notifyEvent()
	return out, nil
}

// CreateConversationForOrg 是 core R3 的会话创建入口：用原始 actorType/actorID，
// 避免 core 依赖 store.Actor。
func (s *Store) CreateConversationForOrg(ctx context.Context, c domain.Conversation, members []domain.ConversationMember, actorType, actorID string) (domain.Conversation, error) {
	return s.CreateConversation(ctx, c, members, Actor{Type: actorType, ID: actorID})
}

// CreateConversationTx 在调用方事务内创建会话 + 成员 + 事件 + 审计。
// setup / provisioning 需要跨表原子写时使用。
func CreateConversationTx(ctx context.Context, tx *sql.Tx, c domain.Conversation, members []domain.ConversationMember, actor Actor) (domain.Conversation, error) {
	normalizeConversation(&c)
	if strings.TrimSpace(c.Type) == "" {
		return domain.Conversation{}, fmt.Errorf("%w: conversation type required", ErrInvalid)
	}
	if _, err := tx.Exec("INSERT INTO conversations("+conversationColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?)",
		c.ID, c.Type, c.Title, c.DepartmentID, c.Visibility, c.Topic, c.CreatedBy, c.Status,
		c.ExpiresAt, c.CreatedAt, c.UpdatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.Conversation{}, fmt.Errorf("%w: conversation %s", ErrConflict, c.ID)
		}
		return domain.Conversation{}, fmt.Errorf("create conversation: %w", err)
	}
	for _, m := range members {
		m.ConversationID = c.ID
		if err := addMemberTx(tx, m); err != nil {
			return domain.Conversation{}, err
		}
	}
	payload, _ := json.Marshal(c)
	if _, err := appendEventTxRaw(tx, domain.Event{
		Type: "conversation.created", ConversationID: c.ID, ObjectType: "conversation", ObjectID: c.ID,
		ActorType: actorOrSystem(actor).Type, ActorID: actorOrSystem(actor).ID, Payload: string(payload), CreatedAt: Now(),
	}); err != nil {
		return domain.Conversation{}, err
	}
	detail, _ := json.Marshal(map[string]any{"type": c.Type, "title": c.Title, "members": len(members)})
	if _, err := appendAuditTxRaw(tx, domain.AuditEntry{
		ActorType: actorOrSystem(actor).Type, ActorID: actorOrSystem(actor).ID, Action: "conversation.create",
		ObjectType: "conversation", ObjectID: c.ID, Result: "ok", Detail: string(detail),
	}); err != nil {
		return domain.Conversation{}, err
	}
	return c, nil
}

func actorOrSystem(a Actor) Actor {
	if a.Type == "" {
		a.Type = domain.SenderSystem
	}
	return a
}

func addMemberTx(tx *sql.Tx, m domain.ConversationMember) error {
	if m.PrincipalType == "" || m.PrincipalID == "" {
		return fmt.Errorf("%w: member principal required", ErrInvalid)
	}
	if m.Role == "" {
		m.Role = "member"
	}
	if m.JoinedAt == "" {
		m.JoinedAt = Now()
	}
	if _, err := tx.Exec("INSERT OR IGNORE INTO conversation_members(conversation_id, principal_type, principal_id, role, joined_at) VALUES(?,?,?,?,?)",
		m.ConversationID, m.PrincipalType, m.PrincipalID, m.Role, m.JoinedAt); err != nil {
		return fmt.Errorf("add conversation member: %w", err)
	}
	return nil
}

// GetConversation 按 ID 读取会话。
func (s *Store) GetConversation(ctx context.Context, id string) (domain.Conversation, error) {
	c, err := scanConversation(s.DB.QueryRowContext(ctx, "SELECT "+conversationColumns+" FROM conversations WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Conversation{}, fmt.Errorf("%w: conversation %s", ErrNotFound, id)
	}
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return c, nil
}

// ListConversations 列出会话（默认排除 archived）。
func (s *Store) ListConversations(ctx context.Context, f ConversationFilter) ([]domain.Conversation, error) {
	var (
		where []string
		args  []any
	)
	if f.Type != "" {
		where = append(where, "type=?")
		args = append(args, f.Type)
	}
	if f.DepartmentID != "" {
		where = append(where, "department_id=?")
		args = append(args, f.DepartmentID)
	}
	if f.Status != "" {
		where = append(where, "status=?")
		args = append(args, f.Status)
	} else if !f.IncludeArchived {
		where = append(where, "status<>'archived'")
	}
	q := "SELECT " + conversationColumns + " FROM conversations"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY updated_at DESC, id"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Conversation, 0, 8)
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateConversation 更新会话可编辑字段（标题/主题/可见性/状态/到期）。
func (s *Store) UpdateConversation(ctx context.Context, c domain.Conversation) (domain.Conversation, error) {
	c.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, "UPDATE conversations SET title=?, topic=?, visibility=?, status=?, expires_at=?, updated_at=? WHERE id=?",
		c.Title, c.Topic, c.Visibility, c.Status, c.ExpiresAt, c.UpdatedAt, c.ID)
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("update conversation: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.Conversation{}, fmt.Errorf("%w: conversation %s", ErrNotFound, c.ID)
	}
	return c, nil
}

// ListConversationMembers 返回会话成员。
func (s *Store) ListConversationMembers(ctx context.Context, conversationID string) ([]domain.ConversationMember, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+memberColumns+" FROM conversation_members WHERE conversation_id=? ORDER BY joined_at, principal_id", conversationID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	out := make([]domain.ConversationMember, 0, 4)
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddConversationMember 添加成员（幂等）。
func (s *Store) AddConversationMember(ctx context.Context, m domain.ConversationMember) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error { return addMemberTx(tx, m) })
}

// RemoveConversationMember 移除成员（不存在不报错）。
func (s *Store) RemoveConversationMember(ctx context.Context, conversationID, principalType, principalID string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM conversation_members WHERE conversation_id=? AND principal_type=? AND principal_id=?",
		conversationID, principalType, principalID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return nil
}

// CountConversationMembers 返回成员数。
func (s *Store) CountConversationMembers(ctx context.Context, conversationID string) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation_members WHERE conversation_id=?", conversationID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count members: %w", err)
	}
	return n, nil
}

// conversationMetaTx 返回写索引/校验需要的会话元数据。
func conversationMetaTx(tx *sql.Tx, conversationID string) (departmentID, visibility, title string, err error) {
	var status string
	err = tx.QueryRow("SELECT department_id, visibility, title, status FROM conversations WHERE id=?", conversationID).
		Scan(&departmentID, &visibility, &title, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", fmt.Errorf("%w: conversation %s", ErrNotFound, conversationID)
	}
	if err != nil {
		return "", "", "", fmt.Errorf("conversation meta: %w", err)
	}
	if status == "archived" {
		return "", "", "", fmt.Errorf("%w: conversation archived", ErrConflict)
	}
	return departmentID, visibility, title, nil
}

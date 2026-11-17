package platform

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

const maxUserContentBytes = 64 * 1024

var conversationTypes = map[string]bool{
	domain.ConvDM:             true,
	domain.ConvProject:        true,
	domain.ConvDepartment:     true,
	domain.ConvCrossDept:      true,
	domain.ConvTemp:           true,
	domain.ConvTaskThread:     true,
	domain.ConvArtifactThread: true,
	domain.ConvDecisionLog:    true,
}

var visibilityValues = map[string]bool{
	domain.VisibilityPrivate:    true,
	domain.VisibilityDepartment: true,
	domain.VisibilityProject:    true,
	domain.VisibilityOrg:        true,
	domain.VisibilityHumanOnly:  true,
}

var userMessageTypes = map[string]bool{
	domain.MsgText: true,
	domain.MsgCard: true,
}

func (s *Server) mustConversation(w http.ResponseWriter, r *http.Request) (*domain.Conversation, bool) {
	conv, err := s.store.GetConversation(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return nil, false
	}
	return &conv, true
}

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	q := r.URL.Query()
	f := store.ConversationFilter{
		Type:            q.Get("type"),
		DepartmentID:    q.Get("department_id"),
		IncludeArchived: q.Get("include_archived") == "1" || q.Get("include_archived") == "true",
	}
	convs, err := s.store.ListConversations(ctx, f)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	items, err := s.conversationDTOs(ctx, convs)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeList(w, items, int64(len(items)), "")
}

type createConversationRequest struct {
	Type         string   `json:"type"`
	Title        string   `json:"title"`
	DepartmentID string   `json:"department_id"`
	Visibility   string   `json:"visibility"`
	Topic        string   `json:"topic"`
	MemberIDs    []string `json:"member_ids"`
	ExpiresAt    string   `json:"expires_at"`
}

func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	var req createConversationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	convType := strings.TrimSpace(req.Type)
	if convType == "" {
		convType = domain.ConvProject
	}
	if !conversationTypes[convType] {
		s.writeErr(w, errInvalid("不支持的会话类型"))
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "新会话"
	}
	if utf8.RuneCountInString(title) > 100 {
		s.writeErr(w, errInvalid("会话标题过长（上限 100 字）"))
		return
	}
	visibility, err := normalizeConversationVisibility(req.Visibility)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if req.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, req.ExpiresAt); err != nil {
			s.writeErr(w, errInvalid("expires_at 必须是 RFC3339 时间"))
			return
		}
	}
	members := []domain.ConversationMember{
		{PrincipalType: domain.SenderUser, PrincipalID: owner.ID, Role: "owner"},
	}
	seen := map[string]bool{}
	for _, agentID := range req.MemberIDs {
		agentID = strings.TrimSpace(agentID)
		if agentID == "" || seen[agentID] {
			continue
		}
		agent, err := s.store.GetAgent(ctx, agentID)
		if err != nil {
			s.writeErr(w, errInvalid("成员不存在: "+agentID))
			return
		}
		seen[agentID] = true
		members = append(members, domain.ConversationMember{
			PrincipalType: domain.SenderAgent, PrincipalID: agent.ID, Role: "member",
		})
	}
	conv, err := s.store.CreateConversation(ctx, domain.Conversation{
		Type: convType, Title: title, DepartmentID: strings.TrimSpace(req.DepartmentID),
		Visibility: visibility, Topic: strings.TrimSpace(req.Topic),
		CreatedBy: owner.ID, ExpiresAt: req.ExpiresAt,
	}, members, store.Actor{Type: domain.SenderUser, ID: owner.ID})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	dto, err := s.conversationDTO(ctx, conv)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, dto)
}

func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	conv, ok := s.mustConversation(w, r)
	if !ok {
		return
	}
	dto, err := s.conversationDTO(r.Context(), *conv)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, dto)
}

type patchConversationRequest struct {
	Title      *string   `json:"title"`
	Topic      *string   `json:"topic"`
	Visibility *string   `json:"visibility"`
	ExpiresAt  *string   `json:"expires_at"`
	MemberIDs  *[]string `json:"member_ids"`
}

func (s *Server) handlePatchConversation(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	conv, ok := s.mustConversation(w, r)
	if !ok {
		return
	}
	var req patchConversationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	changed := map[string]any{}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" || utf8.RuneCountInString(title) > 100 {
			s.writeErr(w, errInvalid("会话标题不合法"))
			return
		}
		conv.Title = title
		changed["title"] = title
	}
	if req.Topic != nil {
		conv.Topic = strings.TrimSpace(*req.Topic)
		changed["topic"] = conv.Topic
	}
	if req.Visibility != nil {
		v, err := normalizeConversationVisibility(*req.Visibility)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		conv.Visibility = v
		changed["visibility"] = v
	}
	if req.ExpiresAt != nil {
		if *req.ExpiresAt != "" {
			if _, err := time.Parse(time.RFC3339, *req.ExpiresAt); err != nil {
				s.writeErr(w, errInvalid("expires_at 必须是 RFC3339 时间"))
				return
			}
		}
		conv.ExpiresAt = *req.ExpiresAt
		changed["expires_at"] = conv.ExpiresAt
	}
	if req.MemberIDs != nil {
		desired := map[string]bool{}
		for _, id := range *req.MemberIDs {
			id = strings.TrimSpace(id)
			if id == "" || id == owner.ID {
				continue
			}
			if _, err := s.store.GetAgent(ctx, id); err != nil {
				s.writeErr(w, errInvalid("成员不存在: "+id))
				return
			}
			desired[id] = true
		}
		members, err := s.store.ListConversationMembers(ctx, conv.ID)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		for _, m := range members {
			if m.PrincipalType != domain.SenderAgent {
				continue
			}
			if !desired[m.PrincipalID] {
				if err := s.store.RemoveConversationMember(ctx, conv.ID, m.PrincipalType, m.PrincipalID); err != nil {
					s.writeErr(w, err)
					return
				}
			}
			delete(desired, m.PrincipalID)
		}
		for id := range desired {
			if err := s.store.AddConversationMember(ctx, domain.ConversationMember{
				ConversationID: conv.ID, PrincipalType: domain.SenderAgent, PrincipalID: id, Role: "member",
			}); err != nil {
				s.writeErr(w, err)
				return
			}
		}
		changed["members"] = len(*req.MemberIDs)
	}
	if len(changed) > 0 {
		updated, err := s.store.UpdateConversation(ctx, *conv)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		conv = &updated
		s.audit(ctx, domain.SenderUser, owner.ID, "conversation.update", "conversation", conv.ID, "ok", jsonDetail(changed))
	}
	dto, err := s.conversationDTO(ctx, *conv)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, dto)
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	conv, ok := s.mustConversation(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	before, err := parseInt64Param(q.Get("before"), 0)
	if err != nil {
		s.writeErr(w, errInvalid("before 不合法"))
		return
	}
	after, err := parseInt64Param(q.Get("after"), 0)
	if err != nil {
		s.writeErr(w, errInvalid("after 不合法"))
		return
	}
	limit, err := parseInt64Param(q.Get("limit"), 50)
	if err != nil || limit <= 0 || limit > 200 {
		s.writeErr(w, errInvalid("limit 必须在 1..200"))
		return
	}
	msgs, err := s.store.ListMessages(ctx, conv.ID, before, after, int(limit))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	total, err := s.store.CountMessages(ctx, conv.ID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	maxSeq, err := s.store.MaxMessageSeq(ctx, conv.ID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	next := ""
	if len(msgs) == int(limit) && len(msgs) > 0 {
		// 默认/before 模式返回升序页，下一页应往前取更旧的：用页首（最旧）seq；
		// after 模式是向前同步，下一页用页尾（最新）seq。
		switch {
		case after > 0:
			next = strconv.FormatInt(msgs[len(msgs)-1].Seq, 10)
		default:
			next = strconv.FormatInt(msgs[0].Seq, 10)
		}
	}
	writeOK(w, messageListDTO{Items: msgs, Total: total, Next: next, MaxSeq: maxSeq})
}

type postMessageRequest struct {
	Content  string          `json:"content"`
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Meta     json.RawMessage `json:"meta"`
	ClientID string          `json:"client_id"`
}

// normalizeMeta 接受 JSON 字符串或 JSON object 两种 meta 形式（契约 DTO 为字符串）。
func normalizeMeta(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", errInvalid("meta 不合法")
		}
		if strings.TrimSpace(s) == "" {
			return "", nil
		}
		if !json.Valid([]byte(s)) {
			return "", errInvalid("meta 必须是 JSON 字符串或对象")
		}
		return s, nil
	}
	if !json.Valid(raw) {
		return "", errInvalid("meta 不合法")
	}
	return string(raw), nil
}

func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	conv, ok := s.mustConversation(w, r)
	if !ok {
		return
	}
	if conv.Status == "archived" {
		s.writeErr(w, errConflict("会话已归档，不能发送消息"))
		return
	}
	// 全局紧急停止：写库前预检，不落库/不索引（core Enqueue 也会拒绝，这里给用户可读反馈）。
	if v, ok := s.settingValue("emergency_stop"); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "on":
			s.writeErr(w, domain.NewError("emergency_stop", "全局紧急停止中，请恢复后再发消息", http.StatusServiceUnavailable))
			return
		}
	}
	var req postMessageRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	msgType := strings.TrimSpace(req.Type)
	if msgType == "" {
		msgType = domain.MsgText
	}
	if !userMessageTypes[msgType] {
		s.writeErr(w, errInvalid("用户只能发送 text/card 消息"))
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		s.writeErr(w, errInvalid("消息内容不能为空"))
		return
	}
	if len(req.Content) > maxUserContentBytes {
		s.writeErr(w, errInvalid("消息过长（上限 64KB）"))
		return
	}
	meta, err := normalizeMeta(req.Meta)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	clientID := strings.TrimSpace(req.ClientID)
	if utf8.RuneCountInString(clientID) > 200 {
		s.writeErr(w, errInvalid("client_id 过长"))
		return
	}
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID != "" {
		parent, err := s.store.GetMessage(ctx, threadID)
		if err != nil {
			s.writeErr(w, errInvalid("thread_id 指向的消息不存在"))
			return
		}
		if parent.ConversationID != conv.ID {
			s.writeErr(w, errInvalid("thread_id 必须属于同一会话"))
			return
		}
		if parent.ThreadID != "" {
			s.writeErr(w, errInvalid("线程最多两级，不能在子消息下再回复"))
			return
		}
	}
	msg, _, created, err := s.store.InsertMessage(ctx, store.NewMessage{
		ConversationID: conv.ID,
		ThreadID:       threadID,
		SenderType:     domain.SenderUser,
		SenderID:       owner.ID,
		SenderName:     owner.Name,
		Type:           msgType,
		Content:        req.Content,
		Meta:           meta,
		ClientID:       clientID,
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if created && msg != nil {
		// 事务已提交：先落库后投递/广播。
		s.onUserMessage(*msg)
	}
	writeOK(w, map[string]any{"message": msg, "created": created})
}

type markReadRequest struct {
	Seq int64 `json:"seq"`
}

func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	conv, ok := s.mustConversation(w, r)
	if !ok {
		return
	}
	var req markReadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	maxSeq, err := s.store.MaxMessageSeq(ctx, conv.ID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	seq := req.Seq
	if seq < 0 {
		s.writeErr(w, errInvalid("seq 不能为负"))
		return
	}
	if seq > maxSeq {
		seq = maxSeq
	}
	if err := s.store.SetReadSeq(ctx, conv.ID, seq); err != nil {
		s.writeErr(w, err)
		return
	}
	readSeq, err := s.store.GetReadSeq(ctx, conv.ID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	unread, err := s.store.CountUnread(ctx, conv.ID, readSeq)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"read_seq": readSeq, "unread_count": unread})
}

func (s *Server) handleJoinConversation(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	conv, ok := s.mustConversation(w, r)
	if !ok {
		return
	}
	if err := s.store.AddConversationMember(ctx, domain.ConversationMember{
		ConversationID: conv.ID, PrincipalType: domain.SenderUser, PrincipalID: owner.ID, Role: "member",
	}); err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "conversation.join", "conversation", conv.ID, "ok", "{}")
	writeOK(w, map[string]any{"conversation_id": conv.ID, "joined": true})
}

func (s *Server) handleLeaveConversation(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	conv, ok := s.mustConversation(w, r)
	if !ok {
		return
	}
	if err := s.store.RemoveConversationMember(ctx, conv.ID, domain.SenderUser, owner.ID); err != nil {
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "conversation.leave", "conversation", conv.ID, "ok", "{}")
	writeOK(w, map[string]any{"conversation_id": conv.ID, "joined": false})
}

type editMessageRequest struct {
	Content string `json:"content"`
}

func (s *Server) loadOwnMessage(w http.ResponseWriter, r *http.Request, owner *store.Owner) (*domain.Message, bool) {
	msg, err := s.store.GetMessage(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return nil, false
	}
	if msg.SenderType != domain.SenderUser || msg.SenderID != owner.ID {
		s.writeErr(w, errForbidden("只能操作自己发送的消息"))
		return nil, false
	}
	if msg.DeletedAt != "" {
		s.writeErr(w, errConflict("消息已删除"))
		return nil, false
	}
	return &msg, true
}

func (s *Server) handleEditMessage(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	msg, ok := s.loadOwnMessage(w, r, owner)
	if !ok {
		return
	}
	var req editMessageRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, err)
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		s.writeErr(w, errInvalid("消息内容不能为空"))
		return
	}
	if len(req.Content) > maxUserContentBytes {
		s.writeErr(w, errInvalid("消息过长（上限 64KB）"))
		return
	}
	updated, _, err := s.store.EditMessage(ctx, msg.ID, req.Content, store.Actor{Type: domain.SenderUser, ID: owner.ID})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"message": updated})
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	msg, ok := s.loadOwnMessage(w, r, owner)
	if !ok {
		return
	}
	updated, _, err := s.store.DeleteMessage(ctx, msg.ID, store.Actor{Type: domain.SenderUser, ID: owner.ID})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"message": updated, "deleted": true})
}

func parseInt64Param(raw string, fallback int64) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

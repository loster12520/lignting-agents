package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

// 附件（FR-CHAT-02 / v2 表）：类型/大小限制可配置，下载强制 attachment + nosniff + CSP sandbox。
const (
	defaultAttachmentMaxBytes = 25 << 20 // 25MB
	settingAttachmentMaxBytes = "attachment.max_bytes"
)

var attachmentTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".svg": "image/svg+xml",
	".pdf": "application/pdf", ".txt": "text/plain; charset=utf-8",
	".md": "text/markdown; charset=utf-8", ".json": "application/json; charset=utf-8",
	".csv": "text/csv; charset=utf-8", ".log": "text/plain; charset=utf-8",
	".zip":  "application/zip",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls":  "application/vnd.ms-excel",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".ppt":  "application/vnd.ms-powerpoint",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

func (s *Server) attachmentMaxBytes() int64 {
	if v, ok := s.settingValue(settingAttachmentMaxBytes); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 && n <= 1<<30 {
			return n
		}
	}
	return defaultAttachmentMaxBytes
}

// SettingWithContext 是 store.Setting 的上下文包装（避免 handler 里到处拼 ctx）。
func (s *Server) settingValue(key string) (string, bool) {
	ctx, cancel := s.opCtx()
	defer cancel()
	v, ok, err := s.store.Setting(ctx, key)
	if err != nil {
		return "", false
	}
	return v, ok
}

func sanitizeAttachmentName(raw string) string {
	raw = strings.ReplaceAll(raw, string(rune(92)), "/")
	name := filepath.Base(raw)
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	name = b.String()
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	if utf8.RuneCountInString(name) > 200 {
		r := []rune(name)
		name = string(r[:200])
	}
	return name
}

// handleCreateAttachment POST /api/conversations/{id}/attachments（multipart 字段 file）。
func (s *Server) handleCreateAttachment(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	conv, err := s.store.GetConversation(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if conv.Status == "archived" {
		s.writeErr(w, errConflict("会话已归档，不能上传附件"))
		return
	}
	maxBytes := s.attachmentMaxBytes()
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1<<20)
	if err := r.ParseMultipartForm(maxBytes + 1<<20); err != nil {
		s.writeErr(w, domain.NewError(domain.CodeInvalid, "multipart 请求不合法或超过大小上限", http.StatusRequestEntityTooLarge))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		s.writeErr(w, errInvalid("缺少 file 字段"))
		return
	}
	defer file.Close()

	filename := sanitizeAttachmentName(header.Filename)
	ext := strings.ToLower(filepath.Ext(filename))
	mime, allowed := attachmentTypes[ext]
	if !allowed {
		s.writeErr(w, errInvalid("不支持的附件类型："+ext))
		return
	}
	messageID := strings.TrimSpace(r.FormValue("message_id"))
	if messageID != "" {
		msg, err := s.store.GetMessage(ctx, messageID)
		if err != nil || msg.ConversationID != conv.ID {
			s.writeErr(w, errInvalid("message_id 不属于该会话"))
			return
		}
	}

	id := domain.NewID("att")
	dir := filepath.Join(s.cfg.DataDir, "attachments", id[:2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.logf("创建附件目录失败: %v", err)
		s.writeErr(w, domain.NewError(domain.CodeUnavailable, "附件存储不可用", http.StatusServiceUnavailable))
		return
	}
	finalPath := filepath.Join(dir, id+ext)
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		s.logf("创建附件临时文件失败: %v", err)
		s.writeErr(w, domain.NewError(domain.CodeUnavailable, "附件存储不可用", http.StatusServiceUnavailable))
		return
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	h := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(file, maxBytes+1))
	if copyErr != nil {
		s.writeErr(w, errInvalid("读取上传文件失败"))
		return
	}
	if written > maxBytes {
		s.writeErr(w, domain.NewError(domain.CodeInvalid, "附件超过大小上限", http.StatusRequestEntityTooLarge))
		return
	}
	if err := tmp.Sync(); err != nil {
		s.logf("附件落盘失败: %v", err)
		s.writeErr(w, domain.NewError(domain.CodeUnavailable, "附件落盘失败", http.StatusServiceUnavailable))
		return
	}
	if err := tmp.Close(); err != nil {
		s.writeErr(w, domain.NewError(domain.CodeUnavailable, "附件落盘失败", http.StatusServiceUnavailable))
		return
	}
	if err := os.Rename(tmpName, finalPath); err != nil {
		s.logf("附件重命名失败: %v", err)
		s.writeErr(w, domain.NewError(domain.CodeUnavailable, "附件落盘失败", http.StatusServiceUnavailable))
		return
	}
	attachment, err := s.store.CreateAttachment(ctx, store.Attachment{
		ID: id, ConversationID: conv.ID, MessageID: messageID, Filename: filename, Mime: mime,
		Size: written, SHA256: hex.EncodeToString(h.Sum(nil)),
		StoragePath: filepath.ToSlash(finalPath), CreatedBy: owner.ID,
	})
	if err != nil {
		_ = os.Remove(finalPath)
		s.writeErr(w, err)
		return
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "attachment.upload", "attachment", attachment.ID, "ok",
		jsonDetail(map[string]any{"conversation_id": conv.ID, "filename": filename, "size": written, "mime": mime}))
	writeOK(w, map[string]any{
		"id": attachment.ID, "conversation_id": attachment.ConversationID, "message_id": attachment.MessageID,
		"filename": attachment.Filename, "mime": attachment.Mime, "size": attachment.Size,
		"sha256": attachment.SHA256, "created_at": attachment.CreatedAt,
	})
}

// attachmentAccessAllowed：owner 全可读；Agent Token 需可见该会话（私有/人类专属要成员）。
func (s *Server) attachmentAccessAllowed(r *http.Request, a store.Attachment) (string, string, bool, error) {
	ctx := r.Context()
	owner, err := s.currentOwner(r)
	if err != nil {
		return "", "", false, err
	}
	if owner != nil {
		return domain.SenderUser, owner.ID, true, nil
	}
	agent, err := s.agentFromToken(ctx, r)
	if err != nil {
		return "", "", false, err
	}
	if agent == nil {
		return "", "", false, errUnauthorized("请先登录")
	}
	conv, err := s.store.GetConversation(ctx, a.ConversationID)
	if err != nil {
		return "", "", false, err
	}
	if conv.Visibility == domain.VisibilityPrivate || conv.Visibility == domain.VisibilityHumanOnly {
		members, err := s.store.ListConversationMembers(ctx, conv.ID)
		if err != nil {
			return "", "", false, err
		}
		for _, m := range members {
			if m.PrincipalType == domain.SenderAgent && m.PrincipalID == agent.ID {
				return domain.SenderAgent, agent.ID, true, nil
			}
		}
		return domain.SenderAgent, agent.ID, false, nil
	}
	switch agent.Kind {
	case domain.AgentKindAssistant:
		return domain.SenderAgent, agent.ID, true, nil
	case domain.AgentKindSupervisor, domain.AgentKindEmployee:
		if conv.DepartmentID != "" && conv.DepartmentID == agent.DepartmentID {
			return domain.SenderAgent, agent.ID, true, nil
		}
	}
	return domain.SenderAgent, agent.ID, false, nil
}

// handleDownloadAttachment GET /api/attachments/{id}（会话/Agent Token 鉴权；强制附件下载）。
func (s *Server) handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	attachment, err := s.store.GetAttachment(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if attachment.DeletedAt != "" {
		s.writeErr(w, errNotFound("附件不存在或已删除"))
		return
	}
	actorType, actorID, allowed, err := s.attachmentAccessAllowed(r, attachment)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if !allowed {
		s.audit(ctx, actorType, actorID, "attachment.download", "attachment", attachment.ID, "denied", "{}")
		s.writeErr(w, errForbidden("无权下载该附件"))
		return
	}
	f, err := os.Open(attachment.StoragePath)
	if err != nil {
		s.logf("附件文件缺失 %s: %v", attachment.ID, err)
		s.writeErr(w, errNotFound("附件内容不存在"))
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		s.writeErr(w, errNotFound("附件内容不存在"))
		return
	}
	h := w.Header()
	h.Set("Content-Type", attachment.Mime)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", "attachment; filename*=UTF-8''"+urlEncodeFilename(attachment.Filename))
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "no-store")
	http.ServeContent(w, r, attachment.Filename, info.ModTime(), f)
}

func urlEncodeFilename(name string) string {
	var b strings.Builder
	for _, c := range []byte(name) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteString("%")
			const hexDigits = "0123456789ABCDEF"
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xf])
		}
	}
	return b.String()
}

// handleDeleteAttachment DELETE /api/attachments/{id}（墓碑 + 删除磁盘文件 + 审计）。
func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	ctx := r.Context()
	attachment, err := s.store.SoftDeleteAttachment(ctx, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if attachment.StoragePath != "" {
		if err := os.Remove(attachment.StoragePath); err != nil && !os.IsNotExist(err) {
			s.logf("删除附件文件失败 %s: %v", attachment.ID, err)
		}
	}
	s.audit(ctx, domain.SenderUser, owner.ID, "attachment.delete", "attachment", attachment.ID, "ok", "{}")
	writeOK(w, map[string]any{"deleted": true, "id": attachment.ID})
}

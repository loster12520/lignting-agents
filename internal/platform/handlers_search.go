package platform

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/search"
	"github.com/lignting/lignting-agents/internal/store"
)

const fixedTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

var searchKinds = map[string]bool{
	"message":  true,
	"task":     true,
	"decision": true,
	"artifact": true,
}

// normalizeTimeParam 接受 RFC3339 或 YYYY-MM-DD，统一成 store 的固定 9 位时间格式。
func normalizeTimeParam(raw string, endOfDay bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC().Format(fixedTimeLayout), nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		if endOfDay {
			return t.Add(24*time.Hour - time.Nanosecond).UTC().Format(fixedTimeLayout), nil
		}
		return t.UTC().Format(fixedTimeLayout), nil
	}
	return "", errInvalid("时间格式必须是 RFC3339 或 YYYY-MM-DD")
}

// searchACL 计算检索视野：
//   - owner 会话（无 Agent Token）：全部可见。
//   - Agent Token（X-Lignting-Agent-Token）：按角色/部门/会话成员做 SQL 前置过滤；
//     human_only 对任何 Agent 都不可见（只能经 memory.read 显式单条读取）。
//
// 返回 restrict=false 表示不限制（owner）。
func (s *Server) searchACL(ctx context.Context, r *http.Request) (vis []string, convIDs []string, restrict bool, err error) {
	token := strings.TrimSpace(r.Header.Get("X-Lignting-Agent-Token"))
	if token == "" {
		return nil, nil, false, nil
	}
	agent, err := s.store.FindAgentByTokenHash(ctx, store.HashToken(token))
	if err != nil {
		return nil, nil, true, errUnauthorized("Agent Token 无效")
	}
	vis = []string{domain.VisibilityDepartment, domain.VisibilityProject, domain.VisibilityOrg}
	convs, err := s.store.ListConversations(ctx, store.ConversationFilter{})
	if err != nil {
		return nil, nil, true, err
	}
	memberOf := map[string]bool{}
	for _, c := range convs {
		members, err := s.store.ListConversationMembers(ctx, c.ID)
		if err != nil {
			return nil, nil, true, err
		}
		for _, m := range members {
			if m.PrincipalType == domain.SenderAgent && m.PrincipalID == agent.ID {
				memberOf[c.ID] = true
			}
		}
		if c.Visibility == domain.VisibilityPrivate || c.Visibility == domain.VisibilityHumanOnly {
			continue
		}
		switch agent.Kind {
		case domain.AgentKindAssistant:
			// 助理可见所有非私有群聊（项目/部门/跨部门），用于协调与汇总。
			memberOf[c.ID] = true
		case domain.AgentKindSupervisor, domain.AgentKindEmployee:
			// 主管/员工默认只在本部门群聊与本人所在会话可见。
			if c.DepartmentID != "" && c.DepartmentID == agent.DepartmentID {
				memberOf[c.ID] = true
			}
		}
	}
	convIDs = make([]string, 0, len(memberOf))
	for id := range memberOf {
		convIDs = append(convIDs, id)
	}
	return vis, convIDs, true, nil
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	_ = owner
	ctx := r.Context()
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	if query == "" {
		s.writeErr(w, errInvalid("q 不能为空"))
		return
	}
	var kinds []string
	for _, k := range strings.Split(q.Get("kind"), ",") {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if !searchKinds[k] {
			s.writeErr(w, errInvalid("不支持的 kind: "+k))
			return
		}
		kinds = append(kinds, k)
	}
	from, err := normalizeTimeParam(q.Get("from"), false)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	to, err := normalizeTimeParam(q.Get("to"), true)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	limit, err := parseInt64Param(q.Get("limit"), 20)
	if err != nil || limit <= 0 || limit > 100 {
		s.writeErr(w, errInvalid("limit 必须在 1..100"))
		return
	}
	offset, err := parseInt64Param(q.Get("offset"), 0)
	if err != nil || offset < 0 {
		s.writeErr(w, errInvalid("offset 不合法"))
		return
	}
	vis, convIDs, restrict, err := s.searchACL(ctx, r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if restrict && len(convIDs) == 0 {
		writeList(w, []any{}, 0, "")
		return
	}
	f := search.Filter{
		Kinds:               kinds,
		ConversationID:      q.Get("conversation_id"),
		ConversationIDs:     convIDs,
		DepartmentID:        q.Get("department_id"),
		SenderID:            q.Get("sender_id"),
		AllowedVisibilities: vis,
		From:                from,
		To:                  to,
		Limit:               int(limit),
		Offset:              int(offset),
	}
	rs, err := search.Search(ctx, s.store.DB, query, f)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	msgIDs := make([]string, 0, len(rs.Items))
	for _, it := range rs.Items {
		if it.Kind == "message" {
			msgIDs = append(msgIDs, it.RefID)
		}
	}
	seqs := map[string]int64{}
	if len(msgIDs) > 0 {
		if seqs, err = s.store.MessageSeqsByIDs(ctx, msgIDs); err != nil {
			s.writeErr(w, err)
			return
		}
	}
	convTitles := map[string]string{}
	for _, it := range rs.Items {
		if it.ConversationID == "" || convTitles[it.ConversationID] != "" {
			continue
		}
		if conv, err := s.store.GetConversation(ctx, it.ConversationID); err == nil {
			convTitles[it.ConversationID] = conv.Title
		}
	}
	items := make([]map[string]any, 0, len(rs.Items))
	for _, it := range rs.Items {
		item := map[string]any{
			"kind":            it.Kind,
			"ref_id":          it.RefID,
			"conversation_id": it.ConversationID,
			"department_id":   it.DepartmentID,
			"title":           it.Title,
			"snippet":         it.Snippet,
			"score":           it.Score,
			"created_at":      it.CreatedAt,
		}
		if it.ConversationID != "" {
			item["conversation_title"] = convTitles[it.ConversationID]
		}
		if it.Kind == "message" {
			if seq, ok := seqs[it.RefID]; ok {
				item["seq"] = seq
			}
		}
		items = append(items, item)
	}
	next := ""
	if int64(offset)+int64(len(items)) < rs.Total {
		next = strconv.FormatInt(offset+int64(len(items)), 10)
	}
	writeList(w, items, rs.Total, next)
}

func (s *Server) handleReindex(w http.ResponseWriter, r *http.Request, owner *store.Owner) {
	if !s.reindexMu.TryLock() {
		s.writeErr(w, errConflict("索引重建进行中，请稍后重试"))
		return
	}
	defer s.reindexMu.Unlock()
	s.setIndexState("building")
	defer s.setIndexState("ready")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	stats, err := search.Rebuild(ctx, s.store.DB)
	if err != nil {
		s.logf("重建检索索引失败: %v", err)
		s.writeErr(w, err)
		return
	}
	s.audit(r.Context(), domain.SenderUser, owner.ID, "search.reindex", "search_index", "", "ok",
		jsonDetail(map[string]any{"documents": stats.Documents, "terms": stats.Terms, "duration_ms": stats.DurationMS}))
	writeOK(w, stats)
}

package platform_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestMessageLifecycleAndIdempotency(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()

	data := e.postMessage(e.dmID, "登录接口要加限流", "client-1")
	msg, _ := data["message"].(map[string]any)
	if msg == nil || msg["content"] != "登录接口要加限流" {
		t.Fatalf("message = %v", msg)
	}
	if data["created"] != true {
		t.Fatalf("created = %v, want true", data["created"])
	}
	firstID, _ := msg["id"].(string)
	firstSeq, _ := msg["seq"].(float64)
	if firstID == "" || firstSeq <= 0 {
		t.Fatalf("bad message id/seq: %v", msg)
	}

	// 同一 client_id 重试：不重复落库、不重复事件
	data2 := e.postMessage(e.dmID, "登录接口要加限流", "client-1")
	msg2, _ := data2["message"].(map[string]any)
	if data2["created"] != false {
		t.Fatalf("second send created = %v, want false", data2["created"])
	}
	if msg2["id"] != firstID {
		t.Fatalf("idempotency failed: %v != %v", msg2["id"], firstID)
	}

	// 列表 = 1 条助理欢迎语 + 1 条用户消息（幂等重放不新增）
	status, out := e.do("GET", "/api/conversations/"+e.dmID+"/messages", nil)
	d := e.mustOK(status, out)
	items, _ := d["items"].([]any)
	userMsgs := 0
	for _, it := range items {
		if it.(map[string]any)["sender_type"] == "user" {
			userMsgs++
		}
	}
	if userMsgs != 1 || len(items) != 2 {
		t.Fatalf("messages = %d (user=%d), want 2 (user=1)", len(items), userMsgs)
	}

	// 会话列表带 last_seq/unread（自己的消息不算未读）
	status, out = e.do("GET", "/api/conversations", nil)
	d = e.mustOK(status, out)
	convs, _ := d["items"].([]any)
	if len(convs) != 1 {
		t.Fatalf("conversations = %d, want 1", len(convs))
	}
	conv := convs[0].(map[string]any)
	if conv["last_seq"].(float64) != firstSeq {
		t.Fatalf("last_seq = %v, want %v", conv["last_seq"], firstSeq)
	}
	if conv["unread_count"].(float64) != 1 {
		t.Fatalf("unread_count = %v, want 1 (assistant greeting; own message not unread)", conv["unread_count"])
	}

	// 已读位置
	status, out = e.doCSRF("POST", "/api/conversations/"+e.dmID+"/read", map[string]any{"seq": firstSeq})
	d = e.mustOK(status, out)
	if d["read_seq"].(float64) != firstSeq {
		t.Fatalf("read_seq = %v, want %v", d["read_seq"], firstSeq)
	}

	// 编辑 → 检索同步
	status, out = e.doCSRF("PATCH", "/api/messages/"+firstID, map[string]any{"content": "部署流水线跑通了"})
	e.mustOK(status, out)
	if rs := e.search(t, "限流"); rs["total"].(float64) != 0 {
		t.Fatalf("edited message should not match 限流: %v", rs)
	}
	if rs := e.search(t, "流水线"); rs["total"].(float64) != 1 {
		t.Fatalf("edited message should match 流水线: %v", rs)
	}

	// 删除 → 检索与列表都消失
	status, out = e.doCSRF("DELETE", "/api/messages/"+firstID, nil)
	d = e.mustOK(status, out)
	if d["deleted"] != true {
		t.Fatalf("delete response = %v", d)
	}
	status, out = e.do("GET", "/api/conversations/"+e.dmID+"/messages", nil)
	d = e.mustOK(status, out)
	if items, _ := d["items"].([]any); len(items) != 1 {
		t.Fatalf("after delete messages = %d, want 1 (greeting only)", len(items))
	}
	if rs := e.search(t, "流水线"); rs["total"].(float64) != 0 {
		t.Fatalf("deleted message still searchable: %v", rs)
	}

	// 非法消息
	if status, _ := e.doCSRF("POST", "/api/conversations/"+e.dmID+"/messages", map[string]any{"content": "x", "type": "system"}); status != http.StatusBadRequest {
		t.Fatalf("system type status = %d, want 400", status)
	}
	if status, _ := e.doCSRF("POST", "/api/conversations/"+e.dmID+"/messages", map[string]any{"content": "   "}); status != http.StatusBadRequest {
		t.Fatalf("empty content status = %d, want 400", status)
	}
}

func (e *env) search(t *testing.T, q string) map[string]any {
	t.Helper()
	status, out := e.do("GET", "/api/search?q="+q, nil)
	return e.mustOK(status, out)
}

func TestSearchAPIChineseTwoCharAndFilters(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	conv1 := e.createConversation("研发群", []string{e.assistantID})
	conv2 := e.createConversation("测试群", nil)
	e.postMessage(conv1, "登录接口要加限流，中文两字必须命中", "")
	e.postMessage(conv2, "部署流水线今天跑通了", "")

	rs := e.search(t, "限流")
	if rs["total"].(float64) != 1 {
		t.Fatalf("限流 total = %v, want 1", rs["total"])
	}
	items, _ := rs["items"].([]any)
	item := items[0].(map[string]any)
	if item["kind"] != "message" || item["conversation_id"] != conv1 {
		t.Fatalf("search item = %v", item)
	}
	snippet, _ := item["snippet"].(string)
	if !contains(snippet, "<em>限流</em>") {
		t.Fatalf("snippet missing highlight: %q", snippet)
	}
	if _, ok := item["seq"]; !ok {
		t.Fatalf("message result should include seq: %v", item)
	}
	if rs := e.search(t, "部署"); rs["total"].(float64) != 1 {
		t.Fatalf("部署 total = %v, want 1", rs["total"])
	}
	if status, out := e.do("GET", "/api/search?q=限流&conversation_id="+conv2, nil); status != http.StatusOK || e.data(out)["total"].(float64) != 0 {
		t.Fatalf("conversation filter failed: %v", out)
	}
	if rs := e.search(t, "完全没有的词"); rs["total"].(float64) != 0 {
		t.Fatalf("empty search total = %v, want 0", rs["total"])
	}
	if status, _ := e.do("GET", "/api/search?q=", nil); status != http.StatusBadRequest {
		t.Fatalf("empty q status = %d, want 400", status)
	}
	if status, _ := e.do("GET", "/api/search?q=限流&kind=bad", nil); status != http.StatusBadRequest {
		t.Fatalf("bad kind status = %d, want 400", status)
	}
}

func TestSSEReplaysSinceAndLastEventID(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	first := e.postMessage(e.dmID, "第一条 SSE", "")
	second := e.postMessage(e.dmID, "第二条 SSE", "")
	firstMsg, _ := first["message"].(map[string]any)
	secondMsg, _ := second["message"].(map[string]any)
	firstID, _ := firstMsg["id"].(string)
	secondID, _ := secondMsg["id"].(string)

	var firstSeq, secondSeq int64
	if err := e.st.DB.QueryRow("SELECT seq FROM events WHERE type='message.created' AND object_id=?", firstID).Scan(&firstSeq); err != nil {
		t.Fatalf("first event seq: %v", err)
	}
	if err := e.st.DB.QueryRow("SELECT seq FROM events WHERE type='message.created' AND object_id=?", secondID).Scan(&secondSeq); err != nil {
		t.Fatalf("second event seq: %v", err)
	}
	if firstSeq <= 0 || secondSeq <= firstSeq {
		t.Fatalf("event seqs = %d, %d", firstSeq, secondSeq)
	}

	// since=0：补发 conversation.created + 两条 message.created
	cancel, sc := e.openSSE("0", "")
	events := collectSSE(t, cancel, sc, 4, 5*time.Second)
	created := map[string]bool{}
	lastID := int64(0)
	for _, ev := range events {
		id, err := parseI64(ev.ID)
		if err != nil {
			t.Fatalf("bad SSE id %q: %v", ev.ID, err)
		}
		if id <= lastID {
			t.Fatalf("SSE ids not increasing: %d after %d", id, lastID)
		}
		lastID = id
		if ev.Type != "message.created" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
			t.Fatalf("event data not JSON: %v", err)
		}
		created[payload["object_id"].(string)] = true
	}
	if !created[firstID] || !created[secondID] {
		t.Fatalf("SSE replay missing messages: %v", created)
	}

	// Last-Event-ID 断线补齐：只补 seq > firstSeq
	cancel, sc = e.openSSE("", fmt.Sprintf("%d", firstSeq))
	events = collectSSE(t, cancel, sc, 2, 5*time.Second)
	if len(events) == 0 {
		t.Fatal("Last-Event-ID replay got no events")
	}
	var got []int64
	for _, ev := range events {
		id, _ := parseI64(ev.ID)
		got = append(got, id)
	}
	if got[0] != secondSeq {
		t.Fatalf("Last-Event-ID replay first id = %d, want %d", got[0], secondSeq)
	}
}

func TestRestartNoMessageLoss(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	e.postMessage(e.dmID, "重启后必须还在", "restart-1")
	e.restart()

	// 重启后 Cookie 失效则用本机免密登录
	status, out := e.do("POST", "/api/login", map[string]any{})
	d := e.mustOK(status, out)
	e.csrf, _ = d["csrf_token"].(string)
	if e.csrf == "" {
		t.Fatal("login after restart did not return csrf")
	}
	status, out = e.do("GET", "/api/conversations/"+e.dmID+"/messages", nil)
	d = e.mustOK(status, out)
	items, _ := d["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("messages after restart = %d, want 2 (greeting + user)", len(items))
	}
	foundUser := false
	for _, it := range items {
		m := it.(map[string]any)
		if m["sender_type"] == "user" && m["content"] == "重启后必须还在" {
			foundUser = true
		}
	}
	if !foundUser {
		t.Fatalf("user message content lost after restart: %v", items)
	}
	if rs := e.search(t, "重启"); rs["total"].(float64) != 1 {
		t.Fatalf("search after restart total = %v, want 1", rs["total"])
	}
}

func TestOrgReadEndpoints(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	status, out := e.do("GET", "/api/org", nil)
	d := e.mustOK(status, out)
	owner, _ := d["owner"].(map[string]any)
	if owner["id"] != "owner" {
		t.Fatalf("org owner = %v", owner)
	}
	assistant, _ := d["assistant"].(map[string]any)
	if assistant == nil || assistant["id"] != e.assistantID {
		t.Fatalf("org assistant = %v", assistant)
	}
	if status, out := e.do("GET", "/api/agents", nil); status != http.StatusOK {
		t.Fatalf("agents status = %d %v", status, out)
	}
	if status, out := e.do("GET", "/api/departments", nil); status != http.StatusOK {
		t.Fatalf("departments status = %d %v", status, out)
	}
	if status, _ := e.do("GET", "/api/agents/agent_missing", nil); status != http.StatusNotFound {
		t.Fatalf("missing agent status = %d, want 404", status)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func parseI64(s string) (int64, error) {
	var v int64
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		v = v*10 + int64(c-'0')
	}
	return v, nil
}

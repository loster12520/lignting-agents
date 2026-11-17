package platform_test

import (
	"strconv"
	"strings"
	"testing"
)

// TestSetupCreatesAssistantGreeting 覆盖 AC-01：setup 后助理主动出现（静态欢迎辞 + 事件 + 审计）。
func TestSetupCreatesAssistantGreeting(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()

	status, out := e.do("GET", "/api/conversations/"+e.dmID+"/messages", nil)
	d := e.mustOK(status, out)
	items, _ := d["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("setup DM messages = %d, want 1 greeting", len(items))
	}
	greeting := items[0].(map[string]any)
	if greeting["sender_type"] != "agent" || greeting["sender_id"] != e.assistantID {
		t.Fatalf("greeting sender = %v/%v, want agent/%s", greeting["sender_type"], greeting["sender_id"], e.assistantID)
	}
	meta, _ := greeting["meta"].(string)
	if !strings.Contains(meta, "greeting") {
		t.Fatalf("greeting meta = %q, want kind=greeting", meta)
	}
	content, _ := greeting["content"].(string)
	for _, want := range []string{"我能做什么", "我不做什么", "如何暂停我", "如何查看我的行为", "下一步"} {
		if !strings.Contains(content, want) {
			t.Fatalf("greeting missing %q: %q", want, content)
		}
	}

	var events, audits int
	if err := e.st.DB.QueryRow("SELECT COUNT(*) FROM events WHERE type='message.created' AND actor_type='agent'").Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 1 {
		t.Fatalf("agent message.created events = %d, want 1", events)
	}
	if err := e.st.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='message.create' AND actor_type='agent'").Scan(&audits); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audits != 1 {
		t.Fatalf("agent message.create audit = %d, want 1", audits)
	}
}

// TestMessagePaginationNoOverlap 覆盖 task-24：limit=2 连续翻页无重复无遗漏，next=="" 表示到底。
func TestMessagePaginationNoOverlap(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	conv := e.createConversation("分页测试", nil)
	for i := 1; i <= 5; i++ {
		e.postMessage(conv, "分页消息 "+strconv.Itoa(i), "")
	}

	// 向后翻页：默认（最新页）+ before=next
	seen := map[int64]bool{}
	var before int64
	for page := 0; page < 10; page++ {
		path := "/api/conversations/" + conv + "/messages?limit=2"
		if before > 0 {
			path += "&before=" + strconv.FormatInt(before, 10)
		}
		status, out := e.do("GET", path, nil)
		d := e.mustOK(status, out)
		items, _ := d["items"].([]any)
		for _, it := range items {
			seq := int64(it.(map[string]any)["seq"].(float64))
			if seen[seq] {
				t.Fatalf("backward pagination duplicated seq %d (page %d)", seq, page)
			}
			seen[seq] = true
		}
		next, _ := d["next"].(string)
		if next == "" {
			break
		}
		var err error
		before, err = strconv.ParseInt(next, 10, 64)
		if err != nil {
			t.Fatalf("next = %q not int", next)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("backward pages covered %d messages, want 5", len(seen))
	}

	// 向前翻页：after=next
	minSeq := int64(1 << 62)
	for seq := range seen {
		if seq < minSeq {
			minSeq = seq
		}
	}
	seenFwd := map[int64]bool{}
	after := minSeq - 1
	for page := 0; page < 10; page++ {
		path := "/api/conversations/" + conv + "/messages?limit=2&after=" + strconv.FormatInt(after, 10)
		status, out := e.do("GET", path, nil)
		d := e.mustOK(status, out)
		items, _ := d["items"].([]any)
		for _, it := range items {
			seq := int64(it.(map[string]any)["seq"].(float64))
			if seenFwd[seq] {
				t.Fatalf("forward pagination duplicated seq %d (page %d)", seq, page)
			}
			seenFwd[seq] = true
		}
		next, _ := d["next"].(string)
		if next == "" {
			break
		}
		var err error
		after, err = strconv.ParseInt(next, 10, 64)
		if err != nil {
			t.Fatalf("next = %q not int", next)
		}
	}
	if len(seenFwd) != 5 {
		t.Fatalf("forward pages covered %d messages, want 5", len(seenFwd))
	}
}

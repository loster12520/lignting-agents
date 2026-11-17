package platform_test

import (
	"context"
	"net/http"
	"testing"
)

func TestEmergencyStopBlocksNewMessages(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()

	before := messageCount(t, e)
	if err := e.st.SetSetting(ctx, "emergency_stop", "true"); err != nil {
		t.Fatalf("set emergency: %v", err)
	}
	status, out := e.doCSRF("POST", "/api/conversations/"+e.dmID+"/messages", map[string]any{"content": "紧急停止时不该落库"})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("emergency send = %d %v, want 503", status, out)
	}
	errObj, _ := out["error"].(map[string]any)
	if errObj["code"] != "emergency_stop" {
		t.Fatalf("error code = %v, want emergency_stop", errObj)
	}
	if after := messageCount(t, e); after != before {
		t.Fatalf("messages changed during emergency stop: %d -> %d", before, after)
	}

	// 恢复后正常发送
	if err := e.st.SetSetting(ctx, "emergency_stop", "false"); err != nil {
		t.Fatalf("clear emergency: %v", err)
	}
	status, out = e.doCSRF("POST", "/api/conversations/"+e.dmID+"/messages", map[string]any{"content": "恢复后正常消息"})
	if status != http.StatusOK {
		t.Fatalf("resume send = %d %v, want 200", status, out)
	}
	if after := messageCount(t, e); after != before+1 {
		t.Fatalf("messages after resume = %d, want %d", after, before+1)
	}
}

func messageCount(t *testing.T, e *env) int64 {
	t.Helper()
	var n int64
	if err := e.st.DB.QueryRow("SELECT COUNT(*) FROM messages WHERE conversation_id=?", e.dmID).Scan(&n); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return n
}

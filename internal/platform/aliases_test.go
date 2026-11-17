package platform_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lignting/lignting-agents/internal/memory"
)

func TestVisibilityAndEntityTypeAliases(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	svc, err := memory.NewService(memory.Options{Repo: memory.NewStoreRepo(e.st), Config: memory.Config{Enabled: true}})
	if err != nil {
		t.Fatalf("memory service: %v", err)
	}
	e.srv.SetMemoryService(svc)

	// 记忆：human_only → human-only；ability → capability
	status, out := e.doCSRF("POST", "/api/memories", map[string]any{
		"direct": true, "scope": "assistant", "content": "人类专属：偏好先写测试",
		"visibility": "human_only", "entity_type": "ability", "entity_key": "owner.testing",
	})
	if status != http.StatusOK {
		t.Fatalf("memory alias = %d %v", status, out)
	}
	d := e.data(out)
	if d["visibility"] != "human-only" {
		t.Fatalf("visibility = %v, want human-only", d["visibility"])
	}
	if d["entity_type"] != "capability" {
		t.Fatalf("entity_type = %v, want capability", d["entity_type"])
	}

	// 记忆旧别名：agent_private → agent-private，team 保持 team
	status, out = e.doCSRF("POST", "/api/memories", map[string]any{
		"direct": true, "scope": "assistant", "content": "私有记忆", "agent_id": e.assistantID,
		"visibility": "agent_private", "entity_type": "person", "entity_key": "a",
	})
	d = e.data(out)
	if status != http.StatusOK || d["visibility"] != "agent-private" {
		t.Fatalf("agent_private alias = %d %v", status, out)
	}
	status, out = e.doCSRF("POST", "/api/memories", map[string]any{
		"direct": true, "scope": "assistant", "content": "团队记忆",
		"visibility": "team", "entity_type": "capability", "entity_key": "b",
	})
	if d := e.data(out); status != http.StatusOK || d["visibility"] != "team" {
		t.Fatalf("team alias = %d %v", status, out)
	}
	// 未知 entity_type / visibility → 400
	if status, _ := e.doCSRF("POST", "/api/memories", map[string]any{
		"direct": true, "scope": "assistant", "content": "x", "visibility": "team", "entity_type": "bogus", "entity_key": "c",
	}); status != http.StatusBadRequest {
		t.Fatalf("unknown entity_type status = %d, want 400", status)
	}
	if status, _ := e.doCSRF("POST", "/api/memories", map[string]any{
		"direct": true, "scope": "assistant", "content": "x", "visibility": "bogus", "entity_type": "person", "entity_key": "c",
	}); status != http.StatusBadRequest {
		t.Fatalf("unknown visibility status = %d, want 400", status)
	}

	// 会话可见性别名
	status, out = e.doCSRF("POST", "/api/conversations", map[string]any{
		"type": "project", "title": "别名会话", "visibility": "human_only",
	})
	d = e.data(out)
	convID, _ := d["id"].(string)
	// 会话词表与记忆词表不同：会话 canonical = human_only（契约 §12.4），记忆 = human-only。
	if status != http.StatusOK || d["visibility"] != "human_only" {
		t.Fatalf("conversation human_only alias = %d %v", status, out)
	}
	status, out = e.doCSRF("PATCH", "/api/conversations/"+convID, map[string]any{"visibility": "agent_private"})
	if d := e.data(out); status != http.StatusOK || d["visibility"] != "private" {
		t.Fatalf("conversation agent_private alias = %d %v", status, out)
	}
	status, out = e.doCSRF("POST", "/api/conversations", map[string]any{"type": "project", "title": "team", "visibility": "team"})
	if d := e.data(out); status != http.StatusOK || d["visibility"] != "department" {
		t.Fatalf("conversation team alias = %d %v", status, out)
	}
	if status, _ := e.doCSRF("POST", "/api/conversations", map[string]any{"type": "project", "title": "bad", "visibility": "bogus"}); status != http.StatusBadRequest {
		t.Fatalf("conversation bad visibility status = %d, want 400", status)
	}
	_ = context.Background()
}

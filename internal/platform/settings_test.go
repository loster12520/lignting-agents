package platform_test

import (
	"net/http"
	"testing"

	"github.com/lignting/lignting-agents/internal/config"
)

func TestSettingsAllowlistAndAPIKeyEndpoints(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()

	status, out := e.do("GET", "/api/settings", nil)
	d := e.mustOK(status, out)
	if d["owner_name"] != "阿哲" {
		t.Fatalf("owner_name = %v", d["owner_name"])
	}
	if d["has_api_key"] != true {
		t.Fatalf("has_api_key = %v, want true", d["has_api_key"])
	}
	for _, forbidden := range []string{"deepseek_api_key", "api_key", "session.secret", "password_hash"} {
		if _, ok := d[forbidden]; ok {
			t.Fatalf("settings must not expose %q: %v", forbidden, d)
		}
	}

	// PUT 设置
	status, out = e.doCSRF("PUT", "/api/settings", map[string]any{
		"owner_name": "阿哲2", "daily_budget_cny": 5.5, "locale": "zh-CN",
	})
	d = e.mustOK(status, out)
	if d["owner_name"] != "阿哲2" || d["daily_budget_cny"].(float64) != 5.5 {
		t.Fatalf("updated settings = %v", d)
	}
	// owner 名称同步到 bootstrap
	status, out = e.do("GET", "/api/bootstrap", nil)
	d = e.mustOK(status, out)
	owner := d["owner"].(map[string]any)
	if owner["name"] != "阿哲2" {
		t.Fatalf("bootstrap owner name = %v", owner["name"])
	}
	if status, _ := e.doCSRF("PUT", "/api/settings", map[string]any{"daily_budget_cny": -1}); status != http.StatusBadRequest {
		t.Fatalf("negative budget status = %d, want 400", status)
	}

	// 密钥：写入 secrets.json，但 API 不回显
	status, out = e.doCSRF("PUT", "/api/settings/api-key", map[string]any{"api_key": "sk-new-key-12345678"})
	d = e.mustOK(status, out)
	if d["has_api_key"] != true {
		t.Fatalf("set api key = %v", d)
	}
	secrets, err := config.LoadSecrets(e.dataDir)
	if err != nil || secrets.DeepSeekAPIKey != "sk-new-key-12345678" {
		t.Fatalf("secrets on disk = %q err=%v", secrets.DeepSeekAPIKey, err)
	}
	status, out = e.do("GET", "/api/settings", nil)
	d = e.mustOK(status, out)
	if _, ok := d["deepseek_api_key"]; ok {
		t.Fatal("api key leaked in settings response")
	}
	// 清除
	status, out = e.doCSRF("DELETE", "/api/settings/api-key", nil)
	d = e.mustOK(status, out)
	if d["has_api_key"] != false {
		t.Fatalf("clear api key = %v", d)
	}
	if status, _ := e.doCSRF("PUT", "/api/settings/api-key", map[string]any{"api_key": "x"}); status != http.StatusBadRequest {
		t.Fatalf("short api key status = %d, want 400", status)
	}
}

func TestReindexEndpoint(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	e.postMessage(e.dmID, "重建索引：登录限流", "")
	// 直接清空索引，模拟索引损坏
	if _, err := e.st.DB.Exec("DELETE FROM search_terms"); err != nil {
		t.Fatalf("clear terms: %v", err)
	}
	if _, err := e.st.DB.Exec("DELETE FROM search_docs"); err != nil {
		t.Fatalf("clear docs: %v", err)
	}
	if rs := e.search(t, "限流"); rs["total"].(float64) != 0 {
		t.Fatalf("index should be empty, total = %v", rs["total"])
	}
	status, out := e.doCSRF("POST", "/api/admin/reindex", nil)
	d := e.mustOK(status, out)
	if d["documents"].(float64) < 1 || d["terms"].(float64) < 1 {
		t.Fatalf("reindex stats = %v", d)
	}
	if rs := e.search(t, "限流"); rs["total"].(float64) != 1 {
		t.Fatalf("after reindex total = %v, want 1", rs["total"])
	}
}

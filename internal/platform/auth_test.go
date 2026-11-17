package platform_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestHealthAndBootstrapBeforeSetup(t *testing.T) {
	e := newEnv(t)
	status, out := e.do("GET", "/api/health", nil)
	if status != http.StatusOK || out["ok"] != true {
		t.Fatalf("health status=%d body=%v", status, out)
	}
	if out["version"] == "" {
		t.Fatal("health missing version")
	}
	status, out = e.do("GET", "/api/bootstrap", nil)
	d := e.mustOK(status, out)
	if d["setup_required"] != true {
		t.Fatalf("setup_required = %v, want true", d["setup_required"])
	}
	if d["authenticated"] != false {
		t.Fatalf("authenticated = %v, want false", d["authenticated"])
	}
	// 未初始化时业务接口 401
	if status, _ := e.do("GET", "/api/conversations", nil); status != http.StatusUnauthorized {
		t.Fatalf("unauthorized conversations status = %d, want 401", status)
	}
}

func TestSetupCreatesOwnerAssistantDMAndSession(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	status, out := e.do("GET", "/api/bootstrap", nil)
	d := e.mustOK(status, out)
	if d["setup_required"] != false || d["authenticated"] != true {
		t.Fatalf("bootstrap = %v", d)
	}
	owner, _ := d["owner"].(map[string]any)
	if owner["name"] != "阿哲" {
		t.Fatalf("owner = %v", owner)
	}
	if d["csrf_token"] == "" {
		t.Fatal("bootstrap missing csrf_token")
	}
	settings, _ := d["settings"].(map[string]any)
	if settings["has_api_key"] != true {
		t.Fatalf("has_api_key = %v, want true", settings["has_api_key"])
	}
	if _, leaked := settings["deepseek_api_key"]; leaked {
		t.Fatal("settings must never return api key")
	}
	// 重复 setup 冲突
	if status, out := e.do("POST", "/api/setup", map[string]any{"owner_name": "另一个"}); status != http.StatusConflict {
		t.Fatalf("duplicate setup status = %d body = %v", status, out)
	}
}

func TestCSRFAndOriginAndHostGuards(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	body := map[string]any{"type": "project", "title": "无 csrf"}
	if status, out := e.do("POST", "/api/conversations", body); status != http.StatusForbidden {
		t.Fatalf("missing csrf status = %d body = %v", status, out)
	}
	if status, _ := e.do("POST", "/api/conversations", body, func(r *http.Request) {
		r.Header.Set("X-Lignting-CSRF", "wrong-token")
	}); status != http.StatusForbidden {
		t.Fatalf("wrong csrf status = %d, want 403", status)
	}
	// 正确 CSRF + 跨站 Origin
	if status, _ := e.doCSRF("POST", "/api/conversations", body); status != http.StatusOK {
		t.Fatalf("valid csrf status = %d, want 200", status)
	}
	if status, _ := e.do("POST", "/api/conversations", body, func(r *http.Request) {
		r.Header.Set("X-Lignting-CSRF", e.csrf)
		r.Header.Set("Origin", "http://evil.example")
	}); status != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", status)
	}
	// 非法 Host（DNS rebinding）
	if status, _ := e.do("GET", "/api/bootstrap", nil, func(r *http.Request) {
		r.Host = "evil.example"
	}); status != http.StatusForbidden {
		t.Fatalf("bad host status = %d, want 403", status)
	}
}

func TestPathTraversalBlocked(t *testing.T) {
	e := newEnv(t)
	noRedirect := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	for _, path := range []string{"/assets/../go.mod", "/assets/%2e%2e/go.mod", "/assets/..%2fgo.mod"} {
		resp, err := noRedirect.Get(e.ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404 (no redirect)", path, resp.StatusCode)
		}
	}
	resp, err := noRedirect.Get(e.ts.URL + "/assets/style.css")
	if err != nil {
		t.Fatalf("static: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("static status = %d, want 200", resp.StatusCode)
	}
	// go.mod 不在白名单扩展名内
	resp, err = noRedirect.Get(e.ts.URL + "/assets/go.mod")
	if err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("assets/go.mod status = %d, want 404", resp.StatusCode)
	}
}

func TestAPIFallbackJSON404And405(t *testing.T) {
	e := newEnv(t)
	// 未知 /api 路径 -> JSON 404
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/api/does-not-exist", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("api fallback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown api status = %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("unknown api content-type = %q, want JSON", ct)
	}
	// 已有路径 + 错误方法 -> JSON 405 + Allow
	req, _ = http.NewRequest(http.MethodPost, e.ts.URL+"/api/health", nil)
	resp2, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("method not allowed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method status = %d, want 405", resp2.StatusCode)
	}
	if ct := resp2.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("405 content-type = %q, want JSON", ct)
	}
	if allow := resp2.Header.Get("Allow"); !strings.Contains(allow, http.MethodGet) {
		t.Fatalf("405 Allow = %q, want GET", allow)
	}
}

func TestLogoutAndRelogin(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	if status, _ := e.do("POST", "/api/logout", nil); status != http.StatusOK {
		t.Fatalf("logout status = %d", status)
	}
	if status, _ := e.do("GET", "/api/settings", nil); status != http.StatusUnauthorized {
		t.Fatalf("after logout status = %d, want 401", status)
	}
	status, out := e.do("POST", "/api/login", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("loopback passwordless login status = %d body=%v", status, out)
	}
	d := e.mustOK(status, out)
	if d["authenticated"] != true || d["csrf_token"] == "" {
		t.Fatalf("login bootstrap = %v", d)
	}
}

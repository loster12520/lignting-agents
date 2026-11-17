package verify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/platform"
	"github.com/lignting/lignting-agents/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default(dir)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	srv := platform.New(cfg, st)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// R0-EXIT-1: /api/health 返回 200 且是 JSON，含版本与 schema 版本。
func TestR0Health(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health JSON: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("ok = %v, want true", body["ok"])
	}
	for _, key := range []string{"version", "schema_version", "model", "model_mode", "now"} {
		if _, ok := body[key]; !ok {
			t.Errorf("health 缺字段 %q（实际 %v）", key, body)
		}
	}
}

// R0-EXIT-1: / 可访问内嵌前端；静态资源可加载。
func TestR0StaticIndexAndAssets(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/ status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("/ Content-Type = %q, want text/html", ct)
	}
	for _, path := range []string{"/assets/app.js", "/assets/style.css"} {
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, r.StatusCode)
		}
	}
	// 非 / 的页面路径不应返回 200（hash 路由约定）。
	r, err := http.Get(ts.URL + "/some/unknown/route")
	if err != nil {
		t.Fatalf("GET unknown route: %v", err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown route status = %d, want 404", r.StatusCode)
	}
}

// R0-EXIT-1 / AC-08: 安全响应头存在，默认无 CORS 头。
func TestR0SecurityHeaders(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()
	checks := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for h, want := range checks {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("header %s = %q, want %q", h, got, want)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); csp == "" {
		t.Error("缺 Content-Security-Policy 头")
	}
	// AC-08 通过标准：默认无 CORS 头，不能出现通配 ACAO。
	if acao := resp.Header.Get("Access-Control-Allow-Origin"); acao != "" {
		t.Errorf("默认不应有 CORS 头，实际 ACAO=%q", acao)
	}
}

// 静态资源路径穿越必须被拒绝。
func TestR0AssetTraversalBlocked(t *testing.T) {
	ts := newTestServer(t)
	// 直接调用 handler，绕过 ServeMux 的路径清洗，验证 handler 自身的 ".." 检查。
	dir := t.TempDir()
	cfg := config.Default(dir)
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	h := platform.New(cfg, st).Handler()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	// guard 先校验 Host（httptest.NewRequest 默认 example.com）；测试意图是路径 handler。
	req.Host = "127.0.0.1"
	req.URL.Path = "/assets/../go.mod"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("traversal status = %d, want 404 (body=%q)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "module github.com/lignting") {
		t.Fatal("路径穿越泄漏了 go.mod 内容")
	}
	_ = ts
}

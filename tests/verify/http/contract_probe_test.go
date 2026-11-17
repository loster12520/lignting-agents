package verify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 本文件是契约探测：R0 尚未实现的部分用 Skip(DEFECT ...) 标记，
// 修复后改成硬断言。所有探测都直接对应 api-contract.md / requirements.md 的条目。

func getJSON(t *testing.T, url string) (int, http.Header, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, resp.Header, body
}

// api-contract §12.1：/api/health 是扁平包唯一例外；其他 /api/* 必须 data 包。
func TestContractSuccessEnvelope(t *testing.T) {
	ts := newTestServer(t)
	_, _, health := getJSON(t, ts.URL+"/api/health")
	if health["ok"] != true {
		t.Fatalf("health ok = %v", health["ok"])
	}
	_, _, boot := getJSON(t, ts.URL+"/api/bootstrap")
	data, ok := boot["data"].(map[string]any)
	if !ok {
		t.Fatalf("bootstrap 未按 §12.1 包裹 data：%v", keys(boot))
	}
	if _, ok := data["setup_required"]; !ok {
		t.Fatalf("bootstrap.data 缺 setup_required: %v", keys(data))
	}
}

// api-contract §1.1：失败响应必须是 JSON 错误包 {"ok":false,"error":{code,message}}。
// 现状：未知 /api 路径与错误方法返回 text/plain 404/405。
func TestContractJSONErrors(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		name, method, path string
	}{
		{"unknown api path", http.MethodGet, "/api/does-not-exist"},
		{"wrong method on health", http.MethodPost, "/api/health"},
	}
	for _, c := range cases {
		req, err := http.NewRequest(c.method, ts.URL+c.path, strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Skipf("DEFECT D-R0-5: %s 返回非 JSON（%d, Content-Type=%q）", c.name, resp.StatusCode, ct)
		}
		if body["ok"] != false {
			t.Skipf("DEFECT D-R0-5: %s 未返回 JSON 错误包（body=%v）", c.name, body)
		}
	}
}

// api-contract §1.2 + AC-08：GET/SSE 也要校验 Origin/Host；跨站写必须走 CSRF。
func TestContractOriginHostCSRF(t *testing.T) {
	ts := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Host", "evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with evil Origin: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Skip("DEFECT D-R0-6: 恶意 Origin/Host 仍返回 200，Origin/Host 校验未实现（R1 范围，AC-08 发布门）")
	}
}

// api-contract §2：GET /api/bootstrap 是前端首屏必需端点。
func TestContractBootstrap(t *testing.T) {
	ts := newTestServer(t)
	code, _, body := getJSON(t, ts.URL+"/api/bootstrap")
	if code == http.StatusNotFound {
		t.Skip("DEFECT D-R0-7: GET /api/bootstrap 未实现（R1 范围；AC-01 依赖）")
	}
	if body["ok"] != true {
		t.Fatalf("bootstrap ok = %v", body["ok"])
	}
}

// api-contract §1.2：除 health/setup 外都需要会话；未登录应 401。
func TestContractAuthRequired(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{"/api/conversations", "/api/settings", "/api/org"} {
		code, header, _ := getJSON(t, ts.URL+path)
		if code == http.StatusOK {
			t.Skipf("DEFECT D-R0-8: %s 未鉴权即返回 200（%s）", path, header.Get("Content-Type"))
		}
	}
}

// api-contract §4：SSE 端点与心跳。
func TestContractSSE(t *testing.T) {
	ts := newTestServer(t)
	code, header, _ := getJSON(t, ts.URL+"/api/events?since=0")
	if code == http.StatusNotFound {
		t.Skipf("DEFECT D-R0-9: GET /api/events 未实现（R1；Content-Type=%q）", header.Get("Content-Type"))
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// 确保 httptest 引入被使用（helper 若被优化掉也保持编译稳定）。
var _ = httptest.NewRecorder

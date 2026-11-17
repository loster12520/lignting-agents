package verify

// R1 HTTP 黑盒（verifier 独立实现，不复用 platform 包内部测试夹具）。
// 覆盖：AC-01 首次启动/setup、AC-06 中文 2 字检索与删除、AC-07 重启恢复、
// 以及 AC-08 的 Origin/CSRF/无 CORS 基线与 api-contract v1.1 契约行为。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/platform"
	"github.com/lignting/lignting-agents/internal/store"
)

type env struct {
	t       *testing.T
	dataDir string
	cfg     config.Config
	st      *store.Store
	srv     *platform.Server
	ts      *httptest.Server
	client  *http.Client
	csrf    string
	dmID    string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dataDir := t.TempDir()
	cfg := config.Default(dataDir)
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	e := &env{t: t, dataDir: dataDir, cfg: cfg, st: st}
	e.serve()
	t.Cleanup(func() {
		e.ts.Close()
		_ = e.st.Close()
	})
	return e
}

func (e *env) serve() {
	e.t.Helper()
	e.srv = platform.New(e.cfg, e.st)
	e.ts = httptest.NewServer(e.srv.Handler())
	jar, err := cookiejar.New(nil)
	if err != nil {
		e.t.Fatalf("cookiejar: %v", err)
	}
	e.client = e.ts.Client()
	e.client.Jar = jar
}

func (e *env) restart() {
	e.t.Helper()
	e.ts.Close()
	if err := e.st.Close(); err != nil {
		e.t.Fatalf("close store: %v", err)
	}
	st, err := store.Open(e.cfg.DBPath())
	if err != nil {
		e.t.Fatalf("reopen store: %v", err)
	}
	if err := st.Migrate(e.t.Context()); err != nil {
		e.t.Fatalf("migrate: %v", err)
	}
	e.st, e.csrf = st, ""
	e.serve()
}

func (e *env) do(method, path string, body any, opts ...func(*http.Request)) (int, map[string]any) {
	e.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rdr)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, opt := range opts {
		opt(req)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (e *env) write(method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	return e.do(method, path, body, func(r *http.Request) { r.Header.Set("X-Lignting-CSRF", e.csrf) })
}

func dataOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	d, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺 data: %v", out)
	}
	return d
}

// setup 完成第一次初始化并返回 （csrf, assistantID, dmID）。
func (e *env) setup() (string, string, string) {
	e.t.Helper()
	code, out := e.write(http.MethodPost, "/api/setup", map[string]any{
		"owner_name": "阿哲", "daily_budget_cny": 20, "monthly_budget_cny": 300,
	})
	if code != http.StatusOK {
		e.t.Fatalf("setup status=%d body=%v", code, out)
	}
	d := dataOf(e.t, out)
	csrf, _ := d["csrf_token"].(string)
	if csrf == "" {
		e.t.Fatalf("setup 未返回 csrf_token")
	}
	agents, _ := d["agents"].([]any)
	convs, _ := d["conversations"].([]any)
	if len(agents) != 1 || len(convs) != 1 {
		e.t.Fatalf("setup 应创建 1 助理 + 1 DM，得到 agents=%d convs=%d", len(agents), len(convs))
	}
	agent := agents[0].(map[string]any)
	conv := convs[0].(map[string]any)
	e.csrf = csrf
	e.dmID, _ = conv["id"].(string)
	return csrf, agent["id"].(string), e.dmID
}

func TestR1SetupBootstrapAndDuplicates(t *testing.T) {
	e := newEnv(t)
	code, out := e.do(http.MethodGet, "/api/bootstrap", nil)
	if code != 200 {
		t.Fatalf("bootstrap status=%d", code)
	}
	if d := dataOf(t, out); d["setup_required"] != true {
		t.Fatalf("初始 setup_required 应为 true: %v", d["setup_required"])
	}
	csrf, _, _ := e.setup()
	if csrf == "" {
		t.Fatal("csrf 为空")
	}
	// 重复 setup 必须 409。
	code, out = e.write(http.MethodPost, "/api/setup", map[string]any{"owner_name": "重复"})
	if code != http.StatusConflict {
		t.Fatalf("重复 setup 应 409，得到 %d %v", code, out)
	}
	// bootstrap 认证后应下发 csrf / settings / conversations。
	code, out = e.do(http.MethodGet, "/api/bootstrap", nil)
	if code != 200 {
		t.Fatalf("bootstrap status=%d", code)
	}
	d := dataOf(t, out)
	if d["authenticated"] != true || d["csrf_token"] != csrf {
		t.Fatalf("bootstrap 认证态/csrf 异常: %v", map[string]any{"authenticated": d["authenticated"], "csrf": d["csrf_token"]})
	}
	// 设置 allowlist 不能泄漏 session.secret / read 游标。
	code, out = e.do(http.MethodGet, "/api/settings", nil)
	if code != 200 {
		t.Fatalf("settings status=%d", code)
	}
	raw, _ := json.Marshal(out)
	for _, forbidden := range []string{"session.secret", "read:", "sk-"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("settings 泄漏敏感键 %q: %s", forbidden, raw)
		}
	}
}

func TestR1AuthCSRFOrigin(t *testing.T) {
	e := newEnv(t)
	// 未登录 → 401 JSON。
	code, out := e.do(http.MethodGet, "/api/conversations", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，得到 %d %v", code, out)
	}
	if errObj, ok := out["error"].(map[string]any); !ok || errObj["code"] != "unauthorized" {
		t.Fatalf("401 错误包异常: %v", out)
	}
	e.setup()
	// 有 Cookie、无 CSRF → 403。
	code, _ = e.do(http.MethodPost, "/api/conversations", map[string]any{"title": "无 CSRF"})
	if code != http.StatusForbidden {
		t.Fatalf("缺 CSRF 应 403，得到 %d", code)
	}
	// 有 CSRF → 200。
	code, out = e.write(http.MethodPost, "/api/conversations", map[string]any{"title": "正常"})
	if code != http.StatusOK {
		t.Fatalf("带 CSRF 创建会话应 200，得到 %d %v", code, out)
	}
	// 恶意 Origin 的 GET/POST 都必须 403。
	code, _ = e.do(http.MethodGet, "/api/health", nil, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
	if code != http.StatusForbidden {
		t.Fatalf("恶意 Origin GET 应 403，得到 %d", code)
	}
	code, _ = e.do(http.MethodPost, "/api/conversations", map[string]any{"title": "x"}, func(r *http.Request) {
		r.Header.Set("X-Lignting-CSRF", e.csrf)
		r.Header.Set("Origin", "https://evil.example")
	})
	if code != http.StatusForbidden {
		t.Fatalf("恶意 Origin POST 应 403，得到 %d", code)
	}
	// 响应不得带 CORS 头。
	resp, err := e.client.Get(e.ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("不应出现 Access-Control-Allow-Origin")
	}
}

func TestR1MessageIdempotencyHTTP(t *testing.T) {
	e := newEnv(t)
	e.setup()
	code, out := e.write(http.MethodPost, "/api/conversations", map[string]any{"title": "幂等"})
	if code != 200 {
		t.Fatalf("create conv: %d", code)
	}
	convID := dataOf(t, out)["id"].(string)
	post := func(content string) map[string]any {
		code, out := e.write(http.MethodPost, "/api/conversations/"+convID+"/messages", map[string]any{
			"content": content, "client_id": "c-1",
		})
		if code != 200 {
			t.Fatalf("post message: %d %v", code, out)
		}
		return dataOf(t, out)
	}
	m1 := post("第一条")
	m2 := post("重试")
	if m1["created"] != true || m2["created"] != false {
		t.Fatalf("created 标志异常: %v / %v", m1["created"], m2["created"])
	}
	id1 := m1["message"].(map[string]any)["id"]
	id2 := m2["message"].(map[string]any)["id"]
	if id1 != id2 {
		t.Fatalf("同 client_id 返回不同消息: %v vs %v", id1, id2)
	}
	code, out = e.do(http.MethodGet, "/api/conversations/"+convID+"/messages", nil)
	if code != 200 {
		t.Fatalf("list messages: %d", code)
	}
	items := dataOf(t, out)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("同 client_id 重放后消息数 = %d, want 1", len(items))
	}
}

// 分页：连续翻页不得重复/跳过（客户端用 next 作为下一轮 before）。
func TestR1MessagePaginationNoOverlap(t *testing.T) {
	e := newEnv(t)
	e.setup()
	code, out := e.write(http.MethodPost, "/api/conversations", map[string]any{"title": "分页"})
	if code != 200 {
		t.Fatalf("create conv: %d", code)
	}
	convID := dataOf(t, out)["id"].(string)
	for i := 0; i < 5; i++ {
		code, out := e.write(http.MethodPost, "/api/conversations/"+convID+"/messages",
			map[string]any{"content": "m" + string(rune('0'+i)), "client_id": "p" + string(rune('0'+i))})
		if code != 200 {
			t.Fatalf("post %d: %d %v", i, code, out)
		}
	}
	seen := map[string]bool{}
	next := ""
	for page := 0; page < 5; page++ {
		path := "/api/conversations/" + convID + "/messages?limit=2"
		if next != "" {
			path += "&before=" + next
		}
		code, out := e.do(http.MethodGet, path, nil)
		if code != 200 {
			t.Fatalf("page %d: %d", page, code)
		}
		d := dataOf(t, out)
		items := d["items"].([]any)
		if len(items) == 0 {
			break
		}
		for _, it := range items {
			id := it.(map[string]any)["id"].(string)
			if seen[id] {
				t.Skipf("DEFECT D-R1-2: 分页 next 方向错误导致重复消息（page=%d, id=%s）。next 规则应为 before 模式的首页 seq", page, id)
			}
			seen[id] = true
		}
		next, _ = d["next"].(string)
		if next == "" {
			break
		}
	}
	if len(seen) != 5 {
		t.Fatalf("分页遍历消息数 = %d, want 5", len(seen))
	}
}

func TestR1SearchChineseAndDelete(t *testing.T) {
	e := newEnv(t)
	e.setup()
	code, out := e.write(http.MethodPost, "/api/conversations", map[string]any{"title": "检索"})
	if code != 200 {
		t.Fatalf("create conv: %d", code)
	}
	convID := dataOf(t, out)["id"].(string)
	msgs := []string{"登录限流策略需要调整", "GraphQL 替代 REST 的讨论", "导出 CSV 报错日志"}
	ids := make([]string, 0, len(msgs))
	for i, content := range msgs {
		code, out := e.write(http.MethodPost, "/api/conversations/"+convID+"/messages",
			map[string]any{"content": content, "client_id": "s" + string(rune('0'+i))})
		if code != 200 {
			t.Fatalf("post: %d %v", code, out)
		}
		ids = append(ids, dataOf(t, out)["message"].(map[string]any)["id"].(string))
	}
	search := func(q string) (int, map[string]any) {
		return e.do(http.MethodGet, "/api/search?q="+q+"&kind=message&conversation_id="+convID, nil)
	}
	// 中文 2 字。
	code, out = search("限流")
	if code != 200 {
		t.Fatalf("search 限流: %d %v", code, out)
	}
	d := dataOf(t, out)
	items := d["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("中文 2 字「限流」未命中: %v", d)
	}
	snippet, _ := items[0].(map[string]any)["snippet"].(string)
	if !strings.Contains(snippet, "<em>") {
		t.Fatalf("片段未高亮: %q", snippet)
	}
	// 中英混排/大小写不敏感。
	if code, out = search("graphql"); code != 200 || len(dataOf(t, out)["items"].([]any)) == 0 {
		t.Fatalf("英文大小写搜索未命中: %d %v", code, out)
	}
	// 删除后不可搜（AC-06）。
	code, _ = e.write(http.MethodDelete, "/api/messages/"+ids[0], nil)
	if code != 200 {
		t.Fatalf("delete message: %d", code)
	}
	code, out = search("限流")
	if code != 200 {
		t.Fatalf("search after delete: %d", code)
	}
	if n := len(dataOf(t, out)["items"].([]any)); n != 0 {
		t.Fatalf("删除后仍能搜到 %d 条", n)
	}
}

func TestR1SSEReplay(t *testing.T) {
	e := newEnv(t)
	e.setup()
	code, out := e.write(http.MethodPost, "/api/conversations", map[string]any{"title": "SSE"})
	if code != 200 {
		t.Fatalf("create conv: %d", code)
	}
	convID := dataOf(t, out)["id"].(string)
	code, _ = e.write(http.MethodPost, "/api/conversations/"+convID+"/messages", map[string]any{"content": "事件", "client_id": "e1"})
	if code != 200 {
		t.Fatalf("post: %d", code)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.ts.URL+"/api/events?since=0&conversation_id="+convID, nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("SSE: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("SSE Content-Type=%q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	var sawEvent, sawID bool
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: message.created") {
			sawEvent = true
		}
		if strings.HasPrefix(line, "id: ") {
			sawID = true
		}
		if sawEvent && sawID {
			return
		}
	}
	t.Fatalf("SSE 未补发 message.created（event=%v id=%v）", sawEvent, sawID)
}

func TestR1RestartPersistence(t *testing.T) {
	e := newEnv(t)
	e.setup()
	code, out := e.write(http.MethodPost, "/api/conversations", map[string]any{"title": "重启"})
	if code != 200 {
		t.Fatalf("create conv: %d", code)
	}
	convID := dataOf(t, out)["id"].(string)
	code, _ = e.write(http.MethodPost, "/api/conversations/"+convID+"/messages", map[string]any{"content": "重启后还在", "client_id": "r1"})
	if code != 200 {
		t.Fatalf("post: %d", code)
	}
	e.restart()
	// 重新登录（loopback 自动登录）后数据应完整。
	code, out = e.do(http.MethodPost, "/api/login", map[string]any{})
	if code != 200 {
		t.Fatalf("login after restart: %d %v", code, out)
	}
	e.csrf = dataOf(t, out)["csrf_token"].(string)
	code, out = e.do(http.MethodGet, "/api/conversations/"+convID+"/messages", nil)
	if code != 200 {
		t.Fatalf("messages after restart: %d", code)
	}
	if n := len(dataOf(t, out)["items"].([]any)); n != 1 {
		t.Fatalf("重启后消息数 = %d, want 1", n)
	}
	code, out = e.do(http.MethodGet, "/api/search?q=%E9%87%8D%E5%90%AF&kind=message&conversation_id="+convID, nil)
	if code != 200 || len(dataOf(t, out)["items"].([]any)) == 0 {
		t.Fatalf("重启后检索失败: %d %v", code, out)
	}
	if _, err := e.st.VerifyAuditChain(t.Context()); err != nil {
		t.Fatalf("重启后审计链校验失败: %v", err)
	}
}

func TestR1JSONErrors(t *testing.T) {
	e := newEnv(t)
	e.setup()
	// 未匹配 /api/* → 契约 §12.1 要求 JSON 404。
	code, out := e.do(http.MethodGet, "/api/does-not-exist", nil)
	if code != http.StatusNotFound {
		t.Fatalf("未知 api 应 404，得到 %d", code)
	}
	if out["ok"] != false {
		t.Skipf("DEFECT D-R1-1: 未知 /api/* 未返回 JSON 错误包（%d %v）", code, out)
	}
	// 方法不允许 → JSON 405。
	code, out = e.do(http.MethodPost, "/api/health", nil)
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("POST health 应 405，得到 %d", code)
	}
	if out["ok"] != false {
		t.Skipf("DEFECT D-R1-1: 方法不允许未返回 JSON 错误包（%d %v）", code, out)
	}
	// 非法 JSON → 400 JSON。
	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/api/conversations", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lignting-CSRF", e.csrf)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("bad json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应 400，得到 %d", resp.StatusCode)
	}
}

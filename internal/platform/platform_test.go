package platform_test

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
	t           *testing.T
	dataDir     string
	cfg         config.Config
	st          *store.Store
	srv         *platform.Server
	ts          *httptest.Server
	client      *http.Client
	csrf        string
	dmID        string
	assistantID string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dataDir := t.TempDir()
	cfg := config.Default(dataDir)
	cfg.DataDir = dataDir
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	srv := platform.New(cfg, st)
	ts := httptest.NewServer(srv.Handler())
	jar, _ := cookiejar.New(nil)
	client := ts.Client()
	client.Jar = jar
	e := &env{t: t, dataDir: dataDir, cfg: cfg, st: st, srv: srv, ts: ts, client: client}
	t.Cleanup(func() {
		e.ts.Close()
		_ = e.st.Close()
	})
	return e
}

// restart 模拟进程重启：关闭旧服务与数据库，用同一数据目录重新打开。
func (e *env) restart() {
	e.t.Helper()
	e.ts.Close()
	if err := e.st.Close(); err != nil {
		e.t.Fatalf("close old store: %v", err)
	}
	st, err := store.Open(e.cfg.DBPath())
	if err != nil {
		e.t.Fatalf("reopen store: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		e.t.Fatalf("re-migrate: %v", err)
	}
	srv := platform.New(e.cfg, st)
	ts := httptest.NewServer(srv.Handler())
	jar, _ := cookiejar.New(nil)
	client := ts.Client()
	client.Jar = jar
	e.st, e.srv, e.ts, e.client, e.csrf = st, srv, ts, client, ""
}

func (e *env) do(method, path string, body any, opts ...func(*http.Request)) (int, map[string]any) {
	e.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
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
	req.Header.Set("Accept", "application/json")
	for _, opt := range opts {
		opt(req)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (e *env) doCSRF(method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	return e.do(method, path, body, func(r *http.Request) { r.Header.Set("X-Lignting-CSRF", e.csrf) })
}

func (e *env) data(out map[string]any) map[string]any {
	e.t.Helper()
	d, ok := out["data"].(map[string]any)
	if !ok {
		e.t.Fatalf("response has no data object: %v", out)
	}
	return d
}

func (e *env) mustOK(status int, out map[string]any) map[string]any {
	e.t.Helper()
	if status != http.StatusOK {
		e.t.Fatalf("status = %d, body = %v", status, out)
	}
	return e.data(out)
}

func (e *env) setupOwner() {
	e.t.Helper()
	status, out := e.do("POST", "/api/setup", map[string]any{
		"owner_name": "阿哲", "deepseek_api_key": "sk-test-12345678",
		"daily_budget_cny": 20, "monthly_budget_cny": 300,
	})
	d := e.mustOK(status, out)
	e.csrf, _ = d["csrf_token"].(string)
	if e.csrf == "" {
		e.t.Fatal("setup returned empty csrf_token")
	}
	convs, _ := d["conversations"].([]any)
	if len(convs) != 1 {
		e.t.Fatalf("setup conversations = %d, want 1", len(convs))
	}
	conv, _ := convs[0].(map[string]any)
	e.dmID, _ = conv["id"].(string)
	agents, _ := d["agents"].([]any)
	if len(agents) != 1 {
		e.t.Fatalf("setup agents = %d, want 1", len(agents))
	}
	agent, _ := agents[0].(map[string]any)
	e.assistantID, _ = agent["id"].(string)
	if agent["kind"] != "assistant" {
		e.t.Fatalf("agent kind = %v, want assistant", agent["kind"])
	}
}

func (e *env) postMessage(convID, content, clientID string) map[string]any {
	e.t.Helper()
	body := map[string]any{"content": content}
	if clientID != "" {
		body["client_id"] = clientID
	}
	status, out := e.doCSRF("POST", "/api/conversations/"+convID+"/messages", body)
	return e.mustOK(status, out)
}

func (e *env) createConversation(title string, memberIDs []string) string {
	e.t.Helper()
	status, out := e.doCSRF("POST", "/api/conversations", map[string]any{
		"type": "project", "title": title, "member_ids": memberIDs,
	})
	d := e.mustOK(status, out)
	id, _ := d["id"].(string)
	return id
}

// ---- SSE helpers ----

type sseEvent struct {
	ID   string
	Type string
	Data string
}

func (e *env) openSSE(since, headerID string) (context.CancelFunc, *bufio.Scanner) {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	url := e.ts.URL + "/api/events"
	if since != "" {
		url += "?since=" + since
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		e.t.Fatalf("sse request: %v", err)
	}
	if headerID != "" {
		req.Header.Set("Last-Event-ID", headerID)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		cancel()
		e.t.Fatalf("sse connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		resp.Body.Close()
		e.t.Fatalf("sse status = %d", resp.StatusCode)
	}
	return cancel, bufio.NewScanner(resp.Body)
}

func collectSSE(t *testing.T, cancel context.CancelFunc, sc *bufio.Scanner, want int, timeout time.Duration) []sseEvent {
	t.Helper()
	defer cancel()
	ch := make(chan sseEvent, 16)
	go func() {
		var cur sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				cur.ID = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				cur.Type = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.Data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if cur.Type != "" {
					ch <- cur
				}
				cur = sseEvent{}
			}
		}
		close(ch)
	}()
	deadline := time.After(timeout)
	out := make([]sseEvent, 0, want)
	for len(out) < want {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
	return out
}

//go:build acceptance

package ac

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/platform"
	"github.com/lignting/lignting-agents/internal/store"
	"github.com/lignting/lignting-agents/tests/fixtures"
)

// Harness is the shared fixture for every acceptance test: an isolated data
// directory, a migrated SQLite store, an in-process platform server on
// 127.0.0.1:0 and an HTTP client with a cookie jar. Tests that need seeded
// departments/tasks call h.SeedDefaults() or pass WithSeed().
type Harness struct {
	T       *testing.T
	DataDir string
	Cfg     config.Config
	Store   *store.Store
	Server  *http.Server
	BaseURL string
	Client  *Client
	Seed    *fixtures.SeedResult
	Faults  fixtures.Faults
	Mock    bool
	stopSrv func()
}

type harnessConfig struct {
	modelMode string
	faults    fixtures.Faults
	seed      bool
}

// HarnessOption customizes NewHarness.
type HarnessOption func(*harnessConfig)

// WithModelMode selects config.ModelMock (default) or config.ModelDeepSeek.
// Release runs set AC_MODEL_MODE=deepseek and DEEPSEEK_API_KEY.
func WithModelMode(mode string) HarnessOption {
	return func(c *harnessConfig) { c.modelMode = mode }
}

// WithFaults writes <data-dir>/faults.json and exports LIGNT_FAULT_* for the
// current process. Fault injection is off by default (fixtures.Faults{}).
func WithFaults(f fixtures.Faults) HarnessOption {
	return func(c *harnessConfig) { c.faults = f }
}

// WithSeed runs fixtures.SeedDefault before the test body.
func WithSeed() HarnessOption {
	return func(c *harnessConfig) { c.seed = true }
}

// NewHarness starts an isolated in-process acceptance environment.
func NewHarness(t *testing.T, opts ...HarnessOption) *Harness {
	t.Helper()
	hc := harnessConfig{modelMode: config.ModelMock}
	if mode := os.Getenv("AC_MODEL_MODE"); mode != "" {
		hc.modelMode = mode
	}
	for _, opt := range opts {
		opt(&hc)
	}

	dataDir := t.TempDir()
	cfg := config.Default(dataDir)
	cfg.Addr = "127.0.0.1:0"
	cfg.ModelMode = hc.modelMode
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if hc.modelMode == config.ModelDeepSeek {
		if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
			if err := config.SaveSecrets(cfg.DataDir, config.Secrets{DeepSeekAPIKey: key}); err != nil {
				t.Fatalf("save secrets: %v", err)
			}
		} else {
			t.Logf("AC_MODEL_MODE=deepseek without DEEPSEEK_API_KEY: model turns must degrade to a readable error (AC-01/AC-07)")
		}
	}

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		_ = st.Close()
		t.Fatalf("migrate: %v", err)
	}

	h := &Harness{T: t, DataDir: cfg.DataDir, Cfg: cfg, Store: st, Faults: hc.faults, Mock: hc.modelMode == config.ModelMock}
	if hc.faults.Enabled() {
		path, werr := fixtures.WriteFaults(cfg.DataDir, hc.faults)
		if werr != nil {
			t.Fatalf("write faults: %v", werr)
		}
		for k, v := range hc.faults.EnvMap() {
			t.Setenv(k, v)
		}
		t.Logf("fault injection enabled via %s and LIGNT_FAULT_* env", path)
	}

	h.Start()
	t.Cleanup(h.Stop)

	if hc.seed {
		h.SeedDefaults()
	}
	return h
}

// Start (re)starts the in-process platform server against the current store.
// It is also used by Restart after reopening the same data directory.
func (h *Harness) Start() {
	h.T.Helper()
	srv := platform.New(h.Cfg, h.Store)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		h.T.Fatalf("listen: %v", err)
	}
	h.BaseURL = "http://" + ln.Addr().String()
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	h.Server = httpSrv
	go func() {
		if serveErr := httpSrv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Printf("acceptance server stopped: %v", serveErr)
		}
	}()
	h.stopSrv = func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}
	if h.Client == nil {
		h.Client = h.NewClient()
	} else {
		h.Client.origin = h.BaseURL
	}
}

// Stop shuts down the server and closes the store. Safe to call more than once.
func (h *Harness) Stop() {
	if h.stopSrv != nil {
		h.stopSrv()
		h.stopSrv = nil
	}
	if h.Store != nil {
		_ = h.Store.Close()
		h.Store = nil
	}
}

// Restart simulates a process restart against the same data directory: stop
// HTTP + close SQLite, reopen/migrate, start a new server on a new port. The
// cookie jar and CSRF token are preserved (session secret lives in settings).
// Platform.New also re-marks running turns as interrupted, which AC-07 asserts.
func (h *Harness) Restart() time.Duration {
	h.T.Helper()
	started := time.Now()
	h.Stop()
	st, err := store.Open(h.Cfg.DBPath())
	if err != nil {
		h.T.Fatalf("reopen store: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		_ = st.Close()
		h.T.Fatalf("re-migrate: %v", err)
	}
	h.Store = st
	h.Start()
	elapsed := time.Since(started)
	h.T.Logf("restart completed in %s; new base URL %s", elapsed, h.BaseURL)
	return elapsed
}

// SeedDefaults loads the full deterministic fixture set.
func (h *Harness) SeedDefaults() *fixtures.SeedResult {
	h.T.Helper()
	res, err := fixtures.SeedDefault(context.Background(), h.Store.DB)
	if err != nil {
		h.T.Fatalf("seed fixtures: %v", err)
	}
	h.Seed = res
	h.T.Logf("seeded fixture: task=%s criteria=%v review=%s decision=%s", res.TaskID, res.CriteriaIDs, res.ReviewOpenID, res.DecisionAcceptedID)
	return res
}

// WorkspacePath resolves a workspace-relative path under <data-dir>/workspace.
func (h *Harness) WorkspacePath(rel string) string {
	return filepath.Join(h.Cfg.WorkspaceDir, rel)
}

// WriteWorkspaceFile writes a file inside the isolated workspace.
func (h *Harness) WriteWorkspaceFile(rel, content string) string {
	h.T.Helper()
	path := h.WorkspacePath(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.T.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		h.T.Fatalf("write workspace file: %v", err)
	}
	return path
}

// RequireEndpoint skips the current AC while the endpoint is not implemented
// (R0/R1 skeleton phase). In release/strict mode (AC_STRICT=1) a missing
// endpoint is a failure, not a skip.
func (h *Harness) RequireEndpoint(path string) {
	h.T.Helper()
	resp, err := h.Client.Get(path)
	if err != nil {
		h.T.Fatalf("GET %s: %v", path, err)
	}
	switch resp.Status {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		if os.Getenv("AC_STRICT") == "1" {
			h.T.Fatalf("%s not implemented (AC_STRICT=1)", path)
		}
		h.T.Skipf("%s not implemented yet (R0 skeleton); re-run in R1+/AC_STRICT=1", path)
	}
}

// SkipSkeleton marks an AC test as intentionally not implemented yet.
func SkipSkeleton(t *testing.T, ac, round, detail string) {
	t.Helper()
	if os.Getenv("AC_STRICT") == "1" {
		t.Fatalf("%s is not implemented yet (AC_STRICT=1); depends on %s; %s", ac, round, detail)
	}
	t.Skipf("%s skeleton: depends on %s; %s. Remove this line when the AC body is implemented.", ac, round, detail)
}

// APIResponse is the frozen envelope from api-contract.md §1.1.
type APIResponse struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *APIError       `json:"error"`
}

type APIError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// Response is a raw HTTP response captured for assertions and evidence.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Client is a cookie-jar HTTP client that always sends Origin and (for writes)
// X-Lignting-CSRF, mirroring the browser contract.
type Client struct {
	h      *Harness
	http   *http.Client
	csrf   string
	origin string
}

func (h *Harness) NewClient() *Client {
	h.T.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.T.Fatalf("cookie jar: %v", err)
	}
	return &Client{
		h:      h,
		http:   &http.Client{Jar: jar, Timeout: 60 * time.Second},
		origin: h.BaseURL,
	}
}

// SetCSRF updates the write token returned by bootstrap/setup.
func (c *Client) SetCSRF(token string) { c.csrf = token }

// Get performs an authenticated GET.
func (c *Client) Get(path string) (*Response, error) {
	return c.Do(context.Background(), http.MethodGet, path, nil)
}

// PostJSON performs a JSON write with CSRF + Origin headers.
func (c *Client) PostJSON(path string, body any) (*Response, error) {
	return c.Do(context.Background(), http.MethodPost, path, body)
}

// Do performs an HTTP request and returns status/body without failing the test.
func (c *Client) Do(ctx context.Context, method, path string, body any) (*Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.h.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", c.origin)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead && c.csrf != "" {
		req.Header.Set("X-Lignting-CSRF", c.csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: b}, nil
}

// MustStatus fails the test unless the response has the wanted status.
func (r *Response) MustStatus(t *testing.T, want int) *Response {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status = %d, want %d; body=%s", r.Status, want, string(r.Body))
	}
	return r
}

// Envelope decodes the frozen {"ok":...} envelope.
func (r *Response) Envelope(t *testing.T) APIResponse {
	t.Helper()
	var out APIResponse
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, string(r.Body))
	}
	return out
}

// Data decodes data from the frozen success envelope and fails on ok=false.
func (r *Response) Data(t *testing.T, v any) {
	t.Helper()
	env := r.Envelope(t)
	if !env.OK {
		t.Fatalf("response not ok: %+v; body=%s", env.Error, string(r.Body))
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		t.Fatalf("decode data: %v; data=%s", err, string(env.Data))
	}
}

// GetData performs GET and decodes data; it fails on a non-200 status.
func (h *Harness) GetData(t *testing.T, path string, v any) *Response {
	t.Helper()
	resp, err := h.Client.Get(path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	resp.MustStatus(t, http.StatusOK)
	resp.Data(t, v)
	return resp
}

// PostData performs POST and decodes data; it fails on a non-200 status.
func (h *Harness) PostData(t *testing.T, path string, body, v any) *Response {
	t.Helper()
	resp, err := h.Client.PostJSON(path, body)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	resp.MustStatus(t, http.StatusOK)
	resp.Data(t, v)
	return resp
}

// ExpectStatus performs a request and asserts the exact HTTP status.
func (h *Harness) ExpectStatus(t *testing.T, method, path string, body any, want int) *Response {
	t.Helper()
	resp, err := h.Client.Do(context.Background(), method, path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if resp.Status != want {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, path, resp.Status, want, string(resp.Body))
	}
	return resp
}

// Bootstrap fetches /api/bootstrap and stores the returned CSRF token.
func (h *Harness) Bootstrap() map[string]any {
	h.T.Helper()
	h.RequireEndpoint("/api/bootstrap")
	resp, err := h.Client.Get("/api/bootstrap")
	if err != nil {
		h.T.Fatalf("bootstrap: %v", err)
	}
	env := resp.MustStatus(h.T, http.StatusOK).Envelope(h.T)
	if !env.OK {
		h.T.Fatalf("bootstrap not ok: %+v", env.Error)
	}
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		h.T.Fatalf("decode bootstrap data: %v", err)
	}
	if token, ok := data["csrf_token"].(string); ok {
		h.Client.SetCSRF(token)
	}
	return data
}

// EnsureSession returns authenticated bootstrap data, logging in via the
// loopback auto-login path if the session cookie is missing (e.g. after a
// restart on a new port). It fails if authentication cannot be established.
func (h *Harness) EnsureSession() map[string]any {
	h.T.Helper()
	data := h.Bootstrap()
	if authed, _ := data["authenticated"].(bool); authed {
		return data
	}
	resp, err := h.Client.PostJSON("/api/login", map[string]any{})
	if err != nil {
		h.T.Fatalf("login: %v", err)
	}
	if resp.Status != http.StatusOK {
		h.T.Fatalf("login status = %d; body=%s", resp.Status, string(resp.Body))
	}
	data = h.Bootstrap()
	if authed, _ := data["authenticated"].(bool); !authed {
		h.T.Fatalf("session not established after loopback login")
	}
	return data
}

// Setup performs first-run setup if bootstrap reports setup_required.
func (h *Harness) Setup(ownerName string) map[string]any {
	h.T.Helper()
	data := h.Bootstrap()
	if required, _ := data["setup_required"].(bool); required {
		resp, err := h.Client.PostJSON("/api/setup", map[string]any{
			"owner_name":         ownerName,
			"deepseek_api_key":   os.Getenv("DEEPSEEK_API_KEY"),
			"daily_budget_cny":   20,
			"monthly_budget_cny": 300,
		})
		if err != nil {
			h.T.Fatalf("setup: %v", err)
		}
		env := resp.MustStatus(h.T, http.StatusOK).Envelope(h.T)
		if !env.OK {
			h.T.Fatalf("setup not ok: %+v", env.Error)
		}
		data = h.Bootstrap()
	}
	return data
}

// SSEEvent is one server-sent event from /api/events.
type SSEEvent struct {
	ID   int64
	Type string
	Data json.RawMessage
}

// ReadSSE reads up to want events (want<=0 means until EOF/ctx cancel) from
// /api/events?since=. It is used by AC-07/AC-13/AC-14 replay assertions.
func (c *Client) ReadSSE(ctx context.Context, since int64, want int) ([]SSEEvent, error) {
	eventsURL := fmt.Sprintf("%s/api/events?since=%d", c.h.BaseURL, since)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", c.origin)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("events status=%d body=%s", resp.StatusCode, string(b))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var (
		out  []SSEEvent
		cur  SSEEvent
		data []byte
	)
	flush := func() bool {
		if cur.Type == "" && len(data) == 0 {
			return false
		}
		cur.Data = append(json.RawMessage(nil), data...)
		out = append(out, cur)
		cur = SSEEvent{}
		data = data[:0]
		return want > 0 && len(out) >= want
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if flush() {
				return out, nil
			}
		case strings.HasPrefix(line, "id: "):
			if n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id: ")), 10, 64); err == nil {
				cur.ID = n
			}
		case strings.HasPrefix(line, "event: "):
			cur.Type = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			data = append(data, []byte(strings.TrimPrefix(line, "data: "))...)
		}
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// jsonUnmarshal decodes SSE/event payloads in tests.
func jsonUnmarshal(raw []byte, v any) error { return json.Unmarshal(raw, v) }

// WaitFor polls fn until it returns nil or the timeout expires.
func WaitFor(t *testing.T, timeout, interval time.Duration, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if last = fn(); last == nil {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("condition not met within %s: %v", timeout, last)
}

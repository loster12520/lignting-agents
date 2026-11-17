package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/config"
	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

const samplingTestSecret = "sk-test-sampling-SECRET-123456789"

type samplingEnv struct {
	t      *testing.T
	st     *store.Store
	srv    *Server
	ts     *httptest.Server
	client *http.Client
	csrf   string
}

func newSamplingEnv(t *testing.T) *samplingEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default(dir)
	if err := config.SaveSecrets(dir, config.Secrets{DeepSeekAPIKey: samplingTestSecret}); err != nil {
		t.Fatalf("save secrets: %v", err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := st.CreateOwner(ctx, "测试主管", ""); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	srv := New(cfg, st)
	mux := http.NewServeMux()
	mux.Handle("/", srv.Handler())
	srv.registerSamplingRoutes(mux)
	ts := httptest.NewServer(mux)
	jar, _ := cookiejar.New(nil)
	client := ts.Client()
	client.Jar = jar
	rec := httptest.NewRecorder()
	if err := srv.issueSession(rec, store.OwnerID); err != nil {
		t.Fatalf("issue session: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("session cookie missing")
	}
	u, _ := url.Parse(ts.URL)
	jar.SetCookies(u, cookies)
	csrf, err := srv.csrfToken(store.OwnerID)
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	e := &samplingEnv{t: t, st: st, srv: srv, ts: ts, client: client, csrf: csrf}
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = st.Close()
	})
	return e
}

func (e *samplingEnv) seedReview(t *testing.T, reviewer, author, department string, approvals int) string {
	t.Helper()
	ctx := context.Background()
	art, err := e.st.CreateArtifact(ctx, domain.Artifact{
		Type: "doc", Title: "artifact " + reviewer + "/" + author,
		DepartmentID: department, CreatedBy: author,
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	rev, err := e.st.CreateReview(ctx, domain.Review{
		ArtifactID: art.ID, OwnerID: author, Claim: "claim " + reviewer,
		Severity: domain.SeverityBlocker, FromDepartment: department, ToDepartment: "review",
	})
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	for i := 0; i < approvals; i++ {
		if _, err := e.st.CreateReviewVerdict(ctx, domain.ReviewVerdict{
			ReviewID: rev.ID, Verdict: domain.VerdictApprove, ReviewerID: reviewer,
			Rationale: "looks good", Checked: "tests",
		}); err != nil {
			t.Fatalf("create verdict: %v", err)
		}
	}
	return rev.ID
}

func (e *samplingEnv) do(method, path string, body any, opts ...func(*http.Request)) (int, map[string]any) {
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
		e.t.Fatalf("request: %v", err)
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
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (e *samplingEnv) doCSRF(method, path string, body any) (int, map[string]any) {
	return e.do(method, path, body, func(r *http.Request) { r.Header.Set("X-Lignting-CSRF", e.csrf) })
}

func samplingItems(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("no data: %v", out)
	}
	raw, ok := data["items"].([]any)
	if !ok {
		t.Fatalf("no items: %v", out)
	}
	items := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("bad item: %v", v)
		}
		items = append(items, m)
	}
	return items
}

func TestSamplingQualityFlow(t *testing.T) {
	e := newSamplingEnv(t)
	reviewID := e.seedReview(t, "reviewer-1", "author-1", "dept-1", 5)

	status, out := e.do("GET", "/api/quality/reviews", nil)
	if status != http.StatusOK {
		t.Fatalf("quality status=%d out=%v", status, out)
	}
	items := samplingItems(t, out)
	if len(items) != 1 {
		t.Fatalf("quality items=%d", len(items))
	}
	st := items[0]
	if st["reviewer_id"] != "reviewer-1" || st["author_id"] != "author-1" ||
		st["total"].(float64) != 5 || st["approvals"].(float64) != 5 ||
		st["pass_rate"].(float64) != 1 || st["sampling_suggested"] != true {
		t.Fatalf("quality stat = %v", st)
	}

	status, out = e.do("GET", "/api/reviews/sampling/suggestions", nil)
	if status != http.StatusOK {
		t.Fatalf("suggestions status=%d out=%v", status, out)
	}
	sugs := samplingItems(t, out)
	if len(sugs) != 1 {
		t.Fatalf("suggestions=%d", len(sugs))
	}
	sug := sugs[0]
	if sug["sampling_suggested"] != true || sug["reviewer_id"] != "reviewer-1" {
		t.Fatalf("suggestion = %v", sug)
	}
	cands, _ := sug["candidates"].([]any)
	if len(cands) != 5 {
		t.Fatalf("candidates=%d", len(cands))
	}
	first := cands[0].(map[string]any)
	if first["review"] == nil || first["verdict"] == nil || first["dissent"] == nil {
		t.Fatalf("candidate missing review/verdict/dissent: %v", first)
	}

	note := "证据不足 " + samplingTestSecret
	status, out = e.doCSRF("POST", "/api/reviews/"+reviewID+"/sampling", map[string]any{
		"result": "invalid", "note": note,
	})
	if status != http.StatusOK {
		t.Fatalf("sampling status=%d out=%v", status, out)
	}
	status, out = e.do("GET", "/api/quality/reviews", nil)
	if status != http.StatusOK {
		t.Fatalf("quality2 status=%d", status)
	}
	st = samplingItems(t, out)[0]
	sm, _ := st["sampling"].(map[string]any)
	if sm["invalid"].(float64) != 1 || sm["total"].(float64) != 1 {
		t.Fatalf("sampling metrics = %v", st["sampling"])
	}
	if st["sampling_suggested"] != true {
		t.Fatalf("sampling_suggested should remain a hint, got %v", st)
	}

	audits, err := e.st.QueryAudit(context.Background(), store.AuditFilter{Action: "review.sampling"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("audit count=%d err=%v", len(audits), err)
	}
	detail := audits[0].Detail
	if strings.Contains(detail, samplingTestSecret) {
		t.Fatalf("audit leaks secret: %s", detail)
	}
	if !strings.Contains(detail, "[REDACTED]") || !strings.Contains(detail, "invalid") ||
		!strings.Contains(detail, "reviewer-1") || !strings.Contains(detail, "author-1") {
		t.Fatalf("audit detail bad: %s", detail)
	}

	if status, _ := e.doCSRF("POST", "/api/reviews/"+reviewID+"/sampling", map[string]any{"result": "bogus"}); status != http.StatusBadRequest {
		t.Fatalf("invalid result status=%d", status)
	}
	if status, _ := e.doCSRF("POST", "/api/reviews/rev_missing/sampling", map[string]any{"result": "valid"}); status != http.StatusNotFound {
		t.Fatalf("missing review status=%d", status)
	}
	if status, _ := e.do("POST", "/api/reviews/"+reviewID+"/sampling", map[string]any{"result": "valid"}); status != http.StatusForbidden {
		t.Fatalf("missing csrf status=%d", status)
	}
	noSessionReq, _ := http.NewRequest("POST", e.ts.URL+"/api/reviews/"+reviewID+"/sampling", nil)
	noSessionReq.Header.Set("X-Lignting-CSRF", e.csrf)
	noSessionResp, err := (&http.Client{}).Do(noSessionReq)
	if err != nil {
		t.Fatalf("missing session request: %v", err)
	}
	noSessionResp.Body.Close()
	if noSessionResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing session status=%d", noSessionResp.StatusCode)
	}

	plain, hash, err := store.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.CreateAgent(context.Background(), domain.Agent{
		Name: "员工", Kind: domain.AgentKindEmployee, DepartmentID: "dept-1", TokenHash: hash,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/reviews/"+reviewID+"/sampling", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lignting-Agent-Token", plain)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("agent sampling request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent sampling status=%d, want 403", resp.StatusCode)
	}
}

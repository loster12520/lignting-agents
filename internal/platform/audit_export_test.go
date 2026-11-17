package platform_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
)

func TestAuditExport(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()
	// 制造一条审计
	if _, err := e.st.AppendAudit(ctx, domain.AuditEntry{
		ActorType: "user", ActorID: "owner", Action: "test.action", ObjectType: "test", ObjectID: "x", Result: "ok", Detail: `{"k":"v"}`,
	}); err != nil {
		t.Fatalf("audit: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/api/audit/export?format=json", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Disposition"), "audit-") {
		t.Fatalf("json export status=%d cd=%q", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	if !strings.Contains(string(body[:n]), "test.action") {
		t.Fatalf("json export missing audit: %s", string(body[:n]))
	}
	req, _ = http.NewRequest(http.MethodGet, e.ts.URL+"/api/audit/export?format=csv", nil)
	resp2, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("csv export: %v", err)
	}
	defer resp2.Body.Close()
	if !strings.Contains(resp2.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("csv content-type = %q", resp2.Header.Get("Content-Type"))
	}
	csvBody := make([]byte, 8192)
	m, _ := resp2.Body.Read(csvBody)
	if !strings.Contains(string(csvBody[:m]), "seq,created_at") || !strings.Contains(string(csvBody[:m]), "test.action") {
		t.Fatalf("csv export = %s", string(csvBody[:m]))
	}
	if status, _ := e.do("GET", "/api/audit/export?format=xml", nil); status != http.StatusBadRequest {
		t.Fatalf("bad format status = %d, want 400", status)
	}
	audits, err := e.st.QueryAudit(ctx, store.AuditFilter{Action: "audit.export"})
	if err != nil || len(audits) != 2 {
		t.Fatalf("audit.export entries = %d err=%v", len(audits), err)
	}
}

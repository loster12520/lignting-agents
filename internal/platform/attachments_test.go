package platform_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/lignting/lignting-agents/internal/domain"
	"github.com/lignting/lignting-agents/internal/store"
	"io"
)

func jsonDecode(r io.Reader, v any) error { return json.NewDecoder(r).Decode(v) }

func (e *env) uploadAttachment(t *testing.T, convID, filename string, data []byte, withCSRF bool) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("multipart: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("multipart write: %v", err)
	}
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, e.ts.URL+"/api/conversations/"+convID+"/attachments", &buf)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if withCSRF {
		req.Header.Set("X-Lignting-CSRF", e.csrf)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = jsonDecode(resp.Body, &out)
	return resp.StatusCode, out
}

func (e *env) downloadAttachment(t *testing.T, id, agentToken string, useSession bool) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.ts.URL+"/api/attachments/"+id, nil)
	if err != nil {
		t.Fatalf("download request: %v", err)
	}
	if agentToken != "" {
		req.Header.Set("X-Lignting-Agent-Token", agentToken)
	}
	var client *http.Client
	if useSession {
		client = e.client
	} else {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	resp.Body.Close()
	return resp, body.Bytes()
}

func TestAttachmentsUploadDownloadACLAndDelete(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	ctx := context.Background()

	status, out := e.uploadAttachment(t, e.dmID, "测试 文档.txt", []byte("hello 附件内容"), true)
	if status != http.StatusOK {
		t.Fatalf("upload = %d %v", status, out)
	}
	d := e.data(out)
	attID, _ := d["id"].(string)
	if attID == "" || d["filename"] != "测试 文档.txt" || d["size"].(float64) != float64(len("hello 附件内容")) {
		t.Fatalf("attachment = %v", d)
	}
	if sha, _ := d["sha256"].(string); len(sha) != 64 {
		t.Fatalf("sha256 = %q", sha)
	}
	if _, leaked := d["storage_path"]; leaked {
		t.Fatalf("storage_path should not be returned: %v", d)
	}

	// 下载：attachment + nosniff + CSP sandbox + UTF-8 文件名
	resp, body := e.downloadAttachment(t, attID, "", true)
	if resp.StatusCode != http.StatusOK || string(body) != "hello 附件内容" {
		t.Fatalf("download status=%d body=%q", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "filename*=UTF-8''") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff = %q", resp.Header.Get("X-Content-Type-Options"))
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("CSP = %q, want sandbox", csp)
	}

	// 路径穿越文件名被截断为 basename
	status, out = e.uploadAttachment(t, e.dmID, "../../evil.txt", []byte("evil"), true)
	if status != http.StatusOK || e.data(out)["filename"] != "evil.txt" {
		t.Fatalf("traversal filename = %d %v", status, out)
	}

	// 不支持的类型
	if status, out := e.uploadAttachment(t, e.dmID, "run.exe", []byte("MZ"), true); status != http.StatusBadRequest {
		t.Fatalf("exe upload = %d %v", status, out)
	}

	// 越权：其他部门员工 Agent Token 下载 → 403 且留审计
	_, hash, _ := store.NewToken()
	plainOther, hashOther, _ := store.NewToken()
	deptB, _ := e.st.CreateDepartment(ctx, domain.Department{Name: "测试部", Kind: "qa"})
	otherAgent, err := e.st.CreateAgent(ctx, domain.Agent{Name: "外部员工", Kind: domain.AgentKindEmployee, DepartmentID: deptB.ID, TokenHash: hashOther})
	if err != nil {
		t.Fatalf("other agent: %v", err)
	}
	_ = otherAgent
	_ = hash
	resp, _ = e.downloadAttachment(t, attID, plainOther, false)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-dept download = %d, want 403", resp.StatusCode)
	}
	audits, err := e.st.QueryAudit(ctx, store.AuditFilter{Action: "attachment.download", Result: "denied"})
	if err != nil || len(audits) != 1 {
		t.Fatalf("denied audit = %d err=%v", len(audits), err)
	}

	// 加入会话成员后可下载
	if err := e.st.AddConversationMember(ctx, domain.ConversationMember{
		ConversationID: e.dmID, PrincipalType: domain.SenderAgent, PrincipalID: otherAgent.ID, Role: "member",
	}); err != nil {
		t.Fatalf("add member: %v", err)
	}
	resp, _ = e.downloadAttachment(t, attID, plainOther, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("member download = %d, want 200", resp.StatusCode)
	}

	// 大小上限（配置项）
	if err := e.st.SetSetting(ctx, "attachment.max_bytes", "10"); err != nil {
		t.Fatalf("set max: %v", err)
	}
	big := bytes.Repeat([]byte("x"), 100)
	if status, out := e.uploadAttachment(t, e.dmID, "big.txt", big, true); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload = %d, want 413, body=%v", status, out)
	}
	_ = e.st.DeleteSetting(ctx, "attachment.max_bytes")

	// 删除 → 墓碑 + 下载 404
	status, out = e.doCSRF("DELETE", "/api/attachments/"+attID, nil)
	if status != http.StatusOK {
		t.Fatalf("delete = %d %v", status, out)
	}
	if resp, _ := e.downloadAttachment(t, attID, "", true); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("download after delete = %d, want 404", resp.StatusCode)
	}
	meta, err := e.st.GetAttachment(ctx, attID)
	if err != nil || meta.DeletedAt == "" {
		t.Fatalf("tombstone = %+v err=%v", meta, err)
	}

	// CSRF 保护
	if status, _ := e.uploadAttachment(t, e.dmID, "x.txt", []byte("x"), false); status != http.StatusForbidden {
		t.Fatalf("upload without csrf = %d, want 403", status)
	}
}

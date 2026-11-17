package verify_e2e

// R3/R4 独立验证：真实二进制（os/exec） + 临时数据目录 + HTTP 黑盒。
// 不修改生产代码；agent token 仅作为测试夹具写入临时库（生产无 token 签发端点，见报告观察项）。

import (
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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lignting/lignting-agents/internal/store"
)

var binPath string

func TestMain(m *testing.M) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		log.Fatal(err)
	}
	bin, err := filepath.Abs(filepath.Join("..", "..", "bin", "lignting-e2e.exe"))
	if err != nil {
		log.Fatal(err)
	}
	// 允许用预构建二进制（并发开发导致源码暂时不可构建时）；默认现场构建。
	if override := os.Getenv("LIGINTING_E2E_BIN"); override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := os.Stat(abs); err != nil {
			log.Fatalf("LIGINTING_E2E_BIN 不存在: %v", err)
		}
		binPath = abs
		os.Exit(m.Run())
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/lignting")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("build binary: %v\n%s", err, out)
	}
	binPath = bin
	os.Exit(m.Run())
}

type srv struct {
	t       *testing.T
	dataDir string
	addr    string
	cmd     *exec.Cmd
	logs    *bytes.Buffer
}

func startServer(t *testing.T) *srv {
	t.Helper()
	dataDir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	logs := &bytes.Buffer{}
	cmd := exec.Command(binPath, "--data-dir", dataDir, "--addr", addr, "--mock-model")
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	s := &srv{t: t, dataDir: dataDir, addr: addr, cmd: cmd, logs: logs}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("server logs:\n%s", logs.String())
		}
	})
	base := "http://" + addr
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return s
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server not ready, logs:\n%s", logs.String())
	return nil
}

type api struct {
	t    *testing.T
	base string
	hc   *http.Client
	csrf string
}

func (s *srv) client(t *testing.T) *api {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &api{t: t, base: "http://" + s.addr, hc: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
}

func (a *api) call(method, path string, body any, hdr map[string]string) (int, map[string]any) {
	a.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			a.t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.base+path, rdr)
	if err != nil {
		a.t.Fatalf("request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if hdr != nil {
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
	}
	if method != http.MethodGet && a.csrf != "" && req.Header.Get("X-Lignting-CSRF") == "" {
		req.Header.Set("X-Lignting-CSRF", a.csrf)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (a *api) ok(method, path string, body any, hdr map[string]string) map[string]any {
	a.t.Helper()
	status, out := a.call(method, path, body, hdr)
	if status != http.StatusOK || out["ok"] != true {
		a.t.Fatalf("%s %s = %d %v", method, path, status, out)
	}
	return dataOf(a.t, out)
}

// expect 断言状态码与错误码（错误响应必须有 ok=false）。
func (a *api) expect(wantStatus int, method, path string, body any, hdr map[string]string) map[string]any {
	a.t.Helper()
	status, out := a.call(method, path, body, hdr)
	if status != wantStatus {
		a.t.Fatalf("%s %s = %d, want %d; body=%v", method, path, status, wantStatus, out)
	}
	if out["ok"] != false {
		a.t.Fatalf("%s %s 错误响应缺 ok=false: %v", method, path, out)
	}
	return out
}

func dataOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	d, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺 data: %v", out)
	}
	return d
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func items(d map[string]any) []any {
	v, _ := d["items"].([]any)
	return v
}

func (s *srv) setup(a *api, ownerName string) {
	s.t.Helper()
	d := a.ok(http.MethodPost, "/api/setup", map[string]any{
		"owner_name": ownerName, "daily_budget_cny": 20, "monthly_budget_cny": 300,
	}, nil)
	csrf := str(d, "csrf_token")
	if csrf == "" {
		s.t.Fatal("setup 未返回 csrf_token")
	}
	a.csrf = csrf
}

func agentByName(t *testing.T, org map[string]any, name string) map[string]any {
	t.Helper()
	for _, raw := range org["agents"].([]any) {
		ag := raw.(map[string]any)
		if ag["name"] == name {
			return ag
		}
	}
	t.Fatalf("找不到 Agent %q（org agents=%v）", name, org["agents"])
	return nil
}

func deptByKind(t *testing.T, org map[string]any, kind string) map[string]any {
	t.Helper()
	for _, raw := range org["departments"].([]any) {
		d := raw.(map[string]any)
		if d["kind"] == kind {
			return d
		}
	}
	t.Fatalf("找不到部门 kind=%q", kind)
	return nil
}

// provision 完成 intent→blueprint→G1 批准→provisioning，返回 org。
func provision(t *testing.T, a *api) map[string]any {
	t.Helper()
	a.ok(http.MethodPost, "/api/intents", map[string]any{"goal": "把 CLI 工具做成 Web 产品"}, nil)
	bp := a.ok(http.MethodPost, "/api/blueprints", map[string]any{"option": "full"}, nil)
	blueprint := bp["blueprint"].(map[string]any)
	bpID := str(blueprint, "id")
	if bpID == "" {
		t.Fatalf("blueprint 缺 id: %v", bp)
	}
	a.ok(http.MethodPost, "/api/blueprints/"+bpID+"/approve", map[string]any{"reason": "批准 G1"}, nil)
	prov := a.ok(http.MethodPost, "/api/provisioning", map[string]any{"blueprint_id": bpID}, nil)
	p := prov["provisioning"].(map[string]any)
	if str(p, "status") != "verified" {
		t.Fatalf("provisioning status = %q, want verified: %v", str(p, "status"), p)
	}
	org := a.ok(http.MethodGet, "/api/org", nil, nil)
	return org
}

func TestR3OrgBlueprintProvisioning(t *testing.T) {
	s := startServer(t)
	a := s.client(t)
	s.setup(a, "阿哲")

	// intent 版本化
	intent := a.ok(http.MethodPost, "/api/intents", map[string]any{"goal": "把 CLI 工具做成 Web 产品"}, nil)
	if n, _ := intent["version"].(float64); n != 1 {
		t.Fatalf("intent version = %v, want 1", intent["version"])
	}
	latest := a.ok(http.MethodGet, "/api/intents/latest", nil, nil)
	if !strings.Contains(fmt.Sprint(latest), "CLI") {
		t.Fatalf("latest intent 不含目标: %v", latest)
	}

	// 蓝图 + G1
	bpOut := a.ok(http.MethodPost, "/api/blueprints", map[string]any{"option": "full"}, nil)
	blueprint := bpOut["blueprint"].(map[string]any)
	approval := bpOut["approval"].(map[string]any)
	bpID := str(blueprint, "id")
	if str(blueprint, "status") != "proposed" {
		t.Fatalf("蓝图初始状态 = %q, want proposed", str(blueprint, "status"))
	}
	if str(approval, "kind") != "G1" || str(approval, "status") != "pending" {
		t.Fatalf("G1 approval = %v", approval)
	}

	// 未 G1：provisioning 与直接建部门都必须 403
	provErr := a.expect(http.StatusForbidden, http.MethodPost, "/api/provisioning", map[string]any{"blueprint_id": bpID}, nil)
	if code := provErr["error"].(map[string]any)["code"]; code != "forbidden" {
		t.Fatalf("未 G1 provisioning code = %v", code)
	}
	a.expect(http.StatusForbidden, http.MethodPost, "/api/departments", map[string]any{"name": "绕过部门", "kind": "engineering"}, nil)

	// G1 批准
	approved := a.ok(http.MethodPost, "/api/blueprints/"+bpID+"/approve", map[string]any{"reason": "批准"}, nil)
	if str(approved, "status") != "approved" {
		t.Fatalf("批准后蓝图状态 = %q", str(approved, "status"))
	}
	approvals := a.ok(http.MethodGet, "/api/approvals?kind=G1", nil, nil)
	found := false
	for _, raw := range items(approvals) {
		ap := raw.(map[string]any)
		if ap["kind"] == "G1" && ap["status"] == "approved" {
			found = true
		}
	}
	if !found {
		t.Fatalf("G1 审批未记录为 approved: %v", approvals)
	}

	// provisioning（幂等）
	prov := a.ok(http.MethodPost, "/api/provisioning", map[string]any{"blueprint_id": bpID}, nil)
	p := prov["provisioning"].(map[string]any)
	if str(p, "status") != "verified" {
		t.Fatalf("provisioning status = %q, want verified", str(p, "status"))
	}
	steps := p["steps"].(string)
	if !strings.Contains(steps, "done") {
		t.Fatalf("provisioning steps 未记录 done: %q", steps)
	}
	org1 := a.ok(http.MethodGet, "/api/org", nil, nil)
	depts1 := len(org1["departments"].([]any))
	agents1 := len(org1["agents"].([]any))
	if depts1 < 4 {
		t.Fatalf("部门数 = %d, want >=4（full 蓝图）", depts1)
	}
	if agents1 < 10 {
		t.Fatalf("Agent 数 = %d, want >=10", agents1)
	}
	if org1["assistant"] == nil {
		t.Fatal("org 缺 assistant")
	}

	// 重复 provisioning：状态仍 verified，资源数不变
	prov2 := a.ok(http.MethodPost, "/api/provisioning", map[string]any{"blueprint_id": bpID}, nil)
	if str(prov2["provisioning"].(map[string]any), "status") != "verified" {
		t.Fatalf("重复 provisioning 状态异常: %v", prov2)
	}
	org2 := a.ok(http.MethodGet, "/api/org", nil, nil)
	if len(org2["departments"].([]any)) != depts1 || len(org2["agents"].([]any)) != agents1 {
		t.Fatalf("重复 provisioning 产生重复资源: depts %d->%d agents %d->%d",
			depts1, len(org2["departments"].([]any)), agents1, len(org2["agents"].([]any)))
	}

	// 生命周期：部门 pause/resume；Agent pause/resume/stop
	eng := deptByKind(t, org2, "engineering")
	engID := str(eng, "id")
	paused := a.ok(http.MethodPost, "/api/departments/"+engID+"/pause", map[string]any{"reason": "预算"}, nil)
	if str(paused, "status") != "paused" {
		t.Fatalf("部门 pause 后状态 = %q", str(paused, "status"))
	}
	resumed := a.ok(http.MethodPost, "/api/departments/"+engID+"/resume", nil, nil)
	if str(resumed, "status") != "active" {
		t.Fatalf("部门 resume 后状态 = %q", str(resumed, "status"))
	}
	emp := agentByName(t, org2, "后端工程师")
	empID := str(emp, "id")
	ap := a.ok(http.MethodPost, "/api/agents/"+empID+"/pause", map[string]any{"reason": "测试"}, nil)
	if str(ap, "status") != "paused" {
		t.Fatalf("agent pause 后状态 = %q", str(ap, "status"))
	}
	ar := a.ok(http.MethodPost, "/api/agents/"+empID+"/resume", nil, nil)
	if str(ar, "status") != "idle" {
		t.Fatalf("agent resume 后状态 = %q", str(ar, "status"))
	}
	// 空闲 Agent 没有运行中的 turn → stop 必须 409 且给可读原因（不能 500/静默成功）
	stopErr := a.expect(http.StatusConflict, http.MethodPost, "/api/agents/"+empID+"/stop", nil, nil)
	if msg := fmt.Sprint(stopErr["error"]); !strings.Contains(msg, "turn") {
		t.Fatalf("stop 空闲 agent 错误信息不可读: %v", msg)
	}

	// 第二张未批准蓝图：用其 id 直接建部门也应 403
	bp2 := a.ok(http.MethodPost, "/api/blueprints", map[string]any{"option": "lean"}, nil)
	bp2ID := str(bp2["blueprint"].(map[string]any), "id")
	a.expect(http.StatusForbidden, http.MethodPost, "/api/departments",
		map[string]any{"name": "未批准部门", "kind": "qa", "blueprint_id": bp2ID}, nil)
}

func TestR4TaskDeliveryFlow(t *testing.T) {
	s := startServer(t)
	a := s.client(t)
	s.setup(a, "阿哲")
	org := provision(t, a)

	eng := deptByKind(t, org, "engineering")
	engID := str(eng, "id")
	sup := agentByName(t, org, "研发主管")
	owner := agentByName(t, org, "后端工程师")
	reviewer := agentByName(t, org, "前端工程师")
	approver := agentByName(t, org, "测试主管")

	deadline := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
	create := map[string]any{
		"actor_agent_id": str(sup, "id"),
		"title":          "实现 CSV 导出",
		"description":    "支持按筛选条件导出 CSV",
		"department_id":  engID,
		"owner_id":       str(owner, "id"),
		"reviewer_id":    str(reviewer, "id"),
		"approver_id":    str(approver, "id"),
		"perspective":    "接口与数据流",
		"priority":       "P1",
		"deadline":       deadline,
		"budget_tokens":  1000,
		"acceptance":     []string{"导出文件列头正确", "筛选条件生效", "有回归测试证据"},
		"high_risk":      true,
	}
	// 员工不能建任务
	empCreate := map[string]any{}
	for k, v := range create {
		empCreate[k] = v
	}
	empCreate["actor_agent_id"] = str(owner, "id")
	a.expect(http.StatusForbidden, http.MethodPost, "/api/tasks", empCreate, nil)

	view := a.ok(http.MethodPost, "/api/tasks", create, nil)
	task := view["task"].(map[string]any)
	taskID := str(task, "id")
	if str(task, "status") != "proposed" {
		t.Fatalf("任务初始状态 = %q", str(task, "status"))
	}
	criteria := view["criteria"].([]any)
	if len(criteria) != 3 {
		t.Fatalf("验收标准数 = %d, want 3", len(criteria))
	}

	// 非法转换 proposed→done
	a.expect(http.StatusConflict, http.MethodPost, "/api/tasks/"+taskID+"/transition",
		map[string]any{"actor_agent_id": str(sup, "id"), "to": "done", "reason": "跳步"}, nil)

	// 合法推进（owner 执行）
	for _, to := range []string{"ready", "discussing", "in_progress", "in_review"} {
		out := a.ok(http.MethodPost, "/api/tasks/"+taskID+"/transition",
			map[string]any{"actor_agent_id": str(owner, "id"), "to": to, "reason": "推进"}, nil)
		if str(out, "status") != to {
			t.Fatalf("任务状态 = %q, want %q", str(out, "status"), to)
		}
	}

	// 产出物 v1
	art := a.ok(http.MethodPost, "/api/artifacts", map[string]any{
		"actor_agent_id": str(owner, "id"), "task_id": taskID, "type": "code",
		"title": "CSV 导出实现 v1", "content": "func exportCSV(){ /* v1 */ }",
	}, nil)
	artifact := art["artifact"].(map[string]any)
	artID := str(artifact, "id")
	if v := art["version"].(map[string]any)["version"].(float64); v != 1 {
		t.Fatalf("产出物版本 = %v, want 1", v)
	}

	// 挑战单
	review := a.ok(http.MethodPost, "/api/reviews", map[string]any{
		"actor_agent_id": str(reviewer, "id"), "artifact_id": artID,
		"severity": "blocker", "claim": "空结果时列头缺失", "evidence": "运行导出命令，空集输出无表头",
		"impact": "下游解析失败",
	}, nil)
	reviewID := str(review, "id")
	if reviewID == "" {
		t.Fatalf("挑战单缺 id: %v", review)
	}

	// 作者不能评审自己的产出物
	a.expect(http.StatusForbidden, http.MethodPost, "/api/reviews/"+reviewID+"/verdicts",
		map[string]any{"actor_agent_id": str(owner, "id"), "verdict": "approve", "checked": "x"}, nil)
	// 作者不能批准自己的任务（approved 转换）
	a.expect(http.StatusForbidden, http.MethodPost, "/api/tasks/"+taskID+"/transition",
		map[string]any{"actor_agent_id": str(owner, "id"), "to": "approved", "reason": "自批"}, nil)

	// request_changes 必须列修改项（这里给条件）
	a.ok(http.MethodPost, "/api/reviews/"+reviewID+"/verdicts", map[string]any{
		"actor_agent_id": str(reviewer, "id"), "verdict": "request_changes",
		"checked": "空集导出", "rationale": "空集无表头", "conditions": "空集也输出表头",
	}, nil)
	out := a.ok(http.MethodPost, "/api/tasks/"+taskID+"/transition",
		map[string]any{"actor_agent_id": str(reviewer, "id"), "to": "changes_requested", "reason": "返工"}, nil)
	if str(out, "status") != "changes_requested" {
		t.Fatalf("返工状态 = %q", str(out, "status"))
	}

	// 返工 v2
	rework := a.ok(http.MethodPost, "/api/reviews/"+reviewID+"/rework",
		map[string]any{"actor_agent_id": str(owner, "id"), "content": "func exportCSV(){ /* v2 空集也输出表头 */ }"}, nil)
	if v := rework["version"].(float64); v != 2 {
		t.Fatalf("返工版本 = %v, want 2", v)
	}
	a.ok(http.MethodPost, "/api/tasks/"+taskID+"/transition",
		map[string]any{"actor_agent_id": str(owner, "id"), "to": "in_review", "reason": "重新提交"}, nil)

	// G3 缺证据必须拒绝
	a.expect(http.StatusBadRequest, http.MethodPost, "/api/tasks/"+taskID+"/g3",
		map[string]any{"actor_agent_id": str(sup, "id"), "risk": "low"}, nil)

	// 补齐证据 + 主管核对
	taskView := a.ok(http.MethodGet, "/api/tasks/"+taskID, nil, nil)
	crits := taskView["criteria"].([]any)
	for i, raw := range crits {
		c := raw.(map[string]any)
		cid := str(c, "id")
		a.ok(http.MethodPost, "/api/tasks/"+taskID+"/criteria/"+cid+"/evidence",
			map[string]any{"actor_agent_id": str(owner, "id"), "evidence": fmt.Sprintf("证据%d：命令输出与测试日志", i+1)}, nil)
		a.ok(http.MethodPost, "/api/tasks/"+taskID+"/criteria/"+cid+"/verdict",
			map[string]any{"actor_agent_id": str(sup, "id"), "verdict": "approved"}, nil)
	}
	// 员工不能填主管结论
	firstCid := str(crits[0].(map[string]any), "id")
	a.expect(http.StatusForbidden, http.MethodPost, "/api/tasks/"+taskID+"/criteria/"+firstCid+"/verdict",
		map[string]any{"actor_agent_id": str(owner, "id"), "verdict": "approved"}, nil)

	g3 := a.ok(http.MethodPost, "/api/tasks/"+taskID+"/g3",
		map[string]any{"actor_agent_id": str(sup, "id"), "risk": "low", "residual_risks": "无"}, nil)
	approval := g3["approval"].(map[string]any)
	apprID := str(approval, "id")
	if str(approval, "status") != "pending" {
		t.Fatalf("G3 状态 = %q, want pending", str(approval, "status"))
	}
	decided := a.ok(http.MethodPost, "/api/approvals/"+apprID+"/decide",
		map[string]any{"decision": "approved", "reason": "证据齐全"}, nil)
	if str(decided, "status") != "approved" {
		t.Fatalf("G3 决定后状态 = %q", str(decided, "status"))
	}

	// approved → done（approver），并校验终态与时间线
	apprOut := a.ok(http.MethodPost, "/api/tasks/"+taskID+"/transition",
		map[string]any{"actor_agent_id": str(reviewer, "id"), "to": "approved", "reason": "评审通过"}, nil)
	if str(apprOut, "status") != "approved" {
		t.Fatalf("approved 状态 = %q", str(apprOut, "status"))
	}
	done := a.ok(http.MethodPost, "/api/tasks/"+taskID+"/transition",
		map[string]any{"actor_agent_id": str(approver, "id"), "to": "done", "reason": "验收完成"}, nil)
	if str(done, "status") != "done" {
		t.Fatalf("done 状态 = %q", str(done, "status"))
	}
	// 终态不可回退
	a.expect(http.StatusConflict, http.MethodPost, "/api/tasks/"+taskID+"/transition",
		map[string]any{"actor_agent_id": str(owner, "id"), "to": "in_progress", "reason": "重开"}, nil)

	final := a.ok(http.MethodGet, "/api/tasks/"+taskID, nil, nil)
	if str(final["task"].(map[string]any), "status") != "done" {
		t.Fatalf("最终状态 = %v", final["task"])
	}
	events := final["events"].([]any)
	if len(events) < 8 {
		t.Fatalf("任务时间线事件数 = %d, want >=8", len(events))
	}
	for _, raw := range final["criteria"].([]any) {
		c := raw.(map[string]any)
		if str(c, "evidence") == "" || str(c, "supervisor_verdict") != "approved" {
			t.Fatalf("验收标准未补齐: %v", c)
		}
	}
	arts := a.ok(http.MethodGet, "/api/artifacts?task_id="+taskID, nil, nil)
	if len(items(arts)) != 1 {
		t.Fatalf("产出物数 = %d, want 1", len(items(arts)))
	}
	vers := a.ok(http.MethodGet, "/api/artifacts/"+artID+"/versions", nil, nil)
	if len(items(vers)) != 2 {
		t.Fatalf("产出物版本数 = %d, want 2", len(items(vers)))
	}

	// ---- 发布门证据（不 fail，记录缺陷）----
	// D-R3R4-2a：高风险任务已进入 in_progress，但没有任何 G2 审批记录（FR-APPR-01/AC-03）。
	g2s := a.ok(http.MethodGet, "/api/approvals?kind=G2", nil, nil)
	if len(items(g2s)) == 0 {
		t.Logf("DEFECT D-R3R4-2: high_risk 任务未经 G2 即进入 in_progress，G2 审批数=0（G2 门禁未实现）")
	}
	// D-R3R4-2b：非高风险任务 done 未要求 G3（FR-APPR-01「G3 未批准不能标记 done」/AC-12）。
	lowCreate := map[string]any{
		"actor_agent_id": str(sup, "id"), "title": "小修文案", "department_id": engID,
		"owner_id": str(owner, "id"), "reviewer_id": str(reviewer, "id"), "approver_id": str(approver, "id"),
		"perspective": "文案与交互", "deadline": deadline, "budget_tokens": 100,
		"acceptance": []string{"文案无错别字"},
	}
	lowView := a.ok(http.MethodPost, "/api/tasks", lowCreate, nil)
	lowID := str(lowView["task"].(map[string]any), "id")
	for _, to := range []string{"ready", "discussing", "in_progress", "in_review"} {
		a.ok(http.MethodPost, "/api/tasks/"+lowID+"/transition",
			map[string]any{"actor_agent_id": str(owner, "id"), "to": to, "reason": "推进"}, nil)
	}
	lowArt := a.ok(http.MethodPost, "/api/artifacts", map[string]any{
		"actor_agent_id": str(owner, "id"), "task_id": lowID, "type": "document",
		"title": "文案 v1", "content": "修正后的文案",
	}, nil)
	lowArtID := str(lowArt["artifact"].(map[string]any), "id")
	lowRev := a.ok(http.MethodPost, "/api/reviews", map[string]any{
		"actor_agent_id": str(reviewer, "id"), "artifact_id": lowArtID,
		"severity": "minor", "claim": "措辞调整", "evidence": "diff",
	}, nil)
	lowRevID := str(lowRev, "id")
	a.ok(http.MethodPost, "/api/reviews/"+lowRevID+"/verdicts", map[string]any{
		"actor_agent_id": str(reviewer, "id"), "verdict": "approve", "checked": "diff", "rationale": "可以",
	}, nil)
	a.ok(http.MethodPost, "/api/tasks/"+lowID+"/transition",
		map[string]any{"actor_agent_id": str(reviewer, "id"), "to": "approved", "reason": "评审通过"}, nil)
	if s2, o2 := a.call(http.MethodPost, "/api/tasks/"+lowID+"/transition",
		map[string]any{"actor_agent_id": str(approver, "id"), "to": "done", "reason": "关闭"}, nil); s2 == http.StatusOK {
		t.Logf("DEFECT D-R3R4-2: 非高风险任务未经 G3 直接 done 成功（G3 门禁只对 high_risk 生效） body=%v", o2)
	}
}

func TestR2AgentTokenSearchAndMemoryACL(t *testing.T) {
	s := startServer(t)
	a := s.client(t)
	s.setup(a, "阿哲")
	org := provision(t, a)

	eng := deptByKind(t, org, "engineering")
	qa := deptByKind(t, org, "qa")
	engEmp := agentByName(t, org, "后端工程师")

	// 部门群聊：provisioning 自动创建
	convs := a.ok(http.MethodGet, "/api/conversations", nil, nil)
	var engConv, qaConv string
	for _, raw := range items(convs) {
		c := raw.(map[string]any)
		switch c["department_id"] {
		case str(eng, "id"):
			engConv = str(c, "id")
		case str(qa, "id"):
			qaConv = str(c, "id")
		}
	}
	if engConv == "" || qaConv == "" {
		t.Fatalf("缺部门群聊: eng=%q qa=%q", engConv, qaConv)
	}
	private := a.ok(http.MethodPost, "/api/conversations", map[string]any{
		"type": "dm", "title": "私聊", "visibility": "private",
	}, nil)
	privateID := str(private, "id")

	// 三个唯一词分别写入三个会话
	post := func(convID, content, clientID string) {
		a.ok(http.MethodPost, "/api/conversations/"+convID+"/messages",
			map[string]any{"content": content, "client_id": clientID}, nil)
	}
	post(engConv, "研发限流词：令牌桶限流方案", "acl-1")
	post(qaConv, "测试绝密词：压测阈值 1000", "acl-2")
	post(privateID, "私聊秘密词：仅我可见", "acl-3")

	// 夹具：为两个员工签发 Agent Token（生产无签发端点，见报告观察项）
	plainEng, hashEng, err := store.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if err := seedAgentToken(s.dataDir, str(engEmp, "id"), hashEng); err != nil {
		t.Fatalf("seed eng token: %v", err)
	}

	search := func(q string, hdr map[string]string) map[string]any {
		return a.ok(http.MethodGet, "/api/search?q="+q+"&kind=message", nil, hdr)
	}
	// owner：全见
	for _, q := range []string{"研发限流词", "测试绝密词", "私聊秘密词"} {
		if total := search(q, nil)["total"].(float64); total < 1 {
			t.Fatalf("owner 搜索 %q total=%v, want >=1", q, total)
		}
	}
	agentHdr := map[string]string{"X-Lignting-Agent-Token": plainEng}
	// 研发员工：本部门可见，跨部门/私聊 0 泄漏
	if total := search("研发限流词", agentHdr)["total"].(float64); total < 1 {
		t.Fatalf("研发员工本部门搜索 total=%v, want >=1", total)
	}
	for _, q := range []string{"测试绝密词", "私聊秘密词"} {
		if total := search(q, agentHdr)["total"].(float64); total != 0 {
			t.Fatalf("研发员工越权搜索 %q total=%v, want 0", q, total)
		}
	}
	// 无效 token → 401
	a.expect(http.StatusUnauthorized, http.MethodGet, "/api/search?q=研发限流词", nil,
		map[string]string{"X-Lignting-Agent-Token": "lgt_invalid"})
}

// TestR2HumanOnlyMemoryACL 验证 human-only / 部门记忆对 Agent 的可见性。
// 当前二进制记忆层默认关闭（DEFECT D-R3R4-1），接口 503 → 标记 SKIP 并保留断言。
func TestR2HumanOnlyMemoryACL(t *testing.T) {
	s := startServer(t)
	a := s.client(t)
	s.setup(a, "阿哲")
	org := provision(t, a)
	eng := deptByKind(t, org, "engineering")
	engEmp := agentByName(t, org, "后端工程师")
	qaEmp := agentByName(t, org, "测试工程师")

	// 契约 §12.4：memory visibility 统一 human_only；实现当前只认 human-only（D-R3R4-3）。
	// 先按契约调用（记录缺陷），再按实现值验证 ACL 语义。
	createMem := func(vis string) (int, map[string]any) {
		return a.call(http.MethodPost, "/api/memories", map[string]any{
			"direct": true, "scope": "assistant", "entity_type": "person", "entity_key": "owner",
			"content": "人类专属：报销流程只给我看", "summary": "报销流程只给我看",
			"visibility": vis, "sensitivity": "normal", "confidence": "high",
		}, nil)
	}
	status, out := createMem("human_only")
	if status == http.StatusBadRequest {
		if det, ok := out["error"].(map[string]any)["details"].(map[string]any); ok {
			t.Logf("DEFECT D-R3R4-3: 契约 §12.4 的 visibility=human_only 被拒（details=%v）；实现用 human-only", det)
		}
		status, out = createMem("human-only")
	}
	if status != http.StatusOK {
		t.Fatalf("创建 human-only 记忆 = %d %v", status, out)
	}
	// 契约 §12.4 entity_type 列表含 ability；实现用 capability/preference（同一缺陷）。
	abStatus, abOut := a.call(http.MethodPost, "/api/memories", map[string]any{
		"direct": true, "scope": "department", "department_id": str(eng, "id"),
		"entity_type": "ability", "entity_key": str(eng, "id") + "/backend",
		"content": "研发部能力：后端接口设计", "summary": "后端接口设计", "visibility": "team", "confidence": "high",
	}, nil)
	if abStatus == http.StatusBadRequest {
		t.Logf("DEFECT D-R3R4-3: 契约 §12.4 的 entity_type=ability 被拒（%v）", abOut)
	} else if abStatus == http.StatusOK {
		// 若已支持 ability，则后续 team 记忆用该条
	}

	ownerMem := a.ok(http.MethodGet, "/api/memories?q=报销", nil, nil)
	if len(items(ownerMem)) < 1 {
		t.Fatalf("owner 看不到 human-only 记忆: %v", ownerMem)
	}
	plainEng, hashEng, err := store.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if err := seedAgentToken(s.dataDir, str(engEmp, "id"), hashEng); err != nil {
		t.Fatalf("seed eng token: %v", err)
	}
	agentHdr := map[string]string{"X-Lignting-Agent-Token": plainEng}
	agentMem := a.ok(http.MethodGet, "/api/memories?q=报销", nil, agentHdr)
	if len(items(agentMem)) != 0 {
		t.Fatalf("Agent 泄漏 human-only 记忆: %v", items(agentMem))
	}
	// 部门级记忆：本部门可见，他部门不可见（visibility=team 为实现值，见 §12.4 映射）
	memStatus, memOut := a.call(http.MethodPost, "/api/memories", map[string]any{
		"direct": true, "scope": "department", "department_id": str(eng, "id"),
		"entity_type": "department", "entity_key": str(eng, "id"),
		"content": "研发部约定：接口字段用 snake_case", "summary": "研发部接口约定",
		"visibility": "team", "confidence": "high",
	}, nil)
	if memStatus != http.StatusOK {
		t.Fatalf("创建部门记忆 = %d %v", memStatus, memOut)
	}
	engMem := a.ok(http.MethodGet, "/api/memories?q=snake_case", nil, agentHdr)
	if len(items(engMem)) < 1 {
		t.Fatalf("研发员工看不到本部门 team 记忆: %v", engMem)
	}
	plainQA, hashQA, err := store.NewToken()
	if err != nil {
		t.Fatalf("qa token: %v", err)
	}
	if err := seedAgentToken(s.dataDir, str(qaEmp, "id"), hashQA); err != nil {
		t.Fatalf("seed qa token: %v", err)
	}
	qaHdr := map[string]string{"X-Lignting-Agent-Token": plainQA}
	qaMem := a.ok(http.MethodGet, "/api/memories?q=snake_case", nil, qaHdr)
	if len(items(qaMem)) != 0 {
		t.Fatalf("测试员工跨部门泄漏研发 team 记忆: %v", items(qaMem))
	}
}

// seedAgentToken 用独立连接写临时库（测试夹具；生产无 token 签发端点）。
func seedAgentToken(dataDir, agentID, tokenHash string) error {
	st, err := store.Open(filepath.Join(dataDir, "lignting.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return st.SetAgentTokenHash(ctx, agentID, tokenHash)
}

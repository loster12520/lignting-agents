// 任务板：真实 R4 API —— 列表/过滤/详情（criteria/证据/依赖/时间线）/状态转换/G3 核对表。
// 状态机与权限由核心校验；前端只展示可用动作，并原样显示 400/403/409。
import { el, clear, icon, badge, notice, emptyState, toast, confirmModal, fmtDateTime, relTime, truncate } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { renderMarkdown } from "../lib/mdrender.js";
import { statusBadge, TASK_STATUS, nameOfAgent, nameOfDept } from "../lib/ui.js";

const STATUSES = ["proposed", "ready", "discussing", "in_progress", "blocked", "in_review", "changes_requested", "approved", "done", "canceled", "failed"];
const TRANSITIONS = {
  proposed: ["ready", "canceled"], ready: ["discussing", "canceled"], discussing: ["in_progress", "changes_requested"],
  in_progress: ["in_review", "blocked", "canceled", "failed"], blocked: ["in_progress", "canceled"],
  in_review: ["approved", "changes_requested"], changes_requested: ["in_review", "canceled"],
  approved: ["done"], done: [], canceled: [], failed: []
};
const VERDICT = { approved: ["通过", "ok"], changes_requested: ["需返工", "warn"], human_needed: ["需人类判断", "violet"] };

function newForm() {
  return { title: "", department_id: "", owner_id: "", perspective: "", priority: "P1", deadline: "", acceptance: "", high_risk: false };
}

export function mount(ctx, route) {
  const detailID = (route && route.query && route.query.id) || "";
  const s = {
    items: [], loading: true, error: null, status: "", departmentId: "",
    detail: null, detailLoading: false, detailError: null, artifacts: [],
    actionError: null, actionHint: "", busy: "", reason: "", createOpen: false, form: newForm()
  };
  let disposed = false;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "任务"),
    el("div", { class: "topbar-sub" },
      el("span", { text: "状态机 / DoR / G3 由核心校验；平台只展示投影" }),
      ctx.demo ? badge("演示数据", "warn") : null)));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", {
      class: "btn btn-sm", attrs: { type: "button" },
      on: { click: function () { if (detailID) loadDetail(detailID); else loadList(); } }
    }, icon("retry", 14), el("span", { text: "刷新" }))));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  function fail(e, hint) { s.actionError = e; s.actionHint = hint || ""; s.busy = ""; render(); }
  function go(id) { ctx.navigate(id ? "#/tasks?id=" + encodeURIComponent(id) : "#/tasks"); }

  async function loadList() {
    s.loading = true; s.error = null; render();
    try {
      const params = {};
      if (s.status) params.status = s.status;
      if (s.departmentId) params.department_id = s.departmentId;
      const res = await ctx.api.tasks(params);
      if (disposed) return;
      s.items = (res && res.items) || []; s.loading = false; render();
    } catch (e) {
      if (disposed) return;
      s.loading = false; s.error = e; render();
    }
  }

  async function loadDetail(id) {
    s.detailLoading = true; s.detailError = null; render();
    try {
      const res = await ctx.api.task(id);
      if (disposed) return;
      s.detail = res || null; s.detailLoading = false; render();
      try {
        const arts = await ctx.api.artifacts({ task_id: id });
        if (!disposed) { s.artifacts = (arts && arts.items) || []; render(); }
      } catch (e2) { /* 产出物读取失败不阻塞任务详情 */ }
    } catch (e) {
      if (disposed) return;
      s.detailLoading = false; s.detailError = e; render();
    }
  }

  function render() { clear(host); if (detailID) renderDetail(); else renderList(); }

  function errCard() {
    return el("div", { class: "card", attrs: { style: "margin-top:12px;border-color:rgba(251,113,133,.4)" } },
      el("div", { class: "card-title" }, el("span", { text: "操作失败" }), badge(s.actionError.code || "error", "bad")),
      el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.actionError.message || String(s.actionError) }),
      s.actionHint ? el("div", { class: "help", attrs: { style: "margin-top:6px" }, text: s.actionHint }) : null,
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.actionError = null; s.actionHint = ""; render(); } } }, "关闭"),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.actionError = null; if (detailID) loadDetail(detailID); else loadList(); } } }, "重新读取")));
  }

  function errorBlock(title, err, retry) {
    return el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title", text: title }),
      el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: (err && err.message) || String(err) }),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: retry } }, "重试")));
  }

  // ---------- 列表 ----------
  function renderList() {
    host.appendChild(el("div", { class: "page-head" },
      el("div", null, el("h2", { text: "任务板" }), el("p", { text: "按部门/状态过滤；点进详情看验收标准、证据与 hash chain 时间线。" })),
      el("span", { class: "grow" }),
      el("button", {
        class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: ctx.demo },
        on: { click: function () { s.createOpen = !s.createOpen; render(); } }
      }, icon("plus", 14), el("span", { text: "新建任务" }))));

    if (ctx.demo) host.appendChild(notice("演示模式：任务列表需要真实后端（R4 /api/tasks）。", "warn"));
    if (s.createOpen) host.appendChild(createForm());

    const filters = el("div", { class: "filters" });
    const statusSel = el("select", { class: "select", attrs: { "aria-label": "按状态过滤" } });
    statusSel.appendChild(el("option", { attrs: { value: "" }, text: "全部状态" }));
    STATUSES.forEach(function (st) { statusSel.appendChild(el("option", { attrs: { value: st }, text: (TASK_STATUS[st] || [st])[0] })); });
    statusSel.value = s.status;
    statusSel.addEventListener("change", function () { s.status = statusSel.value; loadList(); });
    filters.appendChild(statusSel);

    const deptSel = el("select", { class: "select", attrs: { "aria-label": "按部门过滤" } });
    deptSel.appendChild(el("option", { attrs: { value: "" }, text: "全部部门" }));
    state.departments.forEach(function (d) { deptSel.appendChild(el("option", { attrs: { value: d.id }, text: d.name })); });
    deptSel.value = s.departmentId;
    deptSel.addEventListener("change", function () { s.departmentId = deptSel.value; loadList(); });
    filters.appendChild(deptSel);
    host.appendChild(filters);

    if (s.actionError) host.appendChild(errCard());
    if (s.loading) { host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载任务…" }))); return; }
    if (s.error) { host.appendChild(errorBlock("任务加载失败", s.error, loadList)); return; }
    if (!s.items.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "task", title: "还没有任务",
        desc: "任务由助理/部门主管按 create_task 创建（部门、DoR、分工视角）；也可点右上角新建。",
        later: "G3 核对表在任务详情页"
      })));
      return;
    }

    const table = el("table", { class: "table", attrs: { style: "margin-top:14px" } },
      el("thead", null, el("tr", null, ["任务", "状态", "部门", "负责人", "视角", "优先级", "截止"].map(function (h) { return el("th", { text: h }); }))));
    const tbody = el("tbody");
    s.items.forEach(function (t) {
      const dept = state.departments.filter(function (d) { return d.id === t.department_id; })[0];
      tbody.appendChild(el("tr", { attrs: { style: "cursor:pointer" }, on: { click: function () { go(t.id); } } },
        el("td", null, el("div", { attrs: { style: "font-weight:600" }, text: t.title }),
          t.perspective ? el("div", { class: "card-meta", text: "视角：" + t.perspective }) : null),
        el("td", null, statusBadge(t.status, TASK_STATUS)),
        el("td", { text: dept ? dept.name : (t.department_id || "—") }),
        el("td", { text: nameOfAgent(t.owner_id) }),
        el("td", { text: t.perspective || "—" }),
        el("td", null, badge(t.priority || "P2", t.priority === "P0" ? "bad" : t.priority === "P1" ? "warn" : "")),
        el("td", { attrs: { title: fmtDateTime(t.deadline) }, text: t.deadline ? relTime(t.deadline) : "—" })));
    });
    table.appendChild(tbody);
    host.appendChild(table);
  }

  // ---------- 新建 ----------
  function createForm() {
    const f = s.form;
    const field = function (label, node) { return el("div", { class: "field" }, el("label", { text: label }), node); };
    const title = el("input", { class: "input", attrs: { type: "text", placeholder: "任务标题" } });
    title.value = f.title; title.addEventListener("input", function () { f.title = title.value; });
    const deptSel = el("select", { class: "select" });
    deptSel.appendChild(el("option", { attrs: { value: "" }, text: "选择部门" }));
    state.departments.forEach(function (d) { deptSel.appendChild(el("option", { attrs: { value: d.id }, text: d.name })); });
    deptSel.value = f.department_id;
    deptSel.addEventListener("change", function () { f.department_id = deptSel.value; f.owner_id = ""; render(); });
    const ownerSel = el("select", { class: "select" });
    ownerSel.appendChild(el("option", { attrs: { value: "" }, text: "选择 owner（Agent）" }));
    state.agents.filter(function (a) { return a.kind !== "assistant" && (!f.department_id || a.department_id === f.department_id); })
      .forEach(function (a) { ownerSel.appendChild(el("option", { attrs: { value: a.id }, text: a.name + "（" + (a.perspective || a.kind) + "）" })); });
    ownerSel.value = f.owner_id;
    ownerSel.addEventListener("change", function () { f.owner_id = ownerSel.value; });
    const perspective = el("input", { class: "input", attrs: { type: "text", placeholder: "接口 / 页面 / 用例" } });
    perspective.value = f.perspective; perspective.addEventListener("input", function () { f.perspective = perspective.value; });
    const priority = el("select", { class: "select" });
    ["P0", "P1", "P2"].forEach(function (p) { priority.appendChild(el("option", { attrs: { value: p }, text: p })); });
    priority.value = f.priority; priority.addEventListener("change", function () { f.priority = priority.value; });
    const deadline = el("input", { class: "input", attrs: { type: "date" } });
    deadline.value = f.deadline; deadline.addEventListener("change", function () { f.deadline = deadline.value; });
    const acceptance = el("textarea", { class: "textarea", attrs: { rows: "3", placeholder: "验收标准，一行一条（DoR 至少 1 条）" } });
    acceptance.value = f.acceptance; acceptance.addEventListener("input", function () { f.acceptance = acceptance.value; });
    const risk = el("input", { attrs: { type: "checkbox" } });
    risk.checked = f.high_risk; risk.addEventListener("change", function () { f.high_risk = risk.checked; });

    return el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title", text: "新建任务（DoR 校验在核心）" }),
      el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
        field("标题", title), field("部门", deptSel), field("Owner", ownerSel),
        field("分工视角", perspective), field("优先级", priority), field("截止", deadline)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "验收标准" }), acceptance),
      el("label", { class: "row", attrs: { style: "margin-top:10px;gap:6px" } }, risk, el("span", { text: "高风险（需要 G2/G3）" })),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "create" }, on: { click: createTask } },
          s.busy === "create" ? "创建中…" : "创建任务"),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.createOpen = false; render(); } } }, "收起")));
  }

  async function createTask() {
    if (s.busy) return;
    const f = s.form;
    if (!f.title.trim()) { toast("请填写任务标题", "warn"); return; }
    if (!f.department_id) { toast("请选择部门", "warn"); return; }
    if (!f.acceptance.trim()) { toast("DoR 要求至少一条验收标准", "warn"); return; }
    s.busy = "create"; s.actionError = null; render();
    try {
      const createdView = await ctx.api.createTask({
        title: f.title.trim(), department_id: f.department_id, owner_id: f.owner_id,
        perspective: f.perspective, priority: f.priority,
        deadline: f.deadline ? f.deadline + "T23:59:59Z" : "",
        acceptance: f.acceptance.split("\n").map(function (x) { return x.trim(); }).filter(Boolean),
        high_risk: f.high_risk
      });
      const created = (createdView && createdView.task) ? createdView.task : createdView;
      s.busy = ""; s.createOpen = false; s.form = newForm();
      toast("任务已创建：" + created.id, "ok");
      go(created.id);
    } catch (e) {
      fail(e, "创建任务需要部门与验收标准（DoR）；400/403 会原样显示。");
    }
  }

  // ---------- 详情 ----------
  function renderDetail() {
    const back = el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { go(""); } } }, "← 返回任务板");
    if (s.detailLoading && !s.detail) {
      host.appendChild(el("div", { class: "row", attrs: { style: "margin-bottom:12px" } }, back));
      host.appendChild(el("div", { class: "empty" }, el("span", { class: "spinner" }), el("p", { text: "正在加载任务详情…" })));
      return;
    }
    if (s.detailError) {
      host.appendChild(el("div", { class: "row", attrs: { style: "margin-bottom:12px" } }, back));
      host.appendChild(errorBlock("任务详情加载失败", s.detailError, function () { loadDetail(detailID); }));
      return;
    }
    if (!s.detail) {
      host.appendChild(el("div", { class: "row", attrs: { style: "margin-bottom:12px" } }, back));
      host.appendChild(emptyState({ icon: "task", title: "任务不存在", desc: "可能已删除，或链接里的 id 不对。" }));
      return;
    }

    const task = s.detail.task || {};
    const criteria = s.detail.criteria || [];
    const deps = s.detail.dependencies || [];
    const events = s.detail.events || [];
    const dept = state.departments.filter(function (d) { return d.id === task.department_id; })[0];

    host.appendChild(el("div", { class: "row", attrs: { style: "margin-bottom:12px" } }, back,
      el("span", { class: "grow" }),
      el("span", { class: "mono", attrs: { style: "font-family:var(--mono);font-size:11px;color:var(--faint)" }, text: task.id })));

    host.appendChild(el("div", { class: "card" },
      el("div", { class: "card-title" }, el("span", { text: task.title || "—" }), statusBadge(task.status, TASK_STATUS),
        badge(task.priority || "P2", task.priority === "P0" ? "bad" : task.priority === "P1" ? "warn" : "")),
      el("div", { class: "card-meta", attrs: { style: "margin-top:6px" } },
        el("span", { text: "部门：" + (dept ? dept.name : nameOfDept(task.department_id)) }),
        el("span", { text: " · 视角：" + (task.perspective || "—") }),
        el("span", { text: " · owner：" + nameOfAgent(task.owner_id) }),
        el("span", { text: " · reviewer：" + nameOfAgent(task.reviewer_id) }),
        el("span", { text: " · approver：" + nameOfAgent(task.approver_id) })),
      el("div", { class: "card-meta", attrs: { style: "margin-top:2px" } },
        el("span", { text: "截止：" + (task.deadline ? fmtDateTime(task.deadline) : "—") }),
        el("span", { text: " · 预算：" + (task.budget_cost ? "¥" + task.budget_cost : (task.budget_tokens || 0) + " tokens") }),
        el("span", { text: " · 创建：" + relTime(task.created_at) })),
      task.description ? el("div", { class: "card-body" }, renderMarkdown(task.description)) : null,
      transitionRow(task, criteria)));

    if (s.actionError) host.appendChild(errCard());

    const allEvidence = criteria.length > 0 && criteria.every(function (c) { return c.evidence && c.evidence.trim(); });
    const critCard = el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title" }, el("span", { text: "验收标准（" + criteria.length + "）" }),
        allEvidence ? badge("证据齐全", "ok") : badge("缺证据", "warn")));
    if (!criteria.length) {
      critCard.appendChild(el("div", { class: "card-meta", text: "没有验收标准：G3 会被核心拒绝。" }));
    } else {
      const table = el("table", { class: "table", attrs: { style: "margin-top:8px" } },
        el("thead", null, el("tr", null, ["#", "标准", "证据", "主管结论", "操作"].map(function (h) { return el("th", { text: h }); }))));
      const tbody = el("tbody");
      criteria.forEach(function (c, idx) {
        const input = el("input", { class: "input", attrs: { type: "text", placeholder: "证据：链接/命令/摘要", style: "min-width:180px" } });
        input.value = c.evidence || "";
        const v = VERDICT[c.supervisor_verdict] || [c.supervisor_verdict || "未判定", ""];
        tbody.appendChild(el("tr", null,
          el("td", { text: String(idx + 1) }),
          el("td", { text: c.text || "" }),
          el("td", null, input,
            el("button", { class: "btn btn-sm", attrs: { type: "button", style: "margin-top:4px", disabled: ctx.demo }, on: { click: function () { saveEvidence(c, input.value); } } }, "保存证据")),
          el("td", null, badge(v[0], v[1])),
          el("td", null, el("div", { class: "row", attrs: { style: "gap:4px" } },
            el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: ctx.demo }, on: { click: function () { setVerdict(c, "approved"); } } }, "通过"),
            el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: ctx.demo }, on: { click: function () { setVerdict(c, "changes_requested"); } } }, "需返工"),
            el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: ctx.demo }, on: { click: function () { setVerdict(c, "human_needed"); } } }, "需人类")))));
      });
      table.appendChild(tbody);
      critCard.appendChild(table);
    }
    host.appendChild(critCard);

    if (deps.length) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:12px" } },
        el("div", { class: "card-title", text: "依赖（" + deps.length + "）" }),
        el("div", { class: "row", attrs: { style: "margin-top:6px;gap:6px" } }, deps.map(function (d) { return badge(String(d), "sky"); }))));
    }

    host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title" }, el("span", { text: "关联产出物" }), el("span", { class: "grow" }),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { ctx.navigate("#/artifacts?task_id=" + encodeURIComponent(task.id)); } } }, "在产出页查看")),
      s.artifacts.length
        ? el("div", { class: "stack", attrs: { style: "margin-top:8px" } }, s.artifacts.map(function (a) {
          return el("div", { class: "row", attrs: { style: "gap:8px" } },
            badge(a.type || "artifact", ""), el("span", { text: a.title }),
            el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { ctx.navigate("#/artifacts?id=" + encodeURIComponent(a.id)); } } }, "打开"));
        }))
        : el("div", { class: "card-meta", attrs: { style: "margin-top:6px" }, text: "暂无产出物" })));

    host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title", text: "任务时间线（append-only hash chain）" }),
      events.length
        ? el("div", { class: "stack", attrs: { style: "margin-top:8px" } }, events.map(function (ev) {
          return el("div", { attrs: { style: "border-left:2px solid var(--line-2);padding-left:10px" } },
            el("div", { class: "row", attrs: { style: "gap:8px" } },
              badge(ev.type || "event", "sky"),
              (ev.from_status || ev.to_status) ? el("span", { class: "card-meta", text: (ev.from_status || "?") + " → " + (ev.to_status || "?") }) : null,
              el("span", { class: "card-meta", attrs: { title: fmtDateTime(ev.created_at) }, text: relTime(ev.created_at) })),
            ev.reason ? el("div", { class: "card-meta", text: "原因：" + ev.reason }) : null,
            ev.evidence ? el("div", { class: "card-meta", text: "证据：" + truncate(ev.evidence, 120) }) : null,
            el("div", { style: "font-family:var(--mono);font-size:10.5px;color:var(--faint)" },
              "hash " + truncate(ev.hash || "", 18) + " ← prev " + truncate(ev.prev_hash || "", 14)));
        }))
        : el("div", { class: "card-meta", attrs: { style: "margin-top:6px" }, text: "暂无事件" })));
  }

  function transitionRow(task, criteria) {
    const row = el("div", { class: "row", attrs: { style: "margin-top:12px;padding-top:10px;border-top:1px solid var(--line)" } });
    const allowed = TRANSITIONS[task.status] || [];
    const reason = el("input", { class: "input", attrs: { type: "text", placeholder: "原因/证据（标记阻塞必填）", style: "max-width:260px" } });
    reason.value = s.reason || "";
    reason.addEventListener("input", function () { s.reason = reason.value; });
    if (!allowed.length) row.appendChild(el("span", { class: "card-meta", text: "终态：" + (TASK_STATUS[task.status] || [task.status])[0] }));
    allowed.forEach(function (to) {
      row.appendChild(el("button", {
        class: "btn btn-sm" + (to === "approved" || to === "done" ? " btn-primary" : (to === "canceled" || to === "failed" ? " btn-danger" : "")),
        attrs: { type: "button", disabled: ctx.demo || s.busy === "transition" },
        on: { click: function () { transition(task, to); } }
      }, "→ " + (TASK_STATUS[to] || [to])[0]));
    });
    if (allowed.length) row.appendChild(reason);

    const terminal = task.status === "done" || task.status === "canceled" || task.status === "failed";
    const flags = (function () { try { return JSON.parse(task.flags || "{}"); } catch (e) { return {}; } })();
    if (flags.g3_approval_id) {
      row.appendChild(el("span", { class: "grow" }));
      row.appendChild(badge("G3 已提交：" + flags.g3_approval_id, "accent"));
      return row;
    }
    const ready = criteria.length > 0 && criteria.every(function (c) {
      return c.evidence && c.evidence.trim() && c.supervisor_verdict !== "changes_requested";
    });
    row.appendChild(el("span", { class: "grow" }));
    if (terminal) return row;
    row.appendChild(el("button", {
      class: "btn btn-sm" + (ready ? " btn-primary" : ""),
      attrs: { type: "button", disabled: ctx.demo || !ready || s.busy === "g3",
        title: ready ? "提交 G3；人类批准后才能 done" : "证据未齐全或有返工结论，不能提交 G3" },
      on: { click: function () { submitG3(task); } }
    }, s.busy === "g3" ? "提交中…" : (ready ? "提交 G3 验收" : "G3 未就绪")));
    return row;
  }

  // ---------- 动作 ----------
  // 状态机权限：approved 只能由 reviewer/approver/主管做，changes_requested 只能 reviewer/主管。
  // 默认 actor 是助理（代表用户），这两类动作显式以任务上的 reviewer/approver 身份发起；核心仍会校验。
  function actorForTransition(task, to) {
    if (to === "approved") return task.approver_id || task.reviewer_id || "";
    if (to === "changes_requested") return task.reviewer_id || "";
    return "";
  }

  async function transition(task, to) {
    if (s.busy) return;
    if (to === "blocked" && !String(s.reason || "").trim()) { toast("标记阻塞必须写原因", "warn"); return; }
    if (to === "canceled" || to === "failed") {
      const ok = await confirmModal({ title: "确认转换", body: "改为「" + (TASK_STATUS[to] || [to])[0] + "」，会写入时间线与审计。", confirmText: "确认", danger: true });
      if (!ok) return;
    }
    s.busy = "transition"; s.actionError = null; render();
    try {
      await ctx.api.transitionTask(task.id, {
        to: to, reason: s.reason || "", evidence: "",
        actor_agent_id: actorForTransition(task, to)
      });
      s.busy = "";
      toast("已转换：" + (TASK_STATUS[to] || [to])[0], "ok");
      loadDetail(task.id);
    } catch (e) {
      fail(e, "转换由核心状态机/权限校验：409 状态不允许、403 角色不符、400 缺原因。");
    }
  }

  async function saveEvidence(c, evidence) {
    if (s.busy) return;
    s.busy = "ev:" + c.id; s.actionError = null; render();
    try {
      await ctx.api.criterionEvidence(detailID, c.id, { evidence: String(evidence || "").trim() });
      s.busy = ""; toast("证据已保存", "ok"); loadDetail(detailID);
    } catch (e) {
      fail(e, "证据由 owner/主管/助理填写；权限不足返回 403。");
    }
  }

  async function setVerdict(c, verdict) {
    if (s.busy) return;
    s.busy = "vd:" + c.id; s.actionError = null; render();
    try {
      await ctx.api.criterionVerdict(detailID, c.id, { verdict: verdict });
      s.busy = ""; toast("已记录结论：" + (VERDICT[verdict] || [verdict])[0], "ok"); loadDetail(detailID);
    } catch (e) {
      fail(e, "只有部门主管/助理可以填验收结论。");
    }
  }

  async function submitG3(task) {
    if (s.busy) return;
    const ok = await confirmModal({
      title: "提交 G3 验收",
      body: "G3 会创建一条人类审批；批准前任务不能标记 done。核对表必须齐全（证据 + 主管结论）。",
      confirmText: "提交"
    });
    if (!ok) return;
    s.busy = "g3"; s.actionError = null; render();
    try {
      const res = await ctx.api.submitG3(task.id, { risk: "low", residual_risks: "" });
      s.busy = "";
      toast("G3 已提交：等待人类在审批中心处理（" + ((res && res.approval && res.approval.id) || "") + "）", "ok", 5200);
      loadDetail(task.id);
    } catch (e) {
      fail(e, "G3 未就绪：每条标准都要有证据与主管结论（human_needed 需人类判断）。");
    }
  }

  if (detailID) loadDetail(detailID); else loadList();
  return { destroy: function () { disposed = true; } };
}
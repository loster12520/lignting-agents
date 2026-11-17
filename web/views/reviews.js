// 评审与决策：挑战单 / 裁决 / 返工 / 异议 / 决策记录（真实 R4 API）。
import { el, clear, icon, badge, notice, emptyState, toast, relTime, truncate } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { nameOfDept, nameOfAgent } from "../lib/ui.js";

const SEVERITY = { blocker: ["blocker", "bad"], major: ["major", "warn"], minor: ["minor", ""], question: ["question", "sky"] };
const RVERDICT = {
  approve: ["通过", "ok"], approve_with_comments: ["有条件通过", "accent"],
  request_changes: ["要求返工", "warn"], escalate: ["升级", "bad"]
};
const RSTATUS = { open: ["开放", "warn"], approved: ["已通过", "ok"], changes_requested: ["待返工", ""], escalated: ["已升级", "bad"] };

export function mount(ctx, route) {
  const q = (route && route.query) || {};
  const s = {
    tab: q.tab === "decisions" ? "decisions" : "reviews",
    items: [], loading: true, error: null, status: "",
    id: q.id || "", artifactId: q.artifact_id || "", selected: null, detailLoading: false, detailError: null,
    verdict: "approve", rationale: "", checked: "", conditions: "", residualRisk: "", reworkContent: "",
    createOpen: false, form: { artifact_id: q.artifact_id || "", severity: "major", claim: "", evidence: "", impact: "", proposed_resolution: "", from_department: "", to_department: "" },
    decisions: [], dLoading: false, dError: null, dForm: { title: "", context: "", options: "", decision: "", rationale: "", dissent: "" }, dCreateOpen: false,
    actionError: null, actionHint: "", busy: ""
  };
  let disposed = false;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "评审与决策"),
    el("div", { class: "topbar-sub" },
      el("span", { text: "挑战单 / 裁决 / 返工 / 异议保留；作者不能自批" }),
      ctx.demo ? badge("演示数据", "warn") : null)));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { if (s.tab === "decisions") loadDecisions(); else if (s.id) loadDetail(s.id); else loadList(); } } },
      icon("retry", 14), el("span", { text: "刷新" }))));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  function fail(e, hint) { s.actionError = e; s.actionHint = hint || ""; s.busy = ""; render(); }
  function render() { clear(host); if (s.tab === "decisions") renderDecisions(); else (s.id ? renderDetail() : renderList()); }

  async function loadList() {
    s.loading = true; s.error = null; render();
    try {
      const params = {};
      if (s.status) params.status = s.status;
      if (s.artifactId) params.artifact_id = s.artifactId;
      const res = await ctx.api.reviews(params);
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
      const res = await ctx.api.reviews(s.artifactId ? { artifact_id: s.artifactId } : {});
      if (disposed) return;
      const all = (res && res.items) || [];
      s.selected = all.filter(function (r) { return r.id === id; })[0] || null;
      s.items = all; s.detailLoading = false; render();
      if (!s.selected) { s.detailError = { message: "评审不存在或列表未包含该 id", code: "not_found" }; render(); }
    } catch (e) {
      if (disposed) return;
      s.detailLoading = false; s.detailError = e; render();
    }
  }

  async function loadDecisions() {
    s.dLoading = true; s.dError = null; render();
    try {
      const res = await ctx.api.decisions({});
      if (disposed) return;
      s.decisions = (res && res.items) || []; s.dLoading = false; render();
    } catch (e) {
      if (disposed) return;
      s.dLoading = false; s.dError = e; render();
    }
  }

  function errCard() {
    return el("div", { class: "card", attrs: { style: "margin-top:12px;border-color:rgba(251,113,133,.4)" } },
      el("div", { class: "card-title" }, el("span", { text: "操作失败" }), badge(s.actionError.code || "error", "bad")),
      el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.actionError.message || String(s.actionError) }),
      s.actionHint ? el("div", { class: "help", attrs: { style: "margin-top:6px" }, text: s.actionHint }) : null,
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.actionError = null; render(); } } }, "关闭")));
  }

  function tabs() {
    return el("div", { class: "tabs" },
      el("button", { class: "tab" + (s.tab === "reviews" ? " active" : ""), attrs: { type: "button" },
        on: { click: function () { s.tab = "reviews"; s.id = ""; ctx.navigate("#/reviews" + (s.artifactId ? "?artifact_id=" + encodeURIComponent(s.artifactId) : "")); loadList(); } } }, "评审"),
      el("button", { class: "tab" + (s.tab === "decisions" ? " active" : ""), attrs: { type: "button" },
        on: { click: function () { s.tab = "decisions"; s.id = ""; ctx.navigate("#/reviews?tab=decisions"); loadDecisions(); } } }, "决策记录"));
  }

  // ---------- 评审列表 ----------
  function renderList() {
    host.appendChild(el("div", { class: "page-head" },
      el("div", null, el("h2", { text: "评审" }), el("p", { text: "挑战单要有证据；裁决必须非作者；异议保留。" })),
      el("span", { class: "grow" }),
      el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: ctx.demo }, on: { click: function () { s.createOpen = !s.createOpen; render(); } } },
        icon("plus", 14), el("span", { text: "提交挑战单" }))));
    host.appendChild(tabs());
    if (ctx.demo) host.appendChild(notice("演示模式：评审需要真实后端（R4 /api/reviews）。", "warn"));
    if (s.createOpen) host.appendChild(createForm());
    if (s.actionError) host.appendChild(errCard());

    const filters = el("div", { class: "filters" });
    const st = el("select", { class: "select", attrs: { "aria-label": "按状态过滤" } });
    st.appendChild(el("option", { attrs: { value: "" }, text: "全部状态" }));
    ["open", "approved", "changes_requested", "escalated"].forEach(function (k) { st.appendChild(el("option", { attrs: { value: k }, text: (RSTATUS[k] || [k])[0] })); });
    st.value = s.status;
    st.addEventListener("change", function () { s.status = st.value; loadList(); });
    filters.appendChild(st);
    if (s.artifactId) filters.appendChild(el("span", { class: "search-stat", text: "按产出物过滤：" + s.artifactId }));
    host.appendChild(filters);

    if (s.loading) { host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载评审…" }))); return; }
    if (s.error) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:14px" } },
        el("div", { class: "card-title", text: "评审加载失败" }),
        el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.error.message || String(s.error) }),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } }, el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: loadList } }, "重试"))));
      return;
    }
    if (!s.items.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "shield", title: "还没有评审挑战",
        desc: "测试/平台部门对研发产出提挑战单（severity/claim/evidence/impact），研发返工后重新提交。",
        later: "裁决与返工在同一详情页"
      })));
      return;
    }
    const table = el("table", { class: "table", attrs: { style: "margin-top:14px" } },
      el("thead", null, el("tr", null, ["挑战", "级别", "方向", "状态", "创建时间"].map(function (h) { return el("th", { text: h }); }))));
    const tbody = el("tbody");
    s.items.forEach(function (r) {
      tbody.appendChild(el("tr", { attrs: { style: "cursor:pointer" }, on: { click: function () { s.id = r.id; ctx.navigate("#/reviews?id=" + encodeURIComponent(r.id)); loadDetail(r.id); } } },
        el("td", null, el("div", { attrs: { style: "font-weight:600" }, text: truncate(r.claim || "(无 claim)", 70) }),
          el("div", { style: "font-family:var(--mono);font-size:10.5px;color:var(--faint)", text: r.id })),
        el("td", null, badge((SEVERITY[r.severity] || [r.severity || "—"])[0], (SEVERITY[r.severity] || ["", ""])[1])),
        el("td", { text: nameOfDept(r.from_department) + " → " + nameOfDept(r.to_department) }),
        el("td", null, badge((RSTATUS[r.status] || [r.status || "—"])[0], (RSTATUS[r.status] || ["", ""])[1])),
        el("td", { attrs: { title: r.created_at || "" }, text: relTime(r.created_at) })));
    });
    table.appendChild(tbody);
    host.appendChild(table);
  }

  function createForm() {
    const f = s.form;
    const field = function (label, node) { return el("div", { class: "field" }, el("label", { text: label }), node); };
    const art = el("input", { class: "input", attrs: { type: "text", placeholder: "产出物 id（art_...）" } });
    art.value = f.artifact_id; art.addEventListener("input", function () { f.artifact_id = art.value; });
    const sev = el("select", { class: "select" });
    ["blocker", "major", "minor", "question"].forEach(function (k) { sev.appendChild(el("option", { attrs: { value: k }, text: k })); });
    sev.value = f.severity; sev.addEventListener("change", function () { f.severity = sev.value; });
    const claim = el("input", { class: "input", attrs: { type: "text", placeholder: "挑战主张（一句话）" } });
    claim.value = f.claim; claim.addEventListener("input", function () { f.claim = claim.value; });
    const evidence = el("textarea", { class: "textarea", attrs: { rows: "2", placeholder: "证据：文件/命令/测试输出" } });
    evidence.value = f.evidence; evidence.addEventListener("input", function () { f.evidence = evidence.value; });
    const impact = el("input", { class: "input", attrs: { type: "text", placeholder: "影响" } });
    impact.value = f.impact; impact.addEventListener("input", function () { f.impact = impact.value; });
    const depts = el("div", { class: "grid-2" });
    const fromSel = el("select", { class: "select" });
    const toSel = el("select", { class: "select" });
    fromSel.appendChild(el("option", { attrs: { value: "" }, text: "提出部门" }));
    toSel.appendChild(el("option", { attrs: { value: "" }, text: "被挑战部门" }));
    state.departments.forEach(function (d) {
      fromSel.appendChild(el("option", { attrs: { value: d.id }, text: d.name }));
      toSel.appendChild(el("option", { attrs: { value: d.id }, text: d.name }));
    });
    fromSel.addEventListener("change", function () { f.from_department = fromSel.value; });
    toSel.addEventListener("change", function () { f.to_department = toSel.value; });
    depts.appendChild(field("提出部门", fromSel));
    depts.appendChild(field("被挑战部门", toSel));

    return el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title", text: "提交挑战单" }),
      el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } }, field("产出物", art), field("级别", sev)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "挑战主张" }), claim),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "证据" }), evidence),
      el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } }, field("影响", impact), depts),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "review" }, on: { click: createReview } },
          s.busy === "review" ? "提交中…" : "提交挑战"),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.createOpen = false; render(); } } }, "收起")));
  }

  async function createReview() {
    if (s.busy) return;
    const f = s.form;
    if (!f.artifact_id.trim()) { toast("需要产出物 id", "warn"); return; }
    if (!f.claim.trim()) { toast("需要挑战主张", "warn"); return; }
    s.busy = "review"; s.actionError = null; render();
    try {
      const r = await ctx.api.createReview({
        artifact_id: f.artifact_id.trim(), severity: f.severity, claim: f.claim.trim(),
        evidence: f.evidence, impact: f.impact, from_department: f.from_department, to_department: f.to_department
      });
      s.busy = ""; s.createOpen = false;
      toast("挑战单已提交：" + (r && r.id ? r.id : ""), "ok");
      if (r && r.id) { s.id = r.id; ctx.navigate("#/reviews?id=" + encodeURIComponent(r.id)); loadDetail(r.id); } else loadList();
    } catch (e) {
      fail(e, "挑战单需要产出物与证据；权限/参数错误会原样显示。");
    }
  }

  // ---------- 评审详情 ----------
  function renderDetail() {
    const back = el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.id = ""; ctx.navigate("#/reviews"); loadList(); } } }, "← 返回评审");
    host.appendChild(el("div", { class: "row", attrs: { style: "margin-bottom:12px" } }, back));
    if (s.detailLoading) { host.appendChild(el("div", { class: "empty" }, el("span", { class: "spinner" }), el("p", { text: "正在加载评审…" }))); return; }
    if (s.detailError && !s.selected) {
      host.appendChild(el("div", { class: "card" },
        el("div", { class: "card-title", text: "评审加载失败" }),
        el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.detailError.message || String(s.detailError) })));
      return;
    }
    const r = s.selected || {};
    host.appendChild(el("div", { class: "card" },
      el("div", { class: "card-title" }, el("span", { text: truncate(r.claim || "(无 claim)", 90) }),
        badge((SEVERITY[r.severity] || [r.severity || "—"])[0], (SEVERITY[r.severity] || ["", ""])[1]),
        badge((RSTATUS[r.status] || [r.status || "—"])[0], (RSTATUS[r.status] || ["", ""])[1])),
      el("div", { class: "card-meta", attrs: { style: "margin-top:6px" } },
        el("span", { text: "方向：" + nameOfDept(r.from_department) + " → " + nameOfDept(r.to_department) }),
        el("span", { text: " · 产出物：" + (r.artifact_id || "—") }),
        el("span", { text: " · 提出：" + relTime(r.created_at) })),
      r.evidence ? el("div", { class: "card-body", text: "证据：" + r.evidence }) : null,
      r.impact ? el("div", { class: "card-meta", text: "影响：" + r.impact }) : null,
      r.proposed_resolution ? el("div", { class: "card-meta", text: "建议：" + r.proposed_resolution }) : null,
      el("div", { class: "row", attrs: { style: "margin-top:8px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { ctx.navigate("#/artifacts?id=" + encodeURIComponent(r.artifact_id || "")); } } }, "查看产出物"))));

    if (s.actionError) host.appendChild(errCard());

    // 裁决
    const vSel = el("select", { class: "select" });
    Object.keys(RVERDICT).forEach(function (k) { vSel.appendChild(el("option", { attrs: { value: k }, text: RVERDICT[k][0] })); });
    vSel.value = s.verdict;
    vSel.addEventListener("change", function () { s.verdict = vSel.value; });
    const rationale = el("textarea", { class: "textarea", attrs: { rows: "2", placeholder: "裁决理由（必填）" } });
    rationale.value = s.rationale; rationale.addEventListener("input", function () { s.rationale = rationale.value; });
    const checked = el("input", { class: "input", attrs: { type: "text", placeholder: "核对了什么" } });
    checked.value = s.checked; checked.addEventListener("input", function () { s.checked = checked.value; });
    const residual = el("input", { class: "input", attrs: { type: "text", placeholder: "残余风险（可选）" } });
    residual.value = s.residualRisk; residual.addEventListener("input", function () { s.residualRisk = residual.value; });

    host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title", text: "提交裁决（作者不能自批）" }),
      el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
        el("div", { class: "field" }, el("label", { text: "裁决" }), vSel),
        el("div", { class: "field" }, el("label", { text: "核对了什么" }), checked)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "理由" }), rationale),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "残余风险" }), residual),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: ctx.demo || s.busy === "verdict" }, on: { click: submitVerdict } },
          s.busy === "verdict" ? "提交中…" : "提交裁决"))));

    // 返工
    const rework = el("textarea", { class: "textarea", attrs: { rows: "4", placeholder: "返工内容（会生成新的产出物版本）" } });
    rework.value = s.reworkContent; rework.addEventListener("input", function () { s.reworkContent = rework.value; });
    host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title", text: "返工并提交新版本" }),
      el("div", { class: "field", attrs: { style: "margin-top:8px" } }, rework),
      el("div", { class: "row", attrs: { style: "margin-top:8px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: ctx.demo || s.busy === "rework" }, on: { click: doRework } },
          s.busy === "rework" ? "提交中…" : "提交返工"))));
  }

  async function submitVerdict() {
    if (s.busy) return;
    if (!String(s.rationale || "").trim()) { toast("裁决理由必填", "warn"); return; }
    s.busy = "verdict"; s.actionError = null; render();
    try {
      await ctx.api.submitVerdict(s.selected.id, {
        verdict: s.verdict, checked: s.checked, rationale: s.rationale,
        conditions: s.conditions, residual_risk: s.residualRisk
      });
      s.busy = ""; toast("裁决已提交：" + (RVERDICT[s.verdict] || [s.verdict])[0], "ok");
      loadDetail(s.selected.id);
    } catch (e) {
      fail(e, "裁决要求非作者；403/400 会原样显示。");
    }
  }

  async function doRework() {
    if (s.busy) return;
    if (!String(s.reworkContent || "").trim()) { toast("返工内容不能为空", "warn"); return; }
    s.busy = "rework"; s.actionError = null; render();
    try {
      await ctx.api.rework(s.selected.id, { content: s.reworkContent });
      s.busy = ""; s.reworkContent = ""; toast("返工已提交为新版本", "ok");
      loadDetail(s.selected.id);
    } catch (e) {
      fail(e, "返工会生成新的产出物版本；权限/参数错误会原样显示。");
    }
  }

  // ---------- 决策记录 ----------
  function renderDecisions() {
    host.appendChild(el("div", { class: "page-head" },
      el("div", null, el("h2", { text: "决策记录" }), el("p", { text: "不可变、保留异议、可 supersede；决策人可以是人类。" })),
      el("span", { class: "grow" }),
      el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: ctx.demo }, on: { click: function () { s.dCreateOpen = !s.dCreateOpen; render(); } } },
        icon("plus", 14), el("span", { text: "记录决策" }))));
    host.appendChild(tabs());
    if (ctx.demo) host.appendChild(notice("演示模式：决策记录需要真实后端（R4 /api/decisions）。", "warn"));
    if (s.dCreateOpen) host.appendChild(decisionForm());
    if (s.actionError) host.appendChild(errCard());
    if (s.dLoading) { host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载决策…" }))); return; }
    if (s.dError) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:14px" } },
        el("div", { class: "card-title", text: "决策加载失败" }),
        el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.dError.message || String(s.dError) }),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } }, el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: loadDecisions } }, "重试"))));
      return;
    }
    if (!s.decisions.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "audit", title: "还没有决策记录",
        desc: "跨部门辩论结束或 G3 批准后会写决策；也可以手动记录（背景/选项/决定/理由/异议）。",
        later: "supersede 链在详情里"
      })));
      return;
    }
    const stack = el("div", { class: "stack", attrs: { style: "margin-top:14px" } });
    s.decisions.forEach(function (d) {
      stack.appendChild(el("div", { class: "card" },
        el("div", { class: "card-title" }, el("span", { text: d.title || d.id }), badge(d.status || "", d.status === "accepted" ? "ok" : "")),
        el("div", { class: "card-meta", text: "决策人：" + (d.deciders || "—") + (d.dissent ? " · 异议：" + d.dissent : "") }),
        el("div", { class: "card-body", text: truncate(d.decision || "", 200) }),
        d.rationale ? el("div", { class: "card-meta", text: "理由：" + truncate(d.rationale, 160) }) : null));
    });
    host.appendChild(stack);
  }

  function decisionForm() {
    const f = s.dForm;
    const mk = function (key, label, rows) {
      const input = rows ? el("textarea", { class: "textarea", attrs: { rows: String(rows), placeholder: label } })
        : el("input", { class: "input", attrs: { type: "text", placeholder: label } });
      input.value = f[key] || "";
      input.addEventListener("input", function () { f[key] = input.value; });
      return el("div", { class: "field" }, el("label", { text: label }), input);
    };
    return el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title", text: "记录决策" }),
      el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } }, mk("title", "标题"), mk("deciders", "决策人")),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, mk("context", "背景", 2)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, mk("options", "选项", 2)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, mk("decision", "决定", 2)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, mk("rationale", "理由", 2)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, mk("dissent", "异议（保留）", 2)),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "decision" }, on: { click: createDecision } },
          s.busy === "decision" ? "提交中…" : "记录"),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.dCreateOpen = false; render(); } } }, "收起")));
  }

  async function createDecision() {
    if (s.busy) return;
    const f = s.dForm;
    if (!f.title.trim()) { toast("需要决策标题", "warn"); return; }
    s.busy = "decision"; s.actionError = null; render();
    try {
      await ctx.api.createDecision({
        title: f.title, context: f.context, options: f.options, decision: f.decision,
        rationale: f.rationale, dissent: f.dissent, deciders: f.deciders
      });
      s.busy = ""; s.dCreateOpen = false; toast("决策已记录", "ok");
      loadDecisions();
    } catch (e) {
      fail(e, "决策需要标题、背景、决定与理由；权限/参数错误会原样显示。");
    }
  }

  if (s.tab === "decisions") loadDecisions();
  else if (s.id) loadDetail(s.id);
  else loadList();
  return { destroy: function () { disposed = true; } };
}
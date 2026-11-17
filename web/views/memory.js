// 记忆检查器 / 手册 / 上下文快照（R7 真实 API）。
// 记忆：候选→审核→档案；编辑带 expected_version；删除写墓碑；可见性 human-only 等。
import { el, clear, icon, badge, notice, emptyState, toast, confirmModal, relTime, fmtDateTime, truncate } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { statusBadge, MEMORY_STATUS, MEMORY_SCOPE, nameOfAgent, nameOfDept } from "../lib/ui.js";

const SCOPES = [["", "全部范围"], ["assistant", "助理"], ["department", "部门"], ["employee", "员工"]];
const STATUSES = ["candidate", "active", "rejected", "deleted"];
const VISIBILITY = [["agent-private", "Agent 私有"], ["team", "团队"], ["org", "组织"], ["human-only", "仅人类"]];
const ENTITY_TYPES = [["preference", "偏好"], ["fact", "事实"], ["procedure", "流程"], ["decision", "决策依据"]];
const CONFIDENCE = [["", "默认"], ["low", "低"], ["medium", "中"], ["high", "高"]];

function tagsOf(m) {
  if (!m || !m.tags) return [];
  try { const v = JSON.parse(m.tags); return Array.isArray(v) ? v : []; } catch (e) { return String(m.tags).split(",").filter(Boolean); }
}
function fmtTags(tags) { return Array.isArray(tags) ? tags.join(", ") : String(tags || ""); }

export function mount(ctx) {
  const s = {
    tab: "memories",
    items: [], loading: true, error: null,
    scope: "", status: [], q: "", departmentId: "",
    busy: "", actionError: null, actionHint: "",
    editing: "", edit: { summary: "", content: "", tags: "", visibility: "", confidence: "" },
    createOpen: false,
    form: { scope: "department", department_id: "", agent_id: "", entity_type: "preference", entity_key: "", content: "", summary: "", tags: "", confidence: "medium", visibility: "team" },
    manuals: [], mLoading: false, mError: null, mScope: "", mDept: "", mAgent: "",
    mCreateOpen: false, mForm: { scope: "department", department_id: "", agent_id: "", content: "", sources: "" },
    snaps: [], sLoading: false, sError: null, turnId: "", convId: "",
    turn: null, turnError: null
  };
  let disposed = false;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "记忆"),
    el("div", { class: "topbar-sub" },
      el("span", { text: "四层记忆：短期 / 触发 / 档案 / 自适应提示词" }),
      ctx.demo ? badge("演示数据", "warn") : null)));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", {
      class: "btn btn-sm", attrs: { type: "button" },
      on: { click: function () { if (s.tab === "manuals") loadManuals(); else if (s.tab === "snapshots") loadSnaps(); else load(); } }
    }, icon("retry", 14), el("span", { text: "刷新" }))));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  function fail(e, hint) { s.actionError = e; s.actionHint = hint || ""; s.busy = ""; render(); }
  function render() { clear(host); if (s.tab === "manuals") renderManuals(); else if (s.tab === "snapshots") renderSnaps(); else renderMemories(); }

  // ---------- 数据 ----------
  async function load() {
    s.loading = true; s.error = null; render();
    try {
      const params = { limit: 200 };
      if (s.scope) params.scope = s.scope;
      if (s.status.length) params.status = s.status.join(",");
      if (s.departmentId) params.department_id = s.departmentId;
      const res = await ctx.api.memories(params);
      if (disposed) return;
      s.items = (res && res.items) || [];
      if (s.q) {
        const q = s.q.toLowerCase();
        s.items = s.items.filter(function (m) {
          return (m.content || "").toLowerCase().indexOf(q) >= 0 || (m.summary || "").toLowerCase().indexOf(q) >= 0 || (m.tags || "").toLowerCase().indexOf(q) >= 0;
        });
      }
      s.loading = false; render();
    } catch (e) {
      if (disposed) return;
      s.loading = false; s.error = e; render();
    }
  }

  async function loadManuals() {
    s.mLoading = true; s.mError = null; render();
    try {
      const params = {};
      if (s.mScope) params.scope = s.mScope;
      if (s.mDept) params.department_id = s.mDept;
      if (s.mAgent) params.agent_id = s.mAgent;
      const res = await ctx.api.manuals(params);
      if (disposed) return;
      s.manuals = (res && res.items) || []; s.mLoading = false; render();
    } catch (e) {
      if (disposed) return;
      s.mLoading = false; s.mError = e; render();
    }
  }

  async function loadSnaps() {
    s.sLoading = true; s.sError = null; s.turnError = null; render();
    try {
      const params = { limit: 100 };
      if (s.turnId) params.turn_id = s.turnId;
      if (s.convId) params.conversation_id = s.convId;
      const res = await ctx.api.contextSnapshots(params);
      if (disposed) return;
      s.snaps = (res && res.items) || [];
      if (s.turnId) {
        try { s.turn = await ctx.api.turn(s.turnId); } catch (e2) { s.turn = null; s.turnError = e2; }
      } else { s.turn = null; }
      s.sLoading = false; render();
    } catch (e) {
      if (disposed) return;
      s.sLoading = false; s.sError = e; render();
    }
  }

  // ---------- 公共片段 ----------
  function tabs() {
    const defs = [["memories", "记忆"], ["manuals", "手册"], ["snapshots", "上下文快照"]];
    const bar = el("div", { class: "tabs" });
    defs.forEach(function (d) {
      bar.appendChild(el("button", {
        class: "tab" + (s.tab === d[0] ? " active" : ""), attrs: { type: "button" },
        on: {
          click: function () {
            s.tab = d[0];
            if (d[0] === "memories") load(); else if (d[0] === "manuals") loadManuals(); else loadSnaps();
          }
        }
      }, d[1]));
    });
    return bar;
  }

  function errCard() {
    return el("div", { class: "card", attrs: { style: "margin-top:12px;border-color:rgba(251,113,133,.4)" } },
      el("div", { class: "card-title" }, el("span", { text: "操作失败" }), badge(s.actionError.code || "error", "bad")),
      el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.actionError.message || String(s.actionError) }),
      s.actionHint ? el("div", { class: "help", attrs: { style: "margin-top:6px" }, text: s.actionHint }) : null,
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.actionError = null; render(); } } }, "关闭")));
  }

  function errorBlock(title, err, retry) {
    return el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title", text: title }),
      el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: (err && err.message) || String(err) }),
      err && err.code ? el("div", { class: "card-meta", text: "错误码：" + err.code }) : null,
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: retry } }, "重试")));
  }

  function selectOf(list, value, onchange, aria) {
    const sel = el("select", { class: "select", attrs: { "aria-label": aria || "筛选" } });
    list.forEach(function (o) { sel.appendChild(el("option", { attrs: { value: o[0] }, text: o[1] })); });
    sel.value = value;
    sel.addEventListener("change", function () { onchange(sel.value); });
    return sel;
  }

  // ---------- 记忆 ----------
  function renderMemories() {
    const pending = s.items.filter(function (m) { return m.status === "candidate"; }).length;
    host.appendChild(el("div", { class: "page-head" },
      el("div", null, el("h2", { text: "记忆检查器" }),
        el("p", { text: "候选 → 审核 → 档案；删除写墓碑并停止注入；编辑带版本号防覆盖。" })),
      el("span", { class: "grow" }),
      pending ? badge(pending + " 条待审", "warn") : null,
      el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: ctx.demo }, on: { click: function () { s.createOpen = !s.createOpen; render(); } } },
        icon("plus", 14), el("span", { text: "直接写档案" }))));
    host.appendChild(tabs());
    if (ctx.demo) host.appendChild(notice("演示模式：记忆读写需要真实后端（R7 /api/memories）。", "warn"));
    if (s.createOpen) host.appendChild(createForm());
    if (s.actionError) host.appendChild(errCard());

    const filters = el("div", { class: "filters" });
    filters.appendChild(selectOf(SCOPES, s.scope, function (v) { s.scope = v; load(); }, "按范围过滤"));
    const statusWrap = el("div", { class: "row", attrs: { style: "gap:4px" } });
    STATUSES.forEach(function (st) {
      const on = s.status.indexOf(st) >= 0;
      statusWrap.appendChild(el("button", {
        class: "filter-chip", attrs: { type: "button", "aria-pressed": on ? "true" : "false" },
        on: {
          click: function () {
            s.status = on ? s.status.filter(function (x) { return x !== st; }) : s.status.concat([st]);
            load();
          }
        }
      }, (MEMORY_STATUS[st] || [st])[0]));
    });
    filters.appendChild(statusWrap);
    const deptSel = el("select", { class: "select", attrs: { "aria-label": "按部门过滤" } });
    deptSel.appendChild(el("option", { attrs: { value: "" }, text: "全部部门" }));
    state.departments.forEach(function (d) { deptSel.appendChild(el("option", { attrs: { value: d.id }, text: d.name })); });
    deptSel.value = s.departmentId;
    deptSel.addEventListener("change", function () { s.departmentId = deptSel.value; load(); });
    filters.appendChild(deptSel);
    const q = el("input", { class: "input", attrs: { type: "search", placeholder: "搜索内容/摘要/标签", style: "max-width:220px" } });
    q.value = s.q;
    q.addEventListener("input", function () { s.q = q.value; });
    q.addEventListener("keydown", function (e) { if (e.key === "Enter") load(); });
    filters.appendChild(q);
    host.appendChild(filters);

    if (s.loading) { host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载记忆…" }))); return; }
    if (s.error) { host.appendChild(errorBlock("记忆加载失败", s.error, load)); return; }
    if (!s.items.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "memory", title: "还没有记忆",
        desc: "Agent 在任务关闭、返工、裁决、人类拍板后提交候选记忆；人类审核后才进入档案。",
        later: "删除会写墓碑并停止注入"
      })));
      return;
    }

    const stack = el("div", { class: "stack", attrs: { style: "margin-top:14px" } });
    s.items.forEach(function (m) { stack.appendChild(memoryCard(m)); });
    host.appendChild(stack);
  }

  function memoryCard(m) {
    const st = MEMORY_STATUS[m.status] || [m.status, ""];
    const editing = s.editing === m.id;
    const card = el("div", { class: "card", dataset: { id: m.id } },
      el("div", { class: "card-title" },
        el("span", { text: m.summary || truncate(m.content || "", 60) || "(无内容)" }),
        statusBadge(m.status, MEMORY_STATUS),
        badge(MEMORY_SCOPE[m.scope] || m.scope || "—", ""),
        badge(m.visibility || "—", m.visibility === "human-only" ? "warn" : ""),
        badge("v" + (m.version || 1), ""),
        el("span", { class: "grow" }),
        el("span", { style: "font-family:var(--mono);font-size:10.5px;color:var(--faint)", text: m.id })),
      el("div", { class: "card-meta", attrs: { style: "margin-top:4px" } },
        el("span", { text: "部门：" + nameOfDept(m.department_id) }),
        el("span", { text: " · 创建：" + relTime(m.updated_at || m.created_at) }),
        el("span", { text: " · 置信：" + (m.confidence || "—") }),
        tagsOf(m).length ? el("span", { text: " · 标签：" + tagsOf(m).join(", ") }) : null),
      el("div", { class: "card-body", attrs: { style: "margin-top:6px" }, text: m.content || "" }));

    if (editing) {
      const f = s.edit;
      const summary = el("input", { class: "input", attrs: { type: "text", placeholder: "摘要" } }); summary.value = f.summary;
      summary.addEventListener("input", function () { f.summary = summary.value; });
      const content = el("textarea", { class: "textarea", attrs: { rows: "3", placeholder: "内容" } }); content.value = f.content;
      content.addEventListener("input", function () { f.content = content.value; });
      const tags = el("input", { class: "input", attrs: { type: "text", placeholder: "标签（逗号分隔）" } }); tags.value = f.tags;
      tags.addEventListener("input", function () { f.tags = tags.value; });
      const vis = selectOf(VISIBILITY, f.visibility, function (v) { f.visibility = v; }, "可见性");
      const conf = selectOf(CONFIDENCE, f.confidence, function (v) { f.confidence = v; }, "置信");
      card.appendChild(el("div", { class: "stack", attrs: { style: "margin-top:10px;border-top:1px dashed var(--line-2);padding-top:10px" } },
        el("div", { class: "grid-2" },
          el("div", { class: "field" }, el("label", { text: "摘要" }), summary),
          el("div", { class: "field" }, el("label", { text: "标签" }), tags),
          el("div", { class: "field" }, el("label", { text: "可见性" }), vis),
          el("div", { class: "field" }, el("label", { text: "置信" }), conf)),
        el("div", { class: "field" }, el("label", { text: "内容" }), content),
        el("div", { class: "row" },
          el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "save:" + m.id }, on: { click: function () { saveMemory(m); } } },
            s.busy === "save:" + m.id ? "保存中…" : "保存（v" + (m.version || 1) + "）"),
          el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.editing = ""; render(); } } }, "取消"),
          el("span", { class: "card-meta", text: "带 expected_version：并发编辑会 409" }))));
      return card;
    }

    const row = el("div", { class: "row", attrs: { style: "margin-top:10px" } });
    if (m.status === "candidate") {
      row.appendChild(el("button", { class: "btn btn-sm btn-primary", attrs: { type: "button", disabled: ctx.demo || s.busy === "approve:" + m.id }, on: { click: function () { approveMemory(m); } } }, "批准"));
      row.appendChild(el("button", { class: "btn btn-sm btn-danger", attrs: { type: "button", disabled: ctx.demo }, on: { click: function () { rejectMemory(m); } } }, "拒绝"));
    } else if (m.status === "active") {
      row.appendChild(el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: ctx.demo }, on: { click: function () { startEdit(m); } } }, "编辑"));
      row.appendChild(el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: ctx.demo }, on: { click: function () { deleteMemory(m); } } }, "删除（墓碑）"));
    } else {
      row.appendChild(el("span", { class: "card-meta", text: "状态：" + st[0] }));
    }
    card.appendChild(row);
    return card;
  }

  function startEdit(m) {
    s.editing = m.id;
    s.edit = {
      summary: m.summary || "", content: m.content || "",
      tags: tagsOf(m).join(", "), visibility: m.visibility || "team",
      confidence: m.confidence || ""
    };
    render();
  }

  function createForm() {
    const f = s.form;
    const field = function (label, node) { return el("div", { class: "field" }, el("label", { text: label }), node); };
    const scopeSel = selectOf([["department", "部门"], ["assistant", "助理"], ["employee", "员工"]], f.scope, function (v) { f.scope = v; }, "范围");
    const deptSel = el("select", { class: "select" });
    deptSel.appendChild(el("option", { attrs: { value: "" }, text: "部门（可选）" }));
    state.departments.forEach(function (d) { deptSel.appendChild(el("option", { attrs: { value: d.id }, text: d.name })); });
    deptSel.value = f.department_id;
    deptSel.addEventListener("change", function () { f.department_id = deptSel.value; });
    const content = el("textarea", { class: "textarea", attrs: { rows: "3", placeholder: "内容（事实/偏好/流程）" } }); content.value = f.content;
    content.addEventListener("input", function () { f.content = content.value; });
    const summary = el("input", { class: "input", attrs: { type: "text", placeholder: "摘要（短）" } }); summary.value = f.summary;
    summary.addEventListener("input", function () { f.summary = summary.value; });
    const tags = el("input", { class: "input", attrs: { type: "text", placeholder: "标签：逗号分隔" } }); tags.value = f.tags;
    tags.addEventListener("input", function () { f.tags = tags.value; });
    const vis = selectOf(VISIBILITY, f.visibility, function (v) { f.visibility = v; }, "可见性");
    const conf = selectOf(CONFIDENCE, f.confidence, function (v) { f.confidence = v; }, "置信");
    const type = selectOf(ENTITY_TYPES, f.entity_type, function (v) { f.entity_type = v; }, "类型");
    const entityKey = el("input", { class: "input", attrs: { type: "text", placeholder: "唯一实体键，如 login-ratelimit（核心必填）" } });
    entityKey.value = f.entity_key;
    entityKey.addEventListener("input", function () { f.entity_key = entityKey.value; });

    return el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title", text: "直接写档案（人类/助理权限；员工只能提候选）" }),
      el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
        field("范围", scopeSel), field("部门", deptSel), field("类型", type), field("可见性", vis),
        field("置信", conf), field("标签", tags), field("实体键", entityKey)),
      el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } }, field("摘要", summary)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "内容" }), content),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "create" }, on: { click: createMemory } },
          s.busy === "create" ? "写入中…" : "写入档案"),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.createOpen = false; render(); } } }, "收起")));
  }

  // ---------- 手册 ----------
  function renderManuals() {
    host.appendChild(el("div", { class: "page-head" },
      el("div", null, el("h2", { text: "自适应提示词手册" }),
        el("p", { text: "手册按 scope/部门/Agent 分版本；提案→新版本，可回滚到历史版本。" })),
      el("span", { class: "grow" }),
      el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: ctx.demo }, on: { click: function () { s.mCreateOpen = !s.mCreateOpen; render(); } } },
        icon("plus", 14), el("span", { text: "提案新版本" }))));
    host.appendChild(tabs());
    if (ctx.demo) host.appendChild(notice("演示模式：手册需要真实后端（R7 /api/manuals）。", "warn"));
    if (s.mCreateOpen) host.appendChild(manualForm());
    if (s.actionError) host.appendChild(errCard());

    const filters = el("div", { class: "filters" });
    filters.appendChild(selectOf([["", "全部范围"], ["assistant", "助理"], ["department", "部门"], ["employee", "员工"]], s.mScope, function (v) { s.mScope = v; loadManuals(); }, "范围"));
    const deptSel = el("select", { class: "select", attrs: { "aria-label": "部门" } });
    deptSel.appendChild(el("option", { attrs: { value: "" }, text: "全部部门" }));
    state.departments.forEach(function (d) { deptSel.appendChild(el("option", { attrs: { value: d.id }, text: d.name })); });
    deptSel.value = s.mDept;
    deptSel.addEventListener("change", function () { s.mDept = deptSel.value; loadManuals(); });
    filters.appendChild(deptSel);
    const agentSel = el("select", { class: "select", attrs: { "aria-label": "Agent" } });
    agentSel.appendChild(el("option", { attrs: { value: "" }, text: "全部 Agent" }));
    state.agents.forEach(function (a) { agentSel.appendChild(el("option", { attrs: { value: a.id }, text: a.name })); });
    agentSel.value = s.mAgent;
    agentSel.addEventListener("change", function () { s.mAgent = agentSel.value; loadManuals(); });
    filters.appendChild(agentSel);
    host.appendChild(filters);

    if (s.mLoading) { host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载手册…" }))); return; }
    if (s.mError) { host.appendChild(errorBlock("手册加载失败", s.mError, loadManuals)); return; }
    if (!s.manuals.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "memory", title: "还没有手册版本",
        desc: "自适应提示词随记忆与裁决演进；每次提案生成新版本，可回滚。",
        later: "回滚会写审计"
      })));
      return;
    }
    const stack = el("div", { class: "stack", attrs: { style: "margin-top:14px" } });
    s.manuals.forEach(function (m) {
      stack.appendChild(el("div", { class: "card" },
        el("div", { class: "card-title" },
          el("span", { text: "v" + (m.version || 1) }),
          badge(m.scope || "—", ""),
          badge(m.status || "—", m.status === "active" ? "ok" : ""),
          el("span", { class: "grow" }),
          el("span", { style: "font-family:var(--mono);font-size:10.5px;color:var(--faint)", text: m.id })),
        el("div", { class: "card-meta", attrs: { style: "margin-top:4px" } },
          el("span", { text: "部门：" + nameOfDept(m.department_id) }),
          el("span", { text: " · Agent：" + nameOfAgent(m.agent_id) }),
          el("span", { text: " · " + relTime(m.created_at) })),
        el("div", { class: "card-body", attrs: { style: "margin-top:6px;white-space:pre-wrap" }, text: m.content || "" }),
        el("div", { class: "row", attrs: { style: "margin-top:8px" } },
          el("button", {
            class: "btn btn-sm", attrs: { type: "button", disabled: ctx.demo || s.busy === "rollback:" + m.id, title: "回滚到上一版本" },
            on: { click: function () { rollbackManual(m); } }
          }, "回滚到此版本"))));
    });
    host.appendChild(stack);
  }

  function manualForm() {
    const f = s.mForm;
    const scopeSel = selectOf([["department", "部门"], ["assistant", "助理"], ["employee", "员工"]], f.scope, function (v) { f.scope = v; }, "范围");
    const deptSel = el("select", { class: "select" });
    deptSel.appendChild(el("option", { attrs: { value: "" }, text: "部门（可选）" }));
    state.departments.forEach(function (d) { deptSel.appendChild(el("option", { attrs: { value: d.id }, text: d.name })); });
    deptSel.value = f.department_id;
    deptSel.addEventListener("change", function () { f.department_id = deptSel.value; });
    const content = el("textarea", { class: "textarea", attrs: { rows: "5", placeholder: "新的自适应提示词（必须包含「角色」「目标」章节）" } });
    content.value = f.content;
    content.addEventListener("input", function () { f.content = content.value; });
    const NL = String.fromCharCode(10);
    const tpl = function () {
      f.content =
        "## 角色" + NL + "你是本项目的助理，负责澄清目标、提出蓝图、主持跨部门辩论。" + NL + NL +
        "## 目标" + NL + "先对齐目标，再推进任务。" + NL + NL +
        "## 红线" + NL + "不替人类拍板；不绕过审批；不改人类代码仓库。" + NL + NL +
        "## 升级" + NL + "遇到预算、不可逆操作、跨部门死锁时，整理成一张决策卡交给人类。";
      content.value = f.content;
    };
    const sources = el("input", { class: "input", attrs: { type: "text", placeholder: "来源 id（逗号分隔，可选）" } });
    sources.value = f.sources;
    sources.addEventListener("input", function () { f.sources = sources.value; });
    return el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title", text: "提案新手册版本" }),
      el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
        el("div", { class: "field" }, el("label", { text: "范围" }), scopeSel),
        el("div", { class: "field" }, el("label", { text: "部门" }), deptSel),
        el("div", { class: "field" }, el("label", { text: "来源" }), sources)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "内容（必须含「角色」「目标」「红线」「升级」四节）" }), content,
        el("div", { class: "row", attrs: { style: "margin-top:6px" } },
          el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: tpl } }, "填充模板"),
          el("span", { class: "card-meta", text: "核心校验：缺任一节会返回 400" }))),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "manual" }, on: { click: createManual } },
          s.busy === "manual" ? "提交中…" : "提案"),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.mCreateOpen = false; render(); } } }, "收起")));
  }

  // ---------- 快照 ----------
  function renderSnaps() {
    host.appendChild(el("div", { class: "page-head" },
      el("div", null, el("h2", { text: "上下文快照与 turn 回放" }),
        el("p", { text: "每次 Agent turn 的注入内容快照（用了哪些记忆/角色卡/任务）；可按 turn 或会话查。" }))));
    host.appendChild(tabs());
    if (ctx.demo) host.appendChild(notice("演示模式：快照需要真实后端（R7 /api/context-snapshots）。", "warn"));

    const filters = el("div", { class: "filters" });
    const turnIn = el("input", { class: "input", attrs: { type: "search", placeholder: "turn_id（精确）", style: "max-width:260px" } });
    turnIn.value = s.turnId;
    turnIn.addEventListener("change", function () { s.turnId = turnIn.value.trim(); loadSnaps(); });
    const convIn = el("input", { class: "input", attrs: { type: "search", placeholder: "conversation_id（可选）", style: "max-width:260px" } });
    convIn.value = s.convId;
    convIn.addEventListener("change", function () { s.convId = convIn.value.trim(); loadSnaps(); });
    filters.appendChild(turnIn); filters.appendChild(convIn);
    host.appendChild(filters);

    if (s.sLoading) { host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载快照…" }))); return; }
    if (s.sError) { host.appendChild(errorBlock("快照加载失败", s.sError, loadSnaps)); return; }

    if (s.turn) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:12px" } },
        el("div", { class: "card-title" }, el("span", { text: "turn " + (s.turn.turn ? s.turn.turn.id : s.turnId) }),
          s.turn.turn ? statusBadge(s.turn.turn.status || "", {}) : null),
        el("div", { class: "card-meta", attrs: { style: "margin-top:4px" } },
          el("span", { text: "agent：" + nameOfAgent(s.turn.turn ? s.turn.turn.agent_id : "") }),
          el("span", { text: " · started：" + (s.turn.turn ? s.turn.turn.started_at : "") }),
          el("span", { text: " · ended：" + (s.turn.turn ? s.turn.turn.ended_at || "—" : "—") })),
        s.turn.turn && s.turn.turn.error ? el("div", { class: "notice bad", attrs: { style: "margin-top:6px" }, text: s.turn.turn.error }) : null));
    }
    if (s.turnError) host.appendChild(notice("turn 详情加载失败：" + (s.turnError.message || ""), "warn"));

    if (!s.snaps.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "audit", title: "没有快照",
        desc: "Agent turn 完成后会写 context snapshot；输入 turn_id 可看单轮注入内容。",
        later: "「用了哪些记忆」为 R7 注入快照字段"
      })));
      return;
    }
    const stack = el("div", { class: "stack", attrs: { style: "margin-top:14px" } });
    s.snaps.forEach(function (sn) {
      let payload = sn.payload || "";
      try { payload = JSON.stringify(JSON.parse(payload), null, 2); } catch (e) { /* 保留原文 */ }
      stack.appendChild(el("div", { class: "card" },
        el("div", { class: "card-title" },
          el("span", { text: "snapshot " + (sn.id || "") }),
          badge("turn " + (sn.turn_id || "—"), ""),
          el("span", { class: "grow" }),
          el("span", { class: "card-meta", text: relTime(sn.created_at) })),
        el("div", { class: "card-meta", attrs: { style: "margin-top:2px" } },
          el("span", { text: "agent：" + nameOfAgent(sn.agent_id) }),
          el("span", { text: " · 会话：" + (sn.conversation_id || "—") })),
        el("pre", { attrs: { style: "margin-top:8px;max-height:260px;overflow:auto;background:#0a0f14;border:1px solid var(--line);border-radius:8px;padding:8px 10px;font-family:var(--mono);font-size:11.5px;line-height:1.5;color:var(--text-2)" }, text: truncate(payload, 4000) })));
    });
    host.appendChild(stack);
  }

  // ---------- 动作 ----------
  async function approveMemory(m) {
    if (s.busy) return;
    s.busy = "approve:" + m.id; s.actionError = null; render();
    try {
      await ctx.api.approveMemory(m.id);
      s.busy = ""; toast("候选记忆已批准", "ok"); load();
    } catch (e) { fail(e, "只有人类/助理/部门主管可以批准候选；敏感项会被拦。"); }
  }

  async function rejectMemory(m) {
    if (s.busy) return;
    const ok = await confirmModal({ title: "拒绝候选记忆", body: "拒绝需要理由（写审计）。当前骨架使用默认理由，R7 完整版提供输入框。", confirmText: "拒绝", danger: true });
    if (!ok) return;
    s.busy = "reject:" + m.id; s.actionError = null; render();
    try {
      await ctx.api.rejectMemory(m.id, { reason: "用户在记忆检查器拒绝（骨架默认理由）" });
      s.busy = ""; toast("已拒绝", ""); load();
    } catch (e) { fail(e, "拒绝必须带理由；403 表示权限不足。"); }
  }

  async function deleteMemory(m) {
    if (s.busy) return;
    const ok = await confirmModal({ title: "删除记忆（墓碑）", body: "删除后不再注入，并保留墓碑与审计。确认？", confirmText: "删除", danger: true });
    if (!ok) return;
    s.busy = "del:" + m.id; s.actionError = null; render();
    try {
      await ctx.api.deleteMemory(m.id, { reason: "用户在记忆检查器删除（骨架默认理由）" });
      s.busy = ""; toast("已删除（墓碑）", ""); load();
    } catch (e) { fail(e, "删除需要理由；删除后不再注入。"); }
  }

  async function saveMemory(m) {
    if (s.busy) return;
    const f = s.edit;
    s.busy = "save:" + m.id; s.actionError = null; render();
    try {
      const tags = String(f.tags || "").split(",").map(function (x) { return x.trim(); }).filter(Boolean);
      await ctx.api.patchMemory(m.id, {
        summary: f.summary, content: f.content, tags: tags, visibility: f.visibility,
        confidence: f.confidence || undefined, expected_version: m.version || 1
      });
      s.busy = ""; s.editing = ""; toast("已保存（版本 +1）", "ok"); load();
    } catch (e) { fail(e, "编辑带 expected_version：409 表示版本已变化，刷新后重试。"); }
  }

  async function createMemory() {
    if (s.busy) return;
    const f = s.form;
    if (!String(f.content || "").trim()) { toast("内容不能为空", "warn"); return; }
    if (!String(f.entity_key || "").trim()) { toast("实体键（entity_key）必填：用于去重与 supersede", "warn"); return; }
    s.busy = "create"; s.actionError = null; render();
    try {
      const tags = String(f.tags || "").split(",").map(function (x) { return x.trim(); }).filter(Boolean);
      await ctx.api.createMemory({
        direct: true, scope: f.scope, department_id: f.department_id, agent_id: "",
        entity_type: f.entity_type, entity_key: f.entity_key.trim(), content: f.content, summary: f.summary,
        tags: tags, confidence: f.confidence, visibility: f.visibility
      });
      s.busy = ""; s.createOpen = false; toast("已写入档案", "ok"); load();
    } catch (e) { fail(e, "直接写档案需要人类/助理/主管权限；员工只能提候选。"); }
  }

  async function createManual() {
    if (s.busy) return;
    const f = s.mForm;
    if (!String(f.content || "").trim()) { toast("手册内容不能为空", "warn"); return; }
    const required = ["角色", "目标", "红线", "升级"];
    for (let i = 0; i < required.length; i++) {
      if (f.content.indexOf(required[i]) < 0) { toast("手册必须包含「" + required[i] + "」章节（核心会校验）", "warn"); return; }
    }
    s.busy = "manual"; s.actionError = null; render();
    try {
      const sources = String(f.sources || "").split(",").map(function (x) { return x.trim(); }).filter(Boolean);
      await ctx.api.proposeManual({ scope: f.scope, department_id: f.department_id, agent_id: "", content: f.content, sources: sources });
      s.busy = ""; s.mCreateOpen = false; toast("手册新版本已提案", "ok"); loadManuals();
    } catch (e) { fail(e, "手册提案需要权限与内容；错误会原样显示。"); }
  }

  async function rollbackManual(m) {
    if (s.busy) return;
    if (!(m.version > 1)) { toast("没有更早版本可回滚", "warn"); return; }
    const ok = await confirmModal({ title: "回滚手册", body: "将生成一个指向 v" + (m.version - 1) + " 的新版本（不删历史）。", confirmText: "回滚" });
    if (!ok) return;
    s.busy = "rollback:" + m.id; s.actionError = null; render();
    try {
      await ctx.api.rollbackManual(m.id, { to_version: m.version - 1 });
      s.busy = ""; toast("已回滚", "ok"); loadManuals();
    } catch (e) { fail(e, "回滚目标版本必须存在；错误会原样显示。"); }
  }

  load();
  return { destroy: function () { disposed = true; } };
}
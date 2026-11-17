// 产出物：真实 R4 API —— 列表/详情/不可变版本/新增版本/关联评审。
import { el, clear, icon, badge, notice, emptyState, toast, relTime, truncate } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { renderMarkdown } from "../lib/mdrender.js";
import { nameOfAgent } from "../lib/ui.js";
import { diffLines, diffStat } from "../lib/diff.js";

const TYPES = [["", "全部类型"], ["prd", "PRD"], ["design", "方案"], ["code", "代码"], ["test", "测试报告"], ["review", "评审"], ["decision", "决策"]];
const TYPE_LABEL = { prd: "PRD", design: "方案", code: "代码", test: "测试报告", review: "评审", decision: "决策" };

export function mount(ctx, route) {
  const q = (route && route.query) || {};
  const s = {
    items: [], loading: true, error: null, type: "", taskId: q.task_id || "",
    id: q.id || "", artifact: null, versions: [], version: 0, versionData: null,
    versionLoading: false, actionError: null, actionHint: "", busy: "", newContent: "",
    createOpen: false, form: { type: "prd", title: "", content: "", task_id: q.task_id || "" }, reviews: []
  };
  let disposed = false;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "产出物"),
    el("div", { class: "topbar-sub" },
      el("span", { text: "版本只增不改；内容按 hash 存储" }),
      ctx.demo ? badge("演示数据", "warn") : null)));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { if (s.id) loadDetail(s.id); else loadList(); } } },
      icon("retry", 14), el("span", { text: "刷新" }))));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  function go(id) { ctx.navigate(id ? "#/artifacts?id=" + encodeURIComponent(id) : "#/artifacts"); }
  function fail(e, hint) { s.actionError = e; s.actionHint = hint || ""; s.busy = ""; render(); }
  function render() { clear(host); if (s.id) renderDetail(); else renderList(); }

  async function loadList() {
    s.loading = true; s.error = null; render();
    try {
      const params = {};
      if (s.type) params.type = s.type;
      if (s.taskId) params.task_id = s.taskId;
      const res = await ctx.api.artifacts(params);
      if (disposed) return;
      s.items = (res && res.items) || []; s.loading = false; render();
    } catch (e) {
      if (disposed) return;
      s.loading = false; s.error = e; render();
    }
  }

  async function loadDetail(id) {
    s.loading = true; s.error = null; s.artifact = null; s.versions = []; render();
    try {
      const list = await ctx.api.artifacts({});
      if (disposed) return;
      const all = (list && list.items) || [];
      s.artifact = all.filter(function (a) { return a.id === id; })[0] || { id: id, title: id, type: "", status: "" };
      const vers = await ctx.api.artifactVersions(id);
      if (disposed) return;
      s.versions = ((vers && vers.items) || []).slice().sort(function (a, b) { return (b.version || 0) - (a.version || 0); });
      s.loading = false;
      render();
      if (s.versions.length) loadVersion(s.versions[0].version);
      try {
        const rv = await ctx.api.reviews({ artifact_id: id });
        if (!disposed) { s.reviews = (rv && rv.items) || []; render(); }
      } catch (e2) { /* 评审读取失败不阻塞 */ }
    } catch (e) {
      if (disposed) return;
      s.loading = false; s.error = e; render();
    }
  }

  async function loadVersion(v) {
    s.version = v; s.versionLoading = true; render();
    try {
      const data = await ctx.api.artifactVersion(s.id || s.artifact.id, v);
      if (disposed) return;
      s.versionData = data || null; s.versionLoading = false; render();
    } catch (e) {
      if (disposed) return;
      s.versionLoading = false; s.actionError = e; s.actionHint = "读取版本内容失败（版本可能不存在）。"; render();
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

  // ---------- 列表 ----------
  function renderList() {
    host.appendChild(el("div", { class: "page-head" },
      el("div", null, el("h2", { text: "产出物" }), el("p", { text: "PRD / 方案 / 代码 / 测试报告 / 评审 / 决策；版本不可变。" })),
      el("span", { class: "grow" }),
      el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: ctx.demo }, on: { click: function () { s.createOpen = !s.createOpen; render(); } } },
        icon("plus", 14), el("span", { text: "新建产出物" }))));

    if (ctx.demo) host.appendChild(notice("演示模式：产出物需要真实后端（R4 /api/artifacts）。", "warn"));
    if (s.createOpen) host.appendChild(createForm());

    const filters = el("div", { class: "filters" });
    const typeSel = el("select", { class: "select", attrs: { "aria-label": "按类型过滤" } });
    TYPES.forEach(function (t) { typeSel.appendChild(el("option", { attrs: { value: t[0] }, text: t[1] })); });
    typeSel.value = s.type;
    typeSel.addEventListener("change", function () { s.type = typeSel.value; loadList(); });
    filters.appendChild(typeSel);
    if (s.taskId) filters.appendChild(el("span", { class: "search-stat", text: "按任务过滤：" + s.taskId }));
    host.appendChild(filters);

    if (s.actionError) host.appendChild(errCard());
    if (s.loading) { host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载产出物…" }))); return; }
    if (s.error) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:14px" } },
        el("div", { class: "card-title", text: "产出物加载失败" }),
        el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.error.message || String(s.error) }),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } },
          el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: loadList } }, "重试"))));
      return;
    }
    if (!s.items.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "file", title: "还没有产出物",
        desc: "Agent 在任务里提交 PRD/方案/代码/测试报告后会出现在这里；你也可以手动新建。",
        later: "版本只增不改，可查看历史版本"
      })));
      return;
    }

    const table = el("table", { class: "table", attrs: { style: "margin-top:14px" } },
      el("thead", null, el("tr", null, ["产出物", "类型", "状态", "创建人", "创建时间"].map(function (h) { return el("th", { text: h }); }))));
    const tbody = el("tbody");
    s.items.forEach(function (a) {
      tbody.appendChild(el("tr", { attrs: { style: "cursor:pointer" }, on: { click: function () { go(a.id); } } },
        el("td", null, el("div", { attrs: { style: "font-weight:600" }, text: a.title || a.id }),
          el("div", { style: "font-family:var(--mono);font-size:10.5px;color:var(--faint)", text: a.id })),
        el("td", null, badge(TYPE_LABEL[a.type] || a.type || "—", "")),
        el("td", { text: a.status || "—" }),
        el("td", { text: a.created_by || "—" }),
        el("td", { attrs: { title: a.created_at || "" }, text: relTime(a.created_at) })));
    });
    table.appendChild(tbody);
    host.appendChild(table);
  }

  // ---------- 新建 ----------
  function createForm() {
    const f = s.form;
    const typeSel = el("select", { class: "select" });
    TYPES.filter(function (t) { return t[0]; }).forEach(function (t) { typeSel.appendChild(el("option", { attrs: { value: t[0] }, text: t[1] })); });
    typeSel.value = f.type;
    typeSel.addEventListener("change", function () { f.type = typeSel.value; });
    const title = el("input", { class: "input", attrs: { type: "text", placeholder: "标题" } });
    title.value = f.title; title.addEventListener("input", function () { f.title = title.value; });
    const taskSel = el("select", { class: "select" });
    taskSel.appendChild(el("option", { attrs: { value: "" }, text: "关联任务（可选）" }));
    (state.tasks || []).forEach(function (t) { taskSel.appendChild(el("option", { attrs: { value: t.id }, text: t.title })); });
    taskSel.value = f.task_id;
    taskSel.addEventListener("change", function () { f.task_id = taskSel.value; });
    const content = el("textarea", { class: "textarea", attrs: { rows: "6", placeholder: "内容（Markdown）" } });
    content.value = f.content; content.addEventListener("input", function () { f.content = content.value; });

    return el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title", text: "新建产出物（创建即 v1）" }),
      el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
        el("div", { class: "field" }, el("label", { text: "类型" }), typeSel),
        el("div", { class: "field" }, el("label", { text: "标题" }), title),
        el("div", { class: "field" }, el("label", { text: "关联任务" }), taskSel)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "内容" }), content),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "create" }, on: { click: createArtifact } },
          s.busy === "create" ? "创建中…" : "创建"),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.createOpen = false; render(); } } }, "收起")));
  }

  async function createArtifact() {
    if (s.busy) return;
    const f = s.form;
    if (!f.title.trim()) { toast("请填写标题", "warn"); return; }
    s.busy = "create"; s.actionError = null; render();
    try {
      const res = await ctx.api.createArtifact({ type: f.type, title: f.title.trim(), content: f.content, task_id: f.task_id, department_id: "" });
      s.busy = ""; s.createOpen = false; f.title = ""; f.content = "";
      const id = res && res.artifact ? res.artifact.id : "";
      toast("产出物已创建" + (id ? "：" + id : ""), "ok");
      if (id) go(id);
    } catch (e) {
      fail(e, "创建产出物需要会话 + CSRF；权限/参数错误会原样显示。");
    }
  }

  // ---------- 详情 ----------
  function renderDetail() {
    const back = el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { go(""); } } }, "← 返回产出列表");
    host.appendChild(el("div", { class: "row", attrs: { style: "margin-bottom:12px" } }, back, el("span", { class: "grow" })));

    if (s.loading) {
      host.appendChild(el("div", { class: "empty" }, el("span", { class: "spinner" }), el("p", { text: "正在加载产出物…" })));
      return;
    }
    if (s.error) {
      host.appendChild(el("div", { class: "card" },
        el("div", { class: "card-title", text: "产出物加载失败" }),
        el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.error.message || String(s.error) })));
      return;
    }
    const a = s.artifact || {};
    host.appendChild(el("div", { class: "card" },
      el("div", { class: "card-title" }, el("span", { text: a.title || a.id }), badge(TYPE_LABEL[a.type] || a.type || "—", "sky"),
        a.status ? badge(a.status, "") : null),
      el("div", { class: "card-meta", attrs: { style: "margin-top:4px" } },
        el("span", { text: "id：" + (a.id || "") }),
        s.versions.length ? el("span", { text: " · 版本数：" + s.versions.length }) : null,
        a.created_by ? el("span", { text: " · 创建人：" + a.created_by }) : null)));

    if (s.actionError) host.appendChild(errCard());

    // 版本切换 + 内容
    const versCard = el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title" }, el("span", { text: "版本" }), el("span", { class: "grow" })));
    if (!s.versions.length) {
      versCard.appendChild(el("div", { class: "card-meta", text: "还没有版本" }));
    } else {
      const chips = el("div", { class: "row", attrs: { style: "margin-top:8px;gap:6px" } });
      s.versions.forEach(function (v) {
        const selected = v.version === s.version;
        chips.appendChild(el("button", {
          class: "filter-chip", attrs: { type: "button", "aria-pressed": selected ? "true" : "false" },
          on: { click: function () { loadVersion(v.version); } }
        }, "v" + v.version + (v.content_hash ? " · " + truncate(v.content_hash, 8) : "")));
      });
      versCard.appendChild(chips);
    }
    if (s.versionLoading) versCard.appendChild(el("div", { class: "card-meta", attrs: { style: "margin-top:8px" } }, el("span", { class: "spinner" }), " 正在读取版本…"));
    else if (s.versionData) {
      versCard.appendChild(el("div", { class: "card-meta", attrs: { style: "margin-top:8px" } },
        el("span", { text: "作者：" + nameOfAgent(s.versionData.author_id) }),
        el("span", { text: " · " + (s.versionData.created_at || "") })));
      versCard.appendChild(el("div", { class: "card-body", attrs: { style: "margin-top:8px" } }, renderMarkdown(s.versionData.content || "")));
      // 与上一版本对比
      const prev = s.versions.filter(function (x) { return x.version === s.version - 1; })[0];
      if (prev) {
        const ops = diffLines(prev.content || "", s.versionData.content || "");
        const stat = diffStat(ops);
        const diffWrap = el("div", { attrs: { style: "margin-top:10px;border-top:1px dashed var(--line-2);padding-top:8px" } },
          el("div", { class: "row", attrs: { style: "gap:8px" } },
            el("span", { class: "card-meta", text: "与 v" + prev.version + " 对比" }),
            badge("+" + stat.add, "ok"), badge("-" + stat.del, "bad")));
        const pre = el("pre", { attrs: { style: "margin-top:6px;max-height:240px;overflow:auto;background:#0a0f14;border:1px solid var(--line);border-radius:8px;padding:8px 10px;font-family:var(--mono);font-size:12px;line-height:1.5" } });
        ops.forEach(function (op) {
          const prefix = op.t === "add" ? "+ " : op.t === "del" ? "- " : "  ";
          const color = op.t === "add" ? "#6ee7b7" : op.t === "del" ? "#fb7185" : "#8b97a8";
          pre.appendChild(el("div", { attrs: { style: "color:" + color + ";white-space:pre-wrap" }, text: prefix + op.v }));
        });
        diffWrap.appendChild(pre);
        versCard.appendChild(diffWrap);
      }
    }
    host.appendChild(versCard);

    // 新版本
    const ta = el("textarea", { class: "textarea", attrs: { rows: "5", placeholder: "新版本内容（不可变：提交后成为新的版本）" } });
    ta.value = s.newContent; ta.addEventListener("input", function () { s.newContent = ta.value; });
    host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title", text: "提交新版本" }),
      el("div", { class: "field", attrs: { style: "margin-top:8px" } }, ta),
      el("div", { class: "row", attrs: { style: "margin-top:8px" } },
        el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: ctx.demo || s.busy === "version" }, on: { click: addVersion } },
          s.busy === "version" ? "提交中…" : "提交新版本"))));

    // 关联评审
    host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title" }, el("span", { text: "关联评审（" + s.reviews.length + "）" }), el("span", { class: "grow" }),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { ctx.navigate("#/reviews?artifact_id=" + encodeURIComponent(a.id)); } } }, "去评审页")),
      s.reviews.length
        ? el("div", { class: "stack", attrs: { style: "margin-top:8px" } }, s.reviews.map(function (r) {
          return el("div", { class: "row", attrs: { style: "gap:8px" } },
            badge(String(r.severity || "major").toUpperCase(), r.severity === "blocker" ? "bad" : "warn"),
            el("span", { text: truncate(r.claim || "", 80) }),
            badge(r.status || "", ""),
            el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { ctx.navigate("#/reviews?id=" + encodeURIComponent(r.id)); } } }, "打开"));
        }))
        : el("div", { class: "card-meta", attrs: { style: "margin-top:6px" }, text: "暂无评审" })));
  }

  async function addVersion() {
    if (s.busy) return;
    if (!String(s.newContent || "").trim()) { toast("新版本内容不能为空", "warn"); return; }
    s.busy = "version"; s.actionError = null; render();
    try {
      await ctx.api.addArtifactVersion(s.artifact.id, { content: s.newContent, supersedes: "" });
      s.busy = ""; s.newContent = "";
      toast("已提交新版本", "ok");
      loadDetail(s.artifact.id);
    } catch (e) {
      fail(e, "版本只增不改；权限/参数错误会原样显示。");
    }
  }

  if (s.id) loadDetail(s.id); else { if (s.taskId) { ctx.api.tasks({}).then(function (res) { state.tasks = (res && res.items) || []; }).catch(function () {}); } loadList(); }
  return { destroy: function () { disposed = true; } };
}
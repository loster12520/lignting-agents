// 部门讨论 / 跨部门辩论 / 决策卡（R6 真实 API）。
// 讨论与立场都以消息形式落在会话里（meta.kind=dept.discussion / debate.stance），
// 5/15 限制、stall、权限都由核心校验；前端只做计数提示与原样错误展示。
import { el, clear, icon, badge, notice, emptyState, toast, confirmModal, relTime, fmtDateTime, truncate } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { nameOfAgent, nameOfDept, statusBadge } from "../lib/ui.js";

const DECISION_STATUS = { proposed: ["提议中", "warn"], accepted: ["已采纳", "ok"], superseded: ["已取代", ""], rejected: ["已否决", "bad"] };

function metaOf(m) {
  try { return JSON.parse(m.meta || "{}") || {}; } catch (e) { return {}; }
}
function listMeta(items, kind) {
  const out = [];
  items.forEach(function (m) {
    const meta = metaOf(m);
    if (meta.kind === kind) out.push({ m: m, meta: meta });
  });
  return out.sort(function (a, b) { return a.m.seq - b.m.seq; });
}
function countByActor(entries) {
  const map = new Map();
  entries.forEach(function (e) {
    const id = e.meta.actor_id || e.m.sender_id || "?";
    map.set(id, (map.get(id) || 0) + 1);
  });
  return map;
}

export function mount(ctx, route) {
  const q = (route && route.query) || {};
  const deptConv = (state.conversations.filter(function (c) { return c.type === "department"; })[0] || {}).id || "";
  const crossConv = (state.conversations.filter(function (c) { return c.type === "cross_dept"; })[0] || {}).id || "";
  const s = {
    tab: q.tab === "debate" ? "debate" : q.tab === "decisions" ? "decisions" : "dept",
    convId: "", messages: [], loading: false, error: null,
    busy: "", actionError: null, actionHint: "",
    deptForm: { actor_agent_id: "", perspective: "", stance: "", evidence: "" },
    debateForm: { actor_agent_id: "", debate_id: "", round: 1, stance: "", evidence_ids: "" },
    escalateOpen: false, escalate: { level: "L2", issue: "", evidence: "", options: "", recommendation: "", consequence: "", task_id: "" },
    closeOpen: false, close: { title: "", decision: "", rationale: "", dissent: "", affected_tasks: "", affected_artifacts: "", supersedes: "", revisit_trigger: "" },
    reopenEvidence: "",
    decisions: [], dLoading: false, dError: null, q: q.q || "", dStatus: "",
    selected: "", detail: null, detailError: null,
    dissent: "", supersedeId: ""
  };
  let disposed = false;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "讨论与决策"),
    el("div", { class: "topbar-sub" },
      el("span", { text: "部门内讨论 5/15 · 跨部门辩论 R1/R2 · 决策卡与异议" }),
      ctx.demo ? badge("演示数据", "warn") : null)));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", {
      class: "btn btn-sm", attrs: { type: "button" },
      on: { click: function () { if (s.tab === "decisions") loadDecisions(); else loadConversation(); } }
    }, icon("retry", 14), el("span", { text: "刷新" }))));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  function fail(e, hint) { s.actionError = e; s.actionHint = hint || ""; s.busy = ""; render(); }
  function render() { clear(host); if (s.tab === "decisions") renderDecisions(); else renderDebateTab(); }

  function currentConvId() {
    if (s.convId) return s.convId;
    return s.tab === "debate" ? crossConv : deptConv;
  }

  async function loadConversation() {
    const id = currentConvId();
    if (!id) { s.messages = []; s.error = null; render(); return; }
    s.loading = true; s.error = null; render();
    try {
      const res = await ctx.api.messages(id, { limit: 100 });
      if (disposed) return;
      s.messages = (res && res.items) || [];
      s.loading = false; render();
    } catch (e) {
      if (disposed) return;
      s.loading = false; s.error = e; render();
    }
  }

  async function loadDecisions() {
    s.dLoading = true; s.dError = null; render();
    try {
      const params = {};
      if (s.q) params.q = s.q;
      if (s.dStatus) params.status = s.dStatus;
      const res = await ctx.api.decisions(params);
      if (disposed) return;
      s.decisions = (res && res.items) || [];
      s.dLoading = false;
      render();
      if (s.selected && !s.detail) loadDetail(s.selected);
    } catch (e) {
      if (disposed) return;
      s.dLoading = false; s.dError = e; render();
    }
  }

  async function loadDetail(id) {
    s.selected = id; s.detail = null; s.detailError = null; render();
    try {
      const res = await ctx.api.decisionDetail(id);
      if (disposed) return;
      s.detail = res || null; render();
    } catch (e) {
      if (disposed) return;
      s.detailError = e; render();
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
    const defs = [["dept", "部门讨论"], ["debate", "跨部门辩论"], ["decisions", "决策卡"]];
    const bar = el("div", { class: "tabs" });
    defs.forEach(function (d) {
      bar.appendChild(el("button", {
        class: "tab" + (s.tab === d[0] ? " active" : ""), attrs: { type: "button" },
        on: {
          click: function () {
            s.tab = d[0]; s.convId = ""; s.actionError = null;
            ctx.navigate("#/discussions?tab=" + d[0]);
            if (d[0] === "decisions") loadDecisions(); else loadConversation();
          }
        }
      }, d[1]));
    });
    return bar;
  }

  function convSelect(kind, value, onchange) {
    const sel = el("select", { class: "select", attrs: { "aria-label": "选择会话" } });
    const list = state.conversations.filter(function (c) { return c.type === kind; });
    sel.appendChild(el("option", { attrs: { value: "" }, text: list.length ? "选择会话" : "没有可用的" + (kind === "department" ? "部门群聊" : "跨部门群聊") }));
    list.forEach(function (c) { sel.appendChild(el("option", { attrs: { value: c.id }, text: c.title + "（" + nameOfDept(c.department_id) + "）" })); });
    sel.value = value;
    sel.addEventListener("change", function () { onchange(sel.value); });
    return sel;
  }

  // ---------- 讨论 / 辩论 ----------
  function renderDebateTab() {
    const isDebate = s.tab === "debate";
    host.appendChild(el("div", { class: "page-head" },
      el("div", null,
        el("h2", { text: isDebate ? "跨部门辩论" : "部门内讨论" }),
        el("p", { text: isDebate
          ? "主管代表部门发言：R1/R2 立场、必须带 evidence_ids；每人≤5、总计≤15；重复立场无新证据不计轮次（stall 可升级人类）。"
          : "员工/主管按分工视角发言；每人≤5、总计≤15；讨论记录进任务线程。" })),
      el("span", { class: "grow" }),
      el("div", { class: "row" }, convSelect(isDebate ? "cross_dept" : "department", currentConvId(), function (v) {
        s.convId = v; loadConversation();
      }))));
    host.appendChild(tabs());
    if (ctx.demo) host.appendChild(notice("演示模式：讨论/辩论需要真实后端（R6）。", "warn"));
    if (s.actionError) host.appendChild(errCard());

    const convId = currentConvId();
    if (!convId) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "chat", title: isDebate ? "还没有跨部门群聊" : "还没有部门群聊",
        desc: "部门群聊/跨部门评审群由 provisioning 或 R3 创建；也可以先在组织页组建部门。",
        later: "R6：讨论/立场以消息落库"
      })));
      return;
    }

    const entries = isDebate ? listMeta(s.messages, "debate.stance") : listMeta(s.messages, "dept.discussion");
    const byActor = countByActor(entries);
    const total = entries.length;
    const stat = el("div", { class: "filters", attrs: { style: "margin-top:12px" } },
      badge(total + " / 15 条", total >= 15 ? "bad" : total >= 12 ? "warn" : "ok"));
    byActor.forEach(function (n, id) {
      stat.appendChild(badge(nameOfAgent(id) + " " + n + "/5", n >= 5 ? "bad" : ""));
    });
    if (isDebate) {
      const rounds = new Set(entries.map(function (e) { return e.meta.round || 1; }));
      stat.appendChild(badge("轮次 R" + Array.from(rounds).sort().join("/R"), "sky"));
    }
    host.appendChild(stat);

    if (s.loading) { host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载讨论…" }))); return; }
    if (s.error) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:14px" } },
        el("div", { class: "card-title", text: "讨论加载失败" }),
        el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.error.message || String(s.error) }),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } }, el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: loadConversation } }, "重试"))));
      return;
    }

    // 时间线
    if (!entries.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "chat", title: isDebate ? "还没有辩论立场" : "还没有讨论发言",
        desc: isDebate ? "由部门主管提交 R1/R2 立场，必须带 evidence_ids。" : "员工先确认分工视角与计划，有分歧再进入讨论。",
        later: "5/15 上限由核心校验"
      })));
    } else {
      const stack = el("div", { class: "stack", attrs: { style: "margin-top:14px" } });
      let lastRound = null;
      entries.forEach(function (e) {
        const round = e.meta.round || 1;
        if (isDebate && round !== lastRound) {
          lastRound = round;
          stack.appendChild(el("div", { class: "day-sep" }, el("span", { text: "R" + round })));
        }
        stack.appendChild(el("div", { class: "card" },
          el("div", { class: "card-title" },
            el("span", { text: nameOfAgent(e.meta.actor_id || e.m.sender_id) }),
            badge("视角：" + (e.meta.perspective || "—"), ""),
            e.meta.round ? badge("R" + e.meta.round, "sky") : null,
            el("span", { class: "grow" }),
            el("span", { class: "card-meta", text: relTime(e.m.created_at) })),
          el("div", { class: "card-body", attrs: { style: "margin-top:6px" }, text: e.meta.stance || e.m.content || "" }),
          (e.meta.evidence_ids || []).length ? el("div", { class: "card-meta", text: "证据：" + e.meta.evidence_ids.join("、") }) : null,
          e.meta.evidence ? el("div", { class: "card-meta", text: "证据：" + e.meta.evidence }) : null));
      });
      host.appendChild(stack);
    }

    // 发言表单
    host.appendChild(isDebate ? debateForm(convId, entries) : deptForm(convId, entries));

    if (isDebate) {
      host.appendChild(escalateCard(convId));
      host.appendChild(closeCard());
    }
  }

  function agentSelect(list, value, onchange) {
    const sel = el("select", { class: "select" });
    sel.appendChild(el("option", { attrs: { value: "" }, text: "以谁的身份发言（actor）" }));
    list.forEach(function (a) { sel.appendChild(el("option", { attrs: { value: a.id }, text: a.name + "（" + (a.perspective || a.kind) + "）" })); });
    sel.value = value;
    sel.addEventListener("change", function () { onchange(sel.value); });
    return sel;
  }

  function deptForm(convId, entries) {
    const f = s.deptForm;
    const conv = state.conversations.filter(function (c) { return c.id === convId; })[0] || {};
    const members = state.agents.filter(function (a) {
      return (a.kind === "employee" || a.kind === "supervisor") && (!conv.department_id || a.department_id === conv.department_id);
    });
    const actor = agentSelect(members, f.actor_agent_id, function (v) { f.actor_agent_id = v; });
    const perspective = el("input", { class: "input", attrs: { type: "text", placeholder: "分工视角（接口/页面/用例）" } });
    perspective.value = f.perspective; perspective.addEventListener("input", function () { f.perspective = perspective.value; });
    const stance = el("textarea", { class: "textarea", attrs: { rows: "2", placeholder: "立场/意见" } });
    stance.value = f.stance; stance.addEventListener("input", function () { f.stance = stance.value; });
    const evidence = el("input", { class: "input", attrs: { type: "text", placeholder: "证据（可选）" } });
    evidence.value = f.evidence; evidence.addEventListener("input", function () { f.evidence = evidence.value; });
    const mine = f.actor_agent_id ? entries.filter(function (e) { return (e.meta.actor_id || e.m.sender_id) === f.actor_agent_id; }).length : 0;

    return el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title" }, el("span", { text: "发表讨论（核心限制：每人≤5、总≤15）" }),
        f.actor_agent_id ? badge("该成员已发 " + mine + "/5", mine >= 5 ? "bad" : "") : null),
      el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
        el("div", { class: "field" }, el("label", { text: "发言身份" }), actor),
        el("div", { class: "field" }, el("label", { text: "分工视角" }), perspective)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "立场" }), stance),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "证据" }), evidence),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: ctx.demo || s.busy === "dept" }, on: { click: postDiscussion } },
          s.busy === "dept" ? "提交中…" : "发言"),
        el("span", { class: "card-meta", text: "只有部门成员（员工/主管）可以发言；助理会 403。" })));
  }

  function debateForm(convId, entries) {
    const f = s.debateForm;
    if (!f.debate_id) {
      const first = entries[0];
      f.debate_id = (first && first.meta.debate_id) || convId;
    }
    const actors = state.agents.filter(function (a) { return a.kind === "supervisor" || a.kind === "assistant"; });
    const actor = agentSelect(actors, f.actor_agent_id, function (v) { f.actor_agent_id = v; });
    const debateId = el("input", { class: "input", attrs: { type: "text", placeholder: "debate_id（默认=会话 id）" } });
    debateId.value = f.debate_id; debateId.addEventListener("input", function () { f.debate_id = debateId.value; });
    const round = el("select", { class: "select" });
    [1, 2, 3].forEach(function (r) { round.appendChild(el("option", { attrs: { value: String(r) }, text: "R" + r })); });
    round.value = String(f.round);
    round.addEventListener("change", function () { f.round = Number(round.value); });
    const stance = el("textarea", { class: "textarea", attrs: { rows: "2", placeholder: "立场（必须带证据）" } });
    stance.value = f.stance; stance.addEventListener("input", function () { f.stance = stance.value; });
    const evidence = el("input", { class: "input", attrs: { type: "text", placeholder: "evidence_ids：逗号分隔（至少 1 个）" } });
    evidence.value = f.evidence_ids; evidence.addEventListener("input", function () { f.evidence_ids = evidence.value; });

    return el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title", text: "提交立场（部门主管代表部门；R1/R2）" }),
      el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
        el("div", { class: "field" }, el("label", { text: "发言身份（主管/助理）" }), actor),
        el("div", { class: "field" }, el("label", { text: "debate_id" }), debateId),
        el("div", { class: "field" }, el("label", { text: "轮次" }), round)),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "立场" }), stance),
      el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "evidence_ids" }), evidence),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: ctx.demo || s.busy === "stance" }, on: { click: submitStance } },
          s.busy === "stance" ? "提交中…" : "提交立场"),
        el("span", { class: "card-meta", text: "每人≤5/总≤15；重复立场且无新证据会 409（stall 提示）。" })));
  }

  function escalateCard(convId) {
    const f = s.escalate;
    const btn = el("button", {
      class: "btn btn-sm btn-danger", attrs: { type: "button", disabled: ctx.demo },
      on: { click: function () { s.escalateOpen = !s.escalateOpen; render(); } }
    }, "升级人类（L0–L4）");
    const card = el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title" }, el("span", { text: "辩论升级" }), el("span", { class: "grow" }), btn));
    if (!s.escalateOpen) return card;
    const field = function (label, node) { return el("div", { class: "field" }, el("label", { text: label }), node); };
    const mk = function (key, label, rows) {
      const node = rows ? el("textarea", { class: "textarea", attrs: { rows: String(rows), placeholder: label } })
        : el("input", { class: "input", attrs: { type: "text", placeholder: label } });
      node.value = f[key] || "";
      node.addEventListener("input", function () { f[key] = node.value; });
      return node;
    };
    const level = el("select", { class: "select" });
    ["L0", "L1", "L2", "L3", "L4"].forEach(function (l) { level.appendChild(el("option", { attrs: { value: l }, text: l })); });
    level.value = f.level;
    level.addEventListener("change", function () { f.level = level.value; });
    card.appendChild(el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
      field("级别", level),
      field("任务 id（可选）", mk("task_id", "task_...")),
      field("问题", mk("issue", "争议点")),
      field("证据", mk("evidence", "evidence_ids/链接"))));
    card.appendChild(el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
      field("选项", mk("options", "A/B/C")),
      field("建议", mk("recommendation", "建议方案")),
      field("后果", mk("consequence", "如果不决会怎样"))));
    card.appendChild(el("div", { class: "row", attrs: { style: "margin-top:10px" } },
      el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "escalate" }, on: { click: function () { doEscalate(convId); } } },
        s.busy === "escalate" ? "提交中…" : "提交升级包"),
      el("span", { class: "card-meta", text: "升级只记录决策包，不自动裁决；人类可在决策卡里处理。" })));
    return card;
  }

  function closeCard() {
    const f = s.close;
    const btn = el("button", {
      class: "btn btn-sm", attrs: { type: "button", disabled: ctx.demo },
      on: { click: function () { s.closeOpen = !s.closeOpen; render(); } }
    }, "关闭辩论并写决策");
    const card = el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title" }, el("span", { text: "裁决 / 决策记录" }), el("span", { class: "grow" }), btn));
    if (!s.closeOpen) return card;
    const mk = function (key, label, rows) {
      const node = rows ? el("textarea", { class: "textarea", attrs: { rows: String(rows), placeholder: label } })
        : el("input", { class: "input", attrs: { type: "text", placeholder: label } });
      node.value = f[key] || "";
      node.addEventListener("input", function () { f[key] = node.value; });
      return el("div", { class: "field" }, el("label", { text: label }), node);
    };
    card.appendChild(el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
      mk("title", "决策标题"), mk("revisit_trigger", "复查条件")));
    card.appendChild(el("div", { class: "field", attrs: { style: "margin-top:10px" } }, mk("decision", "决定", 2)));
    card.appendChild(el("div", { class: "field", attrs: { style: "margin-top:10px" } }, mk("rationale", "理由", 2)));
    card.appendChild(el("div", { class: "field", attrs: { style: "margin-top:10px" } }, mk("dissent", "异议（保留）", 2)));
    card.appendChild(el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
      mk("affected_tasks", "受影响任务（逗号）"), mk("affected_artifacts", "受影响产出物（逗号）")));
    card.appendChild(el("div", { class: "row", attrs: { style: "margin-top:10px" } },
      el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "close" }, on: { click: doCloseDebate } },
        s.busy === "close" ? "提交中…" : "写决策"),
      el("span", { class: "card-meta", text: "决策不可变；异议可后补；可用 supersede 关联新决策。" })));
    return card;
  }

  // ---------- 决策卡 ----------
  function renderDecisions() {
    host.appendChild(el("div", { class: "page-head" },
      el("div", null, el("h2", { text: "决策卡" }),
        el("p", { text: "背景/选项/决定/理由/异议/复查条件/受影响对象；supersede 链可追溯。" })),
      el("span", { class: "grow" })));
    host.appendChild(tabs());
    if (ctx.demo) host.appendChild(notice("演示模式：决策卡需要真实后端（R6 /api/decisions）。", "warn"));
    if (s.actionError) host.appendChild(errCard());

    const filters = el("div", { class: "filters" });
    const q = el("input", { class: "input", attrs: { type: "search", placeholder: "搜索标题/决定/理由", style: "max-width:240px" } });
    q.value = s.q;
    q.addEventListener("input", function () { s.q = q.value; });
    q.addEventListener("keydown", function (e) { if (e.key === "Enter") loadDecisions(); });
    filters.appendChild(q);
    const st = el("select", { class: "select", attrs: { "aria-label": "状态" } });
    st.appendChild(el("option", { attrs: { value: "" }, text: "全部状态" }));
    Object.keys(DECISION_STATUS).forEach(function (k) { st.appendChild(el("option", { attrs: { value: k }, text: DECISION_STATUS[k][0] })); });
    st.value = s.dStatus;
    st.addEventListener("change", function () { s.dStatus = st.value; loadDecisions(); });
    filters.appendChild(st);
    host.appendChild(filters);

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
        icon: "audit", title: "没有决策记录",
        desc: "跨部门辩论结束（关闭辩论）或 G3 批准后会写决策；也可以从辩论页提交裁决。",
        later: "异议与 supersede 链"
      })));
      return;
    }

    const grid = el("div", { class: "grid-2", attrs: { style: "margin-top:14px;align-items:start" } });
    const list = el("div", { class: "stack" });
    s.decisions.forEach(function (d) {
      const stt = DECISION_STATUS[d.status] || [d.status, ""];
      list.appendChild(el("div", { class: "card", attrs: { style: "cursor:pointer" + (s.selected === d.id ? ";border-color:var(--accent)" : "") },
        on: { click: function () { loadDetail(d.id); } } },
        el("div", { class: "card-title" }, el("span", { text: d.title || d.id }), badge(stt[0], stt[1])),
        el("div", { class: "card-meta", text: "决策人：" + (d.deciders || "—") + (d.dissent ? " · 异议：" + truncate(d.dissent, 40) : "") }),
        el("div", { class: "card-body", text: truncate(d.decision || "", 120) })));
    });
    grid.appendChild(list);
    grid.appendChild(detailPanel());
    host.appendChild(grid);
  }

  function detailPanel() {
    const box = el("div", { class: "card", attrs: { style: "position:sticky;top:10px" } });
    if (!s.selected) {
      box.appendChild(el("div", { class: "card-meta", text: "选择左侧决策查看完整卡片（背景/选项/决定/理由/异议/复查/影响）" }));
      return box;
    }
    if (s.detailError) {
      box.appendChild(el("div", { class: "card-title", text: "决策详情加载失败" }));
      box.appendChild(el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.detailError.message || String(s.detailError) }));
      return box;
    }
    const view = s.detail || {};
    const d = view.decision || view;
    box.appendChild(el("div", { class: "card-title" },
      el("span", { text: d.title || s.selected }),
      badge((DECISION_STATUS[d.status] || [d.status || "—"])[0], (DECISION_STATUS[d.status] || ["", ""])[1]),
      view.reopened ? badge("重开 " + view.reopened + " 次", "warn") : null));
    const kv = function (label, value) {
      return el("div", { attrs: { style: "margin-top:8px" } },
        el("div", { class: "card-meta", text: label }),
        el("div", { class: "card-body", attrs: { style: "margin-top:2px;white-space:pre-wrap" }, text: value || "—" }));
    };
    box.appendChild(kv("背景", d.context));
    box.appendChild(kv("选项", d.options));
    box.appendChild(kv("决定", d.decision));
    box.appendChild(kv("理由", d.rationale));
    box.appendChild(kv("异议（保留）", d.dissent));
    box.appendChild(kv("复查条件", d.revisit_trigger));
    box.appendChild(kv("受影响任务/产出物", [d.affected_tasks, d.affected_artifacts].filter(Boolean).join(" | ")));
    box.appendChild(el("div", { class: "card-meta", attrs: { style: "margin-top:8px" } },
      el("span", { text: "决策人：" + (d.deciders || "—") }),
      el("span", { text: " · 证据：" + (d.evidence || "—") }),
      el("span", { text: " · " + relTime(d.created_at) })));
    if (d.supersedes) box.appendChild(el("div", { class: "card-meta", text: "supersedes：" + d.supersedes }));
    if (d.superseded_by) box.appendChild(el("div", { class: "card-meta", text: "superseded_by：" + d.superseded_by }));

    // 追加异议
    const dissent = el("textarea", { class: "textarea", attrs: { rows: "2", placeholder: "追加异议（保留原文，不覆盖）" } });
    dissent.value = s.dissent;
    dissent.addEventListener("input", function () { s.dissent = dissent.value; });
    box.appendChild(el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "追加异议" }), dissent));

    // supersede / reopen
    const sup = el("input", { class: "input", attrs: { type: "text", placeholder: "新决策 id（supersede）" } });
    sup.value = s.supersedeId;
    sup.addEventListener("input", function () { s.supersedeId = sup.value; });
    const reopen = el("input", { class: "input", attrs: { type: "text", placeholder: "新证据（reopen）" } });
    reopen.value = s.reopenEvidence;
    reopen.addEventListener("input", function () { s.reopenEvidence = reopen.value; });

    box.appendChild(el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "supersede 新决策 id" }), sup));
    box.appendChild(el("div", { class: "field", attrs: { style: "margin-top:10px" } }, el("label", { text: "重开（新证据）" }), reopen));
    box.appendChild(el("div", { class: "row", attrs: { style: "margin-top:10px" } },
      el("button", { class: "btn btn-sm btn-primary", attrs: { type: "button", disabled: ctx.demo || s.busy === "dissent" }, on: { click: addDissent } }, "追加异议"),
      el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: ctx.demo || s.busy === "supersede" }, on: { click: doSupersede } }, "Supersede"),
      el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: ctx.demo || s.busy === "reopen" }, on: { click: doReopen } }, "重开辩论"),
      el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { loadDecisions(); } } }, "刷新")));
    return box;
  }

  // ---------- 动作 ----------
  async function postDiscussion() {
    if (s.busy) return;
    const f = s.deptForm;
    if (!f.actor_agent_id) { toast("先选发言身份（部门成员）", "warn"); return; }
    if (!f.perspective.trim() || !f.stance.trim()) { toast("讨论发言必须带分工视角与立场", "warn"); return; }
    s.busy = "dept"; s.actionError = null; render();
    try {
      await ctx.api.postDiscussion(currentConvId(), {
        actor_agent_id: f.actor_agent_id, perspective: f.perspective.trim(),
        stance: f.stance.trim(), evidence: f.evidence, debate_id: ""
      });
      s.busy = ""; f.stance = ""; f.evidence = "";
      toast("已发言", "ok"); loadConversation();
    } catch (e) { fail(e, "部门讨论仅限本部门员工/主管；超 5/15 会 409。"); }
  }

  async function submitStance() {
    if (s.busy) return;
    const f = s.debateForm;
    if (!f.actor_agent_id) { toast("先选发言身份（主管/助理）", "warn"); return; }
    if (!f.stance.trim()) { toast("立场不能为空", "warn"); return; }
    const ids = String(f.evidence_ids || "").split(",").map(function (x) { return x.trim(); }).filter(Boolean);
    if (!ids.length) { toast("必须带 evidence_ids（可核查证据）", "warn"); return; }
    s.busy = "stance"; s.actionError = null; render();
    try {
      await ctx.api.submitStance(currentConvId(), {
        actor_agent_id: f.actor_agent_id, debate_id: f.debate_id, round: f.round,
        stance: f.stance.trim(), evidence_ids: ids
      });
      s.busy = ""; f.stance = ""; f.evidence_ids = "";
      toast("立场已提交", "ok"); loadConversation();
    } catch (e) { fail(e, "重复立场无新证据会 409（stall）；超限会被拒绝。"); }
  }

  async function doEscalate(convId) {
    if (s.busy) return;
    const f = s.escalate;
    const debateId = s.debateForm.debate_id || convId;
    s.busy = "escalate"; s.actionError = null; render();
    try {
      const res = await ctx.api.escalateDebate(debateId, {
        conversation_id: convId, task_id: f.task_id, level: f.level, issue: f.issue,
        evidence: f.evidence, options: f.options, recommendation: f.recommendation, consequence: f.consequence
      });
      s.busy = ""; s.escalateOpen = false;
      toast("升级包已记录" + (res && res.id ? "：" + res.id : ""), "ok", 4200);
      loadConversation();
    } catch (e) { fail(e, "升级只记录不裁决；debate_id 与会话不匹配会报错。"); }
  }

  async function doCloseDebate() {
    if (s.busy) return;
    const f = s.close;
    if (!f.title.trim() || !f.decision.trim()) { toast("决策标题与决定必填", "warn"); return; }
    const ok = await confirmModal({ title: "关闭辩论并写决策", body: "决策不可变；异议可后补；写审计。确认？", confirmText: "写决策" });
    if (!ok) return;
    s.busy = "close"; s.actionError = null; render();
    try {
      const debateId = s.debateForm.debate_id || currentConvId();
      const res = await ctx.api.closeDebate(debateId, {
        title: f.title.trim(), decision: f.decision.trim(), rationale: f.rationale,
        dissent: f.dissent, affected_tasks: f.affected_tasks, affected_artifacts: f.affected_artifacts,
        supersedes: "", revisit_trigger: f.revisit_trigger
      });
      s.busy = ""; s.closeOpen = false;
      toast("决策已写入" + (res && res.id ? "：" + res.id : ""), "ok");
      loadConversation();
    } catch (e) { fail(e, "关闭辩论需要主管/助理权限；debate_id 必须存在。"); }
  }

  async function addDissent() {
    if (s.busy || !s.selected) return;
    if (!String(s.dissent || "").trim()) { toast("异议内容不能为空", "warn"); return; }
    s.busy = "dissent"; s.actionError = null; render();
    try {
      await ctx.api.decisionDissent(s.selected, { dissent: s.dissent.trim() });
      s.busy = ""; s.dissent = ""; toast("异议已追加（保留原文）", "ok"); loadDetail(s.selected);
    } catch (e) { fail(e, "异议追加失败；权限或参数错误会原样显示。"); }
  }

  async function doSupersede() {
    if (s.busy || !s.selected) return;
    if (!String(s.supersedeId || "").trim()) { toast("填写要取代当前决策的新决策 id", "warn"); return; }
    s.busy = "supersede"; s.actionError = null; render();
    try {
      await ctx.api.request("POST", "/api/decisions/" + encodeURIComponent(s.selected) + "/supersede", { new_id: s.supersedeId.trim() });
      s.busy = ""; toast("supersede 已记录", "ok"); loadDetail(s.selected);
    } catch (e) { fail(e, "supersede 需要新决策已存在；错误会原样显示。"); }
  }

  async function doReopen() {
    if (s.busy || !s.selected) return;
    if (!String(s.reopenEvidence || "").trim()) { toast("重开需要新证据", "warn"); return; }
    s.busy = "reopen"; s.actionError = null; render();
    try {
      await ctx.api.reopenDebate(s.selected, { new_evidence: s.reopenEvidence.trim() });
      s.busy = ""; toast("已重开（记录新证据）", "ok"); loadDetail(s.selected);
    } catch (e) { fail(e, "重开需要新证据；同一决策最多 1 次重开。"); }
  }

  if (s.tab === "decisions") loadDecisions(); else loadConversation();
  return { destroy: function () { disposed = true; } };
}

// Agent 管理页：列表/详情、生命周期、预算/用量、全局紧急停止。
// 只调用真实 API；演示模式由 mock 明确标注，不把 mock 当真实。
import { el, clear, icon, badge, notice, emptyState, toast, confirmModal, fmtDateTime, relTime, truncate } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { statusBadge, AGENT_STATUS, nameOfDept } from "../lib/ui.js";

const KINDS = [["", "全部类型"], ["assistant", "助理"], ["supervisor", "部门主管"], ["employee", "员工"]];
const STATUS_KEYS = Object.keys(AGENT_STATUS);
const KIND_LABEL = { assistant: "助理", supervisor: "部门主管", employee: "员工" };

function parseJSON(text) {
  if (!text) return null;
  if (typeof text === "object") return text;
  try { return JSON.parse(text); } catch (e) { return null; }
}

function roleMission(roleCard) {
  const c = parseJSON(roleCard);
  return c && typeof c.mission === "string" ? c.mission : (typeof roleCard === "string" ? roleCard : "");
}

function startOfDayISO() {
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  return d.toISOString();
}

function startOfMonthISO() {
  const d = new Date();
  d.setDate(1);
  d.setHours(0, 0, 0, 0);
  return d.toISOString();
}

function costOf(totals) {
  if (!totals) return 0;
  if (typeof totals.Cost === "number") return totals.Cost;
  if (typeof totals.cost === "number") return totals.cost;
  return 0;
}

function tokensOf(totals) {
  if (!totals) return 0;
  const p = typeof totals.PromptTokens === "number" ? totals.PromptTokens : (totals.prompt_tokens || 0);
  const c = typeof totals.CompletionTokens === "number" ? totals.CompletionTokens : (totals.completion_tokens || 0);
  return p + c;
}

export function mount(ctx) {
  const q = (state.route && state.route.query) || {};
  const s = {
    items: [],
    tasks: [],
    loading: true,
    error: null,
    actionError: null,
    actionHint: "",
    busy: "",
    filters: { status: q.status || "", kind: q.kind || "", department_id: q.department_id || "" },
    selectedId: q.id || "",
    detail: null,
    detailLoading: false,
    detailError: null,
    recent: [],
    recentError: null,
    detailTask: null,
    budgets: null,
    budgetError: null,
    usageDay: null,
    usageMonth: null,
    budgetForm: { daily: "", monthly: "" },
    savingBudget: false,
    emergency: { enabled: null, at: "", by: "", loading: true, error: null }
  };
  let disposed = false;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "Agent 管理"),
    el("div", { class: "topbar-sub" },
      el("span", { text: "生命周期 / 角色卡 / 预算 / 最近行为；高风险配置需审批" }),
      ctx.demo ? badge("演示数据", "warn") : null)));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { loadAll(); } } },
      icon("retry", 14), el("span", { text: "刷新" }))));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  function render() {
    if (disposed) return;
    clear(host);
    renderPage();
  }

  function fail(e, hint) {
    s.busy = "";
    s.actionError = e;
    s.actionHint = hint || "";
    render();
  }

  async function loadAll() {
    await Promise.all([loadAgents(), loadSide()]);
  }

  async function loadAgents() {
    s.loading = true;
    s.error = null;
    render();
    try {
      const res = await ctx.api.agents({
        status: s.filters.status,
        kind: s.filters.kind,
        department_id: s.filters.department_id
      });
      if (disposed) return;
      s.items = (res && res.items) || [];
      s.loading = false;
      render();
      try {
        const t = await ctx.api.tasks({});
        if (!disposed) { s.tasks = (t && t.items) || []; render(); }
      } catch (e2) { /* 任务标题读取失败不阻塞 Agent 管理 */ }
      if (s.selectedId) loadDetail(s.selectedId);
    } catch (e) {
      if (disposed) return;
      s.loading = false;
      s.error = e;
      render();
    }
  }

  async function loadSide() {
    s.budgetError = null;
    s.emergency.loading = true;
    try {
      const b = await ctx.api.budgets();
      if (disposed) return;
      s.budgets = b || null;
      if (!s.budgetForm.daily && b) s.budgetForm.daily = String(b.daily_cny == null ? "" : b.daily_cny);
      if (!s.budgetForm.monthly && b) s.budgetForm.monthly = String(b.monthly_cny == null ? "" : b.monthly_cny);
    } catch (e) {
      if (!disposed) s.budgetError = e;
    }
    try {
      const day = await ctx.api.usage({ from: startOfDayISO(), to: new Date().toISOString() });
      if (!disposed) s.usageDay = day || null;
    } catch (e) { if (!disposed) s.usageDay = null; }
    try {
      const month = await ctx.api.usage({ from: startOfMonthISO(), to: new Date().toISOString() });
      if (!disposed) s.usageMonth = month || null;
    } catch (e) { if (!disposed) s.usageMonth = null; }
    await loadEmergency();
    render();
  }

  async function loadEmergency() {
    s.emergency.loading = true;
    try {
      const res = await ctx.api.audit({ action: "admin.emergency_stop", limit: 10 });
      if (disposed) return;
      const items = (res && res.items) || [];
      let latest = null;
      items.forEach(function (a) {
        if (!latest) { latest = a; return; }
        if ((a.seq || 0) > (latest.seq || 0)) latest = a;
        else if ((a.seq || 0) === (latest.seq || 0) && String(a.created_at || "") > String(latest.created_at || "")) latest = a;
      });
      let enabled = null;
      if (latest) {
        const d = parseJSON(latest.detail);
        if (d && typeof d.emergency_stop === "boolean") enabled = d.emergency_stop;
      }
      s.emergency = {
        enabled: enabled, at: latest ? latest.created_at : "",
        by: latest ? latest.actor_id : "", loading: false, error: null
      };
    } catch (e) {
      if (!disposed) s.emergency = { enabled: null, at: "", by: "", loading: false, error: e };
    }
    if (!disposed) render();
  }

  async function loadDetail(id) {
    s.detailLoading = true;
    s.detailError = null;
    s.detail = null;
    s.recent = [];
    s.recentError = null;
    s.detailTask = null;
    render();
    try {
      const a = await ctx.api.agent(id);
      if (disposed) return;
      s.detail = a || null;
      s.detailLoading = false;
      render();
      if (a && a.current_task_id) {
        try {
          const t = await ctx.api.task(a.current_task_id);
          if (!disposed) { s.detailTask = t || null; render(); }
        } catch (e2) { /* task not found is non-fatal */ }
      }
      try {
        const audit = await ctx.api.audit({ actor_type: "agent", actor_id: id, limit: 8 });
        if (!disposed) { s.recent = (audit && audit.items) || []; render(); }
      } catch (e3) {
        if (!disposed) { s.recentError = e3; render(); }
      }
    } catch (e) {
      if (disposed) return;
      s.detailLoading = false;
      s.detailError = e;
      render();
    }
  }

  function pageHead() {
    const running = s.items.filter(function (a) { return a.status === "running"; }).length;
    const paused = s.items.filter(function (a) { return a.status === "paused" || a.status === "budget_blocked"; }).length;
    return el("div", { class: "page-head" },
      el("div", null,
        el("h2", { text: "Agent 管理" }),
        el("p", { text: "状态由核心执行；平台展示投影。高风险配置（模型/预算）需要 G2/人类审批。" })),
      el("span", { class: "grow" }),
      badge(s.items.length + " 名 Agent", "sky"),
      running ? badge("运行 " + running, "ok") : null,
      paused ? badge("暂停/阻塞 " + paused, "warn") : null);
  }

  function errorCard(title, err, retry) {
    return el("div", { class: "card", attrs: { style: "margin-top:12px;border-color:rgba(251,113,133,.4)" } },
      el("div", { class: "card-title" }, el("span", { text: title }), badge((err && err.code) || "error", "bad")),
      el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: (err && err.message) || String(err) }),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        retry ? el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: retry } }, "重试") : null));
  }

  function actionCard() {
    if (!s.actionError) return null;
    return el("div", { class: "card", attrs: { style: "margin-top:12px;border-color:rgba(251,113,133,.4)" } },
      el("div", { class: "card-title" }, el("span", { text: "操作失败" }), badge(s.actionError.code || "error", "bad")),
      el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.actionError.message || String(s.actionError) }),
      s.actionHint ? el("div", { class: "help", attrs: { style: "margin-top:6px" }, text: s.actionHint }) : null,
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.actionError = null; s.actionHint = ""; render(); } } }, "关闭")));
  }

  function emergencyCard() {
    const st = s.emergency;
    const known = st.enabled === true || st.enabled === false;
    const enabled = st.enabled === true;
    const busy = s.busy === "emergency";
    const body = el("div", { class: "card", attrs: { style: "margin-top:14px;border-color:" + (enabled ? "rgba(251,113,133,.55)" : "var(--line)") } },
      el("div", { class: "card-title" },
        el("span", { text: "全局紧急停止" }),
        enabled ? badge("已停止", "bad") : (known ? badge("运行中", "ok") : badge("状态未知", "warn")),
        el("span", { class: "grow" }),
        el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: st.loading }, on: { click: function () { loadEmergency(); } } }, "刷新状态")),
      el("div", { class: "card-meta", attrs: { style: "margin-top:6px" },
        text: "影响范围：立即停止所有 Agent 的新 turn；发消息返回 503 emergency_stop；浏览/检索/审批仍可用。恢复后从暂停点继续，不自动重试有副作用的操作。" }),
      st.error ? el("div", { class: "notice warn", attrs: { style: "margin-top:8px" }, text: "无法读取当前状态（审计接口不可用）：" + (st.error.message || String(st.error)) }) : null,
      st.at ? el("div", { class: "card-meta", attrs: { style: "margin-top:4px" },
        text: "最近一次：" + fmtDateTime(st.at) + " · 操作者 " + (st.by || "—") + " · 已写审计" }) : el("div", { class: "card-meta", attrs: { style: "margin-top:4px" }, text: "没有历史停止记录；停止/恢复都会写审计。" }),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        enabled
          ? el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: busy }, on: { click: function () { setEmergency(false); } } }, busy ? "处理中…" : "恢复运行")
          : el("button", { class: "btn btn-danger btn-sm", attrs: { type: "button", disabled: busy }, on: { click: function () { setEmergency(true); } } }, busy ? "处理中…" : "紧急停止"),
        el("span", { class: "help", text: "需要二次确认；这是人类操作，会写审计并广播 admin.emergency_stop 事件。" })));
    return body;
  }

  async function setEmergency(enabled) {
    if (s.busy) return;
    const ok = await confirmModal({
      title: enabled ? "确认全局紧急停止？" : "确认恢复运行？",
      body: enabled
        ? "将立即停止所有 Agent 的新 turn；消息发送会被拒绝（503 emergency_stop）。浏览、检索、审批仍可用。操作会写审计。"
        : "将恢复所有 Agent 接新任务；不会自动重试中断的有副作用操作。操作会写审计。",
      confirmText: enabled ? "停止全部" : "恢复运行",
      danger: enabled
    });
    if (!ok) return;
    s.busy = "emergency";
    s.actionError = null;
    render();
    try {
      const res = await ctx.api.emergencyStop(enabled);
      s.busy = "";
      s.emergency.enabled = res && typeof res.emergency_stop === "boolean" ? res.emergency_stop : enabled;
      s.emergency.at = new Date().toISOString();
      s.emergency.by = (state.owner && state.owner.id) || "owner";
      toast(enabled ? "已全局停止所有 Agent" : "已恢复运行", enabled ? "warn" : "ok");
      loadEmergency();
    } catch (e) {
      fail(e, "紧急停止由平台管理员接口执行；失败会显示真实错误，不会假装成功。");
    }
  }

  function usageBar(used, limit) {
    const pct = limit > 0 ? Math.min(100, Math.max(0, (used / limit) * 100)) : 0;
    const color = pct >= 100 ? "var(--bad)" : pct >= 80 ? "var(--warn)" : "var(--accent)";
    return el("div", { class: "row", attrs: { style: "gap:8px;align-items:center;margin-top:4px" } },
      el("div", { attrs: { style: "flex:1;height:8px;background:var(--line);border-radius:4px;overflow:hidden" } },
        el("div", { attrs: { style: "height:100%;width:" + pct.toFixed(1) + "%;background:" + color } })),
      badge(pct.toFixed(0) + "%", pct >= 100 ? "bad" : pct >= 80 ? "warn" : "ok"));
  }

  function budgetCard() {
    const b = s.budgets;
    const dayUsed = costOf(s.usageDay);
    const monthUsed = costOf(s.usageMonth);
    const daily = b ? Number(b.daily_cny || 0) : 0;
    const monthly = b ? Number(b.monthly_cny || 0) : 0;
    const dailyInput = el("input", { class: "input", attrs: { type: "number", min: "0", step: "0.01", style: "max-width:120px" } });
    dailyInput.value = s.budgetForm.daily;
    dailyInput.addEventListener("input", function () { s.budgetForm.daily = dailyInput.value; });
    const monthlyInput = el("input", { class: "input", attrs: { type: "number", min: "0", step: "0.01", style: "max-width:120px" } });
    monthlyInput.value = s.budgetForm.monthly;
    monthlyInput.addEventListener("input", function () { s.budgetForm.monthly = monthlyInput.value; });

    const card = el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title" },
        el("span", { text: "预算与用量" }),
        daily > 0 && dayUsed >= daily ? badge("日预算 100%", "bad") : daily > 0 && dayUsed >= daily * 0.8 ? badge("日预算 ≥80%", "warn") : null,
        monthly > 0 && monthUsed >= monthly ? badge("月预算 100%", "bad") : monthly > 0 && monthUsed >= monthly * 0.8 ? badge("月预算 ≥80%", "warn") : null,
        el("span", { class: "grow" })),
      s.budgetError ? notice("预算接口读取失败：" + (s.budgetError.message || String(s.budgetError)), "bad") : null,
      b ? el("div", { attrs: { style: "margin-top:8px" } },
        el("div", { class: "row", attrs: { style: "gap:10px;flex-wrap:wrap" } },
          el("div", { attrs: { style: "flex:1;min-width:220px" } },
            el("div", { class: "card-meta", text: "今日 ¥" + dayUsed.toFixed(2) + " / ¥" + daily.toFixed(2) + (s.usageDay ? " · " + tokensOf(s.usageDay) + " tokens" : "") }),
            usageBar(dayUsed, daily)),
          el("div", { attrs: { style: "flex:1;min-width:220px" } },
            el("div", { class: "card-meta", text: "本月 ¥" + monthUsed.toFixed(2) + " / ¥" + monthly.toFixed(2) + (s.usageMonth ? " · " + tokensOf(s.usageMonth) + " tokens" : "") }),
            usageBar(monthUsed, monthly))),
        el("div", { class: "row", attrs: { style: "margin-top:10px;flex-wrap:wrap" } },
          el("span", { class: "card-meta", text: "日预算" }), dailyInput,
          el("span", { class: "card-meta", text: "月预算" }), monthlyInput,
          el("button", { class: "btn btn-sm btn-primary", attrs: { type: "button", disabled: s.savingBudget }, on: { click: saveBudget } }, s.savingBudget ? "保存中…" : "保存预算"),
          el("span", { class: "help", text: "80% 告警、100% 暂停接新任务；改动写审计。追加预算入口即此表单。" })),
        Object.keys(b.departments || {}).length
          ? el("div", { class: "card-meta", attrs: { style: "margin-top:8px" } },
            el("span", { text: "部门预算：" + Object.keys(b.departments).map(function (id) {
              return (nameOfDept(id) === id ? id : nameOfDept(id)) + " ¥" + Number(b.departments[id] || 0).toFixed(2);
            }).join(" · ") }))
          : null)
        : el("div", { class: "card-meta", attrs: { style: "margin-top:8px" }, text: s.emergency.loading ? "正在读取预算…" : "暂无预算设置" }));
    return card;
  }

  async function saveBudget() {
    if (s.savingBudget) return;
    const daily = Number(s.budgetForm.daily);
    const monthly = Number(s.budgetForm.monthly);
    if (!isFinite(daily) || !isFinite(monthly) || daily < 0 || monthly < 0) {
      toast("预算必须是非负数字", "warn");
      return;
    }
    s.savingBudget = true;
    s.actionError = null;
    render();
    try {
      const res = await ctx.api.saveBudgets({ daily_cny: daily, monthly_cny: monthly });
      s.budgets = res || s.budgets;
      s.savingBudget = false;
      toast("预算已保存（写审计）", "ok");
      render();
    } catch (e) {
      s.savingBudget = false;
      fail(e, "预算更新需要会话 + CSRF；失败不会在本地假装成功。");
    }
  }

  function filterCard() {
    const statusSel = el("select", { class: "select", attrs: { "aria-label": "按状态过滤" } });
    statusSel.appendChild(el("option", { attrs: { value: "" }, text: "全部状态" }));
    STATUS_KEYS.forEach(function (k) { statusSel.appendChild(el("option", { attrs: { value: k }, text: AGENT_STATUS[k][0] })); });
    statusSel.value = s.filters.status;
    statusSel.addEventListener("change", function () { s.filters.status = statusSel.value; s.selectedId = ""; loadAgents(); });

    const kindSel = el("select", { class: "select", attrs: { "aria-label": "按类型过滤" } });
    KINDS.forEach(function (k) { kindSel.appendChild(el("option", { attrs: { value: k[0] }, text: k[1] })); });
    kindSel.value = s.filters.kind;
    kindSel.addEventListener("change", function () { s.filters.kind = kindSel.value; s.selectedId = ""; loadAgents(); });

    const deptSel = el("select", { class: "select", attrs: { "aria-label": "按部门过滤" } });
    deptSel.appendChild(el("option", { attrs: { value: "" }, text: "全部部门" }));
    (state.departments || []).forEach(function (d) { deptSel.appendChild(el("option", { attrs: { value: d.id }, text: d.name })); });
    deptSel.value = s.filters.department_id;
    deptSel.addEventListener("change", function () { s.filters.department_id = deptSel.value; s.selectedId = ""; loadAgents(); });

    return el("div", { class: "filters", attrs: { style: "margin-top:14px" } }, statusSel, kindSel, deptSel);
  }

  function taskTitle(id) {
    if (!id) return "";
    const hit = (s.tasks || []).filter(function (t) { return t.id === id; })[0];
    return hit ? hit.title : id;
  }

  function agentCard(a) {
    const dept = (state.departments || []).filter(function (d) { return d.id === a.department_id; })[0];
    const paused = a.status === "paused" || a.status === "budget_blocked" || a.status === "failed" || a.status === "offline";
    const busy = s.busy === "agent:" + a.id;
    const mission = roleMission(a.role_card);
    return el("div", { class: "card", attrs: { style: "background:var(--panel-2)" } },
      el("div", { class: "card-title" },
        el("span", { text: a.name || a.id }),
        badge(KIND_LABEL[a.kind] || a.kind || "Agent", ""),
        statusBadge(a.status, AGENT_STATUS),
        el("span", { class: "grow" }),
        el("span", { attrs: { style: "font-family:var(--mono);font-size:10.5px;color:var(--faint)" }, text: a.id })),
      el("div", { class: "card-meta", attrs: { style: "margin-top:4px" },
        text: (dept ? dept.name : nameOfDept(a.department_id)) + (a.perspective ? " · " + a.perspective : "") + (a.model ? " · " + a.model : "") }),
      mission ? el("div", { class: "card-meta", attrs: { style: "margin-top:4px" }, text: truncate(mission, 90) }) : null,
      el("div", { class: "card-meta", attrs: { style: "margin-top:4px" } },
        a.current_task_id ? el("span", { text: "当前任务：" + taskTitle(a.current_task_id) }) : el("span", { text: "当前任务：无" }),
        a.budget_limit ? el("span", { text: " · 预算上限 ¥" + Number(a.budget_limit).toFixed(2) }) : null,
        a.updated_at ? el("span", { attrs: { title: fmtDateTime(a.updated_at) }, text: " · 更新 " + relTime(a.updated_at) }) : null),
      el("div", { class: "row", attrs: { style: "margin-top:8px;flex-wrap:wrap" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: busy || paused }, on: { click: function () { agentAction(a, "pause"); } } }, "暂停"),
        el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: busy || !paused }, on: { click: function () { agentAction(a, "resume"); } } }, "恢复"),
        el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: busy }, on: { click: function () { stopAgent(a); } } }, "停止当前 turn"),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { ctx.navigate("#/agents?id=" + encodeURIComponent(a.id)); } } }, "详情 / 角色卡")));
  }

  function detailCard(a) {
    const dept = (state.departments || []).filter(function (d) { return d.id === a.department_id; })[0];
    const rc = parseJSON(a.role_card);
    const card = el("div", { class: "card", attrs: { style: "margin-top:18px" } },
      el("div", { class: "card-title" },
        el("span", { text: "角色卡 · " + (a.name || a.id) }),
        statusBadge(a.status, AGENT_STATUS),
        el("span", { class: "grow" }),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { ctx.navigate("#/agents"); } } }, "关闭详情")),
      el("div", { class: "card-meta", attrs: { style: "margin-top:4px" },
        text: "部门：" + (dept ? dept.name : nameOfDept(a.department_id)) + " · 视角：" + (a.perspective || "—") + " · 模型：" + (a.model || "—") }),
      el("div", { class: "card-meta", attrs: { style: "margin-top:2px" },
        text: "预算上限：¥" + Number(a.budget_limit || 0).toFixed(2) + " · 当前任务：" + (a.current_task_id ? taskTitle(a.current_task_id) : "无") + " · 更新：" + (a.updated_at ? fmtDateTime(a.updated_at) : "—") }),
      el("div", { attrs: { style: "margin-top:10px" } },
        el("div", { class: "card-meta", text: "role_card" }),
        el("pre", { attrs: { style: "max-height:240px;overflow:auto;background:#0a0f14;border:1px solid var(--line);border-radius:8px;padding:8px 10px;font-family:var(--mono);font-size:12px;white-space:pre-wrap" },
          text: typeof a.role_card === "string" ? a.role_card : JSON.stringify(rc == null ? {} : rc, null, 2) })),
      el("div", { class: "row", attrs: { style: "margin-top:10px;flex-wrap:wrap" } },
        el("span", { class: "card-meta", text: "高风险配置（model / budget / role_card）需 G2 审批；当前 API 不提供 PATCH，前端显示待审批，不伪造成功。" }),
        badge("待审批 / pending", "warn")),
      el("div", { attrs: { style: "margin-top:12px;border-top:1px solid var(--line);padding-top:10px" } },
        el("div", { class: "card-meta", text: "最近行为（审计）" }),
        s.recentError ? notice("审计读取失败：" + (s.recentError.message || String(s.recentError)), "warn") : null,
        s.recent.length
          ? el("div", { class: "stack", attrs: { style: "margin-top:6px" } }, s.recent.map(function (r) {
            return el("div", { class: "row", attrs: { style: "gap:8px;flex-wrap:wrap" } },
              badge(r.result === "ok" ? "成功" : (r.result || "—"), r.result === "ok" ? "ok" : "bad"),
              el("span", { attrs: { style: "font-family:var(--mono);font-size:11px" }, text: r.action || "—" }),
              el("span", { class: "card-meta", text: (r.object_type || "") + "/" + (r.object_id || "") }),
              el("span", { class: "card-meta", attrs: { title: fmtDateTime(r.created_at) }, text: relTime(r.created_at) }));
          }))
          : el("div", { class: "card-meta", attrs: { style: "margin-top:4px" }, text: s.recentError ? "—" : "暂无最近行为" })),
      el("div", { class: "row", attrs: { style: "margin-top:10px;flex-wrap:wrap" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: s.busy === "agent:" + a.id || a.status === "paused" || a.status === "budget_blocked" }, on: { click: function () { agentAction(a, "pause"); } } }, "暂停"),
        el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: s.busy === "agent:" + a.id || (a.status !== "paused" && a.status !== "budget_blocked") }, on: { click: function () { agentAction(a, "resume"); } } }, "恢复"),
        el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: s.busy === "agent:" + a.id }, on: { click: function () { stopAgent(a); } } }, "停止当前 turn")));
    return card;
  }

  function summary() {
    const counts = {};
    s.items.forEach(function (a) { counts[a.status] = (counts[a.status] || 0) + 1; });
    const row = el("div", { class: "filters", attrs: { style: "margin-top:12px" } });
    ["running", "idle", "waiting", "waiting_review", "budget_blocked", "paused", "failed", "offline"].forEach(function (k) {
      if (counts[k]) row.appendChild(badge((AGENT_STATUS[k] || [k])[0] + " " + counts[k], AGENT_STATUS[k][1]));
    });
    if (!row.childNodes.length) row.appendChild(badge("0 名 Agent", "warn"));
    return row;
  }

  async function agentAction(a, action) {
    if (s.busy) return;
    if (action === "pause" && (a.status === "paused" || a.status === "budget_blocked")) return;
    s.busy = "agent:" + a.id;
    s.actionError = null;
    render();
    try {
      const updated = await ctx.api.agentAction(a.id, action, { reason: "用户在 Agent 管理页操作" });
      s.busy = "";
      if (updated && updated.id) {
        s.items = s.items.map(function (x) { return x.id === updated.id ? updated : x; });
      }
      toast("Agent " + (a.name || a.id) + "：" + ({ pause: "已暂停", resume: "已恢复", stop: "已请求停止当前 turn" }[action] || action), "ok");
      loadAgents();
      if (s.selectedId === a.id) loadDetail(a.id);
    } catch (e) {
      fail(e, "Agent 生命周期由核心执行；失败会显示真实错误，不会假装成功。");
    }
  }

  async function stopAgent(a) {
    const ok = await confirmModal({
      title: "停止当前 turn",
      body: "只会停止当前 turn，不会删除 Agent；已有副作用的结果需要人工核对。操作会写审计。",
      confirmText: "停止", danger: true
    });
    if (!ok) return;
    agentAction(a, "stop");
  }

  function renderPage() {
    host.appendChild(pageHead());
    if (ctx.demo) host.appendChild(notice("演示数据：Agent/预算/紧急停止来自内存 mock；真实操作会调用核心并写审计。", "warn"));
    if (s.actionError) host.appendChild(actionCard());
    host.appendChild(emergencyCard());
    host.appendChild(budgetCard());
    host.appendChild(filterCard());

    if (s.loading) {
      host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载 Agent…" })));
      return;
    }
    if (s.error) { host.appendChild(errorCard("Agent 加载失败", s.error, loadAgents)); return; }
    host.appendChild(summary());
    if (!s.items.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "org", title: "当前过滤条件下没有 Agent",
        desc: "Agent 由部门蓝图/G1 后 provisioning 创建；可清空状态/类型/部门过滤。",
        later: "高风险配置需 G2 审批"
      })));
      return;
    }
    const grid = el("div", { class: "grid-2", attrs: { style: "margin-top:14px" } });
    s.items.forEach(function (a) { grid.appendChild(agentCard(a)); });
    host.appendChild(grid);

    if (s.selectedId) {
      if (s.detailLoading) {
        host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在读取角色卡与最近行为…" })));
      } else if (s.detailError) {
        host.appendChild(errorCard("Agent 详情加载失败", s.detailError, function () { loadDetail(s.selectedId); }));
      } else if (s.detail) {
        host.appendChild(detailCard(s.detail));
      }
    }
  }

  loadAll();
  return {
    destroy: function () { disposed = true; }
  };
}
// 审批中心：真实 API —— G1/G2/G3/不可逆；TTL、拒绝必填理由、错误不打码。
import { el, clear, icon, badge, notice, emptyState, toast, fmtDateTime, relTime } from "../lib/dom.js";
import { state, emit } from "../lib/store.js";
import { statusBadge, APPROVAL_STATUS, APPROVAL_KIND } from "../lib/ui.js";

const STATUS_TABS = [["pending", "待处理"], ["handled", "已处理"], ["all", "全部"]];
const KINDS = [["", "全部类型"], ["G1", "G1 组队"], ["G2", "G2 高风险"], ["G3", "G3 验收"], ["irreversible", "不可逆"]];

function payloadOf(a) {
  if (!a || !a.payload) return {};
  try {
    const p = JSON.parse(a.payload);
    return p && typeof p === "object" ? p : {};
  } catch (e) { return {}; }
}

export function mount(ctx) {
  const s = {
    items: [], loading: true, error: null, tab: "pending", kind: "",
    busy: "", actionError: null, actionHint: "", rejectFor: "", reason: ""
  };
  let disposed = false;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "审批中心"),
    el("div", { class: "topbar-sub" },
      el("span", { text: "G1 组队 · G2 高风险 · G3 验收 · 不可逆动作；Agent 不能自批" }),
      ctx.demo ? badge("演示数据", "warn") : null)));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: load } },
      icon("retry", 14), el("span", { text: "刷新" }))));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  async function load() {
    s.loading = true; s.error = null; render();
    try {
      const params = {};
      if (s.tab === "pending") params.status = "pending";
      if (s.kind) params.kind = s.kind;
      const res = await ctx.api.approvals(params);
      if (disposed) return;
      s.items = (res && res.items) || [];
      state.approvals = res || { items: s.items };
      s.loading = false;
      render();
      emit("agents:changed", {});
    } catch (e) {
      if (disposed) return;
      s.loading = false; s.error = e; render();
    }
  }

  function render() {
    clear(host);
    const pending = s.items.filter(function (a) { return a.status === "pending"; }).length;

    host.appendChild(el("div", { class: "page-head" },
      el("div", null, el("h2", { text: "审批中心" }),
        el("p", { text: "拒绝必须填理由；超时按 TTL 失效；批准会写审计与事件。" })),
      el("span", { class: "grow" }),
      pending ? badge(pending + " 项待处理", "warn") : badge("无待处理", "ok")));

    if (ctx.demo) host.appendChild(notice("演示模式（R4 交付）：审批动作会调用核心接口；演示模式返回明确错误，不假装成功。", "warn"));

    const tabs = el("div", { class: "tabs", attrs: { style: "margin-top:14px" } });
    STATUS_TABS.forEach(function (t) {
      tabs.appendChild(el("button", {
        class: "tab" + (s.tab === t[0] ? " active" : ""), attrs: { type: "button" },
        on: { click: function () { s.tab = t[0]; load(); } }
      }, t[1]));
    });
    host.appendChild(tabs);

    const filters = el("div", { class: "filters" });
    const kindSel = el("select", { class: "select", attrs: { "aria-label": "按类型过滤" } });
    KINDS.forEach(function (k) { kindSel.appendChild(el("option", { attrs: { value: k[0] }, text: k[1] })); });
    kindSel.value = s.kind;
    kindSel.addEventListener("change", function () { s.kind = kindSel.value; load(); });
    filters.appendChild(kindSel);
    host.appendChild(filters);

    if (s.actionError) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:12px;border-color:rgba(251,113,133,.4)" } },
        el("div", { class: "card-title" }, el("span", { text: "操作失败" }), badge(s.actionError.code || "error", "bad")),
        el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.actionError.message || String(s.actionError) }),
        s.actionHint ? el("div", { class: "help", attrs: { style: "margin-top:6px" }, text: s.actionHint }) : null,
        el("div", { class: "row", attrs: { style: "margin-top:10px" } },
          el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.actionError = null; render(); } } }, "关闭"))));
    }

    if (s.loading) { host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载审批…" }))); return; }
    if (s.error) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:14px" } },
        el("div", { class: "card-title", text: "审批加载失败" }),
        el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.error.message || String(s.error) }),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } }, el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: load } }, "重试"))));
      return;
    }
    if (!s.items.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "approval",
        title: s.tab === "pending" ? "没有待处理审批" : "没有记录",
        desc: "部门蓝图（G1）、高风险变更（G2）、验收（G3）与不可逆动作会出现在这里；Agent 不能自批。",
        later: "TTL / 双钥匙为 R8"
      })));
      return;
    }

    const stack = el("div", { class: "stack", attrs: { style: "margin-top:14px" } });
    s.items.forEach(function (a) { stack.appendChild(card(a)); });
    host.appendChild(stack);
  }

  function card(a) {
    const pending = a.status === "pending";
    const payload = payloadOf(a);
    const target = payload.blueprint_id || payload.task_id || payload.agent_id || "";
    const rejectOpen = s.rejectFor === a.id;

    const body = el("div", { class: "card", dataset: { id: a.id } },
      el("div", { class: "card-title" },
        badge(APPROVAL_KIND[a.kind] || a.kind || "审批", "accent"),
        el("span", { text: a.title || "（无标题）" }),
        statusBadge(a.status, APPROVAL_STATUS),
        el("span", { class: "grow" }),
        el("span", { style: "font-family:var(--mono);font-size:10.5px;color:var(--faint)", text: a.id })),
      el("div", { class: "card-meta", attrs: { style: "margin-top:6px" } },
        el("span", { text: "风险：" + (a.risk || "—") }),
        el("span", { text: " · 范围：" + (a.scope || "一次") }),
        el("span", { text: " · 申请人：" + (a.requested_by || "—") })),
      el("div", { class: "card-meta", attrs: { style: "margin-top:2px" } },
        el("span", { text: "创建：" + relTime(a.created_at) }),
        a.expires_at ? el("span", { title: fmtDateTime(a.expires_at), text: " · 到期：" + relTime(a.expires_at) }) : el("span", { text: " · 无 TTL" }),
        target ? el("span", { text: " · 对象：" + String(target) }) : null),
      Object.keys(payload).length
        ? el("div", { class: "card-meta", attrs: { style: "margin-top:4px" } },
          el("span", { text: "payload：" + JSON.stringify(payload).slice(0, 180) }))
        : null);

    if (pending) {
      const row = el("div", { class: "row", attrs: { style: "margin-top:10px" } });
      row.appendChild(el("button", {
        class: "btn btn-sm btn-primary", attrs: { type: "button", disabled: s.busy === "decide:" + a.id || ctx.demo },
        on: { click: function () { decide(a, "approved", ""); } }
      }, "批准"));
      row.appendChild(el("button", {
        class: "btn btn-sm btn-danger", attrs: { type: "button", disabled: s.busy === "decide:" + a.id || ctx.demo },
        on: { click: function () { s.rejectFor = rejectOpen ? "" : a.id; s.reason = ""; render(); } }
      }, "拒绝"));
      if (a.kind === "G3") row.appendChild(el("span", { class: "card-meta", text: "G3 批准后任务可进入 done" }));
      body.appendChild(row);

      if (rejectOpen) {
        const reason = el("input", { class: "input", attrs: { type: "text", placeholder: "拒绝理由（必填，会写审计）", style: "margin-top:8px" } });
        reason.value = s.reason;
        reason.addEventListener("input", function () { s.reason = reason.value; });
        body.appendChild(reason);
        body.appendChild(el("div", { class: "row", attrs: { style: "margin-top:8px" } },
          el("button", {
            class: "btn btn-sm btn-danger", attrs: { type: "button", disabled: s.busy === "decide:" + a.id },
            on: { click: function () { decide(a, "rejected", reason.value); } }
          }, "确认拒绝"),
          el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.rejectFor = ""; render(); } } }, "取消")));
      }
    } else {
      body.appendChild(el("div", { class: "card-meta", attrs: { style: "margin-top:8px" } },
        el("span", { text: "决定：" + (a.decided_by || "—") }),
        a.decision_reason ? el("span", { text: " · 理由：" + a.decision_reason }) : null,
        a.decided_at ? el("span", { text: " · " + relTime(a.decided_at) }) : null));
    }
    return body;
  }

  async function decide(a, decision, reason) {
    if (s.busy) return;
    if (decision === "rejected" && !String(reason || "").trim()) { toast("拒绝审批必须填理由", "warn"); return; }
    s.busy = "decide:" + a.id;
    s.actionError = null;
    render();
    try {
      await ctx.api.decideApproval(a.id, { decision: decision, reason: String(reason || "").trim() });
      s.busy = ""; s.rejectFor = ""; s.reason = "";
      toast(decision === "approved" ? "已批准" : "已拒绝", "ok");
      load();
    } catch (e) {
      s.busy = "";
      s.actionError = e;
      s.actionHint = "审批由核心/存储校验：G3 走 DecideG3；拒绝必须填理由；TTL 过期会返回冲突。";
      render();
    }
  }

  load();
  return { destroy: function () { disposed = true; } };
}

// 审批中心（R1 骨架）。R4 交付：G1/G2/G3 决策、拒绝理由、TTL 与双钥匙。
import { el, clear, icon, badge, emptyState, notice, confirmModal, fmtDateTime, relTime } from "../lib/dom.js";
import { state, emit } from "../lib/store.js";
import { statusBadge, APPROVAL_STATUS, APPROVAL_KIND } from "../lib/ui.js";

const TABS = [
  ["pending", "待处理"],
  ["handled", "已处理"],
  ["all", "全部"]
];

export function mount(ctx) {
  let disposed = false;
  let items = [];
  let loading = true;
  let error = null;
  let tab = "pending";

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "审批中心"),
    el("div", { class: "topbar-sub" }, el("span", { text: "G1 组队 · G2 高风险 · G3 验收 · 不可逆动作" }))
  ));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", {
      class: "btn btn-sm", attrs: { type: "button" },
      on: { click: function () { load(); } }
    }, icon("retry", 14), el("span", { text: "刷新" }))
  ));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  function load() {
    loading = true;
    error = null;
    render();
    const params = {};
    if (tab === "pending") params.status = "pending";
    ctx.api.approvals(params).then(function (res) {
      if (disposed) return;
      items = (res && res.items) || [];
      state.approvals = res || { items: items };
      loading = false;
      render();
      emitNav();
    }).catch(function (e) {
      if (disposed) return;
      loading = false;
      error = e;
      render();
    });
  }

  function emitNav() { emit("agents:changed", {}); }

  function filtered() {
    if (tab === "handled") return items.filter(function (a) { return a.status !== "pending"; });
    return items;
  }

  function render() {
    clear(host);
    const pendingCount = items.filter(function (a) { return a.status === "pending"; }).length;

    host.appendChild(el("div", { class: "page-head" },
      el("div", null,
        el("h2", { text: "审批中心" }),
        el("p", { text: "Agent 不能自批；拒绝需理由；超时按 TTL 过期。" })
      ),
      el("span", { class: "grow" }),
      pendingCount ? badge(pendingCount + " 项待处理", "warn") : badge("无待处理", "ok")
    ));

    host.appendChild(ctx.demo
      ? notice("演示数据（R4 交付）：审批动作会调用核心接口；演示模式下会返回「不支持」而不是假装成功。", "warn")
      : notice("审批接口就绪后显示真实待办；当前为空态（R4 交付）。"));

    const tabs = el("div", { class: "tabs", attrs: { style: "margin-top:14px" } });
    TABS.forEach(function (t) {
      tabs.appendChild(el("button", {
        class: "tab" + (tab === t[0] ? " active" : ""),
        attrs: { type: "button" },
        on: { click: function () { tab = t[0]; load(); } }
      }, t[1]));
    });
    host.appendChild(tabs);

    if (loading) {
      host.appendChild(el("div", { class: "empty" }, el("span", { class: "spinner" }), el("p", { text: "正在加载审批…" })));
      return;
    }
    if (error) {
      host.appendChild(el("div", { class: "card" },
        el("div", { class: "card-title", text: "审批加载失败" }),
        notice(error.message || String(error), "bad"),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } },
          el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: load } }, "重试"))
      ));
      return;
    }

    const list = filtered();
    if (!list.length) {
      host.appendChild(emptyState({
        icon: "approval",
        title: tab === "pending" ? "没有待处理审批" : "没有记录",
        desc: "部门蓝图（G1）、高风险变更（G2）、验收（G3）会出现在这里。",
        later: "审批 TTL、危险动作双钥匙为 R8"
      }));
      return;
    }

    const stack = el("div", { class: "stack" });
    list.forEach(function (a) {
      stack.appendChild(card(a));
    });
    host.appendChild(stack);
  }

  function card(a) {
    const pending = a.status === "pending";
    const approve = el("button", {
      class: "btn btn-sm btn-primary", attrs: { type: "button", disabled: !pending },
      on: { click: function () { decide(a, "approved"); } }
    }, "批准");
    const reject = el("button", {
      class: "btn btn-sm btn-danger", attrs: { type: "button", disabled: !pending },
      on: { click: function () { decide(a, "rejected"); } }
    }, "拒绝");

    return el("div", { class: "card", dataset: { id: a.id } },
      el("div", { class: "card-title" },
        badge(APPROVAL_KIND[a.kind] || a.kind, "accent"),
        el("span", { text: a.title || "（无标题）" }),
        statusBadge(a.status, APPROVAL_STATUS)
      ),
      el("div", { class: "card-meta" },
        el("span", { text: "风险：" + (a.risk || "—") }),
        el("span", { text: " · 范围：" + (a.scope || "一次") }),
        el("span", { text: " · 申请人：" + (a.requested_by || "—") })
      ),
      el("div", { class: "card-meta", attrs: { title: fmtDateTime(a.created_at) } },
        el("span", { text: "创建：" + relTime(a.created_at) }),
        a.expires_at ? el("span", { text: " · 到期：" + relTime(a.expires_at) }) : null,
        el("span", { class: "mono", attrs: { style: "font-family:var(--mono);font-size:11px" }, text: " · " + a.id })
      ),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } }, approve, reject)
    );
  }

  async function decide(a, decision) {
    let reason = "";
    if (decision === "rejected") {
      const ok = await confirmModal({
        title: "拒绝审批",
        body: "拒绝需要理由（审计会记录）。当前骨架先记录默认理由，R4 会提供理由输入框。",
        confirmText: "确认拒绝",
        danger: true
      });
      if (!ok) return;
      reason = "前端骨架默认理由：需补充（R4 改为必填输入）";
    }
    try {
      await ctx.api.decideApproval(a.id, { decision: decision, reason: reason });
      ctx.toast(decision === "approved" ? "已批准" : "已拒绝", "ok");
      load();
    } catch (e) {
      ctx.toast("审批失败：" + e.message, "bad");
    }
  }

  load();
  return { destroy: function () { disposed = true; } };
}
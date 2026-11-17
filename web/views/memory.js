// 记忆检查器（R1 骨架）。R7 交付：候选审核、编辑、钉住、删除、手册 diff/回滚。
import { el, clear, icon, badge, emptyState, notice, debounce, relTime } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { statusBadge, MEMORY_STATUS, MEMORY_SCOPE } from "../lib/ui.js";

const SCOPES = [["", "全部范围"], ["assistant", "助理"], ["department", "部门"], ["employee", "员工"]];
const STATUSES = [["", "全部状态"], ["candidate", "待审"], ["active", "生效"], ["rejected", "已拒"], ["deleted", "已删除"]];

export function mount(ctx, route) {
  let disposed = false;
  let items = [];
  let loading = true;
  let error = null;
  let scope = "";
  let status = "";
  let q = "";
  const targetId = route && route.query ? route.query.id : "";

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "记忆"),
    el("div", { class: "topbar-sub" }, el("span", { text: "四层记忆：短期 / 触发 / 档案 / 自适应提示词" }))
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

  const scopeSel = el("select", { class: "select", attrs: { "aria-label": "按范围过滤" } });
  SCOPES.forEach(function (s) { scopeSel.appendChild(el("option", { attrs: { value: s[0] }, text: s[1] })); });
  scopeSel.addEventListener("change", function () { scope = scopeSel.value; load(); });

  const statusSel = el("select", { class: "select", attrs: { "aria-label": "按状态过滤" } });
  STATUSES.forEach(function (s) { statusSel.appendChild(el("option", { attrs: { value: s[0] }, text: s[1] })); });
  statusSel.addEventListener("change", function () { status = statusSel.value; load(); });

  const searchInput = el("input", { class: "input", attrs: { type: "search", placeholder: "搜索记忆内容…", style: "max-width:220px" } });
  const doSearch = debounce(function () { q = searchInput.value.trim(); load(); }, 320);
  searchInput.addEventListener("input", doSearch);

  function load() {
    loading = true;
    error = null;
    render();
    const params = {};
    if (scope) params.scope = scope;
    if (status) params.status = status;
    if (q) params.q = q;
    ctx.api.memories(params).then(function (res) {
      if (disposed) return;
      items = (res && res.items) || [];
      loading = false;
      render();
    }).catch(function (e) {
      if (disposed) return;
      loading = false;
      error = e;
      render();
    });
  }

  function render() {
    clear(host);
    const pending = items.filter(function (m) { return m.status === "candidate"; }).length;
    host.appendChild(el("div", { class: "page-head" },
      el("div", null,
        el("h2", { text: "记忆检查器" }),
        el("p", { text: "候选 → 审核 → 档案；删除写墓碑并停止注入。" })
      ),
      el("span", { class: "grow" }),
      pending ? badge(pending + " 条待审", "warn") : null
    ));

    host.appendChild(ctx.demo
      ? notice("演示数据（R7 交付）：审核/编辑/删除按钮会调用核心接口；演示模式返回「不支持」。", "warn")
      : notice("记忆接口就绪后显示真实记忆；当前为空态（R7 交付）。"));

    host.appendChild(el("div", { class: "filters", attrs: { style: "margin-top:12px" } },
      scopeSel, statusSel, searchInput,
      el("span", { class: "search-stat", text: "按可见性：agent-private / team / org / human-only" })
    ));

    if (loading) {
      host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载记忆…" })));
      return;
    }
    if (error) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:14px" } },
        el("div", { class: "card-title", text: "记忆加载失败" }),
        notice(error.message || String(error), "bad"),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } },
          el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: load } }, "重试"))
      ));
      return;
    }
    if (!items.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "memory",
        title: "还没有记忆",
        desc: "Agent 在任务关闭、返工、裁决、人类拍板后提交候选记忆；人类审核后才进入档案。",
        later: "R7 交付候选审核与注入快照"
      })));
      return;
    }

    const table = el("table", { class: "table", attrs: { style: "margin-top:14px" } },
      el("thead", null, el("tr", null,
        ["记忆", "范围", "可见性", "状态", "版本", "更新"].map(function (h) { return el("th", { text: h }); })
      ))
    );
    const tbody = el("tbody");
    items.forEach(function (m) {
      const isTarget = targetId && (m.id === targetId);
      const tr = el("tr", { attrs: { style: isTarget ? "background:var(--accent-soft)" : "" } },
        el("td", null,
          el("div", { attrs: { style: "font-weight:600" }, text: m.summary || m.content || "(无内容)" }),
          m.content && m.summary ? el("div", { class: "card-meta", text: m.content }) : null,
          el("div", { class: "row", attrs: { style: "margin-top:6px" } },
            el("button", {
              class: "btn btn-sm", attrs: { type: "button", title: "R7 交付：审核候选" },
              on: { click: function () { ctx.toast("记忆审核/编辑/删除由 /api/memories 提供，R7 交付", "warn"); } }
            }, "审核"),
            m.status === "candidate" ? el("button", {
              class: "btn btn-sm", attrs: { type: "button", title: "R7 交付" },
              on: { click: function () { ctx.toast("批准候选记忆需 R7 接口", "warn"); } }
            }, "批准") : null
          )
        ),
        el("td", { text: MEMORY_SCOPE[m.scope] || m.scope || "—" }),
        el("td", { text: m.visibility || "—" }),
        el("td", null, statusBadge(m.status, MEMORY_STATUS)),
        el("td", { text: "v" + (m.version || 1) }),
        el("td", { attrs: { title: m.created_at || "" }, text: relTime(m.updated_at || m.created_at) })
      );
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    host.appendChild(table);
  }

  load();
  return { destroy: function () { disposed = true; } };
}

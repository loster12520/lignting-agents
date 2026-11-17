// 审计（R1 骨架）。R8 交付：hash chain 校验、导出、敏感搜索审计。
import { el, clear, icon, badge, emptyState, notice, fmtDateTime, debounce } from "../lib/dom.js";

const RESULT_BADGE = { ok: ["成功", "ok"], failed: ["失败", "bad"], denied: ["拒绝", "warn"] };

export function mount(ctx) {
  let disposed = false;
  let items = [];
  let loading = true;
  let error = null;
  let action = "";

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "审计"),
    el("div", { class: "topbar-sub" }, el("span", { text: "append-only · 哈希链 · 不可在 UI 删除" }))
  ));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", {
      class: "btn btn-sm", attrs: { type: "button", title: "R8 交付：导出审计" },
      on: { click: function () { ctx.toast("审计导出需 R8 的 /api/admin/export", "warn"); } }
    }, "导出"),
    el("button", {
      class: "btn btn-sm", attrs: { type: "button" },
      on: { click: function () { load(); } }
    }, icon("retry", 14), el("span", { text: "刷新" }))
  ));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  const actionInput = el("input", { class: "input", attrs: { type: "search", placeholder: "按动作过滤，如 write_event / approval / message.delete", style: "max-width:340px" } });
  const doSearch = debounce(function () { action = actionInput.value.trim(); load(); }, 320);
  actionInput.addEventListener("input", doSearch);

  function load() {
    loading = true;
    error = null;
    render();
    const params = {};
    if (action) params.action = action;
    ctx.api.audit(params).then(function (res) {
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
    host.appendChild(el("div", { class: "page-head" },
      el("div", null,
        el("h2", { text: "审计日志" }),
        el("p", { text: "谁（人类 / 哪个 Agent）在什么时候对什么做了什么；只能 INSERT。" })
      ),
      el("span", { class: "grow" }),
      actionInput
    ));

    host.appendChild(ctx.demo
      ? notice("演示数据（R8 交付）：哈希链字段为占位；真实校验在 /api/audit 与备份流程里。", "warn")
      : notice("审计接口就绪后显示真实记录；当前为空态（R8 交付）。"));

    if (loading) {
      host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载审计…" })));
      return;
    }
    if (error) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:14px" } },
        el("div", { class: "card-title", text: "审计加载失败" }),
        notice(error.message || String(error), "bad"),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } },
          el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: load } }, "重试"))
      ));
      return;
    }
    if (!items.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "audit",
        title: "还没有审计记录",
        desc: "登录、消息发送/删除、工具调用、审批、写文件、配置变更都会留下审计。",
        later: "R8 交付校验与导出"
      })));
      return;
    }

    const table = el("table", { class: "table", attrs: { style: "margin-top:14px" } },
      el("thead", null, el("tr", null,
        ["#", "时间", "动作", "对象", "执行者", "结果", "hash"].map(function (h) { return el("th", { text: h }); })
      ))
    );
    const tbody = el("tbody");
    items.forEach(function (a) {
      const rb = RESULT_BADGE[a.result] || [a.result || "—", ""];
      tbody.appendChild(el("tr", null,
        el("td", { class: "mono", text: String(a.seq || "") }),
        el("td", { attrs: { title: fmtDateTime(a.created_at) }, text: fmtDateTime(a.created_at) }),
        el("td", { class: "mono", text: a.action || "—" }),
        el("td", null,
          el("div", { text: a.object_type || "—" }),
          el("div", { class: "card-meta mono", attrs: { style: "font-family:var(--mono);font-size:11px" }, text: a.object_id || "" })
        ),
        el("td", null,
          el("div", { text: a.actor_id || "—" }),
          el("div", { class: "card-meta", text: a.actor_type || "" })
        ),
        el("td", null, badge(rb[0], rb[1])),
        el("td", { class: "mono", attrs: { style: "max-width:180px;overflow:hidden;text-overflow:ellipsis", title: a.hash || "" }, text: a.hash || "—" })
      ));
    });
    table.appendChild(tbody);
    host.appendChild(table);
    host.appendChild(el("p", { class: "card-meta", attrs: { style: "margin-top:10px" },
      text: "hash = sha256(prev_hash + canonical(payload))；链校验与导出为 R8。" }));
  }

  load();
  return { destroy: function () { disposed = true; } };
}

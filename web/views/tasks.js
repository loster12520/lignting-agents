// 任务板（R1 只读列表）。R4 交付：看板/详情/时间线/状态转换/验收核对表。
import { el, clear, icon, badge, emptyState, notice, fmtDateTime, relTime } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { statusBadge, TASK_STATUS } from "../lib/ui.js";

const STATUS_OPTIONS = [
  ["", "全部状态"], ["proposed", "提议"], ["ready", "待开始"], ["discussing", "讨论中"],
  ["in_progress", "进行中"], ["blocked", "阻塞"], ["in_review", "评审中"],
  ["changes_requested", "待返工"], ["approved", "已通过"], ["done", "完成"]
];

export function mount(ctx, route) {
  let disposed = false;
  let items = [];
  let loading = true;
  let error = null;
  let status = "";

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "任务"),
    el("div", { class: "topbar-sub" }, el("span", { text: "状态机由核心写入；平台只展示投影" }))
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

  const sel = el("select", { class: "select", attrs: { "aria-label": "按状态过滤" } });
  STATUS_OPTIONS.forEach(function (o) {
    sel.appendChild(el("option", { attrs: { value: o[0] }, text: o[1] }));
  });
  sel.addEventListener("change", function () { status = sel.value; load(); });

  function load() {
    loading = true;
    error = null;
    render();
    const params = {};
    if (status) params.status = status;
    ctx.api.tasks(params).then(function (res) {
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
        el("h2", { text: "任务板" }),
        el("p", { text: "按部门/状态/负责人/截止过滤；状态转换受状态机与权限约束。" })
      ),
      el("span", { class: "grow" }),
      el("div", { class: "row" }, sel,
        el("button", {
          class: "btn btn-primary btn-sm", attrs: { type: "button", title: "R4 交付：创建任务（DoR 校验）" },
          on: { click: function () { ctx.toast("创建任务需 R4 的 DoR 校验与状态机接口", "warn"); } }
        }, icon("plus", 14), el("span", { text: "新建任务" }))
      )
    ));

    host.appendChild(ctx.demo
      ? notice("演示数据（R4 交付）：任务来自前端内存；真实任务板需 /api/tasks 与状态机投影。", "warn")
      : notice("任务列表接口就绪后显示真实数据；当前为只读投影空态（R4 交付）。"));

    if (loading) {
      host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载任务…" })));
      return;
    }
    if (error) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:14px" } },
        el("div", { class: "card-title", text: "任务加载失败" }),
        notice(error.message || String(error), "bad"),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } },
          el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: load } }, "重试"))
      ));
      return;
    }
    if (!items.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "task",
        title: "还没有任务",
        desc: "任务由助理或部门主管按结构化动作创建（create_task），需要部门、DoR 字段与分工视角。",
        later: "R4 交付任务详情/时间线/验收核对表"
      })));
      return;
    }

    const table = el("table", { class: "table", attrs: { style: "margin-top:14px" } },
      el("thead", null, el("tr", null,
        ["任务", "状态", "部门", "负责人", "视角", "优先级", "截止"].map(function (h) {
          return el("th", { text: h });
        })
      ))
    );
    const tbody = el("tbody");
    const targetId = route && route.query ? route.query.id : "";
    items.forEach(function (t) {
      const dept = state.departments.filter(function (d) { return d.id === t.department_id; })[0];
      const tr = el("tr", {
        attrs: { style: "cursor:pointer" + (targetId === t.id ? ";background:var(--accent-soft)" : "") },
        on: {
          click: function () { ctx.toast("任务详情（描述/验收标准/依赖/时间线）R4 交付：" + t.title, "", 4200); }
        }
      },
        el("td", null,
          el("div", { attrs: { style: "font-weight:600" }, text: t.title }),
          t.perspective ? el("div", { class: "card-meta", text: "视角：" + t.perspective }) : null
        ),
        el("td", null, statusBadge(t.status, TASK_STATUS)),
        el("td", { text: dept ? dept.name : (t.department_id || "—") }),
        el("td", { text: t.owner_id || "—" }),
        el("td", { text: t.perspective || "—" }),
        el("td", null, badge(t.priority || "P2", t.priority === "P0" ? "bad" : t.priority === "P1" ? "warn" : "")),
        el("td", { attrs: { title: fmtDateTime(t.deadline) }, text: t.deadline ? relTime(t.deadline) : "—" })
      );
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    host.appendChild(table);
  }

  load();
  return { destroy: function () { disposed = true; } };
}

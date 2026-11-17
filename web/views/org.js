// 组织图（R1 只读）。R3 交付：暂停/恢复、角色卡编辑、从组织图发起私聊/任务。
import { el, clear, icon, badge, emptyState, notice } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { statusBadge, AGENT_STATUS, VISIBILITY } from "../lib/ui.js";

const KIND_LABEL = { assistant: "助理", supervisor: "部门主管", employee: "员工" };

export function mount(ctx) {
  let disposed = false;
  let data = null;
  let error = null;
  let loading = true;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "组织"),
    el("div", { class: "topbar-sub" }, el("span", { text: "项目主管 → 助理 → 部门主管 → 员工" }))
  ));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", {
      class: "btn btn-sm", attrs: { type: "button", title: "重新加载组织图" },
      on: { click: function () { load(); } }
    }, icon("retry", 14), el("span", { text: "刷新" }))
  ));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  function laterNote() {
    if (ctx.demo) return notice("演示数据：组织图来自前端内存，暂停/恢复/编辑需 R3 的组织与 provisioning 接口。", "warn");
    return notice("组织图接口就绪后会显示真实部门与 Agent；当前为空态（R3 交付）。");
  }

  function load() {
    loading = true;
    error = null;
    render();
    ctx.api.org().then(function (res) {
      if (disposed) return;
      data = res || {};
      loading = false;
      render();
    }).catch(function (e) {
      if (disposed) return;
      loading = false;
      error = e;
      render();
    });
  }

  function agentCard(a, departmentName) {
    const cls = a.kind === "supervisor" ? "agent" : a.kind === "assistant" ? "agent" : "agent";
    const paused = a.status === "paused" || a.status === "budget_blocked";
    return el("div", { class: "org-node " + cls + (paused ? " org-paused" : "") },
      el("div", { class: "org-name" }, icon(a.kind === "assistant" ? "chat" : "dot", 13), el("span", { text: a.name })),
      el("div", { class: "org-meta", text: (KIND_LABEL[a.kind] || a.kind) + (departmentName ? " · " + departmentName : "") + (a.perspective ? " · " + a.perspective : "") }),
      el("div", { class: "row" },
        statusBadge(a.status, AGENT_STATUS),
        a.model ? badge(a.model, "") : null
      ),
      el("div", { class: "row", attrs: { style: "margin-top:6px" } },
        el("button", {
          class: "btn btn-sm", attrs: { type: "button", title: "R3 交付：从组织图发起私聊" },
          on: { click: function () { ctx.toast("发起私聊需 R3 的会话创建接口", "warn"); } }
        }, "私聊"),
        el("button", {
          class: "btn btn-sm", attrs: { type: "button", title: "R3 交付：暂停/恢复" },
          on: { click: function () { ctx.toast("暂停/恢复由核心执行，R3 交付", "warn"); } }
        }, a.status === "paused" ? "恢复" : "暂停")
      )
    );
  }

  function render() {
    clear(host);
    host.appendChild(el("div", { class: "page-head" },
      el("div", null, el("h2", { text: "组织图" }), el("p", { text: "人类是项目主管；助理是幕僚；部门主管管员工，员工有分工视角。" })),
      el("span", { class: "grow" })
    ));
    host.appendChild(laterNote());

    if (loading) {
      host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载组织…" })));
      return;
    }
    if (error) {
      host.appendChild(el("div", { class: "card", attrs: { style: "margin-top:14px" } },
        el("div", { class: "card-title", text: "组织图加载失败" }),
        notice(error.message || String(error), "bad"),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } },
          el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: load } }, "重试"))
      ));
      return;
    }

    const departments = data.departments || [];
    const agents = data.agents || [];
    const owner = data.owner || state.owner || { name: "项目主管" };
    const assistant = data.assistant || agents.filter(function (a) { return a.kind === "assistant"; })[0] || null;

    host.appendChild(el("div", { class: "org", attrs: { style: "margin-top:16px" } },
      el("div", { class: "org-level" },
        el("div", { class: "org-node", attrs: { style: "border-left:3px solid var(--accent)" } },
          el("div", { class: "org-name" }, icon("shield", 13), el("span", { text: owner.name || "项目主管" })),
          el("div", { class: "org-meta", text: "项目主管（人类）· 唯一用户" }),
          el("div", { class: "row" }, badge("G1/G2/G3 决策权", "accent"))
        )
      ),
      el("div", { class: "org-arrow", text: "↓" }),
      el("div", { class: "org-level" },
        assistant ? agentCard(assistant, "") : el("div", { class: "org-node org-paused" }, el("div", { class: "org-name", text: "助理未创建" }), el("div", { class: "org-meta", text: "首次向导完成后自动创建" }))
      ),
      el("div", { class: "org-arrow", text: "↓" }),
      el("div", { class: "org-level" },
        departments.length ? departments.map(function (d) {
          return el("div", { class: "org-node dept" + (d.status === "paused" ? " org-paused" : "") },
            el("div", { class: "org-name" }, icon("org", 13), el("span", { text: d.name })),
            el("div", { class: "org-meta", text: d.mission || d.kind || "" }),
            el("div", { class: "row" },
              badge(d.status === "paused" ? "暂停" : "运行中", d.status === "paused" ? "" : "ok"),
              badge(String(agents.filter(function (a) { return a.department_id === d.id; }).length) + " 人", "")
            )
          );
        }) : el("div", { class: "org-node org-paused" }, el("div", { class: "org-name", text: "还没有部门" }), el("div", { class: "org-meta", text: "与助理确认蓝图后批准 G1，再 provisioning" }))
      )
    ));

    if (agents.length) {
      host.appendChild(el("h3", { attrs: { style: "margin:22px 0 10px;font-size:14px" }, text: "Agent 角色卡" }));
      const grid = el("div", { class: "grid-2" });
      agents.forEach(function (a) {
        const dept = departments.filter(function (d) { return d.id === a.department_id; })[0];
        grid.appendChild(agentCard(a, dept ? dept.name : ""));
      });
      host.appendChild(grid);
    }
  }

  load();
  return { destroy: function () { disposed = true; } };
}

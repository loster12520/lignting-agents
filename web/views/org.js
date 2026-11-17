// 组织页：真实 R3 API —— 意图卡 → 蓝图（lean/full 两方案）→ G1 审批 → provisioning → 组织图/生命周期。
// 不做 demo 假成功：演示模式下写操作会明确报错；真实错误（403/409/503）原样展示并给出可行路径。
import { el, clear, icon, badge, notice, emptyState, toast, confirmModal, relTime, truncate } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { statusBadge, AGENT_STATUS } from "../lib/ui.js";

const BP_STATUS = {
  draft: ["草稿", ""], proposed: ["待 G1 审批", "warn"], approved: ["已批准", "ok"], rejected: ["已拒绝", "bad"]
};
const PROV_STATUS = {
  planned: ["已计划", ""], running: ["执行中", "sky"], verified: ["已核验", "ok"],
  rolled_back: ["已回滚", "bad"], failed: ["失败", "bad"]
};
const KIND_LABEL = { assistant: "助理", supervisor: "部门主管", employee: "员工" };

function parseJSON(text) {
  if (!text) return null;
  if (typeof text === "object") return text;
  try { return JSON.parse(text); } catch (e) { return null; }
}

function intentGoal(intent) {
  if (!intent) return "";
  const c = parseJSON(intent.content);
  if (c && typeof c.goal === "string") return c.goal;
  if (c && typeof c === "object") return JSON.stringify(c);
  return intent.content || "";
}

function roleMission(roleCard) {
  const c = parseJSON(roleCard);
  return c && c.mission ? c.mission : (typeof roleCard === "string" ? roleCard : "");
}

function listOf(res) { return (res && res.items) || []; }

function pickByPayload(res, field) {
  const items = listOf(res);
  for (let i = 0; i < items.length; i++) {
    const p = parseJSON(items[i].payload);
    if (p && p[field]) return p[field];
  }
  return "";
}

function pickApproval(res) {
  const items = listOf(res);
  for (let i = 0; i < items.length; i++) {
    const p = parseJSON(items[i].payload);
    if (p && p.blueprint_id) return items[i];
  }
  return null;
}

export function mount(ctx) {
  const s = {
    org: null, intent: null, blueprint: null, approval: null, provisioning: null, orphans: null,
    loading: true, loadError: null, actionError: null, actionHint: "", busy: "",
    intentText: "", option: "lean"
  };
  let disposed = false;
  let pollTimer = 0;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "组织"),
    el("div", { class: "topbar-sub" },
      el("span", { text: "意图 → 蓝图（lean/full）→ G1 审批 → provisioning" }),
      ctx.demo ? badge("演示数据", "warn") : null
    )
  ));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", {
      class: "btn btn-sm", attrs: { type: "button", title: "重新读取组织图" },
      on: { click: function () { load(); } }
    }, icon("retry", 14), el("span", { text: "刷新" }))
  ));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  // ---------- 数据加载 ----------
  async function load() {
    s.loading = true;
    s.loadError = null;
    render();
    try {
      const org = await ctx.api.org();
      if (disposed) return;
      s.org = org || {};

      try {
        s.intent = await ctx.api.latestIntent();
      } catch (e) {
        s.intent = null;
        if (!(e && (e.code === "not_found" || e.status === 404))) throw e;
      }
      if (s.intent && !s.intentText) s.intentText = intentGoal(s.intent);

      if (!s.blueprint) {
        const bpID = await findBlueprintID();
        if (bpID && !disposed) {
          try { s.blueprint = await ctx.api.blueprint(bpID); } catch (e2) { s.blueprint = null; }
        }
      }
      if (s.blueprint) {
        const c = parseJSON(s.blueprint.content);
        if (c && c.chosen_option) s.option = c.chosen_option;
        if (s.blueprint.status === "approved") {
          try {
            s.provisioning = await ctx.api.provisioning(s.blueprint.id);
          } catch (e3) {
            s.provisioning = null;
            if (!(e3 && (e3.code === "not_found" || e3.status === 404))) throw e3;
          }
        }
      }
      s.loading = false;
      render();
    } catch (e) {
      if (disposed) return;
      s.loading = false;
      s.loadError = e;
      render();
    }
  }

  async function findBlueprintID() {
    try {
      const pending = await ctx.api.approvals({ kind: "G1", status: "pending" });
      const id1 = pickByPayload(pending, "blueprint_id");
      if (id1) { s.approval = pickApproval(pending); return id1; }
    } catch (e) { /* 审批接口不可用时继续 */ }
    try {
      const approved = await ctx.api.approvals({ kind: "G1", status: "approved" });
      const id2 = pickByPayload(approved, "blueprint_id");
      if (id2) { s.approval = pickApproval(approved); return id2; }
    } catch (e) { /* 忽略：组织图仍可读 */ }
    return "";
  }

  // ---------- 渲染 ----------
  function render() {
    clear(host);
    const org = s.org || {};
    const departments = org.departments || [];
    const agents = org.agents || [];
    const assistant = org.assistant || null;

    host.appendChild(el("div", { class: "page-head" },
      el("div", null,
        el("h2", { text: "组织" }),
        el("p", { text: "人类批准蓝图（G1）后才会 provisioning；Agent 不能自批，也不能自改权限。" })
      ),
      el("span", { class: "grow" }),
      departments.length
        ? el("div", { class: "row" }, badge(departments.length + " 个部门", "ok"), badge(agents.length + " 名 Agent", ""))
        : badge("尚未组建", "warn")
    ));

    if (ctx.demo) {
      host.appendChild(notice("演示模式：组织图来自内存；意图/蓝图/G1/provisioning 需真实后端（18794 或本机实例）。", "warn"));
    }
    if (s.loadError) {
      host.appendChild(errorCard("组织数据加载失败", s.loadError, load));
      return;
    }
    if (s.loading) {
      host.appendChild(el("div", { class: "empty" }, el("span", { class: "spinner" }), el("p", { text: "正在读取组织…" })));
      return;
    }

    if (s.actionError) {
      host.appendChild(el("div", { class: "card", attrs: { style: "border-color:rgba(251,113,133,.4)" } },
        el("div", { class: "card-title" }, el("span", { text: "操作失败" }), badge(s.actionError.code || "error", "bad")),
        el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: s.actionError.message || String(s.actionError) }),
        s.actionHint ? el("div", { class: "help", attrs: { style: "margin-top:6px" }, text: s.actionHint }) : null,
        el("div", { class: "row", attrs: { style: "margin-top:10px" } },
          el("button", {
            class: "btn btn-sm", attrs: { type: "button" },
            on: { click: function () { s.actionError = null; s.actionHint = ""; render(); } }
          }, "关闭"),
          el("button", {
            class: "btn btn-sm", attrs: { type: "button" },
            on: { click: function () { s.actionError = null; s.actionHint = ""; load(); } }
          }, "重新读取"))
      ));
    }

    host.appendChild(intentSection());
    if (s.blueprint) host.appendChild(blueprintSection());
    if (departments.length || assistant) host.appendChild(orgSection());

    if (!departments.length && !s.blueprint) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "org",
        title: "还没有部门",
        desc: "先写一句意图（要解决什么问题），由助理生成 lean/full 两套蓝图；你批准 G1 后系统才会创建部门与 Agent。",
        later: "R3：蓝图 / G1 / provisioning 已接真实 API"
      })));
    }
  }

  function errorCard(title, err, retry) {
    return el("div", { class: "card" },
      el("div", { class: "card-title", text: title }),
      el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: (err && err.message) || String(err) }),
      err && err.code ? el("div", { class: "card-meta", text: "错误码：" + err.code }) : null,
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: retry } }, "重试"))
    );
  }

  // ---------- ① 意图卡 ----------
  function intentSection() {
    const textarea = el("textarea", {
      class: "textarea", attrs: { rows: "3", placeholder: "一句话说清要解决什么问题，例如：给团队做一个本地知识库，支持中文检索" }
    });
    textarea.value = s.intentText || "";
    textarea.addEventListener("input", function () { s.intentText = textarea.value; });

    return el("div", { class: "card", attrs: { style: "margin-top:14px" } },
      el("div", { class: "card-title" },
        el("span", { text: "① 意图卡" }),
        s.intent ? badge("v" + (s.intent.version || 1), "ok") : badge("未保存", "warn"),
        el("span", { class: "grow" }),
        s.intent ? el("span", { class: "card-meta", text: "最近保存：" + relTime(s.intent.created_at || s.intent.updated_at) }) : null
      ),
      el("div", { class: "help", attrs: { style: "margin:6px 0 8px" },
        text: "意图可迭代：保存后版本 +1；蓝图永远基于最新意图生成。" }),
      textarea,
      el("div", { class: "row", attrs: { style: "margin-top:8px" } },
        el("button", {
          class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "intent" || ctx.demo },
          on: { click: function () { saveIntent(textarea.value); } }
        }, s.busy === "intent" ? "保存中…" : "保存意图卡"),
        s.intent ? el("button", {
          class: "btn btn-sm", attrs: { type: "button", disabled: s.busy === "blueprint" || ctx.demo },
          on: { click: function () { proposeBlueprint(s.option); } }
        }, s.busy === "blueprint" ? "生成中…" : "生成蓝图（" + s.option + "）") : null,
        s.intent ? el("span", { class: "card-meta", text: "当前意图：" + truncate(intentGoal(s.intent), 56) }) : null
      )
    );
  }

  // ---------- ② 蓝图 ----------
  function blueprintSection() {
    const bp = s.blueprint;
    const content = parseJSON(bp.content) || {};
    const status = BP_STATUS[bp.status] || [bp.status, ""];
    const options = content.options || [];
    const departments = content.departments || [];

    const card = el("div", { class: "card", attrs: { style: "margin-top:14px" } });
    card.appendChild(el("div", { class: "card-title" },
      el("span", { text: "② 蓝图" }),
      badge(status[0], status[1]),
      content.chosen_option ? badge("方案：" + content.chosen_option, "sky") : null,
      el("span", { class: "grow" }),
      el("span", { class: "mono", attrs: { style: "font-family:var(--mono);font-size:11px;color:var(--faint)" }, text: bp.id })
    ));
    card.appendChild(el("div", { class: "card-meta", attrs: { style: "margin-top:4px" }, text: "mission：" + (content.mission || "—") }));

    if (options.length) {
      const grid = el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } });
      options.forEach(function (opt) {
        const chosen = opt.name === content.chosen_option;
        grid.appendChild(el("div", { class: "card", attrs: { style: chosen ? "border-color:var(--accent);background:var(--accent-soft)" : "" } },
          el("div", { class: "card-title" },
            el("span", { text: (opt.name === "lean" ? "精简版 lean" : opt.name === "full" ? "完整版 full" : opt.name) }),
            chosen ? badge("当前方案", "accent") : null
          ),
          el("div", { class: "card-body", text: opt.summary || "" }),
          el("div", { class: "card-meta", text: "部门：" + ((opt.department_kinds || []).join(" / ") || "—") }),
          opt.cost ? el("div", { class: "card-meta", text: "成本：" + opt.cost }) : null,
          (opt.tradeoffs || []).length ? el("ul", { class: "md", attrs: { style: "margin:6px 0 0;padding-left:18px" } },
            (opt.tradeoffs || []).map(function (t) { return el("li", { attrs: { style: "font-size:12.5px;color:var(--muted)" }, text: t }); })) : null
        ));
      });
      card.appendChild(grid);
    }

    if (departments.length) {
      const list = el("div", { class: "stack", attrs: { style: "margin-top:10px" } });
      departments.forEach(function (d) {
        list.appendChild(el("div", { class: "card", attrs: { style: "background:var(--panel-2)" } },
          el("div", { class: "card-title" }, el("span", { text: d.name || d.kind }), badge(d.kind || "", "")),
          el("div", { class: "card-meta", text: d.mission || "" }),
          el("div", { class: "row", attrs: { style: "margin-top:6px" } },
            (d.roles || []).map(function (r) { return badge(r.name || r.perspective || r.kind || "角色", ""); }))
        ));
      });
      card.appendChild(list);
    }

    const meta = el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } });
    if ((content.success_criteria || []).length) {
      meta.appendChild(el("div", null,
        el("div", { class: "card-meta", text: "成功标准" }),
        el("ul", { class: "md", attrs: { style: "margin:4px 0 0;padding-left:18px" } },
          content.success_criteria.map(function (t) { return el("li", { attrs: { style: "font-size:12.5px" }, text: t }); }))
      ));
    }
    if ((content.risks || []).length || content.budget) {
      meta.appendChild(el("div", null,
        el("div", { class: "card-meta", text: "风险与预算" }),
        el("ul", { class: "md", attrs: { style: "margin:4px 0 0;padding-left:18px" } },
          (content.risks || []).map(function (t) { return el("li", { attrs: { style: "font-size:12.5px" }, text: t }); })),
        content.budget ? el("div", { class: "card-meta", text: content.budget }) : null
      ));
    }
    if (meta.childNodes.length) card.appendChild(meta);

    card.appendChild(actionRow());
    return card;
  }

  function actionRow() {
    const bp = s.blueprint;
    const row = el("div", { class: "row", attrs: { style: "margin-top:12px;padding-top:10px;border-top:1px solid var(--line)" } });

    if (bp.status === "proposed" || bp.status === "draft") {
      row.appendChild(el("button", {
        class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "approve" || ctx.demo },
        on: { click: function () { approveBlueprint(); } }
      }, s.busy === "approve" ? "批准中…" : "批准 G1"));
      row.appendChild(el("button", {
        class: "btn btn-danger btn-sm", attrs: { type: "button", disabled: s.busy === "reject" || ctx.demo },
        on: { click: function () { rejectBlueprint(); } }
      }, "拒绝"));
      row.appendChild(el("span", { class: "card-meta", text: "批准后才创建部门与 Agent；这是人类闸门，Agent 不能自批。" }));
      return row;
    }

    if (bp.status === "rejected") {
      row.appendChild(el("span", { class: "card-meta", text: "蓝图已被拒绝：调整意图后可重新生成。" }));
      row.appendChild(el("button", {
        class: "btn btn-sm", attrs: { type: "button", disabled: ctx.demo },
        on: { click: function () { proposeBlueprint(s.option); } }
      }, "按当前方案重新生成"));
      return row;
    }

    const prov = s.provisioning;
    const st = prov ? (PROV_STATUS[prov.status] || [prov.status, ""]) : null;
    if (st) {
      row.appendChild(el("span", { text: "provisioning：" }));
      row.appendChild(badge(st[0], st[1]));
      if (prov.error) row.appendChild(el("span", { class: "card-meta", attrs: { style: "color:var(--bad)" }, text: prov.error }));
    }
    if (!prov || prov.status === "failed" || prov.status === "rolled_back") {
      row.appendChild(el("button", {
        class: "btn btn-primary btn-sm", attrs: { type: "button", disabled: s.busy === "provision" || ctx.demo },
        on: { click: function () { runProvision(); } }
      }, s.busy === "provision" ? "执行中…" : (prov ? "重试 provisioning" : "执行 provisioning")));
      row.appendChild(el("span", { class: "card-meta", text: "幂等：重复点击不会重复建部门；失败会回滚并给孤儿报告。" }));
    }
    const steps = prov ? parseJSON(prov.steps) : null;
    if (steps && steps.length) {
      const wrap = el("div", { attrs: { style: "width:100%;margin-top:8px" } });
      steps.forEach(function (step) {
        const name = step.name || step.step || step.id || "步骤";
        const stt = step.status || "";
        wrap.appendChild(el("div", { class: "row", attrs: { style: "gap:8px;font-size:12.5px;color:var(--muted)" } },
          el("span", { text: (stt === "done" || stt === "ok" ? "✓ " : stt === "failed" ? "✕ " : "· ") + name }),
          stt ? badge(stt, stt === "failed" ? "bad" : stt === "done" ? "ok" : "") : null,
          step.error ? el("span", { attrs: { style: "color:var(--bad)" }, text: step.error }) : null
        ));
      });
      row.appendChild(wrap);
    }
    return row;
  }

  // ---------- ③ 组织图 / 卡片 ----------
  function orgSection() {
    const org = s.org || {};
    const departments = org.departments || [];
    const agents = org.agents || [];
    const assistant = org.assistant || null;
    const owner = org.owner || state.owner || { name: "项目主管" };

    const section = el("div", { attrs: { style: "margin-top:22px" } },
      el("h3", { attrs: { style: "font-size:15px;margin:0 0 10px" }, text: "③ 组织图" })
    );

    section.appendChild(el("div", { class: "org" },
      el("div", { class: "org-level" },
        el("div", { class: "org-node", attrs: { style: "border-left:3px solid var(--accent)" } },
          el("div", { class: "org-name" }, icon("shield", 13), el("span", { text: owner.name || "项目主管" })),
          el("div", { class: "org-meta", text: "项目主管（人类）· 唯一用户" }),
          el("div", { class: "row" }, badge("G1/G2/G3 决策权", "accent"))
        )),
      el("div", { class: "org-arrow", text: "↓" }),
      el("div", { class: "org-level" }, assistant ? agentCard(assistant, "") : el("div", { class: "org-node org-paused" },
        el("div", { class: "org-name", text: "助理未创建" }),
        el("div", { class: "org-meta", text: "首次向导完成后自动创建" }))),
      el("div", { class: "org-arrow", text: "↓" }),
      el("div", { class: "org-level" },
        departments.length ? departments.map(function (d) {
          const members = agents.filter(function (a) { return a.department_id === d.id; });
          return el("div", { class: "org-node dept" + (d.status === "paused" ? " org-paused" : "") },
            el("div", { class: "org-name" }, icon("org", 13), el("span", { text: d.name })),
            el("div", { class: "org-meta", text: d.mission || d.kind || "" }),
            el("div", { class: "row" },
              badge(d.status === "paused" ? "暂停" : "运行中", d.status === "paused" ? "" : "ok"),
              badge(String(members.length) + " 人", "")
            ),
            deptActions(d)
          );
        }) : el("div", { class: "org-node org-paused" },
          el("div", { class: "org-name", text: "还没有部门" }),
          el("div", { class: "org-meta", text: "批准蓝图后 provisioning 会自动创建" }))
      )
    ));

    departments.forEach(function (d) {
      const members = agents.filter(function (a) { return a.department_id === d.id; });
      section.appendChild(el("div", { class: "card", attrs: { style: "margin-top:12px" } },
        el("div", { class: "card-title" },
          el("span", { text: d.name }),
          badge(d.kind || "", ""),
          d.status === "paused" ? badge("暂停", "warn") : badge("运行中", "ok"),
          el("span", { class: "grow" }),
          el("span", { class: "mono", attrs: { style: "font-family:var(--mono);font-size:11px;color:var(--faint)" }, text: d.id })
        ),
        el("div", { class: "card-meta", text: d.mission || "（无 mission）" }),
        el("div", { class: "grid-2", attrs: { style: "margin-top:10px" } },
          members.length ? members.map(function (a) { return agentCard(a, d.name); })
            : el("div", { class: "card-meta", text: "该部门还没有 Agent" }))
      ));
    });

    if (!departments.length && agents.length) {
      section.appendChild(el("div", { class: "grid-2", attrs: { style: "margin-top:12px" } },
        agents.map(function (a) { return agentCard(a, ""); })));
    }
    return section;
  }

  function agentCard(a, deptName) {
    const paused = a.status === "paused" || a.status === "budget_blocked";
    const busy = s.busy === "agent:" + a.id;
    const mission = roleMission(a.role_card);
    return el("div", { class: "org-node agent" + (paused ? " org-paused" : "") },
      el("div", { class: "org-name" }, icon(a.kind === "assistant" ? "chat" : "dot", 13), el("span", { text: a.name })),
      el("div", { class: "org-meta", text: (KIND_LABEL[a.kind] || a.kind || "") + (deptName ? " · " + deptName : "") + (a.perspective ? " · " + a.perspective : "") }),
      mission ? el("div", { class: "card-meta", text: truncate(mission, 48) }) : null,
      el("div", { class: "row" }, statusBadge(a.status, AGENT_STATUS), a.model ? badge(a.model, "") : null),
      el("div", { class: "row", attrs: { style: "margin-top:6px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: busy || paused || ctx.demo }, on: { click: function () { agentAction(a, "pause"); } } }, "暂停"),
        el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: busy || ctx.demo }, on: { click: function () { agentAction(a, "resume"); } } }, "恢复"),
        el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: busy || ctx.demo, title: "停止当前 turn" }, on: { click: function () { agentAction(a, "stop"); } } }, busy ? "处理中…" : "停止"),
        el("span", { class: "mono", attrs: { style: "font-family:var(--mono);font-size:10.5px;color:var(--faint)" }, text: a.id })
      )
    );
  }

  function deptActions(d) {
    const busy = s.busy === "dept:" + d.id;
    const paused = d.status === "paused";
    return el("div", { class: "row", attrs: { style: "margin-top:6px" } },
      el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: busy || paused || ctx.demo }, on: { click: function () { deptAction(d, "pause"); } } }, "暂停"),
      el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: busy || !paused || ctx.demo }, on: { click: function () { deptAction(d, "resume"); } } }, "恢复")
    );
  }

  // ---------- 动作 ----------
  function fail(e, hint) {
    s.actionError = e;
    s.actionHint = hint || "";
    s.busy = "";
    render();
  }

  async function saveIntent(text) {
    if (s.busy) return;
    const goal = String(text || "").trim();
    if (!goal) { toast("请先写一句意图", "warn"); return; }
    if (ctx.demo) { toast("演示模式不支持真实意图卡，请用真实后端", "warn"); return; }
    s.busy = "intent";
    s.actionError = null;
    render();
    try {
      const res = await ctx.api.saveIntent({ goal: goal });
      s.intent = { id: res.id, version: res.version, content: res.content, status: res.status };
      s.intentText = goal;
      s.busy = "";
      toast("意图卡已保存 v" + res.version, "ok");
      render();
    } catch (e) {
      fail(e, "写操作需要会话 + X-Lignting-CSRF；确认已登录且 CSRF 未过期。");
    }
  }

  async function proposeBlueprint(option) {
    if (s.busy) return;
    if (ctx.demo) { toast("演示模式不支持生成蓝图，请用真实后端", "warn"); return; }
    s.busy = "blueprint";
    s.actionError = null;
    render();
    try {
      const res = await ctx.api.proposeBlueprint({ option: option });
      s.blueprint = res.blueprint;
      s.approval = res.approval;
      s.option = option;
      s.busy = "";
      toast("蓝图已生成，等待你批准 G1（" + option + "）", "ok");
      render();
    } catch (e) {
      fail(e, "生成蓝图前需先保存意图卡；只有助理能提蓝图（403）。");
    }
  }

  async function approveBlueprint() {
    if (s.busy || !s.blueprint) return;
    const depts = (parseJSON(s.blueprint.content) || {}).departments || [];
    const ok = await confirmModal({
      title: "批准蓝图（G1）",
      body: "批准后系统将创建 " + depts.length + " 个部门与对应 Agent。这是人类闸门，会写入审计。",
      confirmText: "批准", danger: false
    });
    if (!ok) return;
    s.busy = "approve";
    s.actionError = null;
    render();
    try {
      s.blueprint = await ctx.api.approveBlueprint(s.blueprint.id, { reason: "用户批准" });
      s.busy = "";
      toast("G1 已批准，可以执行 provisioning", "ok");
      render();
    } catch (e) {
      fail(e, "批准失败：409 表示状态已变化，重新读取后再试。");
    }
  }

  async function rejectBlueprint() {
    if (s.busy || !s.blueprint) return;
    const ok = await confirmModal({
      title: "拒绝蓝图",
      body: "拒绝会记录理由并保留蓝图（不创建部门）。可调整意图后重新生成。",
      confirmText: "拒绝", danger: true
    });
    if (!ok) return;
    s.busy = "reject";
    s.actionError = null;
    render();
    try {
      s.blueprint = await ctx.api.rejectBlueprint(s.blueprint.id, { reason: "用户拒绝（前端骨架默认理由）" });
      s.busy = "";
      toast("蓝图已拒绝", "");
      render();
    } catch (e) {
      fail(e, "拒绝失败：409 表示状态已变化，重新读取后再试。");
    }
  }

  async function runProvision() {
    if (s.busy || !s.blueprint) return;
    s.busy = "provision";
    s.actionError = null;
    render();
    try {
      const res = await ctx.api.provision({ blueprint_id: s.blueprint.id });
      s.provisioning = res && res.provisioning ? res.provisioning : null;
      s.orphans = res && res.orphans ? res.orphans : null;
      s.busy = "";
      render();
      pollProvision(s.blueprint.id);
    } catch (e) {
      fail(e, "provisioning 需要蓝图已 G1 批准；403 表示未批准，先批准再执行。");
    }
  }

  async function pollProvision(bpID) {
    if (pollTimer) { clearTimeout(pollTimer); pollTimer = 0; }
    let tries = 0;
    const tick = async function () {
      if (disposed || tries >= 40) return;
      tries += 1;
      try {
        const prov = await ctx.api.provisioning(bpID);
        if (disposed) return;
        s.provisioning = prov;
        render();
        if (prov.status === "verified" || prov.status === "failed" || prov.status === "rolled_back") {
          if (prov.status === "verified") toast("provisioning 完成：部门与 Agent 已创建", "ok");
          if (prov.status === "failed") toast("provisioning 失败：" + (prov.error || "见步骤详情"), "bad");
          load();
          return;
        }
      } catch (e) { /* 轮询失败继续重试 */ }
      pollTimer = setTimeout(tick, 800);
    };
    pollTimer = setTimeout(tick, 300);
  }

  async function deptAction(d, action) {
    if (s.busy || ctx.demo) return;
    s.busy = "dept:" + d.id;
    s.actionError = null;
    render();
    try {
      await ctx.api.departmentAction(d.id, action, { reason: action === "pause" ? "用户在组织页暂停" : "" });
      s.busy = "";
      toast("部门已" + (action === "pause" ? "暂停" : "恢复") + "：" + d.name, "ok");
      load();
    } catch (e) {
      fail(e, "部门生命周期由核心执行；403/503 表示权限不足或组织服务未注入。");
    }
  }

  async function agentAction(a, action) {
    if (s.busy || ctx.demo) return;
    s.busy = "agent:" + a.id;
    s.actionError = null;
    render();
    try {
      await ctx.api.agentAction(a.id, action, { reason: "用户在组织页操作" });
      s.busy = "";
      toast("Agent " + a.name + "：" + ({ pause: "已暂停", resume: "已恢复", stop: "已请求停止" }[action] || action), "ok");
      load();
    } catch (e) {
      fail(e, "Agent 生命周期由核心执行；失败会显示真实错误，不会假装成功。");
    }
  }

  load();
  return {
    destroy: function () {
      disposed = true;
      if (pollTimer) { clearTimeout(pollTimer); pollTimer = 0; }
    }
  };
}

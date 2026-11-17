// 搜索页：中文 2 字起、结构化过滤、片段高亮、跳转定位。
// 需求：FR-CHAT-11 / FR-SRCH-01..05（R1 只接 /api/search）。
import { el, clear, icon, badge, emptyState, notice, debounce, relTime, fmtDateTime } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { parseSnippet } from "../lib/highlight.js";
import { renderSnippet } from "../lib/mdrender.js";

const KINDS = [
  ["message", "消息"],
  ["task", "任务"],
  ["decision", "决策"],
  ["artifact", "产出物"]
];

const KIND_BADGE = { message: ["消息", "sky"], task: ["任务", "violet"], decision: ["决策", "accent"], artifact: ["产出", ""] };

export function mount(ctx, route) {
  const s = state.search;
  if (route && route.query && route.query.q && !s.q) s.q = route.query.q;

  let kindSel = s.filters.kind || [];
  let departmentId = s.filters.department_id || "";
  let conversationId = s.filters.conversation_id || "";
  let limit = 20;
  let disposed = false;
  let reqSeq = 0;

  // ---------- 顶栏 ----------
  clear(ctx.regions.topbar);
  const resultCount = el("span", { class: "card-meta", text: "" });
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "搜索"),
    el("div", { class: "topbar-sub" },
      el("span", { text: "消息 · 任务 · 决策 · 产出物" }),
      resultCount
    )
  ));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", {
      class: "btn btn-sm", attrs: { type: "button", title: "清空条件" },
      on: { click: function () { resetFilters(); } }
    }, "重置条件")
  ));

  // ---------- 搜索框 ----------
  const input = el("input", {
    class: "input search-input", attrs: {
      type: "search", placeholder: "搜索消息、任务、决策、产出物…（中文 2 字即可，如：限流）",
      "aria-label": "搜索关键词", autocomplete: "off", id: "search-q"
    }
  });
  input.value = s.q || "";

  const form = el("form", { class: "search-bar", on: { submit: function (e) { e.preventDefault(); run(0); } } },
    el("div", { class: "search-input-wrap" }, icon("search", 16), input),
    el("button", { class: "btn btn-primary", attrs: { type: "submit" } }, "搜索")
  );

  // ---------- 过滤器 ----------
  const kindChips = el("div", { class: "row" });
  KINDS.forEach(function (k) {
    const chip = el("button", {
      class: "filter-chip", attrs: { type: "button", "aria-pressed": kindSel.indexOf(k[0]) >= 0 ? "true" : "false" },
      on: {
        click: function () {
          const on = kindSel.indexOf(k[0]) >= 0;
          if (on) kindSel = kindSel.filter(function (x) { return x !== k[0]; });
          else kindSel = kindSel.concat([k[0]]);
          chip.setAttribute("aria-pressed", on ? "false" : "true");
          s.filters.kind = kindSel;
          run(0);
        }
      }
    }, k[1]);
    kindChips.appendChild(chip);
  });

  const deptSel = el("select", { class: "select", attrs: { "aria-label": "按部门过滤" } });
  deptSel.appendChild(el("option", { attrs: { value: "" }, text: "全部部门" }));
  state.departments.forEach(function (d) {
    const o = el("option", { attrs: { value: d.id }, text: d.name });
    deptSel.appendChild(o);
  });
  deptSel.value = departmentId;
  deptSel.addEventListener("change", function () { departmentId = deptSel.value; s.filters.department_id = departmentId; run(0); });

  const convSel = el("select", { class: "select", attrs: { "aria-label": "按会话过滤" } });
  convSel.appendChild(el("option", { attrs: { value: "" }, text: "全部会话" }));
  state.conversations.forEach(function (c) {
    convSel.appendChild(el("option", { attrs: { value: c.id }, text: c.title }));
  });
  convSel.value = conversationId;
  convSel.addEventListener("change", function () { conversationId = convSel.value; s.filters.conversation_id = conversationId; run(0); });

  const fromInput = el("input", { class: "input", attrs: { type: "date", "aria-label": "起始日期" } });
  const toInput = el("input", { class: "input", attrs: { type: "date", "aria-label": "结束日期" } });
  fromInput.addEventListener("change", function () { run(0); });
  toInput.addEventListener("change", function () { run(0); });

  const filters = el("div", { class: "filters" },
    kindChips, deptSel, convSel,
    el("span", { class: "search-stat", text: "时间" }),
    fromInput, el("span", { class: "search-stat", text: "→" }), toInput
  );

  const resultsHost = el("div", { class: "results", attrs: { "aria-live": "polite" } });
  const statusHost = el("div", { class: "row", attrs: { style: "margin-top:14px;justify-content:space-between" } });

  clear(ctx.regions.content);
  ctx.regions.content.appendChild(el("div", { class: "page" },
    el("div", { class: "stack" },
      form,
      filters,
      statusHost,
      resultsHost
    )
  ));

  // ---------- 渲染 ----------
  function renderStatus() {
    clear(statusHost);
    const left = el("div", { class: "search-stat" });
    if (s.loading) {
      left.appendChild(el("span", { class: "spinner" }));
      left.appendChild(document.createTextNode(" 正在检索…"));
    } else if (s.searched && !s.error) {
      left.textContent = "共 " + s.total + " 条结果" + (s.items.length < s.total ? "，已显示 " + s.items.length + " 条" : "");
    }
    statusHost.appendChild(left);
    if (s.searched && !s.error && s.items.length && s.items.length < s.total) {
      statusHost.appendChild(el("button", {
        class: "btn btn-sm", attrs: { type: "button" },
        on: { click: function () { limit += 20; run(limit); } }
      }, "加载更多"));
    }
    resultCount.textContent = s.searched && !s.loading ? "· " + s.total + " 条" : "";
  }

  function renderResults() {
    clear(resultsHost);

    if (s.loading) return;

    if (s.error) {
      resultsHost.appendChild(el("div", { class: "card" },
        el("div", { class: "card-title", text: "检索失败" }),
        notice(s.error.message || "请求失败", "bad"),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } },
          el("button", {
            class: "btn btn-sm", attrs: { type: "button" },
            on: { click: function () { run(0); } }
          }, "重试"))
      ));
      return;
    }

    if (!s.searched) {
      resultsHost.appendChild(emptyState({
        icon: "search",
        title: "输入关键词开始检索",
        desc: "支持中文 2 字（如「限流」「登录」）、英文与代码标识符；删除的消息不会被检索到。"
      }));
      return;
    }

    if (!s.items.length) {
      resultsHost.appendChild(emptyState({
        icon: "search",
        title: "没有找到「" + (s.q || "") + "」",
        desc: "可以试试：换成更短的关键词、放宽类型/部门/时间过滤、或确认消息未被删除。",
        later: "保存搜索 / 最近搜索为 P1"
      }));
      return;
    }

    const groups = new Map();
    s.items.forEach(function (it) {
      if (!groups.has(it.kind)) groups.set(it.kind, []);
      groups.get(it.kind).push(it);
    });

    groups.forEach(function (items, kind) {
      const g = el("div", { class: "result-group" },
        el("div", { class: "result-group-label" },
          el("span", { text: KIND_BADGE[kind] ? KIND_BADGE[kind][0] : kind }),
          badge(String(items.length), "")
        )
      );
      items.forEach(function (it) {
        g.appendChild(resultItem(it));
      });
      resultsHost.appendChild(g);
    });
  }

  function jump(it) {
    if (it.kind === "message" && it.conversation_id) {
      ctx.gotoMessage(it.conversation_id, it.ref_id);
      return;
    }
    if (it.kind === "task") { ctx.navigate("#/tasks?id=" + encodeURIComponent(it.ref_id)); return; }
    if (it.kind === "decision") { ctx.navigate("#/memory?id=" + encodeURIComponent(it.ref_id)); return; }
    if (it.kind === "artifact") { ctx.navigate("#/tasks?id=" + encodeURIComponent(it.ref_id)); return; }
    ctx.toast("该类型暂无详情页（R4 交付）", "warn");
  }

  function resultItem(it) {
    const parts = parseSnippet(it.snippet || "");
    const meta = [];
    if (it.conversation_id) {
      const c = state.conversations.filter(function (x) { return x.id === it.conversation_id; })[0];
      if (c) meta.push(c.title);
    }
    meta.push(relTime(it.created_at));
    if (typeof it.score === "number") meta.push("score " + it.score.toFixed(2));

    return el("button", {
      class: "result", attrs: { type: "button", title: "跳转到上下文" },
      on: { click: function () { jump(it); } }
    },
      el("div", { class: "result-head" },
        badge(KIND_BADGE[it.kind] ? KIND_BADGE[it.kind][0] : it.kind, KIND_BADGE[it.kind] ? KIND_BADGE[it.kind][1] : ""),
        el("span", { class: "result-title", text: it.title || "(无标题)" })
      ),
      renderSnippet(parts),
      el("div", { class: "result-meta", attrs: { title: fmtDateTime(it.created_at) }, text: meta.join(" · ") })
    );
  }

  // ---------- 请求 ----------
  async function run(nextLimit) {
    s.q = input.value.trim();
    s.filters.kind = kindSel;
    s.filters.department_id = departmentId;
    s.filters.conversation_id = conversationId;
    s.filters.from = fromInput.value ? fromInput.value + "T00:00:00Z" : "";
    s.filters.to = toInput.value ? toInput.value + "T23:59:59Z" : "";
    if (typeof nextLimit === "number") limit = nextLimit || 20;

    if (!s.q) {
      s.items = []; s.total = 0; s.searched = false; s.loading = false; s.error = null;
      renderResults(); renderStatus();
      return;
    }

    // 把查询同步到 hash（replaceState：不产生历史条目、不触发 hashchange）
    try {
      const qs = new URLSearchParams();
      qs.set("q", s.q);
      history.replaceState(null, "", "#/search?" + qs.toString());
    } catch (e) { /* 忽略 */ }

    const myReq = ++reqSeq;
    s.loading = true;
    s.error = null;
    s.searched = true;
    renderStatus();
    renderResults();

    try {
      const params = {
        q: s.q,
        kind: kindSel.join(","),
        department_id: departmentId,
        conversation_id: conversationId,
        from: s.filters.from,
        to: s.filters.to,
        limit: limit
      };
      if (ctx.demo) params.limit = 100; // 演示数据量小，一次取全便于看排序
      const res = await ctx.api.search(params);
      if (disposed || myReq !== reqSeq) return;
      s.items = (res && res.items) || [];
      s.total = (res && typeof res.total === "number") ? res.total : s.items.length;
      s.next = (res && res.next) || "";
      s.loading = false;
    } catch (e) {
      if (disposed || myReq !== reqSeq) return;
      s.loading = false;
      s.items = [];
      s.total = 0;
      s.error = e;
      if (e.code !== "not_implemented") ctx.toast("检索失败：" + e.message, "bad");
    }
    renderStatus();
    renderResults();
  }

  function resetFilters() {
    kindSel = [];
    departmentId = "";
    conversationId = "";
    fromInput.value = "";
    toInput.value = "";
    deptSel.value = "";
    convSel.value = "";
    Array.prototype.forEach.call(kindChips.children, function (c) { c.setAttribute("aria-pressed", "false"); });
    run(0);
  }

  const debounced = debounce(function () { run(0); }, 320);
  input.addEventListener("input", debounced);
  input.addEventListener("keydown", function (e) {
    if (e.key === "Escape") { input.value = ""; run(0); }
  });

  setTimeout(function () { input.focus(); }, 0);
  if (s.q) run(0);
  else { renderResults(); renderStatus(); }

  return {
    destroy: function () { disposed = true; }
  };
}

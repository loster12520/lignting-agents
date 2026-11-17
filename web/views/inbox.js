// 收件箱/通知中心：真实 /api/notifications；级别分组 + 会话/部门过滤 + 已读/稍后 + SSE 标脏。
// 注意：通知级别由核心创建，前端只改 status（unread/read/dismissed）；FYI 是分组视图，不是本地伪造的级别。
import { el, clear, icon, badge, notice, emptyState, toast, fmtDateTime, relTime, truncate, debounce } from "../lib/dom.js";
import { state, on } from "../lib/store.js";
import { nameOfDept } from "../lib/ui.js";

const LEVELS = {
  need_decide: ["需要决定", "bad"],
  need_know: ["需要知道", "warn"],
  fyi: ["FYI", ""],
  blocking: ["阻塞", "bad"]
};
const STATUS = {
  unread: ["未读", "warn"],
  read: ["已读", ""],
  dismissed: ["稍后/忽略", ""]
};
const TABS = [
  ["all", "全部"],
  ["need_decide", "需要决定"],
  ["need_know", "需要知道"],
  ["fyi", "FYI"],
  ["blocking", "阻塞"],
  ["unread", "未读"]
];
const PAGE_SIZE = 30;

function levelMeta(level) { return LEVELS[level] || [level || "通知", ""]; }
function statusMeta(status) { return STATUS[status] || [status || "未知", ""]; }

export function mount(ctx) {
  const q = (state.route && state.route.query) || {};
  const s = {
    items: [],
    loading: true,
    error: null,
    actionError: null,
    actionHint: "",
    busy: "",
    group: q.group || "all",
    q: q.q || "",
    conversationId: q.conversation_id || "",
    departmentId: q.department_id || "",
    statusFilter: "",
    page: 1,
    stale: false
  };
  let disposed = false;
  let offStale = null;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "收件箱"),
    el("div", { class: "topbar-sub" },
      el("span", { text: "通知是持久对象；按打扰级别分组，实时打扰只用于需要决定/阻塞" }),
      ctx.demo ? badge("演示数据", "warn") : null)));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { load(); } } },
      icon("retry", 14), el("span", { text: "刷新" }))));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  function render() {
    if (disposed) return;
    clear(host);
    renderPage();
  }

  async function load() {
    s.loading = true;
    s.error = null;
    s.stale = false;
    render();
    try {
      const res = await ctx.api.notifications({ limit: 500 });
      if (disposed) return;
      s.items = dedupe((res && res.items) || []);
      s.loading = false;
      render();
    } catch (e) {
      if (disposed) return;
      s.loading = false;
      s.error = e;
      render();
    }
  }

  // 同一升级包唯一：level + object_type + object_id；没有 object_id 的广播条目按 id 去重。
  function dedupe(items) {
    const seen = new Set();
    return items.filter(function (n) {
      if (!n || !n.id) return false;
      const key = n.object_id ? (n.level + "|" + n.object_type + "|" + n.object_id) : n.id;
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
  }

  function deptOfNotification(n) {
    const cid = n.conversation_id;
    if (!cid) return "";
    const conv = (state.conversations || []).filter(function (c) { return c.id === cid; })[0];
    return conv ? conv.department_id : "";
  }

  function convTitle(cid) {
    const c = (state.conversations || []).filter(function (x) { return x.id === cid; })[0];
    return c ? c.title : cid;
  }

  function filtered() {
    let arr = s.items.slice();
    if (s.group === "unread") arr = arr.filter(function (n) { return n.status === "unread"; });
    else if (s.group !== "all") arr = arr.filter(function (n) { return n.level === s.group; });
    if (s.statusFilter) arr = arr.filter(function (n) { return n.status === s.statusFilter; });
    if (s.conversationId) arr = arr.filter(function (n) { return n.conversation_id === s.conversationId; });
    if (s.departmentId) arr = arr.filter(function (n) { return deptOfNotification(n) === s.departmentId; });
    if (s.q) {
      const q = s.q.toLowerCase();
      arr = arr.filter(function (n) {
        return (n.title || "").toLowerCase().indexOf(q) >= 0 ||
          (n.body || "").toLowerCase().indexOf(q) >= 0 ||
          (n.object_id || "").toLowerCase().indexOf(q) >= 0;
      });
    }
    arr.sort(function (a, b) {
      const au = a.status === "unread" ? 1 : 0;
      const bu = b.status === "unread" ? 1 : 0;
      if (au !== bu) return bu - au;
      return String(b.created_at || "").localeCompare(String(a.created_at || ""));
    });
    return arr;
  }

  function unreadCount(level) {
    return s.items.filter(function (n) {
      return n.status === "unread" && (!level || level === "all" || n.level === level);
    }).length;
  }

  function levelCount(level) {
    return s.items.filter(function (n) { return n.level === level; }).length;
  }

  function tabs() {
    const row = el("div", { class: "tabs", attrs: { style: "margin-top:14px" } });
    TABS.forEach(function (t) {
      const count = t[0] === "all" ? s.items.length : t[0] === "unread" ? unreadCount("all") : levelCount(t[0]);
      row.appendChild(el("button", {
        class: "tab" + (s.group === t[0] ? " active" : ""),
        attrs: { type: "button" },
        on: { click: function () { s.group = t[0]; s.page = 1; render(); } }
      }, t[1] + (count ? " " + count : "")));
    });
    return row;
  }

  function filterCard() {
    const convSel = el("select", { class: "select", attrs: { "aria-label": "按会话过滤" } });
    convSel.appendChild(el("option", { attrs: { value: "" }, text: "全部会话" }));
    (state.conversations || []).forEach(function (c) {
      convSel.appendChild(el("option", { attrs: { value: c.id }, text: c.title || c.id }));
    });
    if (s.conversationId && !(state.conversations || []).some(function (c) { return c.id === s.conversationId; })) {
      convSel.appendChild(el("option", { attrs: { value: s.conversationId }, text: s.conversationId }));
    }
    convSel.value = s.conversationId;
    convSel.addEventListener("change", function () { s.conversationId = convSel.value; s.page = 1; render(); });

    const deptSel = el("select", { class: "select", attrs: { "aria-label": "按部门过滤" } });
    deptSel.appendChild(el("option", { attrs: { value: "" }, text: "全部部门" }));
    (state.departments || []).forEach(function (d) {
      deptSel.appendChild(el("option", { attrs: { value: d.id }, text: d.name })); 
    });
    deptSel.value = s.departmentId;
    deptSel.addEventListener("change", function () { s.departmentId = deptSel.value; s.page = 1; render(); });

    const statusSel = el("select", { class: "select", attrs: { "aria-label": "按状态过滤" } });
    statusSel.appendChild(el("option", { attrs: { value: "" }, text: "全部状态" }));
    Object.keys(STATUS).forEach(function (k) {
      statusSel.appendChild(el("option", { attrs: { value: k }, text: STATUS[k][0] }));
    });
    statusSel.value = s.statusFilter;
    statusSel.addEventListener("change", function () { s.statusFilter = statusSel.value; s.page = 1; render(); });

    const search = el("input", { class: "input", attrs: { type: "search", placeholder: "搜索标题 / 正文 / 对象 ID" } });
    search.value = s.q;
    const onSearch = debounce(function () { s.q = search.value; s.page = 1; render(); }, 220);
    search.addEventListener("input", onSearch);
    search.addEventListener("keydown", function (ev) { if (ev.key === "Enter") { s.q = search.value; s.page = 1; render(); } });

    return el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title" }, el("span", { text: "过滤" }), el("span", { class: "grow" })),
      el("div", { class: "grid-2", attrs: { style: "margin-top:8px" } },
        el("div", { class: "field" }, el("label", { text: "搜索" }), search),
        el("div", { class: "field" }, el("label", { text: "会话" }), convSel),
        el("div", { class: "field" }, el("label", { text: "部门" }), deptSel),
        el("div", { class: "field" }, el("label", { text: "状态" }), statusSel)),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", {
          class: "btn btn-sm", attrs: { type: "button" },
          on: { click: function () {
            s.q = ""; s.conversationId = ""; s.departmentId = ""; s.statusFilter = ""; s.group = "all"; s.page = 1; render();
          } }
        }, "重置过滤"),
        el("span", { class: "help", text: "同一升级包只保留一个条目；通知级别由核心创建，收件箱只改 unread/read/dismissed，FYI 是分组视图。" })));
  }

  function summary() {
    const all = s.items.length;
    const unread = unreadCount("all");
    const decide = unreadCount("need_decide");
    const blocking = unreadCount("blocking");
    return el("div", { class: "filters", attrs: { style: "margin-top:12px" } },
      badge("共 " + all, "sky"),
      unread ? badge("未读 " + unread, "warn") : badge("全部已读", "ok"),
      decide ? badge("待决定 " + decide, "bad") : null,
      blocking ? badge("阻塞 " + blocking, "bad") : null);
  }

  function errorCard() {
    return el("div", { class: "card", attrs: { style: "margin-top:14px;border-color:rgba(251,113,133,.4)" } },
      el("div", { class: "card-title" }, el("span", { text: "收件箱加载失败" }), badge((s.error && s.error.code) || "error", "bad")),
      el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: (s.error && s.error.message) || String(s.error) }),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: load } }, "重试")));
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

  function objectHash(n) {
    const id = encodeURIComponent(n.object_id || "");
    switch (n.object_type) {
      case "task": return "#/tasks?id=" + id;
      case "review": return "#/reviews?id=" + id;
      case "approval": return "#/approvals";
      case "artifact": return "#/artifacts?id=" + id;
      case "agent": return "#/agents?id=" + id;
      case "decision": return "#/discussions?tab=decisions";
      case "conversation": return "#/chat/" + id;
      default: return n.conversation_id ? "#/chat/" + encodeURIComponent(n.conversation_id) : "";
    }
  }

  function notificationCard(n) {
    const lm = levelMeta(n.level);
    const sm = statusMeta(n.status);
    const busy = s.busy === n.id;
    const targetHash = objectHash(n);
    const row = el("div", { class: "row", attrs: { style: "margin-top:10px;flex-wrap:wrap" } });
    if (n.status !== "read") {
      row.appendChild(el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: busy }, on: { click: function () { setStatus(n, "read"); } } }, busy ? "处理中…" : "标记已读"));
    }
    if (n.status !== "dismissed") {
      row.appendChild(el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: busy, title: "平台的 dismissed 状态；稍后可在「全部」里找回" }, on: { click: function () { setStatus(n, "dismissed"); } } }, "稍后"));
    }
    if (n.status !== "unread") {
      row.appendChild(el("button", { class: "btn btn-sm", attrs: { type: "button", disabled: busy }, on: { click: function () { setStatus(n, "unread"); } } }, "标为未读"));
    }
    if (targetHash) {
      row.appendChild(el("button", {
        class: "btn btn-sm" + (n.level === "need_decide" || n.level === "blocking" ? " btn-primary" : ""),
        attrs: { type: "button" },
        on: { click: function () { ctx.navigate(targetHash); } }
      }, "打开对象"));
    }

    return el("div", { class: "card", attrs: { style: "margin-top:10px;border-left:3px solid " + (n.level === "blocking" || n.level === "need_decide" ? "var(--bad)" : "var(--line-2)") } },
      el("div", { class: "card-title" },
        badge(lm[0], lm[1]),
        el("span", { text: n.title || "（无标题）" }),
        badge(sm[0], sm[1]),
        el("span", { class: "grow" }),
        el("span", { class: "card-meta", attrs: { title: fmtDateTime(n.created_at) }, text: relTime(n.created_at) })),
      n.body ? el("div", { class: "card-body", attrs: { style: "margin-top:6px;font-size:13px;color:var(--muted)" }, text: n.body }) : null,
      el("div", { class: "card-meta", attrs: { style: "margin-top:6px;flex-wrap:wrap" } },
        n.conversation_id ? el("span", { text: "会话：" + convTitle(n.conversation_id) }) : el("span", { text: "全局通知" }),
        deptOfNotification(n) ? el("span", { text: " · 部门：" + nameOfDept(deptOfNotification(n)) }) : null,
        n.object_type ? el("span", { text: " · 对象：" + n.object_type + (n.object_id ? " / " + truncate(n.object_id, 24) : "") }) : null,
        el("span", { text: " · " + (n.id || "") })),
      row);
  }

  async function setStatus(n, status) {
    if (s.busy) return;
    s.busy = n.id;
    s.actionError = null;
    s.actionHint = "";
    render();
    try {
      const updated = await ctx.api.notificationStatus(n.id, { status: status });
      s.items = s.items.map(function (x) {
        if (x.id !== n.id) return x;
        return Object.assign({}, x, updated || {}, { status: (updated && updated.status) || status });
      });
      s.busy = "";
      toast("已标记：" + statusMeta(status)[0], "ok");
      render();
    } catch (e) {
      s.busy = "";
      s.actionError = e;
      s.actionHint = "状态只允许 unread/read/dismissed；失败不会在本地假装成功。";
      render();
    }
  }

  function renderPage() {
    host.appendChild(el("div", { class: "page-head" },
      el("div", null,
        el("h2", { text: "收件箱" }),
        el("p", { text: "需要决定/阻塞优先；FYI 不应打断你。同一升级包只保留一个持久条目。" })),
      el("span", { class: "grow" }),
      el("span", { class: "search-stat", text: s.items.length + " 条通知" })));

    if (ctx.demo) host.appendChild(notice("演示数据：收件箱来自内存 mock；真实后端会持久化通知并写审计。", "warn"));
    if (s.stale) {
      host.appendChild(el("div", { class: "notice warn", attrs: { style: "margin-top:10px" } },
        el("span", { text: "收到新通知（SSE notification.created），当前列表已标脏。" }),
        el("button", { class: "btn btn-sm", attrs: { type: "button", style: "margin-left:8px" }, on: { click: load } }, "刷新")));
    }
    if (s.actionError) host.appendChild(actionCard());
    host.appendChild(tabs());

    if (s.loading) {
      host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载收件箱…" })));
      return;
    }
    if (s.error) { host.appendChild(errorCard()); return; }

    host.appendChild(filterCard());
    host.appendChild(summary());

    const arr = filtered();
    if (!arr.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "inbox", title: "当前分组没有通知",
        desc: "需要决定/阻塞会实时提醒；FYI 只进收件箱。可用过滤条件找回稍后/已读条目。",
        later: "通知状态：unread / read / dismissed"
      })));
      return;
    }
    const visible = arr.slice(0, s.page * PAGE_SIZE);
    const stack = el("div", { class: "stack", attrs: { style: "margin-top:12px" } });
    visible.forEach(function (n) { stack.appendChild(notificationCard(n)); });
    host.appendChild(stack);
    if (visible.length < arr.length) {
      host.appendChild(el("div", { class: "row", attrs: { style: "margin-top:12px;justify-content:center" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.page += 1; render(); } } },
          "加载更多（已显示 " + visible.length + " / " + arr.length + "）")));
    } else {
      host.appendChild(el("div", { class: "card-meta", attrs: { style: "margin-top:12px;text-align:center" },
        text: "已显示全部 " + arr.length + " 条（按未读优先、时间倒序）" }));
    }
  }

  offStale = on("data:stale", function (ev) {
    if (!ev || ev.type !== "notification.created") return;
    if (disposed) return;
    s.stale = true;
    render();
  });

  load();
  return {
    destroy: function () {
      disposed = true;
      if (offStale) { offStale(); offStale = null; }
    }
  };
}
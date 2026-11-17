// 应用入口：bootstrap → 首次向导 or 工作台；hash 路由；SSE 接线与事件归约。
//
// 关键约束：
// - 不静默吞错：真实 API 的 4xx/5xx 一律显示错误与重试，绝不替换成演示数据。
// - 演示模式只在 ?demo=1 或接口未实现（404/501）时进入，且页头常驻「演示数据」标注。
// - 不使用 innerHTML；Markdown 与检索片段都由 DOM 节点构造。
import { el, clear, icon, toast as toastNode, badge, confirmModal, emptyState, notice, initials } from "./lib/dom.js";
import {
  state, on, emit, convById, bucket, ingestMessages, upsertMessage, removeMessage,
  applyStreamDelta, endStream, clearStream, setUnread, unreadTotal, previewOf
} from "./lib/store.js";
import { createLiveApi, ApiError } from "./lib/api.js";
import { createDemoApi } from "./lib/mock.js";
import { createEventStream } from "./lib/sse.js";
import { CONV_TYPE, statusBadge, convAvatar, relTime, AGENT_STATUS } from "./lib/ui.js";
import * as chatView from "./views/chat.js";
import * as searchView from "./views/search.js";
import * as orgView from "./views/org.js";
import * as tasksView from "./views/tasks.js";
import * as artifactsView from "./views/artifacts.js";
import * as reviewsView from "./views/reviews.js";
import * as approvalsView from "./views/approvals.js";
import * as memoryView from "./views/memory.js";
import * as auditView from "./views/audit.js";
import * as settingsView from "./views/settings.js";
import { mountSetup } from "./views/setup.js";

const ROUTES = {
  chat: chatView,
  search: searchView,
  org: orgView,
  tasks: tasksView,
  artifacts: artifactsView,
  reviews: reviewsView,
  approvals: approvalsView,
  memory: memoryView,
  audit: auditView,
  settings: settingsView
};

const NAV = [
  ["chat", "会话", "chat"],
  ["search", "搜索", "search"],
  ["org", "组织", "org"],
  ["tasks", "任务", "task"],
  ["artifacts", "产出", "file"],
  ["reviews", "评审", "shield"],
  ["approvals", "审批", "approval"],
  ["memory", "记忆", "memory"],
  ["audit", "审计", "audit"],
  ["settings", "设置", "gear"]
];

const app = document.getElementById("app");
let currentView = null;
let stream = null;
let readTimer = 0;

const ctx = {
  api: null,
  demo: false,
  route: { name: "chat", params: {}, query: {} },
  regions: null,
  navigate: navigate,
  toast: toast,
  refresh: function () { renderRoute(true); },
  reloadConversations: reloadConversations,
  gotoMessage: gotoMessage
};

// ---------- 工具 ----------
function toast(message, kind, ms) { return toastNode(message, kind, ms); }

function parsePayload(p) {
  if (!p) return {};
  if (typeof p === "object") return p;
  try { return JSON.parse(p); } catch (e) { return {}; }
}

function parseHash() {
  let h = location.hash || "";
  if (h.indexOf("#/") !== 0) return { name: "chat", params: { id: "" }, query: {} };
  const raw = h.slice(2);
  const qi = raw.indexOf("?");
  const pathPart = qi >= 0 ? raw.slice(0, qi) : raw;
  const queryPart = qi >= 0 ? raw.slice(qi + 1) : "";
  const segs = pathPart.split("/").filter(Boolean);
  const name = ROUTES[segs[0]] ? segs[0] : "chat";
  const params = { id: segs[1] ? decodeURIComponent(segs[1]) : "" };
  const query = {};
  new URLSearchParams(queryPart).forEach(function (v, k) { query[k] = v; });
  return { name: name, params: params, query: query };
}

function navigate(hash) {
  if (location.hash === hash) { renderRoute(true); return; }
  location.hash = hash;
}

function onHashChange() { renderRoute(false); }

// ---------- 启动 ----------
async function boot() {
  const search = new URLSearchParams(location.search);
  const demoParam = search.get("demo");
  const skipSetup = search.get("skipsetup") === "1";

  let api = null;
  let demoReason = "";

  if (demoParam === "1") {
    api = createDemoApi();
    ctx.demo = true;
    state.mode = "demo";
    demoReason = "?demo=1 演示模式：数据在内存中，刷新即重置";
  } else {
    api = createLiveApi();
  }
  ctx.api = api;

  let boot;
  try {
    boot = await api.bootstrap();
  } catch (e) {
    if (e instanceof ApiError && e.code === "not_implemented" && demoParam !== "0") {
      api = createDemoApi();
      ctx.api = api;
      ctx.demo = true;
      state.mode = "demo";
      demoReason = "后端 API 尚未实现（404），已进入演示模式";
      try { boot = await api.bootstrap(); }
      catch (e2) { renderFatal(e2, "演示模式启动失败"); return; }
    } else {
      renderFatal(e, "无法连接本地核心");
      return;
    }
  }

  state.demoReason = demoReason;
  state.banner = demoReason;

  if (boot && boot.setup_required && !skipSetup) {
    mountSetup(app, {
      api: api,
      demo: ctx.demo,
      onDone: function (data) {
        applyBootstrap(data);
        mountShell();
        startEvents();
        if (!location.hash) location.hash = defaultRouteHash();
        renderRoute(false);
      },
      onSkip: function () {
        toast("已跳过配置：可在「设置」里补齐 Key 与预算；创建部门前必须补齐", "warn", 5200);
        state.setupSkipped = true;
      },
      toast: toast
    });
    return;
  }

  if (!ctx.demo && boot && boot.authenticated === false && !skipSetup) {
    mountLogin("还没有建立本机会话");
    return;
  }

  applyBootstrap(boot);
  mountShell();
  startEvents();
  if (!location.hash) location.hash = defaultRouteHash();
  renderRoute(false);
}

// 默认落到助理私聊（没有则第一条会话）
function defaultRouteHash() {
  let preferred = null;
  for (let i = 0; i < state.conversations.length; i++) {
    if (state.conversations[i].type === "dm") { preferred = state.conversations[i]; break; }
  }
  if (!preferred) preferred = state.conversations[0] || null;
  return preferred ? "#/chat/" + preferred.id : "#/chat";
}

function applyBootstrap(b) {
  if (!b) return;
  state.version = b.version || "";
  state.modelMode = b.model_mode || "";
  state.searchIndexState = b.search_index_state || "ready";
  state.csrf = b.csrf_token || "";
  state.owner = b.owner || null;
  state.settings = b.settings || null;
  state.agents = b.agents || [];
  state.departments = b.departments || [];
  state.conversations = b.conversations || [];
  state.conversations.forEach(function (c) {
    if (typeof c.unread_count === "number" && c.unread_count > 0) state.unread.set(c.id, c.unread_count);
    if (typeof c.last_seq === "number") state.lastRead.set(c.id, Math.max(state.lastRead.get(c.id) || 0, 0));
  });
  emit("conversations:changed", {});
}

function ownerName() {
  if (state.owner && state.owner.name) return state.owner.name;
  if (ctx.demo) return "阿哲";
  return "项目主管";
}

// ---------- 外壳 ----------
function mountShell() {
  clear(app);

  const convList = el("div", { class: "conv-list", attrs: { "aria-label": "会话列表" } });
  const navHost = el("nav", { class: "nav", attrs: { "aria-label": "主导航" } });
  const connDot = el("span", { class: "conn", attrs: { title: "实时连接状态", "aria-label": "实时连接状态" } });
  const meName = el("div", { class: "me-name", text: ownerName() });
  const meRole = el("div", { class: "me-role", text: ctx.demo ? "演示模式 · 数据不落库" : "项目主管 · 单机" });

  const sidebar = el("aside", { class: "sidebar" },
    el("div", { class: "brand" },
      el("div", { class: "brand-mark", text: "L" }),
      el("div", null,
        el("div", { class: "brand-name", text: "Lignting Agents" }),
        el("div", { class: "brand-ver", text: "v0.0.1" + (state.version ? " · " + state.version : "") })
      ),
      el("div", { class: "brand-spacer" })
    ),
    navHost,
    el("div", { class: "conv-section" },
      el("div", { class: "conv-head" },
        el("span", { text: "会话" }),
        el("span", { class: "grow" }),
        el("button", {
          class: "btn btn-ghost btn-icon", attrs: { type: "button", title: "刷新会话列表", "aria-label": "刷新会话列表" },
          on: { click: function () { reloadConversations(); } }
        }, icon("retry", 14))
      ),
      convList
    ),
    el("div", { class: "me" },
      el("div", { class: "me-ava", text: initials(ownerName()) }),
      el("div", { class: "me-main" }, meName, meRole),
      connDot
    )
  );

  const topbar = el("header", { class: "topbar" });
  const content = el("div", { class: "content", attrs: { id: "content" } });
  const composer = el("div", { class: "composer-wrap", attrs: { id: "composer-host" } });
  composer.style.display = "none";

  const main = el("section", { class: "main" }, topbar, content, composer);
  app.appendChild(el("div", { class: "app" }, sidebar, main));

  ctx.regions = { topbar: topbar, content: content, composer: composer, convList: convList, connDot: connDot, navHost: navHost };

  renderNav();
  renderConvList();
  updateConn(state.sse.status);

  on("conversations:changed", renderConvList);
  on("unread:changed", renderConvList);
  on("active:changed", renderConvList);
  on("conv:updated", renderConvList);
  on("agents:changed", function () { renderNav(); });
}

function renderNav() {
  const host = ctx.regions.navHost;
  clear(host);
  NAV.forEach(function (item) {
    const key = item[0];
    const active = state.route.name === key;
    const btn = el("button", {
      class: "nav-item" + (active ? " active" : ""),
      attrs: { type: "button", "aria-current": active ? "page" : "false" },
      on: { click: function () { navigate(pageHash(key)); } }
    },
      el("span", { class: "nav-ico" }, icon(item[2], 17)),
      el("span", { class: "nav-label", text: item[1] })
    );
    if (key === "chat") {
      const total = unreadTotal();
      if (total > 0) btn.appendChild(el("span", { class: "nav-badge", text: String(total > 99 ? "99+" : total) }));
    }
    if (key === "approvals" && state.approvals && state.approvals.items) {
      const pending = state.approvals.items.filter(function (a) { return a.status === "pending"; }).length;
      if (pending > 0) btn.appendChild(el("span", { class: "nav-badge quiet", text: String(pending) }));
    }
    host.appendChild(btn);
  });
}

function pageHash(key) {
  if (key === "chat") {
    const id = state.activeConvId || (state.conversations[0] && state.conversations[0].id) || "";
    return id ? "#/chat/" + id : "#/chat";
  }
  return "#/" + key;
}

// ---------- 登录（v0.0.1 单用户：本地自动会话） ----------
function mountLogin(reason) {
  clear(app);
  const err = el("div", { class: "err", attrs: { role: "alert" } });
  const btn = el("button", { class: "btn btn-primary btn-block", attrs: { type: "button" } }, "登录本机");
  const card = el("div", { class: "wizard-card", attrs: { style: "width:min(460px,100%)" } },
    el("div", { class: "wizard-head" },
      el("div", { class: "wizard-kicker" }, "本机会话"),
      el("h1", { text: "需要登录" }),
      el("p", { text: reason || "会话已失效" })
    ),
    el("div", { class: "wizard-body" },
      el("div", { class: "notice" },
        "v0.0.1 只有一个 owner，登录只建立本机会话（HttpOnly Cookie + CSRF），不需要邮箱或密码。"),
      err
    ),
    el("div", { class: "wizard-foot" },
      el("span", { class: "ghost-hint", text: ctx.demo ? "演示模式不需要登录" : "首次使用若未初始化，会进入向导" }),
      btn
    )
  );
  app.appendChild(el("div", { class: "wizard" }, card));
  btn.addEventListener("click", async function () {
    btn.disabled = true;
    btn.textContent = "正在登录…";
    err.textContent = "";
    try {
      await createLiveApi().login({});
      location.reload();
    } catch (e) {
      btn.disabled = false;
      btn.textContent = "登录本机";
      err.textContent = "登录失败：" + (e && e.message ? e.message : "未知错误");
      err.style.color = "var(--bad)";
    }
  });
  btn.focus();
}

const CONV_GROUPS = [
  ["直接", ["dm"]],
  ["项目与跨部门", ["project", "cross_dept"]],
  ["部门", ["department"]],
  ["线程与日志", ["task_thread", "artifact_thread", "decision_log", "temp"]]
];

function renderConvList() {
  if (!ctx.regions) return;
  const host = ctx.regions.convList;
  clear(host);

  if (!state.conversations.length) {
    host.appendChild(el("div", { class: "conv-group-label", text: "暂无会话，等待后端初始化" }));
    return;
  }

  CONV_GROUPS.forEach(function (group) {
    const items = state.conversations.filter(function (c) { return group[1].indexOf(c.type) >= 0; });
    if (!items.length) return;
    host.appendChild(el("div", { class: "conv-group-label", text: group[0] }));
    items.sort(function (a, b) {
      const av = a.last_message ? new Date(a.last_message.created_at).getTime() : 0;
      const bv = b.last_message ? new Date(b.last_message.created_at).getTime() : 0;
      return bv - av;
    });
    items.forEach(function (c) {
      const unread = state.unread.get(c.id) || 0;
      let preview = c.last_message && c.last_message.preview ? c.last_message.preview : "";
      if (!preview && c.last_message && c.last_message.content) preview = previewOf(c.last_message.content);
      if (!preview) preview = c.topic || "还没有消息";
      const time = c.last_message && c.last_message.created_at ? relTime(c.last_message.created_at) : "";
      const active = state.activeConvId === c.id;
      host.appendChild(el("button", {
        class: "conv-item" + (active ? " active" : "") + (unread > 0 ? " unread" : ""),
        attrs: { type: "button", "aria-current": active ? "true" : "false" },
        on: { click: function () { navigate("#/chat/" + c.id); } }
      },
        convAvatar(c),
        el("div", { class: "conv-main" },
          el("div", { class: "conv-top" },
            el("span", { class: "conv-title", text: c.title }),
            unread > 0 ? el("span", { class: "unread-dot", attrs: { "aria-label": unread + " 条未读" } }) : null,
            el("span", { class: "conv-time", text: time })
          ),
          el("div", { class: "conv-preview", text: preview })
        )
      ));
    });
  });
}

function updateConn(status, detail) {
  const dot = ctx.regions && ctx.regions.connDot;
  if (!dot) return;
  dot.className = "conn" + (status === "open" ? " open" : status === "connecting" ? " connecting" : status === "bad" ? " bad" : "");
  dot.title = "实时连接：" + (status === "open" ? "已连接" : status === "connecting" ? (detail || "重连中") : status === "bad" ? (detail || "断开") : "未连接");
}

// ---------- 路由 ----------
function renderRoute(force) {
  if (!ctx.regions) return;
  const r = parseHash();
  state.route = r;
  renderNav();

  const view = ROUTES[r.name] || ROUTES.chat;
  if (currentView && currentView.destroy) {
    try { currentView.destroy(); } catch (e) { console.error(e); }
  }
  currentView = null;

  clear(ctx.regions.topbar);
  clear(ctx.regions.content);
  clear(ctx.regions.composer);
  ctx.regions.composer.style.display = "none";

  try {
    currentView = view.mount(ctx, r) || null;
  } catch (e) {
    console.error("[lignting] 视图渲染失败", e);
    ctx.regions.content.appendChild(renderErrorCard(e, "页面渲染失败", function () { renderRoute(true); }));
  }
}

// ---------- 实时事件 ----------
let loginShown = false;

function startEvents() {
  on("auth:required", function () {
    if (ctx.demo || loginShown) return;
    loginShown = true;
    if (currentView && currentView.destroy) { try { currentView.destroy(); } catch (e) { /* ignore */ } }
    currentView = null;
    mountLogin("会话已过期或尚未登录");
  });

  if (ctx.demo) {
    const off = ctx.api.subscribeEvents(handleEvent);
    state.sse.status = "open";
    updateConn("open");
    stream = {
      close: off,
      reconnect: function () {},
      setLastSeq: function () {},
      getLastSeq: function () { return 0; }
    };
    return;
  }

  stream = createEventStream({
    since: state.sse.lastSeq,
    onEvent: handleEvent,
    onStatus: function (s) {
      state.sse.status = s.status;
      updateConn(s.status, s.detail);
    },
    onGap: function (g) {
      toast("检测到事件缺口（seq " + g.from + " → " + g.to + "），已按游标补齐消息", "warn", 5000);
      backfill();
    }
  });
  stream.open();

  document.addEventListener("visibilitychange", function () {
    if (document.visibilityState === "visible" && state.sse.status !== "open" && stream) {
      stream.reconnect();
    }
  });
}

function handleEvent(ev) {
  if (!ev) return;
  const d = ev.data || {};
  const type = ev.type || d.type || "";
  const payload = parsePayload(d.payload);
  const convId = d.conversation_id || payload.conversation_id || "";

  if (typeof d.seq === "number" && stream) stream.setLastSeq(d.seq);
  if (typeof d.seq === "number") state.sse.lastSeq = Math.max(state.sse.lastSeq, d.seq);

  switch (type) {
    case "message.created": {
      const m = payload.message;
      if (m) {
        upsertMessage(m.conversation_id || convId, m);
        touchConvFromMessage(m);
        if ((m.conversation_id || convId) !== state.activeConvId && m.sender_type !== "user") {
          bumpUnread(m.conversation_id || convId);
        } else {
          scheduleRead();
        }
      } else if (convId) {
        refreshConversation(convId);
      }
      break;
    }
    case "message.delta": {
      applyStreamDelta(payload.message_id, payload.conversation_id || convId, payload.delta, payload.index);
      break;
    }
    case "message.completed": {
      const m = payload.message;
      if (m) {
        upsertMessage(m.conversation_id || convId, m);
        touchConvFromMessage(m);
      }
      if (payload.message_id) clearStream(payload.message_id);
      if (convId) refreshConversation(convId);
      break;
    }
    case "message.updated": {
      const m = payload.message;
      if (m) { upsertMessage(m.conversation_id || convId, m); touchConvFromMessage(m); }
      else if (convId) refreshConversation(convId);
      break;
    }
    case "message.deleted": {
      if (convId && typeof payload.seq === "number") removeMessage(convId, payload.seq);
      else if (convId) refreshConversation(convId);
      break;
    }
    case "conversation.created": {
      reloadConversations();
      break;
    }
    case "agent.status": {
      updateAgentStatus(payload);
      break;
    }
    case "task.updated":
    case "approval.updated":
    case "write.updated":
    case "notification.created": {
      emit("data:stale", { type: type, payload: payload });
      break;
    }
    default: break;
  }
}

function updateAgentStatus(payload) {
  const id = payload.agent_id;
  if (!id) return;
  let found = null;
  for (let i = 0; i < state.agents.length; i++) {
    if (state.agents[i].id === id) { found = state.agents[i]; break; }
  }
  if (found) {
    found.status = payload.status || found.status;
    emit("agents:changed", { agentId: id });
  }
}

function touchConvFromMessage(m) {
  const c = convById(m.conversation_id);
  if (!c) return;
  if (!c.last_message || m.seq >= (c.last_message.seq || 0)) {
    c.last_seq = m.seq;
    c.last_message = {
      seq: m.seq,
      preview: String(m.content || "").replace(/\s+/g, " ").slice(0, 90),
      sender_name: m.sender_name,
      created_at: m.created_at
    };
  }
  emit("conv:updated", { convId: c.id });
}

function bumpUnread(convId) {
  const c = convById(convId);
  const next = (state.unread.get(convId) || 0) + 1;
  setUnread(convId, next);
  if (c) { c.unread_count = next; }
  emit("conv:updated", { convId: convId });
}

function scheduleRead() {
  const convId = state.activeConvId;
  if (!convId) return;
  clearTimeout(readTimer);
  readTimer = setTimeout(function () { markRead(convId); }, 800);
}

function markRead(convId) {
  const b = bucket(convId);
  setUnread(convId, 0);
  const c = convById(convId);
  if (c) c.unread_count = 0;
  const seq = b.newestSeq || (c && c.last_seq) || 0;
  state.lastRead.set(convId, seq);
  if (seq > 0) {
    ctx.api.markRead(convId, seq).catch(function (e) {
      // 已读上报失败不阻塞阅读；下一次打开会重试
      if (!(e instanceof ApiError && e.code === "not_implemented")) console.warn("[lignting] 已读上报失败", e);
    });
  }
}

function refreshConversation(convId) {
  if (!convId) return;
  const active = state.activeConvId === convId;
  if (!active) {
    // 非当前会话：只更新左侧预览，避免 N+1
    const c = convById(convId);
    if (c) emit("conv:updated", { convId: convId });
    return;
  }
  ctx.api.messages(convId, { limit: 50 }).then(function (res) {
    const items = (res && res.items) || [];
    ingestMessages(convId, items, { replace: false, hasMore: !!(res && res.next) });
  }).catch(function (e) {
    if (!(e instanceof ApiError && e.code === "not_implemented")) console.warn("[lignting] 补齐会话消息失败", e);
  });
}

function backfill() {
  if (state.activeConvId) refreshConversation(state.activeConvId);
  reloadConversations();
}

function reloadConversations() {
  return ctx.api.conversations().then(function (res) {
    state.conversations = (res && res.items) || [];
    state.conversations.forEach(function (c) {
      if (typeof c.unread_count === "number") {
        if (c.unread_count > 0) setUnread(c.id, c.unread_count);
      }
    });
    emit("conversations:changed", {});
    return state.conversations;
  }).catch(function (e) {
    if (!(e instanceof ApiError && e.code === "not_implemented")) {
      toast("会话列表加载失败：" + e.message, "bad");
    }
    return [];
  });
}

function gotoMessage(convId, refId) {
  navigate("#/chat/" + encodeURIComponent(convId) + "?msg=" + encodeURIComponent(refId));
}

// ---------- 错误屏 ----------
function renderFatal(err, title) {
  clear(app);
  const code = err && err.code ? err.code : "";
  const msg = err && err.message ? err.message : String(err);
  app.appendChild(el("div", { class: "fatal", attrs: { role: "alert" } },
    el("h2", { text: title || "启动失败" }),
    el("p", { text: msg }),
    code ? el("p", { class: "card-meta", text: "错误码：" + code }) : null,
    el("div", { class: "row", attrs: { style: "margin-top:14px" } },
      el("button", {
        class: "btn btn-primary", attrs: { type: "button" },
        on: { click: function () { location.reload(); } }
      }, "重新加载"),
      el("button", {
        class: "btn", attrs: { type: "button" },
        on: { click: function () { location.search = "?demo=1"; } }
      }, "以演示模式打开"),
      err && err.status === 401 ? el("button", {
        class: "btn", attrs: { type: "button" },
        on: {
          click: async function () {
            try { await createLiveApi().login({}); location.reload(); }
            catch (e2) { toast(e2.message || "登录失败", "bad"); }
          }
        }
      }, "重新登录") : null
    ),
    el("p", { class: "card-meta", attrs: { style: "margin-top:14px" },
      text: "排查建议：确认 lignting 进程仍在运行；检查端口与 --data-dir 权限；查看终端日志。" })
  ));
}

function renderErrorCard(err, title, onRetry) {
  return el("div", { class: "card" },
    el("div", { class: "card-title", text: title || "加载失败" }),
    el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: (err && err.message) || String(err) }),
    err && err.code ? el("div", { class: "card-meta", text: "错误码：" + err.code }) : null,
    el("div", { class: "row", attrs: { style: "margin-top:10px" } },
      el("button", {
        class: "btn btn-sm", attrs: { type: "button" },
        on: { click: function () { if (onRetry) onRetry(); } }
      }, "重试"))
  );
}

// ---------- 启动 ----------
window.addEventListener("hashchange", onHashChange);
boot().catch(function (e) {
  console.error(e);
  renderFatal(e, "未捕获的启动错误");
});
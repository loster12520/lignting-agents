// 聊天页：消息流、两级线程、结构化卡片、Markdown、输入框、流式/停止/重试、未读与定位。
// 需求：FR-CHAT-02/03/04/05/06/14；R1 交付持久化读取 + R2 交付流式。
import { el, clear, icon, badge, emptyState, notice, toast, fmtTime, fmtDay, fmtDateTime, truncate, initials } from "../lib/dom.js";
import {
  state, on, emit, convById, bucket, ingestMessages, setUnread, previewOf
} from "../lib/store.js";
import { renderMarkdown } from "../lib/mdrender.js";
import { renderCard, avatar, statusBadge, CONV_TYPE, VISIBILITY, MSG_TYPE, msgStateText, AGENT_STATUS } from "../lib/ui.js";

function newClientId() {
  try {
    if (window.crypto && crypto.randomUUID) return crypto.randomUUID();
  } catch (e) { /* 降级 */ }
  return "c-" + Date.now().toString(36) + "-" + Math.random().toString(36).slice(2, 10);
}

export function mount(ctx, route) {
  const convId = route.params.id || state.activeConvId || (state.conversations[0] && state.conversations[0].id) || "";
  const conv = convById(convId);
  state.activeConvId = convId;
  emit("active:changed", { convId: convId });

  let disposed = false;
  let sending = false;
  let pendingContent = "";
  let pendingClientId = "";
  let nearBottom = true;
  let loadOlderBusy = false;
  let streamStopRequested = false;
  const unsubs = [];

  const b = bucket(convId);
  const readonly = conv && conv.type === "decision_log";

  // ---------- 顶栏 ----------
  clear(ctx.regions.topbar);
  const titleNode = el("div", { class: "topbar-title", text: conv ? conv.title : "会话" });
  const subNode = el("div", { class: "topbar-sub" });
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" }, titleNode, subNode));
  const actions = el("div", { class: "topbar-actions" });
  ctx.regions.topbar.appendChild(actions);

  function renderTopbarSub() {
    clear(subNode);
    if (!conv) return;
    const t = CONV_TYPE[conv.type];
    if (t) subNode.appendChild(badge(t[0], t[1]));
    if (conv.visibility) subNode.appendChild(el("span", { text: VISIBILITY[conv.visibility] || conv.visibility }));
    if (conv.topic) subNode.appendChild(el("span", { text: conv.topic }));
    if (ctx.demo) subNode.appendChild(badge("演示数据", "warn"));
    if (state.searchIndexState === "building") subNode.appendChild(badge("索引更新中", "warn"));
  }
  renderTopbarSub();

  actions.appendChild(el("button", {
    class: "btn btn-sm", attrs: { type: "button", title: "刷新当前会话" },
    on: { click: function () { load(true); } }
  }, icon("retry", 14), el("span", { text: "刷新" })));

  // ---------- 消息区 ----------
  clear(ctx.regions.content);
  const msgs = el("div", { class: "msgs", attrs: { role: "log", "aria-live": "polite", "aria-relevant": "additions" } });
  const jumpBtn = el("button", {
    class: "jump-latest", attrs: { type: "button", style: "display:none" },
    on: { click: function () { scrollToBottom(true); } }
  }, icon("down", 14), el("span", { text: "跳到最新" }));
  const pageWrap = el("div", null, msgs, el("div", { attrs: { style: "display:flex;justify-content:center;padding:0 0 14px" } }, jumpBtn));
  ctx.regions.content.appendChild(pageWrap);
  ctx.regions.content.addEventListener("scroll", onScroll);

  function onScroll() {
    const host = ctx.regions.content;
    nearBottom = host.scrollHeight - host.scrollTop - host.clientHeight < 90;
    jumpBtn.style.display = nearBottom ? "none" : "";
  }

  function scrollToBottom(smooth) {
    const host = ctx.regions.content;
    host.scrollTo({ top: host.scrollHeight, behavior: smooth ? "smooth" : "auto" });
    nearBottom = true;
    jumpBtn.style.display = "none";
  }

  // ---------- 消息渲染 ----------
  function streamingFor(convId2) {
    const out = [];
    state.streams.forEach(function (s, id) {
      if (s.convId === convId2) out.push({ id: id, s: s });
    });
    return out;
  }

  function renderMessages() {
    const keepBottom = nearBottom;
    const host = ctx.regions.content;
    const prevHeight = host.scrollHeight;
    const prevTop = host.scrollTop;
    clear(msgs);

    if (!conv) {
      msgs.appendChild(emptyState({
        icon: "chat",
        title: "选择一个会话",
        desc: "首次启动后，助理会在私聊里主动介绍自己；也可以从左栏进入部门群聊旁观。"
      }));
      return;
    }

    if (!b.loaded) {
      msgs.appendChild(el("div", { class: "empty" }, el("span", { class: "spinner" }), el("p", { text: "正在加载消息…" })));
      return;
    }

    if (!b.items.length && !streamingFor(convId).length) {
      msgs.appendChild(emptyState({
        icon: "chat",
        title: "还没有消息",
        desc: readonly ? "这是一个只读流，核心写入后会自动出现在这里。" : "在下面输入第一条消息；消息会先落库再推送，刷新不会丢。"
      }));
      return;
    }

    // 线程归组：thread_id 指向父消息 id（或 seq 字符串）
    const byId = new Map();
    b.items.forEach(function (m) { byId.set(m.id, m); byId.set(String(m.seq), m); });
    const children = new Map();
    const roots = [];
    b.items.forEach(function (m) {
      const parent = m.thread_id ? byId.get(m.thread_id) : null;
      if (parent && parent !== m) {
        if (!children.has(parent.id)) children.set(parent.id, []);
        children.get(parent.id).push(m);
      } else {
        roots.push(m);
      }
    });

    if (b.hasMore) {
      msgs.appendChild(el("div", { attrs: { style: "display:flex;justify-content:center;padding:8px 0" } },
        el("button", {
          class: "btn btn-sm", attrs: { type: "button" },
          on: { click: function () { loadOlder(); } }
        }, loadOlderBusy ? "加载中…" : "加载更早消息")
      ));
    }

    let lastDay = "";
    roots.forEach(function (m) {
      const day = fmtDay(m.created_at);
      if (day !== lastDay) {
        lastDay = day;
        msgs.appendChild(el("div", { class: "day-sep" }, el("span", { text: day })));
      }
      msgs.appendChild(messageNode(m, children.get(m.id) || [], 0));
    });

    streamingFor(convId).forEach(function (item) {
      msgs.appendChild(streamNode(item.id, item.s));
    });

    renderTopbarSub();

    if (keepBottom) scrollToBottom(false);
    else {
      host.scrollTop = prevTop + (host.scrollHeight - prevHeight);
    }
  }

  function senderClass(m) {
    if (m.sender_type === "user") return "owner";
    if (m.sender_type === "agent") return "agent";
    return "system";
  }

  function messageNode(m, kids, depth) {
    const deleted = !!m.deleted_at;
    const node = el("div", {
      class: "msg" + (deleted ? " deleted" : "") + (m.type === "system" ? " system" : ""),
      dataset: { id: m.id, seq: String(m.seq) },
      attrs: { id: "m-" + m.id }
    });
    node.appendChild(avatar(m.sender_name || "?", m.sender_type));

    const body = el("div", { class: "msg-body" });
    const head = el("div", { class: "msg-head" },
      el("span", { class: "msg-name", text: m.sender_name || (m.sender_type === "user" ? "我" : "Agent") }),
      el("span", { class: "msg-time", text: fmtTime(m.created_at), attrs: { title: fmtDateTime(m.created_at) } })
    );
    if (m.type && m.type !== "text" && MSG_TYPE[m.type]) head.appendChild(el("span", { class: "msg-kind", text: MSG_TYPE[m.type] }));
    if (m.edited_at) head.appendChild(el("span", { class: "msg-time", text: "已编辑" }));
    body.appendChild(head);

    const contentHost = el("div", { class: "msg-content" });
    if (deleted) {
      contentHost.textContent = "消息已删除（保留审计墓碑）";
    } else if (m.type === "card") {
      contentHost.appendChild(renderCard(m, {
        navigate: ctx.navigate,
        toast: ctx.toast,
        refresh: function () { load(true); },
        api: ctx.api
      }));
      if (m.content) contentHost.appendChild(el("div", { class: "msg-content", attrs: { style: "margin-top:6px" } }, renderMarkdown(m.content)));
    } else if (m.type === "tool_summary") {
      contentHost.appendChild(el("div", { class: "mcard" },
        el("div", { class: "mcard-head" }, badge("工具调用", ""), el("span", { class: "mcard-title", text: truncate(m.content, 80) })),
        el("div", { class: "mcard-body" }, el("div", { class: "mcard-kv" }, el("b", { text: "状态" }), el("span", { text: "摘要已折叠（展开为 R4 增强）" })))
      ));
    } else if (m.type === "system") {
      contentHost.appendChild(renderMarkdown(m.content || ""));
    } else {
      contentHost.appendChild(renderMarkdown(m.content || ""));
    }
    body.appendChild(contentHost);

    // 线程回复
    if (kids.length) {
      body.appendChild(threadBlock(kids, depth));
    }

    // 悬浮动作
    const tools = el("div", { class: "msg-tools" });
    const act = el("div", { class: "msg-actions" });
    act.appendChild(el("button", {
      class: "msg-tool-btn", attrs: { type: "button", title: "复制消息链接" },
      on: {
        click: async function () {
          const link = location.origin + "/#/chat/" + convId + "?msg=" + encodeURIComponent(m.id);
          try { await navigator.clipboard.writeText(link); toast("已复制消息链接", "ok"); }
          catch (e) { toast("复制失败：" + e.message, "bad"); }
        }
      }
    }, "复制链接"));
    if (m.type === "card" && m.meta) {
      act.appendChild(el("button", {
        class: "msg-tool-btn", attrs: { type: "button", title: "查看卡片原始字段" },
        on: { click: function () { toast(truncate(m.meta, 220), "", 6000); } }
      }, "卡片字段"));
    }
    tools.appendChild(act);
    body.appendChild(tools);

    node.appendChild(body);
    return node;
  }

  function threadBlock(kids, depth) {
    const wrap = el("div", { class: "thread" });
    const label = el("button", {
      class: "thread-toggle", attrs: { type: "button", "aria-expanded": "false" }
    }, "查看 " + kids.length + " 条回复");
    const host = el("div", { style: { display: "none" } });
    label.addEventListener("click", function () {
      const open = label.getAttribute("aria-expanded") === "true";
      label.setAttribute("aria-expanded", open ? "false" : "true");
      label.textContent = (open ? "查看 " : "收起 ") + kids.length + " 条回复";
      host.style.display = open ? "none" : "";
    });
    kids.forEach(function (k) {
      host.appendChild(messageNode(k, [], depth + 1));
    });
    wrap.appendChild(label);
    wrap.appendChild(host);
    return wrap;
  }

  function streamNode(msgId, s) {
    const node = el("div", { class: "msg", dataset: { stream: msgId } });
    node.appendChild(avatar("小灯", "agent"));
    const body = el("div", { class: "msg-body" });
    body.appendChild(el("div", { class: "msg-head" },
      el("span", { class: "msg-name", text: "小灯" }),
      el("span", { class: "msg-time", text: "生成中" })
    ));
    const host = el("div", { class: "msg-content" });
    host.appendChild(renderMarkdown(s.text || ""));
    if (s.state === "streaming") host.appendChild(el("span", { class: "streaming-caret" }));
    else if (s.state === "stopped") host.appendChild(el("div", { class: "msg-state stop", text: msgStateText("stopped") }));
    else if (s.state === "failed") host.appendChild(el("div", { class: "msg-state bad", text: msgStateText("failed", s.error) }));
    body.appendChild(host);
    body.appendChild(el("div", { class: "msg-tools" },
      el("span", { text: s.state === "streaming" ? "流式输出中（演示/真实均由 message.delta 驱动）" : "" })
    ));
    node.appendChild(body);
    return node;
  }

  // ---------- 输入区 ----------
  clear(ctx.regions.composer);
  ctx.regions.composer.style.display = "";
  const ta = el("textarea", {
    class: "composer-input", attrs: {
      rows: "1", placeholder: readonly ? "决策日志为只读流" : "发消息…（Enter 发送，Shift+Enter 换行）",
      "aria-label": "消息输入框", disabled: readonly
    }
  });
  const sendBtn = el("button", {
    class: "btn btn-primary btn-icon", attrs: { type: "button", title: "发送（Enter）", "aria-label": "发送", disabled: readonly },
    on: { click: function () { send(); } }
  }, icon("send", 16));
  const stopBtn = el("button", {
    class: "btn btn-danger btn-sm", attrs: { type: "button", style: "display:none", title: "停止当前生成" },
    on: { click: function () { stopGeneration(); } }
  }, icon("stop", 14), el("span", { text: "停止" }));

  const statusLine = el("div", { class: "composer-status", attrs: { "aria-live": "polite" } });
  const hint = el("span", { class: "composer-hint" },
    readonly ? "只读" : "Enter 发送 · Shift+Enter 换行 · 消息先落库再推送"
  );

  const composer = el("div", { class: "composer" },
    el("div", { class: "composer-box" + (readonly ? " readonly" : "") },
      ta,
      el("div", { class: "composer-bar" },
        el("span", { class: "composer-hint", text: readonly ? "" : "Markdown 基础语法" }),
        el("span", { class: "grow" }),
        stopBtn,
        sendBtn
      )
    ),
    statusLine,
    el("div", { class: "composer-bar" }, hint)
  );
  ctx.regions.composer.appendChild(composer);

  let composing = false;
  ta.addEventListener("compositionstart", function () { composing = true; });
  ta.addEventListener("compositionend", function () { composing = false; });
  ta.addEventListener("input", function () {
    ta.style.height = "auto";
    ta.style.height = Math.min(ta.scrollHeight, 180) + "px";
    updateSendState();
  });
  ta.addEventListener("keydown", function (e) {
    // 中文输入法组合态下 Enter 只上屏，不发送
    if (e.key === "Enter" && !e.shiftKey) {
      if (composing || e.isComposing) return;
      e.preventDefault();
      send();
    }
  });

  function updateSendState() {
    const hasText = ta.value.trim().length > 0;
    sendBtn.disabled = readonly || sending || !hasText;
    const streamCount = streamingFor(convId).length;
    stopBtn.style.display = streamCount > 0 ? "" : "none";
  }

  function setStatus(text, kind) {
    clear(statusLine);
    if (!text) return;
    statusLine.className = "composer-status" + (kind ? " " + kind : "");
    statusLine.appendChild(el("span", { class: "grow", text: text }));
  }

  async function send() {
    if (readonly || sending) return;
    const content = ta.value.trim();
    if (!content) return;

    sending = true;
    pendingContent = content;
    if (!pendingClientId) pendingClientId = newClientId();
    updateSendState();
    setStatus("正在保存到本地库…");

    try {
      const m = await ctx.api.sendMessage(convId, {
        content: content,
        type: "text",
        thread_id: "",
        meta: "{}",
        client_id: pendingClientId
      });
      if (disposed) return;
      if (m && typeof m.seq === "number") ingestMessages(convId, [m], {});
      ta.value = "";
      ta.style.height = "auto";
      pendingContent = "";
      pendingClientId = "";
      setStatus("");
      nearBottom = true;
      scrollToBottom(true);
      markReadNow();
    } catch (e) {
      if (disposed) return;
      setStatus("发送失败：" + (e.message || "未知错误"), "error");
      clear(statusLine);
      statusLine.className = "composer-status error";
      statusLine.appendChild(el("span", { class: "grow", text: "发送失败：" + (e.message || "未知错误") + "（内容已保留）" }));
      statusLine.appendChild(el("button", {
        class: "btn btn-sm", attrs: { type: "button" },
        on: { click: function () { send(); } }
      }, "重试"));
      if (e.code === "not_implemented") {
        ctx.toast("后端发消息接口未实现（R1 进行中）", "warn");
      }
    } finally {
      sending = false;
      updateSendState();
    }
  }

  async function stopGeneration() {
    streamStopRequested = true;
    const assistant = state.agents.filter(function (a) { return a.kind === "assistant"; })[0];
    try {
      if (!assistant) throw new Error("未找到助理 Agent");
      await ctx.api.agentAction(assistant.id, "stop");
      ctx.toast("已请求停止", "ok");
    } catch (e) {
      ctx.toast("停止失败：" + e.message, "bad");
    }
    // 本地立即反映（后端 message.completed/turn 事件到达后会覆盖）
    state.streams.forEach(function (s, id) {
      if (s.convId === convId && s.state === "streaming") {
        s.state = "stopped";
        emit("stream:changed", { convId: convId, messageId: id, ended: true });
      }
    });
  }

  function markReadNow() {
    setUnread(convId, 0);
    const c = convById(convId);
    if (c) c.unread_count = 0;
    const seq = b.newestSeq || (c && c.last_seq) || 0;
    if (seq > 0) {
      ctx.api.markRead(convId, seq).catch(function () { /* 已读是尽力而为 */ });
    }
    emit("unread:changed", { convId: convId });
  }

  // ---------- 数据加载 ----------
  async function load(silent) {
    if (!convId) return;
    if (!silent) {
      b.loaded = false;
      renderMessages();
    }
    try {
      const res = await ctx.api.messages(convId, { limit: 50 });
      if (disposed) return;
      const items = (res && res.items) || [];
      b.items = [];
      b.oldestSeq = 0; b.newestSeq = 0; b.hasMore = false;
      ingestMessages(convId, items, { replace: true, hasMore: !!(res && res.next) });
      if (!items.length) b.loaded = true;
      renderMessages();
      nearBottom = true;
      scrollToBottom(false);
      markReadNow();
      highlightTarget();
    } catch (e) {
      if (disposed) return;
      b.loaded = true;
      clear(msgs);
      msgs.appendChild(el("div", { class: "card", attrs: { style: "margin:16px 0" } },
        el("div", { class: "card-title", text: "消息加载失败" }),
        notice(e.message || String(e), "bad"),
        el("div", { class: "row", attrs: { style: "margin-top:10px" } },
          el("button", {
            class: "btn btn-sm", attrs: { type: "button" },
            on: { click: function () { load(false); } }
          }, "重试"))
      ));
      if (e.code !== "not_implemented") ctx.toast("消息加载失败：" + e.message, "bad");
    }
  }

  async function loadOlder() {
    if (loadOlderBusy) return;
    const before = b.oldestSeq;
    if (!before) return;
    loadOlderBusy = true;
    renderMessages();
    try {
      const res = await ctx.api.messages(convId, { before: before, limit: 50 });
      if (disposed) return;
      const items = (res && res.items) || [];
      ingestMessages(convId, items, { hasMore: !!(res && res.next) });
      if (!items.length) b.hasMore = false;
    } catch (e) {
      ctx.toast("加载更早消息失败：" + e.message, "bad");
    } finally {
      loadOlderBusy = false;
      renderMessages();
    }
  }

  function highlightTarget() {
    const target = (route && route.query && route.query.msg) || "";
    if (!target) return;
    const node = document.getElementById("m-" + target) || document.querySelector('[data-id="' + target + '"]');
    if (node) {
      node.classList.add("flash");
      node.scrollIntoView({ block: "center", behavior: "smooth" });
      nearBottom = false;
      jumpBtn.style.display = "";
      setTimeout(function () { node.classList.remove("flash"); }, 1800);
    }
  }

  // ---------- 订阅 ----------
  unsubs.push(on("messages:changed", function (p) {
    if (!p || p.convId !== convId) return;
    renderMessages();
    if (nearBottom) markReadNow();
  }));
  unsubs.push(on("stream:changed", function (p) {
    if (!p || p.convId !== convId) return;
    renderMessages();
    updateSendState();
  }));
  unsubs.push(on("agents:changed", function () { renderTopbarSub(); }));

  load(false);
  updateSendState();

  return {
    destroy: function () {
      disposed = true;
      unsubs.forEach(function (off) { try { off(); } catch (e) { /* ignore */ } });
      if (ctx.regions.content) ctx.regions.content.removeEventListener("scroll", onScroll);
      state.activeConvId = "";
    }
  };
}

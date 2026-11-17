// 全局状态 + 极简事件总线。视图只在事件里做局部更新，避免整页重绘。
import { toPlainText } from "./markdown.js";

export const state = {
  mode: "live",            // live | demo
  demoReason: "",
  version: "",
  modelMode: "",
  searchIndexState: "ready",
  csrf: "",
  owner: null,              // {id, name}
  settings: null,
  agents: [],
  departments: [],
  conversations: [],
  activeConvId: "",
  messages: new Map(),      // convId -> { items: Message[], loaded, hasMore, oldestSeq, newestSeq }
  streams: new Map(),       // messageId -> { convId, text, index, state, error }
  unread: new Map(),        // convId -> number
  lastRead: new Map(),      // convId -> seq
  typing: new Map(),        // convId -> { agentName, startedAt }
  sse: { status: "idle", lastSeq: 0, mode: "live" },
  search: {
    q: "",
    filters: { kind: [], department_id: "", conversation_id: "", sender_id: "", from: "", to: "" },
    items: [],
    total: 0,
    next: "",
    loading: false,
    error: null,
    searched: false
  },
  org: null,
  tasks: null,
  approvals: null,
  memories: null,
  audit: null,
  usage: null,
  route: { name: "chat", params: {} },
  banner: ""
};

const listeners = new Map();

export function on(type, fn) {
  if (!listeners.has(type)) listeners.set(type, new Set());
  listeners.get(type).add(fn);
  return function off() {
    const set = listeners.get(type);
    if (set) set.delete(fn);
  };
}

export function emit(type, payload) {
  const set = listeners.get(type);
  if (set) {
    const arr = Array.from(set);
    for (let i = 0; i < arr.length; i++) {
      try { arr[i](payload); } catch (e) { console.error("[lignting] 事件处理失败", type, (e && e.stack) ? e.stack : e); }
    }
  }
  const all = listeners.get("*");
  if (all) {
    Array.from(all).forEach(function (fn) {
      try { fn(type, payload); } catch (e) { console.error(e); }
    });
  }
}

// ---------- 会话与消息 ----------
export function convById(id) {
  for (let i = 0; i < state.conversations.length; i++) {
    if (state.conversations[i].id === id) return state.conversations[i];
  }
  return null;
}

export function bucket(convId) {
  let b = state.messages.get(convId);
  if (!b) {
    b = { items: [], loaded: false, hasMore: false, oldestSeq: 0, newestSeq: 0 };
    state.messages.set(convId, b);
  }
  return b;
}

export function ingestMessages(convId, items, opts) {
  const b = bucket(convId);
  const options = opts || {};
  const seen = new Set(b.items.map(function (m) { return m.seq; }));
  const incoming = [];
  for (let i = 0; i < items.length; i++) {
    const m = items[i];
    if (!m || typeof m.seq !== "number") continue;
    if (seen.has(m.seq)) continue;
    seen.add(m.seq);
    incoming.push(m);
  }
  b.items = b.items.concat(incoming).sort(function (x, y) { return x.seq - y.seq; });
  b.loaded = options.replace === true ? true : b.loaded;
  if (incoming.length) {
    const seqs = incoming.map(function (m) { return m.seq; });
    const min = Math.min.apply(null, seqs);
    const max = Math.max.apply(null, seqs);
    b.oldestSeq = b.oldestSeq ? Math.min(b.oldestSeq, min) : min;
    b.newestSeq = Math.max(b.newestSeq, max);
  }
  if (options.hasMore !== undefined) b.hasMore = !!options.hasMore;
  if (incoming.length) emit("messages:changed", { convId: convId, added: incoming.length });
  return incoming;
}

export function upsertMessage(convId, msg) {
  const b = bucket(convId);
  for (let i = 0; i < b.items.length; i++) {
    if (b.items[i].seq === msg.seq || b.items[i].id === msg.id) {
      b.items[i] = Object.assign({}, b.items[i], msg);
      if (msg.seq > b.newestSeq) b.newestSeq = msg.seq;
      emit("messages:changed", { convId: convId, updated: 1 });
      return;
    }
  }
  ingestMessages(convId, [msg]);
}

export function removeMessage(convId, seq) {
  const b = bucket(convId);
  const idx = b.items.findIndex(function (m) { return m.seq === seq; });
  if (idx >= 0) {
    b.items[idx] = Object.assign({}, b.items[idx], { deleted_at: new Date().toISOString(), content: "" });
    emit("messages:changed", { convId: convId, updated: 1 });
  }
}

export function applyStreamDelta(msgId, convId, delta, index) {
  let s = state.streams.get(msgId);
  if (!s) {
    s = { convId: convId, text: "", index: -1, state: "streaming", error: "" };
    state.streams.set(msgId, s);
  }
  if (typeof index === "number") {
    // 乱序/重复的 delta 直接丢弃（at-least-once 的重放）
    if (index <= s.index) return false;
    s.index = index;
  }
  s.text += delta || "";
  emit("stream:changed", { convId: convId, messageId: msgId });
  return true;
}

export function endStream(msgId, finalState, error) {
  const s = state.streams.get(msgId);
  if (!s) return;
  s.state = finalState || "completed";
  s.error = error || "";
  emit("stream:changed", { convId: s.convId, messageId: msgId, ended: true });
}

export function clearStream(msgId) {
  const s = state.streams.get(msgId);
  if (!s) return;
  state.streams.delete(msgId);
  emit("stream:changed", { convId: s.convId, messageId: msgId, removed: true });
}

export function previewOf(content) {
  return toPlainText(content || "", 90);
}

export function unreadTotal() {
  let n = 0;
  state.unread.forEach(function (v) { n += v; });
  return n;
}

export function setUnread(convId, n) {
  const next = Math.max(0, n || 0);
  if ((state.unread.get(convId) || 0) === next) return;
  state.unread.set(convId, next);
  emit("unread:changed", { convId: convId });
}
// DOM 构造工具：所有渲染只走 createElement / createTextNode / textContent，
// 代码库内不使用 innerHTML/outerHTML，也不接受事件属性字符串。

const ICONS = {
  chat: "M21 11.5a8.5 8.5 0 0 1-8.5 8.5H7l-4 3v-6.6A8.5 8.5 0 1 1 21 11.5Z",
  search: "M10.5 17a6.5 6.5 0 1 0 0-13 6.5 6.5 0 0 0 0 13Zm10.5 4-4.9-4.9",
  org: "M12 3v6m0 0a3 3 0 1 0 0 6 3 3 0 0 0 0-6Zm-7 9H3v6h6v-6H5Zm13 0h-6v6h6v-6h-2Z",
  task: "M9 6h11M9 12h11M9 18h11M4 6h1M4 12h1M4 18h1",
  approval: "M9 12.5l2 2 4.5-4.5M12 21a9 9 0 1 1 0-18 9 9 0 0 1 0 18Z",
  memory: "M12 4a4 4 0 0 1 4 4v1a3 3 0 0 1 0 6v1a4 4 0 0 1-8 0v-1a3 3 0 0 1 0-6V8a4 4 0 0 1 4-4Zm0 4v8",
  audit: "M8 4h8a2 2 0 0 1 2 2v14l-4-2-2 2-2-2-4 2V6a2 2 0 0 1 2-2Zm1 5h6M9 13h6",
  gear: "M12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7Zm8-3.5a8 8 0 0 0-.2-1.6l2-1.5-2-3.4-2.4 1a8 8 0 0 0-2.7-1.6L14.2 2h-4l-.5 2.9A8 8 0 0 0 7 6.5l-2.4-1-2 3.4 2 1.5a8 8 0 0 0 0 3.2l-2 1.5 2 3.4 2.4-1a8 8 0 0 0 2.7 1.6L10.2 22h4l.5-2.9a8 8 0 0 0 2.7-1.6l2.4 1 2-3.4-2-1.5c.1-.5.2-1.1.2-1.6Z",
  send: "M4 11.5 20 4l-7.5 16-2-6.5-6.5-2Z",
  stop: "M7 7h10v10H7z",
  retry: "M20 11a8 8 0 1 0-2.3 5.7M20 5v6h-6",
  plus: "M12 5v14M5 12h14",
  close: "M6 6l12 12M18 6 6 18",
  down: "M12 5v14M6 13l6 6 6-6",
  lock: "M7 11V8a5 5 0 0 1 10 0v3M5 11h14v10H5z",
  file: "M7 3h7l5 5v13H7zM14 3v5h5",
  shield: "M12 3l8 3v6c0 5-3.4 8.3-8 9-4.6-.7-8-4-8-9V6l8-3Z",
  dot: "M12 12h.01",
  inbox: "M4 13l2.5-8h11L20 13v6H4zM4 13h5l1 2h4l1-2h5",
  branch: "M6 20V6M6 6a2 2 0 1 0 0-4 2 2 0 0 0 0 4Zm0 6a4 4 0 0 0 4 4h8M18 12a2 2 0 1 0 0 4 2 2 0 0 0 0-4Z",
  clock: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Zm0-13v5l3 2"
};

const SVG_NS = "http://www.w3.org/2000/svg";

export function icon(name, size) {
  const px = size || 18;
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", String(px));
  svg.setAttribute("height", String(px));
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.7");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const p = document.createElementNS(SVG_NS, "path");
  p.setAttribute("d", ICONS[name] || ICONS.dot);
  svg.appendChild(p);
  return svg;
}

const BANNED_PROPS = ["innerHTML", "outerHTML", "insertAdjacentHTML"];

// el('div', {class:'x', on:{click:fn}, dataset:{id:'1'}}, '文本', childNode)
export function el(tag, props) {
  const node = document.createElement(tag);
  if (props) applyProps(node, props);
  for (let i = 2; i < arguments.length; i++) appendOne(node, arguments[i]);
  return node;
}

export function frag() {
  const f = document.createDocumentFragment();
  for (let i = 0; i < arguments.length; i++) appendOne(f, arguments[i]);
  return f;
}

function applyProps(node, props) {
  const keys = Object.keys(props);
  for (let i = 0; i < keys.length; i++) {
    const key = keys[i];
    const value = props[key];
    if (BANNED_PROPS.indexOf(key) >= 0) {
      console.warn("[lignting] 禁止通过属性注入 HTML：" + key);
      continue;
    }
    if (value === undefined || value === null || value === false) continue;

    if (key === "class" || key === "className") { node.className = String(value); continue; }
    if (key === "text") { node.textContent = String(value); continue; }
    if (key === "htmlFor") { node.htmlFor = String(value); continue; }
    if (key === "dataset") {
      const dks = Object.keys(value);
      for (let d = 0; d < dks.length; d++) node.dataset[dks[d]] = String(value[dks[d]]);
      continue;
    }
    if (key === "on") {
      const evs = Object.keys(value);
      for (let e = 0; e < evs.length; e++) {
        const fn = value[evs[e]];
        if (typeof fn === "function") node.addEventListener(evs[e], fn);
      }
      continue;
    }
    if (key === "style" && typeof value === "object") {
      const sks = Object.keys(value);
      for (let s = 0; s < sks.length; s++) node.style[sks[s]] = value[sks[s]];
      continue;
    }
    if (key === "attrs") {
      const aks = Object.keys(value);
      for (let a = 0; a < aks.length; a++) {
        const av = value[aks[a]];
        if (av === undefined || av === null || av === false) continue;
        node.setAttribute(aks[a], av === true ? "" : String(av));
      }
      continue;
    }
    if (key in node) { node[key] = value; continue; }
    node.setAttribute(key, String(value));
  }
}

function appendOne(parent, kid) {
  if (kid === null || kid === undefined || kid === false || kid === true) return;
  if (Array.isArray(kid)) {
    for (let i = 0; i < kid.length; i++) appendOne(parent, kid[i]);
    return;
  }
  if (kid instanceof Node) { parent.appendChild(kid); return; }
  parent.appendChild(document.createTextNode(String(kid)));
}

export function text(s) { return document.createTextNode(s == null ? "" : String(s)); }

export function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
  return node;
}

export function badge(label, kind) {
  return el("span", { class: "badge" + (kind ? " " + kind : ""), text: label });
}

// ---------- 时间格式 ----------
const timeFmt = new Intl.DateTimeFormat("zh-CN", { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
const dateFmt = new Intl.DateTimeFormat("zh-CN", { month: "long", day: "numeric" });
const fullFmt = new Intl.DateTimeFormat("zh-CN", {
  year: "numeric", month: "2-digit", day: "2-digit",
  hour: "2-digit", minute: "2-digit", hourCycle: "h23"
});

function toDate(iso) {
  if (!iso) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
}

function sameDay(a, b) {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

export function fmtTime(iso) {
  const d = toDate(iso);
  return d ? timeFmt.format(d) : "";
}

export function fmtDay(iso) {
  const d = toDate(iso);
  if (!d) return "";
  const now = new Date();
  if (sameDay(d, now)) return "今天";
  const y = new Date(now.getTime() - 86400000);
  if (sameDay(d, y)) return "昨天";
  if (d.getFullYear() === now.getFullYear()) return dateFmt.format(d);
  return fullFmt.format(d);
}

export function fmtDateTime(iso) {
  const d = toDate(iso);
  return d ? fullFmt.format(d) : "";
}

export function relTime(iso) {
  const d = toDate(iso);
  if (!d) return "";
  const diff = Date.now() - d.getTime();
  if (diff < 0) return fmtTime(iso);
  const min = Math.floor(diff / 60000);
  if (min < 1) return "刚刚";
  if (min < 60) return min + " 分钟前";
  const hr = Math.floor(min / 60);
  if (hr < 24) return hr + " 小时前";
  const day = Math.floor(hr / 24);
  if (day < 7) return day + " 天前";
  return fmtDay(iso);
}

export function initials(name) {
  const s = String(name || "?").trim();
  if (!s) return "?";
  if (/[\u3400-\u9fff]/.test(s)) return s.slice(-2);
  const parts = s.split(/[\s._-]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return s.slice(0, 2).toUpperCase();
}

export function debounce(fn, ms) {
  let timer = 0;
  return function debounced() {
    const args = arguments;
    const self = this;
    clearTimeout(timer);
    timer = setTimeout(function () { fn.apply(self, args); }, ms);
  };
}

export function truncate(s, n) {
  const str = String(s || "").replace(/\s+/g, " ").trim();
  return str.length > n ? str.slice(0, n) + "…" : str;
}

// ---------- Toast ----------
let toastHost = null;
function ensureToastHost() {
  if (toastHost && document.body.contains(toastHost)) return toastHost;
  toastHost = el("div", { class: "toasts", attrs: { role: "status", "aria-live": "polite" } });
  document.body.appendChild(toastHost);
  return toastHost;
}

export function toast(message, kind, ms) {
  const host = ensureToastHost();
  const node = el("div", { class: "toast" + (kind ? " " + kind : "") },
    el("div", { class: "grow", text: message }),
    el("button", {
      class: "msg-tool-btn",
      attrs: { type: "button", "aria-label": "关闭通知" },
      on: { click: function () { node.remove(); } }
    }, icon("close", 14))
  );
  host.appendChild(node);
  setTimeout(function () { if (node.isConnected) node.remove(); }, ms || 3600);
  return node;
}

// ---------- 确认弹窗 ----------
export function confirmModal(opts) {
  const options = opts || {};
  return new Promise(function (resolve) {
    let closed = false;
    function close(v) {
      if (closed) return;
      closed = true;
      backdrop.remove();
      document.removeEventListener("keydown", onKey);
      resolve(v);
    }
    function onKey(e) { if (e.key === "Escape") close(false); }

    const okBtn = el("button", {
      class: "btn " + (options.danger ? "btn-danger" : "btn-primary"),
      attrs: { type: "button" },
      on: { click: function () { close(true); } }
    }, options.confirmText || "确认");

    const cancelBtn = el("button", {
      class: "btn",
      attrs: { type: "button" },
      on: { click: function () { close(false); } }
    }, options.cancelText || "取消");

    const dialog = el("div", {
      class: "modal",
      attrs: { role: "dialog", "aria-modal": "true", "aria-labelledby": "modal-title" }
    },
      el("h3", { text: options.title || "确认", attrs: { id: "modal-title" } }),
      options.body ? el("p", { text: options.body }) : null,
      el("div", { class: "modal-foot" }, cancelBtn, okBtn)
    );

    const backdrop = el("div", {
      class: "modal-backdrop",
      on: { click: function (e) { if (e.target === backdrop) close(false); } }
    }, dialog);

    document.body.appendChild(backdrop);
    document.addEventListener("keydown", onKey);
    okBtn.focus();
  });
}

// ---------- 空态 ----------
export function emptyState(opts) {
  const o = opts || {};
  return el("div", { class: "empty" },
    el("div", { class: "empty-ico" }, icon(o.icon || "inbox", 20)),
    el("h3", { text: o.title || "这里还没有内容" }),
    o.desc ? el("p", { text: o.desc }) : null,
    o.later ? el("span", { class: "later", text: o.later }) : null
  );
}

export function notice(msg, kind) {
  return el("div", { class: "notice" + (kind ? " " + kind : ""), text: msg });
}

export async function copyText(value) {
  const s = String(value == null ? "" : value);
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(s);
      return true;
    }
  } catch (e) { /* 降级 */ }
  try {
    const ta = el("textarea", { style: { position: "fixed", opacity: "0" } });
    ta.value = s;
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    ta.remove();
    return ok;
  } catch (e) { return false; }
}

export function nowIso() { return new Date().toISOString(); }

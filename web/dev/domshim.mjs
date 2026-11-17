// 极简 DOM shim：只覆盖工作台前端实际用到的 API，用于 node 下的冒烟测试。
// 这不是 jsdom，不追求标准完整性；目的是在没有浏览器的环境里抓到运行时错误。

const VOID_PROPS = ["value", "disabled", "checked", "selected"];

export class ShimText {
  constructor(text) {
    this.nodeType = 3;
    this.data = String(text == null ? "" : text);
    this.parentNode = null;
  }
  get textContent() { return this.data; }
  set textContent(v) { this.data = String(v == null ? "" : v); }
  get isConnected() { return isConnected(this); }
  remove() { detach(this); }
}

export const ATTRS = new WeakMap();

export class ShimElement {
  constructor(tag, ns) {
    this.nodeType = 1;
    this.tagName = String(tag || "div").toUpperCase();
    this.namespaceURI = ns || null;
    this.childNodes = [];
    this.parentNode = null;
    this._class = "";
    this.dataset = {};
    this.style = {};
    this._listeners = new Map();
    this._attrs = {};
    this.scrollHeight = 100;
    this.scrollTop = 0;
    this.clientHeight = 100;
    ATTRS.set(this, this._attrs);
  }

  get className() { return this._class; }
  set className(v) { this._class = String(v == null ? "" : v); }
  get classList() {
    const self = this;
    const list = function () { return self._class.split(/\s+/).filter(Boolean); };
    return {
      add: function (c) { if (list().indexOf(c) < 0) self._class = (self._class ? self._class + " " : "") + c; },
      remove: function (c) { self._class = list().filter(function (x) { return x !== c; }).join(" "); },
      contains: function (c) { return list().indexOf(c) >= 0; },
      toggle: function (c, force) {
        const has = list().indexOf(c) >= 0;
        const next = force === undefined ? !has : !!force;
        if (next && !has) self._class = (self._class ? self._class + " " : "") + c;
        if (!next && has) self._class = list().filter(function (x) { return x !== c; }).join(" ");
        return next;
      }
    };
  }

  get textContent() {
    let s = "";
    this.childNodes.forEach(function (c) { s += c.nodeType === 3 ? c.data : c.textContent; });
    return s;
  }
  set textContent(v) {
    this.childNodes.forEach(function (c) { c.parentNode = null; });
    this.childNodes = [];
    const s = String(v == null ? "" : v);
    if (s) this.appendChild(new ShimText(s));
  }

  get firstChild() { return this.childNodes[0] || null; }
  get children() { return this.childNodes.filter(function (c) { return c.nodeType === 1; }); }
  get isConnected() { return isConnected(this); }

  appendChild(node) {
    if (!node || (node.nodeType !== 1 && node.nodeType !== 3 && node.nodeType !== 11)) {
      throw new TypeError("Failed to execute 'appendChild' on 'Node': parameter 1 is not of type 'Node'.");
    }
    if (node.nodeType === 11) {
      node.childNodes.slice().forEach((c) => this.appendChild(c));
      return node;
    }
    if (node.parentNode) node.parentNode.removeChild(node);
    node.parentNode = this;
    this.childNodes.push(node);
    return node;
  }

  insertBefore(node, ref) {
    if (!ref) return this.appendChild(node);
    const idx = this.childNodes.indexOf(ref);
    if (node.parentNode) node.parentNode.removeChild(node);
    node.parentNode = this;
    this.childNodes.splice(idx < 0 ? this.childNodes.length : idx, 0, node);
    return node;
  }

  removeChild(node) {
    const idx = this.childNodes.indexOf(node);
    if (idx >= 0) {
      this.childNodes.splice(idx, 1);
      node.parentNode = null;
    }
    return node;
  }

  remove() { detach(this); }

  setAttribute(name, value) {
    this._attrs[name] = String(value);
    if (name === "id") this.id = String(value);
    if (name === "class") this.className = String(value);
    if (name === "title") this.title = String(value);
    // 真实 DOM 中以下属性是 IDL 反射的，测试需要读到同名 property
    const BOOL_ATTRS = ["disabled", "checked", "selected", "readonly", "multiple"];
    if (BOOL_ATTRS.indexOf(name) >= 0) { this[name] = true; return; }
    const REFLECT = ["type", "value", "placeholder", "name", "href", "min", "max",
      "step", "maxlength", "rows", "autocomplete", "target", "rel"];
    if (REFLECT.indexOf(name) >= 0) this[name] = String(value);
  }
  getAttribute(name) { return Object.prototype.hasOwnProperty.call(this._attrs, name) ? this._attrs[name] : null; }
  hasAttribute(name) { return Object.prototype.hasOwnProperty.call(this._attrs, name); }

  addEventListener(type, fn) {
    if (!this._listeners.has(type)) this._listeners.set(type, []);
    this._listeners.get(type).push(fn);
  }
  removeEventListener(type, fn) {
    const arr = this._listeners.get(type) || [];
    const i = arr.indexOf(fn);
    if (i >= 0) arr.splice(i, 1);
  }
  dispatch(type, ev) {
    const arr = this._listeners.get(type) || [];
    const event = Object.assign({ type: type, target: this, preventDefault: function () {}, stopPropagation: function () {} }, ev || {});
    arr.forEach(function (fn) { fn(event); });
    return event;
  }

  querySelector(sel) { return queryOne(this, sel); }
  querySelectorAll(sel) { return queryAll(this, sel); }
  scrollTo() {}
  scrollIntoView() {}
  focus() {}

  get id() { return this._attrs.id || ""; }
  set id(v) { this._attrs.id = String(v); }
  get title() { return this._attrs.title || ""; }
  set title(v) { this._attrs.title = String(v); }
}

export class ShimFragment {
  constructor() { this.nodeType = 11; this.childNodes = []; this.parentNode = null; }
  appendChild(node) {
    if (!node || (node.nodeType !== 1 && node.nodeType !== 3 && node.nodeType !== 11)) {
      throw new TypeError("Failed to execute 'appendChild' on 'Node': parameter 1 is not of type 'Node'.");
    }
    if (node.parentNode) node.parentNode.removeChild(node);
    node.parentNode = this;
    this.childNodes.push(node);
    return node;
  }
  removeChild(node) {
    const i = this.childNodes.indexOf(node);
    if (i >= 0) this.childNodes.splice(i, 1);
    return node;
  }
}

function isConnected(node) {
  let cur = node;
  while (cur) {
    if (cur === globalThis.document || cur === globalThis.document.body) return true;
    cur = cur.parentNode;
  }
  return false;
}

function detach(node) {
  if (node.parentNode) node.parentNode.removeChild(node);
  node.parentNode = null;
}

function matchesSimple(node, sel) {
  if (!node || node.nodeType !== 1) return false;
  if (sel[0] === "#") return node.id === sel.slice(1);
  if (sel[0] === ".") return node.classList.contains(sel.slice(1));
  const m = /^\[([a-zA-Z-]+)="([^"]*)"\]$/.exec(sel);
  if (m) {
    if (m[1] === "data-id") return node.dataset.id === m[2];
    return node.getAttribute(m[1]) === m[2];
  }
  return node.tagName === sel.toUpperCase();
}

// 支持空格分隔的后代选择器（如 "tbody tr"、".result-snippet em"）
function matches(node, sel) {
  if (!node || node.nodeType !== 1) return false;
  const parts = String(sel).trim().split(/\s+/).filter(Boolean);
  if (!parts.length) return false;
  let i = parts.length - 1;
  if (!matchesSimple(node, parts[i])) return false;
  i--;
  let cur = node.parentNode;
  while (i >= 0) {
    let found = false;
    while (cur) {
      if (cur.nodeType === 1 && matchesSimple(cur, parts[i])) { found = true; cur = cur.parentNode; break; }
      cur = cur.parentNode;
    }
    if (!found) return false;
    i--;
  }
  return true;
}

function queryAll(root, sel) {
  const out = [];
  (function walk(node) {
    (node.childNodes || []).forEach(function (c) {
      if (matches(c, sel)) out.push(c);
      walk(c);
    });
  })(root);
  return out;
}

function queryOne(root, sel) {
  const all = queryAll(root, sel);
  return all[0] || null;
}

export function install() {
  function def(name, value) {
    try {
      Object.defineProperty(globalThis, name, { value: value, configurable: true, writable: true });
    } catch (e) {
      try { globalThis[name] = value; } catch (e2) { /* 只读全局，忽略 */ }
    }
  }

  const body = new ShimElement("body");
  const doc = {
    body: body,
    documentElement: new ShimElement("html"),
    visibilityState: "visible",
    createElement: function (tag) { return new ShimElement(tag); },
    createElementNS: function (ns, tag) { return new ShimElement(tag, ns); },
    createTextNode: function (t) { return new ShimText(t); },
    createDocumentFragment: function () { return new ShimFragment(); },
    getElementById: function (id) {
      const hit = queryAll(body, "#" + id);
      return hit[0] || null;
    },
    querySelector: function (sel) { return queryOne(body, sel); },
    querySelectorAll: function (sel) { return queryAll(body, sel); },
    addEventListener: function () {},
    removeEventListener: function () {}
  };
  def("document", doc);
  def("Node", ShimElement);

  const winListeners = new Map();
  def("__winDispatch", function (type) {
    (winListeners.get(type) || []).forEach(function (fn) { fn({ type: type }); });
  });

  def("window", {
    crypto: { randomUUID: function () { return "uuid-" + Math.random().toString(36).slice(2); } },
    addEventListener: function (type, fn) {
      if (!winListeners.has(type)) winListeners.set(type, []);
      winListeners.get(type).push(fn);
    },
    removeEventListener: function (type, fn) {
      const arr = winListeners.get(type) || [];
      const i = arr.indexOf(fn);
      if (i >= 0) arr.splice(i, 1);
    }
  });

  def("navigator", { clipboard: { writeText: async function () { return true; } } });
  def("location", {
    origin: "http://127.0.0.1:8787",
    search: "?demo=1",
    hash: "",
    reload: function () {}
  });
  def("history", {
    replaceState: function (a, b, url) {
      if (url && String(url)[0] === "#") globalThis.location.hash = String(url);
    }
  });

  return doc;
}
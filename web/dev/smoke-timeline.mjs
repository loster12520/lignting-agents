// 无浏览器冒烟测试：最小 DOM shim + fake fetch，验证 timeline 渲染、过滤、diff 切换。
// 运行：node web/dev/smoke-timeline.mjs
import assert from "node:assert/strict";

class Node {
  constructor(tag, ns) {
    this.tagName = tag ? String(tag).toUpperCase() : "";
    this.nodeName = this.tagName;
    this.nodeType = 1;
    this.namespaceURI = ns || null;
    this.childNodes = [];
    this.attributes = {};
    this.style = {};
    this.dataset = {};
    this.className = "";
    this._text = "";
    this.listeners = {};
    this.parentNode = null;
    this.ownerDocument = globalThis.document || null;
    this.value = "";
    this.checked = false;
    this.disabled = false;
    this.open = false;
    this.title = "";
    this.href = "";
  }
  appendChild(child) {
    if (!child) throw new Error("appendChild(null)");
    if (child.nodeType === 11) {
      child.childNodes.slice().forEach((c) => this.appendChild(c));
      child.childNodes = [];
      return child;
    }
    this.childNodes.push(child);
    child.parentNode = this;
    return child;
  }
  removeChild(child) {
    const i = this.childNodes.indexOf(child);
    if (i >= 0) this.childNodes.splice(i, 1);
    child.parentNode = null;
    return child;
  }
  get firstChild() { return this.childNodes[0] || null; }
  get children() { return this.childNodes.filter((c) => c.nodeType === 1); }
  set textContent(v) { this.childNodes = []; this._text = String(v == null ? "" : v); }
  get textContent() {
    if (this.childNodes.length) return this.childNodes.map((c) => c.textContent).join("");
    return this._text;
  }
  setAttribute(k, v) { this.attributes[k] = String(v); if (k === "open") this.open = true; }
  getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attributes, k) ? this.attributes[k] : null; }
  removeAttribute(k) { delete this.attributes[k]; }
  addEventListener(type, fn) {
    if (!this.listeners[type]) this.listeners[type] = [];
    this.listeners[type].push(fn);
  }
  removeEventListener(type, fn) {
    const arr = this.listeners[type] || [];
    const i = arr.indexOf(fn);
    if (i >= 0) arr.splice(i, 1);
  }
  dispatchEvent(ev) {
    const event = ev || {};
    if (!event.target) event.target = this;
    if (!event.stopPropagation) event.stopPropagation = function () {};
    if (!event.preventDefault) event.preventDefault = function () {};
    const arr = (this.listeners[event.type] || []).slice();
    arr.forEach((fn) => fn(event));
    if (this.parentNode && !event._stopped) this.parentNode.dispatchEvent(event);
    return true;
  }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  contains(node) {
    if (node === this) return true;
    return this.childNodes.some((c) => c.contains && c.contains(node));
  }
  focus() {}
  get isConnected() { return true; }
}

class TextNode {
  constructor(text) {
    this.nodeType = 3;
    this.nodeName = "#text";
    this._text = String(text == null ? "" : text);
    this.parentNode = null;
    this.childNodes = [];
  }
  get textContent() { return this._text; }
  set textContent(v) { this._text = String(v == null ? "" : v); }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
}

class Fragment extends Node {
  constructor() { super("#fragment"); this.nodeType = 11; }
}

const body = new Node("body");
const document = {
  body,
  createElement(tag) { const n = new Node(tag); n.ownerDocument = document; return n; },
  createElementNS(ns, tag) { const n = new Node(tag, ns); n.ownerDocument = document; return n; },
  createTextNode(text) { return new TextNode(text); },
  createDocumentFragment() { return new Fragment(); },
  getElementById() { return null; },
  addEventListener() {},
  removeEventListener() {},
  execCommand() { return false; }
};

globalThis.document = document;
globalThis.Node = Node;
Object.defineProperty(globalThis, "navigator", { value: { clipboard: { writeText: async function () {} } }, configurable: true });
globalThis.window = { addEventListener() {}, removeEventListener() {} };
globalThis.location = { hash: "" };

const store = await import("../lib/store.js");
store.state.owner = { id: "owner_1", name: "阿哲" };
store.state.agents = [
  { id: "agent_a", name: "助理甲" },
  { id: "agent_b", name: "研发乙" }
];
store.state.route = { name: "timeline", params: {}, query: {} };

const sha = "abcdef1234567890";
const now = Date.now();
function iso(delta) { return new Date(now - delta).toISOString(); }

const timeline = [
  {
    id: "tl_1", path: "docs/prd.md", write_event_id: "we_1", agent_id: "agent_a", task_id: "task_1",
    diff: "--- a/docs/prd.md\n+++ b/docs/prd.md\n@@ -1,2 +1,2 @@\n-old line\n+new line\n context",
    reason: "补充验收标准", branch: "main", commit_sha: "", status: "applied", created_at: iso(60 * 1000)
  },
  {
    id: "tl_2", path: "docs/prd.md", write_event_id: "we_2", agent_id: "agent_b", task_id: "task_1",
    diff: "--- a/docs/prd.md\n+++ b/docs/prd.md\n@@ -1,1 +1,1 @@\n-x\n+y",
    reason: "冲突后重试", branch: "main", commit_sha: "", status: "rejected", created_at: iso(5 * 60 * 1000)
  },
  {
    id: "tl_3", path: "src/app.js", write_event_id: "we_3", agent_id: "agent_a", task_id: "task_2",
    diff: "--- a/src/app.js\n+++ b/src/app.js\n@@ -1,1 +1,2 @@\n+console.log(1);",
    reason: "接入时间线", branch: "feature/timeline", commit_sha: sha, status: "applied", created_at: iso(20 * 60 * 1000)
  },
  {
    id: "tl_4", path: "src/app.js", write_event_id: "we_4", agent_id: "agent_b", task_id: "task_2",
    diff: "--- a/src/app.js\n+++ b/src/app.js\n@@ -2,1 +2,1 @@\n-old();\n+new();",
    reason: "修复", branch: "feature/timeline", commit_sha: sha, status: "unknown", created_at: iso(21 * 60 * 1000)
  }
];

function jsonResponse(data, status) {
  return {
    ok: status >= 200 && status < 300,
    status: status,
    headers: { get() { return "application/json"; } },
    json: async function () { return data; }
  };
}

globalThis.fetch = async function (url) {
  const parsed = new URL(String(url), "http://local");
  if (parsed.pathname === "/api/writes/timeline") {
    let items = timeline.slice();
    const path = parsed.searchParams.get("path") || "";
    const agent = parsed.searchParams.get("agent_id") || "";
    if (path) items = items.filter((it) => it.path === path);
    if (agent) items = items.filter((it) => it.agent_id === agent);
    return jsonResponse({ ok: true, data: { items: items, total: items.length, next: "" } }, 200);
  }
  if (parsed.pathname.indexOf("/api/writes/") === 0) {
    const id = decodeURIComponent(parsed.pathname.slice("/api/writes/".length));
    const hit = timeline.filter((it) => it.write_event_id === id)[0];
    if (!hit) return jsonResponse({ ok: false, error: { code: "not_found", message: "not found" } }, 404);
    return jsonResponse({ ok: true, data: {
      id: hit.write_event_id, agent_id: hit.agent_id, task_id: hit.task_id, path: hit.path,
      base_hash: "basehash123456", diff: hit.diff, reason: hit.reason, branch: hit.branch,
      status: hit.status, error: hit.status === "unknown" ? "result unknown" : "",
      commit_sha: hit.commit_sha, created_at: hit.created_at
    } });
  }
  return jsonResponse({ ok: false, error: { code: "not_found", message: "not found" } }, 404);
};

const { render } = await import("../views/timeline.js");

function textOf(node) {
  if (!node) return "";
  if (node.nodeType === 3) return node.textContent;
  if (!node.childNodes || !node.childNodes.length) return node.textContent || "";
  return node.childNodes.map(textOf).join("");
}
function findAll(node, pred) {
  const out = [];
  function walk(n) {
    if (!n) return;
    if (pred(n)) out.push(n);
    (n.childNodes || []).forEach(walk);
  }
  walk(node);
  return out;
}
function makeCtx() {
  return {
    demo: false,
    api: {},
    regions: {
      topbar: document.createElement("div"),
      content: document.createElement("div"),
      composer: document.createElement("div")
    },
    navigate() {},
    toast() {}
  };
}
function wait(ms) { return new Promise((r) => setTimeout(r, ms)); }

const ctx = makeCtx();
let view = render(ctx, { query: { path: "" } });
await wait(30);
let text = textOf(ctx.regions.content);
assert.ok(text.indexOf("docs/prd.md") >= 0, "missing file group");
assert.ok(text.indexOf("已拒绝") >= 0, "missing rejected status");
assert.ok(text.indexOf("commit abcdef12") >= 0, "missing commit group");
assert.ok(text.indexOf("不受管变更") < 0, "unexpected unmanaged");

const details = findAll(ctx.regions.content, (n) => n.tagName === "DETAILS");
assert.equal(details.length, 1, "expected exactly one commit group");

const rows = findAll(ctx.regions.content, (n) => n.attributes && n.attributes.role === "button");
assert.ok(rows.length >= 4, "expected entry rows, got " + rows.length);
const target = rows.filter((n) => textOf(n).indexOf("助理甲") >= 0 && textOf(n).indexOf("补充验收标准") >= 0)[0];
assert.ok(target, "entry row not found");
target.dispatchEvent({ type: "click" });
await wait(30);
text = textOf(ctx.regions.content);
assert.ok(text.indexOf("Unified") >= 0, "diff panel not opened");
assert.ok(text.indexOf("+new line") >= 0, "unified diff missing added line");

const sideBtn = findAll(ctx.regions.content, (n) => n.tagName === "BUTTON" && textOf(n) === "并排对照")[0];
assert.ok(sideBtn, "side-by-side button missing");
sideBtn.dispatchEvent({ type: "click" });
await wait(20);
text = textOf(ctx.regions.content);
assert.ok(text.indexOf("并排对照") >= 0, "side-by-side mode not active");
assert.ok(text.indexOf("+1") >= 0, "side-by-side stats missing");

view.destroy();
const filteredCtx = makeCtx();
view = render(filteredCtx, { query: { path: "docs/prd.md" } });
await wait(30);
const filteredText = textOf(filteredCtx.regions.content);
assert.ok(filteredText.indexOf("docs/prd.md") >= 0, "filtered file missing");
assert.ok(filteredText.indexOf("src/app.js") < 0, "filter did not apply");
view.destroy();

console.log("SMOKE OK: timeline render/filter/diff/commit-group");
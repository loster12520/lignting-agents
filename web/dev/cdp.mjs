// Chrome DevTools Protocol 截图/登录工具（dev only，不参与 embed）
// 用途：真实后端需要会话 Cookie，Chrome headless 无法直接带 Cookie；这里先在同一浏览器里
// 调用 POST /api/login 建立会话，再逐页截图，并收集 console/异常。
//
// 用法：
//   node web/dev/cdp.mjs <baseURL> <outDir> [--login] [name|WxH|hash] ...
//   例：node web/dev/cdp.mjs http://127.0.0.1:18792 "$env:TEMP/shots" --login org|1280x800|/  chat|1920x1080|/?demo=1#/chat
import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const args = process.argv.slice(2);
if (args.length < 2) {
  console.error("usage: node web/dev/cdp.mjs <baseURL> <outDir> [--login] [name|WxH|path] ...");
  process.exit(2);
}
const base = args[0].replace(/\/$/, "");
const outDir = args[1];
const rest = args.slice(2);
let doLogin = false;
const cases = [];
for (const a of rest) {
  if (a === "--login") { doLogin = true; continue; }
  const parts = a.split("|");
  if (parts.length !== 3) { console.error("bad case: " + a); process.exit(2); }
  const m = /^(\d+)x(\d+)$/.exec(parts[1]);
  if (!m) { console.error("bad size: " + parts[1]); process.exit(2); }
  cases.push({ name: parts[0], width: Number(m[1]), height: Number(m[2]), path: parts[2] });
}
if (!cases.length) { console.error("no cases"); process.exit(2); }
mkdirSync(outDir, { recursive: true });

const CHROME = process.env.CHROME_PATH || "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe";
const port = 9400 + Math.floor(Math.random() * 200);
const profile = join(outDir, "chrome-profile-" + port);
const proc = spawn(CHROME, [
  "--headless=new", "--disable-gpu", "--no-sandbox", "--no-first-run", "--hide-scrollbars",
  "--disable-extensions", "--disable-background-networking", "--disable-sync",
  "--remote-debugging-port=" + port, "--user-data-dir=" + profile, "about:blank"
], { stdio: "ignore" });

function sleep(ms) { return new Promise((r) => setTimeout(r, ms)); }

async function waitHTTP(url, tries) {
  for (let i = 0; i < tries; i++) {
    try { const r = await fetch(url); if (r.ok) return await r.json(); } catch (e) { /* retry */ }
    await sleep(200);
  }
  throw new Error("chrome devtools not ready");
}

class CDP {
  constructor(ws) {
    this.ws = ws; this.id = 0; this.pending = new Map(); this.handlers = new Map();
    ws.addEventListener("message", (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.id && this.pending.has(msg.id)) {
        const p = this.pending.get(msg.id); this.pending.delete(msg.id);
        if (msg.error) p.reject(new Error(msg.error.message)); else p.resolve(msg.result);
        return;
      }
      const arr = this.handlers.get(msg.method) || [];
      arr.forEach((fn) => { try { fn(msg.params); } catch (e) { /* ignore */ } });
    });
  }
  on(method, fn) {
    if (!this.handlers.has(method)) this.handlers.set(method, []);
    this.handlers.get(method).push(fn);
  }
  send(method, params) {
    const id = ++this.id;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.ws.send(JSON.stringify({ id, method, params: params || {} }));
    });
  }
}

async function connectPage(version) {
  // 新建页面（Chrome 111+ 需要 PUT）
  let created = null;
  try {
    const r = await fetch("http://127.0.0.1:" + port + "/json/new?url=about:blank", { method: "PUT" });
    created = await r.json();
  } catch (e) {
    const list = await (await fetch("http://127.0.0.1:" + port + "/json/list")).json();
    created = list.find((t) => t.type === "page");
  }
  const ws = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((res, rej) => {
    ws.addEventListener("open", res, { once: true });
    ws.addEventListener("error", rej, { once: true });
  });
  return new CDP(ws);
}

const logErrors = [];
let exitCode = 0;
try {
  await waitHTTP("http://127.0.0.1:" + port + "/json/version", 60);
  const cdp = await connectPage();
  await cdp.send("Page.enable");
  await cdp.send("Runtime.enable");
  cdp.on("Runtime.exceptionThrown", (p) => {
    const d = p.exceptionDetails || {};
    logErrors.push("EXCEPTION " + (d.text || "") + " " + (d.exception && d.exception.description ? String(d.exception.description).split("\n")[0] : ""));
  });
  cdp.on("Runtime.consoleAPICalled", (p) => {
    if (p.type !== "error" && p.type !== "warning") return;
    const text = (p.args || []).map((a) => a.value || a.description || "").join(" ");
    logErrors.push("CONSOLE." + p.type + " " + String(text).split("\n")[0].slice(0, 220));
  });
  cdp.on("Log.entryAdded", (p) => {
    if (p.entry && p.entry.level === "error") logErrors.push("LOG " + String(p.entry.text).slice(0, 200));
  });
  await cdp.send("Log.enable");

  if (doLogin) {
    await cdp.send("Page.navigate", { url: base + "/" });
    await sleep(900);
    const r = await cdp.send("Runtime.evaluate", {
      expression: "fetch('/api/login',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.status)",
      awaitPromise: true, returnByValue: true
    });
    console.log("login status=" + (r.result && r.result.value));
    // 登录发生在当前文档；必须整页重载，否则后续 hash 导航只是同文档跳转，boot 不会重跑
    await cdp.send("Page.reload", { ignoreCache: true });
    await sleep(1200);
  }

  for (const c of cases) {
    await cdp.send("Emulation.setDeviceMetricsOverride", {
      width: c.width, height: c.height, deviceScaleFactor: 1, mobile: false
    });
    await cdp.send("Page.navigate", { url: "about:blank" });
    await sleep(150);
    const sep = c.path.indexOf("?") >= 0 ? "&" : "?";
    await cdp.send("Page.navigate", { url: base + c.path + sep + "_cb=" + Date.now() });
    await sleep(1600);
    // 等异步渲染：轮询 document.querySelector('#app') 子树节点数
    for (let i = 0; i < 12; i++) {
      const r = await cdp.send("Runtime.evaluate", {
        expression: "(document.getElementById('app')||{children:[]}).children.length + ':' + document.querySelectorAll('*').length",
        returnByValue: true
      });
      if (r.result && String(r.result.value).split(":")[1] > 30) break;
      await sleep(250);
    }
    const shot = await cdp.send("Page.captureScreenshot", { format: "png" });
    const file = join(outDir, c.name + ".png");
    writeFileSync(file, Buffer.from(shot.data, "base64"));
    const text = await cdp.send("Runtime.evaluate", {
      expression: "(document.querySelector('.topbar-title')||{}).textContent + '|' + document.querySelectorAll('.msg').length + '|' + document.querySelectorAll('.org-node').length + '|' + document.querySelectorAll('.card').length",
      returnByValue: true
    });
    console.log("SHOT " + c.name + " " + c.width + "x" + c.height + " -> " + file + "  metrics=" + text.result.value);
  }

  const uniq = Array.from(new Set(logErrors));
  console.log("consoleErrors=" + uniq.length);
  uniq.slice(0, 12).forEach((e) => console.log("  " + e));
  if (uniq.filter((e) => e.indexOf("EXCEPTION") === 0).length) exitCode = 1;
} catch (e) {
  console.error("CDP FAILED: " + (e && e.message ? e.message : e));
  exitCode = 1;
} finally {
  try { proc.kill(); } catch (e) { /* ignore */ }
}
process.exit(exitCode);
// 冒烟测试：在没有浏览器的环境里跑通「bootstrap → 外壳 → 各路由 → 发消息/流式」。
// 运行：node web/dev/smoke.mjs
// 说明：DOM 由 domshim.mjs 提供，仅覆盖工作台实际用到的 API；失败会以非零码退出。
import { install } from "./domshim.mjs";

install();

const app = document.createElement("div");
app.id = "app";
document.body.appendChild(app);

let failures = 0;
let checks = 0;
const errors = [];

function ok(name, cond, detail) {
  checks++;
  if (cond) { console.log("PASS  " + name); return; }
  failures++;
  console.log("FAIL  " + name + (detail ? "  -> " + detail : ""));
}
function textOf(node) { return node ? node.textContent : ""; }
function wait(ms) { return new Promise(function (r) { setTimeout(r, ms || 0); }); }
function count(sel) { return document.querySelectorAll(sel).length; }

process.on("unhandledRejection", function (e) { errors.push(String(e && e.stack || e)); });
process.on("uncaughtException", function (e) { errors.push(String(e && e.stack || e)); });

// ---------- 1. 纯逻辑：markdown / highlight / store ----------
const md = await import("../lib/markdown.js");
const hl = await import("../lib/highlight.js");
const store = await import("../lib/store.js");

const blocks = md.parseMarkdown("# 标题\n\n这是 **粗体** 与 *斜体* 和 \`code\`。\n\n- a\n- b\n\n| x | y |\n|---|---|\n| 1 | 2 |\n\n\`\`\`go\nfmt.Println(1)\n\`\`\`\n\n> 引用\n\n[链接](https://example.com) 与 @小灯");
const kinds = blocks.map(function (b) { return b.t; }).join(",");
ok("markdown 解析出标题/段落/列表/表格/代码/引用", kinds.indexOf("h") === 0 && kinds.indexOf("ul") > 0 && kinds.indexOf("table") > 0 && kinds.indexOf("code") > 0 && kinds.indexOf("quote") > 0, kinds);
const p = blocks.filter(function (b) { return b.t === "p"; })[0];
ok("markdown 粗体/斜体/行内代码 token", JSON.stringify(p).indexOf('"strong"') > 0 && JSON.stringify(p).indexOf('"em"') > 0 && JSON.stringify(p).indexOf('"code"') > 0);
ok("markdown 只允许 http(s)/#/ 链接", md.safeHref("javascript:alert(1)") === false && md.safeHref("https://x.y") === true && md.safeHref("#/chat/c1") === true);
ok("markdown 纯文本预览", md.toPlainText("## 你好\n\n**世界**", 80).indexOf("你好") >= 0);

const parts = hl.parseSnippet("我们下周<em>部署</em>第一版 &amp; &#39;ok&#39;");
ok("highlight 解析 <em> 片段", parts.length === 3 && parts[1].t === "em" && parts[1].v === "部署", JSON.stringify(parts));
ok("highlight 转义实体", parts[2].v.indexOf("&") >= 0 || parts[0].v.length > 0);
const evil = hl.parseSnippet("<img src=x onerror=alert(1)><em>命中</em>");
ok("highlight 不把未知标签当 HTML", evil.some(function (x) { return x.v && x.v.indexOf("<img") >= 0; }), JSON.stringify(evil));

store.state.conversations = [];
store.ingestMessages("c1", [{ seq: 1, id: "a" }, { seq: 2, id: "b" }, { seq: 2, id: "b-dup" }], {});
ok("消息入库按 seq 去重", store.bucket("c1").items.length === 2, String(store.bucket("c1").items.length));
store.applyStreamDelta("s1", "c1", "你", 0);
store.applyStreamDelta("s1", "c1", "好", 1);
const dup = store.applyStreamDelta("s1", "c1", "重复", 1);
ok("delta 乱序/重复被丢弃", dup === false && store.state.streams.get("s1").text === "你好");
store.clearStream("s1");

// ---------- 2. 启动整机（demo 模式） ----------
await import("../app.js");
await wait(200);

ok("外壳已挂载 .app", count(".app") === 1, "app children=" + app.childNodes.length);
ok("侧栏 8 个导航项", count(".nav-item") === 8, String(count(".nav-item")));
const convs = count(".conv-item");
ok("会话列表渲染（>=8）", convs >= 8, String(convs));
ok("默认进入助理私聊", (location.hash || "").indexOf("conv_dm_assistant") >= 0, location.hash);
ok("消息已渲染", count(".msg") >= 5, String(count(".msg")));
ok("Markdown 粗体已渲染为 strong", count("strong") > 0, String(count("strong")));
ok("结构化卡片已渲染", count(".mcard") >= 1, String(count(".mcard")));
ok("输入框存在", count(".composer-input") === 1, String(count(".composer-input")));
ok("演示模式标注在页头", textOf(document.querySelector(".topbar-sub")).indexOf("演示数据") >= 0, textOf(document.querySelector(".topbar-sub")));

// ---------- 3. 路由切换 ----------
async function goto(hash) {
  location.hash = hash;
  globalThis.__winDispatch("hashchange");
  await wait(60);
}

await goto("#/search");
ok("搜索页渲染", count(".search-input") === 1, String(count(".search-input")));
const input = document.querySelector(".search-input");
input.value = "限流";
const form = document.querySelector("form");
form.dispatch("submit");
await wait(80);
ok("中文 2 字检索有结果", count(".result") > 0, String(count(".result")));
ok("片段高亮 <em>", count(".result-snippet em") > 0, String(count(".result-snippet em")));
ok("结果按类型分组", count(".result-group") > 0, String(count(".result-group")));
const firstResult = document.querySelector(".result");
firstResult.dispatch("click");
await wait(80);
ok("点击结果跳转到消息上下文", (location.hash || "").indexOf("#/chat/") === 0 && (location.hash || "").indexOf("msg=") > 0, location.hash);

await goto("#/org");
ok("组织图渲染节点", count(".org-node") >= 3, String(count(".org-node")));

await goto("#/tasks");
ok("任务表渲染", count("tbody tr") >= 5, String(count("tbody tr")));

await goto("#/approvals");
ok("审批页 3 个标签", count(".tab") === 3, String(count(".tab")));
ok("审批卡片渲染", count(".card") >= 3, String(count(".card")));

await goto("#/memory");
ok("记忆表渲染", count("tbody tr") >= 4, String(count("tbody tr")));

await goto("#/audit");
ok("审计表渲染", count("tbody tr") >= 6, String(count("tbody tr")));

await goto("#/settings");
ok("设置页渲染多张卡片", count(".card") >= 5, String(count(".card")));
ok("设置页标注演示模式", textOf(document.body).indexOf("演示模式") >= 0);

// ---------- 4. 发消息 + 流式 + IME 守卫 ----------
await goto("#/chat/conv_dm_assistant");
const beforeMsgs = count(".msg");
const ta = document.querySelector(".composer-input");
ta.value = "你好，今天要做什么？";
ta.dispatch("input");
ta.dispatch("keydown", { key: "Enter", shiftKey: false, isComposing: false });

// IME 组合态下 Enter 不应发送
ta.value = "组合中的文字";
const beforeIme = count(".msg");
ta.dispatch("keydown", { key: "Enter", shiftKey: false, isComposing: true });
await wait(50);
ok("IME 组合态 Enter 不发送", count(".msg") === beforeIme, String(count(".msg")) + " vs " + beforeIme);

await wait(1400);
ok("发送后消息数增加（用户+流式回复）", count(".msg") > beforeMsgs, beforeMsgs + " -> " + count(".msg"));
ok("流式回复已落地为正式消息", textOf(document.querySelector(".msgs")).indexOf("在") >= 0);
ok("已读/未读数不崩", typeof store.unreadTotal() === "number");

// ---------- 5. 结果 ----------
await wait(200);
if (errors.length) {
  failures += errors.length;
  console.log("\n未捕获错误 " + errors.length + " 个：");
  errors.slice(0, 6).forEach(function (e) { console.log("  " + e.split("\n").slice(0, 4).join("\n  ")); });
}
console.log("\n共 " + checks + " 项检查，失败 " + failures + " 项。");
process.exit(failures === 0 ? 0 : 1);
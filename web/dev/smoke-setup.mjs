// 首次向导冒烟：demo 模式下走完 3 步（称呼 → 跳过 Key → 预算）→ 进入工作台。
// 运行：node web/dev/smoke-setup.mjs
import { install } from "./domshim.mjs";

install();
globalThis.location.search = "?demo=1&setup=1";   // setup=1：强制显示首次向导

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
function wait(ms) { return new Promise(function (r) { setTimeout(r, ms || 0); }); }
function count(sel) { return document.querySelectorAll(sel).length; }
function textOf(n) { return n ? n.textContent : ""; }

process.on("unhandledRejection", function (e) { errors.push(String(e && e.stack || e)); });
process.on("uncaughtException", function (e) { errors.push(String(e && e.stack || e)); });

await import("../app.js");
await wait(150);

ok("首次启动显示向导", count(".wizard") === 1, String(count(".wizard")));
ok("向导 3 步指示器", count(".wizard-step") === 3, String(count(".wizard-step")));
ok("第 1 步是称呼输入", count("#owner-name") === 1, String(count("#owner-name")));

const footBtns = document.querySelectorAll(".wizard-foot .btn");
const nextBtn = footBtns[footBtns.length - 1];
const backBtn = footBtns[0];
ok("上一步在首步隐藏", backBtn.style.display === "none", backBtn.style.display);

// 空称呼点下一步：应留在第 1 步并给错误
nextBtn.dispatch("click");
await wait(30);
ok("空称呼被拦截", count("#owner-name") === 1 && textOf(document.querySelector(".err")).indexOf("称呼") >= 0, textOf(document.querySelector(".err")));

const nameInput = document.querySelector("#owner-name");
nameInput.value = "测试主管";
nextBtn.dispatch("click");
await wait(30);
ok("进入第 2 步（Key，可跳过）", count("#deepseek-key") === 1, String(count("#deepseek-key")));
ok("Key 输入为 password", document.querySelector("#deepseek-key").type === "password");

nextBtn.dispatch("click");  // 跳过 Key
await wait(30);
ok("进入第 3 步（预算）", count("#daily-budget") === 1 && count("#monthly-budget") === 1);
ok("提供两档预算预设", count(".preset") === 2, String(count(".preset")));

// 非法预算：日预算清空应拦截
const daily = document.querySelector("#daily-budget");
daily.value = "";
nextBtn.dispatch("click");
await wait(30);
ok("非法日预算被拦截", count("#daily-budget") === 1, "已离开预算步");

daily.value = "20";
const monthly = document.querySelector("#monthly-budget");
monthly.value = "300";
nextBtn.dispatch("click");
await wait(200);

ok("完成后进入工作台", count(".app") === 1, String(count(".app")));
ok("向导已移除", count(".wizard") === 0, String(count(".wizard")));
ok("会话列表已渲染", count(".conv-item") >= 8, String(count(".conv-item")));
ok("助手私聊默认打开", (location.hash || "").indexOf("conv_dm_assistant") >= 0, location.hash);

await wait(150);
if (errors.length) {
  failures += errors.length;
  console.log("\n未捕获错误 " + errors.length + " 个：");
  errors.slice(0, 5).forEach(function (e) { console.log("  " + e.split("\n").slice(0, 4).join("\n  ")); });
}
console.log("\n共 " + checks + " 项检查，失败 " + failures + " 项。");
process.exit(failures === 0 ? 0 : 1);
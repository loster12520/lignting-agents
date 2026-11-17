// 首次向导：称呼 → DeepSeek Key（可跳过）→ 预算（两档预设 + 自定义）。
// 需求：FR-ONB-02/03。失败不产生半初始化状态：保持表单数据、可重试。
import { el, clear, icon } from "../lib/dom.js";

const STEPS = ["称呼", "模型 Key", "预算"];

export function mountSetup(root, opts) {
  const api = opts.api;
  const isDemo = !!opts.demo;
  const data = { owner_name: "", deepseek_api_key: "", daily_budget_cny: 20, monthly_budget_cny: 300 };
  let step = 0;
  let submitting = false;

  clear(root);

  const stepsHost = el("div", { class: "wizard-steps" });
  const bodyHost = el("div", { class: "wizard-body" });
  const errHost = el("div", { class: "err", attrs: { role: "alert" } });
  const backBtn = el("button", { class: "btn", attrs: { type: "button" } }, "上一步");
  const nextBtn = el("button", { class: "btn btn-primary", attrs: { type: "button" } }, "下一步");
  const skipNote = el("span", { class: "ghost-hint" });

  const card = el("div", { class: "wizard-card" },
    el("div", { class: "wizard-head" },
      el("div", { class: "wizard-kicker" }, "首次启动 · 约 2 分钟"),
      el("h1", { text: "欢迎，先做三件小事" }),
      el("p", { text: "这些配置之后都能改。完成后会自动创建你的助理「小灯」并开启私聊。" })
    ),
    stepsHost,
    bodyHost,
    el("div", { class: "wizard-foot" },
      el("div", { class: "row" }, backBtn, skipNote),
      el("div", { class: "row" }, nextBtn)
    )
  );

  const column = el("div", { style: { display: "grid", gap: "12px", width: "min(640px, 100%)" } });
  if (isDemo) {
    column.appendChild(el("div", { class: "notice warn" },
      "演示模式：配置只存在浏览器内存，刷新即重置；不会写入你的真实数据目录。"));
  }
  column.appendChild(card);
  const wrap = el("div", { class: "wizard" }, column);
  root.appendChild(wrap);

  function renderSteps() {
    clear(stepsHost);
    for (let i = 0; i < STEPS.length; i++) {
      const cls = "wizard-step" + (i < step ? " done" : i === step ? " active" : "");
      stepsHost.appendChild(el("div", { class: cls, attrs: { title: STEPS[i] } }));
    }
  }

  function field(opts2) {
    const input = el("input", {
      class: "input",
      attrs: Object.assign({
        type: opts2.type || "text",
        id: opts2.id,
        autocomplete: opts2.autocomplete || "off",
        placeholder: opts2.placeholder || ""
      }, opts2.attrs || {})
    });
    input.value = opts2.value || "";
    const err = el("div", { class: "err" });
    const wrap2 = el("form", {
      class: "field",
      on: { submit: function (e) { e.preventDefault(); if (typeof opts2.onSubmit === "function") opts2.onSubmit(); } }
    },
      el("label", { attrs: { for: opts2.id }, text: opts2.label }),
      input,
      opts2.help ? el("div", { class: "help", text: opts2.help }) : null,
      err
    );
    return { node: wrap2, input: input, err: err, id: opts2.id };
  }

  function renderStep() {
    renderSteps();
    clear(bodyHost);
    clear(errHost);
    backBtn.style.display = step === 0 ? "none" : "";
    nextBtn.textContent = step === STEPS.length - 1 ? "完成并进入工作台" : "下一步";
    skipNote.textContent = step === 1 ? "没有 Key 也可以先跳过：浏览、检索、导出可用，Agent 回复需配置后才能用。" : "";

    if (step === 0) {
      const name = field({
        id: "owner-name", label: "怎么称呼你？", value: data.owner_name,
        placeholder: "例如：阿哲", help: "1–20 个字符。会显示在消息与审计里。"
      });
      name.input.addEventListener("keydown", function (e) {
        if (e.key === "Enter") { e.preventDefault(); goNext(); }
      });
      // 表单提交（回车）同样推进
      name.node.addEventListener("submit", function (e) { e.preventDefault(); goNext(); });
      bodyHost.appendChild(name.node);
      setTimeout(function () { name.input.focus(); }, 0);
      bodyHost._get = function () { return { name: name }; };
      return;
    }

    if (step === 1) {
      const key = field({
        id: "deepseek-key", label: "DeepSeek API Key（可跳过）", type: "password",
        value: data.deepseek_api_key, placeholder: "sk-…",
        autocomplete: "new-password",
        help: "只接 DeepSeek 官方 API。Key 不会明文回显；保存后由核心加密存放（R8 完善）。"
      });
      const testing = el("div", { class: "help", text: "提示：模型为 deepseek-flash；免费额度用尽或限流时，Agent 会显示可读错误并可重试。" });
      bodyHost.appendChild(key.node);
      bodyHost.appendChild(testing);
      bodyHost._get = function () { return { key: key }; };
      setTimeout(function () { key.input.focus(); }, 0);
      return;
    }

    const presets = el("div", { class: "preset-row" });
    const daily = field({ id: "daily-budget", label: "每日预算上限（元）", type: "number", value: String(data.daily_budget_cny), attrs: { min: "1", step: "1" } });
    const monthly = field({ id: "monthly-budget", label: "每月预算上限（元）", type: "number", value: String(data.monthly_budget_cny), attrs: { min: "1", step: "1" } });

    function markPreset(sel) {
      Array.prototype.forEach.call(presets.children, function (c) { c.classList.toggle("sel", c === sel); });
    }
    [{ n: "试用", d: 20, m: 300, t: "每天 ¥20 / 每月 ¥300 · 适合每天几次对话" },
     { n: "标准", d: 100, m: 1500, t: "每天 ¥100 / 每月 ¥1500 · 适合多部门持续协作" }].forEach(function (p) {
      const node = el("button", {
        class: "preset", attrs: { type: "button" },
        on: {
          click: function () {
            daily.input.value = String(p.d);
            monthly.input.value = String(p.m);
            markPreset(node);
          }
        }
      }, el("b", { text: p.n }), el("span", { text: p.t }));
      presets.appendChild(node);
    });

    bodyHost.appendChild(el("div", { class: "field" },
      el("label", { text: "预算档位" }), presets,
      el("div", { class: "help", text: "参考：一个普通任务约 ¥0.3–¥2。达到 80% 会提醒，100% 自动暂停 Agent（不影响你浏览与检索）。" })
    ));
    bodyHost.appendChild(el("div", { class: "row" }, el("div", { class: "grow" }, daily.node), el("div", { class: "grow" }, monthly.node)));
    markPreset(presets.children[0]);
    bodyHost.appendChild(el("div", { class: "notice" },
      "预算与 Key 都可以稍后在「设置」里修改；修改会留审计记录。"));
    bodyHost._get = function () { return { daily: daily, monthly: monthly }; };
  }

  function readStep() {
    const get = bodyHost._get;
    if (!get) return true;
    const refs = get();
    clear(errHost);
    if (step === 0) {
      data.owner_name = refs.name.input.value.trim();
      if (!data.owner_name) { refs.name.err.textContent = "请填写称呼"; refs.name.input.focus(); return false; }
      if (data.owner_name.length > 20) { refs.name.err.textContent = "最多 20 个字符"; return false; }
      return true;
    }
    if (step === 1) {
      data.deepseek_api_key = refs.key.input.value.trim();
      return true;
    }
    const d = Number(refs.daily.input.value);
    const m = Number(refs.monthly.input.value);
    if (!Number.isFinite(d) || d <= 0) { refs.daily.err.textContent = "请输入大于 0 的数字"; return false; }
    if (!Number.isFinite(m) || m <= 0) { refs.monthly.err.textContent = "请输入大于 0 的数字"; return false; }
    if (m < d) { refs.monthly.err.textContent = "月预算不应小于日预算"; return false; }
    data.daily_budget_cny = d;
    data.monthly_budget_cny = m;
    return true;
  }

  function goBack() {
    if (step === 0) return;
    readStep();
    step -= 1;
    renderStep();
  }

  async function goNext() {
    if (submitting) return;
    if (!readStep()) return;
    if (step < STEPS.length - 1) { step += 1; renderStep(); return; }

    submitting = true;
    nextBtn.disabled = true;
    backBtn.disabled = true;
    nextBtn.textContent = "正在初始化…";
    errHost.textContent = "";

    try {
      const res = await api.setup({
        owner_name: data.owner_name,
        deepseek_api_key: data.deepseek_api_key,
        daily_budget_cny: data.daily_budget_cny,
        monthly_budget_cny: data.monthly_budget_cny
      });
      submitting = false;
      if (opts.onDone) opts.onDone(res);
    } catch (e) {
      submitting = false;
      nextBtn.disabled = false;
      backBtn.disabled = false;
      nextBtn.textContent = "完成并进入工作台";
      errHost.textContent = "初始化失败：" + (e && e.message ? e.message : "未知错误") + "。已填写内容已保留，可直接重试。";
      errHost.style.color = "var(--bad)";
      errHost.style.fontSize = "12px";
      errHost.style.marginTop = "8px";
      bodyHost.appendChild(errHost);
      if (opts.toast) opts.toast("初始化失败，请重试：" + (e && e.message ? e.message : ""), "bad");
    }
  }

  backBtn.addEventListener("click", goBack);
  nextBtn.addEventListener("click", goNext);
  renderStep();

  return { destroy: function () {} };
}
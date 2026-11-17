// 设置：身份、模型 Key、预算、检索索引、数据与运维（R1 骨架，R8 完善）。
import { el, clear, icon, badge, notice, confirmModal } from "../lib/dom.js";
import { state } from "../lib/store.js";

export function mount(ctx) {
  let disposed = false;
  let settings = state.settings || {};
  let usage = null;
  let busy = false;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "设置"),
    el("div", { class: "topbar-sub" }, el("span", { text: "本机配置；Key 只显示是否已配置" }))
  ));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  function loadSettings() {
    ctx.api.settings().then(function (res) {
      if (disposed) return;
      settings = res || settings || {};
      state.settings = settings;
      render();
    }).catch(function (e) {
      if (disposed) return;
      if (e.code === "not_implemented") { render(); return; }
      ctx.toast("设置加载失败：" + e.message, "bad");
      render();
    });
  }

  function loadUsage() {
    ctx.api.usage({ group_by: "department" }).then(function (res) {
      if (disposed) return;
      usage = res;
      render();
    }).catch(function () { /* 预留：R8 */ });
  }

  async function saveSettings(patch) {
    if (busy) return;
    busy = true;
    try {
      await ctx.api.saveSettings(patch);
      ctx.toast("已保存", "ok");
      loadSettings();
    } catch (e) {
      ctx.toast("保存失败：" + e.message, "bad");
    } finally {
      busy = false;
    }
  }

  async function runAdmin(label, fn) {
    const ok = await confirmModal({
      title: label,
      body: "该操作会写入审计。确认执行？",
      confirmText: "执行",
      danger: label.indexOf("紧急") >= 0
    });
    if (!ok) return;
    try {
      await fn();
      ctx.toast(label + "：已提交", "ok");
    } catch (e) {
      ctx.toast(label + "失败：" + e.message, "bad");
    }
  }

  function card(title, children, extra) {
    return el("div", { class: "card" },
      el("div", { class: "card-title" }, el("span", { text: title }), extra || null),
      el("div", { class: "stack", attrs: { style: "margin-top:10px" } }, children)
    );
  }

  function render() {
    clear(host);
    host.appendChild(el("div", { class: "page-head" },
      el("div", null,
        el("h2", { text: "设置" }),
        el("p", { text: "单机单用户；只接 DeepSeek 官方 API；所有变更留审计。" })
      )
    ));

    if (ctx.demo) {
      host.appendChild(notice("演示模式：以下配置只存在内存，刷新即重置；不会写入真实数据目录。", "warn"));
      host.appendChild(el("div", { attrs: { style: "height:12px" } }));
    }
    if (state.modelMode === "mock") {
      host.appendChild(notice("测试模式（model.mode=mock）：Agent 回复为确定性 mock，不影响浏览与检索。", "warn"));
      host.appendChild(el("div", { attrs: { style: "height:12px" } }));
    }

    // 身份
    const nameInput = el("input", { class: "input", attrs: { type: "text", maxlength: "20", value: (state.owner && state.owner.name) || "" } });
    host.appendChild(card("身份", [
      el("div", { class: "field" },
        el("label", { text: "称呼" }), nameInput,
        el("div", { class: "help", text: "显示在消息与审计中；最多 20 个字符。" })
      ),
      el("div", { class: "row" },
        el("button", {
          class: "btn btn-primary btn-sm", attrs: { type: "button" },
          on: { click: function () { saveSettings({ owner_name: nameInput.value.trim() }); } }
        }, "保存称呼")
      )
    ]));

    // 模型与 Key
    host.appendChild(card("模型与 Key", [
      el("div", { class: "row" },
        el("span", { text: "模型" }), badge(settings.model || "deepseek-flash", "sky"),
        el("span", { class: "grow" }),
        badge(settings.api_key_set ? "Key 已配置" : "Key 未配置", settings.api_key_set ? "ok" : "warn")
      ),
      el("div", { class: "field" },
        el("label", { text: "DeepSeek API Key" }),
        el("div", { class: "row" },
          el("input", { class: "input", attrs: { type: "password", placeholder: settings.api_key_set ? "已配置（不回显）" : "sk-…", disabled: true, style: "max-width:320px" } }),
          el("button", {
            class: "btn btn-sm", attrs: { type: "button", title: "R8 交付：Key 单独端点与加密存储" },
            on: { click: function () { ctx.toast("Key 变更走单独端点并加密存储（R8 交付）", "warn"); } }
          }, "更换")
        ),
        el("div", { class: "help", text: "Key 不会明文回显；不会出现在消息、检索或日志里。" })
      )
    ]));

    // 预算
    const daily = el("input", { class: "input", attrs: { type: "number", min: "1", value: String(settings.daily_budget_cny || 20) } });
    const monthly = el("input", { class: "input", attrs: { type: "number", min: "1", value: String(settings.monthly_budget_cny || 300) } });
    host.appendChild(card("预算", [
      el("div", { class: "row" },
        el("div", { class: "field grow" }, el("label", { text: "日上限（元）" }), daily),
        el("div", { class: "field grow" }, el("label", { text: "月上限（元）" }), monthly)
      ),
      el("div", { class: "help", text: "80% 提醒；100% 自动暂停 Agent（不暂停人类浏览/检索）。" }),
      el("div", { class: "row" },
        el("button", {
          class: "btn btn-primary btn-sm", attrs: { type: "button" },
          on: {
            click: function () {
              saveSettings({
                daily_budget_cny: Number(daily.value) || 0,
                monthly_budget_cny: Number(monthly.value) || 0
              });
            }
          }
        }, "保存预算")
      )
    ]));

    // 检索
    const idxState = state.searchIndexState || "ready";
    host.appendChild(card("检索索引", [
      el("div", { class: "row" },
        el("span", { text: "状态" }),
        badge(idxState === "ready" ? "就绪" : idxState === "building" ? "重建中" : "需重建",
          idxState === "ready" ? "ok" : "warn"),
        el("span", { class: "card-meta", text: "中文 2 字起可搜；写入后 ≤100ms 可检索" })
      ),
      el("div", { class: "row" },
        el("button", {
          class: "btn btn-sm", attrs: { type: "button" },
          on: { click: function () { runAdmin("重建索引", function () { return ctx.api.reindex(); }); } }
        }, "重建索引"),
        el("span", { class: "card-meta", text: "重建期间搜索显示「索引更新中」，可降级到旧索引" })
      )
    ]));

    // 成本
    if (usage && usage.items && usage.items.length) {
      const table = el("table", { class: "table" },
        el("thead", null, el("tr", null, ["部门", "模型", "输入", "输出", "费用"].map(function (h) { return el("th", { text: h }); })))
      );
      const tbody = el("tbody");
      usage.items.forEach(function (u) {
        tbody.appendChild(el("tr", null,
          el("td", { text: u.department_id || "—" }),
          el("td", { text: u.model || "—" }),
          el("td", { text: String(u.prompt_tokens || 0) }),
          el("td", { text: String(u.completion_tokens || 0) }),
          el("td", { text: "¥" + (Number(u.cost) || 0).toFixed(2) })
        ));
      });
      table.appendChild(tbody);
      host.appendChild(card("成本（演示/按部门）", [table]));
    }

    // 数据与运维
    host.appendChild(card("数据与运维", [
      el("div", { class: "row" },
        el("button", {
          class: "btn btn-sm", attrs: { type: "button" },
          on: { click: function () { runAdmin("备份", function () { return ctx.api.backup(); }); } }
        }, "备份"),
        el("button", {
          class: "btn btn-sm", attrs: { type: "button", title: "R8 交付" },
          on: { click: function () { ctx.toast("导出为 R8 交付", "warn"); } }
        }, "导出"),
        el("span", { class: "grow" }),
        el("button", {
          class: "btn btn-sm btn-danger", attrs: { type: "button" },
          on: { click: function () { runAdmin("紧急停止", function () { return ctx.api.emergencyStop(); }); } }
        }, "紧急停止")
      ),
      el("div", { class: "help", text: "备份/导出/紧急停止在 R8 完整实现；单机环境下数据目录不应放在同步盘或网络盘（SQLite WAL 限制）。" })
    ]));

    // 关于
    host.appendChild(card("关于", [
      el("div", { class: "row" },
        el("span", { text: "版本" }), badge(state.version || "—", ""),
        el("span", { text: "模式" }), badge(ctx.demo ? "演示" : "真实", ctx.demo ? "warn" : "ok"),
        el("span", { text: "model_mode" }), badge(state.modelMode || "—", "")
      ),
      el("div", { class: "help", text: "工作台是零构建前端（原生 HTML/CSS/ESM），随二进制内嵌；不使用外部 CDN。" })
    ]));
  }

  loadSettings();
  loadUsage();
  return { destroy: function () { disposed = true; } };
}

// 共享 UI 片段：状态徽章、头像、结构化卡片（只读投影）。
import { el, badge, initials, fmtDateTime, relTime } from "./dom.js";

export const AGENT_STATUS = {
  idle: ["空闲", ""], running: ["运行中", "accent"], waiting: ["等待审批", "warn"],
  waiting_review: ["等待评审", "warn"], budget_blocked: ["预算阻塞", "bad"],
  paused: ["暂停", ""], failed: ["失败", "bad"], offline: ["失联", "bad"]
};

export const CONV_TYPE = {
  dm: ["私聊", "sky"], project: ["项目", "violet"], department: ["部门", "violet"],
  cross_dept: ["跨部门", "warn"], temp: ["临时组", ""], task_thread: ["任务线程", "sky"],
  artifact_thread: ["产出讨论", "sky"], decision_log: ["决策日志", ""]
};

export const TASK_STATUS = {
  proposed: ["提议", ""], ready: ["待开始", "sky"], discussing: ["讨论中", "violet"],
  in_progress: ["进行中", "accent"], blocked: ["阻塞", "bad"], in_review: ["评审中", "warn"],
  changes_requested: ["待返工", "warn"], approved: ["已通过", "ok"], done: ["完成", "ok"],
  canceled: ["取消", ""], failed: ["失败", "bad"]
};

export const APPROVAL_STATUS = {
  pending: ["待处理", "warn"], approved: ["已批准", "ok"], rejected: ["已拒绝", "bad"],
  expired: ["已超时", ""], blocked: ["阻塞", "bad"]
};

export const APPROVAL_KIND = {
  G1: "G1 组队", G2: "G2 高风险", G3: "G3 验收", irreversible: "不可逆"
};

export const MEMORY_STATUS = {
  candidate: ["待审", "warn"], active: ["生效", "ok"], rejected: ["已拒", ""], deleted: ["已删除", ""]
};

export const MEMORY_SCOPE = { assistant: "助理", department: "部门", employee: "员工" };

export const VISIBILITY = {
  private: "私密", department: "部门可见", project: "项目可见", org: "组织可见",
  human_only: "仅人类", "agent-private": "Agent 私有", team: "团队"
};

export const MSG_TYPE = {
  text: "文本", card: "卡片", tool_summary: "工具调用", task_event: "任务事件",
  review: "评审", decision: "决策", approval: "审批", artifact: "产出",
  group_event: "群事件", write_event: "写事件", system: "系统", trace: "轨迹"
};

export function statusBadge(status, table) {
  const t = table[status];
  if (!t) return badge(status || "未知");
  return badge(t[0], t[1]);
}

export function labelOf(map, key, fallback) {
  if (Object.prototype.hasOwnProperty.call(map, key)) {
    const v = map[key];
    return Array.isArray(v) ? v[0] : v;
  }
  return fallback || key || "";
}

export function avatar(name, kind) {
  const cls = "msg-ava" + (kind === "agent" ? " agent" : kind === "user" ? " owner" : kind === "system" ? " system" : "");
  return el("div", { class: cls, attrs: { "aria-hidden": "true" }, text: initials(name) });
}

export function convAvatar(conv) {
  const map = { dm: "", project: "", department: " k-dept", cross_dept: " k-cross", decision_log: " k-log", task_thread: " k-dept" };
  return el("div", { class: "conv-ava" + (map[conv.type] || ""), attrs: { "aria-hidden": "true" }, text: initials(conv.title) });
}

export function parseMeta(meta) {
  if (!meta) return {};
  if (typeof meta === "object") return meta;
  try {
    const v = JSON.parse(meta);
    return v && typeof v === "object" ? v : {};
  } catch (e) { return {}; }
}

// kv() 在字段为空时返回 null；必须经由 appendKv 安全追加（浏览器不允许 appendChild(null)）
function appendKv(body, label, value) {
  const node = kv(label, value);
  if (node) body.appendChild(node);
}

function kv(label, value) {
  if (value === undefined || value === null || value === "" || (Array.isArray(value) && !value.length)) return null;
  return el("div", { class: "mcard-kv" },
    el("b", { text: label }),
    el("span", { text: Array.isArray(value) ? value.join("、") : String(value) })
  );
}

// 结构化卡片：任务/评审/决策/审批/产出 的只读投影。
// 动作按钮调用核心 API（R3/R4 后可用），演示模式会返回明确错误。
export function renderCard(msg, ctx) {
  const meta = parseMeta(msg.meta);
  const card = meta.card || meta.kind || "";
  if (!card) {
    return el("div", { class: "mcard" },
      el("div", { class: "mcard-head" }, badge("卡片", ""), el("span", { class: "mcard-title", text: msg.content || "" }))
    );
  }

  const head = el("div", { class: "mcard-head" });
  const body = el("div", { class: "mcard-body" });
  const foot = el("div", { class: "mcard-foot" });

  if (card === "task") {
    head.appendChild(badge("任务", "sky"));
    head.appendChild(el("span", { class: "mcard-title", text: meta.title || msg.content || "任务" }));
    if (meta.status) head.appendChild(statusBadge(meta.status, TASK_STATUS));
    appendKv(body, "负责", meta.owner);
    appendKv(body, "部门", meta.department);
    appendKv(body, "视角", meta.perspective);
    appendKv(body, "验收", meta.criteria);
    if (meta.task_id) {
      foot.appendChild(el("button", {
        class: "btn btn-sm", attrs: { type: "button" },
        on: { click: function () { ctx.navigate("#/tasks?id=" + encodeURIComponent(meta.task_id)); } }
      }, "查看任务"));
    }
  } else if (card === "review") {
    head.appendChild(badge("评审挑战", "warn"));
    head.appendChild(el("span", { class: "mcard-title", text: meta.claim || msg.content || "挑战单" }));
    if (meta.severity) head.appendChild(badge(String(meta.severity).toUpperCase(), "bad"));
    appendKv(body, "影响", meta.impact);
    appendKv(body, "方向", meta.from_department && meta.to_department ? meta.from_department + " → " + meta.to_department : "");
    appendKv(body, "状态", meta.status);
  } else if (card === "decision") {
    head.appendChild(badge("决策", "violet"));
    head.appendChild(el("span", { class: "mcard-title", text: meta.title || msg.content || "决策" }));
    if (meta.status) head.appendChild(badge(labelOf({ accepted: "已采纳", proposed: "提议中", superseded: "已取代", rejected: "已否决" }, meta.status), meta.status === "accepted" ? "ok" : ""));
    appendKv(body, "决策人", meta.deciders);
    appendKv(body, "异议", meta.dissent);
  } else if (card === "approval") {
    head.appendChild(badge("审批 · " + (APPROVAL_KIND[meta.kind] || meta.kind || "闸门"), "accent"));
    head.appendChild(el("span", { class: "mcard-title", text: meta.title || "等待审批" }));
    if (meta.status) head.appendChild(statusBadge(meta.status, APPROVAL_STATUS));
    appendKv(body, "风险", meta.risk);
    appendKv(body, "范围", meta.scope);
    appendKv(body, "申请人", meta.requested_by);
    if (meta.status === "pending" || !meta.status) {
      const approve = el("button", {
        class: "btn btn-sm btn-primary", attrs: { type: "button" },
        on: { click: function () { decide(ctx, meta, "approved", approve, reject); } }
      }, "批准");
      const reject = el("button", {
        class: "btn btn-sm btn-danger", attrs: { type: "button" },
        on: { click: function () { decide(ctx, meta, "rejected", approve, reject); } }
      }, "拒绝");
      foot.appendChild(approve);
      foot.appendChild(reject);
    }
  } else if (card === "artifact") {
    head.appendChild(badge("产出物", ""));
    head.appendChild(el("span", { class: "mcard-title", text: meta.title || msg.content || "产出物" }));
    appendKv(body, "类型", meta.type);
    appendKv(body, "版本", meta.version);
  } else {
    head.appendChild(badge(card, ""));
    head.appendChild(el("span", { class: "mcard-title", text: meta.title || msg.content || "" }));
  }

  const node = el("div", { class: "mcard k-" + card },
    head,
    body.childNodes.length ? body : null,
    foot.childNodes.length ? foot : null
  );
  return node;
}

async function decide(ctx, meta, decision, approveBtn, rejectBtn) {
  const id = meta.approval_id;
  if (!id) { ctx.toast("卡片缺少 approval_id，无法提交审批", "bad"); return; }
  approveBtn.disabled = true;
  rejectBtn.disabled = true;
  try {
    await ctx.api.decideApproval(id, { decision: decision, reason: decision === "approved" ? "" : "（前端待填理由）" });
    ctx.toast(decision === "approved" ? "已批准" : "已拒绝", "ok");
    ctx.refresh();
  } catch (e) {
    ctx.toast(e.message || "审批失败", "bad");
    approveBtn.disabled = false;
    rejectBtn.disabled = false;
  }
}

export function msgStateText(state, error) {
  if (state === "streaming") return "正在生成…";
  if (state === "stopped") return "已停止（保留已生成内容）";
  if (state === "failed") return "生成失败" + (error ? "：" + error : "");
  return "";
}

export function timeTitle(iso) { return fmtDateTime(iso); }
export { relTime };
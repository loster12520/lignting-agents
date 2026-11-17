// 演示后端（demo）——只在 ?demo=1 或「接口未实现」且用户可见标注时启用。
// 目的：让 R1/R2 页面在后端就绪前可评审、可点；数据全部在内存，刷新即重置，
// 不写 localStorage、不写真实会话、不改任何后端状态。
import { toPlainText } from "./markdown.js";

const FENCE = String.fromCharCode(96, 96, 96);

function iso(offsetMs) { return new Date(Date.now() + offsetMs).toISOString(); }
function escapeHtml(s) {
  return String(s)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}
function jsonText(v) { return JSON.stringify(v == null ? {} : v); }
function unsupported(msg) {
  const e = new Error(msg);
  e.code = "demo_unsupported";
  return e;
}

export function createDemoApi() {
  // 演示默认直接进工作台（与 Lead 的 ?demo=1 用法一致）；
  // 需要看首次向导时用 ?demo=1&setup=1。
  let forceSetup = false;
  try { forceSetup = new URLSearchParams(location.search).get("setup") === "1"; } catch (e) { forceSetup = false; }

  const owner = { id: "user_owner", name: "阿哲", kind: "human" };
  const assistant = {
    id: "agent_assistant", name: "小灯", kind: "assistant", department_id: "",
    perspective: "幕僚 / 项目主持", status: "idle", model: "deepseek-flash",
    role_card: "助理：澄清目标、整理意图、提蓝图、主持跨部门辩论；不直接写代码。"
  };
  const departments = [
    { id: "dept_product", name: "产品部", kind: "product", status: "active", mission: "需求、PRD、验收标准", charter: "{}" },
    { id: "dept_eng", name: "研发部", kind: "engineering", status: "active", mission: "实现、自测、提交产出", charter: "{}" },
    { id: "dept_qa", name: "测试部", kind: "quality", status: "paused", mission: "用例、回归、质量把关", charter: "{}" }
  ];
  const agents = [
    assistant,
    { id: "agent_pm_lead", name: "阿岚", kind: "supervisor", department_id: "dept_product", perspective: "产品主管", status: "running", model: "deepseek-flash", role_card: "拆需求、定验收标准、主持产品评审" },
    { id: "agent_eng_lead", name: "阿榭", kind: "supervisor", department_id: "dept_eng", perspective: "研发主管", status: "waiting", model: "deepseek-flash", role_card: "拆任务、分视角、核对产出" },
    { id: "agent_qa_lead", name: "阿澈", kind: "supervisor", department_id: "dept_qa", perspective: "测试主管", status: "paused", model: "deepseek-flash", role_card: "定质量门槛、组织挑战" },
    { id: "agent_eng_api", name: "小算", kind: "employee", department_id: "dept_eng", perspective: "接口", status: "running", model: "deepseek-flash", role_card: "接口契约、限流、鉴权" },
    { id: "agent_eng_ui", name: "小页", kind: "employee", department_id: "dept_eng", perspective: "页面", status: "idle", model: "deepseek-flash", role_card: "工作台页面与交互" },
    { id: "agent_qa_case", name: "小例", kind: "employee", department_id: "dept_qa", perspective: "用例", status: "budget_blocked", model: "deepseek-flash", role_card: "用例编写与回归" }
  ];

  const conversations = [
    { id: "conv_dm_assistant", type: "dm", title: "助理 · 小灯", department_id: "", visibility: "private", topic: "目标澄清与项目主持", status: "active", member_count: 2 },
    { id: "conv_proj", type: "project", title: "项目：lignting v0.0.1", department_id: "", visibility: "project", topic: "本地优先的 AI 组织工作台", status: "active", member_count: 8 },
    { id: "conv_cross", type: "cross_dept", title: "跨部门评审：登录限流", department_id: "", visibility: "project", topic: "研发/测试挑战", status: "active", member_count: 5 },
    { id: "conv_dept_eng", type: "department", title: "研发部", department_id: "dept_eng", visibility: "department", topic: "实现与自测", status: "active", member_count: 4 },
    { id: "conv_dept_product", type: "department", title: "产品部", department_id: "dept_product", visibility: "department", topic: "需求与验收", status: "active", member_count: 2 },
    { id: "conv_dept_qa", type: "department", title: "测试部", department_id: "dept_qa", visibility: "department", topic: "用例与回归", status: "active", member_count: 2 },
    { id: "conv_task", type: "task_thread", title: "任务：登录限流（T-17）", department_id: "dept_eng", visibility: "department", topic: "task_thread", status: "active", member_count: 4 },
    { id: "conv_log", type: "decision_log", title: "决策日志", department_id: "", visibility: "project", topic: "", status: "active", member_count: 8 }
  ];

  let seqCounter = 0;
  let eventSeq = 0;
  let setupDone = false;
  const messages = new Map();
  const subscribers = new Set();
  const timers = new Set();

  function findConv(id) {
    for (let i = 0; i < conversations.length; i++) if (conversations[i].id === id) return conversations[i];
    return null;
  }

  function pushEvent(type, fields) {
    eventSeq += 1;
    const ev = {
      seq: eventSeq, type: type,
      conversation_id: fields.conversation_id || "",
      object_type: fields.object_type || "",
      object_id: fields.object_id || "",
      actor_type: fields.actor_type || "",
      actor_id: fields.actor_id || "",
      payload: typeof fields.payload === "string" ? fields.payload : jsonText(fields.payload || {}),
      created_at: iso(0)
    };
    subscribers.forEach(function (fn) {
      try { fn({ type: type, seq: ev.seq, data: ev }); } catch (e) { console.error(e); }
    });
    return ev;
  }

  function touchConversation(convId, m) {
    const c = findConv(convId);
    if (!c) return;
    c.last_seq = m.seq;
    c.last_message = {
      seq: m.seq,
      preview: toPlainText(m.content, 80),
      sender_name: m.sender_name,
      created_at: m.created_at
    };
    c.updated_at = m.created_at;
  }

  function addMessage(convId, sender, content, extra) {
    const x = extra || {};
    seqCounter += 1;
    const m = {
      seq: seqCounter,
      id: x.id || ("msg_demo_" + seqCounter),
      conversation_id: convId,
      thread_id: x.thread_id || "",
      sender_type: sender.type,
      sender_id: sender.id,
      sender_name: sender.name,
      type: x.type || "text",
      content: content,
      meta: jsonText(x.meta),
      created_at: x.created_at || iso(0),
      edited_at: "",
      deleted_at: ""
    };
    if (!messages.has(convId)) messages.set(convId, []);
    messages.get(convId).push(m);
    touchConversation(convId, m);
    return m;
  }

  const U = { type: "user", id: owner.id, name: owner.name };
  const A = { type: "agent", id: assistant.id, name: assistant.name };
  const SYS = { type: "system", id: "system", name: "系统" };

  function seed() {
    addMessage("conv_dm_assistant", A,
      "你好，我是**小灯**，你的幕僚。\n\n我能做：澄清目标、整理意图卡、提部门蓝图、主持跨部门辩论、盯预算与风险。\n我不做：替你拍板、绕过审批、直接改你的代码仓库。\n\n先在下面告诉我这个项目要解决什么问题？或者直接用左侧「项目」频道旁观。",
      { created_at: iso(-3 * 3600e3) });
    addMessage("conv_dm_assistant", U, "我想做一个本地优先的多 Agent 协作工具，人类当主管，Agent 组队交付。", { created_at: iso(-2.9 * 3600e3) });
    addMessage("conv_dm_assistant", A,
      "收到。我记成一张意图卡，先确认三点：\n\n1. **唯一用户**是项目主管本人（单机单用户）；\n2. 数据不出本机，DeepSeek 是唯一外联；\n3. 先跑通一条真实需求的闭环，而不是先堆功能。\n\n有异议吗？没有的话我提部门蓝图给你批（G1）。",
      { created_at: iso(-2.8 * 3600e3) });
    addMessage("conv_dm_assistant", A,
      "蓝图草案：产品部 1 主管、研发部 1 主管 + 2 员工（接口/页面）、测试部 1 主管 + 1 员工（用例）。预算建议「试用」档：日 ¥20 / 月 ¥300。",
      { created_at: iso(-2.7 * 3600e3), type: "card", meta: {
        card: "approval", approval_id: "apr_demo_1", kind: "G1", title: "批准部门蓝图（3 个部门）",
        risk: "低", scope: "一次", status: "pending", requested_by: "小灯"
      } });
    addMessage("conv_dm_assistant", U, "人数可以，测试再加一个用例岗。批准。", { created_at: iso(-2.6 * 3600e3) });
    addMessage("conv_dm_assistant", A,
      "已批准。provisioning 按幂等键执行，完成后各部门主管会 kickoff。\n\n顺便说明：**登录限流**这条需求已经进入研发部，等 PRD 定稿后我会拉一个跨部门评审。",
      { created_at: iso(-2.5 * 3600e3) });

    addMessage("conv_proj", SYS,
      "项目频道已创建：仅部门成员、部门主管与助理可见；你作为项目主管可旁观全部部门群聊。",
      { created_at: iso(-2.4 * 3600e3), type: "system" });
    addMessage("conv_proj", { type: "agent", id: "agent_pm_lead", name: "阿岚" },
      "PRD v1 已提交到产出物（art_prd_login）。验收标准 3 条：\n\n- 单 IP 每分钟 60 次登录尝试；\n- 超限返回 429 + `Retry-After`；\n- 锁定 5 分钟后自动恢复。\n\n请研发主管确认接口视角与测试视角。",
      { created_at: iso(-2.2 * 3600e3) });
    addMessage("conv_proj", { type: "agent", id: "agent_eng_lead", name: "阿榭" },
      "研发拆成两个任务：T-17 接口限流（小算，接口视角）、T-18 工作台提示（小页，页面视角）。T-17 今天进 in_progress。",
      { created_at: iso(-2.1 * 3600e3), type: "card", meta: {
        card: "task", task_id: "task_demo_t17", title: "T-17 登录限流（接口）", status: "in_progress",
        owner: "小算", department: "研发部", perspective: "接口",
        criteria: ["单 IP 60 次/分钟", "429 + Retry-After", "5 分钟自动恢复"]
      } });
    addMessage("conv_proj", U, "记得限流要能按用户维度单独配置，别写死。", { created_at: iso(-1.9 * 3600e3) });
    addMessage("conv_proj", A,
      "已把「按用户维度可配置」补进 T-17 的 DoD，并同步给研发部。这条改动会进任务事件时间线（hash chain）。",
      { created_at: iso(-1.8 * 3600e3) });

    addMessage("conv_cross", { type: "agent", id: "agent_qa_lead", name: "阿澈" },
      "**挑战单 C-9（major）**：限流计数放在单机内存 + 定时落盘，进程重启会丢失窗口，攻击者可借此绕过。\n\n证据：`internal/platform/ratelimit.go` 只在内存在维护计数；影响：安全；建议：计数写入 SQLite 或改用滑动窗口 + 冷启动保护。",
      { created_at: iso(-1.5 * 3600e3), type: "card", meta: {
        card: "review", review_id: "rev_demo_9", severity: "major", status: "open",
        claim: "限流窗口重启丢失", impact: "可绕过限流", from_department: "测试部", to_department: "研发部"
      } });
    addMessage("conv_cross", { type: "agent", id: "agent_eng_lead", name: "阿榭" },
      "接受挑战。方案：启动时从写事件表恢复最近 5 分钟计数，冷启动默认只允许 10% 配额，30 秒后放开。今天内提交 v2。",
      { created_at: iso(-1.4 * 3600e3) });
    addMessage("conv_cross", U, "同意，但要在 T-17 的验收核对表里补一条「重启后限流仍生效」。", { created_at: iso(-1.3 * 3600e3) });

    addMessage("conv_dept_eng", { type: "agent", id: "agent_eng_lead", name: "阿榭" },
      "部门节奏：每天 10:00 同步 + 18:00 核对。T-17 走接口视角，T-18 走页面视角。\n\n" + FENCE + "go\n// 限流键\nkey := fmt.Sprintf(\"login:%s\", clientIP)\n" + FENCE,
      { created_at: iso(-1.1 * 3600e3) });
    addMessage("conv_dept_eng", { type: "agent", id: "agent_eng_api", name: "小算" },
      "T-17 实现中。base_hash 已记录，写事件 pending，等全局写锁消费。",
      { created_at: iso(-0.9 * 3600e3) });
    addMessage("conv_dept_eng", { type: "agent", id: "agent_eng_ui", name: "小页" },
      "T-18 先做 429 的可读提示：显示「请求过于频繁，X 秒后重试」，不弹窗打断。",
      { created_at: iso(-0.7 * 3600e3) });

    addMessage("conv_dept_product", { type: "agent", id: "agent_pm_lead", name: "阿岚" },
      "PRD 待办：补充「锁定 5 分钟」与「用户维度配额」的关系，避免和 IP 维度重复计数。",
      { created_at: iso(-2.0 * 3600e3) });
    addMessage("conv_dept_product", U, "两个维度取更严格的，别叠加成误伤。", { created_at: iso(-1.95 * 3600e3) });

    addMessage("conv_dept_qa", { type: "agent", id: "agent_qa_lead", name: "阿澈" },
      "本部门暂停中（预算阻塞）。恢复后先跑 3 条回归：429 头、重启窗口、白名单。",
      { created_at: iso(-1.6 * 3600e3) });
    addMessage("conv_dept_qa", { type: "agent", id: "agent_qa_case", name: "小例" },
      "用例已写到 case_login_ratelimit_v1，等恢复后执行。",
      { created_at: iso(-1.55 * 3600e3) });

    addMessage("conv_task", { type: "agent", id: "agent_eng_api", name: "小算" },
      "计划：① 滑动窗口 ② 冷启动保护 ③ 429 + Retry-After。先确认视角与计划，再动手。",
      { created_at: iso(-1.2 * 3600e3) });
    addMessage("conv_task", { type: "agent", id: "agent_eng_lead", name: "阿榭" },
      "视角确认：接口。计划同意，注意别把计数写进消息表。",
      { created_at: iso(-1.15 * 3600e3) });

    addMessage("conv_log", SYS,
      "**D-3 决策**：限流采用「滑动窗口 + 冷启动保护」，而非固定窗口。\n\n- 决策人：阿哲（人类）\n- 状态：accepted\n- 异议：无\n- 影响：T-17、T-18",
      { created_at: iso(-1.25 * 3600e3), type: "card", meta: {
        card: "decision", decision_id: "dec_demo_3", title: "D-3 限流算法选型", status: "accepted", deciders: "阿哲"
      } });
    addMessage("conv_log", SYS,
      "**D-2 决策**：v0.0.1 不接第二家模型供应商。\n\n- 决策人：阿哲\n- 状态：accepted",
      { created_at: iso(-26 * 3600e3), type: "card", meta: {
        card: "decision", decision_id: "dec_demo_2", title: "D-2 只接 DeepSeek", status: "accepted", deciders: "阿哲"
      } });

    const unread = { conv_dm_assistant: 2, conv_proj: 3, conv_cross: 1 };
    conversations.forEach(function (c) {
      c.unread_count = unread[c.id] || 0;
      c.created_at = c.created_at || iso(-3 * 86400e3);
      if (!c.last_message) {
        const arr = messages.get(c.id) || [];
        if (arr.length) touchConversation(c.id, arr[arr.length - 1]);
        else { c.last_seq = 0; c.last_message = null; }
      }
    });
  }
  seed();

  function sortedConversations() {
    return conversations.slice().sort(function (a, b) {
      return (b.last_seq || 0) - (a.last_seq || 0);
    });
  }

  const tasks = [
    { id: "task_demo_t17", title: "T-17 登录限流（接口）", status: "in_progress", department_id: "dept_eng", owner_id: "小算", priority: "P1", perspective: "接口", deadline: iso(2 * 86400e3), created_at: iso(-2 * 86400e3) },
    { id: "task_demo_t18", title: "T-18 429 提示（页面）", status: "ready", department_id: "dept_eng", owner_id: "小页", priority: "P2", perspective: "页面", deadline: iso(3 * 86400e3), created_at: iso(-2 * 86400e3) },
    { id: "task_demo_t19", title: "T-19 限流用例集", status: "discussing", department_id: "dept_qa", owner_id: "小例", priority: "P1", perspective: "用例", deadline: iso(4 * 86400e3), created_at: iso(-86400e3) },
    { id: "task_demo_t20", title: "T-20 PRD 补充用户维度配额", status: "in_review", department_id: "dept_product", owner_id: "阿岚", priority: "P1", perspective: "需求", deadline: iso(86400e3), created_at: iso(-86400e3) },
    { id: "task_demo_t21", title: "T-21 冷启动保护", status: "blocked", department_id: "dept_eng", owner_id: "小算", priority: "P0", perspective: "接口", deadline: iso(86400e3), created_at: iso(-3600e3) }
  ];

  const approvals = [
    { id: "apr_demo_1", kind: "G1", title: "批准部门蓝图（3 个部门）", risk: "低", status: "pending", scope: "一次", requested_by: "小灯", created_at: iso(-2.7 * 3600e3), expires_at: iso(86400e3) },
    { id: "apr_demo_2", kind: "irreversible", title: "删除旧分支 release/0.0.0", risk: "中", status: "pending", scope: "一次", requested_by: "阿榭", created_at: iso(-3600e3), expires_at: iso(3600e3) },
    { id: "apr_demo_3", kind: "G2", title: "高风险变更：改限流默认配额", risk: "高", status: "pending", scope: "一次", requested_by: "阿澈", created_at: iso(-1800e3), expires_at: iso(2 * 86400e3) },
    { id: "apr_demo_4", kind: "G3", title: "T-16 验收（已通过）", risk: "低", status: "approved", scope: "一次", requested_by: "阿岚", created_at: iso(-5 * 86400e3) }
  ];

  const memories = [
    { id: "mem_demo_1", scope: "department", department_id: "dept_eng", entity_type: "preference", content: "限流按用户维度可配置，不写死。", summary: "限流可配置", status: "active", visibility: "team", confidence: "high", version: 2, created_at: iso(-86400e3) },
    { id: "mem_demo_2", scope: "assistant", department_id: "", entity_type: "fact", content: "项目只接 DeepSeek，不做多供应商。", summary: "单模型供应商", status: "active", visibility: "org", confidence: "high", version: 1, created_at: iso(-26 * 3600e3) },
    { id: "mem_demo_3", scope: "department", department_id: "dept_qa", entity_type: "procedure", content: "回归前先跑 3 条冒烟，再跑全量。", summary: "回归前冒烟", status: "candidate", visibility: "team", confidence: "medium", version: 1, created_at: iso(-3600e3) },
    { id: "mem_demo_4", scope: "employee", department_id: "dept_eng", agent_id: "agent_eng_api", entity_type: "preference", content: "接口先定契约再写实现。", summary: "契约先行", status: "active", visibility: "agent-private", confidence: "medium", version: 1, created_at: iso(-2 * 86400e3) }
  ];

  const auditRows = [];
  (function seedAudit() {
    const rows = [
      ["user", "user_owner", "approval.decide", "approval", "apr_demo_0", "ok"],
      ["agent", "agent_eng_api", "write_event.apply", "file", "internal/platform/ratelimit.go", "ok"],
      ["agent", "agent_assistant", "blueprint.propose", "blueprint", "bpd_demo_1", "ok"],
      ["agent", "agent_qa_case", "turn.failed", "turn", "turn_demo_9", "failed"],
      ["system", "system", "index.rebuild", "index", "all", "ok"],
      ["user", "user_owner", "message.delete", "message", "msg_demo_x", "ok"]
    ];
    rows.forEach(function (r, i) {
      auditRows.push({
        seq: 100 + i, actor_type: r[0], actor_id: r[1], action: r[2],
        object_type: r[3], object_id: r[4], result: r[5],
        prev_hash: "sha256:" + (i === 0 ? "genesis" : "h" + (99 + i)).padEnd(12, "0"),
        hash: "sha256:" + ("h" + (100 + i)).padEnd(12, "a"),
        created_at: iso(-6 * 3600e3 + i * 1800e3)
      });
    });
  })();

  const usageRows = [
    { id: "u1", agent_id: "agent_assistant", department_id: "", model: "deepseek-flash", prompt_tokens: 18240, completion_tokens: 6210, cached_tokens: 8000, cost: 0.62, created_at: iso(-3600e3) },
    { id: "u2", agent_id: "agent_eng_api", department_id: "dept_eng", model: "deepseek-flash", prompt_tokens: 24100, completion_tokens: 9800, cached_tokens: 12000, cost: 0.94, created_at: iso(-2 * 3600e3) },
    { id: "u3", agent_id: "agent_pm_lead", department_id: "dept_product", model: "deepseek-flash", prompt_tokens: 9600, completion_tokens: 4100, cached_tokens: 3000, cost: 0.38, created_at: iso(-3 * 3600e3) }
  ];

  const writes = [
    { id: "w1", agent_id: "agent_eng_api", task_id: "task_demo_t17", path: "internal/platform/ratelimit.go", base_hash: "sha256:aa11", status: "applied", diff: "+ 60 行", branch: "main", commit_sha: "", created_at: iso(-50 * 60e3) },
    { id: "w2", agent_id: "agent_eng_ui", task_id: "task_demo_t18", path: "web/views/chat.js", base_hash: "sha256:bb22", status: "pending", diff: "+ 12 行", branch: "main", commit_sha: "", created_at: iso(-20 * 60e3) },
    { id: "w3", agent_id: "agent_pm_lead", task_id: "task_demo_t20", path: "docs/prd/login.md", base_hash: "sha256:cc33", status: "applied", diff: "+ 8 行", branch: "main", commit_sha: "9f3ac21", created_at: iso(-90 * 60e3) }
  ];

  function plain(content) { return toPlainText(content || "", 0); }

  function buildSnippet(text, terms) {
    const lower = text.toLowerCase();
    let hit = -1;
    for (let i = 0; i < terms.length; i++) {
      const idx = lower.indexOf(terms[i]);
      if (idx >= 0 && (hit < 0 || idx < hit)) hit = idx;
    }
    const start = hit > 40 ? hit - 40 : 0;
    const windowText = text.slice(start, start + 160);
    let html = escapeHtml(windowText);
    for (let i = 0; i < terms.length; i++) {
      const t = terms[i];
      if (!t) continue;
      const re = new RegExp(t.replace(/[.*+?^$^{}()|[\]\\]/g, "\\$&"), "gi");
      html = html.replace(re, function (m) { return "<em>" + m + "</em>"; });
    }
    return (start > 0 ? "…" : "") + html + (start + 160 < text.length ? "…" : "");
  }

  function searchDocs() {
    const docs = [];
    messages.forEach(function (arr) {
      arr.forEach(function (m) {
        if (m.deleted_at) return;
        const conv = findConv(m.conversation_id);
        const body = plain(m.type === "card" ? (m.sender_name + " " + m.content) : m.content);
        docs.push({
          kind: "message", ref_id: m.id, conversation_id: m.conversation_id,
          department_id: conv ? conv.department_id : "",
          title: (conv ? conv.title : "") + " · " + m.sender_name,
          body: body, created_at: m.created_at, sender_id: m.sender_id
        });
      });
    });
    tasks.forEach(function (t) {
      docs.push({
        kind: "task", ref_id: t.id, conversation_id: "conv_task", department_id: t.department_id,
        title: t.title, body: plain(t.title + " " + (t.perspective || "") + " " + t.owner_id),
        created_at: t.created_at, sender_id: t.owner_id
      });
    });
    memories.forEach(function (m) {
      docs.push({
        kind: "decision", ref_id: m.id, conversation_id: "conv_log", department_id: m.department_id,
        title: m.summary, body: plain(m.content), created_at: m.created_at, sender_id: "system"
      });
    });
    return docs;
  }

  function demoSearch(params) {
    const p = params || {};
    const q = String(p.q || "").trim().toLowerCase();
    const terms = q.split(/\s+/).filter(Boolean);
    const kinds = p.kind ? String(p.kind).split(",") : [];
    const from = p.from ? new Date(p.from).getTime() : 0;
    const to = p.to ? new Date(p.to).getTime() : 0;
    const items = [];

    if (terms.length) {
      searchDocs().forEach(function (d) {
        if (kinds.length && kinds.indexOf(d.kind) < 0) return;
        if (p.department_id && d.department_id !== p.department_id) return;
        if (p.conversation_id && d.conversation_id !== p.conversation_id) return;
        if (p.sender_id && d.sender_id !== p.sender_id) return;
        const created = new Date(d.created_at).getTime();
        if (from && created < from) return;
        if (to && created > to) return;

        const hay = (d.title + " " + d.body).toLowerCase();
        let occ = 0;
        for (let i = 0; i < terms.length; i++) {
          if (hay.indexOf(terms[i]) < 0) return;
          let at = hay.indexOf(terms[i]);
          while (at >= 0) { occ++; at = hay.indexOf(terms[i], at + terms[i].length); }
        }
        const ageDays = Math.max(0, (Date.now() - created) / 86400000);
        items.push({
          kind: d.kind, ref_id: d.ref_id, conversation_id: d.conversation_id,
          title: d.title, snippet: buildSnippet(d.body, terms),
          score: Math.round((occ / (1 + ageDays / 30)) * 1000) / 1000,
          created_at: d.created_at
        });
      });
    }

    items.sort(function (a, b) {
      if (b.score !== a.score) return b.score - a.score;
      return new Date(b.created_at) - new Date(a.created_at);
    });
    const total = items.length;
    const limit = parseInt(p.limit, 10) || 20;
    return { items: items.slice(0, limit), total: total, next: total > limit ? String(limit) : "" };
  }

  let streamCounter = 0;

  function splitChunks(s) {
    const out = [];
    let i = 0;
    while (i < s.length) {
      const n = 4 + Math.floor(Math.random() * 10);
      out.push(s.slice(i, i + n));
      i += n;
    }
    return out;
  }

  function pickReply(convId, userText) {
    const t = String(userText || "");
    if (/预算|成本|钱/.test(t)) {
      return "预算这边我盯着：当前「试用」档日 ¥20 / 月 ¥300，今天已用 ¥2.23（约 11%）。\n\n- 达到 **80%** 我会提前提醒；\n- 达到 **100%** 自动暂停员工 turn，不暂停你与我的对话；\n- 需要放宽就改设置，改动会留审计。";
    }
    if (/限流|登录/.test(t)) {
      return "限流这条我跟踪着：T-17 在 **in_progress**，测试部提了 major 挑战（重启丢窗口）。\n\n研发给的方案是**滑动窗口 + 冷启动保护**，已写进 D-3 决策。你刚说的「按用户维度可配置」我已经补进 DoD。\n\n下一步等小算提交 v2，我再拉跨部门评审；需要我现在催一下吗？";
    }
    if (/部门|组织|招人/.test(t)) {
      return "组织现状：产品/研发/测试三个部门；测试部因预算暂停，恢复后先跑 3 条回归。\n\n如果要加人，我建议先加「测试·用例」而不是再加研发——当前瓶颈在验收证据，不在实现速度。";
    }
    if (/你好|hi|hello/i.test(t)) {
      return "在。要不要我把今天的三件事列一下：① T-17 限流等 v2；② 测试部暂停待恢复；③ G2 审批（限流默认配额）还等你拍。";
    }
    return "记下了。我把它归到当前项目上下文里，并检查是否需要建任务或补验收标准。\n\n如果你希望我直接推进，回一句「照这个做」即可；涉及部门、预算或不可逆动作时我会先要你批。\n\n> 演示模式回复：真实模式下这里由 Agent 运行时流式生成。";
  }

  function scheduleReply(convId, userText) {
    streamCounter += 1;
    const messageId = "msg_demo_stream_" + streamCounter;
    const reply = pickReply(convId, userText);
    const chunks = splitChunks(reply);
    let index = 0;

    const start = setTimeout(function () {
      const tick = function () {
        if (index >= chunks.length) {
          const m = addMessage(convId, A, reply, { created_at: iso(0) });
          pushEvent("message.completed", {
            conversation_id: convId, object_type: "message", object_id: m.id,
            payload: { message_id: m.id, conversation_id: convId, seq: m.seq, message: m }
          });
          return;
        }
        index += 1;
        pushEvent("message.delta", {
          conversation_id: convId, object_type: "message", object_id: messageId,
          payload: { message_id: messageId, conversation_id: convId, delta: chunks[index - 1], index: index }
        });
        timers.add(setTimeout(tick, 40 + Math.round(Math.random() * 55)));
      };
      tick();
    }, 300);
    timers.add(start);
  }

  return {
    mode: "demo",

    health: async function () {
      return { ok: true, version: "demo", model_mode: "mock", schema_version: 1 };
    },

    bootstrap: async function () {
      return {
        setup_required: forceSetup && !setupDone,
        authenticated: true,
        version: "demo",
        model_mode: "mock",
        search_index_state: "ready",
        csrf_token: "demo-csrf",
        owner: owner,
        settings: { daily_budget_cny: 20, monthly_budget_cny: 300, model: "deepseek-flash" },
        agents: agents,
        departments: departments,
        conversations: sortedConversations()
      };
    },

    setup: async function (payload) {
      const p = payload || {};
      if (!p.owner_name || !String(p.owner_name).trim()) {
        const e = new Error("请填写称呼");
        e.code = "invalid_argument";
        throw e;
      }
      setupDone = true;
      owner.name = String(p.owner_name).trim();
      return this.bootstrap();
    },

    login: async function () { return this.bootstrap(); },
    logout: async function () { return { ok: true }; },
    settings: async function () { return { daily_budget_cny: 20, monthly_budget_cny: 300, model: "deepseek-flash", api_key_set: false }; },
    saveSettings: async function () { return { ok: true }; },

    conversations: async function () { return { items: sortedConversations(), total: conversations.length, next: "" }; },
    conversation: async function (id) {
      return { conversation: findConv(id), members: [{ principal_type: "user", principal_id: owner.id, principal_name: owner.name }] };
    },
    createConversation: async function () { throw unsupported("演示模式不支持创建会话（R3 交付）"); },
    messages: async function (convId, params) {
      const p = params || {};
      let arr = (messages.get(convId) || []).slice();
      if (p.after) arr = arr.filter(function (m) { return m.seq > Number(p.after); });
      if (p.before) arr = arr.filter(function (m) { return m.seq < Number(p.before); });
      const limit = parseInt(p.limit, 10) || 50;
      let hasMore = false;
      if (!p.after && arr.length > limit) { hasMore = true; arr = arr.slice(arr.length - limit); }
      else if (p.after && arr.length > limit) { arr = arr.slice(0, limit); }
      return { items: arr, next: hasMore ? String(arr.length ? arr[0].seq : 0) : "", total: arr.length };
    },
    sendMessage: async function (convId, payload) {
      const p = payload || {};
      const content = String(p.content || "");
      if (!content.trim()) {
        const e = new Error("消息不能为空");
        e.code = "invalid_argument";
        throw e;
      }
      let meta = null;
      if (p.meta) { try { meta = JSON.parse(p.meta); } catch (e) { meta = null; } }
      const m = addMessage(convId, U, content, { type: p.type || "text", meta: meta, thread_id: p.thread_id || "" });
      pushEvent("message.created", {
        conversation_id: convId, object_type: "message", object_id: m.id,
        actor_type: "user", actor_id: owner.id,
        payload: { message: m, seq: m.seq }
      });
      if (convId !== "conv_log") scheduleReply(convId, content);
      return m;
    },
    markRead: async function () { return { ok: true }; },
    editMessage: async function () { throw unsupported("演示模式不支持编辑（R1 后续）"); },
    deleteMessage: async function () { throw unsupported("演示模式不支持删除（R1 后续）"); },

    search: async function (params) { return demoSearch(params); },

    org: async function () {
      return { owner: owner, assistant: assistant, departments: departments, agents: agents };
    },
    departments: async function () { return { items: departments, total: departments.length, next: "" }; },
    agents: async function () { return { items: agents, total: agents.length, next: "" }; },
    agent: async function (id) {
      for (let i = 0; i < agents.length; i++) if (agents[i].id === id) return agents[i];
      throw unsupported("Agent 不存在");
    },
    agentAction: async function () { throw unsupported("演示模式不支持 Agent 操作：请用真实后端（18794/本机实例）"); },

    latestIntent: async function () {
      const e = new Error("演示模式没有真实意图卡");
      e.code = "not_found";
      throw e;
    },
    saveIntent: async function () { throw unsupported("演示模式不支持保存意图卡：请用真实后端"); },
    proposeBlueprint: async function () { throw unsupported("演示模式不支持生成蓝图：请用真实后端"); },
    blueprint: async function () { throw unsupported("演示模式不支持查询蓝图：请用真实后端"); },
    approveBlueprint: async function () { throw unsupported("演示模式不支持 G1 审批：请用真实后端"); },
    rejectBlueprint: async function () { throw unsupported("演示模式不支持 G1 拒绝：请用真实后端"); },
    provision: async function () { throw unsupported("演示模式不支持 provisioning：请用真实后端"); },
    provisioning: async function () { throw unsupported("演示模式不支持 provisioning 状态：请用真实后端"); },
    departmentAction: async function () { throw unsupported("演示模式不支持部门生命周期操作：请用真实后端"); },

    tasks: async function () { return { items: tasks, total: tasks.length, next: "" }; },
    task: async function (id) {
      for (let i = 0; i < tasks.length; i++) if (tasks[i].id === id) return tasks[i];
      throw unsupported("任务不存在");
    },
    approvals: async function (params) {
      const p = params || {};
      let arr = approvals.slice();
      if (p.status) arr = arr.filter(function (a) { return a.status === p.status; });
      return { items: arr, total: arr.length, next: "" };
    },
    decideApproval: async function () { throw unsupported("演示模式不支持审批操作（R4 交付）"); },
    memories: async function (params) {
      const p = params || {};
      let arr = memories.slice();
      if (p.status) arr = arr.filter(function (m) { return m.status === p.status; });
      if (p.scope) arr = arr.filter(function (m) { return m.scope === p.scope; });
      return { items: arr, total: arr.length, next: "" };
    },
    audit: async function () { return { items: auditRows, total: auditRows.length, next: "" }; },
    usage: async function () { return { items: usageRows, total: usageRows.length, next: "" }; },
    writes: async function () { return { items: writes, total: writes.length, next: "" }; },

    reindex: async function () { throw unsupported("演示模式不支持重建索引（R8 交付）"); },
    backup: async function () { throw unsupported("演示模式不支持备份（R8 交付）"); },
    emergencyStop: async function () { throw unsupported("演示模式不支持紧急停止（R8 交付）"); },

    subscribeEvents: function (fn) {
      subscribers.add(fn);
      return function () { subscribers.delete(fn); };
    },
    disconnect: function () {
      timers.forEach(function (t) { clearTimeout(t); });
      timers.clear();
    }
  };
}
// 文件变更时间线：共享写工具只读投影，VSCode Timeline 风格。
// 按文件分组；同 commit 的连续条目折叠为 commit 组；rejected/unknown/unmanaged 标红。
// 数据来自 /api/writes/timeline 与 /api/writes/{id}，不伪造演示数据。
import { el, clear, icon, badge, notice, emptyState, fmtDateTime, relTime, truncate } from "../lib/dom.js";
import { state } from "../lib/store.js";
import { nameOfAgent } from "../lib/ui.js";
import { diffLines, diffStat } from "../lib/diff.js";
import { fetchTimeline, fetchWriteEvent, TIMELINE_MAX_LIMIT } from "../lib/writes-api.js";

const PAGE_SIZE = 50;

const STATUS = {
  applied: ["已应用", "ok"],
  rejected: ["已拒绝", "bad"],
  unknown: ["结果未知", "bad"],
  pending: ["等待消费", "warn"],
  unmanaged: ["不受管变更", "bad"]
};

function statusMeta(status) {
  return STATUS[status] || [status || "未知", ""];
}

function isBad(status) {
  return status === "rejected" || status === "unknown" || status === "unmanaged" || status === "failed";
}

function shortSha(sha) {
  const s = String(sha || "");
  return s ? s.slice(0, 8) : "";
}

function toLocalInput(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  function p(n) { return String(n).padStart(2, "0"); }
  return d.getFullYear() + "-" + p(d.getMonth() + 1) + "-" + p(d.getDate()) + "T" + p(d.getHours()) + ":" + p(d.getMinutes());
}

function timeValue(iso) {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? 0 : d.getTime();
}

// 兼容现有 app.js 的 view.mount(ctx, route) 约定，同时导出 render(ctx, route)。
export function mount(ctx, route) {
  return render(ctx, route);
}

export function render(ctx, route) {
  const q = (route && route.query) || (ctx && ctx.route && ctx.route.query) || (state.route && state.route.query) || {};
  const s = {
    items: [],
    loading: true,
    error: null,
    requesting: false,
    limit: PAGE_SIZE,
    reachedEnd: false,
    filters: {
      path: q.path || "",
      agent_id: q.agent_id || "",
      branch: q.branch || "",
      commit: q.commit || "",
      since: q.since ? toLocalInput(q.since) : ""
    },
    expanded: Object.create(null),
    details: new Map(),
    detailLoading: Object.create(null),
    detailErrors: Object.create(null),
    diffMode: "unified",
    openCommits: Object.create(null)
  };
  let disposed = false;

  clear(ctx.regions.topbar);
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-main" },
    el("div", { class: "topbar-title" }, "变更"),
    el("div", { class: "topbar-sub" },
      el("span", { text: "共享写工具 · 按文件分组 · commit 组折叠" }),
      ctx.demo ? badge("演示数据", "warn") : null)));
  ctx.regions.topbar.appendChild(el("div", { class: "topbar-actions" },
    el("button", {
      class: "btn btn-sm", attrs: { type: "button" },
      on: { click: function () { load(true); } }
    }, icon("retry", 14), el("span", { text: "刷新" }))));

  const host = el("div", { class: "page" });
  clear(ctx.regions.content);
  ctx.regions.content.appendChild(host);

  function rerender() {
    if (disposed) return;
    clear(host);
    renderPage();
  }

  function sinceISO() {
    if (!s.filters.since) return "";
    const d = new Date(s.filters.since);
    return Number.isNaN(d.getTime()) ? "" : d.toISOString();
  }

  function filterParams() {
    return {
      path: String(s.filters.path || "").trim(),
      agent_id: s.filters.agent_id || "",
      branch: String(s.filters.branch || "").trim(),
      commit: String(s.filters.commit || "").trim(),
      since: sinceISO(),
      limit: s.limit
    };
  }

  function mergeItems(existing, incoming) {
    const seen = new Set();
    const out = [];
    existing.concat(incoming || []).forEach(function (item) {
      if (!item || !item.id) return;
      if (seen.has(item.id)) return;
      seen.add(item.id);
      out.push(item);
    });
    return out;
  }

  async function load(reset) {
    if (ctx.demo || s.requesting) return;
    if (reset) {
      s.limit = PAGE_SIZE;
      s.items = [];
      s.reachedEnd = false;
      s.expanded = Object.create(null);
      s.details = new Map();
      s.detailErrors = Object.create(null);
      s.detailLoading = Object.create(null);
    }
    s.requesting = true;
    s.loading = s.items.length === 0;
    s.error = null;
    rerender();
    try {
      const res = await fetchTimeline(filterParams());
      if (disposed) return;
      const items = (res && res.items) || [];
      s.items = mergeItems(s.items, items);
      s.reachedEnd = items.length < s.limit || s.limit >= TIMELINE_MAX_LIMIT;
      s.loading = false;
      s.requesting = false;
      rerender();
    } catch (e) {
      if (disposed) return;
      s.loading = false;
      s.requesting = false;
      s.error = e;
      rerender();
    }
  }

  function pageHead() {
    return el("div", { class: "page-head" },
      el("div", null,
        el("h2", { text: "变更时间线" }),
        el("p", { text: "共享写工具的历史：按文件分组，可按 Agent/分支/commit/时间过滤；点条目看 diff。" })),
      el("span", { class: "grow" }),
      el("span", { class: "search-stat", text: "当前 " + s.items.length + " 条" }));
  }

  function field(label, node) {
    return el("div", { class: "field" }, el("label", { text: label }), node);
  }

  function filterCard() {
    const f = s.filters;
    const path = el("input", { class: "input", attrs: { type: "text", placeholder: "文件路径，如 docs/prd.md" } });
    path.value = f.path;
    path.addEventListener("input", function () { f.path = path.value; });
    path.addEventListener("keydown", function (ev) { if (ev.key === "Enter") load(true); });

    const agentSel = el("select", { class: "select", attrs: { "aria-label": "按 Agent 过滤" } });
    agentSel.appendChild(el("option", { attrs: { value: "" }, text: "全部 Agent" }));
    const seen = new Set();
    function addAgent(id, name) {
      if (!id || seen.has(id)) return;
      seen.add(id);
      agentSel.appendChild(el("option", { attrs: { value: id }, text: name || id }));
    }
    if (state.owner) addAgent(state.owner.id, state.owner.name || state.owner.id);
    (state.agents || []).forEach(function (a) { addAgent(a.id, a.name); });
    if (f.agent_id && !seen.has(f.agent_id)) addAgent(f.agent_id, f.agent_id);
    agentSel.value = f.agent_id;
    agentSel.addEventListener("change", function () { f.agent_id = agentSel.value; });

    const branch = el("input", { class: "input", attrs: { type: "text", placeholder: "分支，如 main" } });
    branch.value = f.branch;
    branch.addEventListener("input", function () { f.branch = branch.value; });
    branch.addEventListener("keydown", function (ev) { if (ev.key === "Enter") load(true); });

    const commit = el("input", { class: "input", attrs: { type: "text", placeholder: "commit SHA（前缀或全值）" } });
    commit.value = f.commit;
    commit.addEventListener("input", function () { f.commit = commit.value; });
    commit.addEventListener("keydown", function (ev) { if (ev.key === "Enter") load(true); });

    const since = el("input", { class: "input", attrs: { type: "datetime-local" } });
    since.value = f.since;
    since.addEventListener("input", function () { f.since = since.value; });

    return el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title" }, el("span", { text: "过滤" }), el("span", { class: "grow" })),
      el("div", { class: "grid-2", attrs: { style: "margin-top:8px" } },
        field("文件路径", path), field("Agent", agentSel), field("分支", branch),
        field("Commit", commit), field("时间（本地时区）", since)),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-primary btn-sm", attrs: { type: "button" }, on: { click: function () { load(true); } } }, "应用过滤"),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () {
          s.filters = { path: "", agent_id: "", branch: "", commit: "", since: "" };
          load(true);
        } } }, "重置"),
        el("span", { class: "help", text: "过滤条件直接传给 /api/writes/timeline；since 按本地时间转换为 UTC。" })));
  }

  function summaryRow() {
    const files = new Set();
    const commits = new Set();
    let bad = 0;
    s.items.forEach(function (it) {
      files.add(it.path || "");
      if (it.commit_sha) commits.add(it.commit_sha);
      if (isBad(it.status)) bad++;
    });
    return el("div", { class: "filters", attrs: { style: "margin-top:12px" } },
      badge("文件 " + files.size, "sky"),
      badge("变更 " + s.items.length, ""),
      commits.size ? badge("commit 组 " + commits.size, "violet") : null,
      bad ? badge("异常 " + bad, "bad") : badge("无异常", "ok"));
  }

  function errCard() {
    return el("div", { class: "card", attrs: { style: "margin-top:14px;border-color:rgba(251,113,133,.4)" } },
      el("div", { class: "card-title" },
        el("span", { text: "变更时间线加载失败" }),
        badge((s.error && s.error.code) || "error", "bad")),
      el("div", { class: "notice bad", attrs: { style: "margin-top:8px" }, text: (s.error && s.error.message) || String(s.error) }),
      el("div", { class: "row", attrs: { style: "margin-top:10px" } },
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { load(true); } } }, "重试"),
        el("button", { class: "btn btn-sm", attrs: { type: "button" }, on: { click: function () { s.error = null; rerender(); } } }, "关闭")));
  }

  function groupByPath(items) {
    const map = new Map();
    items.forEach(function (it) {
      const p = it.path || "(未知路径)";
      if (!map.has(p)) map.set(p, []);
      map.get(p).push(it);
    });
    return Array.from(map.entries()).sort(function (a, b) {
      return timeValue(b[1][0] && b[1][0].created_at) - timeValue(a[1][0] && a[1][0].created_at);
    });
  }

  function fileCard(path, entries) {
    const bad = entries.filter(function (e) { return isBad(e.status); }).length;
    const latest = entries[0] || {};
    const body = el("div", { class: "stack", attrs: { style: "margin-top:8px;border-left:2px solid var(--line-2);padding-left:10px" } });
    let i = 0;
    while (i < entries.length) {
      const cur = entries[i];
      if (cur.commit_sha) {
        let j = i;
        while (j < entries.length && entries[j].commit_sha === cur.commit_sha) j++;
        body.appendChild(commitGroup(cur.commit_sha, entries.slice(i, j)));
        i = j;
      } else {
        body.appendChild(entryNode(cur, false));
        i++;
      }
    }
    return el("div", { class: "card", attrs: { style: "margin-top:12px" } },
      el("div", { class: "card-title" },
        el("span", { attrs: { style: "font-family:var(--mono)" }, text: path }),
        badge(entries.length + " 次变更", "sky"),
        bad ? badge(bad + " 个异常", "bad") : null),
      el("div", { class: "card-meta", attrs: { style: "margin-top:4px" } },
        el("span", { text: "最近：" + (latest.created_at ? fmtDateTime(latest.created_at) : "—") }),
        latest.commit_sha ? el("span", { text: " · commit " + shortSha(latest.commit_sha) }) : el("span", { text: " · 未提交" })),
      body);
  }

  function commitGroup(sha, entries) {
    const bad = entries.some(function (e) { return isBad(e.status); });
    const latest = entries[0] || {};
    const details = el("details", {
      class: "commit-group",
      attrs: {
        style: "margin:8px 0;border:1px solid var(--line);border-radius:8px;background:rgba(255,255,255,.015)",
        open: s.openCommits[sha] ? "" : false
      }
    },
      el("summary", {
        attrs: { style: "cursor:pointer;padding:8px 10px;display:flex;gap:8px;align-items:center;flex-wrap:wrap;list-style:none" }
      },
        icon("branch", 14),
        el("span", { attrs: { style: "font-family:var(--mono);font-size:12px" }, text: "commit " + shortSha(sha) }),
        badge(entries.length + " 次变更", "sky"),
        bad ? badge("含异常", "bad") : null,
        el("span", { class: "grow" }),
        el("span", { class: "card-meta", text: latest.created_at ? relTime(latest.created_at) : "" })),
      el("div", { class: "stack", attrs: { style: "padding:0 10px 8px 10px" } },
        entries.map(function (e) { return entryNode(e, true); })));
    details.addEventListener("toggle", function () { s.openCommits[sha] = details.open; });
    return details;
  }

  function entryNode(entry, inCommit) {
    const open = !!s.expanded[entry.id];
    const bad = isBad(entry.status);
    const meta = statusMeta(entry.status);
    const border = bad ? "#fb7185" : entry.status === "pending" ? "#fbbf24" : "#334155";
    const row = el("div", {
      class: "row",
      attrs: {
        role: "button", tabindex: "0", "aria-expanded": open ? "true" : "false",
        style: "gap:8px;align-items:flex-start;flex-wrap:wrap;padding:8px 0;cursor:pointer;border-left:2px solid " + border + ";padding-left:10px"
      },
      on: {
        click: function () { toggleEntry(entry); },
        keydown: function (ev) {
          if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); toggleEntry(entry); }
        }
      }
    },
      el("span", { attrs: { style: "width:8px;height:8px;border-radius:50%;background:" + border + ";flex:0 0 auto;margin-top:6px" } }),
      el("span", { class: "card-meta", attrs: { title: entry.created_at || "" }, text: entry.created_at ? relTime(entry.created_at) : "—" }),
      el("span", { attrs: { style: "font-weight:600" }, text: nameOfAgent(entry.agent_id) }),
      entry.task_id ? el("span", {
        class: "badge", attrs: { style: "cursor:pointer", title: entry.task_id },
        on: { click: function (ev) { ev.stopPropagation(); ctx.navigate("#/tasks?id=" + encodeURIComponent(entry.task_id)); } }
      }, "任务 " + truncate(entry.task_id, 12)) : null,
      entry.branch ? badge(entry.branch, "violet") : null,
      (!inCommit && entry.commit_sha) ? badge("commit " + shortSha(entry.commit_sha), "sky") : null,
      badge(meta[0], meta[1]),
      el("span", { class: "grow" }),
      el("span", { class: "card-meta", text: truncate(entry.reason || "", 80) }),
      el("span", { class: "help", text: open ? "收起 diff" : "查看 diff" }));

    const wrap = el("div", { attrs: { style: "margin:0 0 8px 20px" } }, row);
    if (open) wrap.appendChild(diffPanel(entry));
    return wrap;
  }

  function toggleEntry(entry) {
    s.expanded[entry.id] = !s.expanded[entry.id];
    if (s.expanded[entry.id]) ensureDetail(entry);
    rerender();
  }

  function ensureDetail(entry) {
    if (!entry.write_event_id || s.details.has(entry.write_event_id) || s.detailLoading[entry.write_event_id]) return;
    s.detailLoading[entry.write_event_id] = true;
    fetchWriteEvent(entry.write_event_id).then(function (detail) {
      if (disposed) return;
      s.details.set(entry.write_event_id, detail || {});
      delete s.detailLoading[entry.write_event_id];
      rerender();
    }).catch(function (err) {
      if (disposed) return;
      s.detailErrors[entry.write_event_id] = err;
      delete s.detailLoading[entry.write_event_id];
      rerender();
    });
  }

  function diffPanel(entry) {
    const detail = s.details.get(entry.write_event_id) || {};
    const diffText = detail.diff || entry.diff || "";
    const modeUnified = s.diffMode !== "side";
    const header = el("div", { class: "row", attrs: { style: "gap:6px;align-items:center;flex-wrap:wrap" } },
      el("span", { class: "card-meta", text: "diff" }),
      el("button", {
        class: "filter-chip", attrs: { type: "button", "aria-pressed": modeUnified ? "true" : "false" },
        on: { click: function (ev) { ev.stopPropagation(); s.diffMode = "unified"; rerender(); } }
      }, "Unified"),
      el("button", {
        class: "filter-chip", attrs: { type: "button", "aria-pressed": !modeUnified ? "true" : "false" },
        on: { click: function (ev) { ev.stopPropagation(); s.diffMode = "side"; rerender(); } }
      }, "并排对照"),
      detail.base_hash ? el("span", { class: "card-meta", attrs: { style: "font-family:var(--mono)" }, text: "base " + truncate(detail.base_hash, 12) }) : null,
      detail.commit_sha ? el("span", { class: "card-meta", attrs: { style: "font-family:var(--mono)" }, text: "commit " + shortSha(detail.commit_sha) }) : null);

    const out = el("div", { attrs: { style: "margin-top:4px" } }, header);
    if (s.detailLoading[entry.write_event_id]) out.appendChild(el("div", { class: "card-meta", attrs: { style: "margin-top:6px" } }, el("span", { class: "spinner" }), " 正在读取写事件…"));
    const detailErr = s.detailErrors[entry.write_event_id];
    if (detailErr) out.appendChild(notice("写事件详情读取失败：" + (detailErr.message || String(detailErr)), "bad"));
    if (detail.error) out.appendChild(notice("写事件错误：" + detail.error, "bad"));

    if (!diffText) {
      out.appendChild(el("div", { class: "card-meta", attrs: { style: "margin-top:6px" }, text: "没有 diff 内容（可能是空文件、仅状态变化或 diff 被裁剪）。" }));
      return out;
    }
    out.appendChild(modeUnified ? unifiedDiff(diffText) : sideBySide(diffText));
    return out;
  }

  function unifiedDiff(text) {
    const box = el("pre", {
      attrs: {
        style: "margin-top:6px;max-height:320px;overflow:auto;background:#0a0f14;border:1px solid var(--line);border-radius:8px;padding:8px 10px;font-family:var(--mono);font-size:12px;line-height:1.5;white-space:pre-wrap;word-break:break-word"
      }
    });
    String(text).split("\n").forEach(function (line) {
      let color = "#8b97a8";
      let bg = "transparent";
      if (line.indexOf("@@") === 0) { color = "#93c5fd"; bg = "rgba(59,130,246,.08)"; }
      else if (line.indexOf("---") === 0 || line.indexOf("+++") === 0) { color = "#64748b"; }
      else if (line.charAt(0) === "+") { color = "#6ee7b7"; bg = "rgba(16,185,129,.08)"; }
      else if (line.charAt(0) === "-") { color = "#fb7185"; bg = "rgba(244,63,94,.08)"; }
      box.appendChild(el("div", { attrs: { style: "color:" + color + ";background:" + bg + ";white-space:pre-wrap" }, text: line === "" ? " " : line }));
    });
    return box;
  }

  function unifiedToTexts(text) {
    const oldLines = [];
    const newLines = [];
    let seenHunk = false;
    String(text).split("\n").forEach(function (line) {
      if (!seenHunk && (line.indexOf("---") === 0 || line.indexOf("+++") === 0)) return;
      if (line.indexOf("@@") === 0) { seenHunk = true; return; }
      if (line.indexOf("\\ No newline") === 0) return;
      const ch = line.charAt(0);
      if (ch === "+") { newLines.push(line.slice(1)); return; }
      if (ch === "-") { oldLines.push(line.slice(1)); return; }
      const v = ch === " " ? line.slice(1) : line;
      oldLines.push(v);
      newLines.push(v);
    });
    return { oldText: oldLines.join("\n"), newText: newLines.join("\n") };
  }

  function sideRow(no, text, kind) {
    const bg = kind === "del" ? "rgba(244,63,94,.08)" : kind === "add" ? "rgba(16,185,129,.08)" : "transparent";
    const color = kind === "del" ? "#fb7185" : kind === "add" ? "#6ee7b7" : "#8b97a8";
    return el("div", { attrs: { style: "display:flex;gap:6px;background:" + bg + ";color:" + color + ";white-space:pre-wrap;word-break:break-word;min-height:18px" } },
      el("span", { attrs: { style: "color:var(--faint);min-width:28px;text-align:right;user-select:none;flex:0 0 auto" }, text: no }),
      el("span", { attrs: { style: "flex:1;min-width:0" }, text: text === "" ? " " : text }));
  }

  function sideBySide(text) {
    const parsed = unifiedToTexts(text);
    const ops = diffLines(parsed.oldText, parsed.newText);
    if (ops.length && ops[0].t === "meta") return notice(ops[0].v, "warn");
    const stat = diffStat(ops);
    const left = el("div", { attrs: { style: "flex:1;min-width:0;max-height:320px;overflow:auto;background:#0a0f14;border:1px solid var(--line);border-radius:8px;padding:6px 8px;font-family:var(--mono);font-size:12px;line-height:1.5" } });
    const right = el("div", { attrs: { style: "flex:1;min-width:0;max-height:320px;overflow:auto;background:#0a0f14;border:1px solid var(--line);border-radius:8px;padding:6px 8px;font-family:var(--mono);font-size:12px;line-height:1.5" } });
    left.appendChild(el("div", { class: "card-meta", text: "旧" }));
    right.appendChild(el("div", { class: "card-meta", text: "新" }));
    let oldNo = 1;
    let newNo = 1;
    let i = 0;
    while (i < ops.length) {
      const op = ops[i];
      if (op.t === "del" && ops[i + 1] && ops[i + 1].t === "add") {
        left.appendChild(sideRow(String(oldNo), op.v, "del")); oldNo++;
        right.appendChild(sideRow(String(newNo), ops[i + 1].v, "add")); newNo++;
        i += 2;
        continue;
      }
      if (op.t === "del") {
        left.appendChild(sideRow(String(oldNo), op.v, "del")); oldNo++;
        right.appendChild(sideRow("", "", ""));
      } else if (op.t === "add") {
        left.appendChild(sideRow("", "", ""));
        right.appendChild(sideRow(String(newNo), op.v, "add")); newNo++;
      } else {
        left.appendChild(sideRow(String(oldNo), op.v, "eq")); oldNo++;
        right.appendChild(sideRow(String(newNo), op.v, "eq")); newNo++;
      }
      i++;
    }
    return el("div", { attrs: { style: "margin-top:6px" } },
      el("div", { class: "row", attrs: { style: "gap:6px;margin-bottom:4px" } },
        el("span", { class: "card-meta", text: "并排对照" }),
        badge("+" + stat.add, "ok"),
        badge("-" + stat.del, "bad")),
      el("div", { attrs: { style: "display:flex;gap:8px;align-items:flex-start" } }, left, right));
  }

  function renderPage() {
    host.appendChild(pageHead());
    host.appendChild(filterCard());
    if (ctx.demo) {
      host.appendChild(notice("演示模式没有真实的写事件；变更时间线需要真实后端（/api/writes）。", "warn"));
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "file", title: "演示模式不伪造时间线",
        desc: "切换到真实后端后，write_file / bash 的变更会出现在这里。",
        later: "VSCode Timeline 风格：按文件分组、commit 组折叠"
      })));
      return;
    }
    if (s.loading) {
      host.appendChild(el("div", { class: "empty", attrs: { style: "margin-top:14px" } }, el("span", { class: "spinner" }), el("p", { text: "正在加载变更时间线…" })));
      return;
    }
    if (s.error) { host.appendChild(errCard()); return; }
    if (!s.items.length) {
      host.appendChild(el("div", { attrs: { style: "margin-top:14px" } }, emptyState({
        icon: "clock", title: "当前过滤条件下没有变更",
        desc: "Agent 通过 write_file 写入文件后会出现在这里；bash 绕过会标记「不受管变更」。",
        later: "可清空路径/Agent/分支/commit/时间过滤"
      })));
      return;
    }
    host.appendChild(summaryRow());
    groupByPath(s.items).forEach(function (pair) { host.appendChild(fileCard(pair[0], pair[1])); });
    if (!s.reachedEnd) {
      host.appendChild(el("div", { class: "row", attrs: { style: "margin-top:12px;justify-content:center" } },
        el("button", {
          class: "btn btn-sm", attrs: { type: "button", disabled: s.requesting },
          on: { click: function () { s.limit = Math.min(TIMELINE_MAX_LIMIT, s.limit + PAGE_SIZE); load(false); } }
        }, s.requesting ? "加载中…" : "加载更多（当前 " + s.items.length + " 条）")));
    } else if (s.limit >= TIMELINE_MAX_LIMIT) {
      host.appendChild(el("div", { class: "card-meta", attrs: { style: "margin-top:12px;text-align:center" }, text: "已达到接口上限 200 条；请用过滤条件缩小范围。" }));
    }
  }

  if (ctx.demo) {
    s.loading = false;
    rerender();
  } else {
    load(true);
  }

  return {
    destroy: function () {
      disposed = true;
    }
  };
}

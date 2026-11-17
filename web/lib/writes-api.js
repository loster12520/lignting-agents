// 共享写工具只读 API：时间线 / 写事件详情 / 写事件列表。
// 只读 GET；错误统一抛 ApiError，不把 4xx/5xx 静默变成空数据。
import { ApiError } from "./api.js";
import { emit } from "./store.js";

export const TIMELINE_MAX_LIMIT = 200;
export const TIMELINE_DEFAULT_LIMIT = 50;

function qs(params) {
  if (!params) return "";
  const parts = [];
  Object.keys(params).forEach(function (k) {
    const v = params[k];
    if (v === undefined || v === null || v === "" || v === false) return;
    parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(String(v)));
  });
  return parts.length ? "?" + parts.join("&") : "";
}

async function request(path) {
  let res;
  try {
    res = await fetch(path, {
      method: "GET",
      credentials: "same-origin",
      headers: { "Accept": "application/json" }
    });
  } catch (e) {
    throw new ApiError("network", "无法连接本地核心（" + (e && e.message ? e.message : "网络错误") + "）。", 0);
  }
  const ct = res.headers.get("content-type") || "";
  let payload = null;
  if (ct.indexOf("application/json") >= 0) {
    try { payload = await res.json(); } catch (e) { payload = null; }
  }
  if ((res.status === 404 || res.status === 501) && (!payload || payload.ok !== false)) {
    throw new ApiError("not_implemented", "接口尚未实现：" + path, res.status);
  }
  if (res.status === 401) emit("auth:required", { path: path });
  if (!res.ok) {
    const err = (payload && payload.error) || {};
    throw new ApiError(err.code || "http_" + res.status, err.message || ("请求失败（HTTP " + res.status + "）"), res.status, err.details);
  }
  if (!payload) throw new ApiError("bad_response", "服务端响应不是合法 JSON", res.status);
  if (payload.ok === false) {
    const err = payload.error || {};
    throw new ApiError(err.code || "error", err.message || "请求失败", res.status, err.details);
  }
  return payload.data === undefined ? payload : payload.data;
}

// GET /api/writes/timeline?path=&branch=&commit=&agent_id=&since=&limit=
export function fetchTimeline(params) {
  const p = params || {};
  return request("/api/writes/timeline" + qs({
    path: p.path,
    branch: p.branch,
    commit: p.commit,
    agent_id: p.agent_id,
    since: p.since,
    limit: p.limit || TIMELINE_DEFAULT_LIMIT
  }));
}

// GET /api/writes?status=&path=&agent_id=&limit=
export function fetchWriteEvents(params) {
  const p = params || {};
  return request("/api/writes" + qs({
    status: p.status,
    path: p.path,
    agent_id: p.agent_id,
    limit: p.limit || 100
  }));
}

// GET /api/writes/{id}
export function fetchWriteEvent(id) {
  return request("/api/writes/" + encodeURIComponent(id));
}

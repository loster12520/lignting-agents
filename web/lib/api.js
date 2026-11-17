// 真实后端客户端（api-contract v1）。所有错误都抛 ApiError，由视图决定如何展示。
//
// 约定：绝不吞错、绝不把 4xx/5xx 静默替换成演示数据。
// 只有「路由尚未实现」（404/501 且非业务 JSON 错误包）才会以 not_implemented
// 交给上层，由上层在用户明确同意（?demo=1）或页头标注的前提下切到演示模式。
import { state, emit } from "./store.js";

export class ApiError extends Error {
  constructor(code, message, status, details) {
    super(message || "请求失败");
    this.name = "ApiError";
    this.code = code || "error";
    this.status = status || 0;
    this.details = details || null;
  }
}

function baseHeaders(method) {
  const h = { "Accept": "application/json" };
  if (!/^(GET|HEAD)$/i.test(method)) h["X-Lignting-CSRF"] = state.csrf || "";
  return h;
}

async function request(method, path, body) {
  const init = { method: method, credentials: "same-origin", headers: baseHeaders(method) };
  if (body !== undefined && body !== null) {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }

  let res;
  try {
    res = await fetch(path, init);
  } catch (e) {
    throw new ApiError("network", "无法连接本地核心（" + (e && e.message ? e.message : "网络错误") + "）。请确认 lignting 进程仍在运行。", 0);
  }

  const ct = res.headers.get("content-type") || "";
  let payload = null;
  if (ct.indexOf("application/json") >= 0) {
    try { payload = await res.json(); } catch (e) { payload = null; }
  }

  // 路由未实现：Go 的 http.NotFound 返回纯文本 404；也接受 501
  if ((res.status === 404 || res.status === 501) && (!payload || payload.ok !== false)) {
    throw new ApiError("not_implemented", "接口尚未实现：" + method + " " + path, res.status);
  }

  if (res.status === 401) {
    // 未登录/会话过期：交给上层决定是否展示登录页，而不是静默当成空数据
    emit("auth:required", { path: path });
  }

  if (!res.ok) {
    const err = (payload && payload.error) || {};
    const msg = err.message || ("请求失败（HTTP " + res.status + "）");
    throw new ApiError(err.code || "http_" + res.status, msg, res.status, err.details);
  }
  if (!payload) throw new ApiError("bad_response", "服务端响应不是合法 JSON", res.status);
  if (payload.ok === false) {
    const err = payload.error || {};
    throw new ApiError(err.code || "error", err.message || "请求失败", res.status, err.details);
  }
  return payload.data === undefined ? payload : payload.data;
}

function qs(params) {
  if (!params) return "";
  const parts = [];
  Object.keys(params).forEach(function (k) {
    const v = params[k];
    if (v === undefined || v === null || v === "" || v === false) return;
    if (Array.isArray(v)) {
      if (!v.length) return;
      parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(v.join(",")));
      return;
    }
    parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(String(v)));
  });
  return parts.length ? "?" + parts.join("&") : "";
}

export function createLiveApi() {
  return {
    mode: "live",

    health: function () { return request("GET", "/api/health"); },
    bootstrap: function () { return request("GET", "/api/bootstrap"); },
    setup: function (payload) { return request("POST", "/api/setup", payload); },
    login: function (payload) { return request("POST", "/api/login", payload); },
    logout: function () { return request("POST", "/api/logout", {}); },
    settings: function () { return request("GET", "/api/settings"); },
    saveSettings: function (payload) { return request("PUT", "/api/settings", payload); },

    conversations: function (params) { return request("GET", "/api/conversations" + qs(params)); },
    conversation: function (id) { return request("GET", "/api/conversations/" + encodeURIComponent(id)); },
    createConversation: function (payload) { return request("POST", "/api/conversations", payload); },
    messages: function (convId, params) {
      return request("GET", "/api/conversations/" + encodeURIComponent(convId) + "/messages" + qs(params));
    },
    sendMessage: function (convId, payload) {
      return request("POST", "/api/conversations/" + encodeURIComponent(convId) + "/messages", payload);
    },
    markRead: function (convId, seq) {
      return request("POST", "/api/conversations/" + encodeURIComponent(convId) + "/read", { seq: seq });
    },
    editMessage: function (id, payload) { return request("PATCH", "/api/messages/" + encodeURIComponent(id), payload); },
    deleteMessage: function (id) { return request("DELETE", "/api/messages/" + encodeURIComponent(id)); },

    search: function (params) { return request("GET", "/api/search" + qs(params)); },

    org: function () { return request("GET", "/api/org"); },

    latestIntent: function () { return request("GET", "/api/intents/latest"); },
    saveIntent: function (payload) { return request("POST", "/api/intents", payload); },
    proposeBlueprint: function (payload) { return request("POST", "/api/blueprints", payload); },
    blueprint: function (id) { return request("GET", "/api/blueprints/" + encodeURIComponent(id)); },
    approveBlueprint: function (id, payload) { return request("POST", "/api/blueprints/" + encodeURIComponent(id) + "/approve", payload || {}); },
    rejectBlueprint: function (id, payload) { return request("POST", "/api/blueprints/" + encodeURIComponent(id) + "/reject", payload || {}); },
    provision: function (payload) { return request("POST", "/api/provisioning", payload); },
    provisioning: function (id) { return request("GET", "/api/provisioning/" + encodeURIComponent(id)); },
    departmentAction: function (id, action, payload) {
      return request("POST", "/api/departments/" + encodeURIComponent(id) + "/" + action, payload || {});
    },
    departments: function () { return request("GET", "/api/departments"); },
    agents: function (params) { return request("GET", "/api/agents" + qs(params)); },
    agent: function (id) { return request("GET", "/api/agents/" + encodeURIComponent(id)); },
    agentAction: function (id, action) {
      return request("POST", "/api/agents/" + encodeURIComponent(id) + "/" + action, {});
    },

    tasks: function (params) { return request("GET", "/api/tasks" + qs(params)); },
    task: function (id) { return request("GET", "/api/tasks/" + encodeURIComponent(id)); },
    approvals: function (params) { return request("GET", "/api/approvals" + qs(params)); },
    decideApproval: function (id, payload) { return request("POST", "/api/approvals/" + encodeURIComponent(id) + "/decide", payload); },
    memories: function (params) { return request("GET", "/api/memories" + qs(params)); },
    audit: function (params) { return request("GET", "/api/audit" + qs(params)); },
    usage: function (params) { return request("GET", "/api/usage" + qs(params)); },
    writes: function (params) { return request("GET", "/api/writes/timeline" + qs(params)); },

    reindex: function () { return request("POST", "/api/admin/reindex", {}); },
    backup: function () { return request("POST", "/api/admin/backup", {}); },
    emergencyStop: function () { return request("POST", "/api/admin/emergency-stop", {}); },

  };
}
// SSE 客户端：增量游标、去重、断线补齐。
//
// 服务端契约（api-contract §4）：事件带 id: <seq>，类型见 EVENT_TYPES。
// 断线后浏览器 EventSource 会自动带 Last-Event-ID 重连；此外我们主动检测 gap，
// 一旦发现 seq 不连续，就通知上层用 REST 重新取消息（不能只靠内存补发）。

export const EVENT_TYPES = [
  "message.created",
  "message.updated",
  "message.deleted",
  "message.delta",
  "message.completed",
  "conversation.created",
  "agent.status",
  "task.updated",
  "approval.updated",
  "write.updated",
  "notification.created"
];

export function createEventStream(opts) {
  const options = opts || {};
  const onEvent = options.onEvent || function () {};
  const onStatus = options.onStatus || function () {};
  const onGap = options.onGap || function () {};

  let es = null;
  let closed = false;
  let lastSeq = Number(options.since || 0);
  let lastErrAt = 0;

  function status(s, detail) { onStatus({ status: s, detail: detail || "", lastSeq: lastSeq }); }

  function handle(type, ev) {
    let seq = 0;
    if (ev && ev.lastEventId) seq = parseInt(ev.lastEventId, 10) || 0;
    let data = null;
    try { data = JSON.parse(ev.data); } catch (e) { return; }
    if (data && typeof data.seq === "number") seq = data.seq;

    if (seq > 0) {
      if (seq <= lastSeq) return;                    // 重复（重连补发）直接丢
      if (lastSeq > 0 && seq > lastSeq + 1) {         // 有缺口
        onGap({ from: lastSeq, to: seq });
      }
      lastSeq = seq;
    }
    onEvent({ type: type, seq: seq, data: data });
  }

  function open() {
    if (closed) return;
    try {
      es = new EventSource("/api/events?since=" + encodeURIComponent(String(lastSeq || 0)));
    } catch (e) {
      status("bad", e.message);
      return;
    }
    status("connecting");

    es.onopen = function () {
      lastErrAt = 0;
      status("open");
    };

    es.onerror = function () {
      lastErrAt = Date.now();
      // EventSource 会自行重连（0.5s~3s），这里只反映状态
      status("connecting", "连接中断，正在重连");
    };

    es.onmessage = function (ev) { handle("message", ev); };

    for (let i = 0; i < EVENT_TYPES.length; i++) {
      const t = EVENT_TYPES[i];
      es.addEventListener(t, function (ev) { handle(t, ev); });
    }
  }

  function close() {
    closed = true;
    if (es) { es.close(); es = null; }
    status("closed");
  }

  // 手动重连（例如从后台切回、或 gap 修复后）：丢弃旧连接，用当前游标重开
  function reconnect() {
    if (es) { es.close(); es = null; }
    status("connecting", "正在按游标重新连接");
    open();
  }

  return {
    open: open,
    close: close,
    reconnect: reconnect,
    setLastSeq: function (n) { if (typeof n === "number" && n > lastSeq) lastSeq = n; },
    getLastSeq: function () { return lastSeq; },
    getLastErrorAt: function () { return lastErrAt; }
  };
}

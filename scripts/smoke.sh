#!/usr/bin/env bash
# v0.0.1 发布冒烟（Linux/macOS，真实二进制 + mock 模型，无需 API Key）。
#
# 覆盖：/api/health(schema_version) -> /api/setup -> /api/bootstrap ->
#       建项目会话 -> 发消息 -> 列表 -> /api/search -> /api/audit -> /api/events(SSE 头)
#
# 用法：
#   go build -o bin/lignting ./cmd/lignting && ./scripts/smoke.sh
#   BIN=./bin/lignting PORT=18891 ./scripts/smoke.sh
set -euo pipefail

BIN="${BIN:-./bin/lignting}"
PORT="${PORT:-18891}"
BASE="http://127.0.0.1:${PORT}"
DATA_DIR="$(mktemp -d)"
JAR="$(mktemp)"
LOG="${DATA_DIR}/server.log"
PID=""
CSRF=""
FAILED=0

cleanup() {
  if [[ -n "${PID}" ]] && kill -0 "${PID}" 2>/dev/null; then kill "${PID}" 2>/dev/null || true; wait "${PID}" 2>/dev/null || true; fi
  if [[ "${FAILED:-0}" == "1" ]]; then echo "--- server log ---"; tail -n 30 "${LOG}" || true; fi
  rm -rf "${DATA_DIR}" "${JAR}"
}
trap cleanup EXIT

fail() { FAILED=1; echo "FAIL: $*" >&2; exit 1; }
json() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))' "$1"; }
step() { echo "== $*"; }

curl_json() { # curl_json METHOD PATH [BODY] [EXTRA_HEADERS...]
  local method="$1" path="$2" body="${3:-}"
  shift 3 || true
  local args=(-sS -b "${JAR}" -c "${JAR}" -X "${method}" "${BASE}${path}" -H "Origin: ${BASE}")
  if [[ -n "${body}" ]]; then args+=(-H "Content-Type: application/json" -d "${body}"); fi
  if [[ -n "${CSRF}" ]]; then args+=(-H "X-Lignting-CSRF: ${CSRF}"); fi
  for h in "$@"; do args+=(-H "${h}"); done
  curl "${args[@]}"
}

[[ -x "${BIN}" ]] || fail "二进制不存在或不可执行: ${BIN}（先 go build -o bin/lignting ./cmd/lignting）"

step "启动真实二进制（--mock-model，data-dir=${DATA_DIR}, addr=127.0.0.1:${PORT}）"
"${BIN}" --data-dir "${DATA_DIR}" --addr "127.0.0.1:${PORT}" --mock-model >"${LOG}" 2>&1 &
PID=$!
for _ in $(seq 1 100); do
  if curl -sf "${BASE}/api/health" >/dev/null 2>&1; then break; fi
  sleep 0.1
done
curl -sf "${BASE}/api/health" >/dev/null || fail "health 不可用"

step "--version"
VER_OUT="$("${BIN}" --version 2>&1)"
echo "  ${VER_OUT}"
[[ "${VER_OUT}" == "lignting 0.0.1" ]] || fail "--version 输出不符: ${VER_OUT}"

step "GET /api/health"
HEALTH="$(curl_json GET /api/health)"
SCHEMA="$(json 'd["schema_version"]' <<<"${HEALTH}")"
VERSION="$(json 'd["version"]' <<<"${HEALTH}")"
MODEL="$(json 'd["model"]' <<<"${HEALTH}")"
echo "  schema_version=${SCHEMA} version=${VERSION} model=${MODEL}"
[[ "${SCHEMA}" == "3" ]] || fail "schema_version=${SCHEMA}, want 3"
[[ "${VERSION}" == "0.0.1" ]] || fail "version=${VERSION}, want 0.0.1"

step "POST /api/setup（owner 初始化）"
SETUP="$(curl_json POST /api/setup '{"owner_name":"阿哲","daily_budget_cny":20,"monthly_budget_cny":300}')"
[[ "$(json 'd["ok"]' <<<"${SETUP}")" == "True" ]] || fail "setup 失败: ${SETUP}"
CSRF="$(json 'd["data"]["csrf_token"]' <<<"${SETUP}")"
[[ -n "${CSRF}" ]] || fail "setup 未返回 csrf_token"

step "GET /api/bootstrap（会话已建立）"
BOOT="$(curl_json GET /api/bootstrap)"
[[ "$(json 'd["data"]["authenticated"]' <<<"${BOOT}")" == "True" ]] || fail "bootstrap 未认证: ${BOOT}"

step "POST /api/conversations（建项目会话）"
CONV="$(curl_json POST /api/conversations '{"type":"project","title":"发布冒烟","visibility":"project"}')"
CONV_ID="$(json 'd["data"]["id"]' <<<"${CONV}")"
[[ -n "${CONV_ID}" ]] || fail "建会话失败: ${CONV}"

step "POST /api/conversations/{id}/messages（发消息）"
MSG="$(curl_json POST "/api/conversations/${CONV_ID}/messages" '{"content":"发布冒烟消息：限流与审计","client_id":"smoke-1"}')"
[[ "$(json 'd["data"]["created"]' <<<"${MSG}")" == "True" ]] || fail "发消息失败: ${MSG}"
SEQ="$(json 'd["data"]["message"]["seq"]' <<<"${MSG}")"
echo "  message seq=${SEQ}"

step "GET /api/conversations/{id}/messages（列表）"
LIST="$(curl_json GET "/api/conversations/${CONV_ID}/messages")"
TOTAL="$(json 'd["data"]["total"]' <<<"${LIST}")"
[[ "${TOTAL}" -ge 1 ]] || fail "消息列表 total=${TOTAL}"

step "GET /api/search（写入后可搜）"
SEARCH="$(curl_json GET "/api/search?q=%E9%99%90%E6%B5%81&kind=message")"
HITS="$(json 'd["data"]["total"]' <<<"${SEARCH}")"
[[ "${HITS}" -ge 1 ]] || fail "检索 total=${HITS}"
echo "  search total=${HITS}"

step "GET /api/audit（审计有记录）"
AUDIT="$(curl_json GET "/api/audit?limit=10")"
AUDIT_N="$(python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(len(d.get("items") or []))' <<<"${AUDIT}")"
[[ "${AUDIT_N}" -ge 1 ]] || fail "审计为空"
echo "  audit items=${AUDIT_N}"

step "GET /api/events?since=0（SSE 事件流可读）"
EVENTS_HEAD="$(curl -sS -o /dev/null -w '%{http_code} %{content_type}' -b "${JAR}" -H "Origin: ${BASE}" \
  --max-time 2 "${BASE}/api/events?since=0" 2>/dev/null || true)"
echo "  ${EVENTS_HEAD}"
[[ "${EVENTS_HEAD}" == 200*text/event-stream* ]] || fail "SSE 事件流头不符: ${EVENTS_HEAD}"

echo
echo "PASS: 发布冒烟全绿（schema_version=${SCHEMA}，消息 seq=${SEQ}，检索 total=${HITS}，审计 ${AUDIT_N} 条）"

#!/usr/bin/env bash
# smoke-server.sh —— 不依赖 Windows 客户端的服务端集成校验。
#
# 校验 3 件事（对应 protocol-design §2.5 / §3.3 / 端点 1、10）：
#   (a) GET  /api/v1/healthz            -> {"success":true,"data":{"status":"ok"}}
#   (b) GET  /api/v1/bootstrap (Bearer) -> success:true 且含 me/channels/ws_url
#   (c) WS   /ws 首帧 {type:auth}        -> 收到 {type:auth_ok, data:{user,...}}
#
# access_token 来源二选一：
#   - 环境变量 ACCESS_TOKEN=<JWT>（真实 IdP 测试令牌，或 scripts/gen-test-jwt.* 产的自签测试 JWT）
#   - 不提供则仅跑 (a)，(b)/(c) 跳过并提示。
#
# 用法:
#   API_BASE=http://127.0.0.1:8080 WS_URL=ws://127.0.0.1:8080/ws ACCESS_TOKEN=<JWT> \
#     bash scripts/smoke-server.sh
#
# 默认: API_BASE=http://127.0.0.1:8080  WS_URL=ws://127.0.0.1:8080/ws
# 依赖: curl（必需）；jq（强烈建议，用于断言）；websocat 或 python3（WS 首帧，二选一）。
set -uo pipefail

API_BASE="${API_BASE:-http://127.0.0.1:8080}"
WS_URL="${WS_URL:-ws://127.0.0.1:8080/ws}"
ACCESS_TOKEN="${ACCESS_TOKEN:-}"
API_PREFIX="${API_PREFIX:-/api/v1}"

pass=0; fail=0; skip=0
ok()   { echo "  [OK]  $*"; pass=$((pass+1)); }
bad()  { echo "  [!!]  $*"; fail=$((fail+1)); }
warn() { echo "  [--]  $*"; skip=$((skip+1)); }

command -v curl >/dev/null 2>&1 || { echo "错误: 需要 curl" >&2; exit 2; }
have_jq=0; command -v jq >/dev/null 2>&1 && have_jq=1
[ "$have_jq" -eq 1 ] || echo "提示: 未装 jq，断言退化为字符串匹配。" >&2

echo "=== Lumen 服务端冒烟 ==="
echo "API_BASE=$API_BASE  WS_URL=$WS_URL  token=$([ -n "$ACCESS_TOKEN" ] && echo present || echo absent)"
echo ""

# ── (a) healthz（public）────────────────────────────────────────────
echo "(a) GET ${API_PREFIX}/healthz"
health="$(curl -fsS --max-time 10 "${API_BASE}${API_PREFIX}/healthz" 2>/dev/null)" || {
  bad "请求失败（服务端未起？检查 ${API_BASE}）"; health=""
}
if [ -n "$health" ]; then
  if [ "$have_jq" -eq 1 ]; then
    if [ "$(echo "$health" | jq -r '.success')" = "true" ] && \
       [ "$(echo "$health" | jq -r '.data.status')" = "ok" ]; then
      ok "healthz 返回 success:true data.status:ok"
    else
      bad "healthz 响应不符: $health"
    fi
  else
    case "$health" in
      *'"success":true'*'"status":"ok"'*|*'"status":"ok"'*'"success":true'*) ok "healthz OK（字符串匹配）";;
      *) bad "healthz 响应不符: $health";;
    esac
  fi
fi
echo ""

# ── (b) bootstrap（Bearer）──────────────────────────────────────────
echo "(b) GET ${API_PREFIX}/bootstrap  (Authorization: Bearer)"
if [ -z "$ACCESS_TOKEN" ]; then
  warn "未提供 ACCESS_TOKEN，跳过 bootstrap（设 ACCESS_TOKEN=<JWT> 后重跑）"
else
  body="$(curl -sS --max-time 15 -H "Authorization: Bearer ${ACCESS_TOKEN}" \
            -w $'\n%{http_code}' "${API_BASE}${API_PREFIX}/bootstrap" 2>/dev/null)"
  code="$(printf '%s' "$body" | tail -n1)"
  json="$(printf '%s' "$body" | sed '$d')"
  echo "  HTTP $code"
  if [ "$code" = "200" ]; then
    if [ "$have_jq" -eq 1 ]; then
      if [ "$(echo "$json" | jq -r '.success')" = "true" ] && \
         [ "$(echo "$json" | jq -e '.data.me and (.data.channels|type=="array") and (.data.ws_url|type=="string")' >/dev/null 2>&1; echo $?)" = "0" ]; then
        ok "bootstrap success:true，含 me/channels/ws_url"
        echo "        me.display_name = $(echo "$json" | jq -r '.data.me.display_name // "?"'), channels = $(echo "$json" | jq -r '.data.channels|length'), ws_url = $(echo "$json" | jq -r '.data.ws_url')"
      else
        bad "bootstrap 响应结构不符: $json"
      fi
    else
      case "$json" in *'"success":true'*) ok "bootstrap 200 success:true（字符串匹配）";; *) bad "bootstrap 响应不符: $json";; esac
    fi
  elif [ "$code" = "401" ]; then
    bad "401 未通过鉴权：核对 token 的 iss/aud/exp 与服务端 LUMEN_OAUTH_* 是否一致（用 scripts/decode-jwt.sh）"
  else
    bad "非预期状态码 $code: $json"
  fi
fi
echo ""

# ── (c) WS 首帧 auth -> auth_ok ─────────────────────────────────────
echo "(c) WS ${WS_URL}  首帧 {type:auth} -> 期望 {type:auth_ok}"
if [ -z "$ACCESS_TOKEN" ]; then
  warn "未提供 ACCESS_TOKEN，跳过 WS auth"
else
  auth_frame="{\"type\":\"auth\",\"data\":{\"access_token\":\"${ACCESS_TOKEN}\"}}"
  resp=""
  if command -v websocat >/dev/null 2>&1; then
    # -n1 收一帧后退出；--no-close 让服务端先回帧
    resp="$(printf '%s\n' "$auth_frame" | timeout 12 websocat -n1 -B 1048576 "$WS_URL" 2>/dev/null || true)"
  elif command -v python3 >/dev/null 2>&1; then
    resp="$(WS_URL="$WS_URL" AUTH_FRAME="$auth_frame" timeout 15 python3 "$(dirname "$0")/_ws_auth.py" 2>/dev/null || true)"
  else
    warn "未找到 websocat 或 python3，无法测 WS 首帧（装 websocat 或 python3+websockets 后重跑）"
  fi

  if [ -n "$resp" ]; then
    echo "  收到: $resp"
    if [ "$have_jq" -eq 1 ]; then
      typ="$(echo "$resp" | jq -r '.type // empty' 2>/dev/null)"
      case "$typ" in
        auth_ok)
          if [ "$(echo "$resp" | jq -e '.data.user' >/dev/null 2>&1; echo $?)" = "0" ]; then
            ok "auth_ok 且含 data.user（sub=$(echo "$resp" | jq -r '.data.user.oauth_subject // "?"')）"
          else
            bad "auth_ok 但缺 data.user: $resp"
          fi ;;
        auth_error)
          bad "auth_error code=$(echo "$resp" | jq -r '.data.code // "?"')（核对 token/软封禁）" ;;
        *) bad "未预期首帧 type='$typ': $resp" ;;
      esac
    else
      case "$resp" in
        *'"type":"auth_ok"'*) ok "auth_ok（字符串匹配）";;
        *'"type":"auth_error"'*) bad "auth_error: $resp";;
        *) bad "未预期首帧: $resp";;
      esac
    fi
  elif command -v websocat >/dev/null 2>&1 || command -v python3 >/dev/null 2>&1; then
    bad "WS 无响应（服务端未起 / WS_URL 错 / 握手超时）"
  fi
fi

echo ""
echo "=== 汇总: OK=$pass  FAIL=$fail  SKIP=$skip ==="
[ "$fail" -eq 0 ]

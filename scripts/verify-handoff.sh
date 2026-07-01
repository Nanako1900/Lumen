#!/usr/bin/env bash
# verify-handoff.sh —— 半自动核对「桌面经官网登录」的回环 handoff（无需 Windows 客户端）。
#
# 复刻桌面侧回环 handoff 时序（web-design §5.2 / protocol §2.2），用浏览器手动完成 IdP 登录：
#   1) 本脚本生成 handoff_verifier + state + challenge=S256(verifier)
#   2) 打印 example.com/desktop/login?redirect_uri=http://127.0.0.1:<port>/cb&state&challenge
#      —— 你在浏览器打开它，完成 IdP 登录，浏览器最终 302 到 127.0.0.1:<port>/cb?handoff_code&state
#   3) 你把浏览器地址栏最终的 http://127.0.0.1:<port>/cb?... 整个 URL 粘回本脚本
#   4) 脚本校验:
#        - 回环 URL 里 **没有** access_token（安全红线：access_token 绝不进 URL）
#        - state 与本地一致
#        - 取出 handoff_code
#   5) POST example.com/api/desktop/exchange {handoff_code, handoff_verifier}
#      -> 期望 {access_token, expires_in, desktop_session_id, profile}
#   6) 校验响应体里 **没有** refresh_token（不下发桌面），并展示 access_token 的 aud/iss/exp
#      （可选，用 decode-jwt.sh）
#
# 用法:
#   WEB_BASE=https://example.com bash scripts/verify-handoff.sh
#   WEB_BASE=http://localhost:8788 LOOPBACK_PORT=53123 bash scripts/verify-handoff.sh
#
# 依赖: bash、curl、openssl（S256）、base64；jq 可选。
set -uo pipefail

WEB_BASE="${WEB_BASE:-https://example.com}"
LOOPBACK_PORT="${LOOPBACK_PORT:-52847}"
REDIRECT_PATH="${REDIRECT_PATH:-/cb}"
REDIRECT_URI="http://127.0.0.1:${LOOPBACK_PORT}${REDIRECT_PATH}"

for c in curl openssl base64; do
  command -v "$c" >/dev/null 2>&1 || { echo "错误: 需要 $c" >&2; exit 2; }
done
have_jq=0; command -v jq >/dev/null 2>&1 && have_jq=1
here="$(cd "$(dirname "$0")" && pwd)"

# base64url（无填充）
b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
# 高熵随机 -> base64url
rand_token() { openssl rand 32 | b64url; }
# S256(verifier) -> base64url（对 ASCII verifier 串做 SHA-256）
s256() { printf '%s' "$1" | openssl dgst -binary -sha256 | b64url; }

handoff_verifier="$(rand_token)"
state="$(rand_token)"
challenge="$(s256 "$handoff_verifier")"

# URL-encode（纯 shell，逐字节；保留 RFC3986 unreserved 字符 A-Za-z0-9 -._~，其余 %HH）
urlenc() {
  local s="$1" i c out=""
  for (( i=0; i<${#s}; i++ )); do
    c="${s:i:1}"
    case "$c" in
      [A-Za-z0-9.~_-]) out+="$c" ;;
      *) out+="$(printf '%%%02X' "'$c")" ;;
    esac
  done
  printf '%s' "$out"
}

login_url="${WEB_BASE}/desktop/login?redirect_uri=$(urlenc "$REDIRECT_URI")&state=$(urlenc "$state")&challenge=$(urlenc "$challenge")"

cat <<EOF
=== Lumen 桌面登录 handoff 半自动核对 ===
WEB_BASE      = $WEB_BASE
redirect_uri  = $REDIRECT_URI   （回环，仅 127.0.0.1；IdP 不登记它）
state         = ${state:0:12}...   （本地保存，用于校验回调来源）
challenge     = ${challenge:0:12}...   （= S256(handoff_verifier)，随 token 绑到 KV.bound_challenge）

步骤 1) 在浏览器打开下面的登录 URL，完成 IdP 登录：

  $login_url

  完成后浏览器会跳到形如：
    ${REDIRECT_URI}?handoff_code=<code>&state=${state:0:8}...
  （注意：该 URL 里**不应**出现 access_token；出现即为安全红线违规）

步骤 2) 把浏览器地址栏最终那条 http://127.0.0.1:${LOOPBACK_PORT}${REDIRECT_PATH}?... 完整粘到这里：
EOF

printf '回环回调 URL> '
read -r cb_url
cb_url="$(printf '%s' "$cb_url" | tr -d '[:space:]')"
[ -n "$cb_url" ] || { echo "错误: 未输入 URL" >&2; exit 1; }

echo ""
echo "=== 校验回环 URL ==="
rc=0

# 安全红线: 回环 URL 不得含 access_token
if printf '%s' "$cb_url" | grep -qiE '[?&#]access_token=|[?&#]token='; then
  echo "  [!!]  回环 URL 含 access_token —— 违反安全红线（access_token 绝不进 URL）"; rc=1
else
  echo "  [OK]  回环 URL 不含 access_token"
fi

# 若 IdP/官网出错，回调可能带 error=
if printf '%s' "$cb_url" | grep -qE '[?&]error='; then
  err="$(printf '%s' "$cb_url" | sed -n 's/.*[?&]error=\([^&]*\).*/\1/p')"
  echo "  [!!]  回环 URL 带错误 error=$err（登录未成功）"; rc=1
fi

# 取 query 里的字段
qget() { printf '%s' "$cb_url" | sed -n "s/.*[?&]$1=\([^&#]*\).*/\1/p"; }
got_state="$(qget state)"
handoff_code="$(qget handoff_code)"

# URL-decode（handoff_code/state 通常是 base64url，一般无需解码；state 若被编码则解码后比对）
urldec() { printf '%b' "$(printf '%s' "$1" | sed 's/+/ /g; s/%/\\x/g')"; }

if [ -z "$handoff_code" ]; then
  echo "  [!!]  回环 URL 缺 handoff_code"; rc=1
else
  echo "  [OK]  取到 handoff_code=${handoff_code:0:10}..."
fi

if [ "$got_state" = "$state" ] || [ "$(urldec "$got_state")" = "$state" ]; then
  echo "  [OK]  state 与本地一致"
else
  echo "  [!!]  state 不一致（回调 '${got_state:0:12}...' != 本地 '${state:0:12}...'）"; rc=1
fi

if [ "$rc" -ne 0 ] || [ -z "$handoff_code" ]; then
  echo ""; echo "回环校验未通过，终止（不进行 exchange）。"; exit "$rc"
fi

echo ""
echo "=== 步骤 3) POST ${WEB_BASE}/api/desktop/exchange ==="
resp="$(curl -sS --max-time 20 -X POST "${WEB_BASE}/api/desktop/exchange" \
  -H 'Content-Type: application/json' \
  -d "{\"handoff_code\":\"${handoff_code}\",\"handoff_verifier\":\"${handoff_verifier}\"}" \
  -w $'\n%{http_code}' 2>/dev/null)"
code="$(printf '%s' "$resp" | tail -n1)"
json="$(printf '%s' "$resp" | sed '$d')"
echo "  HTTP $code"

if [ "$code" != "200" ]; then
  echo "  [!!]  exchange 未成功: $json"; exit 1
fi

if [ "$have_jq" -eq 1 ]; then
  at="$(echo "$json" | jq -r '.access_token // empty')"
  sid="$(echo "$json" | jq -r '.desktop_session_id // empty')"
  exp_in="$(echo "$json" | jq -r '.expires_in // empty')"
  has_rt="$(echo "$json" | jq -r 'has("refresh_token")')"
  [ -n "$at" ]  && echo "  [OK]  返回 access_token（长度 ${#at}）" || { echo "  [!!]  缺 access_token"; rc=1; }
  [ -n "$sid" ] && echo "  [OK]  返回 desktop_session_id（长度 ${#sid}）" || { echo "  [!!]  缺 desktop_session_id"; rc=1; }
  [ -n "$exp_in" ] && echo "  [OK]  expires_in=$exp_in" || echo "  [--]  无 expires_in（桌面按保守默认处理）"
  if [ "$has_rt" = "true" ]; then
    echo "  [!!]  响应体含 refresh_token —— 违反红线（refresh_token 不出 Cloudflare、不下发桌面）"; rc=1
  else
    echo "  [OK]  响应体不含 refresh_token"
  fi
  echo "        profile.display_name = $(echo "$json" | jq -r '.profile.display_name // "?"')"
  # 二次消费应 404（一次性）
  echo ""
  echo "=== 步骤 4) 重放 handoff_code 应为一次性（期望 404）==="
  code2="$(curl -sS --max-time 15 -o /dev/null -w '%{http_code}' -X POST "${WEB_BASE}/api/desktop/exchange" \
    -H 'Content-Type: application/json' \
    -d "{\"handoff_code\":\"${handoff_code}\",\"handoff_verifier\":\"${handoff_verifier}\"}" 2>/dev/null)"
  if [ "$code2" = "404" ]; then echo "  [OK]  二次 exchange 返回 404（一次性消费生效）"; else echo "  [!!]  二次 exchange 返回 $code2（期望 404）"; rc=1; fi
  # 可选: 解码 access_token 核对 aud/iss
  if [ -n "$at" ] && [ -f "${here}/decode-jwt.sh" ]; then
    echo ""; echo "=== 步骤 5) 解码 access_token 核对 aud/iss/alg/exp ==="
    bash "${here}/decode-jwt.sh" "$at" || rc=1
  fi
else
  echo "  [--]  未装 jq，仅打印响应体（请肉眼核对：有 access_token+desktop_session_id、无 refresh_token）:"
  echo "$json"
fi

echo ""
echo "=== 汇总: $([ "$rc" -eq 0 ] && echo 全部通过 || echo 存在失败项) ==="
exit "$rc"

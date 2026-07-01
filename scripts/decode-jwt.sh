#!/usr/bin/env bash
# decode-jwt.sh —— 本地解码 JWT 的 header 与 payload（不验签、不联网）。
#
# 用途: 肉眼核对 access_token 的 alg / iss / aud / exp 是否符合 Lumen 约定：
#   - header.alg == RS256（服务端强制 RS256，防 alg 混淆/none）
#   - payload.iss == LUMEN_OAUTH_ISSUER
#   - payload.aud 含 "lumen-api"（= LUMEN_OAUTH_AUDIENCE）
#   - payload.exp 未过期
#
# 安全: 仅做 base64url 解码与字段展示；不发起任何网络请求。真实签名校验由
#       Go 服务端用 JWKS 完成。请勿在共享/公开环境粘贴真实 token。
#
# 用法:
#   bash scripts/decode-jwt.sh <JWT>
#   echo "$ACCESS_TOKEN" | bash scripts/decode-jwt.sh
#
# 依赖: bash、base64；jq 可选（有则美化 + 断言，无则原样输出 JSON）。
set -euo pipefail

fail() { echo "错误: $*" >&2; exit 1; }

jwt="${1:-}"
if [ -z "$jwt" ]; then
  # 从 stdin 读（允许管道传入），去掉首尾空白与换行
  if [ ! -t 0 ]; then
    jwt="$(cat)"
  fi
fi
jwt="$(printf '%s' "$jwt" | tr -d '[:space:]')"
[ -n "$jwt" ] || fail "未提供 JWT。用法: bash scripts/decode-jwt.sh <JWT>"

# base64url -> base64 -> decode（补齐 padding）
b64url_decode() {
  local data="$1"
  data="${data//-/+}"
  data="${data//_/\/}"
  case $(( ${#data} % 4 )) in
    2) data="${data}==" ;;
    3) data="${data}=" ;;
  esac
  printf '%s' "$data" | base64 -d 2>/dev/null
}

IFS='.' read -r header payload signature <<EOF
$jwt
EOF
[ -n "$header" ] && [ -n "$payload" ] && [ -n "$signature" ] || \
  fail "不是有效的 JWT（应为 header.payload.signature 三段）"

header_json="$(b64url_decode "$header")"   || fail "header base64url 解码失败"
payload_json="$(b64url_decode "$payload")" || fail "payload base64url 解码失败"
[ -n "$header_json" ] && [ -n "$payload_json" ] || fail "解码结果为空，token 可能损坏"

have_jq=0
command -v jq >/dev/null 2>&1 && have_jq=1

echo "=== JWT header ==="
if [ "$have_jq" -eq 1 ]; then echo "$header_json" | jq .; else echo "$header_json"; fi
echo ""
echo "=== JWT payload ==="
if [ "$have_jq" -eq 1 ]; then echo "$payload_json" | jq .; else echo "$payload_json"; fi
echo ""

# ── 断言/提示（需 jq）──────────────────────────────────────────────
if [ "$have_jq" -eq 0 ]; then
  echo "提示: 安装 jq 可自动核对 alg/iss/aud/exp。" >&2
  exit 0
fi

alg="$(echo "$header_json" | jq -r '.alg // empty')"
iss="$(echo "$payload_json" | jq -r '.iss // empty')"
exp="$(echo "$payload_json" | jq -r '.exp // empty')"
# aud 可能是字符串或数组，统一成行
aud_list="$(echo "$payload_json" | jq -r 'if (.aud|type)=="array" then .aud[] else (.aud // empty) end')"

echo "=== 核对结果 ==="
rc=0

# alg == RS256
if [ "$alg" = "RS256" ]; then
  echo "  [OK]  alg = RS256"
else
  echo "  [!!]  alg = '${alg:-<空>}'（期望 RS256；服务端强制 RS256，其它算法将被拒）"; rc=1
fi

# aud 含 lumen-api（可用 EXPECT_AUD 覆盖）
expect_aud="${EXPECT_AUD:-lumen-api}"
if printf '%s\n' "$aud_list" | grep -qx "$expect_aud"; then
  echo "  [OK]  aud 含 '$expect_aud'"
else
  echo "  [!!]  aud 不含 '$expect_aud'（实际: $(printf '%s ' $aud_list)）—— 服务端 LUMEN_OAUTH_AUDIENCE 校验会失败"; rc=1
fi

# iss（可用 EXPECT_ISS 覆盖；未设则仅展示）
if [ -n "${EXPECT_ISS:-}" ]; then
  if [ "$iss" = "$EXPECT_ISS" ]; then
    echo "  [OK]  iss == \$EXPECT_ISS"
  else
    echo "  [!!]  iss = '${iss:-<空>}' 与 \$EXPECT_ISS='$EXPECT_ISS' 不一致"; rc=1
  fi
else
  echo "  [..]  iss = '${iss:-<空>}'（设 EXPECT_ISS=<你的 issuer> 可自动比对）"
fi

# exp 未过期
if [ -n "$exp" ]; then
  now="$(date +%s)"
  if [ "$exp" -gt "$now" ]; then
    echo "  [OK]  exp 未过期（剩余 $(( exp - now )) 秒）"
  else
    echo "  [!!]  exp 已过期（$(( now - exp )) 秒前）"; rc=1
  fi
else
  echo "  [!!]  缺少 exp（服务端要求 exp 存在）"; rc=1
fi

exit "$rc"

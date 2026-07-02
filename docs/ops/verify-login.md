# 集成校验：登录 → access_token → 服务端 auth_ok（无客户端）

> 关联 Issue: #8（服务端+官网侧可验证部分；**全链路语音冒烟需 Windows 客户端**，见 §4）｜ 依据设计: [`protocol-design.md §2.5`](../design/protocol-design.md#25-ws-握手时序) / [`§6.1`](../design/protocol-design.md) / [`web-design.md §5`](../design/web-design.md#5-web-中介登录桌面)
>
> 目标：**不依赖 Windows 客户端**，验证「桌面经登录中介（Go 服务端）登录 → 拿 access_token → 连服务端 → 收 `auth_ok{user}`」这条链路中**服务端（资源服务器 + 登录中介）**可独立验证的部分。占位域名 `example.com`/`chat.example.com` 落地替换；本地用 `localhost`/`127.0.0.1`。

## 脚本一览（`scripts/`）

| 脚本 | 校验点 | 依赖 |
|------|--------|------|
| [`scripts/smoke-server.sh`](../../scripts/smoke-server.sh) | (b)/(c)：`GET /api/v1/healthz`；带 Bearer 的 `GET /api/v1/bootstrap`；WS 首帧 `auth` → `auth_ok{user}` | curl；jq（建议）；websocat 或 python3+websockets |
| [`scripts/verify-handoff.sh`](../../scripts/verify-handoff.sh) | (a)：半自动桌面登录回环（浏览器手动 → `handoff_code` → `POST /api/desktop/exchange`）；(c)：**access_token 不进回环 URL**；一次性消费；refresh_token 不下发 | curl、openssl、base64；jq（建议） |
| [`scripts/decode-jwt.sh`](../../scripts/decode-jwt.sh) | access_token 的 `alg`/`iss`/`aud`/`exp` 是否符合约定（不验签、不联网） | base64；jq（建议） |
| [`scripts/gen-test-jwt.py`](../../scripts/gen-test-jwt.py) | **无真实 IdP** 时生成 RS256 测试 JWT + 匹配 JWKS，校验服务端验签路径 | PyJWT、cryptography |
| [`scripts/_ws_auth.py`](../../scripts/_ws_auth.py) | （被 `smoke-server.sh` 调用的 WS 首帧辅助，无 websocat 时用） | python3+websockets |

---

## (a) 半自动核对桌面登录回环 handoff

复刻桌面回环 handoff 时序（[`web-design.md §5.2`](../design/web-design.md#52-桌面登录时序回环-handoff)），用浏览器手动完成 IdP 登录，脚本负责生成/校验参数并做 exchange：

```bash
# 生产（真实后端 + 真实 IdP）—— 登录中介在 Go 服务端 chat.example.com
WEB_BASE=https://chat.example.com bash scripts/verify-handoff.sh

# 本地（go run ./cmd/lumen-server 起中介 + 本地/测试 IdP client）
WEB_BASE=http://localhost:8080 LOOPBACK_PORT=53123 bash scripts/verify-handoff.sh
```

流程：

1. 脚本生成 `handoff_verifier` + `state` + `challenge=S256(handoff_verifier)`，打印一条 `chat.example.com/desktop/login?redirect_uri=http://127.0.0.1:<port>/cb&state=&challenge=` 登录 URL。
2. 在浏览器打开它，完成 IdP 登录；浏览器最终 302 到 `http://127.0.0.1:<port>/cb?handoff_code=&state=`。
3. 把地址栏那条回环 URL **整条**粘回脚本。
4. 脚本自动校验并 exchange，逐项打印 `[OK]/[!!]`：
   - **回环 URL 不含 `access_token`**（安全红线，[`web-design.md §8.1`](../design/web-design.md#81-安全红线强制)）；
   - `state` 与本地一致；取出 `handoff_code`；
   - `POST /api/desktop/exchange` 返回 `{access_token, expires_in, desktop_session_id, profile}`；
   - **响应体不含 `refresh_token`**（不下发桌面）；
   - 重放同一 `handoff_code` 返回 `404`（一次性消费）；
   - 用 `decode-jwt.sh` 核对拿到的 access_token 的 `aud/iss/alg/exp`。

> 该脚本覆盖 [`web-design.md §10.4`](../design/web-design.md#104-安全核对) 的三条 URL/token 红线中的两条（access_token 不进 URL、refresh_token 不下发），第三条（`client_secret` 不在前端产物）属构建产物审计，见 §5。

---

## (b)(c) 纯脚本打服务端（healthz / bootstrap / WS auth_ok）

拿到一个 `access_token`（来自 (a) 的 exchange，或真实 IdP 测试令牌，或 §D 的测试 JWT）后：

```bash
API_BASE=http://127.0.0.1:8080 \
WS_URL=ws://127.0.0.1:8080/ws \
ACCESS_TOKEN='<JWT>' \
  bash scripts/smoke-server.sh
```

脚本依次校验：

- **(a) `GET /api/v1/healthz`** → `{"success":true,"data":{"status":"ok"},"error":null}`（public，不需 token）。
- **(b) `GET /api/v1/bootstrap`（`Authorization: Bearer`）** → `success:true` 且 `data` 含 `me`/`channels`/`ws_url`（[`protocol §3.4 端点1`](../design/protocol-design.md#34-端点详情)）。401 表示 token 的 `iss`/`aud`/`exp` 与服务端 `LUMEN_OAUTH_*` 不一致——用 `decode-jwt.sh` 排查。
- **(c) WS `/ws` 首帧 `{"type":"auth","data":{"access_token":"..."}}`** → 期望 `{"type":"auth_ok","data":{"user":...}}`（[`protocol §2.5`](../design/protocol-design.md#25-ws-握手时序) / [`§4.3`](../design/protocol-design.md#43-鉴权类消息)）。收到 `auth_error` 则打印其 `code`（`TOKEN_INVALID`/`TOKEN_EXPIRED`/`KICKED`/`HANDSHAKE_TIMEOUT`）。

> 不提供 `ACCESS_TOKEN` 时仅跑 healthz，(b)/(c) 跳过（退出码仍 0）。有任一 `[!!]` 则退出码非 0，可用于 CI/冒烟门禁。
> WS 首帧优先用 `websocat`（`websocat -n1`），无则回退 `python3 scripts/_ws_auth.py`（需 `pip install websockets`）。

### access_token 声明核对

```bash
# 关注 alg==RS256、iss==你的 issuer、aud 含 lumen-api、exp 未过期
EXPECT_ISS="https://auth.example.com/realms/lumen" EXPECT_AUD="lumen-api" \
  bash scripts/decode-jwt.sh "$ACCESS_TOKEN"
```

---

## (D) 无真实 IdP：用测试 JWT 校验服务端验签路径

当手上没有真实 IdP 测试令牌时，可本地自签一个 RS256 JWT 并提供匹配 JWKS，让服务端走完整验签：

```bash
# 1) 生成测试 JWT + JWKS（写到 ./.local/，已被 .gitignore 覆盖）
ACCESS_TOKEN=$(python3 scripts/gen-test-jwt.py \
  --iss https://auth.example.com/realms/lumen \
  --aud lumen-api --sub sub-abc --name "Test User" --ttl 3600)

# 2) 托管 JWKS，并让服务端指向它（issuer/aud 与上面一致）
( cd .local && python3 -m http.server 9999 ) &     # 托管 .local/jwks.json
export LUMEN_OAUTH_JWKS_URL=http://127.0.0.1:9999/jwks.json
export LUMEN_OAUTH_ISSUER='https://auth.example.com/realms/lumen'
export LUMEN_OAUTH_AUDIENCE='lumen-api'
export LUMEN_OWNER_SUBJECTS='sub-abc'    # 让该 sub 成为 owner（可选）
#   ...（其余 LUMEN_* 见 docs/DEV.md 附录 A），然后启动服务端

# 3) 打服务端
API_BASE=http://127.0.0.1:8080 WS_URL=ws://127.0.0.1:8080/ws ACCESS_TOKEN="$ACCESS_TOKEN" \
  bash scripts/smoke-server.sh
```

> ⚠ **仅限本地/测试**：生成的私钥、测试 JWKS、测试 issuer 绝不可用于生产（生产用真实 IdP 的 JWKS）。
> ⚠ **服务端 JWKS 必须 HTTPS 红线**（[`server-design.md §6.6`](../design/server-design.md#66-安全注意)）：若服务端强制 `https://` 的 JWKS URL，则上面的 `http://127.0.0.1:9999` 会被拒——此时改用 HTTPS 托管（如本地自签 TLS）或直接用真实 IdP 的测试令牌跑 (b)/(c)。测试 JWT 方式主要用于验签逻辑联调与开发环境；生产路径以真实 IdP 令牌为准。

---

## §5 安全核对清单（服务端 + 官网侧，可脚本化）

对齐 [`web-design.md §10.4`](../design/web-design.md#104-安全核对) 与 [`server-design.md §6.6`](../design/server-design.md#66-安全注意)：

- [ ] `access_token` **不出现在任何回环 URL**（`verify-handoff.sh` 自动检测；红线）。
- [ ] `refresh_token` **不出现在任何下发桌面的响应体**（`verify-handoff.sh` 检测 exchange 响应）。
- [ ] `handoff_code` **一次性消费**（`verify-handoff.sh` 重放得 `404`）。
- [ ] `/desktop/login` **拒绝非 `127.0.0.1` 回环** `redirect_uri`（返回 `400`）：
      ```bash
      curl -s -o /dev/null -w '%{http_code}\n' \
        "$WEB_BASE/desktop/login?redirect_uri=https://evil.example&state=x&challenge=y"   # 期望 400
      ```
- [ ] access_token `alg==RS256`、`iss`/`aud` 对齐（`decode-jwt.sh`）。
- [ ] `GET /api/v1/healthz` 通过（Coolify 探活口径一致）。
- [ ] `client_secret` **不在前端构建产物**（官网 `dist/`）中：`grep -r` 审计（构建后）：
      ```bash
      ( cd website && npm run build && ! grep -rIn --exclude-dir=node_modules "$KNOWN_SECRET_SUBSTRING" dist/ ) \
        && echo "OK: 产物无 secret" || echo "!! 产物疑似含 secret"
      ```
      （落地时用真实 secret 的一个片段做 needle；或直接确认 secret 仅注入 Go 服务端环境变量、前端产物不含任何 secret。）

---

## §4 需客户端的剩余项（属 #8）

以下必须**真实 Windows 客户端**参与，本轮不覆盖，作为 #8 的剩余验收项：

- [ ] 桌面起 `127.0.0.1:rand` 回环监听、开系统浏览器、自动收 `handoff_code`（本轮由人手动粘 URL 替代）。
- [ ] `desktop_session_id` 落 **Windows 凭据库（DPAPI）**；重启客户端**免重登**（用 `desktop_session_id` 静默刷新 access_token）。
- [ ] 桌面用 access_token 连 **WSS**（生产 TLS，经 Traefik）收 `auth_ok{user}`（本轮用本地 `ws://` 验证协议层）。
- [ ] **全链路语音冒烟**：两个客户端进同一语音频道 → WS 信令 SFU 重协商 → DTLS-SRTP 建立 → 互相听到 Opus（[`00-overview §6.1`](../design/00-overview.md#61-v0--最小开黑回路) 验收）。
- [ ] `access_token` 临期经 `/api/desktop/refresh` 换新；`SESSION_INVALID` 转重新登录；`/api/desktop/logout` 清凭据 + 关 WS + 重置 store。

> 结论：**服务端 + 官网侧的登录/验签/auth_ok 链路可用上面脚本独立验证**；带 DPAPI、系统浏览器自动回收与语音媒体的完整端到端冒烟，待客户端落地后在 #8 完成。

# IdP 登记与配置对齐（OAuth2/OIDC）

> 关联 Issue: #26 ｜ 依据设计: [`web-design.md §3.3`](../design/web-design.md#33-idp-须登记的回调-url) / [`§7.2`](../design/web-design.md#72-idp-侧登记要求) / [`§9`](../design/web-design.md#9-配置环境变量kvsecrets)、[`00-overview.md §7`](../design/00-overview.md#7-部署与运维总览)、[`protocol-design.md §2`](../design/protocol-design.md#2-鉴权流程总览)
>
> **占位域名说明**：本文所有 `example.com` / `chat.example.com` / `auth.example.com` 均为**占位符**，落地时统一替换为真实域名。`sub-abc,sub-def` 等 subject 亦为占位。
>
> **权威口径**：接口契约以 [`protocol-design.md`](../design/protocol-design.md) 为准；域名/端口/命名以设计文档为准。本文只描述「如何在 IdP 与各配置面登记/对齐」，不引入任何新契约。

Lumen 对接**外部** OAuth2/OIDC 身份服务器（IdP），**不自建**。三方必须对同一 issuer / audience / 回调 / scope 达成一致：

1. **IdP**（如 Keycloak / Auth0 / Logto）——登记官网 client、回调、audience、scope。
2. **官网 Worker**（Cloudflare Pages Functions）——confidential OIDC client，持 `client_secret`，跑 Authorization Code + PKCE。
3. **Go 服务端**（Coolify）——只用 IdP 的 JWKS 本地验 `access_token`（验 `iss`/`aud`/`exp`），不感知官网。

---

## 1. 角色与信任边界（先读）

| 角色 | 是否持 `client_secret` | 是否持 `refresh_token` | 与 IdP 的交互 |
|------|:---:|:---:|------|
| 官网 Worker | ✅（仅 CF 加密环境变量） | ✅（仅存 KV `SESSIONS`，不出 Cloudflare） | authorize / token / refresh / userinfo / (revoke) |
| Go 服务端 | ❌ | ❌ | 仅拉 JWKS（+ 可选 userinfo 兜底），本地验签 |
| 桌面客户端 | ❌ | ❌（只持 `desktop_session_id`） | **不直连 IdP**（全部委托官网） |

> 红线：`client_secret` 只在官网 Worker；`refresh_token` 永不下发桌面；`access_token` 绝不进任何 URL（见 [`web-design.md §8.1`](../design/web-design.md#81-安全红线强制)）。

---

## 2. 必须在 IdP 登记的项

官网注册为**一个 confidential client**（带 `client_secret`），供「桌面登录中介」与「账户中心网页登录」共用。

### 2.1 回调 URL（redirect_uri）

在 IdP 该 client 下登记以下**两个**回调（均在官网域、HTTPS）：

| 回调 URL | 用途 |
|----------|------|
| `https://example.com/desktop/callback` | 桌面 Web 中介登录（`GET /desktop/callback`） |
| `https://example.com/auth/callback` | 账户中心网页登录（`GET /auth/callback`） |

> **不要**在 IdP 登记桌面回环地址 `http://127.0.0.1:<port>/cb`——它只是官网 Worker 的 handoff 回环目标，由 Worker 自行校验（仅允许 `http://127.0.0.1:<port>/...`，拒 `localhost` 主机名以防 DNS 重绑定）；IdP 永远只回到 `example.com/desktop/callback`（[`web-design.md §3.3`](../design/web-design.md#33-idp-须登记的回调-url)）。
>
> 预览环境请另建独立 client 或独立 redirect 白名单（如 `https://<preview>.pages.dev/desktop/callback`），避免污染生产（[`web-design.md §2.3`](../design/web-design.md#23-构建与发布要点)）。

### 2.2 授权类型与 PKCE

- 授权类型：`authorization_code` + `refresh_token`。
- **PKCE（S256）**：官网虽为 confidential client，仍启用 PKCE（授权码 + code_verifier 双保险）。
- Client 类型：**confidential**（带 `client_secret`）。

### 2.3 scope

| 流程 | scope | 说明 |
|------|-------|------|
| 桌面 Web 中介登录 | `openid profile email offline_access` | 需要 `offline_access` 以拿 `refresh_token`（存 KV `SESSIONS`） |
| 账户中心网页登录 | `openid profile email` | **不需要** `offline_access`（无离线刷新需求），**不需要** `aud=lumen-api`（不调 Lumen API） |

### 2.4 audience（关键：令 access_token 的 `aud` 含 `lumen-api`）

Go 服务端用 `LUMEN_OAUTH_AUDIENCE=lumen-api` 校验 `access_token.aud`。**只有桌面登录流程**需要令 `access_token` 的 `aud` 含 `lumen-api`；账户中心流程不需要。三种 IdP 的做法见 §3。

---

## 3. 令 `aud` 含 `lumen-api` —— 三种 IdP 示例

> 目标一致：桌面登录拿到的 `access_token`（JWT）其 `aud` claim **包含 `lumen-api`**，且 `iss` 等于服务端 `LUMEN_OAUTH_ISSUER`，签名算法为 **RS256**（服务端强制 RS256 防 alg 混淆/none）。

### 3.1 Keycloak

Keycloak 默认把 client_id 放入 `aud`，需**显式**加一个 audience。两种常见做法（任选其一）：

**做法 A — 定义一个 client scope，挂 Audience mapper（推荐）**

1. 若尚无「API 资源」client，可新建一个 client `lumen-api`（access type: bearer-only 或 confidential 均可，仅作为 audience 标识存在）。
2. Client scopes → 新建 `lumen-api-audience`（或复用已有 scope）→ Add mapper → **Audience**：
   - `Included Client Audience` = `lumen-api`（或 `Included Custom Audience` = `lumen-api`）
   - `Add to access token` = ON
3. 将该 client scope 作为 **Default** 或 **Optional** 挂到官网 client（`lumen-website`）：
   - Default：每次登录 `aud` 恒含 `lumen-api`。
   - Optional：官网在 `/authorize` 时带 `scope=... lumen-api-audience` 才注入（官网可在桌面流程附加该 scope，账户中心流程不带）。
4. 校验：登录后解码 access_token，确认 `"aud"` 含 `"lumen-api"`、`"iss"` 为 realm issuer、`alg` 为 `RS256`。

> Keycloak 无 `audience`/`resource` 请求参数；audience 由 mapper/scope 决定，官网请求端**不需**传 audience 参数（`OIDC_AUDIENCE` 在官网仅作对齐记录/校验用，见 §4）。

**做法 B — 直接在官网 client 上加 Audience mapper**：官网 client → Client scopes → `<client>-dedicated` → Add mapper → Audience（同上），最简单，但该 client 所有流程（含账户中心）都会带 `aud=lumen-api`——可接受（账户中心不调 API，多一个 aud 无害），若要精确区分请用做法 A 的 Optional scope。

- issuer 形如：`https://auth.example.com/realms/lumen`
- JWKS 形如：`https://auth.example.com/realms/lumen/protocol/openid-connect/certs`
- authorize/token/userinfo：`.../protocol/openid-connect/{auth,token,userinfo}`

### 3.2 Auth0

Auth0 用 **API（Resource Server）** + `audience` 请求参数。

1. 建一个 **API**：Dashboard → Applications → APIs → Create API
   - **Identifier（= audience）** = `lumen-api`（此值即 `access_token.aud`；Auth0 惯例用 URL，但用 `lumen-api` 亦可——必须与服务端 `LUMEN_OAUTH_AUDIENCE` **逐字一致**）。
   - Signing Algorithm: **RS256**。
   - 可开启 `Allow Offline Access`（发 `refresh_token`）。
2. 建/用一个 **Regular Web Application**（confidential）作为官网 client；在其 Settings 里登记两个 callback URL（§2.1）。
3. 官网 Worker 在构造 `/authorize` 时**必须带** `audience=lumen-api`（Auth0 只有带 audience 才签发含该 `aud` 的 JWT access_token；否则可能返回不透明 token）。桌面流程带 `audience`；账户中心流程不带。
4. `refresh_token` 需在 `/authorize` 带 `scope=offline_access`（且 API 允许 Offline Access）。

- issuer 形如：`https://<tenant>.us.auth0.com/`（注意 Auth0 issuer **带结尾斜杠**，服务端 `LUMEN_OAUTH_ISSUER` 必须与之逐字一致）。
- JWKS：`https://<tenant>.us.auth0.com/.well-known/jwks.json`
- 端点由 `https://<tenant>.us.auth0.com/.well-known/openid-configuration` 发现。

### 3.3 Logto

Logto 用 **API resource** + `resource` 参数（RFC 8707 Resource Indicators）。

1. Console → API resources → Create：
   - **API Identifier（= resource indicator）** = `lumen-api`（该值即进入 `access_token.aud`；须与服务端 `LUMEN_OAUTH_AUDIENCE` 逐字一致）。
2. Console → Applications → 建 **Traditional Web**（confidential）作为官网 client；登记两个 Redirect URI（§2.1）。
3. 官网 Worker 在 `/authorize` 带 `resource=lumen-api`（Logto 据 `resource` 决定 `aud`）；并带 `scope=openid profile email offline_access`（`prompt=consent` 视配置而定以确保发 refresh_token）。桌面流程带 `resource`；账户中心流程不带。

- issuer 形如：`https://<your>.logto.app/oidc`
- JWKS：`https://<your>.logto.app/oidc/jwks`
- 端点由 `https://<your>.logto.app/oidc/.well-known/openid-configuration` 发现。

> **官网 Worker 侧如何附加 audience/resource**：`GET /desktop/login` 构造 IdP `/authorize` URL 时按 IdP 类型注入——Auth0 用 `audience=<OIDC_AUDIENCE>`，Logto 用 `resource=<OIDC_AUDIENCE>`，Keycloak 不传参数（靠 mapper/scope）。该逻辑在 `functions/_lib/oidc.ts` 的 `buildAuthorizeUrl`（[`web-design.md §5.5`](../design/web-design.md#55-worker-伪代码骨架typescript)）。

---

## 4. 配置对齐矩阵（三方一致）

以下每一行的值，必须在**官网 Worker / Go 服务端 env / IdP** 三处对齐（IdP 侧多为「登记/推导」而非「填值」）。落地时把占位替换为真实值后逐行核对。

| 对齐项 | 官网 Worker（CF env/Secret） | Go 服务端 env（Coolify） | IdP 侧 | 占位/示例 | 必须逐字一致？ |
|--------|------------------------------|--------------------------|--------|-----------|:---:|
| **issuer** | `OIDC_ISSUER` | `LUMEN_OAUTH_ISSUER` | realm/tenant 的 issuer（`.well-known` 的 `issuer`） | `https://auth.example.com/realms/lumen` | ✅（含结尾斜杠差异，如 Auth0） |
| **JWKS URL** | （由 discovery 推导，可选显式 `OIDC_*`） | `LUMEN_OAUTH_JWKS_URL` | JWKS 端点 | `https://auth.example.com/realms/lumen/protocol/openid-connect/certs` | 指向同一 issuer 的 JWKS |
| **audience** | `OIDC_AUDIENCE` | `LUMEN_OAUTH_AUDIENCE` | API/Resource 标识（Keycloak: mapper；Auth0: API Identifier；Logto: API resource） | `lumen-api` | ✅ |
| **userinfo（兜底）** | `OIDC_USERINFO_URL` | `LUMEN_OAUTH_USERINFO_URL`（可选，缺省由 discovery 推导） | userinfo 端点 | `https://auth.example.com/realms/lumen/protocol/openid-connect/userinfo` | 指向同一 issuer |
| **authorize/token** | `OIDC_AUTHORIZE_URL` / `OIDC_TOKEN_URL` | —（服务端不用） | 授权/令牌端点 | `.../openid-connect/auth`、`.../openid-connect/token` | 指向同一 issuer |
| **client_id** | `OIDC_CLIENT_ID` | —（服务端只验 `aud`，不需要 client_id） | 官网 client 的 ID | `lumen-website` | 官网 ↔ IdP 一致 |
| **client_secret** | `OIDC_CLIENT_SECRET`（**Secret**） | —（绝不持有） | 官网 client 的 secret | `***`（仅 Worker） | 官网 ↔ IdP 一致 |
| **desktop redirect** | `OIDC_DESKTOP_REDIRECT_URI` | — | 登记的桌面回调 | `https://example.com/desktop/callback` | ✅（IdP 登记 == Worker 传值） |
| **web redirect** | `OIDC_WEB_REDIRECT_URI` | — | 登记的账户中心回调 | `https://example.com/auth/callback` | ✅ |
| **scope（桌面）** | 代码内 `openid profile email offline_access` | — | 允许该 scope | — | offline_access 决定发 refresh_token |
| **官网域** | `WEB_BASE_URL` | —（服务端无需 CORS，见下） | redirect 域 = 官网域 | `https://example.com` | 桌面 `LUMEN_WEB_BASE_URL` 亦须一致 |
| **API 子域** | `UPDATES_LATEST_URL`（读 `chat.*`）| `LUMEN_PUBLIC_WS_URL`、部署 FQDN | — | `https://chat.example.com`（`/api/v1`、`/ws`、`/updates/`） | 桌面 `LUMEN_API_BASE_URL`/`LUMEN_WS_URL` 亦须一致 |
| **公网 IP** | — | `LUMEN_PUBLIC_IP`（`SetNAT1To1IPs`） | — | `203.0.113.10` | = VPS 实际公网 IP |
| **WebRTC UDP 端口** | — | `LUMEN_WEBRTC_UDP_PORT` | — | `40000` | = Dockerfile EXPOSE = Coolify Ports Mappings = 安全组放行（四处一致） |
| **HTTP/WS 端口** | — | `LUMEN_LISTEN_ADDR`（**`0.0.0.0:8080`**） | — | `0.0.0.0:8080` | = Dockerfile EXPOSE = Coolify Ports Exposes |
| **owner 名单** | — | `LUMEN_OWNER_SUBJECTS`（逗号分隔 `sub`） | 对应用户的 `sub` | `sub-abc,sub-def` | = IdP 中 owner 用户的 `sub` |

> **官网域 vs API 子域**：官网（登录中介 + 营销/下载 + 账户中心）在 `example.com`（Cloudflare Pages）；Lumen API/WS/更新托管在 `chat.example.com`（Go 服务端 + Coolify Traefik）。二者是**不同域**，各自独立部署（[`00-overview.md §7.6`](../design/00-overview.md#76-官网部署cloudflare-pages--worker--kv--secrets)）。账户中心**不调** Lumen API，桌面用**原生 HTTP/WS**（非浏览器同源策略约束）连 API，故 **Go 服务端无需 CORS**（[`server-design.md §6.6`](../design/server-design.md#66-安全注意)）。

### 4.1 KV / Secrets 清单（Cloudflare）

| 类型 | 名称 | 用途 | 注入方式 |
|------|------|------|---------|
| KV 命名空间 | `HANDOFF` | `handoff_code → {access_token, expires_in, refresh_token, sub, bound_challenge}` + 登录上下文 `ctx:*`；TTL≈120s（ctx≈600s），一次性消费 | Pages 项目设置绑定 `env.HANDOFF`（生产/预览各独立命名空间） |
| KV 命名空间 | `SESSIONS` | `desktop_session_id → {refresh_token, sub, created_at}`；无 TTL，logout 删 | Pages 项目设置绑定 `env.SESSIONS` |
| **Secret** | `OIDC_CLIENT_SECRET` | 官网 confidential client 密钥 | `wrangler pages secret put OIDC_CLIENT_SECRET`（或控制台加密环境变量） |
| **Secret** | `SESSION_ENC_KEY` | 账户中心会话 cookie 加密/签名密钥 | `wrangler pages secret put SESSION_ENC_KEY` |
| env（非密） | `OIDC_ISSUER`/`OIDC_AUTHORIZE_URL`/`OIDC_TOKEN_URL`/`OIDC_USERINFO_URL`/`OIDC_CLIENT_ID`/`OIDC_AUDIENCE`/`OIDC_DESKTOP_REDIRECT_URI`/`OIDC_WEB_REDIRECT_URI`/`WEB_BASE_URL`/`UPDATES_LATEST_URL` | 见 §4 矩阵与 [`web-design.md §9.1`](../design/web-design.md#91-worker-环境变量与-secret-清单) | `wrangler.toml [vars]` 或 Pages 环境变量（**非** Secret） |

> Secret 绝不进 `wrangler.toml` / 仓库 / 前端产物 / 桌面；仅 Worker 运行时可读 `env`。KV 绑定与 `wrangler.toml` 示例见 [`web-design.md §9.2`](../design/web-design.md#92-kv-命名空间绑定)。

### 4.2 Coolify（Go 服务端）env 注入清单

在 Coolify 应用 → Environment Variables 注入全部 `LUMEN_*`（改后需重新部署）。完整表见 [`server-design.md §6.1`](../design/server-design.md#61-配置全部环境变量) 与本仓库 `server/.env.example`（由服务端分支提供；若尚未落地，占位清单见 [`docs/DEV.md`](../DEV.md#附录服务端-env-占位清单)）。关键对齐：`LUMEN_LISTEN_ADDR=0.0.0.0:8080`、`LUMEN_WEBRTC_UDP_PORT` 与 Ports Mappings 一致、`LUMEN_OAUTH_AUDIENCE=lumen-api`、`LUMEN_OAUTH_ISSUER` 与官网 `OIDC_ISSUER` 逐字一致。

---

## 5. 登记后自检清单

- [ ] IdP 已登记两个回调：`https://example.com/desktop/callback`、`https://example.com/auth/callback`（占位换真实域）。
- [ ] 官网 client 为 confidential，启用 `authorization_code` + `refresh_token` + PKCE(S256)。
- [ ] 桌面流程能拿到 `access_token`，其 `aud` **含 `lumen-api`**、`iss` == `LUMEN_OAUTH_ISSUER`、`alg` == `RS256`（用 §6 命令解码核对）。
- [ ] 桌面流程 scope 含 `offline_access`，能拿到 `refresh_token`（写入 KV `SESSIONS`，不下发桌面）。
- [ ] 账户中心流程 scope 为 `openid profile email`，**不带** `aud=lumen-api`。
- [ ] `OIDC_CLIENT_SECRET`、`SESSION_ENC_KEY` 仅作为 CF Secret 注入（不在仓库/前端产物）。
- [ ] §4 矩阵逐行核对：issuer / audience / 域名 / 端口 / owner 名单三方一致。
- [ ] Go 服务端 `LUMEN_OAUTH_ISSUER` / `LUMEN_OAUTH_AUDIENCE` 与 IdP 实际签发值一致（否则验签 `TOKEN_INVALID`）。

## 6. 快速核对 access_token 的 aud/iss/alg

拿到一个测试 `access_token`（JWT）后，可本地解码 header/payload（**不验签**，仅肉眼核对声明；勿在日志/共享环境粘贴真实 token）：

```bash
# 用法: bash scripts/decode-jwt.sh <JWT>
bash scripts/decode-jwt.sh "$ACCESS_TOKEN"
# 关注: header.alg == RS256, payload.iss == LUMEN_OAUTH_ISSUER, payload.aud 含 lumen-api, payload.exp 未过期
```

> `scripts/decode-jwt.sh` 见本仓库；它只做 base64url 解码与字段展示，不发起任何网络请求、不打印超出必要的内容。真实签名校验由 Go 服务端用 JWKS 完成。

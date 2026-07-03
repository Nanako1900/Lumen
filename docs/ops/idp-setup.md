# IdP 登记与配置对齐（OAuth2/OIDC）

> 关联 Issue: #26 ｜ 依据设计: [`web-design.md §3.3`](../design/web-design.md#33-idp-须登记的回调-url) / [`§7.2`](../design/web-design.md#72-idp-侧登记要求) / [`§9`](../design/web-design.md#9-配置环境变量kvsecrets)、[`00-overview.md §7`](../design/00-overview.md#7-部署与运维总览)、[`protocol-design.md §2`](../design/protocol-design.md#2-鉴权流程总览)
>
> **本项目实际域名与 IdP**（本文以下均以这些真实值为默认，不再用 `example.com` 等占位）：
> - **前端 / 官网 / 官方站**：`https://lumen.nanako.com.cn`（腾讯 EdgeOne 上的**纯静态 SPA**，只托管 `website/dist/`）
> - **后端 / Go 服务端**（资源服务器 + OIDC 登录中介）：`https://api.lumen.nanako.com.cn`
> - **WebSocket**：`wss://api.lumen.nanako.com.cn/ws`
> - **同 site 判定**：`lumen.*` 与 `api.lumen.*` 同属可注册域 `nanako.com.cn`（`.com.cn` 为公共后缀 public suffix）→ **同 site**，故 `SameSite=Lax` 的 host-only cookie + CORS 设计成立（见 §4 末注）。
> - **IdP**：**自建/定制 OIDC provider**（**非 Keycloak/Auth0/Logto**），部署于 `https://www.nanako.org`，**discovery 在根路径**（无 `/realms` 之类前缀）：
>   - issuer = `https://www.nanako.org`（**必须逐字一致，注意带 `www`，无路径**）
>   - discovery = `https://www.nanako.org/.well-known/openid-configuration`
>   - authorize = `https://www.nanako.org/oauth/authorize`
>   - token = `https://www.nanako.org/api/v1/oauth/token`
>   - userinfo = `https://www.nanako.org/api/v1/oauth/userinfo`
>   - jwks = `https://www.nanako.org/.well-known/jwks.json`
>   - 广告的 scope = `openid profile email phone`（**注意：不含 `offline_access`**——桌面刷新流程可能拿不到 `refresh_token`，见 §2.3 / §3.1 的**离线刷新警告**）
>   - audience = `lumen-api`（IdP client 须配置将 `lumen-api` 写入 `access_token` 的 `aud`）
>
> **占位说明**：本文仍保留为**真正占位符**的敏感值：`LUMEN_OAUTH_CLIENT_ID`、`LUMEN_OAUTH_CLIENT_SECRET`、`LUMEN_SESSION_ENC_KEY`、`LUMEN_REFRESH_ENC_KEY`、`LUMEN_DATABASE_URL` 中的 DB 密码/内网主机、`LUMEN_OWNER_SUBJECTS`（`sub-abc,sub-def` 等 subject 为占位）——**不要**在文档中填入真实密钥。
>
> **权威口径**：接口契约以 [`protocol-design.md`](../design/protocol-design.md) 为准；域名/端口/命名以设计文档为准。本文只描述「如何在 IdP 与各配置面登记/对齐」，不引入任何新契约。

Lumen 对接**外部** OAuth2/OIDC 身份服务器（IdP），**不自建资源服务器侧的账号体系**。三方必须对同一 issuer / audience / 回调 / scope 达成一致：

1. **IdP**（本项目为**自建定制 OIDC provider** `https://www.nanako.org`；若换用其他 IdP 见 §3 的 Keycloak / Auth0 / Logto 参考）——登记 client、回调、audience、scope。
2. **Go 服务端**（Coolify，`api.lumen.nanako.com.cn`）——**既是资源服务器**（用 IdP 的 JWKS 本地验 `access_token`，验 `iss`/`aud`/`exp`），**又是登录中介 / broker**（confidential OIDC client，持 `client_secret`，跑 Authorization Code + PKCE，`refresh_token` 加密存 Postgres）。登录中介与资源服务器同进程（commit `82f344e`：`internal/broker` + `internal/secure` + store broker 表 + `cmd/lumen-server/main.go`）。
3. **官网 SPA**（`lumen.nanako.com.cn`，腾讯 EdgeOne Pages **纯静态**，只托管 `website/dist/`）——**不持任何密钥、无 Pages Functions、无 KV**。账户中心网页在浏览器内**跨源**调 Go 服务端登录中介（`/auth/*`、`/api/me`，cookie 鉴权）。

> **架构变更（本文按此描述）**：登录中介从「官网 Cloudflare/EdgeOne Worker」**整体迁到 Go 服务端 `api.lumen.nanako.com.cn`**。`client_secret` 与 `refresh_token` 现在**只存在 Go 服务端**（Postgres，`refresh_token` 用独立密钥 `LUMEN_REFRESH_ENC_KEY` 静态加密），**不再有任何密钥落在 EdgeOne/Cloudflare**。IdP 侧的**回调域名落在 `api.lumen.nanako.com.cn`**（见 §2.1）。桌面 handoff 线路契约不变，仅宿主改变。

---

## 1. 角色与信任边界（先读）

| 角色 | 是否持 `client_secret` | 是否持 `refresh_token` | 与 IdP 的交互 |
|------|:---:|:---:|------|
| Go 服务端 · 登录中介(broker) | ✅（仅 Coolify 加密环境变量 `LUMEN_OAUTH_CLIENT_SECRET`） | ✅（仅存 Postgres，用 `LUMEN_REFRESH_ENC_KEY` 加密，绝不下发） | authorize / token / refresh / userinfo / (revoke) |
| Go 服务端 · 资源服务器 | ❌ | ❌ | 仅拉 JWKS（+ 可选 userinfo 兜底），本地验签 |
| 官网 SPA（EdgeOne 静态） | ❌ | ❌ | **无**（纯静态；账户中心跨源调 Go 服务端 `/auth/*`） |
| 桌面客户端 | ❌ | ❌（只持 `desktop_session_id`） | **不直连 IdP**（全部委托 Go 服务端登录中介） |

> 红线：`client_secret` 只在 Go 服务端（登录中介）；`refresh_token` 只在 Go 服务端 Postgres（加密），永不下发桌面/浏览器；`access_token` 绝不进任何 URL（见 [`web-design.md §8.1`](../design/web-design.md#81-安全红线强制)）。EdgeOne/Cloudflare 上**不再有任何密钥**。

---

## 2. 必须在 IdP 登记的项

Go 服务端登录中介注册为**一个 confidential client**（带 `client_secret`），供「桌面登录中介」与「账户中心网页登录」共用。

### 2.1 回调 URL（redirect_uri）

在 IdP（`https://www.nanako.org` 的定制 OIDC provider）该 client 下登记以下**两个**回调（均在 **Go 服务端域 `api.lumen.nanako.com.cn`**、HTTPS）：

| 回调 URL | 用途 |
|----------|------|
| `https://api.lumen.nanako.com.cn/desktop/callback` | 桌面 Web 中介登录（`GET /desktop/callback`） |
| `https://api.lumen.nanako.com.cn/auth/callback` | 账户中心网页登录（`GET /auth/callback`） |

> **回调域名**：登录中介托管在 Go 服务端 `api.lumen.nanako.com.cn`，故两个回调均落在该主机（路径 `/desktop/callback`、`/auth/callback`）。落地时务必在 IdP client 的 redirect 白名单中**逐字登记这两个 URL**，否则 IdP 将拒绝回调。
>
> **不要**在 IdP 登记桌面回环地址 `http://127.0.0.1:<port>/cb`——它只是 Go 服务端登录中介的 handoff 回环目标，由登录中介自行校验（仅允许 `http://127.0.0.1:<port>/...`，拒 `localhost` 主机名以防 DNS 重绑定）；IdP 永远只回到 `api.lumen.nanako.com.cn/desktop/callback`（[`web-design.md §3.3`](../design/web-design.md#33-idp-须登记的回调-url)）。
>
> 预览/测试环境请另建独立 client 或独立 redirect 白名单，避免污染生产（[`web-design.md §2.3`](../design/web-design.md#23-构建与发布要点)）。

### 2.2 授权类型与 PKCE

- 授权类型：`authorization_code` + `refresh_token`。
- **PKCE（S256）**：登录中介虽为 confidential client，仍启用 PKCE（授权码 + code_verifier 双保险）。
- Client 类型：**confidential**（带 `client_secret`）。

### 2.3 scope

本项目 IdP（`https://www.nanako.org`）discovery 中 `scopes_supported` 广告的是 **`openid profile email phone`**——**注意其中不含 `offline_access`**（见下方警告）。

| 流程 | scope | 说明 |
|------|-------|------|
| 桌面 Web 中介登录 | `openid profile email offline_access`（IdP 若不支持则回退 `openid profile email`，见下警告） | 期望以 `offline_access` 拿 `refresh_token`（加密存 Go 服务端 Postgres session 表）；**本项目 IdP 未广告该 scope**，是否发 `refresh_token` 需以 IdP 实际行为为准 |
| 账户中心网页登录 | `openid profile email` | **不需要** `offline_access`（无离线刷新需求），**不需要** `aud=lumen-api`（不调 Lumen API） |

> ⚠️ **离线刷新警告（本项目 IdP 关键差异）**：`https://www.nanako.org` 的 discovery **未在 `scopes_supported` 广告 `offline_access`**（仅 `openid profile email phone`）。因此**桌面刷新流程可能拿不到 `refresh_token`**。落地前请务必：
> 1. **确认该定制 IdP 是否仍会对 confidential client 签发 `refresh_token`**（很多 OIDC 实现即使不在 `scopes_supported` 列出 `offline_access`，仍会为 confidential client 默认下发 refresh_token；需实测 `/token` 响应中是否含 `refresh_token` 字段）；
> 2. **把桌面登录 scope 做成可配置**（如新增 env `LUMEN_OAUTH_DESKTOP_SCOPES`，缺省 `openid profile email`，在确认 IdP 支持后再追加 `offline_access` 或 `phone`），避免向不支持的 scope 请求导致 `/authorize` 报 `invalid_scope`；
> 3. 若 IdP **确实不发 `refresh_token`**，桌面端需在 `access_token` 过期后走**重新登录 / re-consent** 而非静默刷新——请据此调整桌面会话生命周期预期。

### 2.4 audience（关键：令 access_token 的 `aud` 含 `lumen-api`）

Go 服务端用 `LUMEN_OAUTH_AUDIENCE=lumen-api` 校验 `access_token.aud`。**只有桌面登录流程**需要令 `access_token` 的 `aud` 含 `lumen-api`；账户中心流程不需要。本项目定制 IdP（`https://www.nanako.org`）的做法见 §3.1；若换用 Keycloak/Auth0/Logto，见 §3.2–§3.4。**核心要求**：IdP 侧该 confidential client 必须被配置为把 `lumen-api` 写入桌面流程 `access_token` 的 `aud` claim。

---

## 3. 令 `aud` 含 `lumen-api` —— IdP 配置

> 目标一致：桌面登录拿到的 `access_token`（JWT）其 `aud` claim **包含 `lumen-api`**，且 `iss` 等于服务端 `LUMEN_OAUTH_ISSUER`，签名算法为 **RS256**（服务端强制 RS256 防 alg 混淆/none）。
>
> **本项目默认为 §3.1 的定制 OIDC provider（`https://www.nanako.org`）**。§3.2–§3.4 的 Keycloak / Auth0 / Logto 仅作**「若换用其他 IdP」**的参考，本项目落地时无需配置。

### 3.1 定制 OIDC provider（`https://www.nanako.org`）——本项目默认

本项目 IdP 为**自建/定制 OIDC provider**，discovery 在**根路径**发现，端点如下（均为本项目真实值）：

| 项 | 值 |
|----|----|
| issuer | `https://www.nanako.org`（**逐字一致，带 `www`，无路径**；即 `LUMEN_OAUTH_ISSUER`） |
| discovery | `https://www.nanako.org/.well-known/openid-configuration` |
| authorize | `https://www.nanako.org/oauth/authorize` |
| token | `https://www.nanako.org/api/v1/oauth/token` |
| userinfo | `https://www.nanako.org/api/v1/oauth/userinfo` |
| jwks | `https://www.nanako.org/.well-known/jwks.json`（即 `LUMEN_OAUTH_JWKS_URL`） |
| scopes 广告 | `openid profile email phone`（**无 `offline_access`**，见 §2.3 离线刷新警告） |

**配置步骤**：

1. 在该 IdP 建/用一个 **confidential client**（带 `client_secret`）作为登录中介 client，登记两个 redirect URI（§2.1）：
   - `https://api.lumen.nanako.com.cn/desktop/callback`
   - `https://api.lumen.nanako.com.cn/auth/callback`
2. **令 `aud` 含 `lumen-api`**：在该 IdP 的 client / API-resource 配置中，把 **`lumen-api` 加入桌面流程 `access_token` 的 `aud`**（该 IdP 具体以 client 配置项 / audience mapper / `resource` 参数中的哪一种实现，须按其管理界面或文档确定；无论何种方式，落地结果必须是桌面 `access_token.aud` **包含 `lumen-api`** 且与服务端 `LUMEN_OAUTH_AUDIENCE` **逐字一致**）。
3. **签名算法**：确认 IdP 以 **RS256** 签发 JWT access_token（服务端强制 RS256）；`iss` 恒等于 `https://www.nanako.org`。
4. **scope / 离线刷新**：桌面流程期望 `offline_access`，但该 IdP 未广告该 scope——按 §2.3 警告先确认 IdP 是否仍对 confidential client 发 `refresh_token`，并把桌面 scope 做成可配置。
5. **校验**：登录后用 §6 命令解码 access_token，确认 `header.alg == RS256`、`payload.iss == https://www.nanako.org`、`payload.aud` 含 `lumen-api`、`payload.exp` 未过期。

> **discovery 在根**：服务端只需配 `LUMEN_OAUTH_ISSUER=https://www.nanako.org`，登录中介即可用 `https://www.nanako.org/.well-known/openid-configuration` 自动发现 authorize/token/userinfo/jwks（无 `/realms/<realm>` 这类子路径）。若 discovery 与上表端点不符，以 discovery 文档实际返回为准，并显式配置对应 `LUMEN_OAUTH_*_URL`。

---

### 3.2 Keycloak（若换用其他 IdP · 参考）

Keycloak 默认把 client_id 放入 `aud`，需**显式**加一个 audience。两种常见做法（任选其一）：

**做法 A — 定义一个 client scope，挂 Audience mapper（推荐）**

1. 若尚无「API 资源」client，可新建一个 client `lumen-api`（access type: bearer-only 或 confidential 均可，仅作为 audience 标识存在）。
2. Client scopes → 新建 `lumen-api-audience`（或复用已有 scope）→ Add mapper → **Audience**：
   - `Included Client Audience` = `lumen-api`（或 `Included Custom Audience` = `lumen-api`）
   - `Add to access token` = ON
3. 将该 client scope 作为 **Default** 或 **Optional** 挂到登录中介 client（`lumen-website`）：
   - Default：每次登录 `aud` 恒含 `lumen-api`。
   - Optional：登录中介在 `/authorize` 时带 `scope=... lumen-api-audience` 才注入（登录中介可在桌面流程附加该 scope，账户中心流程不带）。
4. 校验：登录后解码 access_token，确认 `"aud"` 含 `"lumen-api"`、`"iss"` 为 realm issuer、`alg` 为 `RS256`。

> Keycloak 无 `audience`/`resource` 请求参数；audience 由 mapper/scope 决定，登录中介请求端**不需**传 audience 参数（`LUMEN_OAUTH_AUDIENCE` 在登录中介仅作对齐记录/校验用，见 §4）。

**做法 B — 直接在登录中介 client 上加 Audience mapper**：登录中介 client → Client scopes → `<client>-dedicated` → Add mapper → Audience（同上），最简单，但该 client 所有流程（含账户中心）都会带 `aud=lumen-api`——可接受（账户中心不调 API，多一个 aud 无害），若要精确区分请用做法 A 的 Optional scope。

- issuer 形如：`https://auth.example.com/realms/lumen`
- JWKS 形如：`https://auth.example.com/realms/lumen/protocol/openid-connect/certs`
- authorize/token/userinfo：`.../protocol/openid-connect/{auth,token,userinfo}`

### 3.3 Auth0（若换用其他 IdP · 参考）

Auth0 用 **API（Resource Server）** + `audience` 请求参数。

1. 建一个 **API**：Dashboard → Applications → APIs → Create API
   - **Identifier（= audience）** = `lumen-api`（此值即 `access_token.aud`；Auth0 惯例用 URL，但用 `lumen-api` 亦可——必须与服务端 `LUMEN_OAUTH_AUDIENCE` **逐字一致**）。
   - Signing Algorithm: **RS256**。
   - 可开启 `Allow Offline Access`（发 `refresh_token`）。
2. 建/用一个 **Regular Web Application**（confidential）作为登录中介 client；在其 Settings 里登记两个 callback URL（§2.1，均在 `api.lumen.nanako.com.cn`）。
3. Go 服务端登录中介在构造 `/authorize` 时**必须带** `audience=lumen-api`（Auth0 只有带 audience 才签发含该 `aud` 的 JWT access_token；否则可能返回不透明 token）。桌面流程带 `audience`；账户中心流程不带。
4. `refresh_token` 需在 `/authorize` 带 `scope=offline_access`（且 API 允许 Offline Access）。

- issuer 形如：`https://<tenant>.us.auth0.com/`（注意 Auth0 issuer **带结尾斜杠**，服务端 `LUMEN_OAUTH_ISSUER` 必须与之逐字一致）。
- JWKS：`https://<tenant>.us.auth0.com/.well-known/jwks.json`
- 端点由 `https://<tenant>.us.auth0.com/.well-known/openid-configuration` 发现。

### 3.4 Logto（若换用其他 IdP · 参考）

Logto 用 **API resource** + `resource` 参数（RFC 8707 Resource Indicators）。

1. Console → API resources → Create：
   - **API Identifier（= resource indicator）** = `lumen-api`（该值即进入 `access_token.aud`；须与服务端 `LUMEN_OAUTH_AUDIENCE` 逐字一致）。
2. Console → Applications → 建 **Traditional Web**（confidential）作为登录中介 client；登记两个 Redirect URI（§2.1，均在 `api.lumen.nanako.com.cn`）。
3. Go 服务端登录中介在 `/authorize` 带 `resource=lumen-api`（Logto 据 `resource` 决定 `aud`）；并带 `scope=openid profile email offline_access`（`prompt=consent` 视配置而定以确保发 refresh_token）。桌面流程带 `resource`；账户中心流程不带。

- issuer 形如：`https://<your>.logto.app/oidc`
- JWKS：`https://<your>.logto.app/oidc/jwks`
- 端点由 `https://<your>.logto.app/oidc/.well-known/openid-configuration` 发现。

> **Go 服务端登录中介侧如何附加 audience/resource**：`GET /desktop/login` 构造 IdP `/authorize` URL 时按 IdP 行为注入 audience——**本项目定制 IdP（`https://www.nanako.org`）**须按其文档确定用哪种方式令 `aud` 含 `lumen-api`（client 侧配置 / audience mapper / `resource` 或 `audience` 请求参数其一）；作为参考，Auth0 用 `audience=<LUMEN_OAUTH_AUDIENCE>`，Logto 用 `resource=<LUMEN_OAUTH_AUDIENCE>`，Keycloak 不传参数（靠 mapper/scope）。该逻辑在 Go 服务端 `internal/broker`（构造 authorize URL，见 commit `82f344e`；对应 [`web-design.md §5.5`](../design/web-design.md#55-worker-伪代码骨架typescript) 的登录中介骨架）。

---

## 4. 配置对齐矩阵（三方一致）

以下每一行的值，必须在**Go 服务端 env（登录中介 broker + 资源服务器，同进程） / IdP** 两处对齐（IdP 侧多为「登记/推导」而非「填值」）。登录中介所有 OAuth 相关配置现统一为 `LUMEN_OAUTH_*`，注入 Coolify。下表**示例列已填为本项目真实值**（`www.nanako.org` / `lumen.nanako.com.cn` / `api.lumen.nanako.com.cn`）；仅密钥/DB 密码/owner 名单仍为占位（`***`）。

| 对齐项 | Go 服务端 env（Coolify；broker + 资源服务器） | IdP 侧 | 真实值（本项目） | 必须逐字一致？ |
|--------|-----------------------------------------------|--------|-----------|:---:|
| **issuer** | `LUMEN_OAUTH_ISSUER`（broker + 资源服务器共用） | 定制 IdP 的 issuer（`.well-known` 的 `issuer`） | `https://www.nanako.org`（**带 `www`，无路径，无结尾斜杠**） | ✅（逐字一致，含 `www`） |
| **JWKS URL** | `LUMEN_OAUTH_JWKS_URL`（资源服务器验签；缺省由 discovery 推导） | JWKS 端点 | `https://www.nanako.org/.well-known/jwks.json` | 指向同一 issuer 的 JWKS |
| **audience** | `LUMEN_OAUTH_AUDIENCE`（broker 注入 + 资源服务器校验） | client 须将其写入桌面 access_token 的 `aud` | `lumen-api` | ✅ |
| **userinfo（兜底）** | `LUMEN_OAUTH_USERINFO_URL`（可选，缺省由 discovery 推导） | userinfo 端点 | `https://www.nanako.org/api/v1/oauth/userinfo` | 指向同一 issuer |
| **authorize/token** | `LUMEN_OAUTH_AUTHORIZE_URL` / `LUMEN_OAUTH_TOKEN_URL`（broker 用；可选，discovery 填） | 授权/令牌端点 | `https://www.nanako.org/oauth/authorize`、`https://www.nanako.org/api/v1/oauth/token` | 指向同一 issuer |
| **client_id** | `LUMEN_OAUTH_CLIENT_ID`（broker 用） | 登录中介 client 的 ID | `***`（占位，IdP 分配后填） | 登录中介 ↔ IdP 一致 |
| **client_secret** | `LUMEN_OAUTH_CLIENT_SECRET`（**加密环境变量**，仅 broker 持有） | 登录中介 client 的 secret | `***`（仅 Go 服务端） | 登录中介 ↔ IdP 一致 |
| **desktop redirect** | `LUMEN_OAUTH_DESKTOP_REDIRECT_URI` | 登记的桌面回调 | `https://api.lumen.nanako.com.cn/desktop/callback` | ✅（IdP 登记 == broker 传值） |
| **web redirect** | `LUMEN_OAUTH_WEB_REDIRECT_URI` | 登记的账户中心回调 | `https://api.lumen.nanako.com.cn/auth/callback` | ✅ |
| **scope（桌面）** | broker 代码内 / `LUMEN_OAUTH_DESKTOP_SCOPES`（建议可配置） | 允许该 scope | 期望 `openid profile email offline_access`；**IdP 仅广告 `openid profile email phone`（无 offline_access）** | offline_access 决定发 refresh_token（见 §2.3 警告） |
| **session 加密密钥** | `LUMEN_SESSION_ENC_KEY`（账户中心会话 cookie 加密/签名） | — | `***`（仅 Go 服务端） | 账户中心 cookie 用 |
| **refresh 加密密钥** | `LUMEN_REFRESH_ENC_KEY`（`refresh_token` 静态加密，独立于 session 密钥） | — | `***`（仅 Go 服务端） | Postgres 内 refresh_token 加密用 |
| **数据库** | `LUMEN_DATABASE_URL`（保留 `?sslmode=disable`，内网 Coolify PG） | — | `postgres://lumen:***@<内网主机>:5432/lumen?sslmode=disable`（密码/主机为占位） | 指向 Coolify 内网 Postgres |
| **Web 站点域（CORS/cookie）** | `LUMEN_WEB_BASE_URL`（broker 仅对此源发 CORS，凭据模式） | redirect 域**不**在此（在 API 子域） | `https://lumen.nanako.com.cn`（官网静态 SPA） | SPA 跨源调 broker 时的唯一放行源 |
| **API 子域 / 登录中介域** | 部署 FQDN、`UPDATES_LATEST_URL`（`api.lumen.*`）；`LUMEN_PUBLIC_WS_URL` | redirect 域 = 登录中介域 = API 子域 | `https://api.lumen.nanako.com.cn`（`/api/v1`、`/ws`、`/updates/`、`/desktop/*`、`/auth/*`、`/api/desktop/*`、`/api/me`） | 桌面 `LUMEN_API_BASE_URL`/`LUMEN_WS_URL` 均指向此 |
| **WebSocket** | `LUMEN_PUBLIC_WS_URL` | — | `wss://api.lumen.nanako.com.cn/ws` | = API 子域的 `/ws` |
| **公网 IP** | `LUMEN_PUBLIC_IP`（`SetNAT1To1IPs`） | — | `203.0.113.10`（占位，= VPS 实际公网 IP） | = VPS 实际公网 IP |
| **WebRTC UDP 端口** | `LUMEN_WEBRTC_UDP_PORT` | — | `40000` | = Dockerfile EXPOSE = Coolify Ports Mappings = 安全组放行（四处一致） |
| **HTTP/WS 端口** | `LUMEN_LISTEN_ADDR`（**`0.0.0.0:8090`**） | — | `0.0.0.0:8090` | = Dockerfile EXPOSE = Coolify Ports Exposes |
| **owner 名单** | `LUMEN_OWNER_SUBJECTS`（逗号分隔 `sub`） | 对应用户的 `sub` | `sub-abc,sub-def`（占位，= IdP 中 owner 用户的 `sub`） | = IdP 中 owner 用户的 `sub` |

> **官网静态域 vs API/登录中介子域**：官网 `lumen.nanako.com.cn` 为**纯静态 SPA**（腾讯 EdgeOne Pages，只托管 `website/dist/`，无 Functions/KV/密钥）；Lumen API/WS/更新**以及登录中介（broker）**全部托管在 `api.lumen.nanako.com.cn`（Go 服务端 + Coolify Traefik）。回调域随之落在 `api.lumen.nanako.com.cn`（见 §2.1）。
>
> **跨源与 cookie（账户中心）**：账户中心 SPA 在 `lumen.nanako.com.cn`，浏览器**跨源**调 `api.lumen.nanako.com.cn` 上的 broker（`/auth/*`、`/api/me`）。因 `lumen.nanako.com.cn` 与 `api.lumen.nanako.com.cn` **同属可注册域 `nanako.com.cn`**（`.com.cn` 为公共后缀 public suffix，故 `nanako.com.cn` 是 registrable domain）→ 二者为**同 site**：会话 cookie 用 `SameSite=Lax + HttpOnly + Secure + Path=/ + HOST-ONLY（不设 Domain）`；Go 服务端**仅对 `LUMEN_WEB_BASE_URL`（=`https://lumen.nanako.com.cn`）这一精确源发 CORS 且允许凭据**；SPA 的 XHR 用 `credentials:'include'`，`/auth/login`、`/auth/callback` 为顶层导航。**桌面**仍用**原生 HTTP/WS**（非浏览器同源约束）连 broker 与 API。（[`server-design.md §6.6`](../design/server-design.md#66-安全注意)）

### 4.1 登录中介存储与密钥清单（Go 服务端）

登录中介的状态**从 Cloudflare/EdgeOne KV 迁到 Go 服务端 Postgres**（commit `82f344e` 的 store broker 表）；密钥**从 CF Secret 迁到 Coolify 加密环境变量**。EdgeOne 上**不再有任何 KV 或 Secret**。

| 类型 | 名称 | 用途 | 注入方式 |
|------|------|------|---------|
| Postgres 表 | handoff 表 | `handoff_code → {access_token, expires_in, refresh_token, sub, bound_challenge}` + 登录上下文；TTL≈120s，一次性消费 | Go 服务端 store（迁移随服务端启动执行） |
| Postgres 表 | session 表 | `desktop_session_id → {refresh_token(用 `LUMEN_REFRESH_ENC_KEY` 加密), sub, created_at}`；logout 删 | Go 服务端 store |
| **加密环境变量** | `LUMEN_OAUTH_CLIENT_SECRET` | 登录中介 confidential client 密钥 | Coolify → Environment Variables（标记为 Secret） |
| **加密环境变量** | `LUMEN_SESSION_ENC_KEY` | 账户中心会话 cookie 加密/签名密钥 | Coolify → Environment Variables（标记为 Secret） |
| **加密环境变量** | `LUMEN_REFRESH_ENC_KEY` | Postgres 内 `refresh_token` 静态加密密钥（独立于 session 密钥） | Coolify → Environment Variables（标记为 Secret） |
| env（非密） | `LUMEN_OAUTH_ISSUER`/`LUMEN_OAUTH_AUTHORIZE_URL`/`LUMEN_OAUTH_TOKEN_URL`/`LUMEN_OAUTH_USERINFO_URL`/`LUMEN_OAUTH_CLIENT_ID`/`LUMEN_OAUTH_AUDIENCE`/`LUMEN_OAUTH_DESKTOP_REDIRECT_URI`/`LUMEN_OAUTH_WEB_REDIRECT_URI`/`LUMEN_WEB_BASE_URL` | 见 §4 矩阵与 [`server-design.md §6.1`](../design/server-design.md#61-配置全部环境变量) | Coolify → Environment Variables（非 Secret） |

> 密钥绝不进仓库 / 前端产物 / 桌面 / EdgeOne；仅 Go 服务端运行时可读。`LUMEN_OAUTH_AUTHORIZE_URL`/`TOKEN_URL`/`USERINFO_URL` 可省略——登录中介会用 `LUMEN_OAUTH_ISSUER` 的 `.well-known/openid-configuration` discovery 自动填充。

### 4.2 Coolify（Go 服务端）env 注入清单

在 Coolify 应用 → Environment Variables 注入全部 `LUMEN_*`（含**资源服务器** + **登录中介 broker** 两组，同进程；改后需重新部署）。完整表见 [`server-design.md §6.1`](../design/server-design.md#61-配置全部环境变量) 与本仓库 `server/.env.example`（由服务端分支提供；若尚未落地，占位清单见 [`docs/DEV.md`](../DEV.md#附录服务端-env-占位清单)）。关键对齐：`LUMEN_LISTEN_ADDR=0.0.0.0:8090`、`LUMEN_WEBRTC_UDP_PORT` 与 Ports Mappings 一致、`LUMEN_OAUTH_AUDIENCE=lumen-api`、`LUMEN_OAUTH_ISSUER=https://www.nanako.org`（与 IdP 实际 issuer 逐字一致，带 `www`）、`LUMEN_OAUTH_JWKS_URL=https://www.nanako.org/.well-known/jwks.json`、`LUMEN_PUBLIC_WS_URL=wss://api.lumen.nanako.com.cn/ws`；broker 专属：`LUMEN_OAUTH_CLIENT_ID`/`LUMEN_OAUTH_CLIENT_SECRET`（占位密钥）/`LUMEN_OAUTH_DESKTOP_REDIRECT_URI`(=`https://api.lumen.nanako.com.cn/desktop/callback`)/`LUMEN_OAUTH_WEB_REDIRECT_URI`(=`https://api.lumen.nanako.com.cn/auth/callback`)/`LUMEN_WEB_BASE_URL`(=`https://lumen.nanako.com.cn`)/`LUMEN_SESSION_ENC_KEY`/`LUMEN_REFRESH_ENC_KEY`（占位密钥）。**数据库**：`LUMEN_DATABASE_URL` 指向 Coolify 内网 Postgres，**保留 `?sslmode=disable`**（内网连接无需 TLS），其中 DB 密码与内网主机为占位，勿写入真实值。

---

## 5. 登记后自检清单

- [ ] IdP（`https://www.nanako.org`）已登记两个回调：`https://api.lumen.nanako.com.cn/desktop/callback`、`https://api.lumen.nanako.com.cn/auth/callback`（**注意主机名为 `api.lumen.nanako.com.cn`，非官网 `lumen.nanako.com.cn`**）。
- [ ] 登录中介 client 为 confidential，启用 `authorization_code` + PKCE(S256)（`refresh_token` 视 IdP 是否发放而定，见下条）。
- [ ] 桌面流程能拿到 `access_token`，其 `aud` **含 `lumen-api`**、`iss` == `LUMEN_OAUTH_ISSUER`（=`https://www.nanako.org`，带 `www`）、`alg` == `RS256`（用 §6 命令解码核对）。
- [ ] **离线刷新确认（本项目 IdP 差异）**：IdP discovery 广告 `openid profile email phone`（**无 `offline_access`**）。已实测 `/token` 响应**是否含 `refresh_token`**——若不含，桌面端改走重新登录而非静默刷新；桌面 scope 已做成可配置（`LUMEN_OAUTH_DESKTOP_SCOPES`），不向不支持的 scope 请求以免 `invalid_scope`（见 §2.3）。
- [ ] 账户中心流程 scope 为 `openid profile email`，**不带** `aud=lumen-api`。
- [ ] `LUMEN_OAUTH_CLIENT_SECRET`、`LUMEN_SESSION_ENC_KEY`、`LUMEN_REFRESH_ENC_KEY`、`LUMEN_DATABASE_URL` 中的 DB 密码 仅作为 Coolify 加密环境变量注入（不在仓库/前端产物/EdgeOne）。
- [ ] §4 矩阵逐行核对：issuer / audience / 域名 / 端口 / owner 名单在 Go 服务端 env 与 IdP 两处一致。
- [ ] Go 服务端 `LUMEN_OAUTH_ISSUER`(=`https://www.nanako.org`) / `LUMEN_OAUTH_AUDIENCE`(=`lumen-api`) 与 IdP 实际签发值一致（否则验签 `TOKEN_INVALID`）。
- [ ] 账户中心跨源可用：Go 服务端仅对 `LUMEN_WEB_BASE_URL`（=`https://lumen.nanako.com.cn`）发 CORS 且允许凭据；会话 cookie 为 `SameSite=Lax + HttpOnly + Secure + Path=/ + HOST-ONLY`（不设 Domain）。
- [ ] `LUMEN_DATABASE_URL` 指向内网 Coolify Postgres 且保留 `?sslmode=disable`。
- [ ] 官网 `lumen.nanako.com.cn` 为纯静态（EdgeOne 只托管 `website/dist/`），确认其上**无** Functions/KV/Secret。

## 6. 快速核对 access_token 的 aud/iss/alg

拿到一个测试 `access_token`（JWT）后，可本地解码 header/payload（**不验签**，仅肉眼核对声明；勿在日志/共享环境粘贴真实 token）：

```bash
# 用法: bash scripts/decode-jwt.sh <JWT>
bash scripts/decode-jwt.sh "$ACCESS_TOKEN"
# 关注: header.alg == RS256, payload.iss == https://www.nanako.org (LUMEN_OAUTH_ISSUER，带 www), payload.aud 含 lumen-api, payload.exp 未过期
```

> `scripts/decode-jwt.sh` 见本仓库；它只做 base64url 解码与字段展示，不发起任何网络请求、不打印超出必要的内容。真实签名校验由 Go 服务端用 JWKS 完成。

# Lumen 共享协议与数据模型规范（接口契约）

> 文档版本: 1.0
> 状态: 权威规范（authoritative）
> 适用范围: 服务端（单 Go 二进制）与客户端（Wails v2 + Svelte，仅 Windows）必须共同遵守
> 依据调研: `docs/research/05-oauth-pkce-jwks.md`（鉴权）、`docs/research/01-pion-sfu.md`（信令/媒体）

本文件是 Lumen 服务端与客户端之间**唯一**的接口契约。任何一方实现都必须严格以本文为准；如需变更，先改本文，再改实现。

**版本归属约定**（贯穿全文）：

| 标记 | 含义 |
|------|------|
| `[v0]` | 最小可用闭环：登录、文字历史、单语音频道收发、基本状态广播 |
| `[v1]` | 完整功能：频道 CRUD、踢人、资料双向同步、逐人音量、PTT/VAD、降噪、多频道 |
| `[v2]` | 推迟项：E2E 加密（Insertable Streams + SFrame）。本文仅附录概述 |

未标注的内容默认为 `[v0]` 即需要。`[v2]` 内容仅见 [附录 A](#附录-a-v2-e2e-加密概述)。

---

## 目录

1. [总体架构与约定](#1-总体架构与约定)
2. [鉴权流程总览](#2-鉴权流程总览)
3. [REST API 完整清单](#3-rest-api-完整清单)
4. [WebSocket 信令协议](#4-websocket-信令协议)
5. [数据模型（PostgreSQL DDL）](#5-数据模型postgresql-ddl)
6. [核心数据流时序](#6-核心数据流时序)
7. [版本与错误约定、命名规范、时间格式](#7-版本与错误约定命名规范时间格式)
8. [附录 A：v2 E2E 加密概述](#附录-a-v2-e2e-加密概述)

---

## 1. 总体架构与约定

### 1.1 通信分层

Lumen 采用 **REST + WebSocket + WebRTC** 三层混合：

```
┌────────────────────────────────────────────────────────────────┐
│                     Windows 客户端 (Wails v2)                     │
│   Svelte 前端 (webview)        +        Go 外壳 (原生集成)        │
│   - REST 调用                            - 全局 PTT 热键           │
│   - WS 信令                              - 系统托盘 / 单实例       │
│   - WebRTC (浏览器内置)                  - 自动更新                │
└──┬──────────┬───────────────┬────────────────────────┬──────────┘
   │ HTTPS    │ HTTPS (REST)  │ WSS (信令)             │ DTLS-SRTP (UDP 媒体)
   │ (登录中介)│               │                        │
   ▼          ▼               ▼                        ▼
┌────────────────────┐  ┌──────────────────────────┐  ┌──────────────────┐
│ 官网 (Cloudflare)   │  │ Coolify Traefik 反代      │  │ 裸 UDP 端口映射   │
│ example.com         │  │ (终结 TLS)                │  │ (Traefik 不转发) │
│ - 登录中介 (PKCE→IdP)│  │ - HTTP/TCP → 明文 HTTP/WS │  └────────┬─────────┘
│ - confidential OIDC │  └────────────┬─────────────┘           │
│   client_secret 在  │               ▼                          │
│   Worker 加密环境变量│  ┌──────────────────────────────────────┐│
│ - KV: HANDOFF/SESSIONS│ │  服务端单 Go 二进制 (容器内明文监听)    ││
│ - 持有 refresh_token │  │  REST handlers │ WS hub │ Pion SFU    ││
└─────────┬───────────┘  │  (UDPMux 单端口) │ PostgreSQL          ││
          │ Auth Code+PKCE└──────────────────┬───────────────────┘│
          ▼ (aud=lumen-api)                  ▼                     ▼
┌──────────────────────────────────────────────────────────────────┐
│   外部 IdP (OAuth2 / OIDC)  —— 签发 access_token (JWT)              │
│   服务端仅用其 JWKS 本地验 access_token (验 iss/aud/exp)，不直连官网 │
└──────────────────────────────────────────────────────────────────┘
```

**登录中介边界（Web 中介登录）**：桌面客户端**不再**自行对 IdP 跑 PKCE，而是**经官网（Cloudflare）登录中介**取 token——官网是 confidential OIDC client，用 Authorization Code + PKCE 对 IdP 登录（`client_secret` 仅在 Cloudflare Worker 加密环境变量，绝不下发桌面），并令 `access_token` 的 `aud` 含 `lumen-api`。**Go 服务端完全不变**：仍只用 IdP 的 JWKS 本地验 `access_token`（JWT，验 `iss`/`aud`/`exp`），不感知官网中介的存在，故无需新增 CORS。详细登录契约见 [web-design.md §5](./web-design.md#5-web-中介登录桌面)。

| 层 | 职责 | 协议 | 是否实时 |
|----|------|------|----------|
| REST | 非实时数据：引导数据、频道列表、消息历史分页、成员列表、owner 频道 CRUD | HTTPS（经 Traefik 终结 TLS，容器内明文 HTTP） | 否 |
| WebSocket | 实时：鉴权握手、WebRTC 信令、实时文字广播、状态广播（加入/离开/说话/静音/资料更新） | WSS（经 Traefik，容器内明文 WS） | 是 |
| WebRTC（媒体） | 语音 RTP（Opus）选择性转发，Pion SFU | DTLS-SRTP over 单 UDP 端口 | 是 |

**关键边界约束**：
- 媒体 UDP 流量 **不经过 Traefik**，必须在 Coolify 单独映射裸 UDP 端口（详见 NAT/连通性约定，[§4.6](#46-webrtc-信令与重协商)）。
- 服务端在容器内只监听**明文** HTTP/WS；TLS 由 Traefik 终结。因此服务端代码层面不处理证书。

### 1.2 单服务器模型（single guild）

- 全系统只有**一个**逻辑服务器（guild），无多租户、无 guild 切换概念。
- 频道（channel）直接挂在该单一 guild 下，无需 `guild_id` 字段。
- 规模假设：每语音频道 2~6 人；总成员数量级为十人。设计据此选择内存态房间与托管 PostgreSQL。

### 1.3 全局配置项（环境变量，Coolify 注入）

所有运行期配置走环境变量。服务端启动时**校验必填项存在**，缺失则 fail-fast 退出。

| 环境变量 | 含义 | 示例 | 归属 |
|----------|------|------|------|
| `LUMEN_OAUTH_ISSUER` | OIDC issuer URL（用于校验 `iss` 及 OIDC 发现） | `https://auth.example.com/realms/lumen` | v0 |
| `LUMEN_OAUTH_JWKS_URL` | JWKS 端点（本地验签公钥来源） | `https://auth.example.com/.../certs` | v0 |
| `LUMEN_OAUTH_USERINFO_URL` | userinfo 端点（资料同步；**可选**，缺省由 OIDC discovery 自 issuer 推导，仅在需覆盖时配置） | `https://auth.example.com/.../userinfo` | v0 |
| `LUMEN_OAUTH_AUDIENCE` | 期望的 `aud` 值（验 access_token；官网 client_id 在 Worker，服务端不需要 client_id） | `lumen-api` | v0 |
| `LUMEN_OWNER_SUBJECTS` | owner 的 OAuth subject 列表，逗号分隔 | `sub-abc,sub-def` | v0 |
| `LUMEN_LISTEN_ADDR` | HTTP/WS 监听地址（容器内**必须绑 `0.0.0.0`**，否则 Traefik 无法到达容器） | `0.0.0.0:8080` | v0 |
| `LUMEN_DATABASE_URL` | PostgreSQL 连接串（DSN） | `postgres://lumen:***@lumen-db:5432/lumen?sslmode=disable` | v0 |
| `LUMEN_PUBLIC_IP` | VPS 公网 IP（`SetNAT1To1IPs` 宣告） | `203.0.113.10` | v0 |
| `LUMEN_WEBRTC_UDP_PORT` | WebRTC 媒体单 UDP 端口（需在 Coolify 裸映射；四处对齐） | `40000` | v0 |

> 本表为**服务端** env（不变）。Web 中介登录改造后，IdP 的 issuer/client_id/scope/`client_secret` 全部移到**官网 Cloudflare Worker**（见 [web-design.md §9](./web-design.md#9-配置环境变量kvsecrets)）；服务端仍只需 `LUMEN_OAUTH_ISSUER`/`LUMEN_OAUTH_JWKS_URL`/`LUMEN_OAUTH_AUDIENCE`（`=lumen-api`）等用于 JWKS 本地验签，不感知官网中介。**桌面客户端配置**不再含 issuer/client_id/scope，改为内置 `LUMEN_WEB_BASE_URL`（默认 `https://example.com`）、`LUMEN_API_BASE_URL`、`LUMEN_WS_URL`（见 [client-design §2.1](./client-design.md#21-配置)）。客户端经官网取得的 `access_token` 与服务端必须指向**同一** issuer 与 audience。

---

## 2. 鉴权流程总览

### 2.1 身份模型

- 对接外部 **OAuth2 / OIDC** 服务器（本设计不实现该服务器）。
- `access_token` 为 **JWT**；服务端用 **JWKS 本地验签**（校验签名 + `iss` + `aud` + `exp`）。不使用 introspection。
- **准入策略**：凡能在 OAuth2 服务器登录者皆可进入本应用。无白名单。
- **权限两级**：`owner` 与普通成员。`owner` 由配置 `LUMEN_OWNER_SUBJECTS` 指定的一组 `sub` 决定，**不存于数据库**（详见 [§5.3](#53-owner-判定说明)）。

### 2.2 客户端取 token（Web 中介登录，回环 handoff）

> 桌面客户端**不再**自行对 IdP 跑 PKCE，而是**委托官网（Cloudflare）登录中介**取 token。服务端不参与登录中介；本段仅列契约要点以便桌面/官网对齐。**完整登录契约（官网 Worker 端点、KV、安全红线）见 [web-design.md §5](./web-design.md#5-web-中介登录桌面)**。

**模型变更要点**：
- 官网是 **confidential OIDC client**，用 Authorization Code + PKCE 对 IdP 登录；请求 scope `openid profile email offline_access`，并令 `access_token` 的 `aud` 含 `lumen-api`（= 服务端 `LUMEN_OAUTH_AUDIENCE`）。`client_secret` 仅在 Cloudflare Worker 加密环境变量，**绝不下发桌面**。
- **`refresh_token` 永不落到桌面**：存于官网 KV（SESSIONS）；桌面只持有不透明高熵 `desktop_session_id`（存 Windows 凭据库 DPAPI），`access_token` 仅在内存。
- 桌面不再内置 IdP issuer/client_id/scope，仅内置 `LUMEN_WEB_BASE_URL`（默认 `https://example.com`）等（见 [§1.3](#13-全局配置项环境变量coolify-注入) 备注与 client-design §2.1）。

**官网中介端点**（均在 `example.com`，详见 [web-design.md §5](./web-design.md#5-web-中介登录桌面)）：
- `GET /desktop/login?redirect_uri=<loopback>&state=<s>&challenge=<S256(handoff_verifier)>` —— 校验 `redirect_uri` 为 `http://127.0.0.1:<port>/...` 回环，暂存上下文后 302 到 IdP 授权端点（Auth Code+PKCE，带 `aud=lumen-api`）。
- `GET /desktop/callback?code=&state=` —— Worker 用 `client_secret` 向 IdP 换 token（access+refresh+id），生成一次性 `handoff_code` 写 KV（绑 `bound_challenge`，TTL≈120s），302 回 `redirect_uri?handoff_code=&state=`。**`access_token` 绝不进 URL**。
- `POST /api/desktop/exchange {handoff_code, handoff_verifier}` —— 校验 `S256(handoff_verifier)==bound_challenge`，一次性消费 `handoff_code`，生成 `desktop_session_id` 并写 KV SESSIONS，返回 `{access_token, expires_in, desktop_session_id, profile:{display_name, avatar_url}}`。

**桌面侧时序要点（回环 handoff）**：
1. 桌面起 `127.0.0.1:<rand>` 回环监听，生成 `handoff_verifier` + `state`，系统浏览器打开 `example.com/desktop/login?redirect_uri=http://127.0.0.1:<port>/cb&state&challenge=S256(handoff_verifier)`。
2. 官网完成 OIDC 后 302 回 `http://127.0.0.1:<port>/cb?handoff_code&state`。
3. 桌面校验 `state` → `POST example.com/api/desktop/exchange {handoff_code, handoff_verifier}` → 得 `{access_token, expires_in, desktop_session_id, profile}`。
4. 桌面把 `desktop_session_id` 存入 Windows 凭据库（DPAPI），`access_token` 仅内存。
5. `access_token` 用于：REST 的 `Authorization: Bearer`，以及 WS 首帧 `auth`（与原契约一致，服务端无感）。

### 2.3 服务端验签（JWKS 本地验签）

服务端启动时构造一个全局 `Verifier`（参考 `keyfunc/v3` + `golang-jwt/jwt/v5`）：

- `keyfunc.NewDefaultCtx(ctx, []string{LUMEN_OAUTH_JWKS_URL})`：后台自动拉取/缓存/轮换公钥，遇未知 `kid` 主动刷新。
- 解析用 `jwt.ParseWithClaims(...)` 并显式启用：
  - `jwt.WithValidMethods([]string{"RS256"})`（防 alg 混淆 / `none` 攻击）
  - `jwt.WithIssuer(LUMEN_OAUTH_ISSUER)`
  - `jwt.WithAudience(LUMEN_OAUTH_AUDIENCE)`
  - `jwt.WithExpirationRequired()`（`exp`/`nbf`/`iat` 默认校验）
  - `jwt.WithLeeway(30 * time.Second)`（容忍时钟漂移，可调）

校验通过后，从 claims 取 `sub` 作为用户唯一标识，并读取 `name` / `preferred_username` / `picture` 用于资料 upsert。

> **首登幂等 upsert（消除竞态）**：REST 与 WS **两条通道**在首次见到某 `sub` 时都会**幂等 upsert** `users` 行——成员级 REST 入口（`bootstrap`/`me`）在验签后即用 claims（必要时 userinfo 补齐）`UpsertUser`，WS `auth` 同样如此。这保证新用户首次登录时，无论 REST 先到还是 WS 先到，都不会出现“用户行不存在”的 `NOT_FOUND`。**区别**：只有 WS（`auth`/`reauth`）在检测到资料变化时广播 `user_updated`；REST 的 upsert **不广播**（避免重复/竞态）。

> **Web 中介登录不影响服务端验签**：`access_token` 仍是 **IdP 签发的 JWT**（`aud=lumen-api`），无论它由桌面直连 IdP 取得还是经官网中介（[§2.2](#22-客户端取-tokenweb-中介登录回环-handoff)）取得，服务端的验签路径、claims 读取与 upsert 逻辑**完全不变**——服务端只认 IdP 的 JWKS 与 token 内的 `iss`/`aud`/`exp` 及 `name`/`picture` claims，不感知 token 的获取方式。

### 2.4 鉴权在两个通道的应用

| 通道 | 携带方式 | 校验时机 | 失败响应 |
|------|----------|----------|----------|
| REST | HTTP 头 `Authorization: Bearer <access_token>` | 每个受保护请求经中间件 | `401` + 错误信封 `code=UNAUTHENTICATED` |
| WebSocket | 连接建立后**首帧** `{"type":"auth","data":{"access_token":"..."}}` | 首帧；通过后绑定 `sub` 到连接会话 | `auth_error` 消息后服务端关闭连接 |

> WS 不在 URL query 里放 token（避免泄漏到日志/代理）。token 仅出现在首帧消息体。

### 2.5 WS 握手时序

```
客户端                          服务端 (WS hub)
  │                                  │
  │── WSS 连接建立 ──────────────────▶│  (尚未鉴权，仅接受 auth 帧)
  │                                  │  启动 5s 握手超时
  │── {type:auth,                    │
  │     data:{access_token}} ───────▶│
  │                                  │── Verifier.Verify(token)
  │                                  │   - 验签 + iss/aud/exp
  │                                  │   - 取 sub / name / picture
  │                                  │── users 表 upsert (资料同步)
  │                                  │── 绑定 sub 到连接会话(session)
  │◀── {type:auth_ok,                │
  │      data:{user, server_time}} ──│
  │                                  │
  │   (此后才接受其它消息类型)         │
```

- 鉴权成功前，服务端**只**接受 `auth` 类型消息；收到其它类型一律回 `auth_error` 并关闭。
- 握手超时（建议 5s）内未收到合法 `auth`：服务端关闭连接。
- `auth_ok.data.user` 即当前用户的完整 `User` 对象（见 [§3.5](#35-数据对象-schema)），供客户端无需额外 REST 调用即可拿到自身资料；`server_time` 为 RFC3339，是**服务端权威时间**（客户端可选消费，见 [§7.4](#74-时间格式)）。

### 2.6 token 过期 / 刷新处理

**契约规则**：

> **版本归属**：主动 `reauth`（同一连接热更新 token，不重连）整体为 **[v1]**。**[v0]** 下 access_token 过期一律**等价断线**——由客户端重新取 token 并**重连 + 重发 `auth`** 处理，不实现热 `reauth` 与 WS 期间的 `TOKEN_EXPIRED` 中途路径。

1. **桌面主动刷新（经官网中介）**：桌面在 `access_token` 临近过期时，向**官网中介**请求新 token——`POST https://example.com/api/desktop/refresh {desktop_session_id}`（详见 [web-design.md §5](./web-design.md#5-web-中介登录桌面)）。官网用 `client_secret` + KV 内的 `refresh_token` 向 IdP `grant_type=refresh_token`（IdP 轮换则官网更新 KV），返回 `{access_token, expires_in}`。**桌面不再直连 IdP，也不再持有 `refresh_token`**（`refresh_token` 永远只在官网 KV）。刷新后：
   - REST：后续请求带新 `access_token`。
   - WS **[v1]**：发送 `{"type":"reauth","data":{"access_token":"<new>"}}` 在**同一连接**上更新会话绑定的 token，**无需重连**。服务端重新验签并回 `auth_ok`（复用同一 `type`，`data.reauth=true`）。WS reauth 仍整体为 **[v1]**，用的是刷新后的新 `access_token`，归属不变。**[v0]**：不发 `reauth`；token 过期即按断线重连（重发 `auth`）处理。
   - 资料同步：刷新后的新 `access_token` claims 即承载最新资料（见 [§2.7](#27-资料双向同步)）；桌面 UI 资料以官网 `/exchange`/`/refresh` 返回为准。

2. **REST 收到 401（token 过期）**：桌面应先向官网中介刷新（`/api/desktop/refresh`）后重试**一次**；若官网返回 `401`（错误信封 `code=SESSION_INVALID`，即 `desktop_session_id` 不存在/失效），桌面据此**转重新登录**（重走 [§2.2](#22-客户端取-tokenweb-中介登录回环-handoff) 的 Web 中介登录）。

3. **WS 期间 token 过期 [v1]**：服务端在校验某帧需要的资源时若发现绑定 token 已过期，回 `error`（`code=TOKEN_EXPIRED`）但**不立即断开**，给客户端发送 `reauth` 的机会；客户端应在收到 `TOKEN_EXPIRED` 后立即向官网中介刷新并 `reauth`（用刷新后的新 `access_token`）。若 30s 内未收到合法 `reauth`，服务端关闭连接。**[v0]**：不实现该中途路径，token 过期直接关闭连接，由客户端重连。

> 服务端与桌面**均不**持有 refresh_token、均不直连 IdP 刷新；`refresh_token` 生命周期完全收敛在**官网 Cloudflare**（KV SESSIONS）。桌面侧的「刷新」即调官网 `/api/desktop/refresh`；`desktop_session_id` 失效（`SESSION_INVALID`）即转重新登录。登出经 `POST https://example.com/api/desktop/logout {desktop_session_id}`（删 SESSIONS）后清桌面凭据库、关 WS、重置 store。

### 2.7 资料双向同步

资料（`display_name`、`avatar_url`）来源于 OAuth 身份提供方，**双向保持同步**，本应用内不单独编辑资料。

**同步触发点**：

| 触发点 | 谁执行 | 动作 |
|--------|--------|------|
| 每次登录后 | 桌面经官网 `/exchange` 取最新 token（及 `profile`）→ WS `auth` 携带该 token | 服务端验签时从 claims 取 `name`/`preferred_username`/`picture` |
| 每次刷新 token 后 | 桌面经官网 `/refresh` 取新 token → WS `reauth` | 同上 |
| 服务端首帧/`reauth` 处理 | 服务端 | 对比 DB 现值，**命中既有行且实际有变化（changed）才 upsert 改值** 并向在线成员广播 `user_updated` |

> **`changed` 语义（统一定义）**：`changed` 仅指 **`ON CONFLICT DO UPDATE` 命中既有行，且 `display_name` 或 `avatar_url` 实际发生变化**。**首次 `INSERT`（新用户首登）不算 `changed`，不广播 `user_updated`**——新成员的可见性由 REST `members`/`bootstrap` 与（其加入语音时的）`user_joined` 用户快照负责。`user_updated` 整体为 **[v1]**；[v0] 下各端仅由 `auth_ok.user` / `bootstrap.members` 初始化资料，不实现 `user_updated` 消费分支。

**字段映射规则**（服务端）：
- `display_name` ← claims 的 `name`，缺失则回退 `preferred_username`，再缺失回退 `sub`。
- `avatar_url` ← claims 的 `picture`（直接使用 OIDC picture URL，**不做本地头像存储**）。

> 服务端优先用 `access_token` 内的 claims（若已含 `name`/`picture`）；若 JWT 不含这些 claim，则服务端调 userinfo（端点由 OIDC discovery 自 issuer 推导，或用可选的 `LUMEN_OAUTH_USERINFO_URL` 覆盖）补齐。**这条服务端验签 + 资料同步路径不因 Web 中介登录而改变**：`access_token` 仍是 IdP 签发的 JWT、claims 仍含 `name`/`picture`，服务端不感知 token 由官网中介获取。桌面本地 UI 的头像/昵称由官网 `/exchange`/`/refresh` 返回的 `profile` 提供（不再桌面直拉 userinfo），二者仍以服务端 DB 为准（DB 变化才广播 `user_updated`）。

---

## 3. REST API 完整清单

### 3.1 通用约定

- **Base path**：`/api/v1`（路径含主版本，见 [§7.1](#71-版本约定)）。
- **鉴权**：除显式标注 public 外，所有端点要求 `Authorization: Bearer <access_token>`。
- **Content-Type**：请求与响应均 `application/json; charset=utf-8`。
- **owner 端点**：标注「owner」的端点，服务端在鉴权后额外检查 `sub ∈ LUMEN_OWNER_SUBJECTS`，否则 `403 FORBIDDEN`。
- **时间字段**：一律 RFC3339（UTC，带 `Z`），如 `2026-06-29T08:30:00Z`。
- **ID 字段**：所有实体 ID 为字符串（服务端用 ULID/雪花，详见 [§5.5](#55-id-生成约定)）。

### 3.2 统一响应信封

所有 REST 响应（成功与失败）使用同一信封：

```jsonc
// 成功
{
  "success": true,
  "data": { /* 端点特定负载，可为对象/数组/null */ },
  "error": null
}

// 失败
{
  "success": false,
  "data": null,
  "error": {
    "code": "FORBIDDEN",            // 机器可读错误码，见 §7.2
    "message": "需要 owner 权限",    // 人类可读（中文），不含敏感信息
    "details": null                  // 可选：字段级校验错误等
  }
}
```

分页响应的 `data` 内含 `meta`（见 [§3.4](#34-端点消息历史分页)）。

### 3.3 端点总表

| # | 方法 | 路径 | 鉴权 | 用途 | 归属 |
|---|------|------|------|------|------|
| 1 | GET | `/api/v1/bootstrap` | 成员 | 引导数据（我的资料 + 频道列表 + 成员列表 + WS 接入信息） | v0 |
| 2 | GET | `/api/v1/me` | 成员 | 获取我的资料 | v0 |
| 3 | GET | `/api/v1/channels` | 成员 | 频道列表（text + voice） | v0 |
| 4 | GET | `/api/v1/channels/{channelId}/messages` | 成员 | 消息历史分页（cursor/limit） | v0 |
| 5 | GET | `/api/v1/members` | 成员 | 成员列表（曾登录过的全部用户） | v0 |
| 6 | POST | `/api/v1/channels` | owner | 创建频道 | v1 |
| 7 | PATCH | `/api/v1/channels/{channelId}` | owner | 重命名/调整频道 | v1 |
| 8 | DELETE | `/api/v1/channels/{channelId}` | owner | 删除频道 | v1 |
| 9 | POST | `/api/v1/members/{userId}/kick` | owner | 踢人（断开其所有连接 + 标记） | v1 |
| 10 | GET | `/api/v1/healthz` | public | 健康检查（Coolify 探活） | v0 |

> **踢人语义**：本应用准入策略为「能登录者皆可进入」，故「踢人」= 立即断开该用户所有 WS 连接、将其移出所有内存语音房间、并（`cooldown_seconds>0` 时）在 `users.kicked_until` 写入一个冷却时间戳；冷却期内该用户的 WS `auth`（以及 [v1] `reauth`）握手会被拒（`auth_error`，`code=KICKED`，带 `kicked_until`/`retry_after`）。这是软封禁，OAuth 侧不受影响。
>
> **`KICKED` 的两个下发时机**：① 踢人当下对每条**活动连接**下发 `auth_error{KICKED}` 后关闭（让被踢者实时得到反馈，而非被动当成网络断线重连）；② 冷却期内重连 `auth`/`reauth` 握手校验 `kicked_until` 命中而被拒。两者使用同一 `auth_error{code:KICKED}`。`cooldown_seconds=0`（仅踢出不封禁）时不写 `kicked_until`、`auth_error` 不带 `kicked_until`/`retry_after`，其语义为「瞬时断开不封禁、允许立即重入」，但客户端仍须按收到 `KICKED` 停止自动重连（由用户手动重连），故 `cooldown=0` 与 `>0` 在客户端行为上一致，区别仅在服务端是否在冷却期拒绝下次握手。
>
> **REST 软封禁拦截**：[v0]/[v1] 默认**不**拦截被踢者的 REST 只读端点（`bootstrap`/`me`/`channels`/`messages`/`members`）；REST 软封禁拦截为 [v1] 可选加固。即「冷却期被踢者仍可只读历史/成员，但无法建立 WS、无法进语音、无法发消息」属有意接受的范围裁剪。

> **资料同步无独立 REST 端点**：资料同步发生在 WS `auth`/`reauth` 时（见 [§2.7](#27-资料双向同步)），不通过 REST 触发。`GET /me` 仅读取当前 DB 内的资料快照。

### 3.4 端点详情

#### 端点 1 — `GET /api/v1/bootstrap` [v0]

登录后首屏一次性拉取所需全部引导数据，减少往返。

> 服务端在验签后会用 claims **幂等 upsert** 当前用户（见 [§2.3](#23-服务端验签jwks-本地验签)），故 `me` 必然存在——即使这是新用户、且 `bootstrap` 早于 WS `auth` 到达。

- **鉴权**：成员
- **请求体**：无
- **响应 `data`**：

```jsonc
{
  "me": { /* User 对象，见 §3.5 */ },
  "channels": [ /* Channel 对象数组，按 position 升序 */ ],
  "members": [ /* User 对象数组，全部曾登录用户 */ ],
  "voice_states": [ /* VoiceState 对象数组：当前各语音频道在线成员快照（内存态） */ ],
  "ws_url": "wss://chat.example.com/ws",    // WS 接入地址
  "server_time": "2026-06-29T08:30:00Z"
}
```

- **错误码**：`401 UNAUTHENTICATED`

> `voice_states` 是内存态快照（见 [§5.4](#54-语音房间在线成员内存态)）；客户端进入应用即可知道谁在哪个语音频道。后续变化经 WS `user_joined`/`user_left` 增量更新。

#### 端点 2 — `GET /api/v1/me` [v0]

- **鉴权**：成员
- **请求体**：无
- **响应 `data`**：单个 `User` 对象（含 `is_owner` 计算字段）。
- **错误码**：`401 UNAUTHENTICATED`

> 同 `bootstrap`：验签后幂等 upsert 当前用户，新用户首登经 REST 也保证返回（不依赖 WS `auth` 先到）。

#### 端点 3 — `GET /api/v1/channels` [v0]

- **鉴权**：成员
- **请求体**：无
- **查询参数**：`type`（可选，`text` | `voice`，省略返回全部）
- **响应 `data`**：`Channel` 对象数组，按 `position` 升序。
- **错误码**：`401 UNAUTHENTICATED`

#### 端点 4 — `GET /api/v1/channels/{channelId}/messages` [v0]

消息历史分页。采用 **cursor 分页**（向更早方向翻页，适合「加载更多历史」）。

- **鉴权**：成员
- **路径参数**：`channelId`（必须是 `type=text` 的频道）
- **查询参数**：

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `limit` | int | 50 | 单页条数，范围 1~100，越界服务端钳制 |
| `before` | string | （空） | cursor：返回**早于**该消息 ID 的消息（不含该条）。首次加载省略 |

- **响应 `data`**：

```jsonc
{
  "messages": [ /* Message 对象数组，按 created_at 升序（旧→新） */ ],
  "meta": {
    "limit": 50,
    "has_more": true,                 // 是否还有更早的消息
    "next_before": "01J9...EARLIEST"  // 下一页传给 before 的值（= 本页最早一条的 id）；无更多时为 null
  }
}
```

- **排序与游标语义**：服务端内部按 `id` 降序取 `limit+1` 条（`id` 为**单调熵 ULID，严格递增**，等价于时间序），判断 `has_more`，再反转为升序返回，便于前端直接 append 到顶部。
- **错误码**：`401 UNAUTHENTICATED`、`404 NOT_FOUND`（频道不存在）、`400 VALIDATION_ERROR`（`channelId` 是语音频道 / 参数非法）

#### 端点 5 — `GET /api/v1/members` [v0]

- **鉴权**：成员
- **请求体**：无
- **响应 `data`**：`User` 对象数组（全部曾登录过的用户，按 `display_name` 升序）。
- **错误码**：`401 UNAUTHENTICATED`

> 「成员」= 曾通过 OAuth 登录并被 upsert 进 `users` 表的所有用户。无独立「加入服务器」动作。

#### 端点 6 — `POST /api/v1/channels` [v1]（owner）

- **鉴权**：owner
- **请求体**：

```jsonc
{
  "name": "开黑1队",            // 必填，1~64 字符，去除首尾空白
  "type": "voice",            // 必填，"text" | "voice"
  "position": 3               // 可选，排序权重；省略则追加到末尾
}
```

- **响应 `data`**：新建的 `Channel` 对象。
- **副作用**：服务端向所有在线 WS 广播 `channel_created`（见 [§4.5](#45-频道与成员管理广播)）。
- **错误码**：`401 UNAUTHENTICATED`、`403 FORBIDDEN`（非 owner）、`400 VALIDATION_ERROR`

#### 端点 7 — `PATCH /api/v1/channels/{channelId}` [v1]（owner）

- **鉴权**：owner
- **请求体**（字段均可选，至少一个）：

```jsonc
{
  "name": "新名字",
  "position": 1
}
```

- **响应 `data`**：更新后的 `Channel` 对象。
- **副作用**：广播 `channel_updated`。
- **错误码**：`401`、`403 FORBIDDEN`、`404 NOT_FOUND`、`400 VALIDATION_ERROR`

#### 端点 8 — `DELETE /api/v1/channels/{channelId}` [v1]（owner）

- **鉴权**：owner
- **请求体**：无
- **响应 `data`**：`null`（`success:true`）
- **副作用**：
  - 若为语音频道：服务端关闭该频道内存 Room，断开其中所有 peer 的相关 track，广播 `user_left`（每个在房成员）+ `channel_deleted`。
  - 若为文字频道：该频道历史消息一并删除（外键 `ON DELETE CASCADE`），广播 `channel_deleted`。
- **错误码**：`401`、`403 FORBIDDEN`、`404 NOT_FOUND`

#### 端点 9 — `POST /api/v1/members/{userId}/kick` [v1]（owner）

- **鉴权**：owner
- **路径参数**：`userId`（目标用户 `id`）
- **请求体**：

```jsonc
{
  "cooldown_seconds": 3600    // 可选；省略=默认 3600；>0 写 kicked_until=now+冷却；**0=仅踢出(断连+移出房间)不封禁，不写 kicked_until**
}
```

- **响应 `data`**：`null`
- **自检**：`owner 不可踢自己`——若目标 `userId` == 调用者自身 `user.id`，提前返回 `400 VALIDATION_ERROR`（「不能踢出自己」），不进入下方副作用。
- **副作用（严格顺序）**：
  1. （`cooldown_seconds>0` 时）**先写** `users.kicked_until = now + cooldown`；`cooldown_seconds=0` 时**不写** `kicked_until`，仅断连。
  2. **断开**该用户全部 WS 连接——断开每条**活动连接**前先 `sendNow(auth_error{code:KICKED, message:"你已被移出服务器"})`（`cooldown>0` 时带 `kicked_until`/`retry_after`）再关闭。
  3. **移出**全部内存语音房间（每个语音 PC 随之 Close，见 [§6.3](#63-离开语音频道)）。
  4. **广播** 相关 `user_left`（给房内其他成员）。

  > 顺序约束（先写 `kicked_until` 再断连）用于消除「断开后旧端在 `kicked_until` 落库前抢先重连绕过冷却」的竞态窗口。`cooldown=0` 无 `kicked_until` 可写，故仅靠客户端收到 `KICKED` 后停止自动重连达成「断开并需手动重连」语义。
- **错误码**：`401`、`403 FORBIDDEN`、`404 NOT_FOUND`、`400 VALIDATION_ERROR`（含 owner 踢自己）

#### 端点 10 — `GET /api/v1/healthz` [v0]（public）

- **鉴权**：无
- **响应**：`200`，`{"success":true,"data":{"status":"ok"},"error":null}`。供 Coolify 探活。

### 3.5 数据对象 Schema

REST 与 WS 共享以下 JSON 对象定义（字段命名见 [§7.3](#73-字段命名规范)）。

#### `User`

```jsonc
{
  "id": "01J9X...",                       // 内部用户 ID（字符串）
  "oauth_subject": "sub-abc-123",         // OAuth sub（唯一）
  "display_name": "Nanako",               // 来自 OIDC name/preferred_username
  "avatar_url": "https://cdn.../a.png",   // 来自 OIDC picture，可为空字符串
  "is_owner": true,                       // 计算字段：sub ∈ LUMEN_OWNER_SUBJECTS
  "created_at": "2026-06-01T00:00:00Z",
  "updated_at": "2026-06-29T08:30:00Z"
}
```

> `is_owner` **不是** DB 字段，是服务端按配置实时计算后注入响应（见 [§5.3](#53-owner-判定说明)）。

#### `Channel`

```jsonc
{
  "id": "01J9Y...",
  "name": "大厅",
  "type": "voice",                        // "text" | "voice"
  "position": 0,                          // 排序权重，升序
  "created_at": "2026-06-01T00:00:00Z",
  "updated_at": "2026-06-01T00:00:00Z"
}
```

#### `Message`

```jsonc
{
  "id": "01J9Z...",                       // 单调熵 ULID，严格递增，兼作分页游标
  "channel_id": "01J9Y...",
  "author_id": "01J9X...",                // 引用 User.id
  "author": { /* 可选内联 User 快照，便于前端直接渲染 */ },
  "content": "晚上几点开？",               // 纯文本，UTF-8
  "created_at": "2026-06-29T08:31:00Z"
}
```

> 消息**永久保留**，无编辑/删除/已读跟踪/@提及/附件（均为后续 backlog）。`author` 内联快照为可读性服务，权威以 `author_id` 关联。
>
> **渲染解析约定**：客户端消息区展示作者头像/昵称时，应一律按 `author_id` 关联 `members` store 的**活值**渲染（`members.get(message.author_id)`），内联 `author` 仅作 `members` 未命中时的**回退 / 初始 seed**。这样 `user_updated`（[v1]）更新 `members` 后，消息区随之自动刷新头像/昵称，无需改写已落地的历史消息。

#### `VoiceState`（内存态，仅出现在 API 响应/WS，不入库）

```jsonc
{
  "channel_id": "01J9Y...",
  "user_id": "01J9X...",
  "muted": false,        // 自静音（不发声）
  "deafened": false,     // 扬声静音（不收声，通常同时 muted）
  "speaking": false      // 说话指示（前端 RMS 阈值检测后广播）
}
```

---

## 4. WebSocket 信令协议

### 4.1 连接与统一信封

- **接入地址**：`wss://<host>/ws`（容器内明文 `ws://`，Traefik 终结 TLS）。
- **统一消息信封**：每条消息（双向）均为 JSON：

```jsonc
{
  "type": "send_message",     // 消息类型（小写下划线）
  "data": { /* 类型特定负载 */ },
  "id": "c-7f3a"              // 可选：客户端请求关联 ID，服务端回应时回带（用于 ack/error 配对）
}
```

- `id` 仅用于把异步响应/错误关联回某次客户端请求（如 `webrtc_offer` 的回应、`send_message` 的失败回执）。S→C 的纯广播无 `id`。
- 单连接内消息须为文本帧（UTF-8 JSON）。
- 心跳：使用 WebSocket 协议层 **ping/pong**（服务端每 30s 发 ping，客户端 60s 无 pong 视为断开）。不在应用层定义心跳消息。

### 4.2 消息类型总览

方向标记：`C→S` 客户端发往服务端；`S→C` 服务端发往客户端；`双向` 两端皆可发。

| type | 方向 | 用途 | 归属 |
|------|------|------|------|
| `auth` | C→S | 首帧鉴权 | v0 |
| `reauth` | C→S | token 刷新后更新会话绑定 | v1 |
| `auth_ok` | S→C | 鉴权成功（回带 user + server_time） | v0 |
| `auth_error` | S→C | 鉴权失败（随后关闭连接） | v0 |
| `join_channel` | C→S | 加入某语音频道（进入内存 Room）；失败经通用 `error`（[§4.7](#47-通用错误-error-sc-v0)）回执，携带 `ref` | v0 |
| `leave_channel` | C→S | 离开当前语音频道；失败经通用 `error`（[§4.7](#47-通用错误-error-sc-v0)）回执，携带 `ref` | v0 |
| `user_joined` | S→C | 广播：某用户加入语音频道 | v0 |
| `user_left` | S→C | 广播：某用户离开语音频道 | v0 |
| `send_message` | C→S | 发送文字消息 | v0 |
| `message` | S→C | 广播：新文字消息 | v0 |
| `webrtc_offer` | S→C | SFU（offerer）下发 SDP offer（含重协商） | v0 |
| `webrtc_answer` | C→S | 客户端回 SDP answer | v0 |
| `ice_candidate` | 双向 | Trickle ICE 候选互发 | v0 |
| `speaking_state` | 双向 | 说话指示（C→S 上报，S→C 广播） | v0 |
| `mute_state` | 双向 | 自静音/扬声静音状态（C→S 上报，S→C 广播） | v1 |
| `user_updated` | S→C | 广播：用户资料变化（资料同步） | v1 |
| `channel_created` | S→C | 广播：频道新建 | v1 |
| `channel_updated` | S→C | 广播：频道更新 | v1 |
| `channel_deleted` | S→C | 广播：频道删除 | v1 |
| `error` | S→C | 通用错误（非鉴权致命级） | v0 |

> **逐人本地音量调节 / 本地静音某人 / 输入输出设备选择 / 麦克风测试 / PTT 与 VAD 切换** 均为**纯前端本地行为**，不经 WS（不影响他人、不入库）。`speaking_state` 由前端 `AnalyserNode` RMS 阈值检测后上报，是唯一与「说话」相关的网络消息。

### 4.3 鉴权类消息

#### `auth` (C→S) [v0]

```jsonc
{ "type": "auth", "data": { "access_token": "<JWT>" } }
```

#### `reauth` (C→S) [v1]

```jsonc
{ "type": "reauth", "data": { "access_token": "<new JWT>" } }
```

> 服务端处理 `reauth` 时同样校验 `kicked_until`：冷却期内回 `auth_error{code:KICKED}`（带 `kicked_until`/`retry_after`）并关闭连接，防止旧连接经 `reauth` 续命绕过软封禁。即 `auth` 与 `reauth` 两条路径都执行 `KICKED` 判定。

#### `auth_ok` (S→C) [v0]

```jsonc
{
  "type": "auth_ok",
  "data": {
    "user": { /* User 对象 */ },
    "server_time": "2026-06-29T08:30:00Z",
    "reauth": false              // [v1] true 表示这是 reauth 的回应；[v0] 恒为 false
  }
}
```

#### `auth_error` (S→C) [v0]

```jsonc
{
  "type": "auth_error",
  "data": { "code": "TOKEN_INVALID", "message": "令牌校验失败" }
}
```

当 `code=KICKED` 时（软封禁冷却期），`data` 额外携带两个**可选**字段，便于客户端展示倒计时并按时自动恢复：

```jsonc
{
  "type": "auth_error",
  "data": {
    "code": "KICKED",
    "message": "你已被移出服务器",
    "kicked_until": "2026-06-29T09:30:00Z",   // RFC3339 UTC（对齐 §7.4），冷却到期时刻
    "retry_after": 3540                         // 剩余秒数（int，= kicked_until - now）
  }
}
```

- `kicked_until` / `retry_after` **仅当 `code=KICKED` 时出现**；其它 code（`TOKEN_INVALID`/`TOKEN_EXPIRED`/`HANDSHAKE_TIMEOUT`）不带。属新增可选字段，向后兼容（[§7.1](#71-版本约定) 新增可选字段不升版本）。
- `code` 取值：`TOKEN_INVALID`、`TOKEN_EXPIRED`、`KICKED`、`HANDSHAKE_TIMEOUT`。服务端发送后**关闭连接**。

> **`KICKED` 的两个下发时机**（均使用同一 `auth_error{code:KICKED}`）：
> ① **踢人当下**：owner 调踢人端点（[§3.4](#34-端点详情) 端点 9）时，服务端对该用户每条**活动连接**先下发 `auth_error{KICKED}` 再关闭。
> ② **冷却期内重连被拒**：冷却期内该用户的 WS `auth`（以及 [v1] `reauth`）握手校验 `kicked_until > now`，命中即回 `auth_error{KICKED}` 并关闭。
> `auth`/`reauth` 均校验 `kicked_until`，防止旧连接经 `reauth` 续命绕过软封禁。
> 客户端收到 `KICKED` 后应停止自动重连（区别于网络断线），按 `retry_after` 展示「约 N 分钟后可重连」，到期方可恢复；`cooldown_seconds=0`（仅踢出不封禁）时不带 `kicked_until`/`retry_after`，客户端同样停止自动重连，由用户手动重连。

### 4.4 语音频道加入/离开与状态广播

#### `join_channel` (C→S) [v0]

```jsonc
{ "type": "join_channel", "data": { "channel_id": "01J9Y..." }, "id": "c-7f3a" }
```

- 客户端 **SHOULD** 携带 `id`，以便失败时服务端用 `error.ref` 关联回该请求（见下「失败契约」）。
- 服务端校验该频道存在且 `type=voice`；将该用户加入对应内存 Room；为其创建 `*webrtc.PeerConnection`；触发 SFU 重协商（见 [§4.6](#46-webrtc-信令与重协商)）。PC 生命周期见 [§6.3](#63-离开语音频道)（一个用户在一个语音频道恰好一个服务端 PC，不跨频道复用）。
- 一个用户同一时刻只在一个语音频道；若已在别的频道，服务端先隐式 `leave`。
- **同一用户同一时刻只允许一条「语音活动连接」**：若该用户已有另一条连接在某语音频道（含本频道），服务端先对其旧的语音连接执行**隐式 leave**（仅断开旧连接的语音/PC、解绑，**不广播该 user 的 `user_left`**——因为人未离开，只是换端），再让新连接持有该 user 的 PC。仅当该 user 在该房的**所有连接都离房或断开**时，才广播**一次** `user_left`。
- 成功后向该频道内**其他**成员广播 `user_joined`；并向加入者**逐条**回放房内现有成员的 `user_joined`（房间在线快照），**回放集合排除加入者自身**（集合 = 房内现有成员 \ {加入者}）。统一以**逐条** `user_joined` 为准，不使用聚合形式。`user_joined.data.user` 由服务端信令层用 `store.GetUserByID` + `ToDTO` 组装（见 [§4.4 `user_joined`](#user_joined-sc-v0)）。

**失败契约**：校验失败时服务端回通用 `error`（[§4.7](#47-通用错误-error-sc-v0)）并把请求 `id` 放入 `error.ref`：

| 失败原因 | `error.code` |
|----------|--------------|
| 频道不存在 | `NOT_FOUND` |
| 频道 `type≠voice` | `VALIDATION_ERROR` |
| 服务端入房 / 建 PeerConnection 失败 | `INTERNAL` |

> 客户端在收到自身 `user_joined`（房内回放含自己）或加入 ack 之前**不得认定加入成功**；收到带匹配 `ref` 的 `error` 时应回滚本地半成品状态（拆 PC/管线、复位当前频道）。

#### `leave_channel` (C→S) [v0]

```jsonc
{ "type": "leave_channel", "data": { "channel_id": "01J9Y..." }, "id": "c-8b2c" }
```

- 客户端 **SHOULD** 携带 `id`，以便失败时服务端用 `error.ref` 关联回该请求。
- 服务端将用户移出 Room，关闭/清理其在该房的 track，触发其余 peer 重协商（移除其 track）；仅当该 user 在该房已无任何连接时才广播一次 `user_left`（见 `join_channel` 多端规则）。
- **失败契约**：目标频道不存在 → 回 `error{code:NOT_FOUND, ref}`；用户不在该房 → 回 `error{code:VALIDATION_ERROR, ref}`；否则按成功路径处理。

#### `user_joined` (S→C) [v0]

```jsonc
{
  "type": "user_joined",
  "data": {
    "channel_id": "01J9Y...",
    "voice_state": { /* VoiceState 对象 */ },
    "user": { /* User 对象快照 */ }
  }
}
```

> **`user` 快照来源**：内存 Room 只持有 `user_id` + `VoiceState`（[§5.4](#54-语音房间在线成员内存态)），不持有 User 对象。无论是「新人加入广播给他人」还是「向加入者逐条回放房内现有成员」，`data.user` 均由服务端**信令层**用 `store.GetUserByID(memberID)` + `ToDTO` 组装补齐（Hub 持有 store 与 owners，故 `is_owner` 可一并计算）。这保证加入者收到的每条 `user_joined.user` 都非空，头像/昵称可直接渲染。回放时**排除加入者自身**（见 `join_channel`），避免给加入者重复发一条自身 `user_joined`。

#### `user_left` (S→C) [v0]

```jsonc
{
  "type": "user_left",
  "data": { "channel_id": "01J9Y...", "user_id": "01J9X..." }
}
```

#### `speaking_state` (双向) [v0]

```jsonc
// C→S 上报（前端 AnalyserNode RMS 越过阈值时；变化沿触发，不每帧发）
{ "type": "speaking_state", "data": { "speaking": true } }

// S→C 广播给同频道其他成员
{
  "type": "speaking_state",
  "data": { "channel_id": "01J9Y...", "user_id": "01J9X...", "speaking": true }
}
```

> 仅在 `speaking` **状态翻转**（false↔true）时发送，避免刷屏。说话指示（头像高亮）完全由此驱动，与媒体层解耦。

#### `mute_state` (双向) [v1]

```jsonc
// C→S 上报
{ "type": "mute_state", "data": { "muted": true, "deafened": false } }

// S→C 广播给同频道其他成员
{
  "type": "mute_state",
  "data": { "channel_id": "01J9Y...", "user_id": "01J9X...", "muted": true, "deafened": false }
}
```

> `muted`（自静音）/`deafened`（扬声静音）是「向他人展示的状态」，故经 WS 广播以渲染他人面板上的图标。逐人本地音量与本地静音某人是**纯本地**行为，不广播。

### 4.5 文字消息与频道/成员管理广播

#### `send_message` (C→S) [v0]

```jsonc
{
  "type": "send_message",
  "data": { "channel_id": "01J9Y...", "content": "晚上几点开？" },
  "id": "c-7f3a"
}
```

- 服务端校验：频道存在且 `type=text`；`content` 非空且 ≤ 4000 字符（去首尾空白后）。
- 持久化到 `messages` 表，生成 `id`/`created_at`，然后向所有在线连接广播 `message`。
- 失败回 `error`（回带请求 `id`）。

#### `message` (S→C) [v0]

```jsonc
{ "type": "message", "data": { /* Message 对象（含内联 author） */ } }
```

> **广播范围**：服务端把每条文字消息广播给**所有在线连接**（非仅该频道订阅者）；客户端按 `data.channel_id` 分桶接收。客户端首次进入某频道仍照常 `GET /messages` 拉历史首页，并按 `message.id`（单调熵 ULID，严格递增）与实时条目做并集去重、整体重排为升序，避免 WS 早到条目与历史首页之间出现重复或空洞（去重/分桶细节见客户端设计）。内联 `author` 仅作 `members` store 的 seed/回退（见 [§3.5](#35-数据对象-schema) 渲染解析约定）。

#### `user_updated` (S→C) [v1]

资料同步：当服务端在 `auth`/`reauth` 时检测到某用户 `display_name`/`avatar_url` 变化并 upsert 后，广播给在线成员。

```jsonc
{ "type": "user_updated", "data": { /* 完整 User 对象（更新后） */ } }
```

#### `channel_created` / `channel_updated` / `channel_deleted` (S→C) [v1]

```jsonc
{ "type": "channel_created", "data": { /* Channel 对象 */ } }
{ "type": "channel_updated", "data": { /* Channel 对象 */ } }
{ "type": "channel_deleted", "data": { "channel_id": "01J9Y..." } }
```

> 这三条由对应的 owner REST 端点（[§3.4](#34-端点详情) 端点 6/7/8）触发广播，保证所有客户端频道列表实时一致。

### 4.6 WebRTC 信令与重协商

服务端是 **SFU 且是 offerer**（参考 `sfu-ws` 模式，调研 §3.3/§3.4）。重协商由服务端主动发起，客户端被动应答。

#### NAT/连通性服务端配置（实现约定，非线协议字段）

服务端用 Pion `SettingEngine`：
- `ice.NewMultiUDPMuxFromPort(LUMEN_WEBRTC_UDP_PORT)` + `SetICEUDPMux` —— 单 UDP 端口收敛全部连接，**在创建任何 PeerConnection 之前**启动。
- `SetNAT1To1IPs([]string{LUMEN_PUBLIC_IP}, webrtc.ICECandidateTypeHost)` —— 用公网 IP 替换 host 候选。
- `SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)` —— 容器内关 mDNS。
- 网卡过滤排除 `docker`/`veth`/`br-`。
- Coolify 须将 `LUMEN_WEBRTC_UDP_PORT` 裸映射为 UDP（`-p <port>:<port>/udp`）；Traefik 不参与。

#### 重协商信令时序（新成员加入触发增删 track）

```
客户端 A (已在房)        服务端 SFU              客户端 B (新加入)
   │                       │                        │
   │                       │◀── join_channel ───────│
   │                       │   (建 PC_B, OnTrack)    │
   │                       │── signalPeerConnections()
   │                       │   对每个 peer: AddTrack/RemoveTrack
   │                       │   CreateOffer + SetLocalDescription
   │◀── webrtc_offer ──────│                        │
   │   (含 A 需新增的 track) │                        │
   │── webrtc_answer ──────▶│                        │
   │                       │── SetRemoteDescription  │
   │── ice_candidate ─────▶│◀── ice_candidate ──────│  (Trickle ICE 双向)
   │◀── ice_candidate ─────│── ice_candidate ──────▶│
   │                       │── webrtc_offer ────────▶│
   │                       │◀── webrtc_answer ───────│
   │   ... DTLS-SRTP 建立, Opus RTP 经 SFU 选择性转发 ...
```

#### `webrtc_offer` (S→C) [v0]

服务端在 `AddTrack`/`RemoveTrack` 后生成 offer 下发。每次房间内 track 集合变化（有人进/出、开/停麦导致 track 增删）都会触发。

```jsonc
{
  "type": "webrtc_offer",
  "data": {
    "channel_id": "01J9Y...",
    "sdp": { "type": "offer", "sdp": "v=0\r\n..." }
  },
  "id": "s-renego-12"
}
```

> **媒体↔用户绑定契约**：服务端转发某用户上行音频时，转发用的 `TrackLocalStaticRTP` 的 **StreamID 设为该上行用户的 `user_id`**（即 `NewTrackLocalStaticRTP(cap, remote.ID(), <uploaderUserID>)`，**不沿用** `remote.StreamID()`）。因此**下行每条远端 audio track 的 `MediaStream.id`（msid/StreamID）等于该音源用户的 `user_id`**；接收端据 `e.streams[0].id` 即得 `user_id`，用于把逐人音量/本地静音某人作用到正确音轨。说话高亮走 WS `speaking_state`（自带 `user_id`），不依赖此绑定。

#### `webrtc_answer` (C→S) [v0]

客户端 `setRemoteDescription(offer)` → `createAnswer()` → `setLocalDescription(answer)` 后回传。须回带触发该 answer 的 offer 的 `id`。

```jsonc
{
  "type": "webrtc_answer",
  "data": {
    "channel_id": "01J9Y...",
    "sdp": { "type": "answer", "sdp": "v=0\r\n..." }
  },
  "id": "s-renego-12"
}
```

#### `ice_candidate` (双向) [v0]

Trickle ICE：双方在收集到候选时即时互发。

```jsonc
{
  "type": "ice_candidate",
  "data": {
    "channel_id": "01J9Y...",
    "candidate": {
      "candidate": "candidate:... udp ...",
      "sdpMid": "0",
      "sdpMLineIndex": 0,
      "usernameFragment": "abc"
    }
  }
}
```

> `candidate` 为 `null` 表示候选收集结束（end-of-candidates），双方应能容忍。

#### 重协商并发与可靠性约定（服务端职责）

- 服务端 `signalPeerConnections()` 在每次 track/peer 变更后调用，内部按 `sfu-ws` 模式：清理已关闭 peer → 移除多余 sender → 补发缺失 track → `CreateOffer`/`SetLocalDescription`/下发 offer；同一把锁保护房间状态；最多重试 25 次，仍失败则解锁后 3s 重来，避免死锁。
- 客户端必须实现「滚动重协商」：随时可能收到新的 `webrtc_offer`，需立即 answer，不得丢弃。

### 4.7 通用错误 `error` (S→C) [v0]

非鉴权致命级的运行期错误（不关闭连接）：

```jsonc
{
  "type": "error",
  "data": {
    "code": "VALIDATION_ERROR",
    "message": "content 不能为空",
    "ref": "c-7f3a"            // 关联触发该错误的客户端消息 id（若有）
  }
}
```

`code` 复用 [§7.2](#72-错误码表) 的统一错误码（如 `VALIDATION_ERROR`、`NOT_FOUND`、`TOKEN_EXPIRED`、`RATE_LIMITED`（[v1] 可选生产）、`INTERNAL`）。`FORBIDDEN` 仅在 REST owner 端点产生，WS `error` 不产生（WS 当前无 owner 门控消息）。

---

## 5. 数据模型（PostgreSQL DDL）

### 5.1 总览

持久化仅三张表：`users`、`channels`、`messages`。**语音房间在线成员不入库**（内存态，[§5.4](#54-语音房间在线成员内存态)）。`owner` 也不入库（配置态，[§5.3](#53-owner-判定说明)）。

```
users ───< messages >─── channels
  (author_id)        (channel_id)
```

- **PostgreSQL 14+**；外键约束默认强制（无需 PRAGMA）。服务端用 `jackc/pgx/v5`（纯 Go，`CGO_ENABLED=0`）经连接池访问（详见 [服务端 §5.1](./server-design.md#51-store-封装postgresql)）。
- 时间列用 `TIMESTAMPTZ`（UTC 存储）；ID 列用 `TEXT`（单调熵 ULID，严格递增、可作分页游标）。**线格式（JSON）仍为 RFC3339 UTC（带 `Z`），见 [§7.4](#74-时间格式)**。

### 5.2 建表语句

```sql
-- ============================================================
-- users: 曾通过 OAuth 登录并被 upsert 的用户。资料来自 OIDC。
-- ============================================================
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,            -- 内部用户 ID（ULID 字符串）
    oauth_subject TEXT NOT NULL UNIQUE,        -- OAuth sub，唯一，资料同步与 owner 判定的锚点
    display_name  TEXT NOT NULL DEFAULT '',    -- 来自 OIDC name / preferred_username
    avatar_url    TEXT NOT NULL DEFAULT '',    -- 来自 OIDC picture（直接用 URL，不本地存储头像）
    kicked_until  TIMESTAMPTZ,                 -- 软封禁到期时间；NULL 表示未封禁。[v1]
    created_at    TIMESTAMPTZ NOT NULL,        -- 首次 upsert 时间(UTC)
    updated_at    TIMESTAMPTZ NOT NULL         -- 最近一次资料变化时间(UTC)
);

-- oauth_subject 已是 UNIQUE，登录时按 sub upsert 走唯一索引；无需额外索引。

-- ============================================================
-- channels: 频道定义（持久化）。text 或 voice。单 guild，无 guild_id。
-- ============================================================
CREATE TABLE IF NOT EXISTS channels (
    id         TEXT PRIMARY KEY,               -- 频道 ID（ULID 字符串）
    name       TEXT NOT NULL,                  -- 1~64 字符
    type       TEXT NOT NULL                   -- 'text' | 'voice'
                 CHECK (type IN ('text', 'voice')),
    position   INTEGER NOT NULL DEFAULT 0,     -- 排序权重，升序展示
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- 列表查询按 position 升序，建覆盖排序索引。
CREATE INDEX IF NOT EXISTS idx_channels_position
    ON channels (position, id);

-- ============================================================
-- messages: 文字消息，永久保留，无编辑/删除/已读跟踪。
-- ============================================================
CREATE TABLE IF NOT EXISTS messages (
    id         TEXT PRIMARY KEY,               -- 消息 ID（单调熵 ULID，严格递增，兼作分页游标）
    channel_id TEXT NOT NULL,                  -- 引用 channels.id（须为 text 频道）
    author_id  TEXT NOT NULL,                  -- 引用 users.id
    content    TEXT NOT NULL,                  -- 纯文本，UTF-8，<= 4000 字符
    created_at TIMESTAMPTZ NOT NULL,           -- UTC
    FOREIGN KEY (channel_id) REFERENCES channels (id) ON DELETE CASCADE,  -- 删频道连带删其消息（owner 显式操作，§3.4 端点 8）
    FOREIGN KEY (author_id)  REFERENCES users (id)    ON DELETE RESTRICT  -- 保护历史：用户仍有消息时禁止删其行（消息永久保留，§3.5）
);

-- 分页核心索引：按频道取最近 N 条 / 游标向前翻页（id 降序扫描）。
CREATE INDEX IF NOT EXISTS idx_messages_channel_id
    ON messages (channel_id, id DESC);
```

> **保留语义**：`messages.author_id` 用 `ON DELETE RESTRICT`——保证「消息永久保留」（[§3.5](#35-数据对象-schema)）不被「删用户」静默破坏；如确需移除某用户，须先迁移/清理其消息，或保留其 `users` 行（封禁用 `kicked_until` 软处理，不删行）。`channel_id` 用 `ON DELETE CASCADE`：删频道是 owner 显式操作，连带删其消息属预期（[§3.4](#34-端点详情) 端点 8）。「永久保留」指**不按时间/条数自动清理**，非禁止 owner 的显式删频道。

### 5.2.1 首次部署种子频道 [v0]

v0 的频道创建唯一途径 `POST /api/v1/channels` 为 [v1] owner 端点；纯 v0 部署后 `channels` 表为空，则没有任何 text/voice 频道可供 `send_message`/`join_channel` 校验通过，v0 开黑回路无法走通。为消除「v0 暗依赖 v1」，**v0 必须有首次部署幂等种子频道**。

- **规则**：迁移 DDL 执行后，在同一启动流程内**幂等**插入一组默认频道：text「大厅」+ voice「开黑1」。
- **幂等保证**：用**确定性 ULID**（固定常量，重复启动产出同一 id）配合 `ON CONFLICT (id) DO NOTHING`，保证重复启动不重复插入。
- **归属**：[v0]。

```sql
-- 首次部署幂等种子（确定性 ULID + ON CONFLICT DO NOTHING）
INSERT INTO channels (id, name, type, position, created_at, updated_at)
VALUES ('<固定ULID-text>',  '大厅',   'text',  0, now(), now()),
       ('<固定ULID-voice>', '开黑1',  'voice', 1, now(), now())
ON CONFLICT (id) DO NOTHING;
```

> 种子用确定性 ULID 即可幂等（无需先判表是否为空）；服务端在迁移后调用一个幂等的 `seedDefaultChannels` 完成此插入（详见 [服务端 §5.1](./server-design.md#51-store-封装postgresql)）。

### 5.3 owner 判定说明

**owner 不是数据库字段，而是配置态。** 理由与契约：

- owner 由 `LUMEN_OWNER_SUBJECTS`（逗号分隔的一组 OAuth `sub`）在服务端运行期决定，可随部署调整，无需改库。
- 服务端在内存中维护一个 `set[oauth_subject]`，每次请求/连接鉴权后：`is_owner := subject ∈ ownerSet`。
- 该计算结果作为 `User.is_owner` 注入 REST/WS 响应，**绝不**写入 `users` 表。
- owner 专属端点（频道 CRUD、踢人）在中间件层用同一判定做 `403` 拦截。

> 这样设计避免「DB 中的 owner 标记」与「配置」不一致的双源问题，且单 guild 小规模场景下 ownerSet 极小、判定 O(1)。

### 5.4 语音房间在线成员（内存态）

- 每个语音频道对应一个**内存 Room**：持有 `map[user_id]VoiceState`、各成员 `*webrtc.PeerConnection`、可转发的 `trackLocals`、一把保护锁。
- 「谁在哪个语音频道、是否静音/说话」**纯内存态**，**进程重启即清空**。
- 频道定义本身（`channels` 行）持久化；在线成员不持久化。
- 对外暴露形式：`bootstrap` 的 `voice_states` 快照 + WS 的 `user_joined`/`user_left`/`speaking_state`/`mute_state` 增量。
- 重启后客户端重连 → `bootstrap` 返回空 `voice_states` → 用户需重新 `join_channel`。
- **转发 track 的 StreamID 注入 `user_id`**：`trackLocals` 仍以 track ID 为 key，但创建转发用的 `TrackLocalStaticRTP` 时第三参 `StreamID` 传上行用户 `user_id`，使下行 track 的 msid 承载音源 `user_id`（媒体↔用户绑定，见 [§4.6](#46-webrtc-信令与重协商) webrtc_offer 契约）。
- **同一 user 同时只允许一条「语音活动连接」**：Room 以 `user_id` 单键持有 member（一份 `VoiceState`、一个 PC），PC/`send` 绑定到当前活动连接；新连接 join 时对其旧语音连接做**隐式 leave**（不广播 `user_left`），仅当该 user 在该房所有连接都离开/断开才广播一次 `user_left`。

### 5.5 ID 生成约定

- 所有实体 ID 用 **ULID**（26 字符 Crockford base32，字符串存储）。
- 服务端用 **单调熵 ULID（同毫秒内严格递增）** 生成 `messages.id`，保证**同序列内字典序 = 生成序**，可直接兼作分页游标（无需独立时间游标）；`created_at` 仅毫秒精度，排序以 `id` 为权威、与之一致。
- **实现约束**：必须用单调熵源（如 `oklog/ulid/v2` 的 `ulid.Monotonic`）并在并发下**加锁**（Monotonic 源非线程安全）；同毫秒递增溢出时回退到下一毫秒重试。详见 [服务端 §5.3.1](./server-design.md)。
- 服务端生成；客户端不生成实体 ID。

---

## 6. 核心数据流时序

### 6.1 引导（登录后首屏）

登录后客户端**先建立 WS 并完成鉴权（首屏资料同步发生在此）**，再用 REST `bootstrap` 拉静态数据；二者可并行，但 WS `auth_ok` 是资料同步的权威来源。

```
客户端                         服务端
  │                              │
  │  (已经 Web 中介登录拿到 access_token，见 §2.2)
  │                              │
  ├─ 1) WSS 连接 + auth ────────▶│  验签 → upsert(资料同步) → 绑定 sub
  │◀─ auth_ok{user,server_time}─┤  (若资料变化，向他人广播 user_updated)
  │                              │
  ├─ 2) GET /api/v1/bootstrap ─▶│  (Bearer)
  │◀─ {me,channels,members,     │
  │     voice_states,ws_url} ───┤
  │                              │
  │  3) 渲染首屏：频道树 + 成员列表 + 各语音频道在线人快照
  │                              │
  ├─ 4) GET /channels/{id}/messages?limit=50  （选中文字频道时按需拉历史）
  │◀─ {messages, meta} ─────────┤
```

**首屏需要拉取的数据**：
1. 自身资料（来自 WS `auth_ok.user`，也在 `bootstrap.me`）。
2. 频道列表（`bootstrap.channels`）。
3. 成员列表（`bootstrap.members`）。
4. 各语音频道当前在线成员快照（`bootstrap.voice_states`）。
5. 选中文字频道的最近一页历史（按需，端点 4）。

### 6.2 进入语音频道（端到端：REST + WS + WebRTC）

```
客户端 B                       服务端 SFU                      其他成员 A
  │                              │                               │
  │  (已 auth_ok，WS 在线)         │                               │
  ├─ join_channel{channel_id} ─▶│                               │
  │                              │ 校验 channel.type=voice         │
  │                              │ 入内存 Room；建 PC_B            │
  │                              │ AddTransceiver(audio,sendrecv) │
  │                              ├─ user_joined ─────────────────▶│
  │                              │ signalPeerConnections():        │
  │                              │   为 A/B 增删 track              │
  │◀─ webrtc_offer{sdp} ─────────┤                               │
  │  setRemoteDesc(offer)        │                               │
  │  createAnswer()              │                               │
  ├─ webrtc_answer{sdp} ────────▶│ setRemoteDesc(answer)          │
  │◀─ ice_candidate ─────────────┤                               │
  ├─ ice_candidate ─────────────▶│  (Trickle，双向多次)            │
  │                              │                               │
  │  === DTLS-SRTP 握手完成 ===   │                               │
  │  本地: getUserMedia(         │                               │
  │    echoCancellation:true,    │                               │
  │    autoGainControl:true,     │                               │
  │    noiseSuppression:false)   │  ← 降噪交给 RNNoise            │
  │  → AudioWorklet(RNNoise WASM)│                               │
  │  → 上行 Opus RTP ───────────▶│ OnTrack: 转发 RTP ───────────▶│  A 听到 B
  │◀──────────── 下行 Opus RTP ──┤ ◀── A 上行 RTP 经 SFU 转发 ────┤  B 听到 A
  │                              │                               │
  │  本地 AnalyserNode RMS 越阈值 │                               │
  ├─ speaking_state{true} ──────▶│── speaking_state ────────────▶│  A 看到 B 头像高亮
```

**关键点**：
- 上行音频管线（客户端）：`getUserMedia`（`echoCancellation=true, autoGainControl=true, noiseSuppression=false`）→ AudioWorklet 接 RNNoise WASM 降噪 → WebRTC 上行 Opus。
- 服务端 SFU **不转码**，原样转发 Opus RTP（`TrackLocalStaticRTP.WriteRTP`）。
- 说话指示由前端 `AnalyserNode` RMS 阈值检测，仅在翻转时经 `speaking_state` 广播，与媒体层解耦。
- PTT（按键说话）/VAD（声音激活）切换是前端本地逻辑：PTT 模式下未按键时本地不向上行 track 写入（或静音 sender）；VAD 模式下持续上行。模式切换不需通知服务端。

### 6.3 离开语音频道

```
客户端 B                       服务端 SFU                      其他成员 A
  ├─ leave_channel{channel_id}─▶│                               │
  │                              │ 移出 Room；删 B 的 trackLocals  │
  │                              │ Close PC_B                      │
  │                              │ signalPeerConnections():        │
  │                              │   从 A 的 PC RemoveTrack(B)     │
  │◀──────── (B 已离开) ─────────┤── user_left{user_id=B} ───────▶│
  │                              │   webrtc_offer(给 A 重协商) ───▶│
```

WS 连接意外断开等价于隐式 `leave_channel`：服务端 `OnConnectionStateChange` 到 `Closed` 时清理 Room 并（仅当该 user 在该房已无任何连接时）广播 `user_left`。

> **PC 生命周期约定**：一个用户在一个语音频道对应**恰好一个**服务端 PC；该 PC 由频道 Room 持有，随成员 `join` 建、随显式/隐式 `leave`（含断线、切频道）**Close**，**不跨频道复用**。当同一 `user_id` 以新连接再次 `join` 同一频道（如重连/换端）时，若 `Room.members[user_id]` 仍存在残留 member，先 `removeMember`（Close 旧 PC、删其 `trackLocals`、`signalPeerConnections`）再 `addPeer`，并以新连接的 `send` 闭包重建，避免向已死连接发 offer 的双 PC 竞态。

---

## 7. 版本与错误约定、命名规范、时间格式

### 7.1 版本约定

- **API 主版本**入路径：`/api/v1/...`、WS 路径 `/ws`（协议版本随 API 主版本）。
- 破坏性变更升主版本（`/api/v2`）；新增可选字段不升版本，客户端须**容忍未知字段**。
- 消息信封新增可选字段同理向后兼容。
- 功能特性的 v0/v1/v2 归属见全文标注；`[v2]` E2E 仅见 [附录 A](#附录-a-v2-e2e-加密概述)。

### 7.2 错误码表

机器可读 `code`（REST `error.code` 与 WS `error.data.code` / `auth_error.data.code` 共用）：

| code | HTTP（REST） | 含义 | 出现位置 |
|------|-------------|------|----------|
| `UNAUTHENTICATED` | 401 | 缺少/无效 Bearer | REST |
| `TOKEN_INVALID` | 401 | JWT 验签失败（签名/iss/aud） | REST / WS auth_error |
| `TOKEN_EXPIRED` | 401 | JWT 已过期 | REST / WS error |
| `FORBIDDEN` | 403 | 非 owner 访问 owner 端点 | REST |
| `KICKED` | 403 | 用户处于软封禁冷却期 | WS auth_error |
| `NOT_FOUND` | 404 | 资源不存在 | REST / WS |
| `VALIDATION_ERROR` | 400 | 请求体/参数校验失败 | REST / WS |
| `RATE_LIMITED` | 429 | 触发限流 | REST / WS |
| `HANDSHAKE_TIMEOUT` | — | WS 握手超时未鉴权 | WS auth_error |
| `INTERNAL` | 500 | 服务端内部错误（不泄漏细节） | REST / WS |

- 错误 `message` 用中文、面向用户、**不含敏感信息**（堆栈/SQL/token 一律不外泄）。
- 字段级校验错误可放入 `error.details`，形如 `{"field":"name","reason":"长度需 1~64"}` 数组。
- **`FORBIDDEN` 仅在 REST owner 端点产生**：WS 通道当前无 owner 门控消息，故 WS `error` 不产生 `FORBIDDEN`。
- **`RATE_LIMITED` 的生产者为 [v1] 可选限流**（`send_message` / WS `auth` 每连接令牌桶，详见服务端限流约定）：v0 及 v1-未启用限流时本码不产出，客户端分支为前向兼容预留。

### 7.3 字段命名规范

- **JSON 字段**：`snake_case`（如 `display_name`、`channel_id`、`created_at`）。
- **消息 `type`**：`snake_case` 小写（如 `join_channel`、`webrtc_offer`）。
- **错误 `code`**：`UPPER_SNAKE_CASE`。
- **枚举值**：小写字符串（`text`/`voice`、`offer`/`answer`）。
- **WebRTC SDP/candidate 内部字段**：保持浏览器原生命名（`sdpMid`、`sdpMLineIndex`、`usernameFragment`），因为它们直接对接 `RTCSessionDescription`/`RTCIceCandidate`，不强制转 snake_case。
- **布尔字段**：肯定式命名（`muted`/`deafened`/`speaking`/`is_owner`/`has_more`）。

### 7.4 时间格式

- 一律 **RFC3339**，UTC，带 `Z` 后缀，秒级（必要时毫秒）：`2026-06-29T08:30:00Z`。
- **服务端为权威时钟。** `auth_ok.data.server_time` 与 `bootstrap.data.server_time` 为服务端权威时间戳，**[v0] 客户端可选消费**（预留）；v0 不实现本地时钟偏移校正，时间戳一律按服务端下发的 RFC3339 值、以本地时区渲染。若未来需纠正本地时钟漂移，见 [v1+] 时钟校准（记录 `serverClockOffset` 并按 `now()+offset` 渲染）。
- 客户端展示时按本地时区渲染；**线格式（JSON）一律 RFC3339 UTC（带 `Z`）**；服务端 PostgreSQL 存储用 `TIMESTAMPTZ`（UTC），序列化时格式化为 RFC3339。

---

## 附录 A：v2 E2E 加密概述

> 仅概述，`[v2]` 推迟实现。v0/v1 仅依赖传输层 **DTLS-SRTP**。

- **目标**：在 SFU（仅转发、不解密）之外，对音频负载做端到端加密，服务端无法窃听。
- **技术路线**：浏览器 **Insertable Streams**（`RTCRtpScriptTransform` / `encodedStreams`）+ **SFrame** 对 RTP 负载逐帧加解密。
- **密钥模型**：房间共享对称密钥起步。成员进出时由某一可信方（owner 或派生方案）经 WS **下发 / 轮换** 房间密钥；轮换在 `user_joined`/`user_left` 时触发。
- **新增信令（预留，v2 细化）**：`e2e_key_update`（S→C / 双向），payload 含加密后的房间密钥分发与 epoch 序号。
- **不影响 SFU 转发逻辑**：SFrame 加密发生在 RTP 负载层，SFU 仍按 `TrackLocalStaticRTP` 原样转发密文。
- **兼容性约束**：仅 Windows + Wails webview（Chromium）确保 Insertable Streams 可用；v2 上线前需确认目标 webview 版本支持 `RTCRtpScriptTransform`。

> v2 的密钥分发、轮换 epoch、与重协商的交互细节将在独立的 `docs/design/e2e-design.md` 中展开，本文不再深入。

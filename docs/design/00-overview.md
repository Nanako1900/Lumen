# Lumen 主设计总览

> 文档版本: 1.0
> 状态: 设计总览（导航 + 决策 + 路线图）
> 适用范围: Lumen 全系统（外部身份提供方 + 静态 SPA 官网 + Windows 客户端 + 单 Go 服务端 = 资源服务器 + 登录 broker）
>
> **架构（single-backend，权威——用户已决定）**：EdgeOne Pages = **纯静态**，只发布构建后的 React SPA（`website/dist/`），**无 Pages Functions、无 EdgeOne KV、无任何 secret**。登录 broker 已整体移入**单 Go 服务端**（`chat.example.com`），后者现是**唯一后端 = 资源服务器（JWT/JWKS 验签，既有）+ OIDC 登录 broker（新增，commit `82f344e`）**。`client_secret` 与 `refresh_token` **只**存于服务端（Postgres；refresh_token 用独立密钥加密于列）。桌面 handoff 线协议**逐字节保留**，仅宿主由 `example.com` 改为 `chat.example.com`。
> 配套设计: [协议契约](./protocol-design.md)、[服务端设计](./server-design.md)、[客户端设计](./client-design.md)、[官网设计](./web-design.md)

Lumen 是一个**类 Discord 的轻量语音聊天工具**（开黑用）：Windows 桌面客户端 + 单 Go 二进制服务端，对接已有的外部 OAuth2/OIDC 身份服务器，用 Coolify(Docker) 部署。本文是全局总览与导航；**接口契约的唯一权威是 [`protocol-design.md`](./protocol-design.md)**，本文与各详细设计如有冲突，以契约为准。

**版本归属约定**（贯穿全套文档）：

| 标记 | 含义 |
|------|------|
| `[v0]` | 最小开黑回路：登录验签、文字历史、单语音频道收发、基本状态广播 |
| `[v1]` | 类 Discord 体验：频道 CRUD、踢人、资料双向同步、PTT/VAD、降噪、逐人音量、桌面集成、自动更新、多频道 |
| `[v2]` | 推迟项：E2E 加密（Insertable Streams + SFrame）。仅作附录概述 |

未标注的内容默认 `[v0]` 即需要。

---

## 目录

1. [目标与非目标](#1-目标与非目标)
2. [关键决策总表](#2-关键决策总表)
3. [四方架构与四条主数据通路](#3-四方架构与四条主数据通路)
4. [组件地图与文档导航](#4-组件地图与文档导航)
5. [技术栈与关键依赖清单](#5-技术栈与关键依赖清单)
6. [进度路线与验收标准](#6-进度路线与验收标准)
7. [部署与运维总览](#7-部署与运维总览)
8. [风险与开放项](#8-风险与开放项)

---

## 1. 目标与非目标

### 1.1 目标

- **小圈子开黑语音**：2~6 人/语音频道，单服务器（single guild），总成员十人量级。
- **低延迟语音**：Pion SFU 选择性转发、不转码、保持 Opus 原样；DTLS-SRTP 传输层加密。
- **类 Discord 的基础体验**：多语音/文字频道、说话指示、自静音/扬声静音、逐人音量、PTT/VAD、RNNoise 降噪、文字历史。
- **复用已有身份**：对接外部 OAuth2/OIDC，**不自建身份服务器**；凡能登录者皆可进入。
- **资料零维护**：`display_name`/`avatar` 全部来自 OIDC，**双向保持同步**，应用内不编辑资料。
- **桌面原生集成**：全局 PTT 热键（游戏全屏可用）、系统托盘、最小化隐藏、单实例锁、开机自启、内置自动更新。
- **极简运维**：单 Go 二进制 + 托管 PostgreSQL，Coolify(Docker) 一键部署，全部配置走环境变量。
- **官网（静态 SPA：营销/下载/账户中心 UI）**：React + TailwindCSS 构建为**纯静态站**，部署于 EdgeOne Pages（只发布 `website/dist/`，无 Functions/KV/secret）。登录中介与账户中心的**服务端逻辑不在 EdgeOne**——SPA 跨源调用 Go 服务端（`chat.example.com`）上的登录 broker（confidential OIDC client 在服务端）。

### 1.2 非目标（明确不做 / 推迟）

| 非目标 | 说明 | 归属 |
|--------|------|------|
| 自建 OAuth2/OIDC 服务器 | 对接外部已有/自建身份服务器；本设计不实现它 | — |
| token introspection | 一律 JWKS 本地验签（JWT）；introspection 不在本期范围 | — |
| 多服务器/多 guild、多租户 | 仅单 guild，频道直挂其下，无 `guild_id` | — |
| 本地头像存储/应用内编辑资料 | 头像直接用 OIDC `picture` URL；资料只读、随 OIDC 同步 | — |
| 未读/已读跟踪、@提及、消息编辑/删除、文件上传 | 文字仅历史加载/分页，消息永久保留 | backlog |
| 桌面 toast 通知 | 列为后续可选 | backlog（可选） |
| 多平台客户端 | 仅 Windows/amd64 | — |
| 媒体转码 / MCU | SFU 仅选择性转发，保持 Opus 原样 | — |
| STUN/TURN 依赖 | 服务端公网可达 + `SetNAT1To1IPs` host 候选，通常无需 | — |
| E2E 加密 | 推迟到 v2（Insertable Streams + SFrame） | v2 |

---

## 2. 关键决策总表

所有已锁定的需求决策汇总如下（详细实现见对应详细设计文档）：

| 维度 | 决策 | 归属 | 详见 |
|------|------|------|------|
| **产品形态** | 类 Discord 的轻量语音聊天（开黑），2~6 人/语音频道，单 guild | v0 | [协议 §1.2](./protocol-design.md#12-单服务器模型single-guild) |
| **客户端平台** | 仅 Windows/amd64 | v0 | [客户端 §1](./client-design.md#1-整体架构与职责切分) |
| **服务端形态** | 单 Go 二进制 = **资源服务器 + OIDC 登录 broker**，Coolify(Docker) 部署（`chat.example.com`） | v0 | [服务端 §1](./server-design.md#1-模块包结构与依赖方向) / [服务端 §10](./server-design.md#10-登录-broker账户中心--桌面登录中介) |
| **身份** | 对接外部 OAuth2/OIDC（不自建）；access_token=JWT；JWKS 本地验签（签名+iss+aud+exp） | v0 | [协议 §2](./protocol-design.md#2-鉴权流程总览) |
| **官网** | React + TailwindCSS 构建为**纯静态 SPA**，部署于 EdgeOne Pages（**只发布 `website/dist/`，无 Functions/无 KV/无 secret**）；承载营销/下载 + 账户中心 UI；登录/账户 API 在 Go 服务端 | v0 | [官网 §1](./web-design.md#1-概述与定位) / [官网 §2](./web-design.md#2-技术栈与部署) |
| **登录 broker（服务端托管）** | broker 为 confidential OIDC client，**驻留 Go 服务端**（`client_secret` 走 `LUMEN_OAUTH_CLIENT_SECRET`，仅服务端）；桌面经 broker 登录（回环 handoff），获 `desktop_session_id`；`refresh_token` 仅存服务端 Postgres（`desktop_sessions.refresh_token_enc`，独立密钥加密） | v0 | [服务端 §10](./server-design.md#10-登录-broker账户中心--桌面登录中介) |
| **客户端配置** | 桌面基址指向 `chat.example.com`（broker+API+WS 同域）；不内置 issuer/client_id/scope（在服务端 broker）。handoff 线协议不变，未来客户端改动 = **仅 base URL** | v0 | [服务端 §10.1](./server-design.md#101-broker-包与-9-个路由) / [客户端 §2](./client-design.md#2-go-后端oauth2-pkce-登录与-token-管理) |
| **验签库** | `keyfunc/v3` + `golang-jwt/jwt/v5`，强制 RS256（防 alg 混淆/none） | v0 | [服务端 §2.1](./server-design.md#21-jwks-本地验签keyfunc-v3--golang-jwt-v5) |
| **introspection** | 不实现（不在本期范围） | — | [协议 §2.1](./protocol-design.md#21-身份模型) |
| **准入** | 凡能登录者皆可进入；首次验证按 `sub` 在 `users` 表 upsert；无白名单 | v0 | [协议 §2.1](./protocol-design.md#21-身份模型) |
| **权限** | owner + 普通成员两级；owner 由环境变量 `LUMEN_OWNER_SUBJECTS`（一组 sub）配置，**不入库** | v0 | [协议 §5.3](./protocol-design.md#53-owner-判定说明) |
| **owner 能力** | 建/删频道、踢人；（后续）任命管理员 | v1 | [协议 §3.3](./protocol-design.md#33-端点总表) |
| **资料来源** | `display_name`/`avatar` 来自 OIDC（`name`/`preferred_username`/`picture`）；头像直接用 OIDC picture URL，不本地存储 | v0 | [协议 §2.7](./protocol-design.md#27-资料双向同步) |
| **资料同步** | 双向保持同步：每次登录与每次刷新 token 重新拉 claims/userinfo；DB 有变化则 upsert 并 WS 广播 `user_updated` | v1 | [协议 §2.7](./protocol-design.md#27-资料双向同步) |
| **语音模式** | PTT（按键说话）与 VAD（声音激活）两种，设置里切换；纯前端本地逻辑，不通知服务端 | v1 | [客户端 §8.3](./client-design.md#83-pttvad-门控切换状态机调研-06-2--3) |
| **语音控制** | 自静音(mute)/扬声静音(deafen)（经 WS `mute_state` 广播）；逐人本地音量/本地静音某人/设备选择/麦克风测试（纯前端，不经 WS） | v1 | [协议 §4.2](./protocol-design.md#42-消息类型总览) |
| **说话指示** | 前端 `AnalyserNode` RMS 阈值检测，仅翻转时经 WS `speaking_state` 广播；头像高亮 | v0 | [协议 §4.4](./protocol-design.md#44-语音频道加入离开与状态广播) |
| **降噪** | RNNoise WASM，前端 AudioWorklet 接入；`getUserMedia` 约束 `echoCancellation=true, autoGainControl=true, noiseSuppression=false`（降噪交给 RNNoise） | v1 | [客户端 §8.2](./client-design.md#82-采集--rnnoise-worklet调研-06-1) |
| **文字消息** | 仅历史加载/分页（cursor）；永久保留（PostgreSQL，不自动清理）；无未读/已读、无 @提及/编辑删除/附件 | v0 | [协议 §3.4](./protocol-design.md#34-端点详情) |
| **API 形态** | REST + WebSocket 混合：REST 管非实时数据，WS 管实时（握手/信令/文字广播/状态） | v0 | [协议 §1.1](./protocol-design.md#11-通信分层) |
| **媒体** | Pion SFU，仅选择性转发、不转码、Opus 原样；每语音频道=一个内存 Room；在线成员纯内存态，重启清空（频道定义持久化） | v0 | [协议 §5.4](./protocol-design.md#54-语音房间在线成员内存态) |
| **重协商** | 服务端是 SFU 且是 offerer；track 增删触发重协商，经 WS 信令；客户端被动应答、滚动重协商 | v0 | [协议 §4.6](./protocol-design.md#46-webrtc-信令与重协商) |
| **NAT/连通性** | Pion `SettingEngine`：单 UDP 端口（UDPMux）+ `SetNAT1To1IPs`（公网 IP，host 候选）+ 关 mDNS；通常无需 STUN/TURN | v0 | [服务端 §4.1](./server-design.md#41-共享-apisettingengineudpmux--nat1to1--mdns-off) |
| **E2E 加密** | 推迟到 v2；v0/v1 仅依赖传输层 DTLS-SRTP；v2 用 Insertable Streams + SFrame，房间共享对称密钥起步 | v2 | [协议 附录 A](./protocol-design.md#附录-av2-e2e-加密概述) |
| **客户端外壳** | Wails v2 Go 外壳 + Svelte 前端（WebView2）；Bindings + Events 双通道 | v0 | [客户端 §1.3](./client-design.md#13-bindingevents-通信约定) |
| **桌面集成** | 全局 PTT 热键（后台/全屏游戏）、系统托盘+最小化隐藏、单实例锁、开机自启、内置自动更新；未选桌面 toast 通知 | v1 | [客户端 §3](./client-design.md#3-go-后端桌面集成ptt托盘最小化单实例自启) |
| **token 存储** | 桌面只存不透明高熵 `desktop_session_id`（Windows Credential Manager `wincred`/DPAPI）；access_token 仅内存；`refresh_token` 不落桌面，仅存服务端 Postgres（`desktop_sessions.refresh_token_enc`，AES-256-GCM，独立 `LUMEN_REFRESH_ENC_KEY`） | v0 | [客户端 §2.3](./client-design.md#23-token-安全存储仅-windows用-wincred) / [服务端 §10.3](./server-design.md#103-store-broker-两表--janitor) |
| **CORS（同 SITE 跨源）** | SPA(`example.com`) 跨源调 broker(`chat.example.com`)，二者同 SITE；会话 cookie=`SameSite=Lax+HttpOnly+Secure+Path=/+HOST-ONLY(无 Domain)`；服务端仅对精确 `LUMEN_WEB_BASE_URL` 源发 CORS(带 credentials)，永不用 `*`；SPA XHR 用 `credentials:'include'`，`/auth/*` 为顶层导航 | v0 | [服务端 §10.5](./server-design.md#105-账户中心-cookie同-site-跨源) / [服务端 §10.6](./server-design.md#106-单点-cors-中间件) |
| **部署** | 官网=EdgeOne Pages **纯静态**（`website/dist/`）；Go 服务端=Coolify(Docker)；Traefik 终结 TLS 提供 WSS（HTTP/TCP）；WebRTC UDP 媒体不经 Traefik，单独裸映射 UDP 端口；容器内监听明文 HTTP/WS | v0 | [服务端 §7](./server-design.md#7-部署coolify) |
| **配置** | 全部走环境变量（Coolify 注入），启动校验必填项 fail-fast | v0 | [服务端 §6.1](./server-design.md#61-配置全部环境变量) |
| **ID 生成** | ULID（26 字符），服务端生成；`messages.id` 单调递增兼作分页游标 | v0 | [协议 §5.5](./protocol-design.md#55-id-生成约定) |
| **持久化** | PostgreSQL（`jackc/pgx/v5`，纯 Go、CGO_ENABLED=0），连接池；业务三表 `users`/`channels`/`messages` + broker 两表 `broker_states`/`desktop_sessions`（架构变更新增） | v0 | [服务端 §5.1](./server-design.md#51-store-封装postgresql) / [§10.3](./server-design.md#103-store-broker-两表--janitor) |

> **默认频道为 v0 引导项**：首次部署在迁移后幂等种子默认频道（text『大厅』+ voice『开黑1』，确定性 ULID + ON CONFLICT DO NOTHING），使 v0 空库部署即可发文字/进语音，不依赖 v1 的 owner 频道 CRUD（[协议 §5.2.1](./protocol-design.md#521-首次部署种子频道) / [服务端 §5.1](./server-design.md#51-store-封装postgresql)）。

---

## 3. 四方架构与四条主数据通路

### 3.1 四方架构（身份 / 静态 SPA / 客户端 / 服务端=资源服务器+broker）

```
┌──────────────────────────────────────────────────────────────────────┐
│  ① 身份层（外部，不在本设计实现）                                        │
│     OAuth2 / OIDC 服务器（如 Keycloak）                                  │
│     - Authorization Code + PKCE 登录    - JWKS 公钥端点（验签）           │
│     - userinfo 端点（资料同步）          - 签发 JWT access_token(aud含lumen-api)│
└──────────────────────────────────────────▲──────────────▲──────────────┘
                        broker 中介 IdP       │              │ 拉 JWKS/userinfo
                        (Auth Code+PKCE,       │              │ (服务端本地验签)
                         confidential;         │              │
                         client_secret 在服务端)│              │
┌──────────────────────────┐                  ┌┴──────────────┴──────────────────┐
│ ② 官网层（EdgeOne Pages）  │                  │  ④ 服务端层（单 Go 二进制，chat.*） │
│   React+Tailwind          │  SPA 跨源(同SITE) │   = 资源服务器 + 登录 broker       │
│   *纯静态* website/dist/   │  credentials:    │  ┌────────┐ ┌──────────────┐    │
│  ┌──────────────────────┐ │  'include'       │  │ broker │ │ REST /api/v1 │    │
│  │ 营销/下载 + 账户中心UI │─┼─────────────────▶│  │ 9 路由 │ │ WS hub(信令) │    │
│  │ (无 Functions/无 KV/   │ │  /auth/* 顶层导航 │  │ +secure│ │ (广播)       │    │
│  │  无 secret)            │ │◀────Set-Cookie──┤  └───┬────┘ └──────┬───────┘    │
│  └──────────────────────┘ │  (HOST-ONLY,Lax) │      │  auth/owner  │            │
└──────────────────────────┘                  │      ▼              ▼            │
   SPA fetch /updates/latest.json ────────────▶│  ┌──────┐  ┌──────────────┐     │
                                                │  │store │  │ Pion SFU     │     │
┌────────────────────────────────┐             │  │PgSQL │  │ (UDPMux 单口)│     │
│  ③ 客户端层（Windows，Wails v2） │             │  │+broker│ └──────────────┘     │
│  ┌──────────────┐ ┌─────────────┐│ 回环 handoff │  │表/加密│         ▲            │
│  │ Go 外壳       │ │ Svelte 前端 ││ (base URL=   │  └──────┘         │            │
│  │ - 委托登录    │ │ - REST/WS   ││  chat.*)     └───────────────────┼────────────┘
│  │ - 持session_id│◀▶│ - WebRTC   │┼──── /desktop/*、/api/desktop/* ──┘
│  │ - access内存  │ │ - Web Audio ││  access_token (Bearer / WS auth)
│  │ - PTT/托盘/更新│ │ - RNNoise   ││  HTTPS / WSS / DTLS-SRTP → 同一 chat.* 服务端
│  └──────────────┘ └─────────────┘│
│   Bindings / Events 双通道         │
└────────────────────────────────────┘
```

> **关键流向**：桌面**经服务端 broker 登录**（不自跑 IdP PKCE）；**broker 中介 IdP**（confidential client，`client_secret` 仅在服务端）；桌面用 access_token 连**同一** Go 服务端（handoff 线协议不变，仅宿主 `example.com→chat.example.com`）；**Go 服务端用 IdP JWKS 本地验 access_token**（资源侧逻辑不变）。SPA 直接 `fetch chat.example.com/updates/latest.json`（无下载代理）。

**层职责边界**：

| 层 | 职责 | 关键约束 |
|----|------|----------|
| ① 身份层 | 登录、签发 JWT、暴露 JWKS/userinfo | 外部已有，本设计不实现；broker 与资源验签指向**同一** issuer/audience（access_token `aud` 含 `lumen-api`） |
| ② 官网层（EdgeOne，纯静态） | 只发布构建后的 React SPA（`website/dist/`）：营销/下载 + 账户中心 UI | **无 Pages Functions、无 EdgeOne KV、无任何 secret**；`edgeone.json` 仅留 build 配置 + SPA rewrites；所有登录/账户 API 由服务端 broker 跨源提供 |
| ③ 客户端层 | Go 外壳管原生能力（委托 broker 登录/凭据/热键/托盘/更新）；前端管 UI+网络+WebRTC+音频 | 不内置 IdP issuer/client_id/scope；只持 `desktop_session_id`，access_token 仅内存（[客户端 §1.2](./client-design.md#12-职责切分原则)） |
| ④ 服务端层 | **资源服务器**（验签、REST 数据、WS 信令/广播、SFU 转发、Postgres）**+ 登录 broker**（9 端点、`client_secret`/`refresh_token`、账户中心 cookie 会话） | 容器内只监听明文 HTTP/WS；TLS 由 Traefik 终结；UDP 媒体裸端口直发；对精确 `LUMEN_WEB_BASE_URL` 源发 CORS（同 SITE 跨源，[服务端 §10.6](./server-design.md#106-单点-cors-中间件)） |

### 3.2 四条主数据通路

```
通路 A：身份/鉴权（服务端 broker handoff + HTTPS + WS 首帧）─────────────────
  客户端 Go ──系统浏览器(回环 handoff)──▶ 服务端 broker /desktop/login ──Auth Code+PKCE──▶ OAuth2 服务器
  服务端 broker ──client_secret 换 token──▶ OAuth2 ──access+refresh──▶ broker(Postgres broker_states/desktop_sessions)
  客户端 Go ──POST chat.*/api/desktop/exchange──▶ broker ──{access_token,desktop_session_id,profile}──▶ 客户端
  客户端前端 ──Bearer / WS auth(JWT)──▶ 同一服务端(资源侧) ──JWKS 本地验签──▶ OAuth(拉公钥)
  通过 → upsert(资料同步) → 绑定 sub 到会话 → auth_ok{user}
  刷新：客户端 ──POST chat.*/api/desktop/refresh{desktop_session_id}──▶ broker ──新 access_token──▶ 客户端
  (refresh_token 仅存服务端 Postgres，加密于列；桌面只持 desktop_session_id)
  账户中心(SPA 跨源)：浏览器 ──顶层导航 chat.*/auth/login──▶ IdP ──▶ /auth/callback 设 HOST-ONLY cookie
                    SPA XHR ──credentials:'include' chat.*/api/me──▶ broker ──{display_name,avatar_url}──▶ SPA

通路 B：非实时数据（REST，HTTPS 经 Traefik 终结 TLS）──────────────────
  客户端前端 ──GET /api/v1/bootstrap|messages|channels|members──▶ Traefik
            ──明文 http──▶ 服务端 REST handler ──▶ store(PostgreSQL) ──▶ 信封响应
  owner CRUD：POST/PATCH/DELETE channels、kick → 触发 WS 广播副作用

通路 C：实时控制（WebSocket，WSS 经 Traefik）────────────────────────────
  客户端前端 ◀──WS──▶ 服务端 WS hub
    C→S: auth/reauth、join/leave_channel、send_message、speaking_state、
         mute_state、webrtc_answer、ice_candidate
    S→C: auth_ok、message、user_joined/left、speaking_state、mute_state、
         user_updated、channel_created/updated/deleted、webrtc_offer、ice_candidate、error

通路 D：媒体（WebRTC，DTLS-SRTP over 单 UDP 端口，不经 Traefik）──────────
  客户端前端 ──getUserMedia→RNNoise(AudioWorklet)→Opus 上行──▶ 单 UDP 端口
            ──▶ 服务端 Pion SFU(OnTrack) ──TrackLocalStaticRTP.WriteRTP 选择性转发──▶ 其他成员
  说话指示走通路 C 的 speaking_state，与媒体层解耦
```

> 通路 A/B/C 全部经 Coolify Traefik（终结 TLS、提供 WSS/HTTPS）；**唯独通路 D（UDP 媒体）绕过 Traefik**，必须单独裸映射 UDP 端口（[服务端 §7.1](./server-design.md#71-数据流总览)）。

---

## 4. 组件地图与文档导航

### 4.1 文档导航

| 文档 | 职责 | 主要读者 |
|------|------|----------|
| **本文** [`00-overview.md`](./00-overview.md) | 目标、决策总表、架构总览、路线图、部署/运维汇总、风险 | 全员入口 |
| [`protocol-design.md`](./protocol-design.md) | **唯一权威接口契约**：REST 端点、WS 消息、JSON Schema、PostgreSQL DDL、错误码、时间/命名规范 | 前后端共同遵守 |
| [`server-design.md`](./server-design.md) | 服务端实现蓝图：包结构、auth/signaling/sfu/store/rest 模块、配置、并发、优雅关闭、Coolify 部署 | 服务端实现者 |
| [`client-design.md`](./client-design.md) | 客户端实现蓝图：Wails 外壳（登录/桌面/更新）、Svelte 前端、语音流水线、重协商、断线重连 | 客户端实现者 |
| [`web-design.md`](./web-design.md) | 官网实现蓝图：React+Tailwind **纯静态 SPA**（EdgeOne Pages）、账户中心 UI（跨源调服务端 broker）、SPA 路由/构建配置、下载页；登录 broker 服务端契约见 [服务端 §10](./server-design.md#10-登录-broker账户中心--桌面登录中介) | 官网实现者 |
| `docs/research/01-pion-sfu.md` … `06-frontend-webrtc-rnnoise.md` | 选型/骨架调研依据（被上述设计引用） | 实现时参考 |
| `docs/design/e2e-design.md`（未来） | v2 E2E 密钥分发/轮换 epoch/与重协商交互 | v2 实现者 |

### 4.2 组件地图（组件 → 文档章节）

| 组件 | 客户端 | 服务端 | 契约 |
|------|--------|--------|------|
| 静态 SPA 官网（营销/下载/账户中心 UI） | — | — | [官网 §1](./web-design.md#1-概述与定位) / [官网 §2](./web-design.md#2-技术栈与部署) |
| 登录 broker（回环 handoff + 桌面会话 + 账户中心 cookie） | [§2](./client-design.md#2-go-后端oauth2-pkce-登录与-token-管理)（委托 broker 登录） | [§10](./server-design.md#10-登录-broker账户中心--桌面登录中介) | [官网 §5](./web-design.md#5-web-中介登录桌面) |
| 委托 broker 登录 / token 管理 | [§2](./client-design.md#2-go-后端oauth2-pkce-登录与-token-管理) | [§10.1](./server-design.md#101-broker-包与-9-个路由) | [§2.2](./protocol-design.md#22-客户端取-tokenpkce仅参考) |
| JWKS 验签 / owner 判定 / 资料映射 | — | [§2](./server-design.md#2-鉴权网关auth) | [§2.3](./protocol-design.md#23-服务端验签jwks-本地验签) / [§5.3](./protocol-design.md#53-owner-判定说明) |
| REST 客户端 / handler | [§7.1](./client-design.md#71-rest-客户端) | [§5.4](./server-design.md#54-rest-handler-与契约对应) | [§3](./protocol-design.md#3-rest-api-完整清单) |
| WS 客户端 / 信令 hub | [§7.2](./client-design.md#72-ws-客户端) | [§3](./server-design.md#3-信令模块signaling--ws) | [§4](./protocol-design.md#4-websocket-信令协议) |
| WebRTC PC / SFU | [§9](./client-design.md#9-sfu-多-track-重协商在前端的处理) | [§4](./server-design.md#4-sfu-模块sfupion-v4) | [§4.6](./protocol-design.md#46-webrtc-信令与重协商) |
| 语音流水线 / RNNoise / 说话检测 | [§8](./client-design.md#8-前端语音流水线) | — | [§6.2](./protocol-design.md#62-进入语音频道端到端rest--ws--webrtc) |
| store / PostgreSQL / 分页 | — | [§5](./server-design.md#5-频道消息模块与-storerest--store) | [§5](./protocol-design.md#5-数据模型postgresql-ddl) |
| 桌面集成（PTT/托盘/自启/单实例） | [§3](./client-design.md#3-go-后端桌面集成ptt托盘最小化单实例自启) | — | — |
| 自动更新 | [§4.3](./client-design.md#43-与-coolify-托管更新文件的衔接) | [§7.7](./server-design.md#77-自动更新文件托管)（`/updates/` 静态托管） | — |
| 配置 / 启动装配 / 优雅关闭 | [§2.1](./client-design.md#21-客户端配置来源) | [§6](./server-design.md#6-配置启动装配并发模型优雅关闭可观测性安全) | [§1.3](./protocol-design.md#13-全局配置项环境变量coolify-注入) |
| 部署（Coolify/Traefik/UDP） | — | [§7](./server-design.md#7-部署coolify) | [§1.1](./protocol-design.md#11-通信分层) |

---

## 5. 技术栈与关键依赖清单

### 5.1 技术栈总览

| 层 | 语言/运行时 | 核心框架 |
|----|------------|----------|
| 服务端（资源 + broker） | Go 1.22+（构建用 golang:1.23-alpine，`CGO_ENABLED=0`） | net/http（Go 1.22 路由）、Pion WebRTC v4、jackc/pgx/v5、go-oidc/oauth2（broker）、crypto/aes-gcm（secure） |
| 客户端外壳 | Go（Wails v2，仅 Windows；托盘/全屏 PTT 需 `CGO_ENABLED=1`） | Wails v2、net/http（委托 broker 登录回环 handoff）、wincred（存 `desktop_session_id`） |
| 客户端前端 | TypeScript（WebView2/Chromium） | Svelte + Vite、原生 WebSocket/RTCPeerConnection/Web Audio |
| 官网 | TypeScript（浏览器） | React + TailwindCSS，**构建为纯静态**部署于 EdgeOne Pages（`website/dist/`；无 Functions/无 KV/无 secret） |
| 持久化 | PostgreSQL（pgx 连接池） | 五表：users/channels/messages + broker_states/desktop_sessions（[服务端 §10.3](./server-design.md#103-store-broker-两表--janitor)） |
| 部署 | Docker / Coolify（服务端）+ EdgeOne Pages（官网静态） | Traefik 反代（TLS 终结/WSS）+ 裸 UDP 端口映射 |

### 5.2 服务端 Go 依赖

| 库 | 导入路径 | 版本/约束 | 用途 | 归属 |
|----|----------|-----------|------|------|
| keyfunc | `github.com/MicahParks/keyfunc/v3` | ≥ v3.5.0（推荐 v3.8.0；避开撤回版 v3.0.0–v3.3.7） | JWKS 自动拉取/缓存/轮换 | v0 |
| jwt | `github.com/golang-jwt/jwt/v5` | v5.3.0 | JWT 解析与校验（RS256/iss/aud/exp） | v0 |
| go-oidc | `github.com/coreos/go-oidc/v3/oidc` | v3 | userinfo 补齐（资料兜底） | v0 |
| pion/webrtc | `github.com/pion/webrtc/v4` | ≥ v4.2.5（含 CVE-2026-26014 修复） | SFU PeerConnection/Track/重协商 | v0 |
| pion/ice | `github.com/pion/ice/v4` | v4 | UDPMux 单端口、NAT1To1、mDNS off | v0 |
| pion/rtp | `github.com/pion/rtp` | — | RTP 包解析/转发 | v0 |
| pgx | `github.com/jackc/pgx/v5` | v5 | 纯 Go PostgreSQL 驱动（无 CGO；经 database/sql stdlib，或原生 pgxpool） | v0 |
| ulid | `github.com/oklog/ulid/v2` | v2 | ULID 实体 ID（时间有序，兼分页游标） | v0 |
| websocket | `github.com/coder/websocket`（或 `gorilla/websocket`） | — | WS 升级/读写（context 友好） | v0 |
| 标准库 | `log/slog`、`database/sql`、`net/http`、`context` | Go 1.22+ | 结构化日志、SQL、HTTP 路由、生命周期 | v0 |

### 5.3 客户端 Go 依赖

| 库 | 导入路径 | 版本/约束 | 用途 | 归属 | CGO |
|----|----------|-----------|------|------|:---:|
| Wails v2 | `github.com/wailsapp/wails/v2` | v2 | 桌面外壳（窗口/binding/event/单实例/隐藏到托盘） | v0 | — |
| 标准库 net/http | `net/http`、`crypto/sha256` | — | 委托官网登录（回环监听 + handoff verifier/S256 + 调 `/api/desktop/exchange`/`refresh`/`logout`）；不再自跑 IdP PKCE | v0 | 否 |
| wincred | `github.com/danieljoos/wincred` | — | Windows Credential Manager（存 `desktop_session_id`，DPAPI；refresh_token 不落桌面） | v0 | 否 |
| hotkey | `golang.design/x/hotkey` | v0.6.1 | 默认全局 PTT 热键（`RegisterHotKey`） | v1 | 否 |
| gohook | `github.com/robotn/gohook` | — | 全屏游戏低层钩子（`WH_KEYBOARD_LL`） | v1 | **是** |
| systray | `github.com/energye/systray` | — | 系统托盘 + 菜单（去 GTK fork） | v1 | **是** |
| sys/registry | `golang.org/x/sys/windows/registry` | — | 开机自启（HKCU Run 键） | v1 | 否 |
| 标准库 crypto | `crypto/ed25519`、`crypto/sha256` | — | 自动更新校验（SHA256 + ed25519） | v1 | — |

> CGO 取舍：默认 PTT（hotkey）与自启（registry）无 CGO；托盘（systray）与全屏 PTT（gohook）需 `CGO_ENABLED=1`（mingw/gcc）。本项目接受引入 CGO 以换取托盘与全屏 PTT（[客户端 §3](./client-design.md#3-go-后端桌面集成ptt托盘最小化单实例自启)）。

### 5.4 前端依赖

| 依赖 | 用途 | 版本/约束 | 归属 |
|------|------|-----------|------|
| Svelte + Vite | UI 框架 + 打包（Wails v2 默认前端模板） | Svelte 4/5 | v0 |
| 原生 `WebSocket` / `RTCPeerConnection` / Web Audio | WS 信令、WebRTC、音频处理（`AnalyserNode`/`GainNode`/`AudioWorklet`） | 浏览器内置，无需库 | v0 |
| `wailsjs/runtime` + `wailsjs/go/...` | Wails 自动生成的 binding 调用 + Event 订阅 | 随 Wails | v0 |
| `@timephy/rnnoise-wasm` | RNNoise drop-in `AudioWorkletNode`（自带 worklet + polyfill） | 0.2 fork | v1 |

> Vite 取 worklet URL 用 `?worker&url`；WASM 须以 `application/wasm` MIME 提供（Wails 打包默认正确）。

---

## 6. 进度路线与验收标准

三期渐进交付；**v0+v1 详细到可直接实现，v2 仅附录概述**。

### 6.1 v0 — 最小开黑回路

**目标**：一个频道能登录、能发文字、能进语音说话听到彼此。

**范围**（[服务端 §8](./server-design.md#8-v0v1-归属汇总) / [客户端 §14](./client-design.md#14-附录版本归属速查表) / [官网 §10](./web-design.md#10-v0-归属与验收)）：

- 服务端（资源侧）：配置 fail-fast、JWKS 验签（RS256/iss/aud/exp）、owner 配置态判定、REST（`bootstrap`/`me`/`channels`/`messages`/`members`/`healthz`）、WS 握手（`auth`/`auth_ok`/`auth_error`）、文字（`send_message`/`message`）、语音加入离开（`join_channel`/`leave_channel`/`user_joined`/`user_left`）、`speaking_state`、WebRTC 信令（`webrtc_offer`/`webrtc_answer`/`ice_candidate`）、SFU（UDPMux+NAT1To1+mDNS off、Room、OnTrack 转发、重协商、清理）、store（五表+游标分页+ULID）、userinfo 兜底补齐 + 首次部署幂等种子默认频道（text『大厅』+ voice『开黑1』，确定性 ULID + ON CONFLICT DO NOTHING）。
- 服务端（broker 侧，架构变更新增）：9 个 broker 端点（`/desktop/login`、`/desktop/callback`、`/api/desktop/exchange`、`/api/desktop/refresh`、`/api/desktop/logout`、`/auth/login`、`/auth/callback`、`/auth/logout`、cookie 版 `/api/me`）+ confidential OIDC client（`client_secret` 仅服务端）+ `broker_states`/`desktop_sessions` 两表 + 60s janitor + `internal/secure`（PKCE/AES-GCM）+ 账户中心 HOST-ONLY 封装 cookie + 单点精确 CORS（[服务端 §10](./server-design.md#10-登录-broker账户中心--桌面登录中介)）。`main.go` 落地（此前仅 `.gitkeep`）：装配 + janitor + 优雅关闭。
- 客户端：委托 broker 登录（回环 handoff，不自跑 IdP PKCE）+ `desktop_session_id` 凭据存储 + 经 `chat.*/api/desktop/refresh` 刷新 access_token + access_token 仅内存、REST 客户端、WS 客户端、WebRTC PC（单向 offerer 重协商）、上行采集管线（基础，可省 RNNoise）、说话检测、加入/离开语音、实时文字、状态广播渲染、深色主题、错误 toast、WS 重连 + PC 重建兜底。
- 官网（EdgeOne Pages，纯静态）：构建后的 React SPA（`website/dist/`，无 Functions/无 KV/无 secret）——营销/下载页 + 账户中心 UI（跨源调服务端 broker `/auth/*`、cookie 版 `/api/me`）+ 直接 `fetch chat.*/updates/latest.json`。`edgeone.json` 仅留 build 配置 + SPA rewrites。

**验收标准**：

- [ ] 桌面经服务端 broker 登录（回环 handoff）成功：系统浏览器走 `chat.*/desktop/login` → broker 中介 IdP（confidential client）→ 回环回调 → `chat.*/api/desktop/exchange` 拿到 `{access_token, desktop_session_id, profile}`，并用 access_token 连上同一 Go 服务端（WS/REST）；`desktop_session_id` 落 Credential Manager（refresh_token 仅存服务端 Postgres），重启客户端免重登。
- [ ] access_token 临期经 `POST chat.*/api/desktop/refresh{desktop_session_id}` 换新 access_token；session 失效返回 401（`SESSION_INVALID`）→ 客户端转重新登录；登出 `POST chat.*/api/desktop/logout` 后清凭据库 + 关 WS + 重置 store。
- [ ] 账户中心（EdgeOne 静态 SPA 跨源调服务端 broker）：`chat.*/auth/login` OIDC(PKCE) 登录设 HOST-ONLY 封装 cookie 会话；账户中心 SPA 以 `credentials:'include'` 调 `chat.*/api/me` 显示 OIDC 资料（头像/昵称）+ 下载客户端 + 退出；不调 Lumen API。CORS 仅放行精确 `LUMEN_WEB_BASE_URL`。
- [ ] 服务端用 JWKS 本地验签通过（RS256），非法/过期/错 aud 的 token 被拒（`TOKEN_INVALID`/`TOKEN_EXPIRED`）。
- [ ] `GET /api/v1/bootstrap` 一次返回 me/channels/members/voice_states/ws_url；首屏渲染频道树+成员。
- [ ] 文字频道可发消息并实时广播；`messages?limit&before` 游标分页向上加载历史正常。
- [ ] 两个客户端进入同一语音频道：经 WS 信令完成 SFU 重协商，DTLS-SRTP 建立，互相听到对方 Opus 音频。
- [ ] 头像高亮（`speaking_state` 翻转广播）正确反映谁在说话。
- [ ] WS 断线指数退避重连成功；语音 PC failed 时整体重建并重新 `join_channel` 恢复。
- [ ] Coolify 部署：WSS/HTTPS 经 Traefik；裸 UDP 端口媒体可达；`GET /healthz` 通过。

### 6.2 v1 — 类 Discord 体验

**目标**：完整开黑体验 + 桌面原生集成 + 多频道管理。

**范围**：

- 服务端：WS `reauth` + TOKEN_EXPIRED 中途处理、资料双向同步广播 `user_updated`（DB 变化触发）、`mute_state` 广播、owner REST（`POST/PATCH/DELETE channels` + 广播 `channel_*`）、`kick` + 软封禁 `kicked_until` + 断连、多语音频道（多 Room 并存）；可选限流/REST 软封禁拦截/`/metrics`。
- 客户端：RNNoise 降噪 worklet、PTT/VAD 门控切换、逐人音量/本地静音某人、自静音/扬声静音（`mute_state`）、设备选择/切换/麦克风测试、`reauth`、资料双向同步渲染、频道 CRUD/踢人（owner UI）、全局 PTT 热键（后台/全屏游戏）、系统托盘+菜单、关窗口隐藏到托盘、单实例锁+参数转交、开机自启、自动更新、本地设置持久化。

**验收标准**：

- [ ] OIDC 资料变更（改 name/picture）后，下次登录/刷新触发 `user_updated`，在线成员头像/昵称实时刷新。
- [ ] PTT/VAD 可在设置切换；全局 PTT 热键在应用后台/游戏全屏下仍能按键说话（全屏模式走低层钩子）。
- [ ] RNNoise 降噪生效（`noiseSuppression=false`，降噪交给 RNNoise）；失败时降级为不降噪不阻断通话。
- [ ] 自静音/扬声静音经 `mute_state` 广播，他人面板图标实时更新；逐人音量/本地静音某人纯本地生效（不影响他人）。
- [ ] owner 可建/删/改频道与踢人；`channel_*`/`user_left` 广播使所有客户端列表实时一致；删语音频道关闭 Room。
- [ ] 踢人写 `kicked_until`，冷却期内该用户 WS `auth` 被拒（`KICKED`）。
- [ ] 托盘最小化隐藏（关窗不退出）、单实例锁、开机自启开关、自动更新（SHA256+ed25519 校验通过才安装）均工作。
- [ ] token 刷新后经 `reauth` 在同一 WS 连接更新会话（不重连）；REST 401 刷新重试一次。

### 6.3 v2 — E2E 加密（附录概述，推迟实现）

**目标**：在 SFU（仅转发、不解密）之外对音频负载端到端加密，服务端无法窃听。

**范围**（[协议 附录 A](./protocol-design.md#附录-av2-e2e-加密概述) / [服务端 §9](./server-design.md#9-v2e2e对服务端的影响简述) / [客户端 §13](./client-design.md#13-v2-对前端的影响概述sframe--insertable-streams)）：

- 技术路线：浏览器 Insertable Streams（`RTCRtpScriptTransform`）+ SFrame 逐帧加解密。
- 密钥模型：房间共享对称密钥起步；成员进出时由可信方经 WS `e2e_key_update`（预留信令）下发/轮换，带 epoch 序号。
- 服务端职责仅中转 `e2e_key_update`（按 channel_id 广播），不生成/不解密密钥；SFU 转发逻辑、REST/store/DDL/鉴权/配置均不变。
- 客户端新增 `lib/voice/e2e.ts` + `lib/voice/sframe.worker.ts`；`peer.ts` 在 sender/receiver 挂 transform。

**验收标准（v2 启动前置）**：

- [ ] 确认目标 WebView2（Chromium）版本支持 `RTCRtpScriptTransform`。
- [ ] 服务端 `e2e_key_update` 透传不解密；SFU 仍原样转发密文 RTP。
- [ ] 成员进出触发密钥轮换（epoch 递增），新成员能解密后续音频。
- [ ] 详细密钥分发/轮换/与重协商交互在独立 `docs/design/e2e-design.md` 展开。

---

## 7. 部署与运维总览

部署分两处：**Go 服务端**为 Coolify(Docker)（`chat.example.com`），是**唯一后端 = 资源服务器 + 登录 broker**——HTTP/WS 走 Traefik（终结 TLS、提供 WSS）、WebRTC UDP 媒体裸端口映射绕过 Traefik、broker 9 端点与 `/api/v1/*` 共用同一 HTTP 服务（完整步骤见 [服务端 §7](./server-design.md#7-部署coolify) / [服务端 §10](./server-design.md#10-登录-broker账户中心--桌面登录中介)）；**官网**为 EdgeOne Pages **纯静态**（`example.com`），只发布构建后的 React SPA（`website/dist/`），**无 Functions/无 KV/无 secret**（详见 [官网 §2](./web-design.md#2-技术栈与部署)）。

### 7.1 部署拓扑

```
                       ┌─────────────────────────────────┐
客户端 ──443/tcp───────▶│ Coolify Traefik（边缘，终结 TLS）  │
  (HTTPS REST + WSS)    │  - 自动签 Let's Encrypt 证书      │
                       │  - HTTP/TCP → 容器明文 http/ws    │
                       └──────────────┬──────────────────┘
                                      │ 明文转发
                                      ▼
                       ┌─────────────────────────────────┐
客户端 ──UDP 媒体───────▶│ 容器: lumen-server               │
  (DTLS-SRTP)           │  - http/ws 监听 0.0.0.0:8080      │
  裸 UDP 端口映射         │  - WebRTC 监听 0.0.0.0:40000/udp  │
  (不经 Traefik)         │  - 连 PostgreSQL(5432, 内网)      │
                       └────────────────┬────────────────┘
                                        │ 5432/tcp (Coolify 内网)
                                        ▼
                       ┌─────────────────────────────────┐
                       │ PostgreSQL 服务 (Coolify 资源)    │
                       │  - 自带持久化与备份               │
                       └─────────────────────────────────┘
```

### 7.2 Coolify 配置项

| Coolify 字段 | 值 | 作用 |
|--------------|-----|------|
| Ports Exposes | `8080` | 容器监听端口，Traefik 据此转发 HTTP/WS；第一个=健康检查口 |
| Ports Mappings | `40000:40000/udp` | 裸 UDP 直发宿主机，绕过 Traefik（WebRTC 媒体） |
| Domains (FQDN) | `https://chat.example.com` | Traefik 自动签证书 + 强制 HTTPS；对外即 wss/https |
| PostgreSQL（数据库资源） | 新建 Coolify PostgreSQL 服务（或外部 PG） | 提供 `LUMEN_DATABASE_URL`；持久化/备份由该资源管理（应用容器无需持久卷） |
| Persistent Storage | `/app/updates`（更新文件卷） | 持久化自动更新文件（`latest.json` + NSIS 安装包 + ed25519 签名），由服务端 `GET /updates/` 静态托管 |
| Health Check | `GET /api/v1/healthz` | Coolify 探活 |

> 若 Coolify 版本 Ports Mappings 拒绝 `/udp`，回退用 Docker Compose 部署类型在 compose `ports:` 写 `"40000:40000/udp"`。用 Ports Mappings 部署会失去 Rolling Updates（重部署有短暂中断，语音重连，可接受）。

### 7.3 环境变量清单汇总（Coolify 注入，启动 fail-fast）

| 环境变量 | 含义 | 示例 | 必填 | 归属 |
|----------|------|------|:----:|------|
| `LUMEN_OAUTH_ISSUER` | OIDC issuer（校验 `iss` 及发现） | `https://auth.example.com/realms/lumen` | ✓ | v0 |
| `LUMEN_OAUTH_JWKS_URL` | JWKS 端点（本地验签公钥源） | `https://auth.example.com/realms/lumen/protocol/openid-connect/certs` | ✓ | v0 |
| `LUMEN_OAUTH_USERINFO_URL` | userinfo 端点（资料补齐）；**可选**，缺省由 OIDC discovery 推导 | `https://auth.example.com/realms/lumen/protocol/openid-connect/userinfo` | ✗ | v0 |
| `LUMEN_OAUTH_AUDIENCE` | 期望的 `aud` 值（资源侧验 access_token；broker 侧另用 `LUMEN_OAUTH_CLIENT_ID`） | `lumen-api` | ✓ | v0 |
| `LUMEN_OWNER_SUBJECTS` | owner 的 OAuth sub 列表（逗号分隔） | `sub-abc,sub-def` | ✓ | v0 |
| `LUMEN_LISTEN_ADDR` | HTTP/WS 监听地址（**必须 `0.0.0.0`**） | `0.0.0.0:8080` | ✓ | v0 |
| `LUMEN_DATABASE_URL` | PostgreSQL 连接串（DSN） | `postgres://lumen:***@lumen-db:5432/lumen?sslmode=disable` | ✓ | v0 |
| `LUMEN_PUBLIC_IP` | VPS 公网 IP（`SetNAT1To1IPs` 宣告） | `203.0.113.10` | ✓ | v0 |
| `LUMEN_WEBRTC_UDP_PORT` | WebRTC 媒体单 UDP 端口（与 Ports Mappings 一致） | `40000` | ✓ | v0 |
| `LUMEN_PUBLIC_WS_URL` | 对外 WS 地址（`bootstrap.ws_url`）；缺省由 Host 头推导 | `wss://chat.example.com/ws` | ✗ | v0 |
| `LUMEN_LOG_LEVEL` | 日志级别 `debug/info/warn/error`（默认 info） | `info` | ✗ | v0 |
| `LUMEN_UPDATES_DIR` | 自动更新文件目录（`GET /updates/` 静态托管根）；**可选**，缺省 `/app/updates` | `/app/updates` | ✗ | v1 |
| **登录 broker（随 broker 移入服务端而新增，[服务端 §6.1](./server-design.md#61-配置全部环境变量)/[§10](./server-design.md#10-登录-broker账户中心--桌面登录中介)）** | | | | |
| `LUMEN_OAUTH_CLIENT_ID` | confidential OIDC client id（broker 用） | `lumen-web` | ✓ | v0 |
| `LUMEN_OAUTH_CLIENT_SECRET` | confidential client_secret（**secret，仅服务端**） | `***` | ✓ | v0 |
| `LUMEN_OAUTH_AUTHORIZE_URL` / `LUMEN_OAUTH_TOKEN_URL` | IdP authorize/token 端点；**可选**，缺省 discovery 推导 | — | ✗ | v0 |
| `LUMEN_OAUTH_DESKTOP_REDIRECT_URI` | 桌面登录 IdP 回调 | `https://chat.example.com/desktop/callback` | ✓ | v0 |
| `LUMEN_OAUTH_WEB_REDIRECT_URI` | 账户中心 IdP 回调 | `https://chat.example.com/auth/callback` | ✓ | v0 |
| `LUMEN_WEB_BASE_URL` | SPA 官网源（CORS 精确白名单 + `/account` 重定向目标） | `https://example.com` | ✓ | v0 |
| `LUMEN_SESSION_ENC_KEY` / `LUMEN_REFRESH_ENC_KEY` | 两把 AES-256 密钥（base64 各 32 字节；**须彼此不同**） | `<base64 32B>` | ✓ | v0 |

> **EdgeOne 侧零 secret**：`client_secret`、两把 AES 密钥、`refresh_token` 全部只在 Go 服务端（Coolify env / Postgres）；EdgeOne Pages 纯静态、无环境 secret。IdP redirect_uri 现登记为 `chat.example.com/desktop/callback` 与 `chat.example.com/auth/callback`（原 `example.com/*`）。broker 与资源验签必须指向**同一** issuer 与 audience（access_token `aud` 含 `lumen-api`）。桌面回环 `redirect_uri` 仅允许 `http://127.0.0.1:<port>/…`，由服务端 broker 校验、不在 IdP 登记。

### 7.4 端口/IP 一致性（四处对齐）

| 一致项 | 出现位置 |
|--------|----------|
| WebRTC UDP 端口（如 `40000`） | Dockerfile `EXPOSE 40000/udp` = Coolify Ports Mappings = env `LUMEN_WEBRTC_UDP_PORT` = 云安全组放行 |
| HTTP/WS 端口（如 `8080`） | Dockerfile `EXPOSE 8080` = Coolify Ports Exposes = env `LUMEN_LISTEN_ADDR` |
| 公网 IP | env `LUMEN_PUBLIC_IP`（`SetNAT1To1IPs`）= VPS 实际公网 IP |
| issuer/audience | 服务端 `LUMEN_OAUTH_ISSUER`/`LUMEN_OAUTH_AUDIENCE`（broker 与资源验签共用）= OAuth2 服务器实际值（access_token `aud` 含 `lumen-api`） |
| 官网 Base URL | 服务端 `LUMEN_WEB_BASE_URL`（`https://example.com`，CORS 白名单）= 官网 EdgeOne Pages 域名 = SPA 部署源 |
| IdP redirect_uri | `LUMEN_OAUTH_DESKTOP_REDIRECT_URI`=`chat.example.com/desktop/callback` + `LUMEN_OAUTH_WEB_REDIRECT_URI`=`chat.example.com/auth/callback` = IdP 登记的两个回调（均在服务端域） |

### 7.5 防火墙与运维要点

- **云安全组**（优先于主机 UFW，Docker iptables 会绕过 UFW）：放行入站 `443/tcp`（WSS/HTTPS）+ `40000/udp`（WebRTC 媒体）。
- 容器内**不**自管 TLS（Traefik 终结）；容器非 root 运行（Dockerfile `USER app`）。
- 结构化日志 `log/slog`（JSON），脱敏（不打 token/JWKS 内容）；改 env 后须重新部署生效。
- 自动更新文件（`latest.json` + NSIS 安装包 + ed25519 签名）由服务端 Go 进程 `GET /updates/`（公开、免鉴权）静态托管，对外 `https://chat.example.com/updates/latest.json`（同域复用 Traefik 证书）；文件目录由 `LUMEN_UPDATES_DIR`（默认 `/app/updates`）指定，经 Coolify Persistent Storage 持久化（[客户端 §4.3](./client-design.md#43-与-coolify-托管更新文件的衔接) / [服务端 §7.7](./server-design.md#77-自动更新文件托管)）。
- **更新托管核对项**：`/updates/latest.json` 可经 `https://chat.example.com/updates/` 访问，且 `latest.json` 响应头为 `Cache-Control: no-cache` + `ETag`（安装包文件名含版本号，可长缓存）。

### 7.6 官网部署（EdgeOne Pages，纯静态）+ 登录 broker（服务端）

**架构变更（权威）**：官网现为**纯静态站**，部署于 EdgeOne Pages（`example.com`），只发布构建后的 React SPA（`website/dist/`）；**无 Pages Functions、无 EdgeOne KV、无任何 secret**。登录中介与账户中心的**服务端逻辑全部移入 Go 服务端 broker**（`chat.example.com`，[服务端 §10](./server-design.md#10-登录-broker账户中心--桌面登录中介)）。

| EdgeOne 资源 | 配置 | 作用 |
|--------------|------|------|
| Pages（**纯静态**） | React + TailwindCSS 构建产物（`website/dist/`） | 营销/下载页 + 账户中心 UI（SPA） |
| `edgeone.json` | **仅** build 配置（buildCommand/installCommand/outputDirectory/nodeVersion）+ SPA rewrites（`/account`、`/download`、`/help` 等 → `/index.html`） | 构建与 SPA 前端路由回退；不含任何 Functions/KV/secret |

> broker 状态（原 KV `HANDOFF`/`SESSIONS`）现落 Go 服务端 Postgres 两表 `broker_states`/`desktop_sessions`（[服务端 §10.3](./server-design.md#103-store-broker-两表--janitor)）；`client_secret` 与两把 AES 密钥走 Coolify env（[§7.3](#73-环境变量清单汇总coolify-注入启动-fail-fast)），EdgeOne 侧零 secret。

**域名分工**：

| 域名 | 承载 | 部署 |
|------|------|------|
| `https://example.com` | 官网**静态 SPA**（营销/下载 + 账户中心 UI） | EdgeOne Pages（纯静态，无 Functions/KV/secret） |
| `https://chat.example.com` | Lumen API/WS + **登录 broker 9 端点** + 自动更新静态托管 | Go 服务端（Coolify + Traefik）=资源服务器 + broker |

**跨源关系**：`example.com`（SPA）与 `chat.example.com`（broker）**同 SITE、跨 ORIGIN**。会话 cookie 由 broker 以 `SameSite=Lax + HttpOnly + Secure + Path=/ + HOST-ONLY（无 Domain）` 下发；服务端仅对精确 `LUMEN_WEB_BASE_URL` 源发 CORS（带 credentials，永不用 `*`）；SPA XHR 用 `credentials:'include'`，`/auth/login`、`/auth/callback` 为顶层导航（[服务端 §10.5](./server-design.md#105-账户中心-cookie同-site-跨源)/[§10.6](./server-design.md#106-单点-cors-中间件)）。

**IdP 回调登记**：在外部 OAuth2/OIDC 服务器登记**服务端域**的两个回调（`https://chat.example.com/desktop/callback`、`https://chat.example.com/auth/callback`，均由 `LUMEN_OAUTH_*_REDIRECT_URI` 配置）；broker 桌面流程请求 scope `openid profile email offline_access` 且令 access_token `aud` 含 `lumen-api`，账户中心流程请求 `openid profile email`（无 offline_access/无 aud）；桌面回环 `redirect_uri` 仅允许 `http://127.0.0.1:<port>/...`，由服务端 broker 校验，不在 IdP 登记。

---

## 8. 风险与开放项

以下为访谈中已确认、留作记录的风险与开放项（均已有应对或明确接受）。

| # | 风险/开放项 | 影响 | 应对/现状 | 归属 |
|---|------------|------|-----------|------|
| 1 | Coolify Ports Mappings 可能拒绝 `/udp` 后缀 | WebRTC 媒体口无法映射 | 回退 Docker Compose 部署类型写 `"40000:40000/udp"` | v0 |
| 2 | Ports Mappings 部署失去 Rolling Updates | 重部署短暂中断，语音重连 | 已接受（小规模开黑场景） | v0 |
| 3 | UDPMux 仅 host 候选、无 STUN/TURN | 极端对称 NAT 客户端可能连不上 | 服务端公网可达 + `SetNAT1To1IPs` host 候选，本场景足够；若现网失败再评估 TURN | v0 |
| 4 | 语音在线成员纯内存态，进程重启清空 | 重启后用户需重新 `join_channel` | 已接受；频道定义本身持久化，重连 `bootstrap.voice_states` 为空属预期 | v0 |
| 5 | access_token 缺 name/picture claim | 资料同步需额外 userinfo 调用 | userinfo 兜底补齐（短超时 + 失败降级不阻断登录） | v0 |
| 6 | 全屏独占游戏下默认 PTT（`RegisterHotKey`）可能失效 | 游戏中按不到说话键 | 设置里切「全屏游戏兼容模式」走低层 `WH_KEYBOARD_LL` 钩子；提示以管理员运行 | v1 |
| 7 | 托盘/全屏 PTT 需 CGO | 构建复杂度上升（mingw/gcc） | 已接受引入 CGO 以换取托盘与全屏 PTT | v1 |
| 8 | 自动更新替换运行中 exe | 更新失败可能损坏安装 | NSIS `taskkill` 杀进程树后覆盖；校验顺序 SHA256→ed25519 任一失败即中止 | v1 |
| 9 | Chrome/WebView2 不拉流 bug（无 sink 不解码） | 远端音频不出声 | 每远端 track 挂静音 sink audio 元素驱动拉流 | v1 |
| 10 | 慢客户端 WS 发送 channel 堆积 | 内存膨胀 | send channel 满即断连清理 | v0 |
| 11 | v2 E2E 依赖 `RTCRtpScriptTransform` | webview 版本不支持则 E2E 不可用 | v2 上线前确认目标 WebView2 版本支持 | v2 |
| 12 | RNNoise worklet 初始化失败 | 降噪不可用 | 降级为 `source → gate → dest` 不降噪，不阻断通话 | v1 |
| 13 | keyfunc v3.0.0–v3.3.7 为撤回版本 | 引入即可能有缺陷 | 锁定 ≥ v3.5.0（推荐 v3.8.0） | v0 |
| 14 | pion/webrtc < v4.2.5 含 CVE-2026-26014 | 安全漏洞 | 锁定 ≥ v4.2.5 | v0 |
| 15 | 后续 backlog（未读/@提及/编辑删除/附件/桌面 toast/亮色主题/任命管理员） | 功能缺口 | 明确推迟；本期不做，预留扩展位（如设置项/主题变量） | backlog |
| 16 | 运行期 DB 抖动（PG 重启/网络）healthz 无感知 | 抖动期间 REST/WS store 操作暂时失败 | 已接受简化：pgx 连接池在 PG 恢复后自动重连自愈，不引入就绪探针/重启（小规模开黑） | v0 |

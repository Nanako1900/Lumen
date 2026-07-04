# Lumen 官网与 Web 中介登录设计

> 文档版本: 2.0
> 状态: 详细设计（authoritative for 官网 + Web 中介登录）
> 适用范围: Lumen 官网（React + TailwindCSS，**EdgeOne Pages 纯静态**）与桌面端「委托 Go 服务端 broker 登录」的 Web 中介
> 配套设计: [总览](./00-overview.md)、[协议契约](./protocol-design.md)、[服务端设计](./server-design.md)、[客户端设计](./client-design.md)
> 依据共享契约: Web-Auth 共享契约 `[v0]`（5 份文档严格一致；接口契约的唯一权威仍是 [`protocol-design.md`](./protocol-design.md)，本文与之冲突以契约为准）

**版本归属约定**（贯穿全文）：

| 标记 | 含义 |
|------|------|
| `[v0]` | 官网最小闭环：营销首页 + 下载 + 账户中心登录 + 桌面 Web 中介登录（回环 handoff）+ 刷新/登出 |

> 本文所有内容均为 `[v0]`。官网与 Web 中介登录整体一次性落地，无 v1/v2 分级。

> **架构基线（v2.0，唯一事实源）**：EdgeOne Pages **纯静态**——只托管构建后的 React SPA（`website/dist/`），**无 Pages Functions、无 EdgeOne KV、无任何 secret**。所有 broker 逻辑（`/desktop/*`、`/api/desktop/*`、`/auth/*`、`/api/me`）移入 **Go 服务端（`chat.example.com`）**，broker 与资源服务器是**同一 Go 二进制**（broker 落于 commit `82f344e`，见 `internal/broker` + `internal/secure` + store broker 表 + `cmd/lumen-server/main.go`）。`client_secret` 与 `refresh_token` **仅在服务端**（`refresh_token` 加密存 Postgres）。桌面 handoff 线契约**逐字节保留**，仅 host 由 `example.com` 改为 `chat.example.com`。

---

## 目录

1. [概述与定位](#1-概述与定位)
2. [技术栈与部署](#2-技术栈与部署)
3. [域名与路由](#3-域名与路由)
4. [页面结构](#4-页面结构)
5. [Web 中介登录（桌面，服务端 broker）](#5-web-中介登录桌面服务端-broker)
6. [网页自身登录（账户中心）](#6-网页自身登录账户中心)
7. [与既有设计的衔接](#7-与既有设计的衔接)
8. [安全](#8-安全)
9. [配置（环境变量/Secrets）](#9-配置环境变量secrets)
10. [v0 归属与验收](#10-v0-归属与验收)

---

## 1. 概述与定位

Lumen 官网 `https://example.com` 是一个**纯静态营销站**（EdgeOne Pages 仅托管 `website/dist/` 的 React SPA），本身**不含任何服务端逻辑**。身份中介（broker）由 **Go 服务端（`chat.example.com`）内置**承担。整体职责分为两类：

1. **面向人的官网（纯静态 SPA，在 `example.com`）**：营销首页（Hero + 下载 CTA）、客户端下载页、账户中心（登录后查看 OIDC 资料 + 下载客户端 + 退出）、帮助/隐私/服务条款/关于等静态内容页。
   - **不做网页聊天、不做网页语音**——所有聊天/语音能力只在 Windows 桌面客户端内。
   - 账户中心**不调用 Lumen 资源 API**（`/api/v1/*`），仅**跨源**调用 Go 服务端 broker（`chat.example.com` 的 `/auth/*`、`/api/me`）展示来自 OIDC 的资料（头像/昵称）并提供下载与退出。
2. **面向桌面客户端与账户中心的 Web 中介登录（Go 服务端 broker，在 `chat.example.com`）**：Go 服务端是 **confidential OIDC client**，代替桌面客户端与账户中心完成与 IdP 的 OAuth2/OIDC（Authorization Code + PKCE）交互；桌面客户端不再内置 IdP `issuer/client_id/scope`、不再自行对 IdP 跑 PKCE，改为「委托服务端 broker 登录」。
   - `client_secret` 仅存在于 Go 服务端的环境变量（`LUMEN_OAUTH_CLIENT_SECRET`）中，**绝不下发到桌面/前端**。
   - `refresh_token` **永不落到桌面**——存于服务端 **Postgres**（broker 会话表，以 `LUMEN_REFRESH_ENC_KEY` **静态加密**）；桌面只持有不透明高熵 `desktop_session_id`（存 Windows 凭据库 DPAPI），`access_token` 仅在桌面内存。

### 1.1 为什么把 broker 放进 Go 服务端（相对旧「官网 Worker」的变化）

| 维度 | 旧设计（官网 Cloudflare Worker 中介） | 本设计（Go 服务端 broker） |
|------|------------------------|--------------------|
| broker 运行位置 | 官网 Pages Functions（`example.com`） | **Go 服务端**（`chat.example.com`，同一二进制） |
| 官网 `example.com` | 静态 + Functions + KV + secret | **纯静态**（仅 `website/dist/`，无 Functions/KV/secret） |
| `client_secret` | Cloudflare Worker 加密环境变量 | **Go 服务端环境变量**（`LUMEN_OAUTH_CLIENT_SECRET`） |
| `refresh_token` 落点 | 官网 KV `SESSIONS` | **服务端 Postgres**（broker 会话表，静态加密） |
| 桌面持有的凭据 | `desktop_session_id`（DPAPI）+ access_token（仅内存） | **同（不变）** |
| 桌面 handoff 线契约 | `example.com/desktop/*`、`/api/desktop/*` | **逐字节保留，仅 host → `chat.example.com`** |
| 账户中心 → broker | 同源（Functions 与 SPA 同在 `example.com`） | **跨源**（`example.com` SPA → `chat.example.com` broker），须 CORS+credentials |
| 资源服务器 JWKS 验签 | 不变 | **不变**（与 broker 同进程，但逻辑解耦） |

> Go 服务端资源服务器的鉴权契约（[协议 §2.3](./protocol-design.md#23-服务端验签jwks-本地验签)、[服务端 §2.1](./server-design.md#21-jwks-本地验签keyfunc-v3--golang-jwt-v5)）一字不改：仍只用 IdP 的 JWKS 本地验 `access_token`（JWT，验 `iss`/`aud`/`exp`）。**变化点**：因账户中心 SPA 从 `example.com` **跨源**调 broker（`chat.example.com`），Go 服务端**必须新增 CORS**（精确回显 `example.com` 源 + `Allow-Credentials`），详见 [§6.3](#63-跨源与-cors账户中心) 与 [协议 §2.8](./protocol-design.md#28-web-跨源与-cors-约定账户中心)。

---

## 2. 技术栈与部署

### 2.1 技术栈

**官网（`example.com`，纯静态）**

| 层 | 选型 | 说明 |
|----|------|------|
| 前端框架 | **React** | SPA（可选 SSG 预渲染静态营销页） |
| 样式 | **TailwindCSS** | 深色为主（与桌面客户端视觉一致） |
| 构建 | **Vite** | 输出静态资源到 `website/dist/` |
| 静态托管 | **EdgeOne Pages（STATIC-ONLY）** | 仅托管 `website/dist/`；全球 CDN，自动 HTTPS；**无 Pages Functions、无 KV、无 secret** |

**broker + 资源服务器（`chat.example.com`，同一 Go 二进制）**

| 层 | 选型 | 说明 |
|----|------|------|
| broker 逻辑 | **Go 服务端** | `internal/broker`（端点）+ `internal/secure`（会话/refresh_token 加密）+ store broker 表；端点 `/desktop/*`、`/api/desktop/*`、`/auth/*`、`/api/me` |
| 状态存储 | **PostgreSQL** | broker 表：handoff（一次性短期）+ desktop 会话（长期，含加密 `refresh_token`）+ 网页会话 |
| 机密 | **Go 服务端环境变量（Coolify 注入）** | `LUMEN_OAUTH_CLIENT_SECRET`、`LUMEN_SESSION_ENC_KEY`、`LUMEN_REFRESH_ENC_KEY`；绝不进仓库、绝不下发前端/桌面 |

### 2.2 部署形态（EdgeOne 纯静态 + Go 服务端 broker）

```
┌──── EdgeOne Pages（example.com，STATIC-ONLY）────┐
│   静态资源（website/dist/）                       │
│   ┌──────────────────────────────────────────┐  │
│   │ React SPA：首页/下载/账户中心               │  │
│   │ 帮助/隐私/条款/关于                          │  │
│   │ （无 Functions、无 KV、无 secret）           │  │
│   └──────────────────┬───────────────────────┘  │
└──────────────────────┼──────────────────────────┘
                       │ 账户中心：跨源 XHR（credentials:'include'）
                       │ +  顶层导航（/auth/login, /auth/callback）
                       ▼
┌──────────── Go 服务端（chat.example.com，Coolify）────────────┐
│  同一 Go 二进制                                                │
│  ┌───────────────── broker（登录中介）─────────────────┐      │
│  │ /desktop/login  /desktop/callback                   │      │
│  │ /api/desktop/exchange|refresh|logout                │      │
│  │ /auth/login  /auth/callback  /auth/logout  /api/me  │      │
│  │ confidential OIDC client（Auth Code + PKCE）         │      │
│  └──────────────┬───────────────────┬──────────────────┘      │
│  ┌──────────────▼──┐  ┌─────────────▼──────────────┐          │
│  │ 资源服务器        │  │ PostgreSQL                  │          │
│  │ REST /api/v1/*   │  │ broker 表: handoff / 会话    │          │
│  │ WSS /ws  +  SFU  │  │ (refresh_token 加密存储)     │          │
│  │ JWKS 本地验签     │  │ + users/channels/messages   │          │
│  └──────────────────┘  └─────────────────────────────┘          │
│  env: LUMEN_OAUTH_CLIENT_SECRET / _SESSION_ENC_KEY / _REFRESH_ENC_KEY │
└──────────────────────┬───────────────────────────────────────┘
        │ confidential OIDC client（Auth Code + PKCE，token 交换在 Go 服务端）
        ▼
┌──────────── IdP（auth.example.com，外部 OAuth2/OIDC，不变） ────────────┐
│   /authorize  /token  /userinfo  /jwks  /logout（issuer 发现）            │
└──────────────────────────────────────────────────────────────────────────┘

   桌面客户端                         Go 服务端（chat.example.com）
   ┌──────────────┐  access_token  ┌──────────────────────────────────────┐
   │ Wails Windows│ ─────────────► │ REST /api/v1/*  +  WSS /ws  +  SFU     │
   │ 回环 127.0.0.1│  Bearer JWT    │ JWKS 本地验签（iss/aud=lumen-api/exp） │
   │ handoff:      │ ─────────────► │ broker /desktop/* /api/desktop/*      │
   │ chat.example.com                └──────────────────────────────────────┘
   └──────────────┘
```

### 2.3 构建与发布要点

- **官网（EdgeOne Pages，纯静态）**：
  - 构建命令：`npm ci && npm run build`
  - 构建输出目录：`website/dist/`
  - **无 Functions 目录**：仓库不再包含 `functions/`；EdgeOne 只发布静态产物。
  - **SPA 回退**：`edgeone.json` **仅保留** build config + SPA rewrites——将未命中静态文件的页面路由回退到 `/index.html`（SPA 客户端路由）。不再有任何 Functions 路由需要规避。
  - **无 secret / 无 KV 绑定**：EdgeOne 侧不注入任何 secret、不绑定任何 KV；前端构建产物**不含**任何 secret。
- **broker（Go 服务端，Coolify）**：
  - `client_secret`、`LUMEN_SESSION_ENC_KEY`、`LUMEN_REFRESH_ENC_KEY` 等通过 **Coolify 环境变量**注入；仅 Go 进程运行时可读，绝不进仓库/前端产物/桌面。
  - broker 状态存 **PostgreSQL**（复用服务端已有连接池；broker 表随迁移创建）。
  - **预览 vs 生产**：预览环境用独立数据库/schema 与独立 IdP client（或独立 redirect 白名单），避免污染生产会话。

> **`edgeone.json` 唯一职责**：build config（构建命令/输出目录）+ SPA rewrites（`/* → /index.html`）。**不含** Functions、KV、secret、代理规则。

---

## 3. 域名与路由

### 3.1 域名职责划分

| 域名 | 归属 | 内容 | TLS |
|------|------|------|-----|
| `https://example.com` | **EdgeOne Pages（STATIC-ONLY）** | 官网静态 SPA（`website/dist/`）——**无任何服务端逻辑** | EdgeOne 自动 |
| `https://chat.example.com` | Go 服务端（Coolify，单一后端） | REST `/api/v1/*`、WSS `/ws`、SFU、`/updates/` 静态托管、**broker 端点 `/desktop/*` `/api/desktop/*` `/auth/*` `/api/me`** | Traefik 自动 |
| `https://auth.example.com/realms/lumen` | 外部 IdP（OAuth2/OIDC） | `/authorize`、`/token`、`/userinfo`、`/jwks`、`/logout` | IdP 侧（不变） |

> `example.com` 与 `chat.example.com` **同站（same-site，同一可注册域 `example.com`）** 但 **跨源（cross-origin，host 不同）**——这是账户中心 CORS + host-only cookie 契约的前提（[§6.3](#63-跨源与-cors账户中心)）。

### 3.2 路由总表

**官网页面路由（EdgeOne 纯静态，React Router，SPA rewrites）**

| 路径 | 页面 | 鉴权 |
|------|------|------|
| `/` | 首页（Hero + 下载 CTA） | 公开 |
| `/download` | 下载页（直接 fetch `chat.example.com/updates/latest.json`） | 公开 |
| `/account` | 账户中心（登录后看资料/下载/退出） | 需网页会话（未登录 → 触发 broker `/auth/login`） |
| `/help` | 帮助 | 公开 |
| `/privacy` | 隐私政策 | 公开 |
| `/terms` | 服务条款 | 公开 |
| `/about` | 关于 | 公开 |

> 上述均为 SPA 客户端路由；EdgeOne `edgeone.json` 的 SPA rewrites 把未命中静态文件的路径回退到 `/index.html`。**官网无任何服务端端点**。

**broker 端点路由（Go 服务端 `chat.example.com`）**

| 方法 | 路径 | 用途 | 消费方 |
|------|------|------|--------|
| GET | `/desktop/login` | 起桌面登录：校验回环 redirect_uri，暂存上下文，302 到 IdP | 桌面（系统浏览器） |
| GET | `/desktop/callback` | IdP 回调：服务端换 token，生成 handoff_code，302 回回环 | IdP → 浏览器 |
| POST | `/api/desktop/exchange` | 用 handoff_code+verifier 换 `access_token` + `desktop_session_id` | 桌面 |
| POST | `/api/desktop/refresh` | 用 `desktop_session_id` 刷新 `access_token` | 桌面 |
| POST | `/api/desktop/logout` | 注销 `desktop_session_id`（删 broker 会话行） | 桌面 |
| GET | `/auth/login` | 账户中心登录：OIDC（PKCE）发起（顶层导航） | 浏览器（顶层导航） |
| GET | `/auth/callback` | 账户中心 OIDC 回调：换 token，设 host-only Lax cookie 会话 | IdP → 浏览器 |
| POST | `/auth/logout` | 清网页会话 | 浏览器 SPA（跨源 XHR，credentials） |
| GET | `/api/me` | 账户中心读当前网页会话资料（`{display_name, avatar_url}`）；未登录 → `401` | 浏览器 SPA（跨源 XHR，credentials） |

> 全部 9 个 broker 端点均在 **Go 服务端 `chat.example.com`**（旧版位于官网 Worker `example.com`）。区分：`/api/me`（**cookie 鉴权**的账户中心资料）与资源服务器的 `/api/v1/me`（**Bearer 鉴权**）是**两个不同端点**，前者属 broker、后者属资源服务器。**旧 `/api/download/latest` 代理已删除**——下载页直接 fetch `chat.example.com/updates/latest.json`（见 [§4.2](#42-下载页与自动更新文件的复用download)）。

### 3.3 IdP 须登记的回调 URL

> confidential client 在 IdP 须登记以下两个 redirect_uri（均在 **`chat.example.com`**，HTTPS）：

| 回调 | 用途 |
|------|------|
| `https://chat.example.com/desktop/callback` | 桌面 Web 中介登录 |
| `https://chat.example.com/auth/callback` | 账户中心网页登录 |

> **redirect_uri 迁移**：由旧的 `example.com/desktop/callback` 与 `example.com/auth/callback` 移到 `chat.example.com/*`（broker 现所在 host）。注意：桌面端的 `http://127.0.0.1:<port>/cb` **不是** IdP 的 redirect_uri——它只是 broker 的 handoff 回环地址；IdP 永远只回到 `chat.example.com/desktop/callback`。

---

## 4. 页面结构

### 4.1 各页面内容

| 页面 | 关键内容 |
|------|----------|
| **首页 `/`** | 精简 Hero（产品一句话定位「类 Discord 的轻量开黑语音」）+ 主 CTA「下载 Windows 客户端」+ 次 CTA「登录账户中心」；深色背景、简洁单屏。 |
| **下载页 `/download`** | 下载 `Setup.exe`（指向最新版安装包）+ 版本号/更新日期；数据源为服务端自动更新清单 `https://chat.example.com/updates/latest.json`，**复用桌面自动更新文件**（见 §4.2）。 |
| **账户中心 `/account`** | 登录后展示 OIDC 资料（头像 `avatar_url` + 昵称 `display_name`）+「下载客户端」入口 +「退出登录」按钮。**不调用 Lumen 资源 API（`/api/v1/*`）**；仅跨源调用 broker（`/api/me`、`/auth/logout`）。 |
| **帮助 `/help`** | 常见问题、登录/连接排障、PTT 说明等静态内容。 |
| **隐私 `/privacy`** | 隐私政策（含 refresh_token 留存于 Go 服务端 Postgres、加密存储的说明）。 |
| **条款 `/terms`** | 服务条款。 |
| **关于 `/about`** | 关于 Lumen、版本/许可信息。 |

### 4.2 下载页与自动更新文件的复用（`/download`）

下载页不维护独立的安装包托管，而是**复用服务端的自动更新文件**（[客户端 §4.3](./client-design.md#43-与-coolify-托管更新文件的衔接) / [服务端 §7.7](./server-design.md#77-自动更新文件托管)）：

- **清单**：`GET https://chat.example.com/updates/latest.json` —— 含 `version` 与 `platforms["windows/amd64"].url`（指向 `https://chat.example.com/updates/Lumen-Setup-x.y.z.exe`）。
- **下载页行为**：SPA 在加载时**直接 `fetch`** `https://chat.example.com/updates/latest.json`（公开、免鉴权），取出 `version` 与安装包 URL，渲染下载按钮直接指向 `*.exe`。**不经任何代理**——旧 `/api/download/latest` Worker 代理已删除。
- **下载页不缓存版本号**：与 `latest.json` 的 `Cache-Control: no-cache` + ETag 语义一致，每次进入下载页都读取最新清单。

> **CORS 约定（下载清单）**：官网纯静态、无 Worker，故 SPA 从 `example.com` **跨源**直接 fetch `chat.example.com/updates/latest.json`。Go 服务端须让 `/updates/` 的静态响应带上允许该 GET 跨源读取的 `Access-Control-Allow-Origin`（精确回显 `example.com` 或对公开只读清单放宽为 `*`；该端点无凭据，故允许 `*`）。这与账户中心 broker 的带凭据 CORS（[§6.3](#63-跨源与-cors账户中心)）是**两套独立策略**：`/updates/` 只读无凭据、可宽松；broker 带 cookie、必须精确源 + `Allow-Credentials`。

### 4.3 目录结构

**官网（`website/`，纯静态 React——无 `functions/`）**

```
website/
├── src/                            # React 前端（静态，构建到 dist/）
│   ├── main.tsx
│   ├── App.tsx                     # 路由装配（React Router）
│   ├── routes/
│   │   ├── Home.tsx                # /
│   │   ├── Download.tsx            # /download（fetch chat.example.com/updates/latest.json）
│   │   ├── Account.tsx             # /account（跨源调 broker /api/me、/auth/logout）
│   │   ├── Help.tsx                # /help
│   │   ├── Privacy.tsx             # /privacy
│   │   ├── Terms.tsx               # /terms
│   │   └── About.tsx               # /about
│   ├── components/
│   │   ├── Layout.tsx              # 顶栏/页脚/深色容器
│   │   ├── Hero.tsx
│   │   ├── DownloadButton.tsx
│   │   ├── ProfileCard.tsx         # 账户中心资料卡（头像+昵称）
│   │   └── Nav.tsx
│   └── lib/
│       ├── api.ts                  # 跨源 fetch broker（credentials:'include'）；顶层导航 /auth/login
│       └── format.ts
├── public/
├── index.html
├── tailwind.config.js
├── vite.config.ts
├── edgeone.json                    # 仅 build config + SPA rewrites（无 Functions/KV/secret）
└── package.json
```

> **无 `functions/`、无 `wrangler.toml`、无 `_redirects`**：所有 broker 逻辑移入 Go 服务端（下）。SPA 回退由 `edgeone.json` 的 rewrites 承担。前端 `lib/api.ts` 对 broker 的 XHR 走**跨源 + `credentials:'include'`**；`/auth/login`、`/auth/callback` 走 `window.location` **顶层导航**（非 XHR）。

**broker（Go 服务端仓库，示意——权威以服务端设计为准）**

```
cmd/lumen-server/main.go            # 装配 broker 路由 + 资源服务器路由（同一进程）
internal/broker/                    # broker 端点（commit 82f344e 落地）
│   ├── desktop.go                  # GET /desktop/login|callback；POST /api/desktop/exchange|refresh|logout
│   ├── web.go                      # GET /auth/login|callback；POST /auth/logout；GET /api/me
│   ├── oidc.go                     # discovery / authorize URL / token 交换 / refresh / revoke
│   ├── pkce.go                     # S256(verifier) / 高熵随机（crypto/rand）
│   └── cors.go                     # 账户中心跨源 CORS（精确回显 WEB_BASE_URL + credentials）
internal/secure/                    # 加密（commit 82f344e 落地）
│   ├── session.go                  # 网页会话 cookie 签发/校验（HttpOnly+Secure+SameSite=Lax+host-only）
│   └── refresh.go                  # refresh_token 静态加密/解密（LUMEN_REFRESH_ENC_KEY）
internal/store/                     # broker 表读写（handoff/desktop 会话/网页会话）+ 既有 users/channels/messages
```

### 4.4 Tailwind 约定（Aurora Indigo · 浅色玻璃拟态）

> 官网视觉以设计稿 `外观设计稿/Lumen 网页端.dc.html`（1a–1f）为准，采用 **Aurora Indigo** 浅色系（靛蓝
> #5b6ef5 + 极光青 #29d4b4 + 玻璃质感 + 发光光点），单一浅色主题，不提供暗色切换（旧「深色为默认」方案已弃用）。

- **主题**：浅色为默认且唯一（`color-scheme: light`；固定 `bg-aurora` 渐变底图层）。
- **调色板**（`tailwind.config.js` 扩展语义色）：
  - 品牌：`brand`（#5b6ef5，`deep` #3e44c4）/ 极光：`aurora`（#29d4b4，`deep` #17b47e）
  - 文本：`ink`（#1c1f2e）/ `ink-muted`（#565c74）/ `ink-faint`（#939ab0）
  - 表面：玻璃卡 `bg-white/60 + backdrop-blur + shadow-ringcard`；主按钮 `bg-brand text-white shadow-cta`；页面 `bg-aurora`
  - 语义：`danger` #ec4c56 / `warn` #e3a015；品牌光球 `bg-orb`
- **排版**：`font-sans`（系统字体栈，含 PingFang SC）；标题 `font-extrabold tracking-tight`；正文 `leading-relaxed`。
- **布局**：容器 `max-w-content(72rem) mx-auto px-5 md:px-10`；卡片 `rounded-[18px]`；响应式 `sm:/md:/lg:` 断点。
- **动效**：说话 `animate-eq`、光球呼吸 `animate-orbpulse`、终端光标 `animate-caret` 等；统一 `@media (prefers-reduced-motion)` 降级。
- **可访问性**：交互元素 `focus-visible:ring-2 ring-brand`；装饰元素 `aria-hidden`；对比度满足 WCAG AA。

---

## 5. Web 中介登录（桌面，服务端 broker）

本节是桌面「委托 Go 服务端 broker 登录」的完整契约。所有 broker 端点均在 **`https://chat.example.com`**（Go 服务端）。命名与**请求/响应体形状严格按共享契约保留（逐字节）——旧版位于官网 Worker `example.com`，本次改造仅把 host 由 `example.com` 改为 `chat.example.com`**。

> **线契约保留承诺**：`/desktop/login`、`/desktop/callback`、`/api/desktop/exchange`、`/api/desktop/refresh`、`/api/desktop/logout` 的入参与返回体字段**与旧契约一字不差**。**唯一变化是 host**。因此未来 Windows 客户端的改动仅为**基址 base URL**（`LUMEN_WEB_BASE_URL`：`https://example.com` → `https://chat.example.com`）。

### 5.1 端点契约（逐个）

#### 端点 1 — `GET /desktop/login` `[v0]`

发起桌面登录：校验回环 redirect_uri，暂存上下文，302 到 IdP。

| 项 | 内容 |
|----|------|
| 入参（query） | `redirect_uri`（必填，必须 `http://127.0.0.1:<port>/...` 回环）、`state`（必填，桌面生成的不透明随机串）、`challenge`（必填，`S256(handoff_verifier)`，base64url 无填充） |
| 处理 | 1) 校验 `redirect_uri` 为 `http://127.0.0.1:<port>/...` 回环（host 必须 `127.0.0.1`，scheme `http`）；2) 生成服务端自建的 **OIDC PKCE**（`oidc_verifier` + `oidc_challenge=S256(oidc_verifier)`）与 OIDC `state'`；3) 暂存 `{state, challenge, redirect_uri, oidc_verifier, oidc_state}`（broker handoff 表 `ctx:*`，短 TTL）；4) 302 到 IdP `/authorize`（`response_type=code`、`code_challenge=oidc_challenge`、`code_challenge_method=S256`、`scope=openid profile email offline_access`、**依 IdP 约定**令 `access_token` 的 `aud` 含 `lumen-api`（Keycloak: audience mapper/client scope；Auth0/Logto: `audience`/resource 参数——见 [§7.2](#72-idp-侧登记要求)）、`redirect_uri=https://chat.example.com/desktop/callback`、`state=oidc_state`） |
| 成功响应 | `302 Found` → IdP 授权端点 |
| 错误 | `400`（缺参/`redirect_uri` 非回环/`challenge` 非法 base64url）；JSON 错误信封 `{ "error": { "code": "BAD_REQUEST", "message": "..." } }` |

> 安全：`redirect_uri` 仅允许 `127.0.0.1` 回环（不接受 `localhost` 主机名以避免 DNS 重绑定，端口任意）；`access_token` 绝不进任何 URL。

#### 端点 2 — `GET /desktop/callback` `[v0]`

IdP 回调：服务端用 `client_secret` 换 token，生成一次性 handoff_code，302 回回环。

| 项 | 内容 |
|----|------|
| 入参（query） | `code`（IdP 授权码）、`state`（应等于暂存的 `oidc_state`） |
| 处理 | 1) 取回暂存上下文，校验 `state==oidc_state`；2) 服务端用 `client_secret` + `oidc_verifier` 向 IdP `/token` 换 `{access_token, refresh_token, id_token, expires_in}`（`grant_type=authorization_code`）；3) 解析 `id_token`/`access_token` 得 `sub`，必要时 `/userinfo` 兜底取 `display_name`/`avatar_url`；4) 生成一次性 `handoff_code`（高熵随机）；5) 写 broker handoff 表：`handoff_code → {access_token, expires_in, refresh_token(加密), sub, bound_challenge}`（`bound_challenge` = 端点1 暂存的桌面 `challenge`），TTL≈120s；6) 302 到 `redirect_uri?handoff_code=<code>&state=<原桌面 state>` |
| 成功响应 | `302 Found` → `http://127.0.0.1:<port>/cb?handoff_code=...&state=...` |
| 错误 | `400`（state 不匹配/无暂存上下文）、`502`（IdP token 交换失败）；失败时 302 回 `redirect_uri?error=<code>&state=<state>`（便于桌面回环页展示） |

> 安全红线：`access_token` **绝不进 URL**，仅写入 broker handoff 表，由桌面随后用 verifier 换取。

#### 端点 3 — `POST /api/desktop/exchange` `[v0]`

用 `handoff_code` + `handoff_verifier` 换 `access_token` 与 `desktop_session_id`。

| 项 | 内容 |
|----|------|
| 入参（JSON body） | `{ "handoff_code": "string", "handoff_verifier": "string" }` |
| 处理 | 1) 读 broker handoff 表 `handoff_code`，不存在/过期 → `404`；2) 校验 `S256(handoff_verifier) == bound_challenge`，不匹配 → `400`；3) **一次性消费**（立即删除该 handoff 行，无论后续成败）；4) 生成高熵 `desktop_session_id`；5) 写 broker desktop 会话表 `desktop_session_id → {refresh_token(加密), sub, created_at}`；6) 返回 `access_token`、`expires_in`、`desktop_session_id`、`profile` |
| 成功响应 `200` | `{ "access_token": "<JWT>", "expires_in": 3600, "desktop_session_id": "<opaque>", "profile": { "display_name": "...", "avatar_url": "https://..." } }` |
| 错误 | `400`（缺参/`verifier` 不匹配 `bound_challenge`）；`404`（`handoff_code` 不存在或已消费/过期）；JSON 错误信封 |

#### 端点 4 — `POST /api/desktop/refresh` `[v0]`

用 `desktop_session_id` 刷新 `access_token`。

| 项 | 内容 |
|----|------|
| 入参（JSON body） | `{ "desktop_session_id": "string" }` |
| 处理 | 1) 读 broker desktop 会话表 `desktop_session_id`，不存在/失效 → `401` `SESSION_INVALID`；2) **解密**该会话的 `refresh_token`，服务端用 `client_secret` + 该 `refresh_token` 向 IdP `/token`（`grant_type=refresh_token`）换新 `access_token`；3) **若 IdP 轮换 refresh_token**（返回新 `refresh_token`），**加密后**更新会话行的 `refresh_token`；4) 返回新 `access_token` + `expires_in` |
| 成功响应 `200` | `{ "access_token": "<JWT>", "expires_in": 3600 }` |
| 错误 | `401`（session 不存在/失效，或 IdP 拒绝 refresh）：`{ "error": { "code": "SESSION_INVALID", "message": "session expired or revoked" } }` —— 桌面据此转重新登录 |

> **`expires_in` 约定（防刷新风暴）**：`/api/desktop/exchange` 与 `/api/desktop/refresh` 返回的 `expires_in` 必须为**正整数秒**；若 IdP 未返回则服务端回退保守默认（如 300）。桌面按 `expires_in − 60s` 安排下次刷新；收到缺失/≤0 的 `expires_in` 时立即视为过期并刷新一次，但用最小刷新间隔兜底，避免 `expires_in=0` 触发紧凑循环（见 [客户端 §2.4](./client-design.md#24-静默刷新--暴露有效-token核心)）。

#### 端点 5 — `POST /api/desktop/logout` `[v0]`

注销桌面会话。

| 项 | 内容 |
|----|------|
| 入参（JSON body） | `{ "desktop_session_id": "string" }` |
| 处理 | 1) 删 broker desktop 会话行 `desktop_session_id`；2) 可选：向 IdP token revocation 端点撤销该 `refresh_token`；3) 返回 `204`（幂等：session 不存在也回 `204`） |
| 成功响应 | `204 No Content`（无 body） |
| 错误 | `400`（缺 `desktop_session_id`） |

### 5.2 桌面登录时序（回环 handoff）

```
桌面客户端                 系统浏览器      Go 服务端 broker(chat.example.com)    IdP(auth.example.com)
  │                          │                        │                              │
  │ 1) 起 127.0.0.1:rand 回环监听                      │                              │
  │    gen handoff_verifier + state                   │                              │
  │    challenge = S256(handoff_verifier)             │                              │
  │ 2) 打开浏览器 ───────────►│                        │                              │
  │   chat.example.com/desktop/login?redirect_uri=http://127.0.0.1:port/cb          │
  │                  &state=<s>&challenge=<S256(verifier)>                           │
  │                          │ GET /desktop/login ───►│                              │
  │                          │                        │ 校验 redirect_uri 回环       │
  │                          │                        │ 暂存{state,challenge,         │
  │                          │                        │   redirect_uri,oidc_verifier} │
  │                          │◄── 302 IdP /authorize ─┤ (Auth Code+PKCE, aud=lumen-api)│
  │                          │ GET /authorize ──────────────────────────────────────►│
  │                          │            （用户在 IdP 登录/同意）                     │
  │                          │◄─ 302 chat.example.com/desktop/callback?code&state ────┤
  │                          │ GET /desktop/callback ►│                              │
  │                          │                        │ 用 client_secret+verifier     │
  │                          │                        │   POST /token ──────────────►│
  │                          │                        │◄── access+refresh+id_token ──┤
  │                          │                        │ gen handoff_code              │
  │                          │                        │ handoff表{access,refresh(加密),│
  │                          │                        │   sub,bound_challenge} TTL120s│
  │                          │◄ 302 127.0.0.1:port/cb?handoff_code&state ─────────────┤
  │ 3) 回环收到 ◄────────────┤                        │  (access_token 不进 URL)      │
  │    校验 state == 本地 state                        │                              │
  │    POST chat.example.com/api/desktop/exchange ───►│                              │
  │      {handoff_code, handoff_verifier}             │ 校验 S256(verifier)==          │
  │                          │                        │   bound_challenge             │
  │                          │                        │ 一次性消费 handoff 行         │
  │                          │                        │ gen desktop_session_id        │
  │                          │                        │ 会话表{refresh(加密),sub}     │
  │ ◄── {access_token, expires_in, desktop_session_id, profile} ─────────────────────┤
  │ 4) 存 desktop_session_id → 凭据库(DPAPI)          │                              │
  │    access_token 仅内存                             │                              │
  │                                                                                  │
  │ ====== 连 Go 服务端资源服务器（契约不变）======                                  │
  │    WSS chat.example.com/ws  +  REST /api/v1/*   （Bearer access_token JWT）       │
  │                                                                                  │
  │ ====== 刷新 ======                                                               │
  │ access_token 临期 → POST chat.example.com/api/desktop/refresh{sid} → 新 access_token│
  │                                                                                  │
  │ ====== 登出 ======                                                               │
  │ POST chat.example.com/api/desktop/logout{sid} → 清凭据库 + 关 WS + 重置 store     │
```

> 桌面 handoff 与资源服务器同在 `chat.example.com`（单一后端），但仍是逻辑解耦的两条路径：broker（`/desktop/*`、`/api/desktop/*`）负责取/刷 token；资源服务器（`/api/v1/*`、`/ws`）负责 Bearer 业务。

### 5.3 handoff 安全（challenge / verifier / 一次性 / 回环校验）

- **challenge / verifier 绑定**：桌面生成 `handoff_verifier`（高熵随机），`challenge = S256(handoff_verifier)`（base64url 无填充）。`challenge` 在 `/desktop/login` 经浏览器传到服务端并随 token 写入 handoff 行的 `bound_challenge`；桌面在 `/api/desktop/exchange` 提交 `handoff_verifier`，服务端校验 `S256(handoff_verifier)==bound_challenge`。即便 `handoff_code` 在回环 URL 泄露，攻击者无 `handoff_verifier` 亦无法换取 token。
- **一次性**：`handoff_code` 在 `/api/desktop/exchange` 第一次命中即从 handoff 表删除（无论成败），重放必失败。
- **短 TTL**：handoff 行 TTL≈120s，过期自动清除（按 `expires_at` 列 + 惰性/定时清理）。
- **回环校验**：`redirect_uri` 仅允许 `http://127.0.0.1:<port>/...`（`/desktop/login` 强制校验）；IdP redirect_uri 固定为 `https://chat.example.com/desktop/callback`，与桌面回环解耦。
- **state**：桌面侧 `state` 用于桌面校验回环回调来源；OIDC 侧 `oidc_state` 用于服务端校验 IdP 回调来源，两者独立。
- **access_token 不进 URL**：仅出现在 `/exchange`、`/refresh` 的响应体；回环 URL 只携带 `handoff_code` + `state`。

### 5.4 broker 表结构（Postgres，替代旧 KV）

> broker 状态存 Go 服务端的 PostgreSQL（旧版为 Cloudflare KV `HANDOFF`/`SESSIONS`）。`refresh_token` 落库前以 `LUMEN_REFRESH_ENC_KEY` **静态加密**（AEAD），DB 泄露也不直接暴露明文 refresh_token。列命名示意，权威以服务端迁移/store 实现为准。

**handoff 表（一次性、短期）**——对应旧 `HANDOFF` KV

```
handoff_code     TEXT PRIMARY KEY   // 高熵随机串（base64url）
access_token     TEXT               // 短暂持有，exchange 后随行删除
expires_in       INTEGER
refresh_token    BYTEA              // 加密存储（LUMEN_REFRESH_ENC_KEY）
sub              TEXT
bound_challenge  TEXT               // S256(handoff_verifier), base64url
expires_at       TIMESTAMPTZ        // ≈now()+120s；到期清理；exchange 命中后立即 DELETE（一次性）
```

**desktop 会话表（桌面长期会话）**——对应旧 `SESSIONS` KV

```
desktop_session_id  TEXT PRIMARY KEY // 高熵随机串（base64url），仅桌面 DPAPI 持有对应值
refresh_token       BYTEA            // 加密存储，绝不出服务端、绝不下发桌面
sub                 TEXT
created_at          TIMESTAMPTZ
// 无 TTL；logout 删除；refresh 被 IdP 拒绝时删除并回 SESSION_INVALID
```

**网页会话表（账户中心）**——新架构下账户中心也由服务端 broker 承载

```
web_session_id  TEXT PRIMARY KEY    // 或用加密 cookie payload 承载，二选一
sub             TEXT
display_name    TEXT
avatar_url      TEXT
created_at      TIMESTAMPTZ
// 不持久化 refresh_token（账户中心无离线刷新需求）；logout 删除
```

### 5.5 broker 伪代码骨架（Go，`internal/broker`）

> 以下为 Go 服务端 broker 风格骨架（`net/http` handler），省略错误信封细节；`oidc`/`pkce`/`secure`/`store` 来自 `internal/broker`、`internal/secure`、`internal/store`。落地见 commit `82f344e`。响应体形状与旧契约逐字节一致，**仅 host/存储实现改变**。

```go
// internal/broker/pkce.go —— S256 与高熵随机（crypto/rand + sha256）
func S256(verifier string) string {
    sum := sha256.Sum256([]byte(verifier))
    return base64.RawURLEncoding.EncodeToString(sum[:]) // base64url 无填充
}
func RandomToken(nbytes int) string {
    b := make([]byte, nbytes)
    _, _ = rand.Read(b) // crypto/rand
    return base64.RawURLEncoding.EncodeToString(b)
}

// GET /desktop/login
func (b *Broker) DesktopLogin(w http.ResponseWriter, r *http.Request) {
    q := r.URL.Query()
    redirectURI, state, challenge := q.Get("redirect_uri"), q.Get("state"), q.Get("challenge")
    // 1) 回环校验：仅允许 http://127.0.0.1:<port>/...
    ru, err := url.Parse(redirectURI)
    if err != nil || ru.Scheme != "http" || ru.Hostname() != "127.0.0.1" {
        badRequest(w, "redirect_uri must be 127.0.0.1 loopback"); return
    }
    if state == "" || !isBase64URL(challenge) { badRequest(w, "missing state/challenge"); return }
    // 2) 服务端自建 OIDC PKCE
    oidcVerifier := RandomToken(32)
    oidcChallenge := S256(oidcVerifier)
    oidcState := RandomToken(32)
    // 3) 暂存上下文（broker handoff 表 ctx:*，短 TTL）
    _ = b.store.PutHandoffCtx(r.Context(), oidcState, HandoffCtx{
        State: state, Challenge: challenge, RedirectURI: redirectURI, OIDCVerifier: oidcVerifier,
    }, 600*time.Second)
    // 4) 302 到 IdP /authorize（带 aud=lumen-api、scope offline_access）
    authorize := b.oidc.BuildAuthorizeURL(AuthorizeParams{
        CodeChallenge: oidcChallenge, State: oidcState,
        RedirectURI: b.env.DesktopRedirectURI, // https://chat.example.com/desktop/callback
        Scope: "openid profile email offline_access", Audience: b.env.Audience, // lumen-api
    })
    http.Redirect(w, r, authorize, http.StatusFound)
}

// GET /desktop/callback
func (b *Broker) DesktopCallback(w http.ResponseWriter, r *http.Request) {
    code, oidcState := r.URL.Query().Get("code"), r.URL.Query().Get("state")
    ctx, ok := b.store.TakeHandoffCtx(r.Context(), oidcState) // 取回并删除
    if code == "" || !ok { badRequest(w, "invalid state"); return }
    // 用 client_secret + oidcVerifier 向 IdP /token 换码
    tok, err := b.oidc.ExchangeAuthCode(r.Context(), code, ctx.OIDCVerifier, b.env.DesktopRedirectURI)
    if err != nil { redirectErr(w, r, ctx.RedirectURI, ctx.State, "token_exchange_failed"); return }
    sub := subjectFrom(tok.IDToken, tok.AccessToken)
    // 生成一次性 handoff_code，写 handoff 表（refresh_token 加密，绑 bound_challenge=ctx.Challenge）
    handoffCode := RandomToken(32)
    encRefresh := b.secure.EncryptRefresh(tok.RefreshToken) // LUMEN_REFRESH_ENC_KEY
    _ = b.store.PutHandoff(r.Context(), handoffCode, Handoff{
        AccessToken: tok.AccessToken, ExpiresIn: tok.ExpiresIn,
        RefreshToken: encRefresh, Sub: sub, BoundChallenge: ctx.Challenge,
    }, 120*time.Second)
    // 302 回回环：仅带 handoff_code + 原桌面 state（access_token 绝不进 URL）
    back, _ := url.Parse(ctx.RedirectURI)
    qq := back.Query(); qq.Set("handoff_code", handoffCode); qq.Set("state", ctx.State)
    back.RawQuery = qq.Encode()
    http.Redirect(w, r, back.String(), http.StatusFound)
}

// POST /api/desktop/exchange
func (b *Broker) DesktopExchange(w http.ResponseWriter, r *http.Request) {
    var in struct{ HandoffCode, HandoffVerifier string }
    if err := readJSON(r, &in); err != nil || in.HandoffCode == "" || in.HandoffVerifier == "" {
        badRequest(w, "missing fields"); return
    }
    h, ok := b.store.TakeHandoff(r.Context(), in.HandoffCode) // 一次性消费（读即删，无论后续成败）
    if !ok { notFound(w, "HANDOFF_NOT_FOUND"); return }
    if S256(in.HandoffVerifier) != h.BoundChallenge { badRequest(w, "verifier mismatch"); return }
    sid := RandomToken(48) // 高熵 desktop_session_id
    _ = b.store.PutDesktopSession(r.Context(), sid, DesktopSession{
        RefreshToken: h.RefreshToken /* 已加密 */, Sub: h.Sub, CreatedAt: time.Now().UTC(),
    })
    profile := b.oidc.FetchProfile(r.Context(), h.AccessToken) // display_name + avatar_url
    writeJSON(w, 200, map[string]any{
        "access_token": h.AccessToken, "expires_in": h.ExpiresIn,
        "desktop_session_id": sid, "profile": profile,
    })
}

// POST /api/desktop/refresh
func (b *Broker) DesktopRefresh(w http.ResponseWriter, r *http.Request) {
    var in struct{ DesktopSessionID string }
    _ = readJSON(r, &in)
    s, ok := b.store.GetDesktopSession(r.Context(), in.DesktopSessionID)
    if !ok { jsonError(w, 401, "SESSION_INVALID", "session expired or revoked"); return }
    refresh := b.secure.DecryptRefresh(s.RefreshToken) // 解密后用于 IdP
    tok, err := b.oidc.Refresh(r.Context(), refresh) // grant_type=refresh_token
    if err != nil {
        _ = b.store.DeleteDesktopSession(r.Context(), in.DesktopSessionID)
        jsonError(w, 401, "SESSION_INVALID", "refresh rejected by IdP"); return
    }
    if tok.RefreshToken != "" && tok.RefreshToken != refresh { // IdP 轮换则加密后更新
        _ = b.store.UpdateDesktopRefresh(r.Context(), in.DesktopSessionID,
            b.secure.EncryptRefresh(tok.RefreshToken))
    }
    writeJSON(w, 200, map[string]any{"access_token": tok.AccessToken, "expires_in": tok.ExpiresIn})
}

// POST /api/desktop/logout
func (b *Broker) DesktopLogout(w http.ResponseWriter, r *http.Request) {
    var in struct{ DesktopSessionID string }
    if err := readJSON(r, &in); err != nil || in.DesktopSessionID == "" {
        badRequest(w, "missing desktop_session_id"); return
    }
    if s, ok := b.store.GetDesktopSession(r.Context(), in.DesktopSessionID); ok {
        _ = b.store.DeleteDesktopSession(r.Context(), in.DesktopSessionID)
        _ = b.oidc.Revoke(r.Context(), b.secure.DecryptRefresh(s.RefreshToken)) // 可选
    }
    w.WriteHeader(http.StatusNoContent) // 幂等
}
```

---

## 6. 网页自身登录（账户中心）

账户中心是**人**在浏览器里的登录，与桌面 handoff 完全独立。它只展示 OIDC 资料 + 下载入口 + 退出，**不调用 Lumen 资源 API（`/api/v1/*`）**。登录/资料/登出端点由 **Go 服务端 broker（`chat.example.com`）** 承载；SPA（`example.com`）**跨源**调用之。

### 6.1 端点契约

#### `GET /auth/login` `[v0]`（顶层导航）

| 项 | 内容 |
|----|------|
| 处理 | 生成 OIDC PKCE（`verifier`/`challenge`）+ `state`，暂存（broker 表或加密 cookie，短 TTL）；302 到 IdP `/authorize`（`response_type=code`、`code_challenge_method=S256`、`scope=openid profile email`、`redirect_uri=https://chat.example.com/auth/callback`）。账户中心登录**不需要** `offline_access`，也**不需要** `aud=lumen-api`（不调 Lumen 资源 API）。 |
| 触发方式 | 浏览器**顶层导航**（`window.location` 跳到 `chat.example.com/auth/login`），非 XHR |
| 响应 | `302` → IdP |

#### `GET /auth/callback` `[v0]`（顶层导航）

| 项 | 内容 |
|----|------|
| 入参 | `code`、`state` |
| 处理 | 校验 `state`；服务端用 `client_secret` + `verifier` 向 IdP `/token` 换码；解析 `id_token`/`/userinfo` 得 `{sub, display_name, avatar_url}`；建立**最小网页会话**（存 broker 网页会话表或加密 cookie）；设置会话 cookie（属性见 [§6.2](#62-会话与账户中心页面)：`HttpOnly + Secure + SameSite=Lax + Path=/ + host-only`）；302 回 **`LUMEN_WEB_BASE_URL`（`https://example.com`）的 `/account`**。**网页会话不持久化 refresh_token**（账户中心无离线刷新需求）。 |
| 触发方式 | IdP → 浏览器**顶层导航**回 `chat.example.com/auth/callback`（顶层导航才能在 `SameSite=Lax` 下写入 cookie） |
| 响应 | `302` → `https://example.com/account`（带会话 cookie） |

#### `POST /auth/logout` `[v0]`（跨源 XHR，credentials）

| 项 | 内容 |
|----|------|
| 处理 | 清除会话（删 broker 网页会话行或失效 cookie），清 cookie；可选发起 IdP RP-initiated logout。 |
| 触发方式 | SPA 跨源 `fetch`（`credentials:'include'`）；broker 回带 CORS 头（[§6.3](#63-跨源与-cors账户中心)） |
| 响应 | `204`（SPA 侧再本地跳 `/`） |

#### `GET /api/me` `[v0]`（跨源 XHR，credentials；cookie 鉴权）

| 项 | 内容 |
|----|------|
| 处理 | 读会话 cookie → broker 网页会话，返回 `{display_name, avatar_url}`；未登录 → `401`。 |
| 触发方式 | SPA 跨源 `fetch`（`credentials:'include'`）；broker 回带 CORS 头 |
| 响应 | `200 {display_name, avatar_url}` / `401` |

> **`/api/me`（broker，cookie 鉴权）≠ `/api/v1/me`（资源服务器，Bearer 鉴权）**：两者是不同端点、不同鉴权方式、不同用途。账户中心只用前者。

### 6.2 会话与账户中心页面

- **会话 cookie**（broker 在 `chat.example.com` 下发）：
  - `HttpOnly`（前端 JS 不可读）
  - `Secure`（仅 HTTPS）
  - `SameSite=Lax`（同站顶层导航可带；`example.com` 与 `chat.example.com` 同站；跨源 XHR 由 CORS+credentials 补足）
  - `Path=/`
  - **host-only（不设 `Domain`）**——cookie 仅绑 `chat.example.com`，不泛化到 `example.com`/其它子域
  - cookie 值为不透明会话 id 或加密 payload（`LUMEN_SESSION_ENC_KEY`）。
- **账户中心 `/account`**（SPA 在 `example.com`）：
  - 前端进入时**跨源** `GET https://chat.example.com/api/me`（`credentials:'include'`）读取 `{display_name, avatar_url}`；`401` → 顶层导航跳 `chat.example.com/auth/login`。
  - UI：资料卡（头像 + 昵称）+「下载客户端」按钮（跳 `/download`）+「退出登录」按钮（跨源 `POST /auth/logout`）。
  - **不展示任何 Lumen 频道/消息/语音数据**；不带 `access_token`、不调资源 API `/api/v1/*`。

### 6.3 跨源与 CORS（账户中心）

> **关键更正（相对旧模型）**：旧设计中账户中心端点与 SPA 同在 `example.com`（Cloudflare Pages Functions），属**同源**，故文档声称「无需 CORS」。**新架构把 broker 移到 `chat.example.com`**，SPA（`example.com`）调 broker（`chat.example.com`）变为**跨源（cross-origin）**——因此账户中心**必须**新增 CORS + 带凭据 cookie。此断言由「同源 / 无 CORS」**更正为「跨源 / CORS + credentials / host-only Lax cookie」**。

- `example.com` 与 `chat.example.com`：**同站（same-site，同一可注册域 `example.com`）、跨源（cross-origin，host 不同）**。
- **Go broker CORS**（对 `/api/me`、`/auth/logout` 等被 SPA 以 XHR 调用者）：
  - `Access-Control-Allow-Origin`：**精确回显** `LUMEN_WEB_BASE_URL`（`https://example.com`）单一源，**不用** `*`（带凭据时 `*` 非法）。
  - `Access-Control-Allow-Credentials: true`。
  - 正确处理 `OPTIONS` 预检；按需 `Allow-Methods`/`Allow-Headers`。
- **SPA 侧**：对 broker 的 XHR 一律 `credentials:'include'`；`/auth/login`、`/auth/callback` 走**顶层导航**（`window.location`），不是 XHR。
- **顶层导航 vs XHR**：`SameSite=Lax` 允许**同站顶层导航**携带 cookie，故 `/auth/login`→IdP→`/auth/callback` 的重定向链能正常设置/携带会话 cookie；`/api/me`、`/auth/logout` 作为跨源 XHR，则靠 `credentials:'include'` + broker 的 `Allow-Credentials` CORS 携带 cookie。
- 详见 [协议 §2.8](./protocol-design.md#28-web-跨源与-cors-约定账户中心)（唯一权威）。

---

## 7. 与既有设计的衔接

### 7.1 Go 服务端：资源服务器验签不变，broker 与 CORS 为新增

- **资源服务器验签不变**：仍只用 IdP 的 **JWKS 本地验签**校验 `access_token`（JWT，验 `iss` / `aud` / `exp`），见 [协议 §2.3](./protocol-design.md#23-服务端验签jwks-本地验签) 与 [服务端 §2.1](./server-design.md#21-jwks-本地验签keyfunc-v3--golang-jwt-v5)。
- `LUMEN_OAUTH_AUDIENCE = lumen-api`（[服务端环境变量](./server-design.md#9-配置与环境变量)）保持不变；broker 请求 token 时令 `access_token` 的 `aud` 含 `lumen-api`，与此对齐。
- **新增 broker（同一进程）**：Go 服务端现同时是 **confidential OIDC client**，持有 `client_secret` 与（加密的）`refresh_token`（存 Postgres）。这**改变了旧「Go 服务端不持有 `client_secret`/`refresh_token`」的红线**——现红线为：`client_secret` 仅在服务端环境变量、`refresh_token` 仅在服务端 Postgres 且**静态加密**，二者**绝不下发桌面/前端**。
- **必须新增 CORS**：账户中心 SPA（`example.com`）**跨源**调 broker（`chat.example.com`）的 `/api/me`、`/auth/logout`，故 Go 服务端**必须**对 `example.com` 源开启带凭据 CORS（[§6.3](#63-跨源与-cors账户中心)、[协议 §2.8](./protocol-design.md#28-web-跨源与-cors-约定账户中心)）。桌面仍用**原生 HTTP/WS**（非浏览器同源策略约束）连资源服务器，不受 CORS 影响。

### 7.2 IdP 侧登记要求

- Go 服务端注册为 **confidential client**（带 `client_secret`），允许 `authorization_code` + `refresh_token` 授权类型与 **PKCE（S256）**。
- 允许该 client 请求令 `access_token` 的 `aud` 含 **`lumen-api`**（Keycloak 经 audience mapper / client scope；其它 IdP 经 resource/audience 参数）。
- 登记 redirect_uri：**`https://chat.example.com/desktop/callback`** 与 **`https://chat.example.com/auth/callback`**（§3.3）——由旧 `example.com/*` 迁到 `chat.example.com/*`。
- scope：桌面登录 `openid profile email offline_access`；账户中心 `openid profile email`。

### 7.3 桌面客户端改动点摘要

> 替换原「桌面直连 IdP PKCE」配置（[客户端 §2](./client-design.md#2-go-后端oauth2-pkce-登录与-token-管理)）。**桌面此次唯一实质改动是 handoff 基址 host** —— 线契约逐字节保留。

| 改动点 | 原（桌面直连 IdP） | 新（本设计） |
|--------|----|----|
| 配置 | 内置 IdP `issuer`/`client_id`/`scope` | **移除**；新增 `LUMEN_WEB_BASE_URL`（登录中介基址，默认 **`https://chat.example.com`**）、保留 `LUMEN_API_BASE_URL`（`https://chat.example.com/api/v1`）、`LUMEN_WS_URL`（`wss://chat.example.com/ws`） |
| 登录 | 桌面自跑 PKCE → IdP | **回环 handoff**：起 `127.0.0.1:rand` 监听 → 系统浏览器开 `chat.example.com/desktop/login` → 收 `handoff_code` → `POST chat.example.com/api/desktop/exchange` |
| 凭据存储 | refresh_token + access_token（DPAPI） | **`desktop_session_id`（DPAPI）**；`access_token` 仅内存；**不存** refresh_token |
| 刷新 | 桌面直连 IdP `refresh_token` | `POST chat.example.com/api/desktop/refresh{desktop_session_id}` |
| 登出 | 本地清 token | `POST chat.example.com/api/desktop/logout{desktop_session_id}` → 清凭据库 + 关 WS + 重置 store |
| 连 Go 服务端资源服务器 | Bearer access_token（不变） | Bearer access_token（**不变**） |

> **未来客户端变更 = 仅基址**：因 handoff 线契约逐字节保留，若 broker host 再变（如换域名），Windows 客户端只需改 `LUMEN_WEB_BASE_URL`，请求/响应体无需任何改动。

### 7.4 交叉链接

- 接口契约权威：[`../design/protocol-design.md`](./protocol-design.md)（鉴权流程 §2、REST §3、WS §4）
- 桌面实现：[`../design/client-design.md`](./client-design.md)（登录 §2、自动更新 §4）
- 服务端实现：[`../design/server-design.md`](./server-design.md)（JWKS 验签 §2.1、`/updates/` 托管 §7.7、环境变量 §9）
- 全局总览：[`../design/00-overview.md`](./00-overview.md)

---

## 8. 安全

### 8.1 安全红线（强制）

| 红线 | 要求 |
|------|------|
| handoff_code | 一次性消费 + 短 TTL（≈120s）+ 绑 `bound_challenge`（`S256(handoff_verifier)`） |
| redirect_uri | 仅允许 `http://127.0.0.1:<port>/...` 回环（拒 `localhost` 主机名、拒非 http/非 127.0.0.1） |
| access_token | 不进任何 URL（仅 `/api/desktop/exchange`、`/api/desktop/refresh` 响应体） |
| desktop_session_id | 高熵随机（≥32 字节）且仅存 Windows 凭据库（DPAPI），不写明文文件/日志 |
| client_secret | 仅在 **Go 服务端环境变量**（`LUMEN_OAUTH_CLIENT_SECRET`），绝不进前端产物/仓库/桌面 |
| refresh_token | **仅在 Go 服务端 Postgres**（broker 表，以 `LUMEN_REFRESH_ENC_KEY` **静态加密**）；不进任何响应体下发桌面/前端；解密仅在换/刷 token 的瞬间于内存进行 |
| 网页会话 cookie | `HttpOnly + Secure + SameSite=Lax + Path=/ + host-only`（不设 `Domain`）；值加密/签名（`LUMEN_SESSION_ENC_KEY`） |
| CORS（账户中心） | broker 只对精确 `LUMEN_WEB_BASE_URL`（`example.com`）源开 `Allow-Credentials` CORS，**不用** `*` |

### 8.2 输入校验（broker）

- 所有端点对入参做 schema 校验（必填字段、类型、长度上限）；`handoff_code`/`handoff_verifier`/`desktop_session_id` 必须是合法 base64url，限定长度，防注入与超长。
- `redirect_uri` 用 URL 解析后逐项校验（scheme/host/port），不接受相对/畸形 URL。
- 失败一律返回统一 JSON 错误信封 `{ "error": { "code": "...", "message": "..." } }`，不泄露内部细节（不回显 token/secret/堆栈）。
- 解析 IdP token/JWT 时校验 `iss`、签名（如本地需用到）与基本字段；以服务端时间判 TTL。

### 8.3 broker 表 TTL / 清理

- handoff 表：写入即设 `expires_at ≈ now()+120s`；`/exchange` 命中即 DELETE（一次性）；暂存上下文 `ctx:*` 设 ≈600s TTL。到期行由惰性检查 + 定时清理任务清除。
- desktop 会话表：无 TTL（或远大于 refresh_token 寿命）；`/logout` 删除；refresh 失败（IdP 拒绝）时应删除该会话并回 `SESSION_INVALID`。
- 网页会话表：随会话生命周期；`/auth/logout` 删除。

### 8.4 静态加密（refresh_token）

- `refresh_token` 落 handoff/desktop 会话表前，以 `LUMEN_REFRESH_ENC_KEY` 做 AEAD（如 `crypto/aes` + GCM 或 `secretbox`）加密后存 `BYTEA`；换/刷 token 时解密到内存、用完即弃。
- `LUMEN_REFRESH_ENC_KEY` 与 `LUMEN_SESSION_ENC_KEY` **分离**（职责隔离：前者护 refresh_token、后者护网页会话 cookie）。
- 即使 DB 泄露，未拿到 `LUMEN_REFRESH_ENC_KEY` 也无法还原 refresh_token 明文。

### 8.5 速率限制建议

- 对 `/api/desktop/exchange`、`/api/desktop/refresh`、`/auth/login`、`/auth/callback` 配置速率限制（按 IP + 路径，Traefik 中间件或应用层令牌桶），限制暴力枚举 `handoff_code`/`desktop_session_id`。
- `handoff_code`/`desktop_session_id` 的高熵 + 一次性/TTL 是主要防线，速率限制为纵深防御。
- 所有端点强制 HTTPS（Traefik 终结 TLS，容器内明文）。

---

## 9. 配置（环境变量/Secrets）

> broker 与资源服务器同进程，配置统一走 **Go 服务端环境变量（Coolify 注入）**，命名以 `LUMEN_` 前缀（与 [协议 §1.3](./protocol-design.md#13-全局配置项环境变量coolify-注入) broker env 表对齐，此处为唯一权威扩展）。**不再有官网 Worker env / KV 绑定 / wrangler.toml**。

### 9.1 broker 环境变量与 Secret 清单（Go 服务端）

| 名称 | 类型 | 含义 | 示例 |
|------|------|------|------|
| `LUMEN_OAUTH_ISSUER` | env | IdP issuer（用于 discovery / 校验 `iss`；资源服务器亦用） | `https://auth.example.com/realms/lumen` |
| `LUMEN_OAUTH_AUDIENCE` | env | 令 `access_token` 的 `aud` 含此值（资源服务器验签亦用） | `lumen-api` |
| `LUMEN_OAUTH_CLIENT_ID` | env | confidential client_id | `lumen-app` |
| `LUMEN_OAUTH_CLIENT_SECRET` | **Secret** | confidential client_secret（仅服务端） | `***` |
| `LUMEN_OAUTH_AUTHORIZE_URL` | env（可选） | IdP 授权端点（缺省由 discovery 推导） | `https://auth.example.com/.../auth` |
| `LUMEN_OAUTH_TOKEN_URL` | env（可选） | IdP token 端点（缺省由 discovery 推导） | `https://auth.example.com/.../token` |
| `LUMEN_OAUTH_USERINFO_URL` | env（可选） | IdP userinfo（资料兜底；缺省 discovery 推导） | `https://auth.example.com/.../userinfo` |
| `LUMEN_OAUTH_DESKTOP_REDIRECT_URI` | env | 桌面中介回调（IdP 登记） | `https://chat.example.com/desktop/callback` |
| `LUMEN_OAUTH_WEB_REDIRECT_URI` | env | 账户中心回调（IdP 登记） | `https://chat.example.com/auth/callback` |
| `LUMEN_WEB_BASE_URL` | env | 官网源（CORS 白名单 + 网页会话重定向目标） | `https://example.com` |
| `LUMEN_SESSION_ENC_KEY` | **Secret** | 网页会话 cookie 加密/签名密钥 | `***` |
| `LUMEN_REFRESH_ENC_KEY` | **Secret** | `refresh_token` 落库静态加密密钥（与会话密钥分离） | `***` |

> Secret（`LUMEN_OAUTH_CLIENT_SECRET`、`LUMEN_SESSION_ENC_KEY`、`LUMEN_REFRESH_ENC_KEY`）经 Coolify 环境变量注入，仅 Go 进程运行时可读，**永不**进仓库/前端产物/桌面。资源服务器另需 `LUMEN_OAUTH_JWKS_URL` 等（见 [协议 §1.3](./protocol-design.md#13-全局配置项环境变量coolify-注入)）。

### 9.2 broker 存储（PostgreSQL，替代 KV）

| 表 | 用途 | 生命周期 |
|----|------|----------|
| handoff | `handoff_code → {access_token, expires_in, refresh_token(加密), sub, bound_challenge}` + 登录上下文 `ctx:*` | ≈120s（ctx ≈600s），一次性消费 |
| desktop 会话 | `desktop_session_id → {refresh_token(加密), sub, created_at}` | 无 TTL（logout 删） |
| 网页会话 | `web_session_id → {sub, display_name, avatar_url, created_at}` | 随会话（logout 删） |

> broker 复用服务端已有 PostgreSQL 连接池（`jackc/pgx/v5`）；表随迁移创建（见服务端设计）。**无 Cloudflare KV、无 `wrangler.toml`、无 KV 命名空间绑定**。

### 9.3 官网构建配置（EdgeOne Pages，纯静态）

| 项 | 值 |
|----|----|
| 构建命令 | `npm ci && npm run build` |
| 输出目录 | `website/dist/` |
| Functions | **无**（EdgeOne STATIC-ONLY） |
| Node 版本 | LTS（如 20.x） |
| SPA 回退 | `edgeone.json` 的 SPA rewrites：`/* → /index.html`（无 Functions 路由需规避） |
| secret / KV | **无**（前端产物不含任何 secret；无 KV 绑定） |
| 生产/预览 | broker 侧用独立数据库/schema 与独立 IdP client/redirect 白名单（官网静态产物本身无环境差异） |

> **`edgeone.json` 内容边界**：仅 build config（构建命令、输出目录）+ SPA rewrites。**不含** Functions、KV、secret、任何代理/后端路由。

---

## 10. v0 归属与验收

本文全部内容为 **`[v0]`**。下列为可独立验收的标准：

### 10.1 桌面经服务端 broker 登录（核心闭环）

- [ ] 桌面起 `127.0.0.1:rand` 回环监听，生成 `handoff_verifier`+`state`，系统浏览器打开 `chat.example.com/desktop/login?redirect_uri=http://127.0.0.1:<port>/cb&state=<s>&challenge=<S256(verifier)>`。
- [ ] broker `/desktop/login` 拒绝非 `127.0.0.1` 回环的 `redirect_uri`（返回 `400`）。
- [ ] broker 完成 IdP OIDC（Auth Code+PKCE，`scope=openid profile email offline_access`，`access_token.aud` 含 `lumen-api`），`/desktop/callback` 写 handoff 表（TTL≈120s，绑 `bound_challenge`，refresh_token 加密），302 回 `127.0.0.1:<port>/cb?handoff_code&state`，URL 中**无** `access_token`。
- [ ] 桌面校验 `state`，`POST /api/desktop/exchange{handoff_code,handoff_verifier}` 得 `{access_token, expires_in, desktop_session_id, profile}`；`bound_challenge` 校验通过；`handoff_code` 二次提交返回 `404`（一次性）。
- [ ] 桌面用该 `access_token` 成功连上 Go 服务端资源服务器 WSS `/ws` 与 REST `/api/v1/*`（资源服务器验签契约不变，JWKS 验签通过）。
- [ ] **线契约核对**：`/desktop/*`、`/api/desktop/*` 的请求/响应体字段与旧契约逐字节一致，唯 host 为 `chat.example.com`。

### 10.2 刷新与登出

- [ ] `access_token` 临期 → `POST chat.example.com/api/desktop/refresh{desktop_session_id}` 返回新 `access_token`+`expires_in`；IdP 轮换 refresh_token 时 desktop 会话表（加密列）同步更新。
- [ ] `desktop_session_id` 失效/不存在时 `/api/desktop/refresh` 返回 `401` `SESSION_INVALID`，桌面据此转重新登录。
- [ ] `POST chat.example.com/api/desktop/logout{desktop_session_id}` 返回 `204` 并删 desktop 会话行；桌面随后清凭据库 + 关 WS + 重置 store。

### 10.3 账户中心（跨源）

- [ ] 未登录访问 `example.com/account` → SPA 跨源 `GET chat.example.com/api/me` 得 `401` → 顶层导航 `chat.example.com/auth/login` → IdP → `chat.example.com/auth/callback` 设 `HttpOnly+Secure+SameSite=Lax+Path=/+host-only` 会话 cookie → 302 回 `example.com/account`。
- [ ] 账户中心展示 OIDC 资料（头像 `avatar_url` + 昵称 `display_name`）+ 下载入口 + 退出，且**全程不调用 Lumen 资源 API（`/api/v1/*`）**。
- [ ] SPA 跨源 XHR（`/api/me`、`/auth/logout`）带 `credentials:'include'`，broker 回带 `Access-Control-Allow-Origin: https://example.com` + `Allow-Credentials: true`（非 `*`），预检 `OPTIONS` 正确处理。
- [ ] 跨源 `POST /auth/logout` 清会话；再访问 `/account` 需重新登录。

### 10.4 静态官网核对

- [ ] `example.com` **纯静态**：仅托管 `website/dist/`，**无 Functions、无 KV、无 secret**；`edgeone.json` 仅含 build config + SPA rewrites。
- [ ] 下载页从 `chat.example.com/updates/latest.json` **直接 fetch**（无 `/api/download/latest` 代理）读取最新版本与 `Setup.exe` 链接；`/updates/` 响应带允许跨源读取的 CORS 头。

### 10.5 安全核对

- [ ] `client_secret` 不在前端产物/仓库/桌面中可见（仅 Go 服务端 `env`）。
- [ ] `refresh_token` 不出现在任何下发桌面/前端的响应体中（仅存 Postgres，静态加密）。
- [ ] `access_token` 不出现在任何 URL（仅 `/exchange`、`/refresh` 响应体）。
- [ ] `desktop_session_id` 高熵且仅存 DPAPI（不落明文文件/日志）。
- [ ] 网页会话 cookie 为 `HttpOnly+Secure+SameSite=Lax+Path=/+host-only`（不设 `Domain`）。
- [ ] `LUMEN_REFRESH_ENC_KEY` 与 `LUMEN_SESSION_ENC_KEY` 分离；DB 无 `LUMEN_REFRESH_ENC_KEY` 无法还原 refresh_token 明文。

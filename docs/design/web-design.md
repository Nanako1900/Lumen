# Lumen 官网与 Web 中介登录设计

> 文档版本: 1.0
> 状态: 详细设计（authoritative for 官网 + Web 中介登录）
> 适用范围: Lumen 官网（React + TailwindCSS，Cloudflare Pages/Functions/KV）与桌面端「委托官网登录」的 Web 中介
> 配套设计: [总览](./00-overview.md)、[协议契约](./protocol-design.md)、[服务端设计](./server-design.md)、[客户端设计](./client-design.md)
> 依据共享契约: Web-Auth 共享契约 `[v0]`（5 份文档严格一致；接口契约的唯一权威仍是 [`protocol-design.md`](./protocol-design.md)，本文与之冲突以契约为准）

**版本归属约定**（贯穿全文）：

| 标记 | 含义 |
|------|------|
| `[v0]` | 官网最小闭环：营销首页 + 下载 + 账户中心登录 + 桌面 Web 中介登录（回环 handoff）+ 刷新/登出 |

> 本文所有内容均为 `[v0]`。官网与 Web 中介登录整体一次性落地，无 v1/v2 分级。

---

## 目录

1. [概述与定位](#1-概述与定位)
2. [技术栈与部署](#2-技术栈与部署)
3. [域名与路由](#3-域名与路由)
4. [页面结构](#4-页面结构)
5. [Web 中介登录（桌面）](#5-web-中介登录桌面)
6. [网页自身登录（账户中心）](#6-网页自身登录账户中心)
7. [与既有设计的衔接](#7-与既有设计的衔接)
8. [安全](#8-安全)
9. [配置（环境变量/KV/Secrets）](#9-配置环境变量kvsecrets)
10. [v0 归属与验收](#10-v0-归属与验收)

---

## 1. 概述与定位

Lumen 官网 `https://example.com` 是一个**纯静态营销站 + 轻量身份中介**，承担两类彼此独立的职责：

1. **面向人的官网**：营销首页（Hero + 下载 CTA）、客户端下载页、账户中心（登录后查看 OIDC 资料 + 下载客户端 + 退出）、帮助/隐私/服务条款/关于等静态内容页。
   - **不做网页聊天、不做网页语音**——所有聊天/语音能力只在 Windows 桌面客户端内。
   - 账户中心**不调用 Lumen API**（`chat.example.com`），仅展示来自 OIDC 的资料（头像/昵称）并提供下载与退出。
2. **面向桌面客户端的 Web 中介登录**：官网是 **confidential OIDC client**，代替桌面客户端完成与 IdP 的 OAuth2/OIDC（Authorization Code + PKCE）交互；桌面客户端不再内置 IdP `issuer/client_id/scope`、不再自行对 IdP 跑 PKCE，改为「委托官网登录」。
   - `client_secret` 仅存在于 Cloudflare Worker 的加密环境变量中，**绝不下发到桌面**。
   - `refresh_token` **永不落到桌面**——存于官网 KV（`SESSIONS`）；桌面只持有不透明高熵 `desktop_session_id`（存 Windows 凭据库 DPAPI），`access_token` 仅在桌面内存。

### 1.1 为什么引入 Web 中介（相对原直连 PKCE 的变化）

| 维度 | 原设计（桌面直连 IdP） | 本设计（官网中介） |
|------|------------------------|--------------------|
| OIDC client 类型 | public client（桌面 PKCE，无 secret） | confidential client（官网 Worker 持 `client_secret`） |
| `client_secret` | 无 | 仅 Worker 加密环境变量 |
| `refresh_token` 落点 | 桌面本地（DPAPI） | 官网 KV `SESSIONS`，不出 Cloudflare |
| 桌面持有的凭据 | refresh_token + access_token | `desktop_session_id`（DPAPI）+ access_token（仅内存） |
| 桌面内置 IdP 配置 | issuer/client_id/scope | **无**（移到官网 Worker） |
| Go 服务端 | JWKS 本地验 IdP JWT（aud=lumen-api） | **完全不变**（仍 JWKS 本地验签） |

> Go 服务端的鉴权契约（[协议 §2](./protocol-design.md#2-鉴权流程总览)、[服务端 §2.1](./server-design.md#21-jwks-本地验签keyfunc-v3--golang-jwt-v5)）一字不改：仍只用 IdP 的 JWKS 本地验 `access_token`（JWT，验 `iss`/`aud`/`exp`）。账户中心不调 Lumen API，故 Go 服务端无需新增 CORS。

---

## 2. 技术栈与部署

### 2.1 技术栈

| 层 | 选型 | 说明 |
|----|------|------|
| 前端框架 | **React** | SPA（可选 SSG 预渲染静态营销页） |
| 样式 | **TailwindCSS** | 深色为主（与桌面客户端视觉一致） |
| 构建 | **Vite** | 输出静态资源到 `dist/` |
| 静态托管 | **Cloudflare Pages** | 静态 SPA/SSG，全球 CDN，自动 HTTPS |
| 服务端逻辑 | **Cloudflare Pages Functions（Worker）** | 即仓库内 `functions/` 目录，承载所有 `/desktop/*`、`/api/desktop/*`、`/auth/*` 端点 |
| 状态存储 | **Cloudflare KV** | 两个命名空间：`HANDOFF`（一次性短期）、`SESSIONS`（桌面长期会话） |
| 机密 | **CF 加密环境变量（Secrets）** | `client_secret` 等绝不进仓库、绝不下发前端 |

### 2.2 部署形态（Cloudflare Pages + Functions + KV）

```
┌──────────────────────── Cloudflare（example.com） ────────────────────────┐
│                                                                            │
│   静态资源（Pages）                Pages Functions（Worker, functions/）     │
│   ┌──────────────────┐            ┌─────────────────────────────────────┐  │
│   │ React SPA/SSG     │            │ /desktop/login  /desktop/callback   │  │
│   │ 首页/下载/账户中心 │  fetch →   │ /api/desktop/exchange|refresh|logout│  │
│   │ 帮助/隐私/条款/关于│  (同源)    │ /auth/login  /auth/callback /logout │  │
│   └──────────────────┘            └──────────┬──────────────┬───────────┘  │
│                                              │ KV           │ Secrets       │
│                                   ┌──────────▼───┐ ┌────────▼──────────┐    │
│                                   │ KV: HANDOFF  │ │ OIDC_CLIENT_SECRET│    │
│                                   │ KV: SESSIONS │ │ SESSION_ENC_KEY...│    │
│                                   └──────────────┘ └───────────────────┘    │
└────────────────────────────────────────────────────────────────────────────┘
        │ confidential OIDC client（Auth Code + PKCE，token 交换在 Worker）
        ▼
┌──────────── IdP（auth.example.com，外部 OAuth2/OIDC，不变） ────────────┐
│   /authorize  /token  /userinfo  /jwks  /logout（issuer 发现）            │
└──────────────────────────────────────────────────────────────────────────┘

   桌面客户端                         Go 服务端（chat.example.com，不变）
   ┌──────────────┐  access_token  ┌──────────────────────────────────────┐
   │ Wails Windows│ ─────────────► │ REST /api/v1/*  +  WSS /ws  +  SFU     │
   │ 回环 127.0.0.1│  Bearer JWT    │ JWKS 本地验签（iss/aud=lumen-api/exp） │
   └──────────────┘                └──────────────────────────────────────┘
```

### 2.3 构建与发布要点

- **目录约定（Cloudflare Pages 默认）**：
  - 构建命令：`npm ci && npm run build`
  - 构建输出目录：`dist/`
  - **Functions 目录：`functions/`**（Pages 自动按文件路由部署为 Worker；无需独立 `wrangler.toml` 入口，但 KV/Secrets 绑定在 Pages 项目设置或 `wrangler.toml` 的 `[[kv_namespaces]]` 中声明）。
- **SPA 回退**：在 `dist/` 放置 `_redirects`，将未命中静态文件的页面路由回退到 `/index.html`（`/*  /index.html  200`），但**不得**覆盖 `functions/` 端点（Functions 路由优先级高于静态回退）。
- **机密注入**：`client_secret`、`SESSION_ENC_KEY` 等通过 `wrangler pages secret put <NAME>` 或 Pages 控制台「加密环境变量」写入；**前端构建产物不得包含任何 secret**（仅 Worker 运行时可读 `env`）。
- **KV 绑定**：在 Pages 项目设置中将 `HANDOFF`、`SESSIONS` 两个 KV 命名空间绑定到 Functions 的 `env.HANDOFF` / `env.SESSIONS`。
- **预览 vs 生产**：预览环境用独立 KV 命名空间与独立 IdP client（或独立 redirect 白名单），避免污染生产会话。

---

## 3. 域名与路由

### 3.1 域名职责划分

| 域名 | 归属 | 内容 | TLS |
|------|------|------|-----|
| `https://example.com` | Cloudflare Pages + Functions | 官网静态页 + 中介登录 Worker 端点 | Cloudflare 自动 |
| `https://chat.example.com` | Go 服务端（Coolify） | REST `/api/v1/*`、WSS `/ws`、SFU、`/updates/` 静态托管 | Traefik 自动（不变） |
| `https://auth.example.com/realms/lumen` | 外部 IdP（OAuth2/OIDC） | `/authorize`、`/token`、`/userinfo`、`/jwks`、`/logout` | IdP 侧（不变） |

### 3.2 路由总表

**页面路由（静态，React Router）**

| 路径 | 页面 | 鉴权 |
|------|------|------|
| `/` | 首页（Hero + 下载 CTA） | 公开 |
| `/download` | 下载页（指向 `chat.example.com/updates/`） | 公开 |
| `/account` | 账户中心（登录后看资料/下载/退出） | 需网页会话（未登录 → 触发 `/auth/login`） |
| `/help` | 帮助 | 公开 |
| `/privacy` | 隐私政策 | 公开 |
| `/terms` | 服务条款 | 公开 |
| `/about` | 关于 | 公开 |

**Worker 端点路由（`functions/`）**

| 方法 | 路径 | 用途 | 消费方 |
|------|------|------|--------|
| GET | `/desktop/login` | 起桌面登录：校验回环 redirect_uri，暂存上下文，302 到 IdP | 桌面（系统浏览器） |
| GET | `/desktop/callback` | IdP 回调：Worker 换 token，生成 handoff_code，302 回回环 | IdP → 浏览器 |
| POST | `/api/desktop/exchange` | 用 handoff_code+verifier 换 `access_token` + `desktop_session_id` | 桌面 |
| POST | `/api/desktop/refresh` | 用 `desktop_session_id` 刷新 `access_token` | 桌面 |
| POST | `/api/desktop/logout` | 注销 `desktop_session_id`（删 SESSIONS） | 桌面 |
| GET | `/auth/login` | 账户中心登录：OIDC（PKCE）发起 | 浏览器 |
| GET | `/auth/callback` | 账户中心 OIDC 回调：换 token，设 httpOnly cookie 会话 | IdP → 浏览器 |
| POST | `/auth/logout` | 清网页会话 | 浏览器 |
| GET | `/api/me` | 账户中心读当前网页会话资料（`{display_name, avatar_url}`）；未登录 → `401` | 浏览器 |

### 3.3 IdP 须登记的回调 URL

> 官网 client 在 IdP 须登记以下两个 redirect_uri（均在 `example.com`，HTTPS）：

| 回调 | 用途 |
|------|------|
| `https://example.com/desktop/callback` | 桌面 Web 中介登录 |
| `https://example.com/auth/callback` | 账户中心网页登录 |

> 注意：桌面端的 `http://127.0.0.1:<port>/cb` **不是** IdP 的 redirect_uri——它只是官网 Worker 的 handoff 回环地址；IdP 永远只回到 `example.com/desktop/callback`。

---

## 4. 页面结构

### 4.1 各页面内容

| 页面 | 关键内容 |
|------|----------|
| **首页 `/`** | 精简 Hero（产品一句话定位「类 Discord 的轻量开黑语音」）+ 主 CTA「下载 Windows 客户端」+ 次 CTA「登录账户中心」；深色背景、简洁单屏。 |
| **下载页 `/download`** | 下载 `Setup.exe`（指向最新版安装包）+ 版本号/更新日期；数据源为服务端自动更新清单 `https://chat.example.com/updates/latest.json`，**复用桌面自动更新文件**（见 §4.3）。 |
| **账户中心 `/account`** | 登录后展示 OIDC 资料（头像 `avatar_url` + 昵称 `display_name`）+「下载客户端」入口 +「退出登录」按钮。**不调用 Lumen API**。 |
| **帮助 `/help`** | 常见问题、登录/连接排障、PTT 说明等静态内容。 |
| **隐私 `/privacy`** | 隐私政策（含官网仅存会话映射、refresh_token 留存于 Cloudflare KV 的说明）。 |
| **条款 `/terms`** | 服务条款。 |
| **关于 `/about`** | 关于 Lumen、版本/许可信息。 |

### 4.2 下载页与自动更新文件的复用（`/download`）

下载页不维护独立的安装包托管，而是**复用服务端的自动更新文件**（[客户端 §4.3](./client-design.md#43-与-coolify-托管更新文件的衔接) / [服务端 §7.7](./server-design.md#77-自动更新文件托管)）：

- **清单**：`GET https://chat.example.com/updates/latest.json` —— 含 `version` 与 `platforms["windows/amd64"].url`（指向 `https://chat.example.com/updates/Lumen-Setup-x.y.z.exe`）。
- **下载页行为**：前端在加载时 `fetch` 该 `latest.json`（公开、免鉴权、CORS 由 Go 服务端的静态 FileServer 默认放行 GET；若浏览器跨域被拦，可改由官网 Worker 代理 `GET /api/download/latest` 转发该 JSON 以规避 CORS——见下注），取出 `version` 与安装包 URL，渲染下载按钮直接指向 `*.exe`。
- **下载页不缓存版本号**：与 `latest.json` 的 `Cache-Control: no-cache` + ETag 语义一致，每次进入下载页都读取最新清单。

> CORS 注记：`/updates/` 为 Go 服务端 `http.FileServer` 静态托管，默认不带 `Access-Control-Allow-Origin`。若官网前端从 `example.com` 直接 `fetch` `chat.example.com/updates/latest.json` 被浏览器同源策略拦截，则**新增官网 Worker 代理端点** `GET /api/download/latest`：Worker 服务端侧（无 CORS 限制）拉取 `latest.json` 后以同源 JSON 返回前端。该代理纯只读、无鉴权，不改变 Go 服务端契约（Go 侧仍无需加 CORS）。

### 4.3 React 目录结构

```
website/
├── functions/                      # Cloudflare Pages Functions（Worker）
│   ├── desktop/
│   │   ├── login.ts                # GET  /desktop/login
│   │   └── callback.ts             # GET  /desktop/callback
│   ├── api/
│   │   ├── desktop/
│   │   │   ├── exchange.ts         # POST /api/desktop/exchange
│   │   │   ├── refresh.ts          # POST /api/desktop/refresh
│   │   │   └── logout.ts           # POST /api/desktop/logout
│   │   ├── download/
│   │   │   └── latest.ts           # GET  /api/download/latest（可选 CORS 代理）
│   │   └── me.ts                   # GET  /api/me（账户中心会话资料；未登录 401）
│   ├── auth/
│   │   ├── login.ts                # GET  /auth/login
│   │   ├── callback.ts             # GET  /auth/callback
│   │   └── logout.ts               # POST /auth/logout
│   └── _lib/                       # Worker 共享库（非页面）
│       ├── oidc.ts                 # discovery / authorize URL / token 交换 / refresh / revoke
│       ├── pkce.ts                 # S256(verifier) / randomString（Web Crypto）
│       ├── kv.ts                   # HANDOFF / SESSIONS 读写封装（TTL/一次性消费）
│       ├── session.ts             # 网页会话 cookie 签发/校验（httpOnly+secure+SameSite）
│       ├── http.ts                 # JSON 响应/错误信封/输入校验
│       └── env.ts                  # 环境变量类型与读取
├── src/                            # React 前端（静态）
│   ├── main.tsx
│   ├── App.tsx                     # 路由装配
│   ├── routes/
│   │   ├── Home.tsx                # /
│   │   ├── Download.tsx            # /download
│   │   ├── Account.tsx             # /account
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
│       ├── api.ts                  # 前端 fetch（同源 /auth/*、/api/download/*）
│       └── format.ts
├── public/
│   └── _redirects                  # SPA 回退（不覆盖 functions 路由）
├── index.html
├── tailwind.config.js
├── vite.config.ts
├── wrangler.toml                   # KV 绑定声明（HANDOFF/SESSIONS）
└── package.json
```

### 4.4 Tailwind 约定（深色为主）

- **主题**：深色为默认（`<html class="dark">` 常驻，不提供亮色切换，与 backlog「亮色主题」一致推迟）。
- **调色板**（`tailwind.config.js` 扩展，示意）：
  - 背景：`bg-zinc-950` / 卡片 `bg-zinc-900` / 边框 `border-zinc-800`
  - 文本：主 `text-zinc-100` / 次 `text-zinc-400`
  - 强调：`text-indigo-400` / 主按钮 `bg-indigo-600 hover:bg-indigo-500`
- **排版**：`font-sans`（系统字体栈）；标题 `tracking-tight`；正文 `leading-relaxed`。
- **布局**：容器 `max-w-5xl mx-auto px-4`；卡片 `rounded-2xl shadow-lg`；响应式以 `md:` 断点为主。
- **可访问性**：交互元素 `focus-visible:ring-2 ring-indigo-400`；对比度满足 WCAG AA。

---

## 5. Web 中介登录（桌面）

本节是桌面「委托官网登录」的完整契约。所有端点均在 `https://example.com`。命名严格按共享契约。

### 5.1 端点契约（逐个）

#### 端点 1 — `GET /desktop/login` `[v0]`

发起桌面登录：校验回环 redirect_uri，暂存上下文，302 到 IdP。

| 项 | 内容 |
|----|------|
| 入参（query） | `redirect_uri`（必填，必须 `http://127.0.0.1:<port>/...` 回环）、`state`（必填，桌面生成的不透明随机串）、`challenge`（必填，`S256(handoff_verifier)`，base64url 无填充） |
| 处理 | 1) 校验 `redirect_uri` 为 `http://127.0.0.1:<port>/...` 回环（host 必须 `127.0.0.1`，scheme `http`）；2) 生成官网自建的 **OIDC PKCE**（`oidc_verifier` + `oidc_challenge=S256(oidc_verifier)`）与 OIDC `state'`；3) 暂存 `{state, challenge, redirect_uri, oidc_verifier, oidc_state}`（加密 cookie 或 KV，短 TTL）；4) 302 到 IdP `/authorize`（`response_type=code`、`code_challenge=oidc_challenge`、`code_challenge_method=S256`、`scope=openid profile email offline_access`、**依 IdP 约定**令 `access_token` 的 `aud` 含 `lumen-api`（Keycloak: audience mapper/client scope；Auth0/Logto: `audience`/resource 参数——见 [§7.2](#72-idp-侧登记要求)）、`redirect_uri=https://example.com/desktop/callback`、`state=oidc_state`） |
| 成功响应 | `302 Found` → IdP 授权端点 |
| 错误 | `400`（缺参/`redirect_uri` 非回环/`challenge` 非法 base64url）；JSON 错误信封 `{ "error": { "code": "BAD_REQUEST", "message": "..." } }` |

> 安全：`redirect_uri` 仅允许 `127.0.0.1` 回环（不接受 `localhost` 主机名以避免 DNS 重绑定，端口任意）；`access_token` 绝不进任何 URL。

#### 端点 2 — `GET /desktop/callback` `[v0]`

IdP 回调：Worker 用 `client_secret` 换 token，生成一次性 handoff_code，302 回回环。

| 项 | 内容 |
|----|------|
| 入参（query） | `code`（IdP 授权码）、`state`（应等于暂存的 `oidc_state`） |
| 处理 | 1) 取回暂存上下文，校验 `state==oidc_state`；2) Worker 用 `client_secret` + `oidc_verifier` 向 IdP `/token` 换 `{access_token, refresh_token, id_token, expires_in}`（`grant_type=authorization_code`）；3) 解析 `id_token`/`access_token` 得 `sub`，必要时 `/userinfo` 兜底取 `display_name`/`avatar_url`；4) 生成一次性 `handoff_code`（高熵随机）；5) 写 KV `HANDOFF`：`handoff_code → {access_token, expires_in, refresh_token, sub, bound_challenge}`（`bound_challenge` = 端点1 暂存的桌面 `challenge`），TTL≈120s；6) 302 到 `redirect_uri?handoff_code=<code>&state=<原桌面 state>` |
| 成功响应 | `302 Found` → `http://127.0.0.1:<port>/cb?handoff_code=...&state=...` |
| 错误 | `400`（state 不匹配/无暂存上下文）、`502`（IdP token 交换失败）；失败时 302 回 `redirect_uri?error=<code>&state=<state>`（便于桌面回环页展示） |

> 安全红线：`access_token` **绝不进 URL**，仅写入 KV `HANDOFF`，由桌面随后用 verifier 换取。

#### 端点 3 — `POST /api/desktop/exchange` `[v0]`

用 `handoff_code` + `handoff_verifier` 换 `access_token` 与 `desktop_session_id`。

| 项 | 内容 |
|----|------|
| 入参（JSON body） | `{ "handoff_code": "string", "handoff_verifier": "string" }` |
| 处理 | 1) 读 KV `HANDOFF[handoff_code]`，不存在/过期 → `404`；2) 校验 `S256(handoff_verifier) == bound_challenge`，不匹配 → `400`；3) **一次性消费**（立即删除 KV `HANDOFF[handoff_code]`，无论后续成败）；4) 生成高熵 `desktop_session_id`；5) 写 KV `SESSIONS[desktop_session_id] = {refresh_token, sub, created_at}`；6) 返回 `access_token`、`expires_in`、`desktop_session_id`、`profile` |
| 成功响应 `200` | `{ "access_token": "<JWT>", "expires_in": 3600, "desktop_session_id": "<opaque>", "profile": { "display_name": "...", "avatar_url": "https://..." } }` |
| 错误 | `400`（缺参/`verifier` 不匹配 `bound_challenge`）；`404`（`handoff_code` 不存在或已消费/过期）；JSON 错误信封 |

#### 端点 4 — `POST /api/desktop/refresh` `[v0]`

用 `desktop_session_id` 刷新 `access_token`。

| 项 | 内容 |
|----|------|
| 入参（JSON body） | `{ "desktop_session_id": "string" }` |
| 处理 | 1) 读 KV `SESSIONS[desktop_session_id]`，不存在/失效 → `401` `SESSION_INVALID`；2) Worker 用 `client_secret` + 该会话的 `refresh_token` 向 IdP `/token`（`grant_type=refresh_token`）换新 `access_token`；3) **若 IdP 轮换 refresh_token**（返回新 `refresh_token`），更新 KV `SESSIONS[desktop_session_id].refresh_token`；4) 返回新 `access_token` + `expires_in` |
| 成功响应 `200` | `{ "access_token": "<JWT>", "expires_in": 3600 }` |
| 错误 | `401`（session 不存在/失效，或 IdP 拒绝 refresh）：`{ "error": { "code": "SESSION_INVALID", "message": "session expired or revoked" } }` —— 桌面据此转重新登录 |

> **`expires_in` 约定（防刷新风暴）**：`/api/desktop/exchange` 与 `/api/desktop/refresh` 返回的 `expires_in` 必须为**正整数秒**；若 IdP 未返回则 Worker 回退保守默认（如 300）。桌面按 `expires_in − 60s` 安排下次刷新；收到缺失/≤0 的 `expires_in` 时立即视为过期并刷新一次，但用最小刷新间隔兜底，避免 `expires_in=0` 触发紧凑循环（见 [客户端 §2.4](./client-design.md#24-静默刷新--暴露有效-token核心)）。

#### 端点 5 — `POST /api/desktop/logout` `[v0]`

注销桌面会话。

| 项 | 内容 |
|----|------|
| 入参（JSON body） | `{ "desktop_session_id": "string" }` |
| 处理 | 1) 删 KV `SESSIONS[desktop_session_id]`；2) 可选：向 IdP token revocation 端点撤销该 `refresh_token`；3) 返回 `204`（幂等：session 不存在也回 `204`） |
| 成功响应 | `204 No Content`（无 body） |
| 错误 | `400`（缺 `desktop_session_id`） |

### 5.2 桌面登录时序（回环 handoff）

```
桌面客户端                 系统浏览器            官网 Worker(example.com)        IdP(auth.example.com)
  │                          │                        │                              │
  │ 1) 起 127.0.0.1:rand 回环监听                      │                              │
  │    gen handoff_verifier + state                   │                              │
  │    challenge = S256(handoff_verifier)             │                              │
  │ 2) 打开浏览器 ───────────►│                        │                              │
  │   example.com/desktop/login?redirect_uri=http://127.0.0.1:port/cb               │
  │                  &state=<s>&challenge=<S256(verifier)>                           │
  │                          │ GET /desktop/login ───►│                              │
  │                          │                        │ 校验 redirect_uri 回环       │
  │                          │                        │ 暂存{state,challenge,         │
  │                          │                        │   redirect_uri,oidc_verifier} │
  │                          │◄── 302 IdP /authorize ─┤ (Auth Code+PKCE, aud=lumen-api)│
  │                          │ GET /authorize ──────────────────────────────────────►│
  │                          │            （用户在 IdP 登录/同意）                     │
  │                          │◄── 302 example.com/desktop/callback?code&state ────────┤
  │                          │ GET /desktop/callback ►│                              │
  │                          │                        │ 用 client_secret+verifier     │
  │                          │                        │   POST /token ──────────────►│
  │                          │                        │◄── access+refresh+id_token ──┤
  │                          │                        │ gen handoff_code              │
  │                          │                        │ KV HANDOFF{access,refresh,    │
  │                          │                        │   sub,bound_challenge} TTL120s│
  │                          │◄ 302 127.0.0.1:port/cb?handoff_code&state ─────────────┤
  │ 3) 回环收到 ◄────────────┤                        │  (access_token 不进 URL)      │
  │    校验 state == 本地 state                        │                              │
  │    POST example.com/api/desktop/exchange ────────►│                              │
  │      {handoff_code, handoff_verifier}             │ 校验 S256(verifier)==          │
  │                          │                        │   bound_challenge             │
  │                          │                        │ 一次性消费 HANDOFF            │
  │                          │                        │ gen desktop_session_id        │
  │                          │                        │ KV SESSIONS{refresh,sub}      │
  │ ◄── {access_token, expires_in, desktop_session_id, profile} ─────────────────────┤
  │ 4) 存 desktop_session_id → 凭据库(DPAPI)          │                              │
  │    access_token 仅内存                             │                              │
  │                                                                                  │
  │ ====== 连 Go 服务端（契约不变）======                                            │
  │    WSS chat.example.com/ws  +  REST /api/v1/*   （Bearer access_token JWT）       │
  │                                                                                  │
  │ ====== 刷新 ======                                                               │
  │ access_token 临期 → POST /api/desktop/refresh{desktop_session_id} → 新 access_token│
  │                                                                                  │
  │ ====== 登出 ======                                                               │
  │ POST /api/desktop/logout{desktop_session_id} → 清凭据库 + 关 WS + 重置 store      │
```

### 5.3 handoff 安全（challenge / verifier / 一次性 / 回环校验）

- **challenge / verifier 绑定**：桌面生成 `handoff_verifier`（高熵随机），`challenge = S256(handoff_verifier)`（base64url 无填充）。`challenge` 在 `/desktop/login` 经浏览器传到官网并随 token 写入 `HANDOFF.bound_challenge`；桌面在 `/api/desktop/exchange` 提交 `handoff_verifier`，Worker 校验 `S256(handoff_verifier)==bound_challenge`。即便 `handoff_code` 在回环 URL 泄露，攻击者无 `handoff_verifier` 亦无法换取 token。
- **一次性**：`handoff_code` 在 `/api/desktop/exchange` 第一次命中即从 KV 删除（无论成败），重放必失败。
- **短 TTL**：`HANDOFF` TTL≈120s，过期自动清除。
- **回环校验**：`redirect_uri` 仅允许 `http://127.0.0.1:<port>/...`（`/desktop/login` 强制校验）；IdP redirect_uri 固定为 `https://example.com/desktop/callback`，与桌面回环解耦。
- **state**：桌面侧 `state` 用于桌面校验回环回调来源；OIDC 侧 `oidc_state` 用于官网校验 IdP 回调来源，两者独立。
- **access_token 不进 URL**：仅出现在 `/exchange`、`/refresh` 的响应体；回环 URL 只携带 `handoff_code` + `state`。

### 5.4 KV 数据结构

**`HANDOFF`（一次性、短期）**

```
key   = handoff_code            // 高熵随机串（base64url）
value = {
  "access_token":    "<JWT>",
  "expires_in":      3600,
  "refresh_token":   "<opaque>",
  "sub":             "<oidc-subject>",
  "bound_challenge": "<S256(handoff_verifier), base64url>"
}
TTL   ≈ 120s                    // 写入即设；exchange 命中后立即删除（一次性消费）
```

**`SESSIONS`（桌面长期会话）**

```
key   = desktop_session_id      // 高熵随机串（base64url），仅桌面 DPAPI 持有
value = {
  "refresh_token": "<opaque>",  // 不出 Cloudflare
  "sub":           "<oidc-subject>",
  "created_at":    "2026-06-30T12:00:00Z"   // 可选
}
TTL   = 无（或远大于 refresh_token 寿命）；logout 删除
```

### 5.5 Worker 伪代码骨架（TypeScript）

> 以下为 Cloudflare Pages Functions 风格骨架（`onRequestGet` / `onRequestPost`），省略错误信封细节；`oidc`/`pkce`/`kv` 来自 `functions/_lib/`。

```typescript
// functions/_lib/pkce.ts —— Web Crypto S256
export async function s256(verifier: string): Promise<string> {
  const data = new TextEncoder().encode(verifier);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return base64url(new Uint8Array(digest)); // base64url 无填充
}
export function randomToken(bytes = 32): string {
  const b = crypto.getRandomValues(new Uint8Array(bytes));
  return base64url(b);
}

// functions/desktop/login.ts —— GET /desktop/login
export const onRequestGet: PagesFunction<Env> = async ({ request, env }) => {
  const url = new URL(request.url);
  const redirectUri = url.searchParams.get("redirect_uri") ?? "";
  const state = url.searchParams.get("state") ?? "";
  const challenge = url.searchParams.get("challenge") ?? "";
  // 1) 回环校验：仅允许 http://127.0.0.1:<port>/...
  const ru = safeUrl(redirectUri);
  if (!ru || ru.protocol !== "http:" || ru.hostname !== "127.0.0.1")
    return badRequest("redirect_uri must be 127.0.0.1 loopback");
  if (!state || !isBase64Url(challenge)) return badRequest("missing state/challenge");
  // 2) 官网自建 OIDC PKCE
  const oidcVerifier = randomToken();
  const oidcChallenge = await s256(oidcVerifier);
  const oidcState = randomToken();
  // 3) 暂存上下文（此处用 KV，TTL 短；亦可用加密 cookie）
  await env.HANDOFF.put(`ctx:${oidcState}`,
    JSON.stringify({ state, challenge, redirectUri, oidcVerifier }),
    { expirationTtl: 600 });
  // 4) 302 到 IdP /authorize（带 aud=lumen-api、scope offline_access）
  const authorize = buildAuthorizeUrl(env, {
    codeChallenge: oidcChallenge, state: oidcState,
    redirectUri: env.OIDC_DESKTOP_REDIRECT_URI, // https://example.com/desktop/callback
    scope: "openid profile email offline_access", audience: env.OIDC_AUDIENCE, // lumen-api
  });
  return Response.redirect(authorize, 302);
};

// functions/desktop/callback.ts —— GET /desktop/callback
export const onRequestGet: PagesFunction<Env> = async ({ request, env }) => {
  const url = new URL(request.url);
  const code = url.searchParams.get("code");
  const oidcState = url.searchParams.get("state") ?? "";
  const raw = await env.HANDOFF.get(`ctx:${oidcState}`);
  if (!code || !raw) return badRequest("invalid state");
  await env.HANDOFF.delete(`ctx:${oidcState}`);
  const ctx = JSON.parse(raw); // { state, challenge, redirectUri, oidcVerifier }
  // 用 client_secret + oidcVerifier 向 IdP /token 换码
  const tok = await exchangeAuthCode(env, code, ctx.oidcVerifier,
    env.OIDC_DESKTOP_REDIRECT_URI); // { access_token, refresh_token, id_token, expires_in }
  const sub = subjectFrom(tok.id_token ?? tok.access_token);
  // 生成一次性 handoff_code，写 HANDOFF（绑 bound_challenge=ctx.challenge）
  const handoffCode = randomToken();
  await env.HANDOFF.put(handoffCode, JSON.stringify({
    access_token: tok.access_token, expires_in: tok.expires_in,
    refresh_token: tok.refresh_token, sub, bound_challenge: ctx.challenge,
  }), { expirationTtl: 120 });
  // 302 回回环：仅带 handoff_code + 原桌面 state（access_token 绝不进 URL）
  const back = new URL(ctx.redirectUri);
  back.searchParams.set("handoff_code", handoffCode);
  back.searchParams.set("state", ctx.state);
  return Response.redirect(back.toString(), 302);
};

// functions/api/desktop/exchange.ts —— POST /api/desktop/exchange
export const onRequestPost: PagesFunction<Env> = async ({ request, env }) => {
  const { handoff_code, handoff_verifier } = await readJson(request);
  if (!handoff_code || !handoff_verifier) return badRequest("missing fields");
  const raw = await env.HANDOFF.get(handoff_code);
  if (!raw) return notFound("HANDOFF_NOT_FOUND");
  await env.HANDOFF.delete(handoff_code); // 一次性消费（无论后续成败）
  const h = JSON.parse(raw);
  if ((await s256(handoff_verifier)) !== h.bound_challenge)
    return badRequest("verifier mismatch");
  const desktopSessionId = randomToken(48); // 高熵
  await env.SESSIONS.put(desktopSessionId, JSON.stringify({
    refresh_token: h.refresh_token, sub: h.sub, created_at: new Date().toISOString(),
  }));
  const profile = await fetchProfile(env, h.access_token); // display_name + avatar_url
  return json(200, {
    access_token: h.access_token, expires_in: h.expires_in,
    desktop_session_id: desktopSessionId, profile,
  });
};

// functions/api/desktop/refresh.ts —— POST /api/desktop/refresh
export const onRequestPost: PagesFunction<Env> = async ({ request, env }) => {
  const { desktop_session_id } = await readJson(request);
  const raw = desktop_session_id && await env.SESSIONS.get(desktop_session_id);
  if (!raw) return jsonError(401, "SESSION_INVALID", "session expired or revoked");
  const s = JSON.parse(raw);
  const tok = await refreshWithIdp(env, s.refresh_token); // grant_type=refresh_token
  if (!tok) return jsonError(401, "SESSION_INVALID", "refresh rejected by IdP");
  if (tok.refresh_token && tok.refresh_token !== s.refresh_token) {
    await env.SESSIONS.put(desktop_session_id,
      JSON.stringify({ ...s, refresh_token: tok.refresh_token })); // IdP 轮换则更新
  }
  return json(200, { access_token: tok.access_token, expires_in: tok.expires_in });
};

// functions/api/desktop/logout.ts —— POST /api/desktop/logout
export const onRequestPost: PagesFunction<Env> = async ({ request, env }) => {
  const { desktop_session_id } = await readJson(request);
  if (!desktop_session_id) return badRequest("missing desktop_session_id");
  const raw = await env.SESSIONS.get(desktop_session_id);
  if (raw) {
    await env.SESSIONS.delete(desktop_session_id);
    // 可选：await revokeWithIdp(env, JSON.parse(raw).refresh_token);
  }
  return new Response(null, { status: 204 }); // 幂等
};
```

---

## 6. 网页自身登录（账户中心）

账户中心是**人**在浏览器里的登录，与桌面 handoff 完全独立。它只展示 OIDC 资料 + 下载入口 + 退出，**不调用 Lumen API**。

### 6.1 端点契约

#### `GET /auth/login` `[v0]`

| 项 | 内容 |
|----|------|
| 处理 | 生成 OIDC PKCE（`verifier`/`challenge`）+ `state`，暂存（加密 cookie 或 KV，短 TTL）；302 到 IdP `/authorize`（`response_type=code`、`code_challenge_method=S256`、`scope=openid profile email`、`redirect_uri=https://example.com/auth/callback`）。账户中心登录**不需要** `offline_access`，也**不需要** `aud=lumen-api`（不调 Lumen API）。 |
| 响应 | `302` → IdP |

#### `GET /auth/callback` `[v0]`

| 项 | 内容 |
|----|------|
| 入参 | `code`、`state` |
| 处理 | 校验 `state`；Worker 用 `client_secret` + `verifier` 向 IdP `/token` 换码；解析 `id_token`/`/userinfo` 得 `{sub, display_name, avatar_url}`；建立**最小网页会话**（会话记录存 KV 或加密 cookie）；设置 **httpOnly + Secure + SameSite=Lax** 的会话 cookie；302 回 `/account`。**网页会话不持久化 refresh_token**（账户中心无离线刷新需求）。 |
| 响应 | `302` → `/account`（带会话 cookie） |

#### `POST /auth/logout` `[v0]`

| 项 | 内容 |
|----|------|
| 处理 | 清除会话（删 KV 会话记录或失效 cookie），清 cookie；可选发起 IdP RP-initiated logout。 |
| 响应 | `204` 或 `302` → `/` |

### 6.2 会话与账户中心页面

- **会话 cookie**：`httpOnly`（前端 JS 不可读）+ `Secure`（仅 HTTPS）+ `SameSite=Lax`（防 CSRF，同站导航可带）；cookie 值为不透明会话 id 或加密 payload。
- **账户中心 `/account`**：
  - 前端进入时调用同源 `GET /api/me`（或在 `/account` 的 Function 直接渲染/返回会话内资料）读取 `{display_name, avatar_url}`；未登录 → 302/前端跳 `/auth/login`。
  - UI：资料卡（头像 + 昵称）+「下载客户端」按钮（跳 `/download`）+「退出登录」按钮（`POST /auth/logout`）。
  - **不展示任何 Lumen 频道/消息/语音数据**；不带 `access_token`、不连 `chat.example.com`。

---

## 7. 与既有设计的衔接

### 7.1 Go 服务端：完全不变

- 仍只用 IdP 的 **JWKS 本地验签**校验 `access_token`（JWT，验 `iss` / `aud` / `exp`），见 [协议 §2.3](./protocol-design.md#23-服务端验签jwks-本地验签) 与 [服务端 §2.1](./server-design.md#21-jwks-本地验签keyfunc-v3--golang-jwt-v5)。
- `LUMEN_OAUTH_AUDIENCE = lumen-api`（[服务端环境变量](./server-design.md#9-配置与环境变量)）保持不变；官网请求 token 时令 `access_token` 的 `aud` 含 `lumen-api`，与此对齐。
- **无需新增 CORS**：账户中心不调 Lumen API；桌面用原生 HTTP/WS（非浏览器同源策略约束）连 `chat.example.com`。Go 服务端不持有 `client_secret`/`refresh_token`（[服务端 §8 安全](./server-design.md)）这一红线维持。

### 7.2 IdP 侧登记要求

- 官网注册为 **confidential client**（带 `client_secret`），允许 `authorization_code` + `refresh_token` 授权类型与 **PKCE（S256）**。
- 允许该 client 请求令 `access_token` 的 `aud` 含 **`lumen-api`**（Keycloak 经 audience mapper / client scope；其它 IdP 经 resource/audience 参数）。
- 登记 redirect_uri：`https://example.com/desktop/callback` 与 `https://example.com/auth/callback`（§3.3）。
- scope：桌面登录 `openid profile email offline_access`；账户中心 `openid profile email`。

### 7.3 桌面客户端改动点摘要

> 替换原「桌面直连 IdP PKCE」配置（[客户端 §2](./client-design.md#2-go-后端oauth2-pkce-登录与-token-管理)）：

| 改动点 | 原 | 新 |
|--------|----|----|
| 配置 | 内置 IdP `issuer`/`client_id`/`scope` | **移除**；新增 `LUMEN_WEB_BASE_URL`（默认 `https://example.com`）、保留 `LUMEN_API_BASE_URL`（`https://chat.example.com/api/v1`）、`LUMEN_WS_URL`（`wss://chat.example.com/ws`） |
| 登录 | 桌面自跑 PKCE → IdP | **回环 handoff**：起 `127.0.0.1:rand` 监听 → 系统浏览器开 `example.com/desktop/login` → 收 `handoff_code` → `POST /api/desktop/exchange` |
| 凭据存储 | refresh_token + access_token（DPAPI） | **`desktop_session_id`（DPAPI）**；`access_token` 仅内存；**不存** refresh_token |
| 刷新 | 桌面直连 IdP `refresh_token` | `POST example.com/api/desktop/refresh{desktop_session_id}` |
| 登出 | 本地清 token | `POST example.com/api/desktop/logout{desktop_session_id}` → 清凭据库 + 关 WS + 重置 store |
| 连 Go 服务端 | Bearer access_token（不变） | Bearer access_token（**不变**） |

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
| client_secret | 仅在 Cloudflare Worker 加密环境变量，绝不进前端产物/仓库/桌面 |
| refresh_token | 不出 Cloudflare（仅存 KV `SESSIONS` / 写入 `HANDOFF` 的瞬态值；不进任何响应体下发桌面） |

### 8.2 Worker 输入校验

- 所有端点对入参做 schema 校验（必填字段、类型、长度上限）；`handoff_code`/`handoff_verifier`/`desktop_session_id` 必须是合法 base64url，限定长度，防注入与超长。
- `redirect_uri` 用 URL 解析后逐项校验（scheme/host/port），不接受相对/畸形 URL。
- 失败一律返回统一 JSON 错误信封 `{ "error": { "code": "...", "message": "..." } }`，不泄露内部细节（不回显 token/secret/堆栈）。
- 解析 IdP token/JWT 时校验 `iss`、签名（如本地需用到）与基本字段；以服务端时间判 TTL。

### 8.3 KV TTL

- `HANDOFF`：写入即设 `expirationTtl ≈ 120`；`/exchange` 命中即删除（一次性）；暂存上下文 `ctx:*` 设 ≈600s TTL。
- `SESSIONS`：无 TTL（或远大于 refresh_token 寿命）；`/logout` 删除；refresh 失败（IdP 拒绝）时应删除该会话并回 `SESSION_INVALID`。

### 8.4 速率限制建议

- 对 `/api/desktop/exchange`、`/api/desktop/refresh`、`/auth/login`、`/auth/callback` 配置 Cloudflare Rate Limiting（按 IP + 路径），限制暴力枚举 `handoff_code`/`desktop_session_id`。
- `handoff_code`/`desktop_session_id` 的高熵 + 一次性/TTL 是主要防线，速率限制为纵深防御。
- 启用 Cloudflare Bot/WAF 基线规则；所有端点强制 HTTPS（CF 默认）。

---

## 9. 配置（环境变量/KV/Secrets）

### 9.1 Worker 环境变量与 Secret 清单

| 名称 | 类型 | 含义 | 示例 |
|------|------|------|------|
| `OIDC_ISSUER` | env | IdP issuer（用于 discovery / 校验 `iss`） | `https://auth.example.com/realms/lumen` |
| `OIDC_AUTHORIZE_URL` | env | IdP 授权端点（可由 discovery 推导） | `https://auth.example.com/realms/lumen/protocol/openid-connect/auth` |
| `OIDC_TOKEN_URL` | env | IdP token 端点 | `https://auth.example.com/realms/lumen/protocol/openid-connect/token` |
| `OIDC_USERINFO_URL` | env | IdP userinfo（资料兜底） | `https://auth.example.com/realms/lumen/protocol/openid-connect/userinfo` |
| `OIDC_CLIENT_ID` | env | 官网 client_id | `lumen-website` |
| `OIDC_CLIENT_SECRET` | **Secret** | 官网 client_secret（仅 Worker） | `***（加密环境变量）` |
| `OIDC_AUDIENCE` | env | 令 `access_token` 的 `aud` 含此值 | `lumen-api` |
| `OIDC_DESKTOP_REDIRECT_URI` | env | 桌面中介回调（IdP 登记） | `https://example.com/desktop/callback` |
| `OIDC_WEB_REDIRECT_URI` | env | 账户中心回调（IdP 登记） | `https://example.com/auth/callback` |
| `WEB_BASE_URL` | env | 官网基址 | `https://example.com` |
| `UPDATES_LATEST_URL` | env | 下载页/代理读取的清单地址 | `https://chat.example.com/updates/latest.json` |
| `SESSION_ENC_KEY` | **Secret** | 网页会话 cookie 加密/签名密钥 | `***（加密环境变量）` |

> Secret（`OIDC_CLIENT_SECRET`、`SESSION_ENC_KEY`）通过 `wrangler pages secret put` 或 Pages 控制台加密环境变量注入，**永不**出现在 `wrangler.toml`/仓库/前端产物。

### 9.2 KV 命名空间绑定

| 绑定名 | 命名空间 | 用途 | TTL |
|--------|----------|------|-----|
| `HANDOFF` | `lumen-handoff`（生产）/ `lumen-handoff-preview` | `handoff_code → {access_token, expires_in, refresh_token, sub, bound_challenge}` + 登录上下文 `ctx:*` | ≈120s（ctx ≈600s），一次性消费 |
| `SESSIONS` | `lumen-sessions`（生产）/ `lumen-sessions-preview` | `desktop_session_id → {refresh_token, sub, created_at}` | 无（logout 删） |

`wrangler.toml`（绑定声明示意）：

```toml
name = "lumen-website"
pages_build_output_dir = "dist"

[[kv_namespaces]]
binding = "HANDOFF"
id = "<handoff-namespace-id>"

[[kv_namespaces]]
binding = "SESSIONS"
id = "<sessions-namespace-id>"
```

### 9.3 Pages 构建配置

| 项 | 值 |
|----|----|
| 构建命令 | `npm ci && npm run build` |
| 输出目录 | `dist/` |
| Functions 目录 | `functions/`（Pages 自动识别） |
| Node 版本 | LTS（如 20.x）经 `NODE_VERSION` 环境变量固定 |
| SPA 回退 | `public/_redirects`：`/*  /index.html  200`（不影响 `functions/` 路由） |
| 生产/预览 | 各绑定独立 KV 命名空间与 IdP client/redirect 白名单 |

---

## 10. v0 归属与验收

本文全部内容为 **`[v0]`**。下列为可独立验收的标准：

### 10.1 桌面经官网登录（核心闭环）

- [ ] 桌面起 `127.0.0.1:rand` 回环监听，生成 `handoff_verifier`+`state`，系统浏览器打开 `example.com/desktop/login?redirect_uri=http://127.0.0.1:<port>/cb&state=<s>&challenge=<S256(verifier)>`。
- [ ] 官网 `/desktop/login` 拒绝非 `127.0.0.1` 回环的 `redirect_uri`（返回 `400`）。
- [ ] 官网完成 IdP OIDC（Auth Code+PKCE，`scope=openid profile email offline_access`，`access_token.aud` 含 `lumen-api`），`/desktop/callback` 写 KV `HANDOFF`（TTL≈120s，绑 `bound_challenge`），302 回 `127.0.0.1:<port>/cb?handoff_code&state`，URL 中**无** `access_token`。
- [ ] 桌面校验 `state`，`POST /api/desktop/exchange{handoff_code,handoff_verifier}` 得 `{access_token, expires_in, desktop_session_id, profile}`；`bound_challenge` 校验通过；`handoff_code` 二次提交返回 `404`（一次性）。
- [ ] 桌面用该 `access_token` 成功连上 Go 服务端 WSS `/ws` 与 REST `/api/v1/*`（Go 服务端契约不变，JWKS 验签通过）。

### 10.2 刷新与登出

- [ ] `access_token` 临期 → `POST /api/desktop/refresh{desktop_session_id}` 返回新 `access_token`+`expires_in`；IdP 轮换 refresh_token 时 KV `SESSIONS` 同步更新。
- [ ] `desktop_session_id` 失效/不存在时 `/api/desktop/refresh` 返回 `401` `SESSION_INVALID`，桌面据此转重新登录。
- [ ] `POST /api/desktop/logout{desktop_session_id}` 返回 `204` 并删 KV `SESSIONS`；桌面随后清凭据库 + 关 WS + 重置 store。

### 10.3 账户中心

- [ ] 未登录访问 `/account` 触发 `/auth/login` → IdP → `/auth/callback` 设 httpOnly+Secure+SameSite 会话 cookie → 回 `/account`。
- [ ] 账户中心展示 OIDC 资料（头像 `avatar_url` + 昵称 `display_name`）+ 下载入口 + 退出，且**全程不调用 Lumen API**。
- [ ] `POST /auth/logout` 清会话；再访问 `/account` 需重新登录。

### 10.4 安全核对

- [ ] `client_secret` 不在前端产物/仓库/桌面中可见（仅 Worker `env`）。
- [ ] `refresh_token` 不出现在任何下发桌面的响应体中（仅存 KV）。
- [ ] `access_token` 不出现在任何 URL（仅 `/exchange`、`/refresh` 响应体）。
- [ ] `desktop_session_id` 高熵且仅存 DPAPI（不落明文文件/日志）。
- [ ] 下载页可从 `latest.json` 读取最新版本与 `Setup.exe` 链接（或经 Worker 代理 `GET /api/download/latest`）。

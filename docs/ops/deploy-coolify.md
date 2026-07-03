# 部署：后端服务端 → Coolify（步骤清单）

> 目标：把 `server/`（单 Go 二进制）部署到 Coolify（Docker）。依据 [服务端设计 §7](../design/server-design.md#7-部署coolify) 与 [调研 02](../research/02-coolify-udp.md)。
> 仍需按实际替换的值：`<VPS_PUBLIC_IP>`、以及各类密钥/机密（见下）。IdP 值来源见 [`idp-setup.md`](./idp-setup.md)。

> **本项目实际域名**（本文档已用下列真实值替换早期占位 `example.com`/`chat.example.com`/Keycloak）：
> - 前端 / 官网（EdgeOne 上的**纯静态 SPA**）= `https://lumen.nanako.com.cn`
> - 后端 / Go 服务端（资源服务器 + OIDC 中介）= `https://api.lumen.nanako.com.cn`；WebSocket = `wss://api.lumen.nanako.com.cn/ws`
> - **同站基础**：`lumen.*` 与 `api.lumen.*` 同属可注册域 `nanako.com.cn`（`.com.cn` 是公共后缀 / public suffix）→ 二者**同站（same-site）**，因此 `SameSite=Lax` host-only cookie + CORS 的设计成立。
> - **IdP**：`www.nanako.org` 上的一个**自建 OIDC Provider（不是 Keycloak）**；discovery 落在**根路径**（无 `/realms` 之类路径）：
>   - `issuer` = `https://www.nanako.org`（**必须精确匹配**，注意是 `www`、且无 `/realms` 路径）
>   - discovery = `https://www.nanako.org/.well-known/openid-configuration`
>   - authorize = `https://www.nanako.org/oauth/authorize`
>   - token = `https://www.nanako.org/api/v1/oauth/token`
>   - userinfo = `https://www.nanako.org/api/v1/oauth/userinfo`
>   - jwks_uri = `https://www.nanako.org/.well-known/jwks.json`
>   - 广告的 scopes = `openid profile email phone`（**注意：没有 `offline_access`** → 桌面刷新流程可能拿不到 `refresh_token`，见下方 caveat）
>   - audience = `lumen-api`（IdP 侧 client 必须配置把 `lumen-api` 放进 `access_token` 的 `aud`）
> - **IdP 回调地址**（在 IdP 为 confidential client 登记）：`https://api.lumen.nanako.com.cn/desktop/callback` 与 `https://api.lumen.nanako.com.cn/auth/callback`。
>
> ⚠️ **caveat（无 `offline_access`）**：该 IdP 广告的 scopes 里**不含 `offline_access`**。桌面登录流程可能因此**拿不到 `refresh_token`**——此时 `POST /api/desktop/refresh` 无法续期，桌面会话在 `access_token` 到期后需重新走登录回环。若需要长期免登，需在 IdP 侧为该 client 显式开启 refresh_token / offline_access 支持。

> **架构提示（唯一后端）**：这台 Go 服务器（`https://api.lumen.nanako.com.cn`）现在是**唯一后端**——既是资源服务器（JWT/JWKS 验签，原有），也是 **OIDC 登录中介**（新增，落在 `commit 82f344e`：`internal/broker` + `internal/secure` + store 中介表 + `cmd/lumen-server/main.go`）。它同时承载**桌面登录中介**与 **Web 账户中心**后端；`client_secret` 与 `refresh_token` **只**存在服务端（Postgres，`refresh_token` 用独立密钥静态加密）。EdgeOne Pages 只托管纯静态 SPA（`https://lumen.nanako.com.cn`）、**没有任何服务端逻辑/密钥**（见 [`deploy-edgeone.md`](./deploy-edgeone.md)）。因此本服务器还需**对官网源 `LUMEN_WEB_BASE_URL` 开 CORS（带凭据）**。

## 0. 前置
- [ ] 一台有**公网 IP** 的 VPS，已接入你的 Coolify 实例。
- [ ] 外部 OAuth2/OIDC（IdP，本项目为 `www.nanako.org` 上的自建 OIDC，非 Keycloak）已就绪，拿到 `issuer=https://www.nanako.org` / JWKS URL / `audience=lumen-api` / owner 的 `sub`，以及**官网 client 的 `client_id` / `client_secret`**（中介用它换 token）。
- [ ] IdP 侧回调登记为 `https://api.lumen.nanako.com.cn/desktop/callback` 与 `https://api.lumen.nanako.com.cn/auth/callback`。
- [ ] 云厂商**安全组/防火墙**可放行入站 `443/tcp` 与一个 UDP 端口（下用 `40000/udp`）。

## 1. 建 PostgreSQL 资源
1. Coolify 项目内 **+ New → Database → PostgreSQL**（16）。
2. 记下其**内部连接串**（同项目内服务用内部主机名，如 `postgres://lumen:***@<db-internal-host>:5432/lumen`）。
3. 该连接串填入下面的 `LUMEN_DATABASE_URL`（内网 `sslmode=disable`；跨网络托管 PG 用 `sslmode=require`）。

## 2. 新建应用（Git + Dockerfile）
1. **+ New → Application → Public/Private Repository** → 选 `Nanako1900/Lumen`，分支 `main`。
2. Build Pack：**Dockerfile**。
3. **Base Directory**：`/server`（Dockerfile 在 `server/Dockerfile`）。
4. 先不部署，先配下面的端口/环境变量/域名。

## 3. 端口（关键：HTTP 走 Traefik，WebRTC 走裸 UDP）
- [ ] **Ports Exposes** = `8090` （容器内明文 HTTP/WS，Traefik 据此反代、终结 TLS）。
  - ⚠️ **端口从 8080 改为 8090**：本宿主机上 `127.0.0.1:8080` 已被其它服务占用（容器内探测 `/api/v1/healthz` 返回 **502 Bad Gateway** 而非本服务的 200），故本服务改用 **8090**。三处必须同时一致：**Ports Exposes** `8090` = env **`LUMEN_LISTEN_ADDR=0.0.0.0:8090`** = **Health Check 的 Port** `8090`（见 §7）。
- [ ] **Ports Mappings** = `40000:40000/udp` （WebRTC 媒体，裸 UDP 直发宿主机、**不经 Traefik**）。
  - 若该版本 Coolify 的 Ports Mappings 不接受 `/udp`：改用 **Docker Compose 部署类型**，在 compose `ports:` 写 `"40000:40000/udp"`（见 [调研 02 §6.2](../research/02-coolify-udp.md)）。
  - ⚠️ 用了 Ports Mappings 会**失去 Rolling Updates**（重部署有短暂中断，语音会重连，可接受）。

## 4. 环境变量（Environment Variables）
按 [服务端设计 §6.1](../design/server-design.md#61-配置全部环境变量) 注入（缺必填启动即 fail-fast）：

**资源服务器 + WebRTC（原有）：**
```
LUMEN_DATABASE_URL      = postgres://lumen:***@<db-internal-host>:5432/lumen?sslmode=disable
LUMEN_OAUTH_ISSUER      = https://www.nanako.org                        # 自建 OIDC，issuer 精确匹配（www、无 /realms 路径）
LUMEN_OAUTH_JWKS_URL    = https://www.nanako.org/.well-known/jwks.json
LUMEN_OAUTH_AUDIENCE    = lumen-api
LUMEN_OWNER_SUBJECTS    = <owner-sub-1>,<owner-sub-2>
LUMEN_LISTEN_ADDR       = 0.0.0.0:8090          # 必须 0.0.0.0；端口须与 Ports Exposes / Health Check 一致（8080 被占→改 8090）
LUMEN_PUBLIC_IP         = <VPS_PUBLIC_IP>       # SetNAT1To1IPs 宣告，WebRTC 必需
LUMEN_WEBRTC_UDP_PORT   = 40000                 # 与 Ports Mappings 一致
LUMEN_PUBLIC_WS_URL     = wss://api.lumen.nanako.com.cn/ws
LUMEN_UPDATES_DIR       = /app/updates          # 可选（自动更新文件托管）
LUMEN_LOG_LEVEL         = info
```

**OIDC 登录中介 + 账户中心（新增，随 `commit 82f344e` 落地）：**
```
LUMEN_OAUTH_CLIENT_ID            = <IdP 官网 client_id>         # 官网 client_id（换 token 用）
LUMEN_OAUTH_CLIENT_SECRET        = <IdP 官网 client secret>      # 密钥：只存服务端，绝不进 EdgeOne/前端产物
LUMEN_OAUTH_DESKTOP_REDIRECT_URI = https://api.lumen.nanako.com.cn/desktop/callback
LUMEN_OAUTH_WEB_REDIRECT_URI     = https://api.lumen.nanako.com.cn/auth/callback
LUMEN_WEB_BASE_URL               = https://lumen.nanako.com.cn  # 官网源；中介据此发 CORS（带凭据）+ 回跳账户中心
LUMEN_SESSION_ENC_KEY            = <openssl rand -base64 32>     # 会话 cookie 加密密钥（AES-256-GCM）
LUMEN_REFRESH_ENC_KEY            = <openssl rand -base64 32>     # refresh_token 静态加密密钥（与上一把必须不同）
# 以下三项可选：缺省由 OIDC discovery 从 issuer（https://www.nanako.org/.well-known/openid-configuration）推导
# LUMEN_OAUTH_AUTHORIZE_URL      = https://www.nanako.org/oauth/authorize
# LUMEN_OAUTH_TOKEN_URL          = https://www.nanako.org/api/v1/oauth/token
# LUMEN_OAUTH_USERINFO_URL       = https://www.nanako.org/api/v1/oauth/userinfo
```

> ⚠️ **两把密钥必须不同**：`LUMEN_SESSION_ENC_KEY`（会话 cookie）与 `LUMEN_REFRESH_ENC_KEY`（refresh_token 静态加密）各生成一次，**不可复用同一值**：
> ```bash
> openssl rand -base64 32   # → LUMEN_SESSION_ENC_KEY
> openssl rand -base64 32   # → LUMEN_REFRESH_ENC_KEY（再跑一次，取不同值）
> ```
> `LUMEN_OAUTH_CLIENT_SECRET` / `LUMEN_REFRESH_ENC_KEY` / `LUMEN_SESSION_ENC_KEY` 都是**服务端专属密钥**——EdgeOne（纯静态）上不再有任何密钥。
>
> `LUMEN_OAUTH_AUDIENCE=lumen-api` 必须与中介换 token 时请求的 audience、以及 IdP 侧一致（见配置对齐矩阵 [`idp-setup.md`](./idp-setup.md)）。

> **本服务器现在同时提供的 HTTP 面**（同一 `:8090`、同一 CORS 中间件后）：
> - 资源服务器（Bearer）：`/api/v1/*`（`/api/v1/healthz`、`/api/v1/bootstrap`、`/api/v1/me` …）+ `/ws`。
> - 桌面中介：`GET /desktop/login`、`GET /desktop/callback`、`POST /api/desktop/exchange`、`POST /api/desktop/refresh`、`POST /api/desktop/logout`。
> - 账户中心（cookie 认证）：`GET /auth/login`、`GET /auth/callback`、`POST /auth/logout`、`GET /api/me`（cookie 版 me，区别于 Bearer 版 `/api/v1/me`）。
>
> **CORS（跨源账户中心）**：官网 SPA 部署在 `lumen.nanako.com.cn`，跨源调用本中介（`api.lumen.nanako.com.cn`）。二者为**同站**（same-site，同一可注册域 `nanako.com.cn`，`.com.cn` 为公共后缀），故：会话 cookie = `SameSite=Lax` + `HttpOnly` + `Secure` + `Path=/` + **host-only（不设 `Domain`）**；中介**仅**对精确的 `LUMEN_WEB_BASE_URL` 源发 CORS 且带凭据；SPA 的 XHR 用 `credentials:'include'`，`/auth/login`·`/auth/callback` 走顶层导航。桌面握手线格式**逐字节保持不变**，host 为 `api.lumen.nanako.com.cn`。

## 5. 域名与 TLS
- [ ] **Domains (FQDN)** = `https://api.lumen.nanako.com.cn` → Traefik 自动签 Let's Encrypt、强制 HTTPS；对外即 `https://` + `wss://`。
- [ ] DNS：`api.lumen.nanako.com.cn` A 记录指向 `<VPS_PUBLIC_IP>`。

## 6. 持久化（自动更新文件，可选）
- [ ] **Persistent Storage** 挂载容器路径 `/app/updates`（配合 `LUMEN_UPDATES_DIR`，`GET /updates/` 静态托管客户端更新包）。v0 若暂不做自动更新可跳过。
- 注意：数据库持久化由**第 1 步的 PostgreSQL 资源**负责，应用容器本身**无需**数据卷。

## 7. 健康检查
- [ ] **Health Check**：Type `HTTP`、Method `GET`、Scheme `http`、**Host `127.0.0.1`**（**不要用 `localhost`**——容器内可能优先解析成 IPv6 `::1`，而服务只绑 IPv4）、**Port `8090`**、Path `/api/v1/healthz`、Return Code `200`、Response Text **留空**、Start Period `30`。
  - ⚠️ Coolify 生成的探测命令形如 `curl ... || wget ...`；alpine 无 `curl`（会 `curl: not found`），靠 `wget` 兜底，所以 Host/Port/Path 必须精确对上正在监听的 `0.0.0.0:8090`。
  - 改动健康检查后**必须 Redeploy** 才生效（探针在容器创建时写入）。

## 8. 防火墙 / 安全组
- [ ] 云安全组放行入站 **`443/tcp`**（HTTPS/WSS）+ **`40000/udp`**（WebRTC）。
- [ ] ⚠️ Docker 的 iptables 会绕过主机 UFW —— **优先用云安全组**；若用 UFW 需配 `ufw-docker`。

## 9. 部署 + 验证
1. 点 **Deploy**。查看 Build/Runtime 日志：应看到 `listening addr=0.0.0.0:8090 udp=40000`，且**启动时幂等建表 + 种子频道**（大厅 / 开黑1）；中介表（会话/握手/refresh）也在此幂等建好。
2. 校验（用仓库 [`scripts/`](../../scripts)）：
   - `GET https://api.lumen.nanako.com.cn/api/v1/healthz` → `200`。
   - 带真实 IdP 的 `access_token`（`aud=lumen-api`）打 `GET /api/v1/bootstrap` → 返回 me/channels/members；WS 首帧 `auth` → 收到 `auth_ok`。参见 [`verify-login.md`](./verify-login.md)、`scripts/smoke-server.sh`。
   - **桌面中介**：`GET https://api.lumen.nanako.com.cn/desktop/login?redirect_uri=http://127.0.0.1:<port>/cb&state=...&challenge=...` → IdP 登录 → 回环收到 `?handoff_code=...`；`POST https://api.lumen.nanako.com.cn/api/desktop/exchange` → 返回 `{access_token, expires_in, desktop_session_id, profile}`（`scripts/verify-handoff.sh`）。
   - **账户中心（跨源）**：浏览器在 `https://lumen.nanako.com.cn/account` 未登录跳 `/auth/login`（顶层导航到 `api.lumen.nanako.com.cn`）→ IdP 登录 → 回 `lumen.nanako.com.cn/account`；`GET https://api.lumen.nanako.com.cn/api/me`（`credentials:'include'`）返回资料。
3. 改环境变量后需**重新部署**才生效。

## 10. 已知与后续
- **WebRTC 全链路语音**需 Windows 客户端（本批未做）才能端到端验证；现可验证 REST/WS/healthz/JWKS 验签 + 中介登录回环。
- 三方（IdP / 官网 SPA / 本服务端 = 资源服务器 + 中介）的 `issuer`/`audience`/域名/回调对齐见 [`idp-setup.md`](./idp-setup.md) 的对齐矩阵。
- 桌面握手线格式**逐字节保持不变**，未来 Windows 客户端改动仅为 base URL（指向 `api.lumen.nanako.com.cn`）。
- EdgeOne（纯静态）不再托管任何函数/密钥；前端如何部署见 [`deploy-edgeone.md`](./deploy-edgeone.md)。

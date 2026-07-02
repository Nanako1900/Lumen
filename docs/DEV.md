# 本地开发指南（DEV）

> 关联 Issue: #3 ｜ 依据设计: [`server-design.md §6.1`](./design/server-design.md#61-配置全部环境变量) / [`client-design.md §2.1`](./design/client-design.md#21-客户端配置来源)
>
> 本文说明如何在本地起 **服务端**（Go，含 **OIDC 登录中介 + 账户中心**）与 **官网（纯静态 SPA）**，以及所需环境变量。客户端（Wails）需 Windows + WebView2，另见 [`client-design.md`](./design/client-design.md)。
>
> **架构提示**：官网（`website/`）现在是**纯静态 SPA**——本地只跑 Vite（`npm run dev`），**没有 wrangler / Pages Functions / KV / `.dev.vars`**。登录中介、账户中心、OIDC、`client_secret`、`refresh_token` 都在 **Go 服务器**里（`go run ./cmd/lumen-server`，随 `commit 82f344e` 落地的 `internal/broker` + `internal/secure`）。账户中心 / 桌面登录的本地联调都指向 Go 服务器。
>
> **占位说明**：`example.com`/`chat.example.com`/`auth.example.com` 为占位域名，本地开发用 `localhost` 覆盖；IdP 登记与三方配置对齐见 [`docs/ops/idp-setup.md`](./ops/idp-setup.md)。**不要提交任何真实密钥**——`.env` 已被 `.gitignore` 忽略。

---

## 0. 前置工具

| 工具 | 版本 | 用于 |
|------|------|------|
| Go | 1.23（CI 口径；设计最低 1.22） | 服务端 / 客户端外壳 |
| Node.js | 20（LTS） | 官网 / 客户端前端 |
| PostgreSQL | 14+ | 服务端持久化 + 中介表（本地可用 Docker） |
| Docker | 任意近版 | 起本地 PostgreSQL（可选） |
| Wails CLI | v2（`go install .../wails@latest`） | 客户端（仅 Windows） |

> 仓库结构：`server/`（Go 服务端 = 资源服务器 + OIDC 中介 + 账户中心）、`website/`（纯静态 React SPA，Vite）、`client/`（Wails，Windows）、`docs/`（设计与运维文档）。官网本地开发**只需 Vite**，不再需要 wrangler / Miniflare。

---

## 1. 服务端（`server/`）

### 1.1 起本地 PostgreSQL（Docker，可选）

```bash
docker run -d --name lumen-pg \
  -e POSTGRES_USER=lumen -e POSTGRES_PASSWORD=lumen -e POSTGRES_DB=lumen \
  -p 5432:5432 postgres:16-alpine
```

对应连接串：`postgres://lumen:lumen@127.0.0.1:5432/lumen?sslmode=disable`。

### 1.2 配置环境变量

复制 `server/.env.example` 为 `server/.env` 并按需填值（`.env` 已被忽略，不会提交）。

> 若 `server/.env.example` 尚未随服务端分支落地，可先用[附录 A](#附录-a服务端-env-占位清单)的占位清单手动导出这些变量。服务端启动时对**必填项** fail-fast（缺失即退出并报缺哪些）。

本地最小必填（值为占位，按你的 IdP 替换 issuer/jwks/audience/owner）：

```bash
export LUMEN_OAUTH_ISSUER="https://auth.example.com/realms/lumen"
export LUMEN_OAUTH_JWKS_URL="https://auth.example.com/realms/lumen/protocol/openid-connect/certs"
export LUMEN_OAUTH_AUDIENCE="lumen-api"
export LUMEN_OWNER_SUBJECTS="sub-abc,sub-def"
export LUMEN_LISTEN_ADDR="0.0.0.0:8080"
export LUMEN_DATABASE_URL="postgres://lumen:lumen@127.0.0.1:5432/lumen?sslmode=disable"
export LUMEN_PUBLIC_IP="127.0.0.1"          # 本地开发用回环即可；线上填 VPS 公网 IP
export LUMEN_WEBRTC_UDP_PORT="40000"
# 可选：
export LUMEN_PUBLIC_WS_URL="ws://127.0.0.1:8080/ws"   # 本地非 TLS 用 ws://
export LUMEN_LOG_LEVEL="debug"

# ── OIDC 登录中介 + 账户中心（本地跑中介必填）──
export LUMEN_OAUTH_CLIENT_ID="lumen-website"
export LUMEN_OAUTH_CLIENT_SECRET="__dev_secret__"                        # 仅本地，勿提交
export LUMEN_OAUTH_DESKTOP_REDIRECT_URI="http://127.0.0.1:8080/desktop/callback"
export LUMEN_OAUTH_WEB_REDIRECT_URI="http://127.0.0.1:8080/auth/callback"
export LUMEN_WEB_BASE_URL="http://localhost:5173"                        # 本地 Vite 源；中介据此发 CORS + 回跳账户中心
export LUMEN_SESSION_ENC_KEY="$(openssl rand -base64 32)"                # 会话 cookie 密钥
export LUMEN_REFRESH_ENC_KEY="$(openssl rand -base64 32)"               # refresh_token 密钥（必须与上一把不同）
# 以下三项可选：缺省由 OIDC discovery 从 issuer 推导
# export LUMEN_OAUTH_AUTHORIZE_URL="https://auth.example.com/realms/lumen/protocol/openid-connect/auth"
# export LUMEN_OAUTH_TOKEN_URL="https://auth.example.com/realms/lumen/protocol/openid-connect/token"
# export LUMEN_OAUTH_USERINFO_URL="https://auth.example.com/realms/lumen/protocol/openid-connect/userinfo"
```

> 完整变量含义/必填性见 [`server-design.md §6.1`](./design/server-design.md#61-配置全部环境变量) 与[附录 A](#附录-a服务端-env-占位清单)。JWKS URL 必须 HTTPS（服务端红线）；本地若无外部 IdP，可用 §4 的测试 JWT 方式跑纯脚本校验（不连真实 IdP）。中介的两把加密密钥 `LUMEN_SESSION_ENC_KEY` / `LUMEN_REFRESH_ENC_KEY` **必须不同**（各 `openssl rand -base64 32` 一次）。本地做完整 IdP 往返时，记得在 IdP 临时登记本地回调（`http://127.0.0.1:8080/desktop/callback`、`http://127.0.0.1:8080/auth/callback`），或使用 IdP 的预览/开发 client。

### 1.3 运行

```bash
cd server
go run ./cmd/lumen-server
# 或
go build -o /tmp/lumen-server ./cmd/lumen-server && /tmp/lumen-server
```

启动后：

- REST/WS 监听 `0.0.0.0:8080`（健康检查 `GET /api/v1/healthz`）。
- WebRTC 媒体监听 `0.0.0.0:40000/udp`。
- 首次启动自动执行 DDL（含中介：会话/握手/refresh 表）并幂等种子默认频道（text『大厅』+ voice『开黑1』）。
- 同一 `:8080` 还提供中介/账户中心面：`/desktop/login`、`/desktop/callback`、`/api/desktop/exchange|refresh|logout`、`/auth/login`、`/auth/callback`、`/auth/logout`、`/api/me`（cookie 版）。账户中心的跨源 CORS 只对 `LUMEN_WEB_BASE_URL` 源开放（带凭据）。

### 1.4 冒烟核对

```bash
curl -s http://127.0.0.1:8080/api/v1/healthz    # 期望 {"success":true,"data":{"status":"ok"},"error":null}
```

更完整的（带 Bearer 的 `/bootstrap`、WS 首帧 `auth`）见 [`docs/ops/verify-login.md`](./ops/verify-login.md) 与 `scripts/`。

---

## 2. 官网（`website/`）——纯静态 SPA

官网 = **纯静态 React SPA**（`src/`）。本地**只跑 Vite**——没有 wrangler / Pages Functions / KV / `.dev.vars`。所有 `/auth/*`、`/desktop/*`、`/api/*` 端点都在 **Go 服务器**上（§1），本地即 `http://127.0.0.1:8080`。

### 2.1 安装依赖

```bash
cd website
npm ci        # 首次可用 npm install
```

### 2.2 本地环境变量（仅构建期 `VITE_*` 公开变量）

SPA **没有任何密钥**。本地用 `website/.env.local`（**已被 `.gitignore` 忽略**）提供 Vite 的 `VITE_*` 公开变量，指向本地 Go 服务器：

```dotenv
# website/.env.local —— 仅本地；这些是构建期公开值，不是密钥
VITE_WEB_BASE_URL="http://localhost:5173"
VITE_API_BASE_URL="http://127.0.0.1:8080"                 # Go 服务器 = 资源服务器 + 中介 + 账户中心
VITE_UPDATES_URL="http://127.0.0.1:8080/updates/latest.json"
```

> 精确变量名以 `website/.env.example` 为准（Vite 只暴露 `VITE_` 前缀变量到前端）。`client_secret` / 会话密钥 / `refresh` 密钥全在 Go 服务器（见 §1.2 的 `LUMEN_OAUTH_*` / `LUMEN_*_ENC_KEY`），**不在官网**。

### 2.3 运行

```bash
cd website
npm run dev                         # Vite dev server，默认 http://localhost:5173
```

> 账户中心/桌面登录端点由 **Go 服务器**（§1.3，`http://127.0.0.1:8080`）提供，与 Vite dev server **分别**运行；SPA 跨源调用它（`credentials:'include'`）。要联调登录，先起 Go 服务器（含 §1.2 的 `LUMEN_OAUTH_*` 中介 env），再起 Vite。中介端点契约见 [`web-design.md §5`](./design/web-design.md#5-web-中介登录桌面)。

### 2.4 账户中心 / 桌面登录本地冒烟（打 Go 服务器）

端点在 `http://127.0.0.1:8080`（不是 Vite 的 5173）：

```bash
# 桌面 /desktop/login 必须拒绝非回环 redirect_uri（期望 400）
curl -s -o /dev/null -w '%{http_code}\n' \
  "http://127.0.0.1:8080/desktop/login?redirect_uri=https://evil.example&state=x&challenge=y"
# 期望: 400
```

- **账户中心（跨源）**：浏览器在 `http://localhost:5173/account` 未登录跳 `/auth/login`（顶层导航到 `127.0.0.1:8080`）→ IdP 登录 → 回 SPA；`GET http://127.0.0.1:8080/api/me`（`credentials:'include'`）返回资料。
- 半自动的完整登录回环核对见 [`docs/ops/verify-login.md`](./ops/verify-login.md)。

---

## 3. 客户端（`client/`，仅 Windows）

客户端不再内置 IdP issuer/client_id/scope，只需三个 base URL（[`client-design.md §2.1`](./design/client-design.md#21-客户端配置来源)）：

```
LUMEN_WEB_BASE_URL = https://example.com            # 官网 SPA（本地: http://localhost:5173）
LUMEN_API_BASE_URL = https://chat.example.com/api/v1 # 本地: http://127.0.0.1:8080/api/v1
LUMEN_WS_URL       = wss://chat.example.com/ws        # 本地: ws://127.0.0.1:8080/ws
```

运行（Windows，装好 Wails v2 工具链 + WebView2）：

```bash
cd client
wails dev      # 开发（热重载）
wails build    # 构建（-nsis 出安装包；CGO 需 mingw/gcc）
```

> 客户端 `.env.example`（若随客户端分支落地）位于 `client/.env.example`。客户端全链路语音冒烟需真实 Windows 客户端，属 Issue #8 的剩余项（见 [`docs/ops/verify-login.md §4`](./ops/verify-login.md#4-需客户端的剩余项属-8)）。
>
> 桌面登录中介现在也在 Go 服务器（`chat.example.com`）；握手线格式**逐字节不变**，未来客户端改动仅 `LUMEN_WEB_BASE_URL`（原来指官网中介，现指 `chat.example.com`）。

---

## 4. 不依赖客户端的集成校验（脚本）

即使没有 Windows 客户端，也可校验「服务端 + 官网」侧：

| 脚本 | 用途 |
|------|------|
| `scripts/smoke-server.sh` | 打服务端 `/api/v1/healthz`，带 Bearer 打 `/api/v1/bootstrap`，WS 首帧 `auth` 验 `auth_ok` |
| `scripts/verify-handoff.sh` | 半自动核对桌面登录回环（浏览器手动 → `handoff_code` → `POST /api/desktop/exchange`），并校验 `access_token` 不出现在回环 URL |
| `scripts/decode-jwt.sh` | 本地解码 JWT header/payload，核对 `alg`/`iss`/`aud`/`exp`（不验签、不联网） |

用法与验收细节见 [`docs/ops/verify-login.md`](./ops/verify-login.md)。测试 JWT 的生成方式（用一个本地 RSA 私钥 + 自建 JWKS）也在该文档，便于**无真实 IdP** 时校验服务端验签路径。

---

## 5. CI 本地预检（可选）

推 PR 前可本地跑与 CI 等价的命令（[`.github/workflows/ci.yml`](../.github/workflows/ci.yml)）：

```bash
# 服务端（代码落地后）
cd server && gofmt -l . && go build ./... && go vet ./... && go test -race ./...

# 官网（代码落地后）
cd website && npm ci && npm run build && (npm run | grep -qE '^\s*test' && npm test || echo "no test script")
```

> CI 用「存在守卫」探测各端是否已落地代码：`server/go.mod` / `website/package.json` / `client/wails.json`。守卫未命中则该 job 步骤全 skip，骨架期 CI 为绿；代码合入后自动转真实 build/test。

---

## 附录 A：服务端 env 占位清单

> 当 `server/.env.example` 尚未随服务端分支落地时的**占位参考**（真值以 [`server-design.md §6.1`](./design/server-design.md#61-配置全部环境变量) 与服务端分支交付的 `.env.example` 为准；此处不代写服务端源码，仅列变量便于本地导出）。**不含任何真实密钥**。

```dotenv
# ── 必填 ──
LUMEN_OAUTH_ISSUER=https://auth.example.com/realms/lumen
LUMEN_OAUTH_JWKS_URL=https://auth.example.com/realms/lumen/protocol/openid-connect/certs
LUMEN_OAUTH_AUDIENCE=lumen-api
LUMEN_OWNER_SUBJECTS=sub-abc,sub-def
LUMEN_LISTEN_ADDR=0.0.0.0:8080
LUMEN_DATABASE_URL=postgres://lumen:lumen@127.0.0.1:5432/lumen?sslmode=disable
LUMEN_PUBLIC_IP=127.0.0.1
LUMEN_WEBRTC_UDP_PORT=40000

# ── 中介 + 账户中心（本地跑中介必填）──
LUMEN_OAUTH_CLIENT_ID=lumen-website
LUMEN_OAUTH_CLIENT_SECRET=__dev_secret__                 # 仅本地，勿提交
LUMEN_OAUTH_DESKTOP_REDIRECT_URI=http://127.0.0.1:8080/desktop/callback
LUMEN_OAUTH_WEB_REDIRECT_URI=http://127.0.0.1:8080/auth/callback
LUMEN_WEB_BASE_URL=http://localhost:5173
LUMEN_SESSION_ENC_KEY=__set_via_openssl_rand_base64_32__  # 两把密钥必须不同
LUMEN_REFRESH_ENC_KEY=__set_via_openssl_rand_base64_32__  # 与上一把不同

# ── 可选 ──
LUMEN_OAUTH_USERINFO_URL=https://auth.example.com/realms/lumen/protocol/openid-connect/userinfo
LUMEN_OAUTH_AUTHORIZE_URL=https://auth.example.com/realms/lumen/protocol/openid-connect/auth   # 缺省由 discovery 推导
LUMEN_OAUTH_TOKEN_URL=https://auth.example.com/realms/lumen/protocol/openid-connect/token      # 缺省由 discovery 推导
LUMEN_PUBLIC_WS_URL=ws://127.0.0.1:8080/ws
LUMEN_UPDATES_DIR=/app/updates
LUMEN_LOG_LEVEL=info
```

## 附录 B：官网前端 env 占位清单（纯静态，仅 `VITE_*` 公开变量）

> 官网现在是**纯静态 SPA**，**没有任何密钥、没有 OIDC 变量、没有 KV/Worker**——OIDC/`client_secret`/会话密钥/`refresh` 密钥全部搬到 Go 服务器（见附录 A 的 `LUMEN_OAUTH_*` / `LUMEN_*_ENC_KEY`，及 [`deploy-coolify.md`](./ops/deploy-coolify.md)）。前端只需构建期 `VITE_*` 公开变量（真值以 `website/.env.example` 为准）。本地用 `website/.env.local`（见 §2.2）。**这些是公开值，不是密钥。**

```dotenv
# 构建期公开变量（会被打进前端产物；只放非敏感值）
VITE_WEB_BASE_URL=https://example.com               # 本地: http://localhost:5173
VITE_API_BASE_URL=https://chat.example.com          # 本地: http://127.0.0.1:8080
VITE_UPDATES_URL=https://chat.example.com/updates/latest.json
```

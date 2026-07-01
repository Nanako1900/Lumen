# 本地开发指南（DEV）

> 关联 Issue: #3 ｜ 依据设计: [`server-design.md §6.1`](./design/server-design.md#61-配置全部环境变量) / [`web-design.md §9`](./design/web-design.md#9-配置环境变量kvsecrets) / [`client-design.md §2.1`](./design/client-design.md#21-客户端配置来源)
>
> 本文说明如何在本地起 **服务端** 与 **官网（含 Cloudflare Worker）**，以及所需环境变量。客户端（Wails）需 Windows + WebView2，另见 [`client-design.md`](./design/client-design.md)。
>
> **占位说明**：`example.com`/`chat.example.com`/`auth.example.com` 为占位域名，本地开发用 `localhost` 覆盖；IdP 登记与三方配置对齐见 [`docs/ops/idp-setup.md`](./ops/idp-setup.md)。**不要提交任何真实密钥**——`.env` / `.dev.vars` 已被 `.gitignore` 忽略。

---

## 0. 前置工具

| 工具 | 版本 | 用于 |
|------|------|------|
| Go | 1.23（CI 口径；设计最低 1.22） | 服务端 / 客户端外壳 |
| Node.js | 20（LTS） | 官网 / 客户端前端 |
| PostgreSQL | 14+ | 服务端持久化（本地可用 Docker） |
| Docker | 任意近版 | 起本地 PostgreSQL（可选） |
| wrangler | 3.x（`npm i -D wrangler` 或全局） | 本地跑官网 Worker（Pages Functions） |
| Wails CLI | v2（`go install .../wails@latest`） | 客户端（仅 Windows） |

> 仓库结构：`server/`（Go 服务端）、`website/`（React + Cloudflare Pages/Functions）、`client/`（Wails，Windows）、`docs/`（设计与运维文档）。当前仅文档/骨架已落地，代码由各功能分支填充；本文步骤在对应端代码落地后即可照做。

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
```

> 完整变量含义/必填性见 [`server-design.md §6.1`](./design/server-design.md#61-配置全部环境变量) 与[附录 A](#附录-a服务端-env-占位清单)。JWKS URL 必须 HTTPS（服务端红线）；本地若无外部 IdP，可用 §4 的测试 JWT 方式跑纯脚本校验（不连真实 IdP）。

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
- 首次启动自动执行 DDL（三表）并幂等种子默认频道（text『大厅』+ voice『开黑1』）。

### 1.4 冒烟核对

```bash
curl -s http://127.0.0.1:8080/api/v1/healthz    # 期望 {"success":true,"data":{"status":"ok"},"error":null}
```

更完整的（带 Bearer 的 `/bootstrap`、WS 首帧 `auth`）见 [`docs/ops/verify-login.md`](./ops/verify-login.md) 与 `scripts/`。

---

## 2. 官网（`website/`）

官网 = React SPA（`src/`）+ Cloudflare Pages Functions（`functions/`，即 Worker）。本地用 wrangler 同时跑静态与 Functions。

### 2.1 安装依赖

```bash
cd website
npm ci        # 首次可用 npm install
```

### 2.2 本地环境变量与 Secrets（`.dev.vars`）

Worker 端点需要 OIDC 配置与 Secret。本地用 `website/.dev.vars`（**已被 `.gitignore` 忽略**）提供，wrangler 会把它注入 `env`：

```dotenv
# website/.dev.vars —— 本地开发用，勿提交（含 client_secret）
OIDC_ISSUER="https://auth.example.com/realms/lumen"
OIDC_AUTHORIZE_URL="https://auth.example.com/realms/lumen/protocol/openid-connect/auth"
OIDC_TOKEN_URL="https://auth.example.com/realms/lumen/protocol/openid-connect/token"
OIDC_USERINFO_URL="https://auth.example.com/realms/lumen/protocol/openid-connect/userinfo"
OIDC_CLIENT_ID="lumen-website"
OIDC_CLIENT_SECRET="__dev_secret__"                       # Secret：仅本地，勿提交
OIDC_AUDIENCE="lumen-api"
OIDC_DESKTOP_REDIRECT_URI="http://localhost:8788/desktop/callback"   # 本地端口
OIDC_WEB_REDIRECT_URI="http://localhost:8788/auth/callback"
WEB_BASE_URL="http://localhost:8788"
UPDATES_LATEST_URL="http://127.0.0.1:8080/updates/latest.json"
SESSION_ENC_KEY="__dev_session_key__"                     # Secret：仅本地，勿提交
```

> 生产用 `wrangler pages secret put OIDC_CLIENT_SECRET` / `... SESSION_ENC_KEY` 注入 Secret，非密项用 Pages 环境变量或 `wrangler.toml [vars]`。清单与三方对齐见 [`docs/ops/idp-setup.md §4`](./ops/idp-setup.md#4-配置对齐矩阵三方一致)。
>
> 本地做完整 IdP 往返时，记得在 IdP 临时登记本地回调（`http://localhost:8788/desktop/callback`、`http://localhost:8788/auth/callback`），或使用 IdP 的预览/开发 client。

### 2.3 KV（本地）

wrangler 本地默认用 **Miniflare 内存/本地 KV**（无需真实 Cloudflare KV）。`wrangler.toml` 声明 `HANDOFF`/`SESSIONS` 绑定即可；本地 `--local`（默认）会用本地模拟实现（数据在本地目录，重启可能清空）。

### 2.4 运行

**方式 A（推荐，静态 + Functions 一起）**：

```bash
cd website
npm run build                       # 产出 dist/
npx wrangler pages dev dist         # 默认 http://localhost:8788，自动挂 functions/ 与 .dev.vars
```

**方式 B（仅前端热更新，不含 Functions）**：

```bash
cd website
npm run dev                         # Vite dev server（仅静态；/desktop/*、/auth/* 端点不可用）
```

> 端点（`/desktop/login`、`/desktop/callback`、`/api/desktop/exchange|refresh|logout`、`/auth/*`、`/api/me`、可选 `/api/download/latest`）只有在 `wrangler pages dev`（方式 A）下才生效。契约见 [`web-design.md §5`](./design/web-design.md#5-web-中介登录桌面)。

### 2.5 官网端点冒烟

```bash
# /desktop/login 必须拒绝非回环 redirect_uri（期望 400）
curl -s -o /dev/null -w '%{http_code}\n' \
  "http://localhost:8788/desktop/login?redirect_uri=https://evil.example&state=x&challenge=y"
# 期望: 400
```

半自动的完整登录回环核对见 [`docs/ops/verify-login.md`](./ops/verify-login.md)。

---

## 3. 客户端（`client/`，仅 Windows）

客户端不再内置 IdP issuer/client_id/scope，只需三个 base URL（[`client-design.md §2.1`](./design/client-design.md#21-客户端配置来源)）：

```
LUMEN_WEB_BASE_URL = https://example.com            # 官网中介（本地: http://localhost:8788）
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

# ── 可选 ──
LUMEN_OAUTH_USERINFO_URL=https://auth.example.com/realms/lumen/protocol/openid-connect/userinfo
LUMEN_PUBLIC_WS_URL=ws://127.0.0.1:8080/ws
LUMEN_UPDATES_DIR=/app/updates
LUMEN_LOG_LEVEL=info
```

## 附录 B：官网 Worker env/Secret 占位清单

> 当 `website/.env.example` 尚未随官网分支落地时的占位参考（真值以 [`web-design.md §9.1`](./design/web-design.md#91-worker-环境变量与-secret-清单) 与官网分支交付为准）。本地用 `website/.dev.vars`（见 §2.2）。**Secret 项绝不提交**。

```dotenv
# 非密（Pages 环境变量 / wrangler.toml [vars]）
OIDC_ISSUER=https://auth.example.com/realms/lumen
OIDC_AUTHORIZE_URL=https://auth.example.com/realms/lumen/protocol/openid-connect/auth
OIDC_TOKEN_URL=https://auth.example.com/realms/lumen/protocol/openid-connect/token
OIDC_USERINFO_URL=https://auth.example.com/realms/lumen/protocol/openid-connect/userinfo
OIDC_CLIENT_ID=lumen-website
OIDC_AUDIENCE=lumen-api
OIDC_DESKTOP_REDIRECT_URI=https://example.com/desktop/callback
OIDC_WEB_REDIRECT_URI=https://example.com/auth/callback
WEB_BASE_URL=https://example.com
UPDATES_LATEST_URL=https://chat.example.com/updates/latest.json

# Secret（wrangler pages secret put ...；绝不进仓库/前端产物）
OIDC_CLIENT_SECRET=__set_via_wrangler_secret__
SESSION_ENC_KEY=__set_via_wrangler_secret__
```

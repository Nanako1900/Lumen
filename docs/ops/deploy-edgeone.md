# 部署：官网前端 → 腾讯 EdgeOne Pages（步骤清单）

> 目标：把 `website/`（React 静态 SPA + Pages Functions 登录中介 + KV）部署到腾讯 **EdgeOne Pages**。
> ✅ 已按 EdgeOne 官方文档核实（2026-07），下列约定不再是「Cloudflare 兼容假设」，可直接执行。
> 占位：`example.com`（官网域名）、`chat.example.com`（API，用于下载页）、IdP 值——按实际替换。

## 0. 已核实的关键事实（决定下面每一步）
- **单仓多项目（monorepo）**：官网在 `website/` 子目录 → 必须在**控制台设「根目录 Root directory = `website`」**。EdgeOne 的「根目录」只有控制台有，`edgeone.json` **没有** `root` 字段。构建/输出目录都相对该根目录。
- **`edgeone.json` 只放构建配置**：真实字段是顶层 `buildCommand` / `installCommand` / `outputDirectory` / `nodeVersion`（及 `redirects`/`rewrites`/`headers`）。**KV 绑定、环境变量、加密变量都在控制台配，不写进 `edgeone.json`/仓库**。仓库 `website/edgeone.json` 已改成此形态。
- **Functions 路由**：`functions/` 文件路由；扁平文件即可（`functions/auth/login.ts → /auth/login`），支持命名导出 `onRequestGet/onRequestPost/…`（本项目正是此写法）。KV/变量经 `context.env` 注入。
- **KV 无原生 TTL**：`put(key,value)` 无过期参数；本项目已用「值内嵌 `exp` + 读取时惰性删除」保证过期语义（`functions/_lib/kv.ts`），不依赖平台 TTL。
- **测试不上边缘**：`*.test.ts` 与 `testutil.ts` 已移出 `functions/`（迁到 `website/test/`），避免把 `vitest` 打进边缘产物。
- **SPA 深链回退**：客户端路由（`/download` `/account` `/help` `/privacy` `/terms` `/about`）用 `edgeone.json` 的 `rewrites` **逐条精确**回退到 `/index.html`（不含通配 → **绝不遮蔽** `/api`·`/auth`·`/desktop` 函数路由，也不碰静态资源）；`public/_redirects` 的 `/*` 作额外兜底（EdgeOne 沿用 `_redirects` 则覆盖未知路径，不沿用则被忽略）。语法依据：Tencent `pages-templates/examples/custom-rules/edgeone.json`（`rewrites` 支持字面量精确 source）。

## 1. 建 EdgeOne Pages 项目（连 Git）
1. EdgeOne 控制台 → **Pages → 新建项目 → 导入 Git 仓库** → 选 `Nanako1900/Lumen`，分支 `main`。
2. **构建部署配置**（关键，逐项填）：
   - **根目录（Root directory）= `website`** ← 最关键；不填会在仓库根跑构建，报 `npm install ... no package.json` 并退化成「pure project」（无函数）。
   - **安装命令 = `npm ci`**、**构建命令 = `npm run build`**、**输出目录 = `dist`**（均相对根目录）。
   - **Node 版本 = 20.18.0**（EdgeOne 预装版本之一）。
   - 说明：以上四项 `website/edgeone.json` 已声明可覆盖控制台；但**根目录仍须在控制台设**（配置文件无此字段）。
   - **框架预设**：选「无 / Others」（本项目是 Vite，不要选 Next.js 等，以免覆盖构建/输出）。

## 2. 建 KV 命名空间并绑定（控制台）
1. EdgeOne → **KV 存储**：开通 KV 账户，新建两个命名空间（例：`lumen-handoff`、`lumen-sessions`）。
2. 在 Pages 项目 → **KV 绑定**：把命名空间绑定到函数运行时变量名 —— **变量名必须是 `HANDOFF` 与 `SESSIONS`**（代码用 `env.HANDOFF` / `env.SESSIONS`）。
   - handoff 的短期过期由代码值内嵌 `exp` 兜底（见事实清单），不需要 KV 原生 TTL。

## 3. 环境变量与加密变量（控制台，不进仓库）
**加密变量（Secret / 敏感变量）——绝不写入仓库或前端产物：**
```
OIDC_CLIENT_SECRET   = <IdP 官网 client 的 secret>
SESSION_ENC_KEY      = <openssl rand -base64 32>   # 账户中心会话 cookie 的 AES-256-GCM 密钥
```
**普通环境变量：**
```
OIDC_ISSUER                = https://<IdP>/realms/lumen
OIDC_AUTHORIZE_URL         = https://<IdP>/realms/lumen/protocol/openid-connect/auth
OIDC_TOKEN_URL             = https://<IdP>/realms/lumen/protocol/openid-connect/token
OIDC_USERINFO_URL          = https://<IdP>/realms/lumen/protocol/openid-connect/userinfo
OIDC_CLIENT_ID             = lumen-website
OIDC_AUDIENCE              = lumen-api
OIDC_DESKTOP_REDIRECT_URI  = https://example.com/desktop/callback
OIDC_WEB_REDIRECT_URI      = https://example.com/auth/callback
WEB_BASE_URL               = https://example.com
UPDATES_LATEST_URL         = https://chat.example.com/updates/latest.json
```
> 变量名与 `website/functions/_lib/env.ts` 一一对应，缺任一必填项对应端点会抛 `missing required environment variable`。前端构建若需 `VITE_*`，见 `website/.env.example`（构建期变量，非函数运行时变量）。

## 4. 域名与 HTTPS
- [ ] Pages 项目绑定 `example.com`（EdgeOne 边缘自动签发证书）。
- [ ] 按 EdgeOne 指引把 `example.com` 解析/接入 EdgeOne。

## 5. IdP 登记（与官网域名对齐）
- [ ] 在 IdP 登记两个回调：`https://example.com/desktop/callback`、`https://example.com/auth/callback`。
- [ ] 允许官网 client 令 `access_token.aud` 含 `lumen-api`（Keycloak audience mapper / Auth0·Logto audience 参数）。
- 详见 [`idp-setup.md`](./idp-setup.md)（Keycloak/Auth0/Logto 三例 + 三方对齐矩阵）。

## 6. 部署 + 验证
1. 触发部署，等构建完成。构建日志应能看到 `npm ci` 在 `website/` 成功、`vite build` 产出 `dist/`、并识别到 `functions/`（不再是 "pure project"）。
2. **账户中心（浏览器）**：访问 `https://example.com/account` → 未登录跳 `/auth/login` → IdP 登录 → 回 `/account` 显示头像/昵称；`GET /api/me` 返回资料；退出可用。
3. **桌面中介端点（无客户端也可半自动核对）**：用 [`verify-login.md`](./verify-login.md) + `scripts/verify-handoff.sh`：
   - 浏览器打开 `https://example.com/desktop/login?redirect_uri=http://127.0.0.1:<port>/cb&state=...&challenge=...` → IdP 登录 → 回环收到 `?handoff_code=...`；
   - `POST https://example.com/api/desktop/exchange {handoff_code, handoff_verifier}` → 返回 `{access_token, expires_in, desktop_session_id, profile}`；
   - 核对：`access_token` **不出现在任何回环 URL**；`handoff_code` 二次提交返回 404（一次性）。
4. 用上一步拿到的 `access_token` 打后端 `chat.example.com/api/v1/bootstrap`（验证前后端 `aud` 对齐）。
5. **SPA 深链**：浏览器地址栏直达 `https://example.com/account` 并刷新 → 应加载 SPA（非 404）；同时确认 `/auth/login`、`/api/me` 等**函数路由仍正常**（未被回退遮蔽）。若深链 404，检查 `edgeone.json` 的 `rewrites` 是否生效（第 0 节 SPA 回退）。

## 7. 客户端对接（后续，客户端就绪时）
- 客户端配置 `LUMEN_WEB_BASE_URL = https://example.com`、`LUMEN_API_BASE_URL = https://chat.example.com/api/v1`、`LUMEN_WS_URL = wss://chat.example.com/ws`。

## 8. 排障：本次遇到的构建错误
现象（构建日志）：
```
[builder] InstallCommand: npm install
npm error enoent Could not read package.json: /dev/shm/repo/lumen-fn-xxxx/package.json
[builder] "npm install" failed, exit code: 254
[cli] No server-handler detected, generating routes.json for pure project...
```
根因与修复：
- **根目录没设成 `website`** → 构建在仓库根执行，找不到 `package.json`（在 `website/`），也找不到 `functions/`，于是退化成纯静态。**修复：控制台把根目录设为 `website`**（第 1 步）。
- **旧 `edgeone.json` 用了 Cloudflare/wrangler 形态**（`build{}`、`kvNamespaces`、`vars`、`secrets`）→ EdgeOne 不识别这些字段。**修复：已改为 EdgeOne 真实字段**（`buildCommand`/`installCommand`/`outputDirectory`/`nodeVersion`），KV/变量/密钥改到控制台配（第 2、3 步）。

## 9. 变量总表（复制到控制台）
| 类别 | 名称 | 值/来源 |
|---|---|---|
| 构建 | 根目录 | `website` |
| 构建 | 安装命令 | `npm ci` |
| 构建 | 构建命令 | `npm run build` |
| 构建 | 输出目录 | `dist` |
| 构建 | Node 版本 | `20.18.0` |
| KV 绑定 | `HANDOFF` | → 命名空间 `lumen-handoff` |
| KV 绑定 | `SESSIONS` | → 命名空间 `lumen-sessions` |
| 加密变量 | `OIDC_CLIENT_SECRET` | IdP 官网 client secret |
| 加密变量 | `SESSION_ENC_KEY` | `openssl rand -base64 32` |
| 环境变量 | `OIDC_ISSUER` | `https://<IdP>/realms/lumen` |
| 环境变量 | `OIDC_AUTHORIZE_URL` | `.../protocol/openid-connect/auth` |
| 环境变量 | `OIDC_TOKEN_URL` | `.../protocol/openid-connect/token` |
| 环境变量 | `OIDC_USERINFO_URL` | `.../protocol/openid-connect/userinfo` |
| 环境变量 | `OIDC_CLIENT_ID` | `lumen-website` |
| 环境变量 | `OIDC_AUDIENCE` | `lumen-api` |
| 环境变量 | `OIDC_DESKTOP_REDIRECT_URI` | `https://example.com/desktop/callback` |
| 环境变量 | `OIDC_WEB_REDIRECT_URI` | `https://example.com/auth/callback` |
| 环境变量 | `WEB_BASE_URL` | `https://example.com` |
| 环境变量 | `UPDATES_LATEST_URL` | `https://chat.example.com/updates/latest.json` |

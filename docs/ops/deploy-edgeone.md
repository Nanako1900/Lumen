# 部署：官网前端 → 腾讯 EdgeOne Pages（步骤清单）

> 目标：把 `website/`（React 静态 SPA + Pages Functions 登录中介 + KV）部署到腾讯 **EdgeOne Pages**。
> ✅ 前提已满足：官网 EdgeOne 化已并入 `main`（PR #39，CI 绿）。可直接按本清单执行。
> 占位：`example.com`（官网域名）、`chat.example.com`（API，用于下载页）、IdP 值——按实际替换。
> 🔎 标注 `EDGEONE-VERIFY` 的项：以 EdgeOne 控制台/CLI 实际字段为准（本清单按 Cloudflare-Pages 兼容模型给出，落地时核对）。

## 0. 前置
- [ ] 腾讯云 EdgeOne 已开通、已接入官网域名 `example.com`。
- [ ] 外部 IdP 就绪，且官网作为 **confidential OIDC client**：拿到 `client_id` / `client_secret`，能登记回调、能让 `access_token` 的 `aud` 含 `lumen-api`（见 [`idp-setup.md`](./idp-setup.md)）。
- [ ] 后端已部署（[`deploy-coolify.md`](./deploy-coolify.md)），`chat.example.com/updates/latest.json` 可访问（下载页要用；无自动更新文件时下载页可先占位）。

## 1. 建 EdgeOne Pages 项目
1. EdgeOne 控制台 → **Pages → 新建项目 → 连接 Git 仓库** → 选 `Nanako1900/Lumen`，分支 `main`。 `EDGEONE-VERIFY`
2. 构建配置： `EDGEONE-VERIFY`
   - **根目录 / Base**：`website`
   - **构建命令**：`npm ci && npm run build`
   - **输出目录**：`dist`
   - **Functions 目录**：`functions`（按文件路由自动识别；若需在 `edgeone.json` 声明，见仓库 `website/edgeone.json`）

## 2. 建 KV 命名空间并绑定
1. EdgeOne → **KV 存储**：新建两个命名空间 `lumen-handoff`、`lumen-sessions`（生产）。建议再建预览用的独立命名空间。 `EDGEONE-VERIFY`
2. 在 Pages 项目**绑定**到函数环境：绑定名必须是 `HANDOFF` 与 `SESSIONS`（代码用 `env.HANDOFF` / `env.SESSIONS`）。 `EDGEONE-VERIFY`
   - 说明：handoff 的短期过期已由**值内嵌 `exp` + 读取时惰性删除**兜底（见 `website/functions/_lib/kv.ts`），**不依赖** EdgeOne KV 是否支持原生 TTL；若支持原生 TTL 则为额外兜底。

## 3. 环境变量与加密变量
**加密变量（Secret，绝不进仓库/前端）** `EDGEONE-VERIFY`（EdgeOne 加密变量入口）：
```
OIDC_CLIENT_SECRET   = <IdP 官网 client 的 secret>
SESSION_ENC_KEY      = <openssl rand -base64 32>   # 账户中心会话 cookie 的 AES-256-GCM 密钥
```
**普通变量**：
```
OIDC_ISSUER                = https://<IdP>/realms/lumen
OIDC_AUTHORIZE_URL         = https://<IdP>/.../auth
OIDC_TOKEN_URL             = https://<IdP>/.../token
OIDC_USERINFO_URL          = https://<IdP>/.../userinfo
OIDC_CLIENT_ID             = lumen-website
OIDC_AUDIENCE              = lumen-api
OIDC_DESKTOP_REDIRECT_URI  = https://example.com/desktop/callback
OIDC_WEB_REDIRECT_URI      = https://example.com/auth/callback
WEB_BASE_URL               = https://example.com
UPDATES_LATEST_URL         = https://chat.example.com/updates/latest.json
```
> 变量名与 `website/functions/_lib/env.ts` 一致。前端构建若需 `VITE_*` 注入 API/域名，见 `website/.env.example`。

## 4. 域名与 HTTPS
- [ ] Pages 项目绑定 `example.com`（EdgeOne 边缘自动签发证书）。 `EDGEONE-VERIFY`
- [ ] DNS 按 EdgeOne 指引把 `example.com` 解析/接入 EdgeOne。

## 5. IdP 登记（与官网域名对齐）
- [ ] 在 IdP 登记两个回调：`https://example.com/desktop/callback`、`https://example.com/auth/callback`。
- [ ] 允许官网 client 请求令 `access_token.aud` 含 `lumen-api`（Keycloak audience mapper / Auth0·Logto audience 参数）。
- 详见 [`idp-setup.md`](./idp-setup.md)（含 Keycloak/Auth0/Logto 三种示例 + 三方对齐矩阵）。

## 6. 部署 + 验证
1. 触发部署，等构建完成。
2. **账户中心（浏览器）**：访问 `https://example.com/account` → 未登录跳 `/auth/login` → IdP 登录 → 回 `/account` 显示头像/昵称；`GET /api/me` 返回资料；退出可用。
3. **桌面中介端点（无客户端也可半自动核对）**：用 [`verify-login.md`](./verify-login.md) + `scripts/verify-handoff.sh`：
   - 浏览器打开 `https://example.com/desktop/login?redirect_uri=http://127.0.0.1:<port>/cb&state=...&challenge=...` → IdP 登录 → 回环收到 `?handoff_code=...`；
   - `POST https://example.com/api/desktop/exchange {handoff_code, handoff_verifier}` → 返回 `{access_token, expires_in, desktop_session_id, profile}`；
   - 核对：`access_token` **不出现在任何回环 URL**；`handoff_code` 二次提交返回 404（一次性）。
4. 用上一步拿到的 `access_token` 打后端 `chat.example.com/api/v1/bootstrap`（验证前后端 `aud` 对齐）。

## 7. 客户端对接（后续，客户端就绪时）
- 客户端配置 `LUMEN_WEB_BASE_URL = https://example.com`、`LUMEN_API_BASE_URL = https://chat.example.com/api/v1`、`LUMEN_WS_URL = wss://chat.example.com/ws`。

## 8. EDGEONE-VERIFY 待核实清单
- [ ] Pages Functions 是否为 `functions/` 文件路由 + `onRequest*` 导出 + `context.env` 注入 KV/变量（本移植按此假设）。
- [ ] KV 在函数内的访问 API 是否为 `env.HANDOFF.get/put/delete`；绑定名配置位置。
- [ ] `edgeone.json` 的实际字段/是否需要（否则纯控制台配置）。
- [ ] 加密变量（Secret）的配置入口与在函数中的读取方式。
- [ ] 自定义域名与证书流程。

> 以上任一与假设不符时，改动集中在 `website/functions/_lib/runtime.ts`（类型/上下文）与 `website/functions/_lib/kv.ts`（KV 适配），业务逻辑无需改。

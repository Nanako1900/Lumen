# Lumen 官网（React + Vite + TailwindCSS + Cloudflare Pages）

官网 = **桌面登录中介**（confidential OIDC client）+ 营销首页 + 客户端下载 + 账户中心。
部署于 Cloudflare Pages（静态 SPA）+ Pages Functions（Worker，按文件路由）+ KV。

> 详细设计：[`../docs/design/web-design.md`](../docs/design/web-design.md)（端点契约/安全在 §5/§8/§9）。

## 技术栈

React 18 + Vite 5 + TypeScript + TailwindCSS（深色默认）；Cloudflare Pages Functions + KV；
测试用 Vitest + `@cloudflare/vitest-pool-workers`（真实 workerd + Miniflare KV，IdP token 端点 mock）。

## 开发脚本

```bash
npm install            # 安装依赖
npm run dev            # Vite 本地开发（仅前端 SPA，端点走 Pages 时用 wrangler）
npm run build          # typecheck（app+functions+node）+ vite build → dist/
npm run typecheck      # 仅类型检查（生产代码）
npm run typecheck:test # 类型检查测试代码（含 cloudflare:test 类型）
npm test               # Vitest（Worker 端点 + _lib 单测，85 用例）
npm run pages:dev      # wrangler pages dev（本地跑 functions/ + 静态 dist/）
```

## 结构

```
src/                    React SPA（Vite + Tailwind）
  routes/               Home / Download / Account / Help / Privacy / Terms / About / NotFound
  components/           Layout / Nav / Hero / DownloadButton / ProfileCard / PageSection
  lib/                  api（同源 /auth·/api fetch）/ config（VITE_* 注入）/ format
functions/              Cloudflare Pages Functions（按文件路由）
  desktop/              GET /desktop/login、GET /desktop/callback
  api/desktop/          POST /api/desktop/exchange|refresh|logout
  api/download/         GET /api/download/latest（CORS 代理 latest.json）
  api/me.ts             GET /api/me（账户中心会话资料；未登录 401）
  auth/                 GET /auth/login、GET /auth/callback、POST /auth/logout
  _lib/                 env / http / pkce / kv / oidc / session / loopback（+ testutil）
public/_redirects       SPA 回退（不覆盖 functions 路由）
wrangler.toml           KV 绑定（HANDOFF/SESSIONS）+ 非机密 vars
.env.example            环境变量与 Secret 清单（复制为 .dev.vars 本地开发）
```

## KV / Secrets（web-design §9）

- **KV**：`HANDOFF`（`handoff_code → {access_token, expires_in, refresh_token, sub, bound_challenge, profile}` +
  登录上下文 `ctx:*`，TTL≈120s / ctx≈600s，一次性消费）；`SESSIONS`（`desktop_session_id → {refresh_token, sub, created_at}`，logout 删）。
- **Secrets（CF 加密环境变量，绝不进仓库/前端）**：`OIDC_CLIENT_SECRET`、`SESSION_ENC_KEY`。
- **非机密 env**：OIDC issuer/authorize/token/userinfo URL、`OIDC_CLIENT_ID`、`OIDC_AUDIENCE`、
  redirect_uri、`WEB_BASE_URL`、`UPDATES_LATEST_URL`（见 `.env.example` / `wrangler.toml [vars]`）。

## 部署（Cloudflare Pages）

1. 创建两个 KV 命名空间并替换 `wrangler.toml` 中的占位 id（或在 Pages 项目设置绑定到 `env.HANDOFF`/`env.SESSIONS`）：
   ```bash
   npx wrangler kv namespace create HANDOFF
   npx wrangler kv namespace create SESSIONS
   ```
2. 注入 Secrets（绝不写入 wrangler.toml）：
   ```bash
   npx wrangler pages secret put OIDC_CLIENT_SECRET
   npx wrangler pages secret put SESSION_ENC_KEY   # openssl rand -base64 32
   ```
3. Pages 构建配置：构建命令 `npm ci && npm run build`，输出目录 `dist/`，Functions 目录 `functions/`（自动识别）。
4. 在 IdP 登记两个 redirect_uri：`https://<域名>/desktop/callback` 与 `https://<域名>/auth/callback`（§3.3）。
5. 预览环境使用独立 KV 命名空间与独立 IdP client/redirect 白名单，避免污染生产会话。

> 域名 `example.com` 为占位，全部经环境变量可配置——不写死真实域名。

## 安全红线（web-design §8.1）

- `client_secret` 仅在 Worker 加密环境变量；`refresh_token` 不出 Cloudflare（仅 KV）。
- `access_token` 绝不进任何 URL（仅 `/exchange`、`/refresh` 响应体）。
- handoff：一次性消费 + 短 TTL（≈120s）+ challenge 绑定（`S256(handoff_verifier)==bound_challenge`）。
- `redirect_uri` 仅允许 `http://127.0.0.1:<port>/...` 回环（拒 `localhost`）。
- 账户中心不调用 Lumen API；会话 cookie 为 httpOnly + Secure + SameSite=Lax。

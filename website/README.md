# Lumen 官网（React + Vite + TailwindCSS + 腾讯 EdgeOne Pages）

官网 = **桌面登录中介**（confidential OIDC client）+ 营销首页 + 客户端下载 + 账户中心。
部署于腾讯 EdgeOne Pages（静态 SPA）+ EdgeOne Pages Functions（按文件路由）+ EdgeOne KV。

> 详细设计：[`../docs/design/web-design.md`](../docs/design/web-design.md)（端点契约/安全在 §5/§8/§9）。
> 注：设计文档成文于 Cloudflare 方案；本目录已移植到腾讯 EdgeOne，**端点契约与安全红线完全不变**，
> 仅运行时/配置/依赖改为 EdgeOne。运行时相关假设均以 `EDGEONE-VERIFY` 注释标出，部署前请核实。

## 技术栈

React 18 + Vite 5 + TypeScript + TailwindCSS（深色默认）；EdgeOne Pages Functions + EdgeOne KV；
测试用 Vitest（node 环境）+ 内存 KV（运行时无关，IdP token/userinfo 端点用 fetch stub mock）。

运行时特性被隔离在 `functions/_lib/runtime.ts`（`EdgeKV` / `PagesFunction` 最小类型垫片，
不依赖任何 vendor 类型包）。若 EdgeOne 实际的路由/注入形态与假设不同，通常仅需改本文件与各 handler 的导出包装。

## 开发脚本

```bash
npm install            # 安装依赖
npm run dev            # Vite 本地开发（仅前端 SPA）
npm run build          # typecheck（app+functions+node）+ vite build → dist/
npm run typecheck      # 仅类型检查（生产代码）
npm run typecheck:test # 类型检查测试代码
npm test               # Vitest（Worker/端点 + _lib 单测）
npm run edgeone:dev    # 占位：本地跑 functions/ + 静态 dist/（用 EdgeOne CLI/控制台）
                       # EDGEONE-VERIFY: 以 EdgeOne 官方 CLI/控制台的本地开发命令为准
```

## 结构

```
src/                    React SPA（Vite + Tailwind）
  routes/               Home / Download / Account / Help / Privacy / Terms / About / NotFound
  components/           Layout / Nav / Hero / DownloadButton / ProfileCard / PageSection
  lib/                  api（同源 /auth·/api fetch）/ config（VITE_* 注入）/ format
functions/              EdgeOne Pages Functions（按文件路由）
  desktop/              GET /desktop/login、GET /desktop/callback
  api/desktop/          POST /api/desktop/exchange|refresh|logout
  api/download/         GET /api/download/latest（CORS 代理 latest.json）
  api/me.ts             GET /api/me（账户中心会话资料；未登录 401）
  auth/                 GET /auth/login、GET /auth/callback、POST /auth/logout
  _lib/                 env / http / pkce / kv / oidc / session / loopback / runtime（+ testutil）
public/_redirects       SPA 回退（不覆盖 functions 路由）
edgeone.json            EdgeOne Pages 项目配置（KV 绑定 HANDOFF/SESSIONS + 非机密 vars 占位）
.env.example            环境变量与 Secret 清单（复制为 .dev.vars 本地开发）
```

## KV / Secrets（web-design §9）

- **KV（EdgeOne KV）**：`HANDOFF`（`handoff_code → {access_token, expires_in, refresh_token, sub, bound_challenge, profile}` +
  登录上下文 `ctx:*`，TTL≈120s / ctx≈600s，一次性消费）；`SESSIONS`（`desktop_session_id → {refresh_token, sub, created_at}`，logout 删）。
  - **过期正确性不依赖平台原生 TTL**：`_lib/kv.ts` 把每条记录包一层 `{ v, exp }`（`exp = 写入时刻 + ttl`），
    读取时若已过期则删除并返回 null；同时仍把 `expirationTtl` 传给 `put` 作为原生 TTL 兜底
    （EdgeOne 若不支持则忽略，正确性仍由内嵌 `exp` 保证）。
- **Secrets（EdgeOne 加密变量，绝不进仓库/前端）**：`OIDC_CLIENT_SECRET`、`SESSION_ENC_KEY`。
- **非机密 env**：OIDC issuer/authorize/token/userinfo URL、`OIDC_CLIENT_ID`、`OIDC_AUDIENCE`、
  redirect_uri、`WEB_BASE_URL`、`UPDATES_LATEST_URL`（见 `.env.example` / `edgeone.json` 的 vars）。

## 部署（腾讯 EdgeOne Pages）

> 以下步骤为 best-effort；具体命令/入口以 EdgeOne 官方控制台/CLI 为准（见各 `EDGEONE-VERIFY` 注释）。

1. 创建两个 EdgeOne KV 命名空间，并在 EdgeOne 项目设置中把它们绑定到 `env.HANDOFF` / `env.SESSIONS`
   （`edgeone.json` 中的 namespace 为占位，替换为真实命名空间标识）。
2. 注入 Secrets（绝不写入 `edgeone.json`/仓库）：在 EdgeOne 控制台把 `OIDC_CLIENT_SECRET` 与
   `SESSION_ENC_KEY`（`openssl rand -base64 32`，解码须为 32 字节）设为**加密变量**。
3. 构建配置：构建命令 `npm ci && npm run build`，输出目录 `dist/`，Functions 目录 `functions/`。
4. 在 IdP 登记两个 redirect_uri：`https://<域名>/desktop/callback` 与 `https://<域名>/auth/callback`（§3.3）。
5. 预览环境使用独立 KV 命名空间与独立 IdP client/redirect 白名单，避免污染生产会话。

> 域名 `example.com` 为占位，全部经环境变量可配置——不写死真实域名。

## 安全红线（web-design §8.1）

- `client_secret` 仅在服务端加密变量；`refresh_token` 不出服务端（仅 KV `SESSIONS`）。
- `access_token` 绝不进任何 URL（仅 `/exchange`、`/refresh` 响应体）。
- handoff：一次性消费 + 短 TTL（≈120s）+ challenge 绑定（`S256(handoff_verifier)==bound_challenge`）。
- `redirect_uri` 仅允许 `http://127.0.0.1:<port>/...` 回环（拒 `localhost`）。
- 账户中心不调用 Lumen API；会话 cookie 为 httpOnly + Secure + SameSite=Lax。

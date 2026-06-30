# Lumen 官网（React + TailwindCSS + Cloudflare Pages）

官网 = **桌面登录中介**（confidential OIDC client）+ 营销首页 + 客户端下载 + 账户中心。部署于 Cloudflare Pages（静态）+ Pages Functions（Worker）+ KV。

> 详细设计：[`../docs/design/web-design.md`](../docs/design/web-design.md)。

## 结构（骨架）

```
src/                    React SPA（Vite + Tailwind）
  routes/               Home / Download / Account / Help / Privacy / Terms / About
  components/           Layout / Hero / DownloadButton / ProfileCard / Nav
  lib/                  前端 fetch（同源 /auth/*、/api/*）
functions/              Cloudflare Pages Functions（按文件路由）
  desktop/              GET /desktop/login、GET /desktop/callback
  api/desktop/          POST /api/desktop/exchange|refresh|logout
  api/download/         GET /api/download/latest（可选 CORS 代理）
  api/me.ts             GET /api/me（账户中心会话资料）
  auth/                 GET /auth/login、/auth/callback、POST /auth/logout
  _lib/                 oidc / pkce / kv 辅助
public/                 _redirects（SPA 回退，不覆盖 functions 路由）
```

## KV / Secrets

- KV：`HANDOFF`（handoff_code→token set，TTL≈120s，一次性）、`SESSIONS`（desktop_session_id→{refresh_token,sub}）。
- Secrets（CF 加密环境变量）：OIDC issuer/authorize/token URL、`client_id`、`client_secret`、redirect 基址、audience。

安全红线与端点契约见 [官网设计 §5/§8/§9](../docs/design/web-design.md)。

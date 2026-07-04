# Lumen 官网（React + Vite + TailwindCSS + 腾讯 EdgeOne Pages，静态托管）

官网 = 营销首页 + 客户端下载 + 账户中心（纯静态 React SPA）。

**架构：EdgeOne Pages 仅静态托管 `dist/`** —— 无 Pages Functions、无 EdgeOne KV、无任何 secret。
全部后端（资源服务器 + **OIDC 登录中介**：桌面握手 + 网页账户中心会话）在 Go 服务端
`chat.example.com`。`client_secret` / `refresh_token` 仅存于 Go 服务端（Postgres，refresh_token 静态加密）。
SPA 从 `example.com` **跨域**（同 registrable domain，同站）调用 Go 中介。

> 详细设计：[`../docs/design/web-design.md`](../docs/design/web-design.md)。

## 技术栈

React 18 + Vite 5 + TypeScript + TailwindCSS。视觉为 **Aurora Indigo**（靛蓝 #5b6ef5 +
极光青 #29d4b4 的浅色玻璃拟态设计，见 `../外观设计稿/` 与 [`../docs/design/web-design.md`](../docs/design/web-design.md)）。
构建产物为纯静态 SPA。

## 开发脚本

```bash
npm install            # 安装依赖
npm run dev            # Vite 本地开发（仅前端 SPA）
npm run build          # typecheck（app+node）+ vite build → dist/
npm run typecheck      # 仅类型检查
npm run preview        # 本地预览构建产物
```

## 结构

```
src/                    React SPA（Vite + Tailwind，Aurora Indigo 设计系统）
  routes/               Home / Download / Account / Help / Privacy / Terms / About / NotFound
  components/           Layout / Nav / Footer / Logo / PageSection / DownloadButton / ProfileCard / icons
    ui/                 Orb / GlassCard / Eyebrow / button（buttonClass）—— 设计系统原子
    home/               HeroSection / ProductShot / StatsBar / FeatureGrid / SelfHostSection / FinalCta
  lib/                  api（跨域直连 Go 中介，credentials:'include'）/ config（VITE_* 注入）/ format
public/_redirects       SPA 深链回退（额外兜底）
edgeone.json            EdgeOne Pages 项目配置（仅 buildCommand/installCommand/outputDirectory/nodeVersion/rewrites）
.env.example            构建期 VITE_* 环境变量（均非机密；无 secret）
```

## 后端交互（Go 中介，chat.example.com）

- `GET /api/me` —— 账户中心会话资料；未登录 401。`fetch` + `credentials:'include'`。
- `GET /auth/login`、`GET /auth/callback` —— 网页登录/回调，**顶层导航**（非 XHR）。
- `POST /auth/logout` —— 退出登录。`fetch` + `credentials:'include'`。
- 下载清单：SPA 直连 `chat.example.com/updates/latest.json`（公开 GET）。
- 会话 cookie 由 Go 端下发：`SameSite=Lax` + `HttpOnly` + `Secure` + `Path=/` + **host-only**（无 Domain）。
  Go 仅对精确的 `WEB_BASE_URL` origin 放行带凭据的 CORS。

## 构建期环境变量（仅 VITE_*，均非机密）

见 `.env.example`：`VITE_API_BASE_URL`（=`https://chat.example.com`）、
`VITE_UPDATES_LATEST_URL`、`VITE_WEB_BASE_URL`。任何 secret（client_secret / 会话密钥）
仅在 Go 服务端（Coolify 环境变量），绝不进前端产物/仓库/桌面。

## 部署（腾讯 EdgeOne Pages，静态）

1. 构建配置：构建命令 `npm run build`，安装命令 `npm ci`，输出目录 `dist/`（见 `edgeone.json`）。
2. 在 EdgeOne 控制台设置构建期环境变量 `VITE_*`（非机密）。
3. Go 中介的 IdP redirect_uri：`https://chat.example.com/desktop/callback` 与
   `https://chat.example.com/auth/callback`（在 IdP 登记；secret 在 Coolify 侧配置）。

> 域名 `example.com` / `chat.example.com` 为占位，全部经环境变量可配置——不写死真实域名。

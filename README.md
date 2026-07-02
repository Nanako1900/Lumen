# Lumen

一款类 Discord 的轻量**语音聊天工具**（开黑用）：Windows 桌面客户端 + 单 Go 二进制服务端（资源服务器 + 登录中介 broker）+ 官网（纯静态 SPA：营销 / 下载 / 账户中心），对接外部 OAuth2/OIDC 身份服务器。

> 当前状态：**设计完成，进入实现阶段**。本仓库为 monorepo 骨架，代码按 milestone 逐步填充。许可证：待定。

## 架构总览（四方）

```
                        外部 OAuth2/OIDC (IdP)
                          ▲              ▲
        client_secret + PKCE│              │JWKS 验签
        refresh_token(加密)  │              │
  ┌───────────────────────────────────────────────────┐
  │  Go 服务端 chat.example.com (Coolify/Docker + PostgreSQL)  │
  │  ├─ 资源服务器：JWKS 本地验 access_token (aud=lumen-api)  │
  │  └─ 登录中介 broker：/desktop/*、/auth/*、/api/desktop/*、/api/me │
  └───────────────────────────────────────────────────┘
      ▲ 回环 handoff (桌面)        ▲ WSS / REST (Bearer)    ▲ 跨源 XHR+cookie (账户中心)
      │                          │                        │
  Windows 客户端 (Wails v2 + Svelte)                 官网 example.com
  └── 经登录中介登录，持 desktop_session_id          (腾讯 EdgeOne Pages，纯静态 SPA：website/dist/)
```

- **登录中介（broker）现在 Go 服务端**：桌面/账户中心委托 `chat.example.com` 完成 OAuth（confidential client，`client_secret` 仅在 Go 服务端 Coolify 加密环境变量）；桌面经本地回环 handoff 拿 token；`refresh_token` **加密存 Go 服务端 PostgreSQL、绝不下发**，桌面只存 `desktop_session_id`。桌面 handoff 线路契约不变，仅宿主 `example.com` → `chat.example.com`。
- **服务端**：**资源服务器 + 登录中介同进程**。资源服务器仅用 IdP 的 JWKS 本地验 `access_token`（JWT，`aud=lumen-api`）；REST + WebSocket 混合；Pion SFU 转发语音（Opus，不转码）；PostgreSQL 持久化（含 broker 的 handoff/session 表）。
- **官网**：`example.com` 为**纯静态 SPA**（EdgeOne Pages 只托管 `website/dist/`，**无 Pages Functions / 无 KV / 无任何密钥**）。账户中心网页在浏览器内**跨源**调 `chat.example.com` 的登录中介（`SameSite=Lax` HttpOnly cookie；服务端仅对 `example.com` 发带凭据 CORS）。
- **客户端**：Wails v2 外壳（原生能力）+ Svelte 前端（UI/WebRTC/Web Audio），仅 Windows。

## 仓库结构

| 目录 | 内容 | 技术栈 |
|------|------|--------|
| [`server/`](./server) | 单 Go 二进制服务端（**资源服务器**：鉴权/信令/SFU/REST/store + **登录中介 broker**：OIDC 登录/handoff/session） | Go、Pion WebRTC v4、pgx、coder/websocket |
| [`client/`](./client) | Windows 桌面客户端 | Wails v2（Go）+ Svelte + TypeScript |
| [`website/`](./website) | 官网（**纯静态 SPA**：营销/下载/账户中心；**无 Functions/KV/密钥**，登录中介已移至服务端） | React + TailwindCSS + 腾讯 EdgeOne Pages（仅静态托管 `dist/`） |
| [`shared/`](./shared) | （可选）前后端共享的协议类型定义 | — |
| [`docs/`](./docs) | 设计文档与技术调研 | Markdown |

## 设计文档（权威）

- **总览**：[`docs/design/00-overview.md`](./docs/design/00-overview.md)
- **接口契约（唯一权威）**：[`docs/design/protocol-design.md`](./docs/design/protocol-design.md)
- **服务端**：[`docs/design/server-design.md`](./docs/design/server-design.md)
- **客户端**：[`docs/design/client-design.md`](./docs/design/client-design.md)
- **官网**：[`docs/design/web-design.md`](./docs/design/web-design.md)
- **技术调研**：[`docs/research/`](./docs/research)

实现如与 `protocol-design.md` 冲突，**以接口契约为准**。

## 路线（里程碑）

- **v0** — 最小开黑回路 + 服务端登录中介（登录→文字→单语音频道收发；官网静态下载/账户中心；Coolify + 腾讯 EdgeOne 静态部署）。
- **v1** — 类 Discord 体验（频道 CRUD、踢人、资料双向同步、PTT/VAD、降噪、逐人音量、桌面集成、自动更新、多频道）。
- **v2** — E2E 加密（Insertable Streams + SFrame，附录概述）。

任务跟踪见仓库 [Issues](../../issues) 与 [Milestones](../../milestones)。

## 本地开发

完整步骤（起服务端（含登录中介）/ 官网静态站、所需环境变量、集成校验）见 **[`docs/DEV.md`](./docs/DEV.md)**。速览：

- **服务端**：`cd server && go run ./cmd/lumen-server`（需 PostgreSQL 与 `LUMEN_*` 环境变量，**含资源服务器 + 登录中介 broker 两组**：`LUMEN_OAUTH_CLIENT_ID/SECRET`、`LUMEN_OAUTH_DESKTOP_REDIRECT_URI`、`LUMEN_OAUTH_WEB_REDIRECT_URI`、`LUMEN_WEB_BASE_URL`、`LUMEN_SESSION_ENC_KEY`、`LUMEN_REFRESH_ENC_KEY` 等；清单见 [服务端设计 §6.1](./docs/design/server-design.md#61-配置全部环境变量) 与 [`docs/DEV.md`](./docs/DEV.md#1-服务端-server)）。
- **官网**：`cd website && npm ci && npm run build`，产物在 `website/dist/`；`npm run dev` 起前端。官网现为**纯静态 SPA**——**无边缘函数 / 无 KV / 无密钥**，无需 EdgeOne CLI 起函数；登录中介端点全部在 Go 服务端 `chat.example.com`。账户中心通过跨源 XHR（带凭据）调服务端，见 [`docs/DEV.md`](./docs/DEV.md#2-官网-website)。
- **客户端**：`cd client && wails dev`（需 Wails v2 工具链与 WebView2；仅 Windows，见 [客户端设计](./docs/design/client-design.md)）。

> `.env` 已被 `.gitignore` 忽略；**勿提交任何真实密钥**。密钥（`LUMEN_OAUTH_CLIENT_SECRET`、`LUMEN_SESSION_ENC_KEY`、`LUMEN_REFRESH_ENC_KEY`）仅注入 Go 服务端（Coolify 加密环境变量），**不落 EdgeOne / 前端产物 / 桌面**。各端 `.env.example` 由对应功能分支提供；在其落地前，占位清单见 [`docs/DEV.md` 附录](./docs/DEV.md#附录-a服务端-env-占位清单)。

### 运维与集成

- **CI**：[`.github/workflows/ci.yml`](./.github/workflows/ci.yml) —— server(Go 1.24) / website(Node 20) / client(Wails) 三 job；用「存在守卫」探测各端代码，骨架期为绿，代码合入后自动转真实 build/test。
- **IdP 登记与配置对齐**：[`docs/ops/idp-setup.md`](./docs/ops/idp-setup.md)（回调 `chat.example.com/{desktop,auth}/callback`、`aud=lumen-api`、scope、issuer/audience/域名/端口对齐矩阵；`client_secret`/`refresh_token` 均在 Go 服务端）。
- **登录链路集成校验（无客户端）**：[`docs/ops/verify-login.md`](./docs/ops/verify-login.md) + [`scripts/`](./scripts)（healthz / Bearer bootstrap / WS `auth_ok`；半自动 handoff；access_token 不进 URL 核对）。
- **部署 · 后端（服务端 + 登录中介）**：[`docs/ops/deploy-coolify.md`](./docs/ops/deploy-coolify.md)（Coolify：PostgreSQL + 8080/Traefik + 40000/udp + env（含 `LUMEN_OAUTH_*` broker 组）+ healthz；登录中介与资源服务器同进程）。
- **部署 · 前端（纯静态）**：[`docs/ops/deploy-edgeone.md`](./docs/ops/deploy-edgeone.md)（EdgeOne Pages：**仅静态托管 `website/dist/` + SPA 重写 + 域名**；`edgeone.json` 只保留 build 配置 + SPA rewrites，**无 Functions / 无 KV / 无 Secrets**）。

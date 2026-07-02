# Lumen

一款类 Discord 的轻量**语音聊天工具**（开黑用）：Windows 桌面客户端 + 单 Go 二进制服务端 + 官网（登录中介 / 营销 / 下载 / 账户中心），对接外部 OAuth2/OIDC 身份服务器。

> 当前状态：**设计完成，进入实现阶段**。本仓库为 monorepo 骨架，代码按 milestone 逐步填充。许可证：待定。

## 架构总览（四方）

```
  外部 OAuth2/OIDC (IdP)  ──  官网 example.com (Cloudflare Pages/Functions/KV，登录中介)
         ▲                              │ 回环 handoff
         │ JWKS 验签                     ▼
  Go 服务端 chat.example.com  ◀──WSS──  Windows 客户端 (Wails v2 + Svelte)
  (Coolify/Docker + PostgreSQL)         └── 经官网登录，持 desktop_session_id
```

- **登录**：桌面委托官网完成 OAuth（官网为 confidential client，`client_secret` 仅在 Worker）；本地回环 handoff 拿 token；`refresh_token` 不出 Cloudflare，桌面只存 `desktop_session_id`。
- **服务端**：仅用 IdP 的 JWKS 本地验 `access_token`（JWT，`aud=lumen-api`）；REST + WebSocket 混合；Pion SFU 转发语音（Opus，不转码）；PostgreSQL 持久化。
- **客户端**：Wails v2 外壳（原生能力）+ Svelte 前端（UI/WebRTC/Web Audio），仅 Windows。

## 仓库结构

| 目录 | 内容 | 技术栈 |
|------|------|--------|
| [`server/`](./server) | 单 Go 二进制服务端（鉴权/信令/SFU/REST/store） | Go、Pion WebRTC v4、pgx、coder/websocket |
| [`client/`](./client) | Windows 桌面客户端 | Wails v2（Go）+ Svelte + TypeScript |
| [`website/`](./website) | 官网（登录中介 + 营销/下载/账户中心） | React + TailwindCSS + Cloudflare Pages/Functions/KV |
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

- **v0** — 最小开黑回路 + 官网中介登录（登录→文字→单语音频道收发；官网下载/账户中心；Coolify + Cloudflare 部署）。
- **v1** — 类 Discord 体验（频道 CRUD、踢人、资料双向同步、PTT/VAD、降噪、逐人音量、桌面集成、自动更新、多频道）。
- **v2** — E2E 加密（Insertable Streams + SFrame，附录概述）。

任务跟踪见仓库 [Issues](../../issues) 与 [Milestones](../../milestones)。

## 本地开发

完整步骤（起服务端 / 官网 Worker、所需环境变量、集成校验）见 **[`docs/DEV.md`](./docs/DEV.md)**。速览：

- **服务端**：`cd server && go run ./cmd/lumen-server`（需 PostgreSQL 与 `LUMEN_*` 环境变量；清单见 [服务端设计 §6.1](./docs/design/server-design.md#61-配置全部环境变量) 与 [`docs/DEV.md`](./docs/DEV.md#1-服务端-server)）。
- **官网**：`cd website && npm ci && npm run build && npx wrangler pages dev dist`（Cloudflare Pages Functions；本地 Secrets 走 `website/.dev.vars`，见 [`docs/DEV.md`](./docs/DEV.md#2-官网-website)）。仅 `npm run dev` 只起前端、不含 Worker 端点。
- **客户端**：`cd client && wails dev`（需 Wails v2 工具链与 WebView2；仅 Windows，见 [客户端设计](./docs/design/client-design.md)）。

> `.env` / `.dev.vars` 已被 `.gitignore` 忽略；**勿提交任何真实密钥**。各端 `.env.example` 由对应功能分支提供；在其落地前，占位清单见 [`docs/DEV.md` 附录](./docs/DEV.md#附录-a服务端-env-占位清单)。

### 运维与集成

- **CI**：[`.github/workflows/ci.yml`](./.github/workflows/ci.yml) —— server(Go 1.24) / website(Node 20) / client(Wails) 三 job；用「存在守卫」探测各端代码，骨架期为绿，代码合入后自动转真实 build/test。
- **IdP 登记与配置对齐**：[`docs/ops/idp-setup.md`](./docs/ops/idp-setup.md)（回调、`aud=lumen-api`、scope、issuer/audience/域名/端口三方对齐矩阵）。
- **登录链路集成校验（无客户端）**：[`docs/ops/verify-login.md`](./docs/ops/verify-login.md) + [`scripts/`](./scripts)（healthz / Bearer bootstrap / WS `auth_ok`；半自动 handoff；access_token 不进 URL 核对）。
- **部署 · 后端**：[`docs/ops/deploy-coolify.md`](./docs/ops/deploy-coolify.md)（Coolify：PostgreSQL + 8080/Traefik + 40000/udp + env + healthz）。
- **部署 · 前端**：[`docs/ops/deploy-edgeone.md`](./docs/ops/deploy-edgeone.md)（EdgeOne Pages：KV 绑定 + Secrets + 域名；官网已 EdgeOne 化并入 `main`，可直接执行）。

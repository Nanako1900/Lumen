# 部署：后端服务端 → Coolify（步骤清单）

> 目标：把 `server/`（单 Go 二进制）部署到 Coolify（Docker）。依据 [服务端设计 §7](../design/server-design.md#7-部署coolify) 与 [调研 02](../research/02-coolify-udp.md)。
> 占位：`chat.example.com`（API/WS 域名）、`<VPS_PUBLIC_IP>`、IdP 相关值——按实际替换。IdP 值来源见 [`idp-setup.md`](./idp-setup.md)。

## 0. 前置
- [ ] 一台有**公网 IP** 的 VPS，已接入你的 Coolify 实例。
- [ ] 外部 OAuth2/OIDC（IdP）已就绪，拿到 `issuer` / JWKS URL / `audience=lumen-api` / owner 的 `sub`。
- [ ] 云厂商**安全组/防火墙**可放行入站 `443/tcp` 与一个 UDP 端口（下用 `40000/udp`）。

## 1. 建 PostgreSQL 资源
1. Coolify 项目内 **+ New → Database → PostgreSQL**（16）。
2. 记下其**内部连接串**（同项目内服务用内部主机名，如 `postgres://lumen:***@<db-internal-host>:5432/lumen`）。
3. 该连接串填入下面的 `LUMEN_DATABASE_URL`（内网 `sslmode=disable`；跨网络托管 PG 用 `sslmode=require`）。

## 2. 新建应用（Git + Dockerfile）
1. **+ New → Application → Public/Private Repository** → 选 `Nanako1900/Lumen`，分支 `main`。
2. Build Pack：**Dockerfile**。
3. **Base Directory**：`/server`（Dockerfile 在 `server/Dockerfile`）。
4. 先不部署，先配下面的端口/环境变量/域名。

## 3. 端口（关键：HTTP 走 Traefik，WebRTC 走裸 UDP）
- [ ] **Ports Exposes** = `8080` （容器内明文 HTTP/WS，Traefik 据此反代、终结 TLS）。
- [ ] **Ports Mappings** = `40000:40000/udp` （WebRTC 媒体，裸 UDP 直发宿主机、**不经 Traefik**）。
  - 若该版本 Coolify 的 Ports Mappings 不接受 `/udp`：改用 **Docker Compose 部署类型**，在 compose `ports:` 写 `"40000:40000/udp"`（见 [调研 02 §6.2](../research/02-coolify-udp.md)）。
  - ⚠️ 用了 Ports Mappings 会**失去 Rolling Updates**（重部署有短暂中断，语音会重连，可接受）。

## 4. 环境变量（Environment Variables）
按 [服务端设计 §6.1](../design/server-design.md#61-配置全部环境变量) 注入（缺必填启动即 fail-fast）：

```
LUMEN_DATABASE_URL      = postgres://lumen:***@<db-internal-host>:5432/lumen?sslmode=disable
LUMEN_OAUTH_ISSUER      = https://<你的 IdP>/realms/lumen
LUMEN_OAUTH_JWKS_URL    = https://<你的 IdP>/realms/lumen/protocol/openid-connect/certs
LUMEN_OAUTH_AUDIENCE    = lumen-api
LUMEN_OWNER_SUBJECTS    = <owner-sub-1>,<owner-sub-2>
LUMEN_LISTEN_ADDR       = 0.0.0.0:8080          # 必须 0.0.0.0，否则 Traefik 到不了容器
LUMEN_PUBLIC_IP         = <VPS_PUBLIC_IP>       # SetNAT1To1IPs 宣告，WebRTC 必需
LUMEN_WEBRTC_UDP_PORT   = 40000                 # 与 Ports Mappings 一致
LUMEN_PUBLIC_WS_URL     = wss://chat.example.com/ws
LUMEN_UPDATES_DIR       = /app/updates          # 可选（自动更新文件托管）
LUMEN_LOG_LEVEL         = info
# LUMEN_OAUTH_USERINFO_URL 可选（缺省由 OIDC discovery 推导）
```

> `LUMEN_OAUTH_AUDIENCE=lumen-api` 必须与官网 Worker 请求 token 时的 audience、以及 IdP 侧一致（见配置对齐矩阵 [`idp-setup.md`](./idp-setup.md)）。

## 5. 域名与 TLS
- [ ] **Domains (FQDN)** = `https://chat.example.com` → Traefik 自动签 Let's Encrypt、强制 HTTPS；对外即 `https://` + `wss://`。
- [ ] DNS：`chat.example.com` A 记录指向 `<VPS_PUBLIC_IP>`。

## 6. 持久化（自动更新文件，可选）
- [ ] **Persistent Storage** 挂载容器路径 `/app/updates`（配合 `LUMEN_UPDATES_DIR`，`GET /updates/` 静态托管客户端更新包）。v0 若暂不做自动更新可跳过。
- 注意：数据库持久化由**第 1 步的 PostgreSQL 资源**负责，应用容器本身**无需**数据卷。

## 7. 健康检查
- [ ] **Health Check** = `GET /api/v1/healthz`（Coolify 探活；返回 `{"success":true,...}`）。

## 8. 防火墙 / 安全组
- [ ] 云安全组放行入站 **`443/tcp`**（HTTPS/WSS）+ **`40000/udp`**（WebRTC）。
- [ ] ⚠️ Docker 的 iptables 会绕过主机 UFW —— **优先用云安全组**；若用 UFW 需配 `ufw-docker`。

## 9. 部署 + 验证
1. 点 **Deploy**。查看 Build/Runtime 日志：应看到 `listening addr=0.0.0.0:8080 udp=40000`，且**启动时幂等建表 + 种子频道**（大厅 / 开黑1）。
2. 校验（用仓库 [`scripts/`](../../scripts)）：
   - `GET https://chat.example.com/api/v1/healthz` → `200`。
   - 带真实 IdP 的 `access_token`（`aud=lumen-api`）打 `GET /api/v1/bootstrap` → 返回 me/channels/members；WS 首帧 `auth` → 收到 `auth_ok`。参见 [`verify-login.md`](./verify-login.md)、`scripts/smoke-server.sh`。
3. 改环境变量后需**重新部署**才生效。

## 10. 已知与后续
- **WebRTC 全链路语音**需 Windows 客户端（本批未做）才能端到端验证；现可验证 REST/WS/healthz/JWKS 验签。
- 三方（IdP / 官网 Worker / 本服务端）的 `issuer`/`audience`/域名/端口对齐见 [`idp-setup.md`](./idp-setup.md) 的对齐矩阵。

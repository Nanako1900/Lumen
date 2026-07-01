# Lumen 服务端部署（Coolify / Docker）

> 权威依据：[`../docs/design/server-design.md §7`](../docs/design/server-design.md) 与 [`../docs/design/00-overview.md §7`](../docs/design/00-overview.md)。
> 本文是可执行的部署清单；配置字段、端口对齐、环境变量以设计文档为准。

## 架构要点

- HTTP/WS（REST + WebSocket 信令）经 **Coolify Traefik** 终结 TLS，容器内监听**明文** `0.0.0.0:8080`。
- WebRTC 媒体（DTLS-SRTP）走**裸 UDP 端口**（默认 `40000/udp`），**绕过 Traefik**，由 Coolify Ports Mappings 直发宿主机。
- PostgreSQL 为 Coolify 资源（或外部托管 PG）；应用启动幂等建表 + 幂等种子频道（text『大厅』+ voice『开黑1』），**应用容器无需持久卷**。
- 自动更新文件（`[v1]`）经 `GET /updates/` 静态托管，目录由 Persistent Storage 挂载。

```
客户端 ──443/tcp (HTTPS REST + WSS)──▶ Traefik(终结 TLS) ──明文 http/ws──▶ 容器 0.0.0.0:8080
客户端 ──40000/udp (DTLS-SRTP 媒体)──▶ 宿主机:40000/udp (Ports Mappings) ──▶ 容器 0.0.0.0:40000/udp
容器 ──5432/tcp (Coolify 内网)──▶ PostgreSQL 资源
```

## 前置

1. 一台有**公网 IP** 的 VPS，已接入 Coolify。
2. 一个指向该 VPS 的域名（如 `chat.example.com`）。
3. 外部 OAuth2/OIDC 服务器已就绪，可提供 JWKS 端点；`access_token` 的 `aud` 含 `lumen-api`。

## 步骤

### 1. 创建 PostgreSQL 资源

在同一 Coolify 项目内新建一个 PostgreSQL 服务（推荐 PG 16）。记录其内部主机名/端口/账号密码，拼成 DSN：

```
postgres://lumen:<password>@<db-service-name>:5432/lumen?sslmode=disable
```

> 同项目内经 Coolify 内部网络用**服务名**互联，`sslmode=disable`；外部托管 PG 用 `sslmode=require`。持久化/备份由该 PostgreSQL 资源负责。

### 2. 创建应用（从本仓库构建）

- **Build Pack**：Dockerfile。
- **Base/Build 目录**：`server/`（Dockerfile 与构建上下文均在此目录）。
- Coolify 会用 `server/Dockerfile` 多阶段构建（`CGO_ENABLED=0`，alpine 运行时，非 root `USER app`）。

### 3. 端口配置（四处对齐是关键）

| Coolify 字段 | 值 | 作用 |
|--------------|-----|------|
| **Ports Exposes** | `8080` | 容器监听端口，Traefik 据此转发 HTTP/WS；第一个=健康检查口 |
| **Ports Mappings** | `40000:40000/udp` | 裸 UDP 直发宿主机（WebRTC 媒体），绕过 Traefik |
| **Domains (FQDN)** | `https://chat.example.com` | Traefik 自动签 Let's Encrypt + 强制 HTTPS，对外即 wss/https |
| **Health Check** | `GET /api/v1/healthz` | Coolify 探活（纯存活探针，不查 DB） |

> **端口四处一致**：Dockerfile `EXPOSE 40000/udp` = Coolify Ports Mappings = env `LUMEN_WEBRTC_UDP_PORT=40000` = 云安全组放行。
>
> 若 Coolify 版本的 Ports Mappings **拒绝 `/udp` 后缀**，改用 **Docker Compose 部署类型**，在 compose `ports:` 写 `"40000:40000/udp"`（Docker 原生语义）。
>
> 用 Ports Mappings 部署会**失去 Rolling Updates**：重新部署有短暂中断、语音需重连（小规模开黑可接受）。

### 4. Persistent Storage（自动更新，`[v1]`，可选）

挂载一个卷到容器内 `/app/updates`（= `LUMEN_UPDATES_DIR` 默认值），用于放 `latest.json` + NSIS 安装包 + ed25519 签名。对外经 `https://chat.example.com/updates/latest.json` 访问（同域复用 Traefik 证书）。若不做自动更新可跳过。

### 5. 环境变量（Environment Variables）

按 [`server/.env.example`](./.env.example) 注入全部 `LUMEN_*`。**必填缺失即 fail-fast**。关键对齐：

| 变量 | 值 | 备注 |
|------|-----|------|
| `LUMEN_LISTEN_ADDR` | `0.0.0.0:8080` | **必须 0.0.0.0**，否则 Traefik 到不了容器 |
| `LUMEN_WEBRTC_UDP_PORT` | `40000` | 与 Ports Mappings 一致 |
| `LUMEN_PUBLIC_IP` | `<VPS 公网 IP>` | `SetNAT1To1IPs` 宣告；替换 host 候选 |
| `LUMEN_DATABASE_URL` | `postgres://lumen:***@<db>:5432/lumen?sslmode=disable` | 步骤 1 的 DSN |
| `LUMEN_OAUTH_ISSUER` | `https://auth.example.com/realms/lumen` | 校验 `iss` + OIDC discovery 基址 |
| `LUMEN_OAUTH_JWKS_URL` | `https://auth.example.com/.../certs` | 本地验签公钥源（须 HTTPS 可达） |
| `LUMEN_OAUTH_AUDIENCE` | `lumen-api` | 服务端只验 aud，不需要 client_id |
| `LUMEN_OWNER_SUBJECTS` | `<sub-a>,<sub-b>` | owner 的 OAuth sub，逗号分隔 |
| `LUMEN_PUBLIC_WS_URL` | `wss://chat.example.com/ws` | 可选；缺省由 Host 头推导 |

> 改 env 后必须**重新部署**才生效。启动时会同步拉取 JWKS——若 IdP 暂不可达，keyfunc 会后台重试；OIDC discovery（userinfo 补齐）失败则降级为仅用 claims，不阻断启动。

### 6. 防火墙放行

**优先用云厂商安全组**（Docker iptables 会绕过主机 UFW）：放行入站

- `443/tcp`（WSS/HTTPS，经 Traefik）
- `40000/udp`（WebRTC 媒体，裸端口）

若主机用 UFW，单独放行不够，需配 `ufw-docker`。

## 部署后核对清单

- [ ] `GET https://chat.example.com/api/v1/healthz` 返回 `{"success":true,"data":{"status":"ok"},"error":null}`。
- [ ] 首次启动日志出现 `listening`（`addr=0.0.0.0:8080 udp=40000`），无 fail-fast 报错。
- [ ] PostgreSQL 已建表（`users`/`channels`/`messages`）且有种子频道（`大厅`/`开黑1`）。
- [ ] 云安全组放行 `443/tcp` + `40000/udp`。
- [ ] 用真实 `access_token` 调 `GET /api/v1/bootstrap` 返回 me/channels/members/voice_states/ws_url；无效/过期/错 aud 的 token 被拒（`TOKEN_INVALID`/`TOKEN_EXPIRED`）。
- [ ] 两客户端进同一语音频道能听到彼此（DTLS-SRTP 经 40000/udp 建立）。
- [ ] （`[v1]`）`https://chat.example.com/updates/latest.json` 可访问，响应头含 `Cache-Control: no-cache` + `ETag`。

## 本地运行（开发）

```bash
cd server
cp .env.example .env   # 填入本地 IdP / Postgres
set -a && . ./.env && set +a
go run ./cmd/lumen-server
```

单元/集成测试：

```bash
go build ./... && go vet ./... && go test ./...
# 需要 PostgreSQL 的 store 集成测试（否则自动跳过）：
docker run -d --name pg -e POSTGRES_USER=lumen -e POSTGRES_PASSWORD=lumen -e POSTGRES_DB=lumen -p 55432:5432 postgres:16-alpine
LUMEN_TEST_DATABASE_URL="postgres://lumen:lumen@127.0.0.1:55432/lumen?sslmode=disable" go test ./...
```

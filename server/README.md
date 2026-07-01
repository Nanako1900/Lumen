# Lumen 服务端（Go 单二进制）

鉴权网关（JWKS 验签）+ WebSocket 信令 + Pion SFU + REST + PostgreSQL，全部打包进一个可执行文件。Coolify(Docker) 部署。

> 详细设计：[`../docs/design/server-design.md`](../docs/design/server-design.md)；接口契约：[`../docs/design/protocol-design.md`](../docs/design/protocol-design.md)。

## 包结构（骨架）

```
cmd/lumen-server/      入口：装配、启动、优雅关闭
internal/
  config/              环境变量加载与校验（fail-fast）
  auth/                JWKS 本地验签 + owner 判定 + claims→profile 映射 + REST 中间件
  store/               PostgreSQL 封装（pgx）：迁移、种子频道、users/channels/messages、游标分页、ULID
  rest/                REST handler：bootstrap/me/channels/messages/members/healthz/updates
  signaling/           WS hub：连接生命周期、握手、消息路由、广播
  sfu/                 Pion SFU：SettingEngine(UDPMux+NAT1To1)、Room、转发、重协商、清理
  protocol/            共享 DTO 与 WS 信封（与契约 §3.5/§4 对应）
```

## 关键依赖

`pion/webrtc/v4`、`pion/ice/v4`、`pion/rtp`、`coder/websocket`、`jackc/pgx/v5`、`MicahParks/keyfunc/v3`、`golang-jwt/jwt/v5`、`coreos/go-oidc/v3`、`oklog/ulid/v2`。`CGO_ENABLED=0`。

## 配置

全部走环境变量（`LUMEN_*`），启动校验必填项 fail-fast。清单见 [服务端设计 §6.1](../docs/design/server-design.md) 与 [`.env.example`](./.env.example)。

## 构建 / 测试 / 运行

```bash
cd server
go build ./...   # 编译
go vet ./...     # 静态检查
go test ./...    # 单元测试（store 集成测试无 DB 时自动跳过）

# 需要 PostgreSQL 的 store 集成测试：
docker run -d --name pg -e POSTGRES_USER=lumen -e POSTGRES_PASSWORD=lumen \
  -e POSTGRES_DB=lumen -p 55432:5432 postgres:16-alpine
LUMEN_TEST_DATABASE_URL="postgres://lumen:lumen@127.0.0.1:55432/lumen?sslmode=disable" \
  go test ./... -cover

# 本地运行（先填 .env）
cp .env.example .env && set -a && . ./.env && set +a && go run ./cmd/lumen-server
```

> **Go 版本**：设计锁 Go 1.23，但安全基线依赖（pion/webrtc ≥v4.2.5 的传递依赖 ice/dtls、pgx v5、go-oidc v3）已把最低 Go 提到 1.24；为保住 CVE 修复不变量，`go.mod` 与 Dockerfile 用 **Go 1.24**（对本项目源码向后兼容）。

## 部署

多阶段 [`Dockerfile`](./Dockerfile)（`CGO_ENABLED=0`，alpine，非 root）；Coolify 部署步骤见 [`DEPLOY.md`](./DEPLOY.md)。

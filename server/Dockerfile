# Lumen 服务端多阶段构建（server-design.md §7.2）
# 构建上下文 = server/ 目录（含 go.mod / go.sum / cmd / internal）。
# pgx + pion 均为纯 Go，CGO_ENABLED=0 静态构建，契合 alpine。

# ---- build ----
# 注：设计文档锁 Go 1.23，但当前安全基线依赖（pion/webrtc ≥v4.2.5 的传递依赖
# ice/v4、dtls/v3，以及 pgx v5、go-oidc v3）已将最低 Go 版本提到 1.24。为保住
# CVE-2026-26014 修复等安全不变量，构建 Go 提升到 1.24（1.24 对本项目源码向后兼容）。
FROM golang:1.24-alpine AS build
WORKDIR /src

# 先拷依赖清单以复用 layer 缓存。
COPY go.mod go.sum ./
RUN go mod download

# 拷源码并构建静态二进制。
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/lumen-server ./cmd/lumen-server

# ---- runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/lumen-server /app/lumen-server

# 自动更新静态托管目录（LUMEN_UPDATES_DIR 默认值），由 Coolify Persistent Storage 挂载。
RUN mkdir -p /app/updates && chown -R app:app /app

# 8090     = 信令/HTTP/WS（给 Traefik；Coolify Ports Exposes 第一个=健康检查口）。
#            注：容器实际监听端口由 env LUMEN_LISTEN_ADDR 决定（须与此处 EXPOSE、
#            Coolify Ports Exposes、健康检查端口三者一致）。8080 曾在本宿主机被占用
#            并返回 502，故改用 8090。EXPOSE 仅为元数据，真正绑定看 LUMEN_LISTEN_ADDR。
# 40000/udp = WebRTC 媒体单口（给 host 直发，Coolify Ports Mappings，绕过 Traefik）
EXPOSE 8090
EXPOSE 40000/udp

# 健康检查：命中公开的 /api/v1/healthz（无需鉴权，静态返回 {"success":true,"data":{"status":"ok"}}，
# 不依赖 DB，故不会因 DB 抖动误判）。Coolify 据此判断容器就绪并消除“未配置健康检查”提示。
# alpine 自带 busybox wget。注意：健康检查与 TLS 证书签发无关——证书由 Traefik/ACME 负责。
#
# 【回环必须绕过代理】若部署宿主机给容器注入 http_proxy（如国内经 sing-box 172.17.0.1:2080
# 由 ~/.docker/config.json 注入），busybox wget 会把对 127.0.0.1:8090 的回环请求也发给代理
# → 代理从宿主机侧回连 127.0.0.1:8090（空）→ 502，容器被误判 unhealthy、Traefik 拒绝路由。
# busybox wget 不认 no_proxy，故在命令内显式清空代理环境变量，令回环探测直连本容器进程。
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=3 \
  CMD http_proxy= https_proxy= HTTP_PROXY= HTTPS_PROXY= \
      wget -q -O - http://127.0.0.1:8090/api/v1/healthz >/dev/null 2>&1 || exit 1

USER app
ENTRYPOINT ["/app/lumen-server"]

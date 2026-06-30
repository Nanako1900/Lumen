# 调研 02 · Coolify(Docker) 暴露裸 UDP 端口并与 Traefik 反代共存

> ⚠️ **数据库已变更（决策更新）**：本项目持久化由 SQLite 改为 **PostgreSQL**（独立的 Coolify 数据库资源 / 外部托管 PG，经 `LUMEN_DATABASE_URL` 连接）。本调研中关于 **SQLite / Persistent Storage `/data` / `SQLITE_PATH` / compose 卷 `lumen-data`** 的段落与示例**已被取代**——应用容器不再需要持久卷，持久化与备份交给 PostgreSQL 资源。本文关于 **UDP 端口映射、Traefik、WSS、env 注入** 的结论仍然有效。以 [`docs/design/server-design.md §5.1/§7`](../design/server-design.md) 与 [`protocol-design.md §5`](../design/protocol-design.md) 为准。

> 调研日期: 2026-06-29 · 调研员: 技术调研子代理
> 适用项目: 本应用(类 Discord 语音聊天) — Go 单二进制服务端(信令 WSS + Pion SFU WebRTC),部署在 Coolify 管理的 VPS。
> 信息来源: Coolify 官方文档(coolify.io/docs,本地镜像 fetched 2026-06-06)、Coolify openapi.json、Coolify GitHub Discussions/Issues、Pion 官方文档与源码。知识截止后的细节以下述检索结果为准。

---

## 0. TL;DR(结论先行)

1. **Coolify 支持裸端口直发**:用应用设置里的 **Ports Mappings** 字段(对应 API `ports_mappings`),格式 `HOST:CONTAINER`。它走 Docker 原生 published-ports,**绕过 Traefik**。UDP 用 `/udp` 后缀(`40000:40000/udp`)——这是 Docker Compose 标准语法,官方应用文档示例只给了 TCP(`8080:80`)、未明确写 `/udp` 后缀,但社区与 Compose 部署方式已验证 `/udp` 可用(见“前提假设 A”)。
2. **UDP 不经 Traefik**:Traefik 在 Coolify 里只做 HTTP/HTTPS(及 TCP)的 7 层路由,WebRTC 媒体的 UDP 必须用 host 端口直发。因此 **VPS 主机防火墙 + 云厂商安全组都要放行该 UDP 端口**;注意 Docker 的 iptables 会绕过 UFW,需用云安全组或 `ufw-docker`。
3. **WSS 这条**:把信令服务挂到 Coolify 的 **Domains(FQDN)** 上,Traefik 自动签 Let's Encrypt 证书并在边缘**终结 TLS**,然后以**明文 HTTP/WS 转发**到容器端口。**容器内服务只监听明文 HTTP/WS 即可**(不要在容器内再做 TLS)。客户端连 `wss://域名`,容器内监听 `ws://0.0.0.0:8080`。
4. **配置注入**:用 Coolify 的 **Environment Variables**(UI 或 API `POST/PATCH /applications/{uuid}/envs`)注入 OAuth issuer、JWKS URL、监听端口、WebRTC UDP 端口、NAT 公网 IP 等。改完 env 要触发 `GET /deploy?uuid=` 重新部署才生效。
5. **关键架构点(单 UDP 端口)**:Pion 默认每个 PeerConnection 开随机 UDP 端口;要把所有 WebRTC 媒体收敛到**一个固定 UDP 端口**(便于 Coolify 只映射一个口、防火墙只放一个口),需用 `SettingEngine.SetICEUDPMux(webrtc.NewICEUDPMux(...))`,并配合 `SetNAT1To1IPs(公网IP)` 让 ICE candidate 通告正确的公网地址。这把 DESIGN.md 里“开放一段 UDP 端口范围”收敛成“单口”,更契合 Coolify 的单端口映射模型。

---

## 1. Coolify 的三个端口字段(务必区分)

Coolify 应用设置里三个概念,作用完全不同:

| 字段 | API 字段 | 作用 | 走 Traefik? | 用于本项目 |
|---|---|---|---|---|
| **Ports Exposes** | `ports_exposes` | 告诉 Docker(以及 Coolify→Traefik)容器**监听哪个端口**;第一个端口是健康检查默认端口 | 是(Traefik 据此把 HTTP 流量转进容器) | 信令/HTTP 端口,如 `8080` |
| **Ports Mappings** | `ports_mappings` | Docker 原生 `-p HOST:CONTAINER` 发布,**直接绑到宿主机 IP**,绕过 Traefik | **否** | WebRTC UDP 端口,如 `40000:40000/udp` |
| **Domains (FQDN)** | `fqdn` | 绑定域名,Traefik 据此做 Host 路由 + 自动 TLS | 是 | 信令的 `wss://chat.example.com` |

官方原文(coolify.io/docs/applications):
- Ports Exposes: *“Port exposes are required for Docker Engine to know which ports to expose. The first port will be the default port for health checks.”*
- Ports Mappings: *“This will map the port 8080 on the host system to the port 80 inside the container.”* 并警告 *“You will lose some functionality if you map a port to the host system, like Rolling Updates.”*

> openapi.json 中 `applications` 的 create/update/get 等 schema 均含 `ports_exposes`(string)与 `ports_mappings`(string)两个字段,证实可通过 API 设置。

### 1.1 关于 `/udp` 后缀(重要前提)

- **官方应用文档的 Ports Mappings 示例只写了 TCP**(`8080:80`),**没有**明示 `/udp` / `/tcp` 后缀;曾有 GitHub Improvement 提议显式支持协议后缀(`80:8080/UDP`)。
- 但 Ports Mappings 底层就是 Docker published-ports,**Docker/Compose 的标准协议后缀 `HOST:CONTAINER/udp` 是通用且生效的**。社区在 Coolify 上跑 TURN / 媒体服务器(UDP)正是这么做的:
  - 走 **Docker Compose builder** 部署时,直接在 compose 的 `ports:` 写 `"40000:40000/udp"`(已被社区验证可跑 TURN);
  - 走普通应用(Dockerfile/镜像)时,在 **Ports Mappings** 字段填 `40000:40000/udp`。
- **前提假设 A**:本调研采信“Ports Mappings 接受 `/udp` 后缀”。若你的 Coolify 版本该字段拒绝 `/udp`(早期版本只接受 `HOST:CONTAINER`),则**回退方案**是改用 **Docker Compose 部署类型**,在 compose `ports:` 里写 `"40000:40000/udp"`(100% 是 Docker 原生语义,无版本风险)。本文第 6 节同时给出两种骨架。

---

## 2. UDP 为什么不能走 Traefik(以及防火墙必须放行)

- Traefik 在 Coolify 中负责 **HTTP/HTTPS 7 层路由 + 自动 TLS**;它虽然技术上支持 UDP entrypoint,但 **UDP 只是裸 4 层负载均衡,没有 Host/路径路由语义**,且 Coolify 默认开了 HTTP/3(`--entrypoints.https.http3`),HTTP/3 本身占用 443/udp,会与自定义 UDP entrypoint 冲突。结论:**WebRTC 媒体 UDP 不要试图走 Traefik,直接用 host 端口映射**。
- Ports Mappings 直发到宿主机后:
  - **Docker 的 NAT iptables 规则会绕过 UFW**——官方原文:*“Coolify runs on Docker, which uses NAT-based iptables rules that can bypass traditional Linux firewalls like UFW … blocking ports using UFW alone will not be effective.”*
  - **所以放行要点**:
    1. **云厂商安全组/防火墙**必须放行该 **UDP 端口**(官方推荐优先用云厂商防火墙)。例如 AWS Security Group、阿里云/腾讯云安全组、Oracle Cloud Dashboard、Hetzner Firewall 等,加一条 inbound UDP 规则。
    2. 若主机用 UFW,**仅 UFW 放行不够**(Docker 发布的端口会绕过它),需要用 `ufw-docker` 才能真正按预期管控 Docker 发布端口。
  - 安全提示:Ports Mappings 默认绑 `0.0.0.0`(对公网开放),对面向公网的 WebRTC 媒体口这正是期望行为;但别误把数据库等内部端口也这么发布。

---

## 3. WSS:Traefik 终结 TLS,容器内监听明文 WS

这是最常见的误区,务必按下面来:

```
客户端  --- wss://chat.example.com (443, TLS) --->  Traefik(边缘终结 TLS)
                                                       |
                                                       | 明文 http/ws 转发(容器网络内)
                                                       v
                                              容器: ws://0.0.0.0:8080 (明文)
```

- 客户端连 `wss://<域名>`(TLS,443)。
- **Traefik 在边缘终结 TLS**(用 Coolify 自动申请的 Let's Encrypt 证书),然后以**明文 HTTP/WS** 转发到容器的 `ports_exposes` 端口。
- **容器内的 Go 服务端只监听明文 `ws://0.0.0.0:8080`**,**不要**在容器内再配证书/TLS。WebSocket 升级(`Upgrade: websocket`)是标准 HTTP 升级,Traefik 默认透传,无需额外标签。
- 因此 DESIGN.md 第 14 节“信令必须走 WSS”由 **Coolify+Traefik 在边缘自动满足**,服务端无需自带 Caddy/TLS。
- 服务端务必监听 `0.0.0.0`(不是 `127.0.0.1`),否则 Traefik 到不了容器(官方排障文档明确点名此坑)。

**需要的 Coolify 配置:**
- **Domains** 填 `https://chat.example.com`(Coolify 会自动签发并强制 HTTPS;若信令端口非 80,可写 `https://chat.example.com` 并由 Ports Exposes 指明容器端口)。
- **Ports Exposes** 填 `8080`(容器内监听端口)。
- 一般**无需手写 Traefik 标签**;Coolify 会根据 Domains + Ports Exposes 自动生成 Traefik labels。仅在需要特殊路由(如多个端口/中间件)时,才在应用的 “Labels” / Compose 里加自定义 `traefik.*` 标签。

> WebSocket 长连接超时:Traefik 对 WS 默认不主动断;若有需要,可在服务端做应用层心跳(ping/pong),无需改 Traefik。

---

## 4. 用 Coolify 环境变量注入配置

UI:应用 → **Environment Variables**,逐条加 `KEY=VALUE`;支持标记 Build-time / Runtime,以及 “Is Literal” 等。

API(以本地 openapi.json 为准,端点是 GET 触发部署这一反直觉点见 coolify-ops skill):

```bash
B=$COOLIFY_BASE_URL/api/v1
AUTH=(-H "Authorization: Bearer $COOLIFY_API_TOKEN")
UUID=$(curl -sS "${AUTH[@]}" $B/applications | jq -r '.[] | select(.name=="lumen-server") | .uuid')

# 新建 env: POST;已存在改值: PATCH。body 必填仅 key/value
curl -sS "${AUTH[@]}" -X POST -H 'Content-Type: application/json' \
  -d '{"key":"OAUTH_ISSUER","value":"https://auth.example.com"}' \
  $B/applications/$UUID/envs

# 批量
curl -sS "${AUTH[@]}" -X PATCH -H 'Content-Type: application/json' \
  -d '{"data":[{"key":"WEBRTC_UDP_PORT","value":"40000"},{"key":"NAT_PUBLIC_IP","value":"203.0.113.10"}]}' \
  $B/applications/$UUID/envs/bulk

# 改完必须重新部署才注入(仅 restart 不保证)
curl -sS "${AUTH[@]}" "$B/deploy?uuid=$UUID"
```

**建议注入的 env(对应 DESIGN.md 第 14 节配置项):**

| KEY | 示例值 | 说明 |
|---|---|---|
| `HTTP_ADDR` | `0.0.0.0:8080` | 信令/HTTP 监听(Traefik 转发目标);必须 0.0.0.0 |
| `OAUTH_ISSUER` | `https://auth.example.com` | OAuth2 issuer |
| `OAUTH_JWKS_URL` | `https://auth.example.com/.well-known/jwks.json` | JWKS 验签 |
| `OAUTH_CLIENT_ID` | `lumen-desktop` | 客户端 client_id(校验 aud) |
| `WEBRTC_UDP_PORT` | `40000` | Pion 单口 UDP 媒体端口(与 Ports Mappings 一致) |
| `NAT_PUBLIC_IP` | `203.0.113.10` | VPS 公网 IP,给 Pion `SetNAT1To1IPs` 通告 ICE host candidate |
| `SQLITE_PATH` | `/data/lumen.db` | SQLite 文件(配合 Coolify Persistent Storage 挂卷) |

> 数据持久化:SQLite 文件要放到 Coolify 的 **Persistent Storage**(卷或 bind mount,如挂到 `/data`),否则重新部署会丢。

---

## 5. 架构关键:把 WebRTC 收敛到“单个固定 UDP 端口”(Pion)

DESIGN.md 第 7.3/14 节说“开放一段 UDP 端口范围”。但 Coolify 的单端口映射模型 + 防火墙最小放行,更适合**单口**。Pion 支持把所有 PeerConnection 的媒体复用到一个 UDP 端口:

- API:`webrtc.SettingEngine.SetICEUDPMux(mux)`,其中 `mux = webrtc.NewICEUDPMux(logger, udpConn)`。
  - 上游签名:`NewICEUDPMux(logger logging.LeveledLogger, udpConn net.PacketConn)`(logger 传 `nil` 即可)。
- 配合 `settingEngine.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)`,让 ICE candidate 通告**公网 IP**(否则容器内通告的是 Docker 内网 IP,客户端连不上)。
- **限制**:UDPMux 只对 **host candidate** 生效。本项目服务端有公网 IP、客户端直连服务端,**不需要 STUN/TURN**(srflx/relay),正好落在 UDPMux 的适用范围内,与 DESIGN.md 第 7.3 节“通常不需要 STUN/TURN”一致。
- 若将来确实要端口范围而非单口:用 `ice.NewMultiUDPMuxFromPort(port)` 或回退到端口区间映射。

本项目落地组合:
- 容器内 Pion 监听 `udp 0.0.0.0:40000`(单口)。
- Coolify Ports Mappings:`40000:40000/udp`(host:container,直发宿主机)。
- env:`WEBRTC_UDP_PORT=40000`、`NAT_PUBLIC_IP=<VPS公网IP>`。
- 云安全组放行 `40000/udp` inbound。

---

## 6. 落地骨架

### 6.1 Dockerfile(Go 单二进制服务端)

```dockerfile
# ---- build ----
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# modernc.org/sqlite 是纯 Go,无需 CGO
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/lumen-server ./server

# ---- runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/lumen-server /app/lumen-server

# 关键:
# - 8080 = 信令/HTTP/WS,给 Traefik(Ports Exposes,第一个端口=健康检查口)
# - 40000/udp = WebRTC 媒体单口,给 host 直发(Ports Mappings)
EXPOSE 8080
EXPOSE 40000/udp

# 服务端必须监听 0.0.0.0,否则 Traefik 到不了容器
ENV HTTP_ADDR=0.0.0.0:8080 \
    WEBRTC_UDP_PORT=40000

USER app
ENTRYPOINT ["/app/lumen-server"]
```

> Coolify “普通应用(Dockerfile)”部署时:
> - **Ports Exposes** 填 `8080`
> - **Ports Mappings** 填 `40000:40000/udp`
> - **Domains** 填 `https://chat.example.com`
> - Persistent Storage 挂 `/data`(SQLite)

### 6.2 docker-compose(Coolify “Docker Compose” 部署类型 / 也是无版本风险的回退方案)

```yaml
services:
  lumen-server:
    build: .                      # 或 image: ghcr.io/you/lumen-server:latest
    restart: unless-stopped
    environment:
      HTTP_ADDR: "0.0.0.0:8080"
      WEBRTC_UDP_PORT: "40000"
      NAT_PUBLIC_IP: "${NAT_PUBLIC_IP}"      # 由 Coolify env 注入(VPS 公网 IP)
      OAUTH_ISSUER: "${OAUTH_ISSUER}"
      OAUTH_JWKS_URL: "${OAUTH_JWKS_URL}"
      OAUTH_CLIENT_ID: "${OAUTH_CLIENT_ID}"
      SQLITE_PATH: "/data/lumen.db"
    expose:
      - "8080"                    # 仅容器网络内暴露,供 Traefik 转发(= Ports Exposes 语义)
    ports:
      - "40000:40000/udp"         # 裸 UDP 直发宿主机,绕过 Traefik(= Ports Mappings 语义)
    volumes:
      - lumen-data:/data          # SQLite 持久化
    labels:
      # Coolify 通常据 Domains 自动生成以下标签,这里显式给出便于理解“边缘终结 TLS+明文转发到 8080”
      - "traefik.enable=true"
      - "traefik.http.routers.lumen.rule=Host(`chat.example.com`)"
      - "traefik.http.routers.lumen.entrypoints=https"
      - "traefik.http.routers.lumen.tls=true"
      - "traefik.http.routers.lumen.tls.certresolver=letsencrypt"
      - "traefik.http.services.lumen.loadbalancer.server.port=8080"  # 明文转发目标

volumes:
  lumen-data:
```

> 说明:
> - `expose: 8080` 不发布到宿主机,只在 Docker 网络内,Traefik 通过容器网络访问 → 信令走 Traefik(WSS 终结后明文转发到 8080)。
> - `ports: "40000:40000/udp"` 发布到宿主机 0.0.0.0 → WebRTC 媒体直发,不经 Traefik。
> - 在 Coolify 里用 Compose 部署时,FQDN/证书一般通过 Coolify 的 Domains 字段管理,上面的 `traefik.*` 标签可省略(Coolify 自动注入);保留是为了说明数据流。WebSocket 升级 Traefik 默认透传,无需特殊中间件。

### 6.3 Pion 单口 + NAT1To1 关键代码骨架

```go
import (
    "net"
    "os"
    "strconv"
    "github.com/pion/webrtc/v4"
)

func newWebRTCAPI() (*webrtc.API, error) {
    port, _ := strconv.Atoi(os.Getenv("WEBRTC_UDP_PORT")) // 40000
    udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: port})
    if err != nil {
        return nil, err
    }

    se := webrtc.SettingEngine{}
    // 所有 PeerConnection 复用这一个 UDP 端口
    se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udpConn))
    // 通告公网 IP,否则 host candidate 会是 Docker 内网地址
    if ip := os.Getenv("NAT_PUBLIC_IP"); ip != "" {
        se.SetNAT1To1IPs([]string{ip}, webrtc.ICECandidateTypeHost)
    }
    return webrtc.NewAPI(webrtc.WithSettingEngine(se)), nil
}
// 之后: api.NewPeerConnection(webrtc.Configuration{}) —— 全部走 40000/udp
```

---

## 7. 前提假设(明确写出)

- **假设 A(/udp 后缀)**:Coolify 当前版本的 **Ports Mappings** 字段接受 Docker 标准协议后缀 `HOST:CONTAINER/udp`。官方应用文档示例仅给 TCP 形式,未逐字写 `/udp`;若你的版本拒绝,回退到 **Docker Compose 部署类型**在 `ports:` 写 `"40000:40000/udp"`(Docker 原生语义,无版本风险)。**部署前请在你的 Coolify 实例上以一个临时 UDP 服务实测一次。**
- **假设 B(自动 TLS / Traefik)**:你的 Coolify 用默认 **Traefik** 代理(非 Caddy/自定义),且服务器已正确解析域名、能签 Let's Encrypt。Caddy 代理模式下 Domains 行为类似,但标签语法不同。
- **假设 C(单口可行)**:本项目服务端有公网 IP、客户端直连、不需要 STUN/TURN,因此 Pion UDPMux 的“仅 host candidate”限制不构成问题。
- **假设 D(防火墙)**:VPS 通过**云厂商安全组**放行 `40000/udp`;若用 UFW,需配 `ufw-docker`(否则 Docker 发布端口绕过 UFW)。
- **假设 E(端口号)**:`8080`(HTTP/WS)与 `40000`(UDP)为示例,可换成你实际选用的端口,只需保持 Dockerfile EXPOSE / Ports Exposes / Ports Mappings / env 四处一致。

---

## 8. 部署核对清单

- [ ] Dockerfile `EXPOSE 8080` + `EXPOSE 40000/udp`;服务监听 `0.0.0.0`。
- [ ] Coolify **Ports Exposes** = `8080`。
- [ ] Coolify **Ports Mappings** = `40000:40000/udp`(或 Compose `ports:`)。
- [ ] Coolify **Domains** = `https://chat.example.com`(自动 TLS,边缘终结)。
- [ ] 容器内**不**自管 TLS;信令监听明文 `ws://`。
- [ ] **云安全组放行 `40000/udp` inbound**(+ 443/tcp 给 WSS)。UFW 场景配 `ufw-docker`。
- [ ] env 注入 `NAT_PUBLIC_IP`=VPS 公网 IP;Pion `SetNAT1To1IPs` 已调用。
- [ ] Pion `SetICEUDPMux` 绑到 `40000`,与映射/防火墙一致。
- [ ] SQLite 走 Persistent Storage(`/data`)。
- [ ] 改 env 后 `GET /deploy?uuid=` 重新部署。
- [ ] 注意:用 Ports Mappings 会**失去 Rolling Updates**(部署时有短暂中断,语音会重连)。

---

## [参考链接]

- Coolify 官方 — Applications(Ports Exposes / Ports Mappings 字段与格式): https://coolify.io/docs/applications
- Coolify 官方 — Firewall(Docker iptables 绕过 UFW、ufw-docker、云安全组、默认端口): https://coolify.io/docs/knowledge-base/server/firewall
- Coolify 官方 — Custom Compose Overrides(Compose 端口绑定语法、`ports` 列表替换语义): https://coolify.io/docs/knowledge-base/custom-compose-overrides
- Coolify 官方 — 排障 No Available Server(Ports Exposes 字段、容器需监听 0.0.0.0): https://coolify.io/docs/troubleshoot/applications/no-available-server
- Coolify 官方 — Databases(Ports Mapping vs Public Port,端口映射永久 vs Nginx TCP 代理): https://coolify.io/docs/databases
- Coolify GitHub — Discussion #1014 “Expose udp ports”: https://github.com/coollabsio/coolify/discussions/1014
- Coolify GitHub — Discussion #2576 “Expose public port”(TURN over compose/UDP 社区验证): https://github.com/coollabsio/coolify/discussions/2576
- Coolify GitHub — Issue/Discussion #2498 & #3161 “Port mapping refactor”(协议后缀 UDP/TCP 提议): https://github.com/coollabsio/coolify/issues/2498
- Coolify openapi.json(`ports_exposes` / `ports_mappings` / `/applications/{uuid}/envs` 字段): 本地镜像 /Users/nanako/Dropbox/SProject/coolify-ops/docs/docs/api-reference/api/openapi.json
- Pion WebRTC — UDPMux 单端口实现 commit d0a5251: https://github.com/pion/webrtc/commit/d0a52518b0c1b43ad74efbac4a775185cf3cce8f
- Pion WebRTC — Discussion #1965 / #1787(SetICEUDPMux + SetNAT1To1IPs 单口+NAT): https://github.com/pion/webrtc/discussions/1965
- Pion WebRTC — pkg.go.dev(SettingEngine.SetICEUDPMux / NewICEUDPMux / SetNAT1To1IPs API): https://pkg.go.dev/github.com/pion/webrtc/v4
- Pion ICE — Issue #400(UDPMux 仅 host candidate 限制)/ NewMultiUDPMuxFromPort: https://github.com/pion/ice/issues/400
- Traefik 官方 — EntryPoints(UDP entrypoint、HTTP/3 占用 443/udp 冲突): https://doc.traefik.io/traefik/reference/install-configuration/entrypoints/
- ufw-docker(UFW 与 Docker 发布端口桥接): https://github.com/chaifeng/ufw-docker

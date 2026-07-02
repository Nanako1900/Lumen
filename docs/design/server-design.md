# Lumen 服务端详细设计（Go 单二进制）

> 文档版本: 1.0
> 状态: 详细设计（可直接实现）
> 适用范围: Lumen 服务端（单 Go 二进制；Coolify/Docker 部署）
> 权威依据: `docs/design/protocol-design.md`（接口契约，端点名/消息名/字段名/DDL 以其为准）
> 配套调研: `docs/research/01-pion-sfu.md`、`docs/research/02-coolify-udp.md`、`docs/research/05-oauth-pkce-jwks.md`

本文是服务端实现蓝图。任何与 `protocol-design.md` 冲突之处，**以接口契约为准**；本文不自创端点、消息类型、字段名或 DDL。

**版本归属约定**（与契约一致）：

| 标记 | 含义 |
|------|------|
| `[v0]` | 最小可用闭环：登录验签、文字历史、单语音频道收发、基本状态广播 |
| `[v1]` | 完整功能：频道 CRUD、踢人、资料双向同步、逐人音量（前端）、PTT/VAD（前端）、降噪（前端）、多频道 |
| `[v2]` | 推迟项：E2E 加密。仅见 [§9](#9-v2e2e-对服务端的影响简述) |

未标注默认 `[v0]`。

---

## 目录

1. [模块/包结构与依赖方向](#1-模块包结构与依赖方向)
2. [鉴权网关（auth）](#2-鉴权网关auth)
3. [信令模块（signaling / ws）](#3-信令模块signaling--ws)
4. [SFU 模块（sfu，Pion v4）](#4-sfu-模块sfupion-v4)
5. [频道/消息模块与 store（rest + store）](#5-频道消息模块与-storerest--store)
6. [配置、启动装配、并发模型、优雅关闭、可观测性、安全](#6-配置启动装配并发模型优雅关闭可观测性安全)
7. [部署（Coolify）](#7-部署coolify)
8. [v0/v1 归属汇总](#8-v0v1-归属汇总)
9. [v2（E2E）对服务端的影响简述](#9-v2e2e-对服务端的影响简述)
10. [登录 broker（账户中心 + 桌面登录中介）](#10-登录-broker账户中心--桌面登录中介)

---

## 1. 模块/包结构与依赖方向

### 1.1 设计原则

- **单二进制**：一个 `main` 装配所有模块，进程内通信，无微服务拆分。
- **小而聚焦**：每个包单一职责，文件 200~400 行典型、800 上限。
- **依赖单向**：上层依赖下层，下层不反向依赖上层；通过接口解耦（`store` 接口、广播回调）。
- **不可变优先**：DTO/配置一旦构造不再原地修改；房间内可变态由锁保护。

### 1.2 目录布局

```
lumen-server/
├── cmd/
│   └── lumen-server/
│       └── main.go              # 入口：装配、启动、broker janitor、优雅关闭（已落地，见 §6.2）
├── internal/
│   ├── config/                 # 环境变量加载与校验（fail-fast）
│   │   └── config.go           # 含 LUMEN_OAUTH_*/WEB_BASE_URL/两把 AES 密钥（§10.4）
│   ├── auth/                   # JWKS 验签 + owner 判定 + claims→profile 映射
│   │   ├── verifier.go         # keyfunc v3 + golang-jwt v5
│   │   ├── owner.go            # ownerSet（配置态）
│   │   ├── profile.go          # claims/userinfo → display_name/avatar_url
│   │   └── middleware.go       # REST Bearer 中间件
│   ├── secure/                 # 登录 broker 共享密码学原语（§10.2）
│   │   ├── aesgcm.go           # AES-256-GCM Sealer：Seal/Open（cookie）+ Encrypt/Decrypt（at-rest bytea）
│   │   └── token.go            # base64url / RandomToken / S256 / 常量时间比较（镜像 pkce.ts）
│   ├── broker/                 # OIDC 登录 broker + 账户中心（EdgeOne Functions 的 Go 移植，§10.1）
│   │   ├── broker.go           # Handler 装配 + Register(mux) 挂 9 路由
│   │   ├── http.go             # broker 私有 JSON 信封（{error:{code,message}}，区别于 rest 信封）
│   │   ├── oidc.go             # OIDC discovery/authorize/token/userinfo 客户端（confidential client）
│   │   ├── pkce.go             # 回环 handoff verifier/S256、loopback redirect_uri 校验
│   │   ├── loopback.go         # 127.0.0.1 回环 redirect_uri 白名单校验
│   │   ├── session.go          # 账户中心两枚 cookie（lumen_session / lumen_auth_flow，§10.5）
│   │   └── cors.go             # broker 独立 withCORS（仅供 Routes() 独立/测试用；生产用 rest 单点 CORS）
│   ├── store/                  # PostgreSQL 封装（jackc/pgx/v5）
│   │   ├── db.go               # 连接池、Store 接口、New/NewWithSealer（refresh sealer 注入）
│   │   ├── migrate.go          # 建表 DDL（含 broker_states / desktop_sessions，§10.3）
│   │   ├── users.go            # users upsert / 查询 / kick
│   │   ├── channels.go         # channels CRUD + 种子频道
│   │   ├── messages.go         # messages 插入 / 游标分页
│   │   ├── broker.go           # broker_states/desktop_sessions 读写 + 过期清理（§10.3）
│   │   └── ids.go              # ULID 生成
│   ├── rest/                   # REST handler（契约 §3）
│   │   ├── router.go           # 路由表 /api/v1/* + 挂 broker.Register + 单点 CORS
│   │   ├── cors.go             # 生产单点 withCORS（精确匹配 WEB_BASE_URL，§10.6）
│   │   ├── envelope.go         # 统一响应信封 + 错误码映射
│   │   ├── bootstrap.go        # GET /bootstrap
│   │   ├── me.go               # GET /me（Bearer；区别于 broker 的 cookie /api/me）
│   │   ├── channels.go         # GET/POST/PATCH/DELETE channels
│   │   ├── messages.go         # GET messages（分页）
│   │   ├── members.go          # GET members / POST kick
│   │   └── health.go           # GET /healthz
│   ├── signaling/              # WS hub：连接生命周期 + 消息路由 + 广播
│   │   ├── hub.go              # 全局 hub：连接注册表、广播
│   │   ├── client.go           # 单连接会话（读/写泵、sub 绑定）
│   │   ├── handshake.go        # auth/reauth 首帧握手
│   │   ├── router.go           # type → handler 分发
│   │   ├── messages_ws.go      # send_message → 持久化 → 广播
│   │   ├── voice_ws.go         # join/leave/speaking/mute
│   │   └── webrtc_ws.go        # webrtc_answer / ice_candidate 入站
│   ├── sfu/                    # Pion SFU
│   │   ├── api.go              # SettingEngine（UDPMux + NAT1To1 + mDNS off）
│   │   ├── room.go             # 单语音频道内存 Room
│   │   ├── rooms.go            # RoomManager：channel_id → Room
│   │   └── peer.go             # PeerConnection 生命周期 + 重协商
│   └── protocol/               # 共享 DTO（与契约 §3.5/§4 字段一一对应）
│       ├── dto.go              # User/Channel/Message/VoiceState
│       └── ws.go               # WS Envelope 与各 type 的 data 结构
└── go.mod
```

> `internal/` 防止被外部 import；`protocol/` 是所有层共享的纯数据定义（无业务逻辑），避免循环依赖。
>
> **架构变更（single-backend，权威）**：登录 broker 从 EdgeOne Pages Functions **整体移植到本 Go 服务端**（commit `82f344e` 落地 `internal/broker` + `internal/secure` + `store` broker 表 + `cmd/lumen-server/main.go`）。EdgeOne Pages 现为**纯静态**——只发布构建后的 React SPA（`website/dist/`），**无 Pages Functions、无 EdgeOne KV、无任何 secret**。因此本 Go 服务端不再只是"资源服务器"，而是**唯一后端 = 资源服务器（JWT/JWKS 验签，既有）+ OIDC 登录 broker（新增）**。`client_secret` 与 `refresh_token` **只**存在于服务端（Postgres；`refresh_token` 用独立密钥加密于列 `desktop_sessions.refresh_token_enc`）。详见 [§10](#10-登录-broker账户中心--桌面登录中介)。

### 1.3 依赖方向图

```
                      ┌─────────────┐
                      │    main     │  装配所有模块、注入依赖
                      └──────┬──────┘
        ┌──────────────┬─────┴──────┬───────────────┐
        ▼              ▼            ▼                ▼
   ┌─────────┐   ┌──────────┐  ┌──────────┐   ┌─────────┐
   │  rest   │   │signaling │  │   sfu    │   │ config  │
   └────┬────┘   └────┬─────┘  └────┬─────┘   └─────────┘
        │             │             │
        │   ┌─────────┴───────┐     │  signaling 持有 sfu.RoomManager
        ▼   ▼                 ▼     ▼
     ┌──────────┐         ┌──────────┐
     │   auth   │         │  store   │
     └──────────┘         └──────────┘
        │                     │
        └────────┬────────────┘
                 ▼
           ┌──────────┐
           │ protocol │  纯 DTO，无依赖（最底层）
           └──────────┘
```

依赖规则：

| 包 | 依赖 | 不依赖 |
|----|------|--------|
| `protocol` | 标准库 | 任何业务包（最底层） |
| `config` | 标准库、`secure`（仅用 `DecodeKey` 校验 AES 密钥长度） | 其它业务包 |
| `secure` | 标准库（`crypto/*`） | 任何业务包（与 `protocol` 同为底层） |
| `store` | `protocol`、`secure`（refresh sealer）、`jackc/pgx/v5`、`oklog/ulid` | `rest`/`signaling`/`sfu`/`auth`/`broker` |
| `auth` | `protocol`、`config`、`keyfunc/v3`、`golang-jwt/v5`、`go-oidc/v3` | `rest`/`signaling`/`sfu`/`broker` |
| `sfu` | `protocol`、`pion/webrtc/v4`、`pion/ice/v4` | `rest`/`signaling`/`store`（解耦） |
| `broker` | `config`、`store`、`secure`、`golang.org/x/oauth2`/`go-oidc/v3` | `rest`/`signaling`/`sfu`/`auth`（自管 handoff/cookie 鉴权，不用 Bearer 中间件） |
| `rest` | `protocol`、`auth`、`store`、`config`，`signaling.Broadcaster` 接口，及 `*broker.Handler`（挂 `Register` + 单点 CORS） | `sfu`（仅经接口间接触发） |
| `signaling` | `protocol`、`auth`、`store`、`sfu` | `rest`/`broker` |
| `main` | 全部 | — |

**关键解耦点**：

- `sfu` 不直接 import `signaling`。`sfu` 通过注入的回调把"需下发的 offer / ice / 房间事件"推回信令层（见 [§4.6](#46-sfu-与信令的衔接回调接口)）。
- `rest` 的 owner CRUD 需要触发 WS 广播（`channel_created` 等）与关闭语音 Room（删频道时）。`rest` 依赖 `signaling` 暴露的窄接口 `Broadcaster`，而非反过来。

```go
// internal/signaling/hub.go —— 供 rest 调用的窄接口
type Broadcaster interface {
    // BroadcastAll 向所有已鉴权在线连接发送一条 WS 消息。
    BroadcastAll(msg protocol.Envelope)
    // DisconnectUser 断开某用户全部连接（踢人用），并移出语音房间。
    // reasonCode 即 auth_error.code（踢人传 "KICKED"）：对该用户每条活动连接
    // 先 sendNow(auth_error{code: reasonCode, ...}) 再关闭，使断开原因真正告知客户端。
    DisconnectUser(userID string, reasonCode string)
    // CloseVoiceChannel 关闭某语音频道 Room（删频道用），广播 user_left。
    CloseVoiceChannel(channelID string)
}
```

**`DisconnectUser` 实现语义（kick-1/kick-4，让已存在的 `reasonCode` 形参真正被消费）**：

```go
// internal/signaling/hub.go（要点）
// DisconnectUser 对该用户每条活动连接：先下发 auth_error 告知断开原因，再关闭。
func (h *Hub) DisconnectUser(userID string, reasonCode string) {
    h.mu.RLock()
    conns := append([]*Client(nil), h.byUser[userID]...) // 锁内拷贝，锁外操作
    h.mu.RUnlock()
    for _, c := range conns {
        // 复用握手失败时"auth_error + 关闭"同形路径（sendNow 同步写出，绕过 send chan）。
        // 踢人时 reasonCode = "KICKED"，data 带 kicked_until/retry_after（见下）。
        c.sendNow(c.hub.kickedAuthError(userID, reasonCode))
        c.closeConn() // 从 Hub 注销 + 移出语音房间（按连接粒度，见 §3.3）
    }
}
```

> `kickedAuthError` 读取 `users.kicked_until` 组装 `auth_error{code: reasonCode, message:"你已被移出服务器", kicked_until, retry_after}`（`reasonCode != "KICKED"` 或 `kicked_until` 未来值不存在时省略 `kicked_until/retry_after`）。结构化日志（[§6.5](#65-日志与可观测性)）记 `reasonCode` 与 `sub` 前缀（脱敏）。

---

## 2. 鉴权网关（auth）

实现契约 §2。两个通道共用同一个 `Verifier`：REST 经中间件、WS 经握手。

> **token 来源（信息性，服务端逻辑不变）**：`access_token` 由官网（`example.com`，confidential OIDC client）作为中介从 IdP 取得，再交给桌面客户端/网页（详见 [./web-design.md](./web-design.md)）。但服务端**只验 IdP 签发的 JWT**（`aud=lumen-api` = `LUMEN_OAUTH_AUDIENCE`），验签逻辑与信任锚（IdP 的 JWKS）**完全不变**——服务端不感知、不依赖官网中介，仍按本节用 IdP 的 JWKS 本地验 `iss/aud/exp`。中介仅改变 token 的“分发路径”，不改变 token 的“签发方”与服务端的“验证方式”。

### 2.1 JWKS 本地验签（keyfunc v3 + golang-jwt v5）

库基线（调研 §0）：

| 库 | 导入路径 | 版本 |
|----|----------|------|
| keyfunc | `github.com/MicahParks/keyfunc/v3` | >= v3.5.0（推荐 v3.8.0；**避开撤回版本** v3.0.0–v3.3.7） |
| jwt | `github.com/golang-jwt/jwt/v5` | v5.3.0 |
| go-oidc | `github.com/coreos/go-oidc/v3/oidc` | v3（仅用于 userinfo 补齐） |

`Verifier` 在启动时构造一次，全局复用；后台 goroutine 自动刷新/轮换公钥，遇未知 `kid` 主动刷新。

```go
// internal/auth/verifier.go
package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// Claims 期望的 access_token 声明（契约 §2.3 字段映射）。
type Claims struct {
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Picture           string `json:"picture"`
	jwt.RegisteredClaims // Subject(sub)/Issuer/Audience/ExpiresAt
}

// Verifier 持有自动刷新的 JWKS keyfunc 与校验参数（构造后不可变）。
type Verifier struct {
	kf       keyfunc.Keyfunc
	issuer   string
	audience string
	leeway   time.Duration
}

// NewVerifier 用 ctx 控制后台刷新 goroutine 生命周期（关闭时 cancel 回收）。
func NewVerifier(ctx context.Context, jwksURL, issuer, audience string) (*Verifier, error) {
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL}) // 自动拉取+缓存+轮换
	if err != nil {
		return nil, fmt.Errorf("初始化 JWKS keyfunc 失败: %w", err)
	}
	return &Verifier{kf: kf, issuer: issuer, audience: audience, leeway: 30 * time.Second}, nil
}

// Verify 验签并校验 iss/aud/exp，返回通过的 Claims。
func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(
		tokenString, claims, v.kf.Keyfunc,
		jwt.WithValidMethods([]string{"RS256"}), // 防 alg 混淆 / none 攻击
		jwt.WithIssuer(v.issuer),                // 校验 iss
		jwt.WithAudience(v.audience),            // 校验 aud
		jwt.WithExpirationRequired(),            // 要求 exp（exp/nbf/iat 默认校验）
		jwt.WithLeeway(v.leeway),                // 容忍时钟漂移
	)
	if err != nil {
		return nil, fmt.Errorf("JWT 校验失败: %w", err) // 调用方据 errors.Is(err, jwt.ErrTokenExpired) 区分过期
	}
	if !token.Valid {
		return nil, fmt.Errorf("JWT 无效")
	}
	return claims, nil
}
```

**错误区分**（用于映射契约错误码，[§5.4](#54-错误码映射)）：

| 判定 | 错误码 | 备注 |
|------|--------|------|
| `errors.Is(err, jwt.ErrTokenExpired)` | `TOKEN_EXPIRED` | token 过期，给刷新机会（WS）或 401（REST） |
| 其它验签失败（签名/iss/aud/格式） | `TOKEN_INVALID` | — |
| 缺 Bearer 头（REST）/ 首帧非 auth（WS） | `UNAUTHENTICATED` / `auth_error` | — |

### 2.2 owner 判定（配置态，不入库）

实现契约 §5.3：owner 由 `LUMEN_OWNER_SUBJECTS` 决定，内存 `set[sub]`，判定 O(1)，**绝不写库**。

```go
// internal/auth/owner.go
package auth

// OwnerSet 是不可变的 owner subject 集合（构造后只读）。
type OwnerSet struct {
	subs map[string]struct{}
}

// NewOwnerSet 从逗号分隔的配置值构造（已 trim、去空项）。
func NewOwnerSet(subjects []string) *OwnerSet {
	m := make(map[string]struct{}, len(subjects))
	for _, s := range subjects {
		if s != "" {
			m[s] = struct{}{}
		}
	}
	return &OwnerSet{subs: m}
}

// IsOwner 判定某 sub 是否 owner。
func (o *OwnerSet) IsOwner(sub string) bool {
	_, ok := o.subs[sub]
	return ok
}
```

`User.is_owner` 是**计算字段**：`store` 取出的 `User` 不含该字段；在 REST/WS 序列化前由 `OwnerSet.IsOwner(user.OAuthSubject)` 注入。统一通过一个组装函数完成：

```go
// internal/auth/profile.go
func ToDTO(u store.User, owners *OwnerSet) protocol.User {
	dto := protocol.User{ /* id, oauth_subject, display_name, avatar_url, created_at, updated_at */ }
	dto.IsOwner = owners.IsOwner(u.OAuthSubject)
	return dto
}
```

### 2.3 claims → profile 字段映射

实现契约 §2.7 字段映射规则：

| 目标列 | 来源（按优先级回退） |
|--------|---------------------|
| `display_name` | claims `name` → `preferred_username` → `sub` |
| `avatar_url` | claims `picture`（空则空字符串，不本地存储头像） |

```go
// internal/auth/profile.go
package auth

import "strings"

// Profile 是从 token/userinfo 归一化出的资料（不可变值对象）。
type Profile struct {
	Subject     string
	DisplayName string
	AvatarURL   string
}

// ProfileFromClaims 按契约 §2.7 回退规则映射。
func ProfileFromClaims(c *Claims) Profile {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = strings.TrimSpace(c.PreferredUsername)
	}
	if name == "" {
		name = c.Subject
	}
	return Profile{Subject: c.Subject, DisplayName: name, AvatarURL: strings.TrimSpace(c.Picture)}
}
```

**userinfo 补齐**（契约 §2.7：JWT 缺 name/picture 时）：若 `c.Name`、`c.PreferredUsername`、`c.Picture` 全为空，用该 access_token 拉一次 userinfo 并解析 `name/preferred_username/picture`，再走同一回退规则。**userinfo 端点的获取方式唯一**：由 `oidc.NewProvider(ctx, issuer)` 经 OIDC discovery（`.well-known/openid-configuration`）自动得到，再用 `provider.UserInfo(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: raw}))` + `userInfo.Claims(&dst)`（go-oidc 内置 UserInfo 不含这些字段，必须用 `Claims` 反序列化）。`LUMEN_OAUTH_USERINFO_URL` 为**可选覆盖**（discovery 不可用或需手动指定时才配置、直接 GET 它），二者取其一，**不重复消费**。userinfo 调用包一层短超时（3s）与失败降级（仅用 claims 已有值，不阻断登录）。

> v0 通常 access_token 已含 name/picture（Keycloak 等可在 token 映射器配置），userinfo 补齐属兜底；标注为 `[v0]` 兜底 + `[v1]` 完整双向同步触发点。

### 2.4 连接会话与 sub 绑定

- **REST**：无状态。每个受保护请求经中间件验签 → 把 `*Claims` 放入 `request.Context()`；handler 取 `sub` 与 `is_owner`。
- **WS**：有状态。握手成功后，把 `sub`、当前 token 的 `exp`、`*store.User`、`is_owner` 绑定到该连接的 `Client` 会话对象（见 [§3.2](#32-单连接会话client)）。`reauth` 时原地更新绑定的 token/exp，不换连接。

### 2.5 REST Bearer 中间件

```go
// internal/auth/middleware.go
package auth

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey string

const ClaimsKey ctxKey = "claims"

// RequireAuth 校验 Bearer，失败写统一错误信封（由 rest 包提供的 writeErr）。
func RequireAuth(v *Verifier, writeErr func(http.ResponseWriter, int, string, string), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "UNAUTHENTICATED", "缺少 Bearer 令牌")
			return
		}
		claims, err := v.Verify(strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			code := "TOKEN_INVALID"
			if isExpired(err) {
				code = "TOKEN_EXPIRED"
			}
			writeErr(w, http.StatusUnauthorized, code, "令牌校验失败")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ClaimsKey, claims)))
	})
}

// RequireOwner 在 RequireAuth 之后链式使用：检查 sub ∈ ownerSet，否则 403 FORBIDDEN。
func RequireOwner(owners *OwnerSet, writeErr func(http.ResponseWriter, int, string, string), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := r.Context().Value(ClaimsKey).(*Claims)
		if claims == nil || !owners.IsOwner(claims.Subject) {
			writeErr(w, http.StatusForbidden, "FORBIDDEN", "需要 owner 权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

> 软封禁（kick）在 WS 握手层强制（契约：冷却期内 `auth_error code=KICKED`）。REST 端点本身不做 kick 拦截（REST 仅读 + owner 管理；被踢者真正被切断的是 WS/语音）。可选加固：REST 中间件也查 `kicked_until` 返回 403 `KICKED`——列为 `[v1]` 可选。

---

## 3. 信令模块（signaling / ws）

实现契约 §4。WebSocket 升级用 `github.com/coder/websocket`（原 nhooyr.io/websocket，context 友好、API 简洁）或 `gorilla/websocket`；本设计以 `coder/websocket` 描述（任一可，行为等价）。

### 3.1 Hub：全局连接注册表与广播

```go
// internal/signaling/hub.go
package signaling

import (
	"sync"

	"lumen/internal/protocol"
	"lumen/internal/sfu"
	"lumen/internal/store"
)

// Hub 持有所有已鉴权连接，提供广播与定向断开。并发安全。
type Hub struct {
	mu       sync.RWMutex
	clients  map[*Client]struct{}     // 全部已鉴权连接
	byUser   map[string][]*Client     // user_id → 该用户的多个连接（同一用户可多端）
	rooms    *sfu.RoomManager         // SFU 房间管理器（注入）
	store    store.Store              // 持久化（注入）
	verifier *auth.Verifier           // 验签器（注入）
	owners   *auth.OwnerSet           // owner 判定（注入）
}
```

广播策略（契约决定广播范围）：

| 广播 | 范围 | 触发 |
|------|------|------|
| `message` | **全部在线连接**（文字消息全局可见） | `send_message` 成功 |
| `user_updated` | 全部在线连接 | 资料同步检测到变化 |
| `channel_created/updated/deleted` | 全部在线连接 | owner REST 端点 |
| `user_joined`/`user_left` | **同语音频道成员** | join/leave/断线/踢人/删频道 |
| `speaking_state`/`mute_state` | 同语音频道**其他**成员 | 上报后转发 |
| `webrtc_offer`/`ice_candidate` | **定向**到单个连接 | 重协商 |

`Hub` 提供两类发送原语：

```go
// 向所有已鉴权连接发送（实现 Broadcaster.BroadcastAll）。
func (h *Hub) BroadcastAll(msg protocol.Envelope)

// 向某语音频道成员发送（excludeUserID 可空，用于"广播给其他人"）。
func (h *Hub) BroadcastToChannel(channelID string, msg protocol.Envelope, excludeUserID string)

// 向某用户的全部连接发送（资料/定向）。
func (h *Hub) SendToUser(userID string, msg protocol.Envelope)
```

发送实现：把 `Envelope` JSON 编码后投递到每个 `Client` 的有缓冲发送 channel；写泵串行写出（见下）。Hub 锁仅保护注册表的读写，**不**在持锁期间做网络 IO。

### 3.2 单连接会话（Client）

```go
// internal/signaling/client.go
package signaling

import (
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Client 表示单个 WS 连接的会话（一连接一 goroutine 对）。
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan protocol.Envelope // 有缓冲；写泵消费

	// 鉴权后绑定（reauth 时原地更新 token/exp）。
	authed   atomic.Bool
	userID   string      // 内部 user.id
	subject  string      // oauth sub
	isOwner  bool
	tokenExp atomic.Int64 // 当前 token 的 exp（unix 秒）

	// 当前所在语音频道（同一时刻至多一个，契约 §4.4）。
	voiceChannelID atomic.Value // string；空表示不在任何语音频道
}
```

**两 goroutine 模型**（每连接）：

- **读泵 `readLoop`**：循环 `conn.Read` → JSON 反序列化 `Envelope` → 分发（[§3.4](#34-消息路由)）。读出错/EOF → 触发清理。
- **写泵 `writeLoop`**：`for msg := range c.send { conn.Write(msg) }`。**唯一**写出口，避免并发写同一连接（WS 库要求写串行）。另起 ticker 每 30s 发协议层 ping（契约 §4.1）。

`send` channel 满（慢客户端）时的策略：丢弃该连接（关闭 + 清理），避免内存堆积——小规模场景下简单可靠。

### 3.3 连接生命周期

```
建立 WS ──▶ 启动 5s 握手超时定时器 ──▶ 仅接受 auth 帧
   │                                        │
   │  收到 auth ──▶ Verify ──▶ upsert(资料同步) ──▶ 绑定 sub ──▶ auth_ok
   │                  │失败                                         │
   │                  ▼                                            ▼
   │              auth_error + 关闭                          注册到 Hub
   │                                                              │
   │  超时未鉴权 ──▶ auth_error(HANDSHAKE_TIMEOUT) + 关闭           │
   │                                                              ▼
   │                                              进入正常消息循环（接受全部 type）
   │                                                              │
断开（Read err / 写泵失败 / 被踢 / 服务关闭）                       │
   └──────────────────────────────────────────────────────────────┘
        清理：从 Hub 注销 → 若在语音频道则按连接粒度收敛（见下）→ 关闭 send → 关闭 conn
```

**连接清理的语音收敛（多端去重，loop-2）**：Hub 支持同一 `user_id` 多端连接（`byUser[userID]` 多条），但 SFU Room 以 `user_id` 单键、单 PC。清理某条连接时：

- 若被清理的连接是该 user 在该房的**语音活动连接**（`member.activeClient`），且该 user 在该房**已无任何其它连接** → 调 `RoomManager.Leave(channelID, userID)` 移出 Room（`pc.Close` + 删 trackLocals + `signalPeerConnections`）+ 经回调广播 `user_left`。
- 若该 user 在该房仍有其它连接（多端） → **仅清理本连接**，保留 Room 内 member（人未离开，只是某端断开），**不广播 user_left**。
- 被踢（`DisconnectUser`，user 粒度）/进程关闭：清掉该 user 全部连接，由 `RoomManager.Leave/LeaveAll` 整体移出并广播一次 `user_left`。被踢时先 `sendNow(auth_error{code:"KICKED", ...})` 再关闭连接（见 [§1.3](#13-依赖方向图) `DisconnectUser`）。

握手实现（契约 §2.5 / §2.6）：

```go
// internal/signaling/handshake.go（要点伪代码）
func (c *Client) handshake(ctx context.Context) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	for {
		select {
		case <-deadline.C:
			c.sendNow(authError("HANDSHAKE_TIMEOUT", "握手超时"))
			return errClose
		default:
		}
		env, err := c.readEnvelope(ctx) // 带读超时
		if err != nil { return err }
		if env.Type != "auth" {
			c.sendNow(authError("TOKEN_INVALID", "需先鉴权"))
			return errClose // 鉴权前只接受 auth
		}
		return c.applyAuth(env, false) // false=非 reauth
	}
}
```

`applyAuth`（auth 与 reauth 共用，契约 §2.5/§2.6/§2.7）：

1. `Verify(access_token)` → 失败：`auth`（首帧）回 `auth_error` 并关闭；`reauth` 回 `error code=TOKEN_*` 不关闭（30s 窗口）。
2. **检查软封禁（auth 与 reauth 均执行，loop-5）**：若 `users.kicked_until > now` → `auth_error code=KICKED` 关闭。`auth_error.data` 带 `kicked_until`（RFC3339 UTC，对齐契约 §7.4）与 `retry_after`（剩余秒数 = `kicked_until - now`），仅 `KICKED` 时出现（其它 code 不带）。`reauth` 同样执行本判定，防止冷却前已建立的旧连接经 reauth 续命绕过软封禁。
3. `ProfileFromClaims`（必要时 userinfo 补齐）→ `store.UpsertUser(sub, displayName, avatarURL)`，返回 `(user, changed bool)`。
4. 绑定 `userID/subject/isOwner/tokenExp` 到 `Client`；首帧时注册进 Hub。
5. 回 `auth_ok{user, server_time, reauth}`。
6. 若 `changed`（资料**实际变化**，见 §5.2 changed 语义）→ `Hub.BroadcastAll(user_updated{user})`（契约 §2.7 / §4.4）。**首次 INSERT（新用户首登）一律 `changed=false`，不广播 `user_updated`**（sync-3）；新成员可见性由 REST `bootstrap`/`members` 与其加入语音时的 `user_joined` 快照负责。

### 3.4 消息路由

鉴权后按 `Envelope.type` 分发（契约 §4.2 总表）：

```go
// internal/signaling/router.go
func (c *Client) dispatch(ctx context.Context, env protocol.Envelope) {
	switch env.Type {
	case "reauth":          c.handleReauth(env)        // [v1]
	case "join_channel":    c.handleJoin(ctx, env)     // [v0]
	case "leave_channel":   c.handleLeave(env)         // [v0]
	case "send_message":    c.handleSendMessage(env)   // [v0]
	case "speaking_state":  c.handleSpeaking(env)      // [v0]
	case "mute_state":      c.handleMute(env)          // [v1]
	case "webrtc_answer":   c.handleAnswer(env)        // [v0]
	case "ice_candidate":   c.handleICE(env)           // [v0]
	default:
		c.send <- wsError("VALIDATION_ERROR", "未知消息类型", env.ID)
	}
}
```

**TOKEN_EXPIRED 中途处理 [v1]**（契约 §2.6 规则 3）：在需要"动作权限"的入站消息（send_message / join_channel 等）处理前，检查 `tokenExp`：若已过期 → 回 `error code=TOKEN_EXPIRED`（不断开），启动/复用 30s reauth 窗口定时器；超时未收到合法 `reauth` 则关闭连接。**[v0]**：不实现该中途路径，绑定 token 过期即关闭连接，由客户端重连重新 `auth`。

> **`reauth` 的软封禁复查 [v1]**（loop-5）：`handleReauth` 复用 `applyAuth`，因此其第 2 步的 `kicked_until` 判定对 reauth 同样生效——冷却期内 `reauth` 回 `auth_error code=KICKED`（带 `kicked_until/retry_after`）并关闭，防止旧连接经 reauth 续命绕过软封禁。

### 3.5 文字消息路由（send_message → message）

实现契约 §4.5：

```go
// internal/signaling/messages_ws.go（要点）
func (c *Client) handleSendMessage(env protocol.Envelope) {
	var d struct{ ChannelID, Content string }
	parse(env.Data, &d)

	content := strings.TrimSpace(d.Content)
	if content == "" || utf8.RuneCountInString(content) > 4000 {
		c.send <- wsError("VALIDATION_ERROR", "content 不能为空且 ≤ 4000 字符", env.ID)
		return
	}
	ch, err := c.hub.store.GetChannel(d.ChannelID)
	if err != nil || ch.Type != "text" {
		c.send <- wsError("NOT_FOUND", "频道不存在或非文字频道", env.ID)
		return
	}
	msg, err := c.hub.store.InsertMessage(d.ChannelID, c.userID, content) // 生成 ULID + created_at
	if err != nil {
		c.send <- wsError("INTERNAL", "保存失败", env.ID)
		return
	}
	// 内联 author 快照（契约 §3.5 Message.author）
	dto := toMessageDTO(msg, c.currentUserDTO())
	c.hub.BroadcastAll(protocol.Envelope{Type: "message", Data: dto})
}
```

> 失败回 `error` 并回带请求 `id`（`env.ID`）。成功不单独 ack，直接广播 `message`（发送者也收到，作为自己消息的确认与回显）。

### 3.6 语音状态路由

- `join_channel`（契约 §4.4，**含失败回执契约**）：
  - **校验失败显式分支**（与 `handleSendMessage` 同形，均回带请求 `env.ID` 以便客户端 `error.ref` 关联）：
    - `ch, err := store.GetChannel(channelID)`；err 或不存在 → `c.send <- wsError("NOT_FOUND", "频道不存在", env.ID)` 返回。
    - `ch.Type != "voice"` → `c.send <- wsError("VALIDATION_ERROR", "该频道非语音频道", env.ID)` 返回。
    - **多端语音去重（loop-2）**：Join 前若该 `user_id` 已有另一条连接在某语音频道（含本频道），先对其旧的语音连接执行隐式 voice-leave（旧连接 `pc.Close` + 解绑，**不广播 user_left**——人未离开，只是换端），再让新连接持有该 user 的 PC。
    - 若该用户当前在**别的**语音频道 → 先隐式 leave 旧频道（移出旧 Room、广播旧频道 `user_left`）。
    - `RoomManager.Join(...)` 返回 err → `c.send <- wsError("INTERNAL", "加入语音失败", env.ID)`，并回滚已建的部分 PC/Room 状态。
  - **全部成功后**才：向频道**其他**成员广播 `user_joined` → 向加入者**逐条**回放房内现有成员（**排除加入者自身**，voice-5）的 `user_joined`（契约 §4.4 快照语义）→ 触发 SFU 重协商（[§4](#4-sfu-模块sfupion-v4)）。
  - **回放/广播 user_joined 的 user 快照来源（loop-4 / voice-5）**：`RoomManager.Join` 返回房内现有成员 `userID` 列表后，由信令层逐个用 `auth.ToDTO(store.GetUserByID(memberID), owners)` 在 Hub 内组装 `data.user`（Hub 已持有 `store` 与 `owners`），定向发给加入者；对外广播 `user_joined` 可直接复用加入者的 `currentUserDTO()`。`RoomEventSink.UserJoined` 仅传 `VoiceState`，`User` 字段一律在 Hub 内补齐（见 [§4.6](#46-sfu-与信令的衔接回调接口)），避免空 user。
  - **PC 生命周期（voice-3）**：一个用户在一个语音频道对应恰好一个服务端 PC，由该 Room 持有、随 join 建、随显式/隐式 leave（含断线/切频道）`Close`，**不跨频道复用**。当同一 `user_id` 以新连接再次 join 同一频道（如重连）时，若 `Room.members[userID]` 仍存在残留 member，先 `removeMember`（Close 旧 PC、删其 trackLocals、`signalPeerConnections`）再 `addPeer`，以新连接的 `send` 闭包重建，避免向已死连接发 offer 的双 PC 竞态。
- `leave_channel`（**含失败回执契约**）：目标频道不存在或用户不在该房 → `c.send <- wsError("NOT_FOUND"/"VALIDATION_ERROR", ..., env.ID)`；否则 `RoomManager.Leave` → 广播 `user_left`（仅当该 user 在该房最后一条连接离开，多端见 [§3.3](#33-连接生命周期)）→ 其余 peer 重协商（移除该用户 track）。
- `speaking_state`（双向）：仅在翻转时上报；服务端补 `channel_id/user_id` 后向同频道其他成员转发（契约 §4.4）。
- `mute_state`（双向，[v1]）：同理转发，并更新 Room 内 `VoiceState.muted/deafened`（供新加入者快照）。

VoiceState 内存态字段（`muted/deafened/speaking`）随上述消息更新；该状态由 `sfu.Room` 持有（[§4.2](#42-room-内存结构)），信令层经 `RoomManager` 读写。

---

## 4. SFU 模块（sfu，Pion v4）

实现契约 §4.6 与调研 01 的 sfu-ws 模式。库：`github.com/pion/webrtc/v4`（>= v4.2.5，含 CVE-2026-26014 修复）、`github.com/pion/ice/v4`、`github.com/pion/rtp`。

### 4.1 共享 API：SettingEngine（UDPMux + NAT1To1 + mDNS off）

**必须在创建任何 PeerConnection 之前**初始化一次，全部 PC 共享此 `*webrtc.API`（调研 §1.2）。

```go
// internal/sfu/api.go
package sfu

import (
	"net"
	"strings"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

// NewAPI 构造容器/VPS 友好的共享 API：单 UDP 端口 + 公网 IP host 候选 + 关 mDNS。
func NewAPI(udpPort int, publicIP string) (*webrtc.API, error) {
	se := webrtc.SettingEngine{}

	// (a) 单 UDP 端口 mux：所有 PeerConnection 收敛到一个口（契约 §4.6）。
	//     监听所有网卡（容器内等价绑 0.0.0.0:udpPort/udp），排除虚拟网卡。
	mux, err := ice.NewMultiUDPMuxFromPort(udpPort,
		ice.UDPMuxFromPortWithInterfaceFilter(func(name string) bool {
			return !strings.Contains(name, "docker") &&
				!strings.Contains(name, "veth") &&
				!strings.HasPrefix(name, "br-")
		}),
	)
	if err != nil {
		return nil, err
	}
	se.SetICEUDPMux(mux)

	// (b) 用公网 IP 替换 host 候选（1:1 NAT，调研 §2）。否则候选是 Docker 内网 IP，客户端连不上。
	if publicIP != "" {
		se.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)
	}

	// (c) 容器内关闭 mDNS，避免 .local 候选与多播失败（调研 §4）。
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)

	return webrtc.NewAPI(webrtc.WithSettingEngine(se)), nil
}
```

容器内 ICE 注意（调研 §4，逐条落实）：

| 要点 | 落实 |
|------|------|
| 绑 `0.0.0.0` 收包 | `NewMultiUDPMuxFromPort` 监听所有网卡 |
| 宣告公网 IP | `SetNAT1To1IPs([]{publicIP}, Host)` |
| 关 mDNS | `SetICEMulticastDNSMode(MulticastDNSModeDisabled)`（枚举，非数字） |
| 排除虚拟网卡 | `UDPMuxFromPortWithInterfaceFilter` 排除 docker/veth/br- |
| 端口四处一致 | `LUMEN_WEBRTC_UDP_PORT` = Dockerfile EXPOSE = Coolify Ports Mappings = 防火墙放行 |
| 无需 STUN/TURN | 服务端公网可达 + host 候选；UDPMux 仅 host 候选限制在本场景不构成问题 |

### 4.2 Room 内存结构

每语音频道一个 Room；契约 §5.4：纯内存态，进程重启清空。

```go
// internal/sfu/room.go
package sfu

import (
	"sync"

	"github.com/pion/webrtc/v4"
	"lumen/internal/protocol"
)

// member 是房间内一个成员的全部运行态。
type member struct {
	userID       string
	activeClient *Client                     // 当前持有该 user 语音的连接（多端去重，loop-2）；send/pc 绑定到它
	pc           *webrtc.PeerConnection
	state        protocol.VoiceState         // muted/deafened/speaking（内存态）
	send         func(env protocol.Envelope) // 定向下发 offer/ice 给 activeClient 的连接
}

// Room 是单语音频道的内存房间。一把锁保护全部可变态（调研 §3.3）。
type Room struct {
	channelID   string
	mu          sync.Mutex
	members     map[string]*member                       // user_id → member
	trackLocals map[string]*webrtc.TrackLocalStaticRTP   // track ID → 可转发本地 track
	api         *webrtc.API                              // 共享 API（来自 NewAPI）
	onEvent     RoomEventSink                            // 回调信令层（见 §4.6）
}
```

> `trackLocals` key 仍用 track ID（保留原始 Opus 参数，调研 §3.1）；但**创建转发 track 时 `StreamID` 注入上行用户 `user_id`**（见 §4.4 `addTrack`），使下行每条远端 audio track 的 `MediaStream.id`（msid/StreamID）等于音源用户的 `user_id`，接收端据 `e.streams[0].id` 即得 `user_id`，逐人音量/本地静音某人方能落地（voice-1）。**本项目偏离 research 01 sfu-ws 原样复用源 `StreamID` 的做法，原因即承载 user_id。** 说话/静音状态（`VoiceState`）随 WS `speaking_state`/`mute_state` 更新，与媒体层解耦（说话指示由前端 RMS 驱动，契约 §4.4）。

### 4.3 RoomManager

```go
// internal/sfu/rooms.go
type RoomManager struct {
	mu    sync.RWMutex
	api   *webrtc.API
	rooms map[string]*Room // channel_id → Room
	sink  RoomEventSink
}

func (m *RoomManager) Join(channelID, userID string, send func(protocol.Envelope)) (*protocol.VoiceState, error)
func (m *RoomManager) Leave(channelID, userID string) // 仅在该 user 在该房最后一条连接离开时整体移除并广播 user_left（多端，loop-2）
func (m *RoomManager) HandleAnswer(channelID, userID string, sdp webrtc.SessionDescription) error
func (m *RoomManager) HandleICE(channelID, userID string, cand webrtc.ICECandidateInit) error
func (m *RoomManager) Snapshot() []protocol.VoiceState // 给 bootstrap.voice_states
func (m *RoomManager) CloseRoom(channelID string)      // 删频道用
func (m *RoomManager) LeaveAll(userID string)          // 断线/踢人/进程关闭用；user 粒度，清掉该 user 全部连接（与按连接的隐式 leave 区别见 §3.3）
```

Room 在首个成员 `Join` 时惰性创建，最后一人离开时销毁（清理 mux 上连接）。

### 4.4 新增/移除成员的重协商（服务端 offerer）

服务端是 offerer（契约 §4.6 / 调研 §3.3-§3.4）。核心是 `signalPeerConnections()`：任何 track/peer 变更后调用，同一把锁保护，最多重试 25 次，仍失败则解锁 3s 后重来（避免死锁）。

```go
// internal/sfu/peer.go（重协商核心，基于调研 §3.3/§6 骨架，适配 Room）
func (r *Room) signalPeerConnections() {
	r.mu.Lock()
	defer r.mu.Unlock()

	attemptSync := func() (tryAgain bool) {
		// 1) 清理已关闭 peer
		for id, m := range r.members {
			if m.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
				delete(r.members, id)
				r.onEvent.UserLeft(r.channelID, id) // 通知信令广播 user_left
			}
		}
		for _, m := range r.members {
			existing := map[string]bool{}
			// 2) 移除已不在房间的 track
			for _, sender := range m.pc.GetSenders() {
				if sender.Track() == nil {
					continue
				}
				tid := sender.Track().ID()
				if _, ok := r.trackLocals[tid]; !ok {
					if err := m.pc.RemoveTrack(sender); err != nil {
						return true
					}
				} else {
					existing[tid] = true
				}
			}
			// 不回发自己上行的 track
			for _, recv := range m.pc.GetReceivers() {
				if recv.Track() != nil {
					existing[recv.Track().ID()] = true
				}
			}
			// 3) 补发缺失 track
			for tid, local := range r.trackLocals {
				if existing[tid] {
					continue
				}
				if _, err := m.pc.AddTrack(local); err != nil {
					return true
				}
			}
			// 4) 重协商：生成 offer 下发（契约 webrtc_offer）
			offer, err := m.pc.CreateOffer(nil)
			if err != nil {
				return true
			}
			if err = m.pc.SetLocalDescription(offer); err != nil {
				return true
			}
			m.send(protocol.Envelope{
				Type: "webrtc_offer",
				Data: protocol.WebRTCSDP{ChannelID: r.channelID, SDP: offer},
				ID:   nextRenegoID(), // 形如 "s-renego-12"，answer 须回带
			})
		}
		return false
	}

	for attempt := 0; ; attempt++ {
		if attempt == 25 {
			go func() { time.Sleep(3 * time.Second); r.signalPeerConnections() }()
			return
		}
		if !attemptSync() {
			return
		}
	}
}
```

新成员加入时建 PC、挂回调：

```go
// internal/sfu/peer.go（建 PC 要点，基于调研 §6）
func (r *Room) addPeer(userID string, send func(protocol.Envelope)) (*webrtc.PeerConnection, error) {
	pc, err := r.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	// 仅音频，sendrecv
	if _, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv}); err != nil {
		return nil, err
	}
	// Trickle ICE：本地候选 → 下发 ice_candidate（契约 §4.6）
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		payload := protocol.ICEPayload{ChannelID: r.channelID}
		if cand != nil {
			init := cand.ToJSON()
			payload.Candidate = &init // {candidate,sdpMid,sdpMLineIndex,usernameFragment}
		} // cand==nil → end-of-candidates，Candidate 留 nil
		send(protocol.Envelope{Type: "ice_candidate", Data: payload})
	})
	// 生命周期清理（调研 §5.1）
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		switch s {
		case webrtc.PeerConnectionStateFailed:
			_ = pc.Close()
		case webrtc.PeerConnectionStateClosed:
			r.signalPeerConnections() // 让其余 peer 摘掉走掉者的 track
		}
	})
	// 收上行 → 建本地转发 track → 转发 RTP（调研 §3.2）
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		// addTrack 内 NewTrackLocalStaticRTP 第三参 StreamID 传 userID（承载 user_id，voice-1）：
		//   local, _ = webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, remote.ID(), userID)
		local := r.addTrack(remote, userID) // 存入 trackLocals + signalPeerConnections()
		defer r.removeTrack(local)
		buf := make([]byte, 1500)
		pkt := &rtp.Packet{}
		for {
			n, _, err := remote.Read(buf)
			if err != nil {
				return
			}
			if err = pkt.Unmarshal(buf[:n]); err != nil {
				return
			}
			pkt.Extension = false // sfu-ws 做法：清扩展位
			pkt.Extensions = nil
			if err = local.WriteRTP(pkt); err != nil {
				return
			}
		}
	})
	return pc, nil
}
```

> **Join 时残留 member 清理（voice-3 / loop-2）**：`RoomManager.Join` 在调 `addPeer` 前，若 `members[userID]` 已存在（重连或多端换端遗留），先 `r.removeMember(userID)`（Close 旧 PC、删其 trackLocals、`signalPeerConnections`）再 `addPeer`，并把 `member.activeClient/pc/send` 指向新连接——不依赖 `OnConnectionStateChange` 的延迟回收，避免向已死连接发 offer 的双 PC 竞态。

入站信令对接（契约 §4.6 客户端 → 服务端）：

- `webrtc_answer` → `RoomManager.HandleAnswer` → 找到该 user 的 PC → `pc.SetRemoteDescription(answer)`。
- `ice_candidate` → `RoomManager.HandleICE` → `pc.AddICECandidate(init)`；`candidate==nil`（end-of-candidates）容忍跳过。

> SetRemoteDescription(answer) 与 signalPeerConnections 的并发：answer 处理只对单个 PC 操作，加 Room 锁即可；不与正在生成的 offer 冲突（offer 已 SetLocalDescription 完成后才下发）。仅音频无需周期 PLI/FIR（调研 §5.2）。

### 4.5 清理

| 场景 | 动作 |
|------|------|
| 成员 leave / 断线 | `Room.removeMember`：`pc.Close()`（幂等）→ 删其 trackLocals → `signalPeerConnections()`（其余 peer 摘 track）→ 经回调广播 `user_left`。**多端（loop-2）：仅当该 user 在该房无任何其它连接时才 removeMember + 广播 user_left；否则仅清理本连接、保留 member**（人未离开，只是换端） |
| 房间空 | 销毁 Room，从 RoomManager 移除；按需 `MultiUDPMuxDefault.RemoveConnByUfrag` 摘底层连接 |
| 删频道（owner） | `CloseRoom`：逐个 removeMember + 广播 user_left，再广播 `channel_deleted`（契约 §3.4 端点 8） |
| 踢人（owner） | `DisconnectUser(userID,"KICKED")`：对其每条连接先 `sendNow(auth_error{KICKED})` 再关闭 + `LeaveAll(userID)` 移出全部房间 + 广播 user_left（给他人，kick-1） |
| 进程关闭 | 关闭所有 Room 的全部 PC（优雅关闭，[§6.4](#64-优雅关闭)） |

### 4.6 SFU 与信令的衔接（回调接口）

`sfu` 不 import `signaling`（避免循环依赖）。信令层注入回调 `RoomEventSink`，由 SFU 在房间事件时调用，信令据此广播：

```go
// internal/sfu/rooms.go —— 由 signaling 实现并注入
type RoomEventSink interface {
	UserJoined(channelID string, vs protocol.VoiceState) // → 广播 user_joined（User 快照由信令层经 store.GetUserByID + auth.ToDTO 补，loop-4/voice-5）
	UserLeft(channelID, userID string)                   // → 广播 user_left
}
```

下发 offer/ice 给具体连接：通过 `addPeer/Join` 时传入的 `send func(protocol.Envelope)` 闭包（指向该用户连接的 `Client.send`），SFU 直接调用即可定向下发，无需反查 Hub。

衔接时序（契约 §4.6，新成员加入）：

```
Client.handleJoin
  └─ RoomManager.Join(channelID, userID, client.enqueue)
       ├─ addPeer(): NewPeerConnection + transceiver + OnTrack/OnICECandidate/OnConnStateChange
       ├─ members[userID] = member{pc, send: client.enqueue, state}
       ├─ sink.UserJoined → signaling 广播 user_joined 给频道其他人
       └─ signalPeerConnections() → 对每个 peer AddTrack/RemoveTrack + CreateOffer + send(webrtc_offer)
Client.handleAnswer → RoomManager.HandleAnswer → pc.SetRemoteDescription(answer)
Client.handleICE    → RoomManager.HandleICE    → pc.AddICECandidate
OnICECandidate(本地) → send(ice_candidate) 下发  （Trickle 双向）
```

---

## 5. 频道/消息模块与 store（rest + store）

### 5.1 store 封装（PostgreSQL）

纯 Go PostgreSQL 驱动 `github.com/jackc/pgx/v5`（`CGO_ENABLED=0`，契合 alpine 静态构建）。本设计经 `database/sql` 标准接口使用 pgx 的 stdlib 驱动（驱动名 `"pgx"`，`import _ "github.com/jackc/pgx/v5/stdlib"`）；如需 LISTEN/NOTIFY、COPY、批量等原生特性，可改用 `pgxpool.Pool`（接口随之改为 `*pgxpool.Pool`）。

连接与池配置（契约 §5.1）：

```go
// internal/store/db.go
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql 驱动名 "pgx"
)

// Open 连接 PostgreSQL 并配置连接池（契约 §5.1）。dsn = LUMEN_DATABASE_URL。
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn) // 如 postgres://user:pass@host:5432/lumen?sslmode=disable
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// PostgreSQL 原生 MVCC 并发——无需像 SQLite 那样串行化；小规模给一个适度连接池。
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
	db.SetConnMaxIdleTime(10 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("连接数据库失败（检查 LUMEN_DATABASE_URL/网络/sslmode）: %w", err)
	}
	return db, nil
}
```

> **连接串（DSN）**：内部网络（同一 Coolify 项目内的 Postgres 服务）通常 `sslmode=disable`；跨网络/托管 PG 用 `sslmode=require` 或更严。密码等敏感值经 Coolify 环境变量注入，**不入代码/日志**。
>
> **时间列**：契约 §5.2 时间列为 `TIMESTAMPTZ`。store 层结构体的 `created_at`/`updated_at`/`kicked_until` 用 `time.Time`（`kicked_until` 用 `sql.Null[time.Time]` 或 `*time.Time`）；DTO 组装时 `.UTC().Format(time.RFC3339)` 转线格式（契约 §7.4）。pgx 直接在 `time.Time` ↔ `TIMESTAMPTZ` 间扫描/绑定。`SetKickedUntil` 据此收 `time.Time` 入参（见 [§5.2](#52-store-接口与实现)），不经中间 string 解析（data-2）。

迁移：启动时执行契约 §5.2 的全部建表/索引 DDL（`CREATE TABLE IF NOT EXISTS ...`、`CREATE INDEX IF NOT EXISTS ...`），幂等。三张业务表 DDL **原样照搬契约 §5.2**，不增删列。规模更大或需版本化迁移时，可引入 `golang-migrate`（PostgreSQL 驱动），本期启动期幂等 DDL 已够。

> **broker 两表（架构变更新增，`internal/store/migrate.go`）**：同一 `schemaDDL` 追加两张登录 broker 表 `broker_states` 与 `desktop_sessions`（[§10.3](#103-store-broker-两表--janitor)），随迁移一并幂等创建。`store.New(db)` 建的 Store 其桌面会话方法会返回 `ErrNoSealer`；生产用 `store.NewWithSealer(db, refreshSealer)` 注入 refresh sealer 后，`desktop_sessions.refresh_token_enc` 才能加解密（[§6.2](#62-启动装配main)）。

**首次部署种子频道 `seedDefaultChannels`（loop-1，[v0]）**：迁移 DDL 执行后，在同一启动流程内调用幂等的 `seedDefaultChannels(ctx, store)`——**当 `channels` 表为空时**插入一组默认频道：text『大厅』+ voice『开黑1』。用**确定性 ULID**（固定常量）+ `ON CONFLICT (id) DO NOTHING` 保证重复启动不重复插入。这样纯 v0 部署后 `bootstrap.channels` 即非空，`send_message`/`join_channel` 的 `GetChannel` 校验可通过，v0 开黑回路无需 [v1] 的 owner 频道 CRUD 即可走通。

```go
// internal/store/channels.go（要点）
// 固定 ULID 常量（确定性，保证幂等）。
const (
	seedTextChannelID  = "01HZY0000000000000000TEXT0" // text 大厅（有效 Crockford base32，26 位；与实现一致）
	seedVoiceChannelID = "01HZY0000000000000000V01C1" // voice 开黑1（勿用 I/L/O/U——非法 base32）
)

// SeedDefaultChannels 仅当 channels 表为空时插入默认频道；ON CONFLICT 幂等。
func (s *pgStore) SeedDefaultChannels(ctx context.Context) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO channels (id, name, type, position, created_at, updated_at)
		 SELECT * FROM (VALUES
		   ($1, '大厅', 'text', 0, $3::timestamptz, $3::timestamptz),
		   ($2, '开黑1', 'voice', 1, $3::timestamptz, $3::timestamptz)
		 ) AS v
		 WHERE NOT EXISTS (SELECT 1 FROM channels)
		 ON CONFLICT (id) DO NOTHING`,
		seedTextChannelID, seedVoiceChannelID, now)
	return err
}
```

### 5.2 store 接口与实现

```go
// internal/store/db.go
type Store interface {
	// users（契约 §5.2）
	UpsertUser(subject, displayName, avatarURL string) (user User, changed bool, err error)
	GetUserByID(id string) (User, error)
	ListUsers() ([]User, error)              // 按 display_name 升序
	SetKickedUntil(userID string, until time.Time) error // 写 kicked_until=until（与 §5.1 TIMESTAMPTZ↔time.Time 一致，data-2）；handler 负责把 cooldown_seconds 换算为 until
	GetUserBySubject(subject string) (User, error)

	// channels（契约 §5.2）
	ListChannels(typeFilter string) ([]Channel, error) // 按 position 升序；typeFilter 空=全部
	GetChannel(id string) (Channel, error)
	CreateChannel(name, ctype string, position *int) (Channel, error) // position=nil → 同事务追加末尾
	UpdateChannel(id string, name *string, position *int) (Channel, error)
	DeleteChannel(id string) error            // ON DELETE CASCADE 连带删消息

	// messages（契约 §5.2 / §3.4 分页）
	InsertMessage(channelID, authorID, content string) (Message, error)
	ListMessages(channelID string, before string, limit int) (msgs []Message, hasMore bool, err error)
}
```

`User`/`Channel`/`Message` 为 store 层结构体（含 DB 列；不含 `is_owner`/`author` 这类计算/内联字段，那些在 DTO 组装时注入）。

> **`CreateChannel` 的 `position` 语义（评审 HIGH）**：参数为 `*int`——非 nil 用指定值；**nil（请求省略 `position`）时在同一事务内取 `SELECT COALESCE(MAX(position), -1) + 1 FROM channels` 追加到末尾**，避免多个省略 `position` 的频道都落在 DDL 默认值 `0` 上并列。契约 §3.4 端点 6「省略则追加到末尾」即指此规则。

### 5.3 消息分页查询（契约 §3.4 游标语义）

契约规定：内部按 `id` 降序取 `limit+1` 条判断 `has_more`，再反转为升序返回。

```go
// internal/store/messages.go
func (s *pgStore) ListMessages(channelID, before string, limit int) ([]Message, bool, error) {
	if limit < 1 { limit = 50 }
	if limit > 100 { limit = 100 } // 契约：1~100 钳制

	var rows *sql.Rows
	var err error
	if before == "" {
		// 首次：取最新 limit+1 条（id DESC，命中 idx_messages_channel_id）。PostgreSQL 用 $N 占位。
		rows, err = s.db.Query(
			`SELECT id, channel_id, author_id, content, created_at
			   FROM messages WHERE channel_id = $1
			   ORDER BY id DESC LIMIT $2`, channelID, limit+1)
	} else {
		// 翻页：早于 before 的（不含），id < before（ULID 字符串字典序 = 时间序）
		rows, err = s.db.Query(
			`SELECT id, channel_id, author_id, content, created_at
			   FROM messages WHERE channel_id = $1 AND id < $2
			   ORDER BY id DESC LIMIT $3`, channelID, before, limit+1)
	}
	if err != nil { return nil, false, err }
	defer rows.Close()

	out := make([]Message, 0, limit+1)
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.Content, &m.CreatedAt); err != nil {
			return nil, false, err
		}
		out = append(out, m)
	}
	hasMore := len(out) > limit
	if hasMore { out = out[:limit] }       // 丢弃多取的第 limit+1 条
	reverse(out)                            // DESC → ASC（旧→新），便于前端 append 到顶部
	return out, hasMore, nil
}
```

REST handler 组装 `meta`（契约 §3.4）：`next_before` = 本页最早一条的 `id`（即 `messages[0].id`），无更多时 `null`。

> **store 实现约定（PostgreSQL）**：`Store` 的具体类型为 `pgStore{db *sql.DB}`。所有查询用 **`$N` 占位**（非 SQLite 的 `?`）；`InsertMessage`/`CreateChannel` 用 `INSERT ... RETURNING *` 回取整行；`CreateChannel(position=nil)` 在同一事务先 `SELECT COALESCE(MAX(position),-1)+1`。
>
> **`UpsertUser` 的 `changed` 语义（sync-3）**：`changed = ON CONFLICT DO UPDATE 命中既有行 AND 旧值≠新值`；**首次 INSERT（新用户首登）一律 `changed=false`**。实现用 `INSERT ... ON CONFLICT (oauth_subject) DO UPDATE SET display_name=EXCLUDED.display_name, avatar_url=EXCLUDED.avatar_url, updated_at=EXCLUDED.updated_at WHERE users.display_name IS DISTINCT FROM EXCLUDED.display_name OR users.avatar_url IS DISTINCT FROM EXCLUDED.avatar_url RETURNING ...`，并以「受影响行=1 且 `xmax<>0`（即命中既有行）」判定 `changed=true`；新插入（`xmax=0`）或 `WHERE` 不满足（值未变、不更新行）时 `changed=false`，不广播 `user_updated`（§3.3 步骤 6）。

### 5.3.1 ULID 生成

```go
// internal/store/ids.go
package store

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// 服务端用单调熵保证同序列严格单调递增（data-1）：
// oklog/ulid/v2 的 ulid.Monotonic 源非并发安全，必须加锁；同毫秒递增溢出时
// ulid.New 返回错误，回退到下一毫秒重试，保证 messages.id 同序列内字典序=生成序。
var (
	entMu   sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// NewID 生成单调递增 ULID（契约 §5.5）。messages.id 兼作分页游标（§5.3 id DESC / id<before）。
func NewID() string {
	entMu.Lock()
	defer entMu.Unlock()
	ts := ulid.Timestamp(time.Now().UTC())
	for {
		id, err := ulid.New(ts, entropy)
		if err == nil { // 溢出（同毫秒熵耗尽）时回退下一毫秒重试
			return id.String()
		}
		ts++
	}
}
```

### 5.4 REST handler 与契约对应

路由表（契约 §3.3）：

```go
// internal/rest/router.go（要点；已落地，改用 Deps 结构 + 挂 broker + 单点 CORS）
type Deps struct {
	Verifier *auth.Verifier
	Owners   *auth.OwnerSet
	Enricher *auth.ProfileEnricher    // 可选 userinfo 补齐
	Store    store.Store
	Rooms    *sfu.RoomManager         // bootstrap.voice_states 用
	Hub      signaling.Broadcaster
	Config   config.Config
	Logger   *slog.Logger
	Broker   *broker.Handler          // 挂账户中心 + 桌面 broker 9 路由（PUBLIC）；nil 则跳过
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux() // Go 1.22+ 支持方法+路径模式

	// public
	mux.Handle("GET /api/v1/healthz", http.HandlerFunc(health))

	// 自动更新文件静态托管（loop-6/desktop-5，[v1]）：公开免鉴权，仅 GET。
	// 对外即 https://chat.example.com/updates/（与部署 FQDN 同域，复用 Traefik 证书）。
	mux.Handle("GET /updates/", http.StripPrefix("/updates/", http.FileServer(http.Dir(d.Config.UpdatesDir))))

	// 成员（RequireAuth）
	authed := func(h http.HandlerFunc) http.Handler { return auth.RequireAuth(d.Verifier, writeErr, h) }
	mux.Handle("GET /api/v1/bootstrap", authed(bootstrap(d.Store, d.Owners, d.Enricher, d.Rooms, d.Config)))
	mux.Handle("GET /api/v1/me", authed(me(d.Store, d.Owners, d.Enricher))) // Bearer 版 me
	mux.Handle("GET /api/v1/channels", authed(listChannels(d.Store)))
	mux.Handle("GET /api/v1/channels/{channelId}/messages", authed(listMessages(d.Store, d.Owners)))
	mux.Handle("GET /api/v1/members", authed(listMembers(d.Store, d.Owners)))

	// owner（RequireAuth → RequireOwner）
	owner := func(h http.HandlerFunc) http.Handler {
		return auth.RequireAuth(d.Verifier, writeErr, auth.RequireOwner(d.Owners, writeErr, h))
	}
	mux.Handle("POST /api/v1/channels", owner(createChannel(d.Store, d.Hub)))            // [v1]
	mux.Handle("PATCH /api/v1/channels/{channelId}", owner(updateChannel(d.Store, d.Hub))) // [v1]
	mux.Handle("DELETE /api/v1/channels/{channelId}", owner(deleteChannel(d.Store, d.Hub))) // [v1]
	mux.Handle("POST /api/v1/members/{userId}/kick", owner(kickMember(d.Store, d.Owners, d.Hub))) // [v1]

	// 登录 broker（decision 10）：挂在同一 root mux 上作 PUBLIC 路由（无 RequireAuth；
	// broker 自管 handoff/cookie 鉴权）。broker 的 cookie 版 GET /api/me 与上面 Bearer 版
	// GET /api/v1/me 路径不同、互不冲突。
	if d.Broker != nil {
		d.Broker.Register(mux)
	}

	// 单点 CORS（decision 3）：精确匹配 d.Config.WebBaseURL，包住整个 mux（含 broker）；
	// 置于 recover/logging 之外，使 preflight OPTIONS 与 Vary: Origin 恒设。
	return withCORS(d.Config.WebBaseURL, withRecover(d.Logger, withLogging(d.Logger, mux)))
}
```

端点 ↔ store ↔ 副作用对应：

| 端点 | store 调用 | WS 副作用 |
|------|-----------|-----------|
| `GET /bootstrap` | `UpsertUser`(claims，幂等)+`ListChannels`+`ListUsers`+`RoomManager.Snapshot` | — |
| `GET /me` | `UpsertUser`(claims，幂等；注入 is_owner) | — |
| `GET /channels` | `ListChannels(type)` | — |
| `GET /channels/{id}/messages` | `GetChannel`（校验 text）+`ListMessages` | — |
| `GET /members` | `ListUsers` | — |
| `POST /channels` | `CreateChannel` | `BroadcastAll(channel_created)`（广播**完整 Channel 对象含 `position`**，供客户端按 `(position, id)` 升序重排，与 §5.2 索引一致，chan-1） |
| `PATCH /channels/{id}` | `UpdateChannel` | `BroadcastAll(channel_updated)`（广播**完整 Channel 对象含 `position`**；客户端以 `id` 定位替换后按 `(position, id)` 重排，chan-1） |
| `DELETE /channels/{id}` | `GetChannel`+`DeleteChannel` | voice: `CloseVoiceChannel`(广播 user_left)；都广播 `BroadcastAll(channel_deleted)` |
| `POST /members/{id}/kick` | 先校验 target≠self（否则 400 VALIDATION_ERROR）→ `GetUserByID` → (cooldown>0 时) `SetKickedUntil`（先写）→ `DisconnectUser(userID,"KICKED")` | `DisconnectUser`（向被踢者每条连接发 `auth_error{KICKED}` 后断连+移出房间）+ 广播 `user_left`（给他人） |

`bootstrap` 的 `ws_url`：由配置推导。容器内不知道外部域名，故新增可选 env `LUMEN_PUBLIC_WS_URL`（如 `wss://chat.example.com/ws`）；若未配置，回退用请求的 `Host` 头拼 `wss://<host>/ws`（Traefik 终结 TLS，对外是 wss）。`server_time` = `time.Now().UTC().Format(time.RFC3339)`。

> **首登竞态修复（评审 CRITICAL）**：`bootstrap`/`me` 在验签后用 `auth.ProfileFromClaims(claims)`（必要时 userinfo 补齐）调 `store.UpsertUser`（幂等），而非只读 `GetUserBySubject`。这样新用户**首次登录**时，即使 REST `bootstrap` 早于 WS `auth` 到达，也不会因「用户行不存在」返回 `NOT_FOUND`/`500`。与 WS 不同，REST 的 upsert **不广播** `user_updated`（资料变化广播仅由 WS `auth`/`reauth` 触发，契约 §2.3/§2.7）。
>
> **`kick` 端点（`kickMember` 流程顺序，kick-2/kick-3/loop-5/data-2）**：
> 1. 解析路径参数 `{userId}`；由请求 claims 经 `GetUserBySubject` 得到调用者自身 `user.id`。**若 `target == 调用者自身`** → `writeErr(w, 400, "VALIDATION_ERROR", "不能踢出自己")` 提前返回（不进入后续步骤），与契约 §3.4 端点 9 对齐。
> 2. `cooldown_seconds`（请求体，省略默认 3600）：**`>0` 时先 `SetKickedUntil`**——`until := time.Now().UTC().Add(time.Duration(cooldown_seconds)*time.Second)`，`st.SetKickedUntil(userID, until)`（**先写后断**，消除『断开后旧端在 `kicked_until` 落库前抢先重连绕过冷却』的竞态窗口）。
> 3. `DisconnectUser(userID, "KICKED")`：对该用户每条活动连接先 `sendNow(auth_error{code:"KICKED", message, kicked_until, retry_after})` 再关闭，并移出全部内存房间。
> 4. 广播 `user_left`（给他人）。
>
> `cooldown_seconds==0`：**不写** `kicked_until`（不调 `SetKickedUntil`），仅 `DisconnectUser(userID,"KICKED")`（断连+移出房间）。其语义为『瞬时断开不封禁、允许立即重入』，客户端收到 `auth_error{KICKED}` 后停止自动重连、由用户手动重连——与 `cooldown>0` 在客户端行为上一致（区别仅在服务端是否在冷却期拒绝下次握手）。

### 5.4 错误码映射

统一信封（契约 §3.2）与错误码表（契约 §7.2）：

```go
// internal/rest/envelope.go
type Envelope struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data"`
	Error   *APIError   `json:"error"`
}
type APIError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details"`
}

// writeOK / writeErr 写统一信封。
func writeOK(w http.ResponseWriter, status int, data interface{}) { /* success:true */ }
func writeErr(w http.ResponseWriter, status int, code, message string) { /* success:false */ }
```

| 业务错误 | HTTP | code |
|----------|------|------|
| 缺/无效 Bearer | 401 | `UNAUTHENTICATED` |
| 验签失败 | 401 | `TOKEN_INVALID` |
| token 过期 | 401 | `TOKEN_EXPIRED` |
| 非 owner | 403 | `FORBIDDEN` |
| 资源不存在 | 404 | `NOT_FOUND` |
| 参数/请求体非法 | 400 | `VALIDATION_ERROR`（字段级放 `details`） |
| 内部错误 | 500 | `INTERNAL`（不泄漏堆栈/SQL/token） |

WS 错误复用同表（`error.data.code` / `auth_error.data.code`），见 [§3](#3-信令模块signaling--ws)。

---

## 6. 配置、启动装配、并发模型、优雅关闭、可观测性、安全

### 6.1 配置（全部环境变量）

实现契约 §1.3。启动时**校验必填项**，缺失 fail-fast 退出。

| 环境变量 | 含义 | 示例 | 必填 | 归属 |
|----------|------|------|:----:|------|
| `LUMEN_OAUTH_ISSUER` | OIDC issuer（校验 `iss`） | `https://auth.example.com/realms/lumen` | ✓ | v0 |
| `LUMEN_OAUTH_JWKS_URL` | JWKS 端点（本地验签公钥源） | `https://auth.example.com/realms/lumen/protocol/openid-connect/certs` | ✓ | v0 |
| `LUMEN_OAUTH_USERINFO_URL` | userinfo 端点（资料补齐）；**可选**，缺省由 OIDC discovery 自 issuer 推导 | `https://auth.example.com/realms/lumen/protocol/openid-connect/userinfo` | ✗ | v0 |
| `LUMEN_OAUTH_AUDIENCE` | 期望 `aud`（资源侧验 access_token；broker 侧另持 `LUMEN_OAUTH_CLIENT_ID`/`_SECRET`，见下方 broker 段） | `lumen-api` | ✓ | v0 |
| `LUMEN_OWNER_SUBJECTS` | owner sub 列表（逗号分隔） | `sub-abc,sub-def` | ✓ | v0 |
| `LUMEN_LISTEN_ADDR` | HTTP/WS 监听地址（容器内必须 `0.0.0.0`） | `0.0.0.0:8080` | ✓ | v0 |
| `LUMEN_DATABASE_URL` | PostgreSQL 连接串（DSN） | `postgres://lumen:***@lumen-db:5432/lumen?sslmode=disable` | ✓ | v0 |
| `LUMEN_PUBLIC_IP` | VPS 公网 IP（`SetNAT1To1IPs`） | `203.0.113.10` | ✓ | v0 |
| `LUMEN_WEBRTC_UDP_PORT` | WebRTC 媒体单 UDP 端口 | `40000` | ✓ | v0 |
| `LUMEN_PUBLIC_WS_URL` | 对外 WS 地址（bootstrap.ws_url）；缺省由 Host 头推导 | `wss://chat.example.com/ws` | ✗ | v0 |
| `LUMEN_UPDATES_DIR` | 自动更新文件静态托管目录（`GET /updates/`） | `/app/updates` | ✗（默认 `/app/updates`） | v1 |
| `LUMEN_LOG_LEVEL` | 日志级别 `debug/info/warn/error` | `info` | ✗（默认 info） | v0 |
| **登录 broker（[§10](#10-登录-broker账户中心--桌面登录中介)，随 broker 移入服务端而新增）** | | | | |
| `LUMEN_OAUTH_CLIENT_ID` | confidential OIDC client id（broker 用；资源验签仍只用 aud） | `lumen-web` | ✓ | v0 |
| `LUMEN_OAUTH_CLIENT_SECRET` | confidential client_secret（**secret，仅服务端持有，永不下发/记日志**） | `***` | ✓ | v0 |
| `LUMEN_OAUTH_AUTHORIZE_URL` | IdP authorize 端点；**可选**，缺省由 discovery 推导 | `https://auth.example.com/.../auth` | ✗ | v0 |
| `LUMEN_OAUTH_TOKEN_URL` | IdP token 端点；**可选**，缺省由 discovery 推导 | `https://auth.example.com/.../token` | ✗ | v0 |
| `LUMEN_OAUTH_DESKTOP_REDIRECT_URI` | 桌面登录 IdP 回调（登记在 IdP） | `https://chat.example.com/desktop/callback` | ✓ | v0 |
| `LUMEN_OAUTH_WEB_REDIRECT_URI` | 账户中心 IdP 回调（登记在 IdP） | `https://chat.example.com/auth/callback` | ✓ | v0 |
| `LUMEN_WEB_BASE_URL` | SPA 官网源（CORS 精确白名单 + `/account` 重定向目标） | `https://example.com` | ✓ | v0 |
| `LUMEN_SESSION_ENC_KEY` | 账户中心 cookie 封装密钥（base64 解码须 32 字节） | `<base64 32B>` | ✓ | v0 |
| `LUMEN_REFRESH_ENC_KEY` | `refresh_token` at-rest 加密密钥（base64 32 字节；**须与 SESSION_ENC_KEY 不同**） | `<base64 32B>` | ✓ | v0 |

> `LUMEN_OAUTH_USERINFO_URL` 现由 broker 与资料补齐**共用**：缺省时 broker 的 OIDC 客户端经 discovery 推导，两把 AES 密钥必须 base64 解码为恰好 32 字节且**彼此不同**（decision 2），`Load` 与其它必填项一样聚合报错、fail-fast。

```go
// internal/config/config.go（已落地；broker 字段与两把密钥）
type Config struct {
	OAuthIssuer, OAuthJWKSURL, OAuthUserinfoURL string
	OAuthAudience                               string
	OwnerSubjects                               []string
	ListenAddr, DatabaseURL, PublicIP           string
	WebRTCUDPPort                               int
	PublicWSURL, UpdatesDir, LogLevel           string

	// --- 登录 broker（EdgeOne Functions 的 Go 移植）---
	OAuthClientID        string
	OAuthClientSecret    string // secret：永不记日志/回显
	OAuthAuthorizeURL    string // 可选；空则 discovery 推导
	OAuthTokenURL        string // 可选；空则 discovery 推导
	OAuthDesktopRedirect string // = https://chat.example.com/desktop/callback
	OAuthWebRedirect     string // = https://chat.example.com/auth/callback
	WebBaseURL           string // = https://example.com（CORS 白名单）

	// 解码后的 32 字节 AES-256-GCM 密钥（Load 校验；两把必须不同）。
	// sessionEncKey 封装账户中心两枚 cookie；refreshEncKey 加密 refresh_token at-rest。
	sessionEncKey []byte
	refreshEncKey []byte
}

// SessionEncKey/RefreshEncKey 返回密钥的防御性拷贝。
func (c Config) SessionEncKey() []byte { return cloneKey(c.sessionEncKey) }
func (c Config) RefreshEncKey() []byte { return cloneKey(c.refreshEncKey) }

// Load 从环境读取并校验必填项；缺失返回聚合错误（fail-fast）。
func Load() (Config, error) {
	var miss []string
	get := func(k string, required bool) string {
		v := strings.TrimSpace(os.Getenv(k))
		if required && v == "" { miss = append(miss, k) }
		return v
	}
	c := Config{
		OAuthIssuer:      get("LUMEN_OAUTH_ISSUER", true),
		OAuthJWKSURL:     get("LUMEN_OAUTH_JWKS_URL", true),
		OAuthUserinfoURL: get("LUMEN_OAUTH_USERINFO_URL", false), // 可选：缺省由 OIDC discovery 推导
		OAuthAudience:    get("LUMEN_OAUTH_AUDIENCE", true),
		OwnerSubjects:    splitCSV(get("LUMEN_OWNER_SUBJECTS", true)),
		ListenAddr:       get("LUMEN_LISTEN_ADDR", true),
		DatabaseURL:      get("LUMEN_DATABASE_URL", true),
		PublicIP:         get("LUMEN_PUBLIC_IP", true),
		PublicWSURL:      get("LUMEN_PUBLIC_WS_URL", false),
		UpdatesDir:       orDefault(get("LUMEN_UPDATES_DIR", false), "/app/updates"),
		LogLevel:         orDefault(get("LUMEN_LOG_LEVEL", false), "info"),

		// broker（confidential client + 回环/账户中心回调 + SPA 源）
		OAuthClientID:        get("LUMEN_OAUTH_CLIENT_ID", true),
		OAuthClientSecret:    get("LUMEN_OAUTH_CLIENT_SECRET", true),
		OAuthAuthorizeURL:    get("LUMEN_OAUTH_AUTHORIZE_URL", false),
		OAuthTokenURL:        get("LUMEN_OAUTH_TOKEN_URL", false),
		OAuthDesktopRedirect: get("LUMEN_OAUTH_DESKTOP_REDIRECT_URI", true),
		OAuthWebRedirect:     get("LUMEN_OAUTH_WEB_REDIRECT_URI", true),
		WebBaseURL:           get("LUMEN_WEB_BASE_URL", true),
	}
	port, err := strconv.Atoi(get("LUMEN_WEBRTC_UDP_PORT", true))
	if err != nil || port < 1 || port > 65535 {
		miss = append(miss, "LUMEN_WEBRTC_UDP_PORT(无效)")
	}
	c.WebRTCUDPPort = port

	// 两把 AES 密钥：必填、base64 解码须恰好 32 字节、且彼此不同（decision 2）。
	c.sessionEncKey = decodeAESKey(get("LUMEN_SESSION_ENC_KEY", true), "LUMEN_SESSION_ENC_KEY", &miss)
	c.refreshEncKey = decodeAESKey(get("LUMEN_REFRESH_ENC_KEY", true), "LUMEN_REFRESH_ENC_KEY", &miss)
	if c.sessionEncKey != nil && c.refreshEncKey != nil && bytesEqual(c.sessionEncKey, c.refreshEncKey) {
		miss = append(miss, "LUMEN_REFRESH_ENC_KEY(必须与 LUMEN_SESSION_ENC_KEY 不同)")
	}

	if len(miss) > 0 {
		return Config{}, fmt.Errorf("缺失/无效环境变量: %s", strings.Join(miss, ", "))
	}
	return c, nil
}
```

### 6.2 启动装配（main）

> **`cmd/lumen-server/main.go` 现已落地**（commit `82f344e`；此前该目录仅有 `.gitkeep`）。真实实现把装配放在返回 `error` 的 `run()` 里（便于 defer 清理与测试），`main` 仅调用它并按需 `os.Exit(1)`；下列骨架与真实代码一致，新增 **broker 装配**、**refresh sealer**、**broker janitor**（60s 清理过期 broker_states）与**优雅关闭**（`srv.Shutdown` + `rooms.CloseAllRooms`）。

```go
// cmd/lumen-server/main.go（装配顺序，已落地）
func main() {
	if err := run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	// 1) Config（fail-fast）：任何缺失/非法键在任何 I/O 前中止。
	cfg, err := config.Load()
	if err != nil { return err }
	logger := newLogger(cfg.LogLevel); slog.SetDefault(logger)

	// rootCtx 在 SIGINT/SIGTERM 时取消：约束后台 goroutine（JWKS 刷新、broker janitor）并触发优雅关闭。
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 2) store：连接池 + 迁移 + 种子（受 startupTimeout 约束）。
	startCtx, cancelStart := context.WithTimeout(rootCtx, startupTimeout); defer cancelStart()
	db, err := store.Open(startCtx, cfg.DatabaseURL); if err != nil { return err }
	defer db.Close()
	// refresh_token at-rest sealer 用独立的 LUMEN_REFRESH_ENC_KEY（decision 2）。
	refreshSealer, err := secure.NewSealer(cfg.RefreshEncKey()); if err != nil { return err }
	st := store.NewWithSealer(db, refreshSealer)          // 注入 sealer → desktop_sessions.refresh_token_enc 加密
	if err := st.Migrate(startCtx); err != nil { return err }        // 建表 DDL（含 broker 两表，幂等）
	if err := st.SeedDefaultChannels(startCtx); err != nil { return err } // [v0] 首部署种子频道

	// 3) auth：JWKS 验签器（后台刷新绑 rootCtx）、owner 集、可选 userinfo enricher。
	verifier, err := auth.NewVerifier(rootCtx, cfg.OAuthJWKSURL, cfg.OAuthIssuer, cfg.OAuthAudience); if err != nil { return err }
	owners := auth.NewOwnerSet(cfg.OwnerSubjects)
	enricher, err := auth.NewProfileEnricher(startCtx, cfg.OAuthIssuer, cfg.OAuthUserinfoURL)
	if err != nil { logger.Warn("userinfo enricher 初始化失败，降级为仅凭 claims", "err", err); enricher = nil }

	// 4) sfu + signaling hub（SetSink 交叉注入，打破 sfu↔signaling 循环）。
	api, err := sfu.NewAPI(cfg.WebRTCUDPPort, cfg.PublicIP); if err != nil { return err }
	rooms := sfu.NewRoomManager(api)
	hub := signaling.NewHub(st, verifier, owners, rooms, enricher, logger)
	rooms.SetSink(hub)

	// 5) 登录 broker（decision 10）：NewHandler 内做 discovery 回退 + 用 LUMEN_SESSION_ENC_KEY 建 cookie sealer。
	brk, err := broker.NewHandler(startCtx, cfg, st, logger); if err != nil { return err }

	// 6) Router：REST（+ broker.Register）置于单点 CORS 之内；再由 signaling.Mount 包住以升级 GET /ws。
	router := rest.NewRouter(rest.Deps{
		Verifier: verifier, Owners: owners, Enricher: enricher, Store: st,
		Rooms: rooms, Hub: hub, Config: cfg, Logger: logger, Broker: brk,
	})
	handler := signaling.Mount(router, hub)

	// 7) broker janitor：每 60s 回收过期 broker_states（decision 4）。
	go runJanitor(rootCtx, st, logger)

	// 8) HTTP server + 优雅关闭。
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: handler, ReadHeaderTimeout: readHeaderTimeout}
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr, "udp", cfg.WebRTCUDPPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) { serveErr <- err; return }
		serveErr <- nil
	}()
	select {
	case <-rootCtx.Done(): logger.Info("shutdown signal received, draining")
	case err := <-serveErr: return err
	}
	shutCtx, cancelShut := context.WithTimeout(context.Background(), shutdownTimeout); defer cancelShut()
	if err := srv.Shutdown(shutCtx); err != nil { rooms.CloseAllRooms(); return err }
	rooms.CloseAllRooms()
	return nil
}
```

> 常量：`startupTimeout=30s`（约束一次性启动 I/O：DB open/migrate/seed/discovery）、`janitorInterval=60s`、`shutdownTimeout=15s`、`readHeaderTimeout=10s`（防 slow-loris）。

### 6.3 并发模型

| 组件 | 并发单元 | 同步原语 |
|------|----------|----------|
| HTTP/REST | 每请求一 goroutine（net/http 标准） | store 走 pgx 连接池（max~10），PostgreSQL MVCC 原生并发；ownerSet/config 只读无锁 |
| WS 每连接 | 读泵 + 写泵两 goroutine | `Client.send` channel 串行写出；`atomic` 存 token/voiceChannel |
| Hub | 多 goroutine 读写注册表 | `sync.RWMutex`（仅护 map，不在锁内做 IO） |
| SFU Room | 信令 goroutine + Pion 内部 goroutine（OnTrack/OnICE/OnConnState） | 每 Room 一把 `sync.Mutex`，护 members/trackLocals；重协商 25 次重试 + 3s 退避避死锁 |
| RTP 转发 | 每上行 track 一 goroutine（OnTrack 读循环） | 复用 `[]byte`(1500)/`*rtp.Packet`，`WriteRTP` 到各 local track |
| JWKS 刷新 | keyfunc 后台 goroutine | 库内部管理；ctx 取消即停 |

**避免锁内 IO**：Hub 广播时先在锁内拷贝目标连接切片，解锁后再投递到各 `send` channel；Room 锁内只做 Pion 状态操作（AddTrack/CreateOffer），下发 offer 用闭包入队到连接的 send channel（非阻塞 IO）。

### 6.4 优雅关闭

真实 `run()` 内联优雅关闭（[§6.2](#62-启动装配main)）：`rootCtx`（`signal.NotifyContext(SIGINT/SIGTERM)`）被取消后，`shutdownTimeout=15s` 内 `srv.Shutdown` 停收新 HTTP/WS、等在途完成，再 `rooms.CloseAllRooms()` 关闭全部 PeerConnection（`pc.Close` 幂等）并释放 UDPMux；`db.Close()` 与 `stop()` 经 `defer` 回收。`rootCtx` 取消会连带停掉两个后台 goroutine：**keyfunc JWKS 刷新** 与 **broker janitor**（`runJanitor` 收到 `ctx.Done()` 即返回，decision 4）。

```go
// run() 内（要点）：收到信号后
shutCtx, cancelShut := context.WithTimeout(context.Background(), shutdownTimeout) // 15s
defer cancelShut()
if err := srv.Shutdown(shutCtx); err != nil { rooms.CloseAllRooms(); return err }
rooms.CloseAllRooms()  // 关闭所有 PeerConnection、释放 UDPMux
// defer: db.Close()（连接池）→ stop()（取消 rootCtx → JWKS 刷新 + broker janitor 退出）
```

> 用 Ports Mappings 部署会**失去 Rolling Updates**（调研 02 §8）：重新部署有短暂中断，语音会重连——可接受（小规模开黑场景）。

### 6.5 日志与可观测性

- **结构化日志**：标准库 `log/slog`，JSON handler，级别由 `LUMEN_LOG_LEVEL` 控制。
- **关键日志点**：启动配置摘要（**脱敏**：不打 token/JWKS 内容）、鉴权失败（记 code 与 sub 前缀，不记完整 token）、join/leave、重协商失败、PC 状态转换、panic 恢复。
- **健康检查**：`GET /api/v1/healthz`（契约端点 10）返回 `{"success":true,"data":{"status":"ok"},"error":null}`，供 Coolify 探活；不依赖 DB（纯存活探针）。如需就绪探针可另查 DB ping（可选）。
  - **取舍声明（接受，loop-7）**：`healthz` 为纯存活探针，**运行期 DB 抖动（PG 重启/网络）不反映于健康检查、不触发容器重启**；依赖 pgx 连接池在 PostgreSQL 恢复后自动重建连接实现数据层自愈（PG 抖动期间 store 操作暂时失败，PG 恢复后自动恢复，无需重启容器）。鉴于开黑规模（2~6 人/单 guild），**不引入就绪探针**；若后续需要可加 `GET /api/v1/readyz` 查 DB ping（DB 不可用返回 503）。
- **panic 恢复**：REST 用 `withRecover` 中间件；每个 WS goroutine、每个 OnTrack 读循环用 `defer recover()` 防单连接崩溃拖垮进程。
- **指标**（可选 `[v1]`）：在线连接数、各 Room 成员数、消息速率——可经 slog 周期输出或加 `/metrics`（Prometheus，非必须）。

### 6.6 安全注意

| 项 | 措施 |
|----|------|
| secret 集中于服务端 | **架构变更后**：`client_secret` 与 `refresh_token` **只**存在于本 Go 服务端（decision 2）——`client_secret` 走 `LUMEN_OAUTH_CLIENT_SECRET`（永不记日志/回显），`refresh_token` 仅存 `desktop_sessions.refresh_token_enc`（用独立的 `LUMEN_REFRESH_ENC_KEY` AES-256-GCM 加密于列，[§10.3](#103-store-broker-两表--janitor)）；桌面只持不透明 `desktop_session_id`。access_token 不落库（仅在 handoff 行的 ~120s 窗口内经 `broker_states` 中转）。EdgeOne 侧**零 secret**（纯静态 SPA）。 |
| CORS（精确白名单） | SPA 在 `example.com` **跨源**调本服务端 broker（`chat.example.com`）；二者**同 SITE**（同一注册域），故：会话 cookie 用 `SameSite=Lax + HttpOnly + Secure + Path=/ + HOST-ONLY（无 Domain）`；服务端**仅**对精确等于 `LUMEN_WEB_BASE_URL` 的 Origin 发 CORS（带 credentials），永不用 `*`，恒发 `Vary: Origin`（[§10.6](#106-单点-cors-中间件)）。SPA XHR 用 `credentials:'include'`；`/auth/login`、`/auth/callback` 为顶层导航（非 XHR）。桌面客户端非浏览器、不受同源策略约束。 |
| JWT 验签红线 | 强制 `RS256`（防 alg 混淆/none）、校验 iss/aud/exp、JWKS 必须 HTTPS（调研 §4.1） |
| token 不入日志/URL | WS token 仅首帧 body，不进 query（契约 §2.4）；日志脱敏 |
| 输入校验 | content ≤4000 去空判空、channel name 1~64、type∈{text,voice}、limit 钳制 1~100、SDP/candidate 反序列化失败即拒 |
| SQL 注入 | 全部参数化查询（pgx `$N` 占位），无字符串拼接 |
| 错误不泄漏 | 对外 message 中文、不含堆栈/SQL/token（契约 §7.2） |
| owner 双重校验 | REST 中间件 `RequireOwner` + handler 内不再信任客户端传入的 is_owner |
| 软封禁 | WS `auth` 与 `reauth` 均查 `kicked_until`，冷却期 `auth_error code=KICKED`（带 `kicked_until/retry_after`）关闭；踢人时对活动连接亦下发 `auth_error{KICKED}`（契约端点 9） |
| 慢客户端 | send channel 满即断连，防内存膨胀 |
| 限流（可选 [v1]） | send_message / auth 每连接令牌桶，触发回 `RATE_LIMITED` |
| 容器最小权限 | 非 root 用户运行（Dockerfile `USER app`） |

---

## 7. 部署（Coolify）

实现调研 02。核心：HTTP/WS 走 Traefik（终结 TLS、提供 WSS）；WebRTC UDP 裸端口映射绕过 Traefik。

> **官网静态化 + broker 内置**：官网为**独立的 EdgeOne Pages 纯静态部署**（`example.com`，只发布 `website/dist/`，无 Functions/无 KV/无 secret）。登录 broker 已**内置进本 Go 服务端**（`chat.example.com`，[§10](#10-登录-broker账户中心--桌面登录中介)）：与 `/api/v1/*` **共用同一 HTTP 服务与端口**（8080 → Traefik），**无新增对外端口**；新增的运行期依赖是既有 Postgres（broker 两表）与到 IdP 的 authorize/token/userinfo/discovery 出站调用（confidential client）。服务端现对精确 `LUMEN_WEB_BASE_URL` 源发 CORS（供 SPA 跨源账户中心，[§10.6](#106-单点-cors-中间件)）。SPA 部署细节见 [./web-design.md](./web-design.md)。

### 7.1 数据流总览

```
客户端 --- HTTPS REST + WSS 信令 (443/tcp, TLS) ---> Traefik(边缘终结 TLS)
                                                        │ 明文 http/ws 转发
                                                        ▼
                                          容器: ws/http 0.0.0.0:8080 ──┐
客户端 --- DTLS-SRTP 媒体 (40000/udp) -----------------------------------> 容器: 0.0.0.0:40000/udp
        （裸 UDP 端口映射，不经 Traefik）                                  │ 5432/tcp（Coolify 内部网络）
                                                                          ▼
                                                          PostgreSQL 服务（Coolify 资源，自带持久化）
```

### 7.2 Dockerfile 草案

```dockerfile
# ---- build ----
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0           # pgx + pion 纯 Go，无需 CGO
RUN go build -trimpath -ldflags="-s -w" -o /out/lumen-server ./cmd/lumen-server

# ---- runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/lumen-server /app/lumen-server

# 8080 = 信令/HTTP/WS（给 Traefik，Ports Exposes 第一个=健康检查口）
# 40000/udp = WebRTC 媒体单口（给 host 直发，Ports Mappings）
EXPOSE 8080
EXPOSE 40000/udp

USER app
ENTRYPOINT ["/app/lumen-server"]
```

> 端口四处一致（调研 02 §7 假设 E）：Dockerfile EXPOSE = Coolify Ports Exposes/Mappings = env `LUMEN_WEBRTC_UDP_PORT` = 防火墙放行。HTTP 端口与 `LUMEN_LISTEN_ADDR` 一致（8080）。

### 7.3 Coolify 配置项

| Coolify 字段 | 值 | 作用 |
|--------------|-----|------|
| **Ports Exposes** | `8080` | 容器监听端口，Traefik 据此转发 HTTP/WS；第一个端口=健康检查口 |
| **Ports Mappings** | `40000:40000/udp` | 裸 UDP 直发宿主机，绕过 Traefik（WebRTC 媒体） |
| **Domains (FQDN)** | `https://chat.example.com` | Traefik 自动签 Let's Encrypt + 强制 HTTPS；对外即 wss/https |
| **PostgreSQL（数据库资源）** | 新建一个 Coolify PostgreSQL 服务 | 提供 `LUMEN_DATABASE_URL`；数据库自身持久化与备份由该资源管理 |
| **Persistent Storage** | `/app/updates`（更新文件卷） | 自动更新文件持久化目录，经 `GET /updates/` 静态托管（`LUMEN_UPDATES_DIR`，[v1]） |
| **Health Check** | `GET /api/v1/healthz` | Coolify 探活 |

> **数据库（PostgreSQL）**：在同一 Coolify 项目内新建一个 PostgreSQL 资源（或用外部托管 PG）。同项目内服务经 Coolify 内部网络以**服务名/内部主机名**互联（如 `lumen-db:5432`，`sslmode=disable`）；外部 PG 用 `sslmode=require`。把生成的连接串填入应用的 `LUMEN_DATABASE_URL`。数据库的持久卷与备份由 Coolify 的 PostgreSQL 资源负责，**应用容器无需 Persistent Storage**。应用启动时幂等执行 §5.2 DDL。
>
> `/udp` 后缀（调研 02 §1.1 假设 A）：若 Coolify 版本 Ports Mappings 拒绝 `/udp`，回退用 **Docker Compose 部署类型**，在 compose `ports:` 写 `"40000:40000/udp"`（Docker 原生语义，无版本风险，骨架见调研 02 §6.2）。

### 7.4 环境变量注入（Coolify）

在 Coolify 应用 → Environment Variables 注入 [§6.1](#61-配置全部环境变量) 全部 `LUMEN_*`。改 env 后须触发重新部署（`GET /deploy?uuid=`，调研 02 §4）才生效。关键对齐：

- `LUMEN_LISTEN_ADDR=0.0.0.0:8080`（**必须 0.0.0.0**，否则 Traefik 到不了容器，调研 02 §3）。
- `LUMEN_WEBRTC_UDP_PORT=40000` 与 Ports Mappings 一致。
- `LUMEN_PUBLIC_IP=<VPS 公网 IP>`（`SetNAT1To1IPs`）。
- `LUMEN_DATABASE_URL=postgres://lumen:***@lumen-db:5432/lumen?sslmode=disable`（Coolify 内部网络；外部 PG 用 `sslmode=require`）。
- `LUMEN_PUBLIC_WS_URL=wss://chat.example.com/ws`（bootstrap.ws_url）。

### 7.5 防火墙放行

- **云厂商安全组**：放行入站 `443/tcp`（WSS/HTTPS）+ `40000/udp`（WebRTC 媒体）。**优先用云安全组**——Docker iptables 会绕过 UFW（调研 02 §2）。
- 若主机用 UFW：单独放行不够，需配 `ufw-docker`。
- 安全提示：Ports Mappings 默认绑 `0.0.0.0`（公网开放），对 WebRTC 媒体口是期望行为；勿把内部端口（如 DB）这么发布。

### 7.6 健康检查与部署核对清单

- [ ] Dockerfile `EXPOSE 8080` + `EXPOSE 40000/udp`；服务监听 `0.0.0.0:8080`。
- [ ] Coolify Ports Exposes=`8080`、Ports Mappings=`40000:40000/udp`、Domains=`https://chat.example.com`。
- [ ] 容器内**不**自管 TLS（Traefik 终结）。
- [ ] 云安全组放行 `443/tcp` + `40000/udp`。
- [ ] env 全量注入并与端口/IP 对齐；`LUMEN_LISTEN_ADDR` 用 `0.0.0.0`。
- [ ] PostgreSQL 数据库已创建（Coolify 资源或外部 PG）且 `LUMEN_DATABASE_URL` 连通；启动 DDL 自动建表。
- [ ] 改 env 后重新部署。
- [ ] 健康检查 `GET /api/v1/healthz` 通过。
- [ ] [v1] 自动更新：Persistent Storage 挂载 `/app/updates`（`LUMEN_UPDATES_DIR`）；`/updates/` 静态目录可经 `https://chat.example.com/updates/latest.json` 访问，`latest.json` 用 `Cache-Control: no-cache` + ETag、`*.exe` 长缓存生效。
- [ ] 已知：Ports Mappings 失去 Rolling Updates，部署有短暂中断（语音重连）。

### 7.7 自动更新文件托管（`[v1]`）

客户端自动更新（[客户端 §4](./client-design.md#4-go-后端自动更新)）所需的 `latest.json` + NSIS 安装包 + 签名，**由本服务端 Go 进程直接静态托管**，不引入额外容器/域名（统一决策，loop-6/desktop-5）：

- **路由**：`GET /updates/`（[§5.4](#54-rest-handler-与契约对应) 路由表）= `http.StripPrefix("/updates/", http.FileServer(http.Dir(cfg.UpdatesDir)))`，**公开、免鉴权**（仅静态下载）。
- **目录**：`LUMEN_UPDATES_DIR`（默认 `/app/updates`，[§6.1](#61-配置全部环境变量)），经 Coolify Persistent Storage 挂载持久化（[§7.3](#73-coolify-配置项)）。
- **对外地址**：`https://chat.example.com/updates/latest.json`——与部署 FQDN **同域**，复用 Traefik 证书（无需独立 `updates.*` 域名）。客户端配置的更新地址须与此一致（[客户端 §4.1/§4.3](./client-design.md#4-go-后端自动更新)）。
- **缓存头**：`latest.json` 用 `Cache-Control: no-cache` + ETag（避免读到旧版本清单）；`*.exe` 文件名含版本号、不可变，用长缓存。
- **发布流程**：bump semver → `wails build -nsis` → `sha256sum` → 离线 ed25519 签名 → 生成 `latest.json` → 上传到 `LUMEN_UPDATES_DIR` 卷。校验顺序 SHA256 → ed25519 → 才执行安装（客户端 §4）。

> 安全：`/updates/` 仅暴露只读静态文件，目录内不得放任何敏感文件；`http.FileServer` 默认禁止目录穿越。该路由是 [v1] 自动更新闭环在服务端侧的落地点。

---

## 8. v0/v1 归属汇总

| 模块/能力 | v0 | v1 |
|-----------|:--:|:--:|
| 配置加载 + fail-fast | ✓ | |
| JWKS 验签（RS256/iss/aud/exp） | ✓ | |
| owner 判定（配置态） | ✓ | |
| REST `bootstrap`/`me`/`channels`/`messages`/`members`/`healthz` | ✓ | |
| WS 握手 `auth`/`auth_ok`/`auth_error`（含 HANDSHAKE_TIMEOUT/KICKED 判定路径） | ✓ | |
| WS `send_message`/`message`、`join_channel`/`leave_channel`、`user_joined`/`user_left` | ✓ | |
| WS `speaking_state`、`webrtc_offer`/`webrtc_answer`/`ice_candidate` | ✓ | |
| SFU：UDPMux+NAT1To1+mDNS off、Room、OnTrack 转发、重协商、清理 | ✓ | |
| store：users/channels/messages、消息游标分页、ULID | ✓ | |
| 默认频道种子（首部署引导：text 大厅 + voice 开黑1） | ✓ | |
| 资料同步 userinfo 补齐（兜底） | ✓ | |
| WS `reauth`（token 刷新更新会话） + TOKEN_EXPIRED 中途处理 | | ✓ |
| 资料双向同步广播 `user_updated`（DB 变化触发） | | ✓ |
| WS `mute_state`（自静音/扬声静音广播） | | ✓ |
| owner REST：`POST/PATCH/DELETE channels` + 广播 `channel_*` | | ✓ |
| owner REST：`kick` + 软封禁 `kicked_until` + 断连 | | ✓ |
| 多语音频道（多 Room 并存） | | ✓ |
| 自动更新文件静态托管（`GET /updates/` FileServer，`LUMEN_UPDATES_DIR`） | | ✓ |
| 可选：限流、REST 软封禁拦截、指标 `/metrics` | | ✓（可选） |

> 纯前端能力（逐人本地音量/本地静音某人/输入输出设备/麦克风测试/PTT-VAD 切换/RNNoise 降噪）**不经服务端**（契约 §4.2 注），服务端无对应代码。

---

## 9. v2（E2E）对服务端的影响简述

> 仅概述，`[v2]` 推迟实现（契约附录 A）。v0/v1 仅依赖传输层 DTLS-SRTP。

- **SFU 转发逻辑不变**：SFrame 加密发生在 RTP 负载层，服务端仍按 `TrackLocalStaticRTP.WriteRTP` 原样转发密文，**不解密、不感知密钥**。`OnTrack`/`signalPeerConnections`/重协商均无需改动。
- **唯一新增信令**：`e2e_key_update`（S→C / 双向，契约附录 A 预留）。服务端职责仅限**中转**：在 `user_joined`/`user_left`（成员进出触发轮换）时，把某可信方（owner 或派生方案）下发的"加密后的房间密钥 + epoch 序号"透传给房内其他成员。服务端**不生成、不解密**密钥。
- **信令层改动点**：在 `signaling` 增加一个 `e2e_key_update` 的转发分支（按 `channel_id` 广播给同房成员），复用现有 `BroadcastToChannel`；与媒体层完全解耦。
- **新增内存态**：Room 可选记录当前 epoch（用于新成员加入时的密钥同步协调），不入库。
- **不影响**：REST、store、DDL、鉴权、配置均无需变更。
- **上线前确认**：目标 Wails webview（Chromium）版本支持 `RTCRtpScriptTransform`（Insertable Streams）——属客户端约束，服务端仅需就绪 `e2e_key_update` 转发。
- 详细密钥分发/轮换 epoch/与重协商交互将在独立 `docs/design/e2e-design.md` 展开，本文不深入。

---

## 10. 登录 broker（账户中心 + 桌面登录中介）

> **架构来源（权威，用户已决定）**：登录 broker 从 EdgeOne Pages Functions **整体移植进本 Go 服务端**（commit `82f344e`），落地为 `internal/broker` + `internal/secure` + `store` broker 两表 + `cmd/lumen-server/main.go` 装配。EdgeOne Pages 自此**纯静态**（只发布 `website/dist/` 的 React SPA，无 Functions、无 KV、无 secret）。本 Go 服务端是**唯一后端 = 资源服务器（[§2](#2-鉴权网关auth) 既有）+ OIDC 登录 broker（本节新增）**。broker 是既有 EdgeOne TypeScript Functions 的 **1:1 Go 移植**——`internal/secure` 镜像 `_lib/pkce.ts` + `_lib/session.ts`，`internal/broker` 镜像各 handler，行为逐字节等价，仅**宿主从 `example.com` 改为 `chat.example.com`**。

### 10.1 broker 包与 9 个路由

`broker.Handler`（构造后不可变、并发安全）经 `Register(mux)` 把 **9 个端点**挂到 root mux（与 `/api/v1/*` 共享），全部 **PUBLIC**（无 `RequireAuth`；broker 自管 handoff 码与封装 cookie 鉴权）：

| # | 方法+路径 | 类别 | 职责 | 镜像自 |
|---|-----------|------|------|--------|
| 1 | `GET /desktop/login` | 桌面 | 校验回环 `redirect_uri`(`http://127.0.0.1:<port>/…`)/`state`/`challenge`，暂存 login_ctx（PKCE），302 到 IdP `/authorize`（scope 含 `offline_access`、`aud=lumen-api`） | `desktop/login.ts` |
| 2 | `GET /desktop/callback` | 桌面 | 用 `code` 换 token，暂存**一次性** handoff（绑 desktop challenge），302 回回环带 `handoff_code + state`；access_token 绝不进 URL；设 `Referrer-Policy: no-referrer` | `desktop/callback.ts` |
| 3 | `POST /api/desktop/exchange` | 桌面 | `handoff_code + handoff_verifier` 换 `{access_token, expires_in, desktop_session_id, profile}`，校验 `S256(verifier)==bound_challenge`（常量时间）+ 一次性消费 | `exchange.ts` |
| 4 | `POST /api/desktop/refresh` | 桌面 | 用 `desktop_session_id` 刷新 access_token；IdP 返回新 refresh_token 则轮换入库；IdP 拒绝则删会话回 `401 SESSION_INVALID` | `refresh.ts` |
| 5 | `POST /api/desktop/logout` | 桌面 | 删 `desktop_session_id`（幂等），`204` | `logout.ts` |
| 6 | `GET /auth/login` | 账户中心 | 用封装短时 cookie（`lumen_auth_flow`）暂存 PKCE flow，302 到 IdP（scope **无** `offline_access`、**无** `aud=lumen-api`——账户中心不调 Lumen API） | `auth/login.ts` |
| 7 | `GET /auth/callback` | 账户中心 | 校验 state、换 code、建封装会话 cookie（`lumen_session`），302 到 `WEB_BASE_URL/account`；`Referrer-Policy: no-referrer` | `auth/callback.ts` |
| 8 | `POST /auth/logout` | 账户中心 | 清 `lumen_session` cookie，`204`（无服务端态可删） | `auth/logout.ts` |
| 9 | `GET /api/me` | 账户中心 | 从封装 `lumen_session` cookie 回 `{display_name, avatar_url}`，否则 `401 UNAUTHENTICATED`；**cookie 版**，区别于 Bearer 版 `GET /api/v1/me`（[§5.4](#54-rest-handler-与契约对应)） | `api/me.ts` |

> **`/api/download/latest` 代理已删除**：SPA 直接 `fetch chat.example.com/updates/latest.json`（[§7.7](#77-自动更新文件托管lv1) 已有的静态托管），不再经 broker 代理。
>
> **桌面 handoff 线协议逐字节保留**：端点 1~5 的入参/回执/绑定/错误码全部不变，唯一变化是**宿主域名 `example.com → chat.example.com`**。未来 Windows 客户端改动 = **仅 base URL**（`LUMEN_WEB_BASE_URL`/相关基址由 `example.com` 改指 `chat.example.com`）。
>
> **broker 私有 JSON 信封**（`internal/broker/http.go`，镜像 `_lib/http.ts`）：`{ "error": { "code", "message" } }`，与 rest 的 `{success,data,error}` 信封（[§5.4](#54-错误码映射)）**不同**；成功端点回裸 body。响应恒 `Cache-Control: no-store`，绝不回显 token/secret/堆栈。

`Register` 只挂路由、**不加任何中间件**（CORS 在 root 层统一加一次，见 [§10.6](#106-单点-cors-中间件)），以便与 `/api/v1/*` 共享 root mux。另有 `Routes()` 返回自带 broker 私有 `withCORS` 的独立 `http.Handler`——**仅供测试/独立运行**，生产路径不用。

### 10.2 internal/secure：共享密码学原语

`internal/secure` 是与 `protocol` 同层的底层包（仅依赖 `crypto/*`），把 EdgeOne `_lib/pkce.ts` + `_lib/session.ts` 逐一移植，令 Go 与（并行留存的）TS 行为等价：

- **token.go**：`Base64URLEncode/Decode`（RFC 7636 无填充）、`IsBase64URL`、`RandomToken(n)`（仅 `crypto/rand`）、`S256(verifier)=base64url(SHA256)`、`ConstantTimeEqualString`。常量：`DefaultTokenBytes=32`（verifier/state/handoff_code）、`SessionIDBytes=48`（desktop_session_id）。
- **aesgcm.go**：`Sealer`（固定 32 字节 AES-256-GCM 密钥，不可变、并发安全）。两组用法：
  - `Seal/Open`（返回 `base64url(iv).base64url(ct)` 字符串）——封装账户中心两枚 cookie（用 `sessionEncKey`）。
  - `Encrypt/Decrypt`（返回 `iv||ct||tag` 的 `[]byte`）——`refresh_token` at-rest 存 bytea（用 `refreshEncKey`）。
  - `NewSealer(key)` 校验 key 长度；`DecodeKey` 供 config 校验 base64→32 字节。

两把密钥来自 [§6.1](#61-配置全部环境变量) 的 `LUMEN_SESSION_ENC_KEY` 与 `LUMEN_REFRESH_ENC_KEY`，config 层强制**二者不同**（decision 2）。

### 10.3 store broker 两表 + janitor

`internal/store/migrate.go` 的 `schemaDDL` 在三张业务表后追加：

```sql
-- broker_states：两种单次消费的 kind，按不透明 token 主键。
--   login_ctx：/desktop/login 暂存的 oidc verifier/state/challenge，/desktop/callback 消费一次（TTL~600s）
--   handoff  ：/desktop/callback 暂存的 token 集 + bound_challenge，/api/desktop/exchange 消费一次（TTL~120s）
-- 每次读都 WHERE expires_at > now()；消费用事务内 DELETE ... RETURNING（无 TOCTOU/无重放）。
-- access_token 只在 handoff 行的 ~120s 窗口内存在，别处不落库。
CREATE TABLE IF NOT EXISTS broker_states (
    key TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('login_ctx','handoff')),
    payload JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_broker_states_expires_at ON broker_states (expires_at);

-- desktop_sessions：长会话。refresh_token 以 AES-256-GCM 加密存 bytea（LUMEN_REFRESH_ENC_KEY）；
-- access_token 不落此表。无 expires_at：会话存活至 logout 或 IdP 刷新被拒时删除。
CREATE TABLE IF NOT EXISTS desktop_sessions (
    id TEXT PRIMARY KEY,
    refresh_token_enc BYTEA NOT NULL,
    sub TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
```

`Store` 接口新增 broker 方法（`internal/store/broker.go` 实现）：

| 方法 | 语义 |
|------|------|
| `PutLoginContext(ctx, oidcState, LoginContext)` | upsert login_ctx（TTL 600s） |
| `TakeLoginContext(ctx, oidcState) (LoginContext, error)` | 事务内 `DELETE ... RETURNING` 一次性取（过期/缺失 → `ErrNotFound`） |
| `PutHandoff(ctx, handoffCode, Handoff)` | upsert handoff（TTL 120s；payload 含 access_token/refresh_token/sub/bound_challenge/profile） |
| `ConsumeHandoff(ctx, handoffCode) (Handoff, error)` | 一次性消费（映射 `HANDOFF_NOT_FOUND`） |
| `DeleteExpiredBrokerStates(ctx) (int64, error)` | janitor 清过期行，回删除条数 |
| `PutSession(ctx, DesktopSession)` | upsert 桌面会话；用 refresh sealer 加密 `refresh_token_enc`（无 sealer → `ErrNoSealer`） |
| `GetSession(ctx, id) (DesktopSession, error)` | 读并解密 refresh_token |
| `DeleteSession(ctx, id) error` | 删会话（幂等，缺失即 no-op） |

**janitor（decision 4，`cmd/lumen-server/main.go` `runJanitor`）**：每 `janitorInterval=60s` 调 `DeleteExpiredBrokerStates` 回收过期 login_ctx/handoff（读路径已用 `expires_at > now()` 屏蔽过期行，janitor 仅回收空间）；`rootCtx` 取消即退出，单次清理失败仅记日志、循环继续。`desktop_sessions` 无 TTL、不被 janitor 清理。

### 10.4 broker 装配（NewHandler）

`broker.NewHandler(ctx, cfg, store, logger)`：

1. 用 `cfg.SessionEncKey()` 建 cookie sealer（封装 `lumen_session`/`lumen_auth_flow`）。
2. 建 OIDC 客户端：`authorize/token/userinfo` URL 为空时经 **OIDC discovery**（`.well-known/openid-configuration`，自 `LUMEN_OAUTH_ISSUER`）推导；持有 `client_id/client_secret/audience`。
3. store 必须以 `NewWithSealer`（注入 refresh sealer）构造，否则桌面会话方法 `ErrNoSealer`（[§6.2](#62-启动装配main)）。

桌面/账户中心两条流程的 scope 区分（`broker.go`）：桌面 `openid profile email offline_access` + `aud=lumen-api`；账户中心 `openid profile email`（无 offline_access、无 aud——账户中心永不调 Lumen API）。

### 10.5 账户中心 cookie（同 SITE 跨源）

SPA 在 `example.com`，broker 在 `chat.example.com`——**同 SITE**（同一注册域）、**跨 ORIGIN**。据此（`internal/broker/session.go`，decision 1）：

| cookie | 用途 | MaxAge | 属性 |
|--------|------|--------|------|
| `lumen_session` | 账户中心会话（封装 `{sub, display_name, avatar_url, exp}`） | 8h | `SameSite=Lax` + `HttpOnly` + `Secure` + `Path=/` + **HOST-ONLY（无 Domain）** |
| `lumen_auth_flow` | OIDC 登录流上下文（封装 `{verifier, state, exp}`） | 10m | 同上 |

**HOST-ONLY（不设 `Domain`）** 使 cookie 只回发到签发主机 `chat.example.com`，绝不写成 `Domain=.example.com`。`/auth/login`、`/auth/callback` 是**顶层导航**（浏览器地址栏跳转，`SameSite=Lax` 允许携带 cookie）；账户中心 XHR（如 `GET /api/me`、`POST /auth/logout`）由 SPA 以 `credentials:'include'` 发起，靠 [§10.6](#106-单点-cors-中间件) 的精确白名单 CORS + `Allow-Credentials` 携带 cookie。

### 10.6 单点 CORS 中间件

**唯一** CORS 中间件在 root 层加一次（`internal/rest/cors.go`，`withCORS(cfg.WebBaseURL, handler)`，decision 3），包住整个 mux（含 `/api/v1/*` 与 broker 9 路由）：

- 仅当请求 `Origin` **精确等于** `LUMEN_WEB_BASE_URL`（先 `canonicalOrigin` 归一到 `scheme://host[:port]`、去尾斜杠）时才发 `Access-Control-Allow-Origin: <origin>` + `Allow-Credentials: true` + `Allow-Methods: GET, POST, OPTIONS` + `Allow-Headers: Content-Type` + `Max-Age: 600`；**永不用 `*`**。
- **恒发 `Vary: Origin`**（共享缓存按 Origin 分键，绝不把某源的 ACAO body 发给别的源）。
- `OPTIONS` preflight 就地回 `204`；不匹配的源得到无 ACAO 的裸 `204`，浏览器判 CORS 失败。
- 空 `WEB_BASE_URL` → 完全禁用 CORS（永不发 ACAO），安全默认。

> `internal/broker/cors.go` 有一份等价的 broker 私有 `withCORS`，**仅**用于 `Handler.Routes()` 的独立/测试路径；生产由 `rest.NewRouter` 的这一份单点包裹，broker 的 `Register` 不再叠加中间件，避免重复 CORS 头。

### 10.7 v0/v1 归属（broker）

broker 全套（9 端点 + 两表 + secure + janitor + 单点 CORS）随 EdgeOne→Go 的迁移落地，与既有 v0 登录回路一同视为 **v0**（[00-overview §6.1](./00-overview.md#61-v0--最小开黑回路)）；账户中心资料展示/退出亦 v0。owner/多频道等仍按 [§8](#8-v0v1-归属汇总) 的 v0/v1 归属。

# 语音聊天应用 · 设计文档（原始设计稿）

> ⚠️ **本文件是最初的设计稿。** 经需求访谈确认细节后，已产出**完整、定稿**的设计文档集，位于 [`docs/design/`](./docs/design/)：
> - [`docs/design/00-overview.md`](./docs/design/00-overview.md) — 主总览（目标、决策总表、架构、路线图、部署）
> - [`docs/design/protocol-design.md`](./docs/design/protocol-design.md) — **唯一权威接口契约**（REST + WS 协议 + SQLite 数据模型）
> - [`docs/design/server-design.md`](./docs/design/server-design.md) — 服务端详细设计（Go 单二进制）
> - [`docs/design/client-design.md`](./docs/design/client-design.md) — 客户端详细设计（Wails v2 + Svelte，仅 Windows）
> - [`docs/research/`](./docs/research/) — 6 份技术选型调研（Pion/Coolify/Wails/OAuth/前端）
>
> 本稿内容已被上述定稿集**完整吸收并扩充**，保留于此仅作历史参考。**实现以 `docs/design/` 为准。**

---

> 一款类 Discord 的轻量语音聊天工具，主要给小圈子朋友开黑用。客户端仅限 Windows，服务端自托管。本文件是交付给实现方（Claude Code）的完整设计说明。项目代号随意替换，下文统一称「本应用」。

---

## 1. 目标与非目标

**目标**
- 低延迟语音通话（开黑核心），文字聊天为辅。
- 类 Discord 的频道结构：一个服务器下分多个文字频道 / 语音频道。
- 使用**自有的 OAuth2 服务器作为唯一登录方式**，业务服务端不自管密码。
- 客户端是独立的 Windows 原生 exe；服务端是单个 Go 二进制，运行在一台有公网 IP 的小 VPS 上。
- 轻量、易部署、易维护，规模目标是同一语音频道 2~6 人。

**非目标（当前阶段不做）**
- 大规模并发（>10 人同房）。需要时再从 SFU 升级，不在此设计范围。
- 视频通话、屏幕共享。
- 移动端 / macOS / Linux 客户端。
- 完整的 MLS 群组密钥协议（E2E 起步用简化方案，见第 9 节）。

---

## 2. 技术栈

| 层 | 选型 | 说明 |
|---|---|---|
| 客户端外壳 | Wails v2 | Go 后端 + webview 前端，编译成独立原生 exe，依赖 Win10/11 自带的 WebView2 |
| 客户端前端 | webview 内的 web 技术 | 可用纯 HTML/JS 或任意框架（Svelte / React / Vue 皆可），承载 UI、WebRTC、降噪 |
| 服务端 | Go，单二进制 | 信令 + SFU + 频道/消息 + 鉴权，全部打包进一个可执行文件 |
| 媒体传输 | WebRTC | 语音走 WebRTC，Opus 编码；传输层 DTLS-SRTP 加密 |
| SFU | pion/webrtc | 纯 Go 的 WebRTC 实现，做选择性转发（每人上传一份，服务器分发） |
| 信令 | WebSocket | 推荐 `coder/websocket` 或 `gorilla/websocket` |
| 降噪 | RNNoise（WASM） | 前端经 AudioWorklet 接入，库如 `@jitsi/rnnoise-wasm`；升级可选 DeepFilterNet |
| 数据存储 | SQLite | 推荐纯 Go 的 `modernc.org/sqlite`，无需 CGO；存用户、频道、消息 |
| 鉴权 | OAuth2 Authorization Code + PKCE | token 验证优先 JWT + JWKS 本地验签 |
| 端对端加密（v2） | Insertable Streams + SFrame | 在 RTP 帧上再加一层应用层加密，SFU 只转发密文 |

---

## 3. 架构总览

系统分三层：**身份**（外部 OAuth2 服务器）、**客户端**（Wails）、**业务服务端**（Go）。客户端和服务端都只信任 OAuth2 服务器签发的 token，服务端自身不存储登录凭据。

```
            ┌─────────────────────────┐
            │   你的 OAuth2 服务器      │  唯一身份来源
            └─────────────────────────┘
                 ▲ ①取token   ▲ ③验token
                 │             │
   ┌─────────────┴───┐   ┌────┴──────────────┐
   │  Wails 客户端    │   │  Go 服务器(单二进制)│
   │ ┌─────────────┐ │   │ ┌───────────────┐ │
   │ │登录 OAuth2   │ │   │ │鉴权网关 验token│ │
   │ │  PKCE        │ │   │ └───────────────┘ │
   │ ├─────────────┤ │②  │ ┌───────────────┐ │
   │ │信令+文字 WS  │◄┼───┼►│信令+SFU+频道   │ │
   │ ├─────────────┤ │④  │ └───────────────┘ │
   │ │语音 WebRTC   │◄┼───┼►│ ┌───────────────┐ │
   │ │  +RNNoise    │ │   │ │SQLite 用户/消息 │ │
   │ └─────────────┘ │   │ └───────────────┘ │
   └─────────────────┘   └───────────────────┘
```

**四条主要数据通路**
- ① 客户端向 OAuth2 服务器登录、取 token。
- ② 客户端带 token 连服务端 WebSocket（信令 + 文字消息）。
- ③ 服务端验证 token（向 OAuth2 服务器取公钥本地验签，或调 introspection）。
- ④ 客户端与服务端 SFU 建 WebRTC 连接传语音。

**职责划分的关键决策**
- WebRTC、麦克风采集（getUserMedia）、回声消除、降噪 worklet、SFrame 加密都放在**前端（webview）**，因为浏览器引擎原生支持这些 API，几乎零成本。
- OAuth2 的 PKCE 流程、本地回调监听、token 安全存储放在**客户端 Go 层**，因为这些需要原生能力（起本地端口、访问系统凭据库、调系统浏览器）。
- SFU **不单独鉴权**：语音连接通过已鉴权的信令通道协商，SFU 只服务信令层已授权的连接。

---

## 4. 客户端（Wails）

**前端（webview）负责**
- 全部 UI（登录界面、频道列表、文字聊天、语音频道成员与说话指示）。
- WebSocket 客户端：连服务端，收发信令与文字消息。
- WebRTC：`getUserMedia` 取麦克风，`RTCPeerConnection` 连 SFU。getUserMedia 约束开启 `echoCancellation` 与 `autoGainControl`；`noiseSuppression` 交给 RNNoise（见第 8 节）。
- （v2）`RTCRtpScriptTransform` / encoded transform 做 SFrame 加解密。

**Go 后端负责**
- OAuth2 PKCE 登录流程（见第 6 节）：生成 PKCE 校验码、起本地回调 HTTP 监听、用系统浏览器打开授权页、用授权码换 token。
- token 安全存储：access_token 内存持有；refresh_token 写入 Windows 凭据库（DPAPI / Credential Manager，库如 `danieljoos/wincred` 或 `zalando/go-keyring`）。
- token 过期时用 refresh_token 静默刷新。
- 通过 Wails 的 binding 把「当前有效 access_token」暴露给前端，供前端连 WebSocket / WebRTC 使用。

---

## 5. 服务端（Go，单二进制）

一个进程内包含以下模块：

- **鉴权网关**：所有 WebSocket 连接建立时验证 token（见第 7 节）。验证通过后把用户身份（`sub` claim）绑定到该连接会话。
- **信令模块**：维护房间（语音频道）成员关系；中转 WebRTC 的 SDP offer/answer 与 ICE candidate；广播成员状态（加入/离开/说话）。
- **SFU 模块（Pion）**：每个语音频道对应一个内存中的 Room；客户端上传一条 audio track，SFU 订阅后转发给房间内其他成员（见第 8 节… 实为第 7.2）。
- **频道 / 消息模块**：频道的增删改查、成员关系、文字消息的广播与持久化。
- **数据层**：SQLite，封装在一个 store 包内。

部署形态：单二进制 + 一个 SQLite 文件 + 配置（OAuth2 的 issuer / JWKS URL / client_id 等）。

---

## 6. OAuth2 PKCE 登录流程

桌面应用是 OAuth2 的「公开客户端」，不能安全保存 client secret，因此采用 **Authorization Code + PKCE**，回调用本地回环地址（RFC 8252 原生应用最佳实践）。

**步骤**
1. **准备**：客户端 Go 层生成随机 `code_verifier`，计算 `code_challenge = BASE64URL(SHA256(code_verifier))`；在 `127.0.0.1` 上起一个临时 HTTP 监听（随机可用端口），路径 `/callback`；生成随机 `state` 防 CSRF。
2. **打开授权页**：用**系统默认浏览器**（非内嵌 webview，更安全）打开 OAuth2 授权端点，query 参数：
   `response_type=code`、`client_id=<本应用>`、`redirect_uri=http://127.0.0.1:<port>/callback`、`code_challenge`、`code_challenge_method=S256`、`scope=<所需 scope>`、`state`。
3. **登录授权**：用户在你的 OAuth2 服务器登录并授权；浏览器重定向到本地回调，带回 `code` 与 `state`。本地 HTTP 监听收到后给浏览器返回一个「可关闭」的成功页。
4. **换取 token**：客户端校验 `state` 一致后，向 OAuth2 的 token 端点 POST：`grant_type=authorization_code`、`code`、`code_verifier`、`redirect_uri`、`client_id`，换回 `access_token`（+ `refresh_token`，若签发）。refresh_token 写入 Windows 凭据库。
5. **连接**：前端带 `access_token` 连服务端 WebSocket（放在握手 Authorization header 或连接后的首条鉴权消息），服务端验签通过即放行。

实现库：客户端 Go 层可用 `golang.org/x/oauth2`（含 PKCE 支持）。

---

## 7. 鉴权与信令

### 7.1 Token 验证（服务端）

- **首选：JWT + JWKS 本地验签**。服务端启动时从 OAuth2 服务器的 JWKS 端点拉取公钥（推荐 `MicahParks/keyfunc` 配合 `golang-jwt/jwt`），对每个 access_token 本地校验签名、`iss`、`aud`、`exp`。零网络往返，适合频繁连接。
- **备选：Token Introspection（RFC 7662）**。每次向 OAuth2 服务器询问 token 是否有效，简单但每次一次往返；适用于不签发 JWT 的 OAuth2 实现。
- 用户首次通过验证时，按 token 的 `sub`（subject）在 `users` 表 upsert 一条记录。

### 7.2 信令协议（WebSocket，JSON 消息）

所有消息形如 `{ "type": "...", "data": { ... } }`。建议的消息类型：

- 鉴权：连接后首条 `auth`（带 token），服务端回 `auth_ok` / `auth_error`。
- 频道：`join_channel` / `leave_channel`；服务端广播 `user_joined` / `user_left`。
- 文字：客户端 `send_message { channel_id, content }`；服务端持久化后广播 `message { id, channel_id, user_id, content, created_at }`。
- 语音信令：`webrtc_offer` / `webrtc_answer` / `ice_candidate`，在客户端与 SFU 之间双向中转。
- 状态：`speaking_state { user_id, speaking }`，用于说话指示。

### 7.3 SFU 媒体转发（Pion）

- 每个**语音频道**在服务端对应一个 Room（内存对象，持有该房间内所有成员的 PeerConnection 与其 audio track）。
- 成员加入语音频道：与服务端建立一个 `RTCPeerConnection`，上传自己的一条 Opus audio track。
- 服务端转发逻辑：新成员加入时，把房间内已有成员的 track 添加到新成员的 PeerConnection；同时把新成员的 track 添加到每个已有成员的 PeerConnection。track 的增删触发**重协商**（重新 offer/answer，经信令通道）。
- 服务端**只转发，不转码**；保持 Opus 原样。
- **NAT/打洞**：服务端有公网 IP、客户端直连服务端，通常**不需要 STUN/TURN**。仅当服务端本身处于 NAT 之后才需要额外配置。

---

## 8. 降噪（RNNoise）

- 前端流水线：`getUserMedia` 取麦克风流 → 接一个加载了 RNNoise WASM 的 `AudioWorkletNode` 处理 → 处理后的输出流作为 track 加入 `RTCPeerConnection`。
- getUserMedia 约束：开启 `echoCancellation: true`、`autoGainControl: true`；`noiseSuppression` 可关闭（由 RNNoise 接管）。
- 推荐库：`@jitsi/rnnoise-wasm` 或 `@shiguredo/rnnoise-wasm`。
- 升级路径：若 RNNoise 效果不够，可换 DeepFilterNet（效果更强、更重、集成更复杂）。开黑场景 RNNoise 通常足够。

---

## 9. 端对端加密（E2E，v2）

**概念澄清**：WebRTC 传输层已强制 DTLS-SRTP 加密，所以「客户端↔服务端」一定是密文；但在 SFU 架构下，服务端为转发需要能处理媒体，**这不等于端到端**。真正的端到端（连自托管服务端都听不到）需要额外一层应用层加密。

**方案**
- v1：仅依赖传输加密（DTLS-SRTP），已能阻挡网络中间人。鉴于服务端自托管、仅服务信任的朋友，这一层在初期可接受。
- v2：使用 `Insertable Streams`（`RTCRtpScriptTransform` / encoded transform），在 RTP 帧编码后、发送前用房间密钥做 AES-GCM 加密（SFrame 风格），接收端解密。SFU 只转发密文。
- **密钥管理（v2 起步，简化版）**：每个语音房间一把共享对称密钥，通过已鉴权的信令通道分发；成员加入时下发当前密钥，成员退出时轮换（rekey）。
- **完整方案（v2 之后，可选）**：参考 Discord 的 DAVE 协议，用 MLS（Messaging Layer Security）做正规的群组密钥协商。复杂度高，按需引入。

---

## 10. 数据模型（SQLite）

```sql
CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  oauth_subject TEXT UNIQUE NOT NULL,      -- OAuth2 token 的 sub claim
  display_name  TEXT NOT NULL,
  avatar_url    TEXT,
  created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE channels (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  type       TEXT NOT NULL CHECK (type IN ('text','voice')),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id INTEGER NOT NULL REFERENCES channels(id),
  user_id    INTEGER NOT NULL REFERENCES users(id),
  content    TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_messages_channel ON messages(channel_id, created_at);
```

> 开黑场景默认单服务器（single guild），故未建 servers/guilds 表。若将来要多服务器，再加 `servers` 表并给 `channels` 加 `server_id`。

---

## 11. 建议目录结构

```
/server                  Go 服务端（单二进制）
  main.go                启动、配置、装配各模块
  /auth                  token 验证（JWKS / introspection）
  /signaling             WebSocket 信令、连接会话管理
  /sfu                   Pion 房间与转发逻辑
  /channels              频道与消息业务逻辑
  /store                 SQLite 封装
  config.go              配置（OAuth issuer、JWKS URL、client_id、监听端口等）
  go.mod

/client                  Wails 应用
  /backend               Go：OAuth PKCE、凭据存储、暴露给前端的 binding
  /frontend              webview：UI、WS 客户端、WebRTC、RNNoise worklet、(v2)SFrame
  wails.json
  go.mod

/shared                  （可选）信令消息类型定义，前后端共享
```

---

## 12. 实现路线（分期）

**v0 — 打通最小回路（先能开黑）**
- 单个语音频道 + 单个文字频道。
- OAuth2 PKCE 登录跑通；服务端 token 验签。
- WebSocket 信令 + Pion SFU 转发，两人能听到彼此。
- 前端接入 RNNoise。
- 验收：两台机器登录后进同一语音频道，能清晰对话；文字消息能收发。

**v1 — 类 Discord 体验**
- 多个文字 / 语音频道，频道切换。
- 成员列表、在线状态、说话指示。
- 文字消息持久化与历史加载。

**v2 — 端对端加密**
- Insertable Streams + SFrame，房间共享密钥起步。
- 成员进出时的密钥下发与轮换。
- （可选）后续引入 MLS。

---

## 13. 关键依赖清单

**服务端（Go）**
- `github.com/pion/webrtc/v4` — SFU
- `github.com/coder/websocket`（或 `gorilla/websocket`）— 信令
- `modernc.org/sqlite` — 纯 Go SQLite（无需 CGO）
- `github.com/golang-jwt/jwt/v5` + `github.com/MicahParks/keyfunc/v3` — JWT/JWKS 验签

**客户端（Go 层）**
- `github.com/wailsapp/wails/v2`
- `golang.org/x/oauth2` — OAuth2 + PKCE
- `github.com/danieljoos/wincred`（或 `github.com/zalando/go-keyring`）— Windows 凭据存储

**客户端（前端）**
- `@jitsi/rnnoise-wasm`（或 `@shiguredo/rnnoise-wasm`）— 降噪

---

## 14. 部署与运维注意

- 服务端跑在一台有公网 IP 的 VPS；开黑规模带宽需求极小（数路 Opus 约每路 ~32–64 kbps）。
- **信令必须走 WSS（TLS）**，因为 token 在传输中。可用 Caddy 自动签证书反代，或服务端自带 TLS。
- Pion 的 WebRTC 媒体需要开放一段 **UDP 端口范围**（在防火墙/安全组放行）。
- 服务端配置项：OAuth2 issuer、JWKS URL（或 introspection URL）、client_id、监听端口、SQLite 文件路径、WebRTC UDP 端口范围、TLS 证书路径。
- 客户端配置项：OAuth2 授权/token 端点、client_id、scope、服务端 WSS 地址。

---

## 15. 待确认 / 留给实现方的开放项

- 前端框架的最终选择（纯 HTML/JS vs Svelte/React/Vue）——本设计不约束，按实现方习惯。
- OAuth2 服务器是否签发 JWT：若是，用 JWKS 本地验签；若否，用 introspection。需在配置中体现两种模式之一。
- 说话检测（speaking indicator）用前端音量阈值还是 WebRTC 的 audio level，二选一即可。
- v0 是否需要房间持久化：开黑场景房间可纯内存，进程重启即清空，简化实现。

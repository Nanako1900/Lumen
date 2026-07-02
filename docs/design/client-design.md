# Lumen 客户端详细设计（Wails v2 + Svelte，仅 Windows）

> 文档版本: 1.0
> 状态: 详细设计（可直接实现）
> 适用范围: Lumen 桌面客户端（Wails v2 外壳 + Svelte 前端，仅 Windows/amd64）
> 上游契约: [`docs/design/protocol-design.md`](./protocol-design.md)（**唯一权威接口契约**，端点名/消息名/字段名以其为准）
> 依据调研: `docs/research/03-wails-desktop.md`、`04-wails-autoupdate.md`、`05-oauth-pkce-jwks.md`、`06-frontend-webrtc-rnnoise.md`

本文是客户端实现的详细设计。**所有与服务端交互的端点路径、WS 消息 `type`、JSON 字段名一律以协议契约为准**；本文若与契约冲突，以契约为准。

**版本归属约定**（与契约一致，贯穿全文）：

| 标记 | 含义 |
|------|------|
| `[v0]` | 最小可用闭环：登录、文字历史、单语音频道收发、基本状态广播 |
| `[v1]` | 完整功能：频道 CRUD、踢人、资料双向同步、逐人音量、PTT/VAD、降噪、多频道、桌面集成、自动更新 |
| `[v2]` | 推迟项：E2E 加密（Insertable Streams + SFrame）。仅见 [§13](#13-v2-对前端的影响概述sframe--insertable-streams) |

未标注的内容默认 `[v0]` 即需要。

---

## 目录

1. [整体架构与职责切分](#1-整体架构与职责切分)
2. [Go 后端：Web 中介登录与 token 管理](#2-go-后端web-中介登录与-token-管理)
3. [Go 后端：桌面集成（PTT/托盘/最小化/单实例/自启）](#3-go-后端桌面集成ptt托盘最小化单实例自启)
4. [Go 后端：自动更新](#4-go-后端自动更新)
5. [前端结构（Svelte）](#5-前端结构svelte)
6. [前端 stores 与状态模型](#6-前端-stores-与状态模型)
7. [前端交互层：REST 客户端与 WS 客户端](#7-前端交互层rest-客户端与-ws-客户端)
8. [前端语音流水线](#8-前端语音流水线)
9. [SFU 多 track 重协商在前端的处理](#9-sfu-多-track-重协商在前端的处理)
10. [WS 断线重连与 PC 重建策略](#10-ws-断线重连与-pc-重建策略)
11. [错误处理与用户提示](#11-错误处理与用户提示)
12. [UI 布局、主题与本地设置持久化](#12-ui-布局主题与本地设置持久化)
13. [v2 对前端的影响概述（SFrame + Insertable Streams）](#13-v2-对前端的影响概述sframe--insertable-streams)
14. [附录：版本归属速查表](#14-附录版本归属速查表)

---

## 1. 整体架构与职责切分

### 1.1 顶层结构

Lumen 客户端 = **Wails v2 Go 外壳**（提供操作系统级原生能力）+ **Svelte 前端**（运行于 WebView2/Chromium，承载全部 UI 与 WebRTC）。二者通过 Wails 的两种通道通信：**Bindings**（前端 → Go 的方法调用，含返回值/错误）与 **Events**（Go ↔ 前端的异步事件总线）。

```
┌──────────────────────────────────────────────────────────────────────┐
│                      Lumen.exe （单进程，Wails v2）                      │
│                                                                        │
│   ┌────────────────────────────┐        ┌──────────────────────────┐  │
│   │   Go 外壳 (原生能力)         │        │   Svelte 前端 (WebView2) │  │
│   │                            │        │                          │  │
│   │  • 调登录中介登录(回环handoff)│ binding│  • UI (频道/文字/语音/设置)│  │
│   │  • session 存储/静默刷新     │◀──────▶│  • REST fetch 调用        │  │
│   │  • 资料缓存 (/exchange)      │ events │  • WS 客户端 (信令+实时)   │  │
│   │  • 全局 PTT 热键 (后台)      │───────▶│  • WebRTC PeerConnection  │  │
│   │  • 系统托盘 / 最小化         │        │  • getUserMedia/RNNoise   │  │
│   │  • 单实例锁 / 开机自启       │        │  • AudioWorklet/Analyser  │  │
│   │  • 自动更新                  │        │  • Web Audio (逐人音量)   │  │
│   └────────────┬───────────────┘        └─────────────┬────────────┘  │
└────────────────┼──────────────────────────────────────┼───────────────┘
                 │ (系统浏览器/回环/凭据管理器/注册表/键盘钩子)│ HTTPS REST / WSS / DTLS-SRTP
                 ▼                                        ▼
   Windows OS / 登录中介 chat.example.com (Go 服务端)  Lumen 服务端 (经 Coolify Traefik)
```

### 1.2 职责切分原则

**铁律**：凡浏览器 webview 能做的（UI、网络 REST/WS、WebRTC、音频处理）都放前端；凡浏览器做不到或做不好的（系统浏览器拉起登录中介、回环 handoff、OS 凭据存储、全屏游戏全局热键、托盘、注册表、替换 exe）才放 Go 外壳。**IdP 客户端机密（client_secret/issuer/client_id/scope）全部移到 Go 服务端登录中介（`chat.example.com`，与资源服务器同进程），桌面不再持有**（见 [§2](#2-go-后端web-中介登录与-token-管理) 与 [`./web-design.md`](./web-design.md) §5）。

> **登录中介宿主变更（仅 base URL）**：登录中介（`/desktop/login`、`/desktop/callback`、`/api/desktop/{exchange,refresh,logout}` 等 9 个端点）已从官网 `example.com` 迁到 **Go 服务端 `chat.example.com`**（与既有资源服务器 JWT/JWKS 校验同进程；`client_secret`/`refresh_token` 仅存服务端 Postgres，`refresh_token` 用独立密钥静态加密）。官网 `example.com` 退化为**纯静态 SPA**（腾讯 EdgeOne Pages 只托管 `website/dist/`，无 Pages Functions、无 KV、无任何密钥）。**桌面 handoff 的线路契约逐字节保持不变，仅宿主由 `example.com` 改为 `chat.example.com`**；对 Windows 客户端而言，未来变更 = 只改登录中介 base URL（`LUMEN_WEB_BASE_URL`），本文其余登录设计（端点名、请求/响应字段、handoff 校验）一律不动。本节代码仍为**设计示例，实现暂不纳入**。

| 能力 | 归属 | 理由 | 版本 |
|------|------|------|------|
| 调登录中介登录（系统浏览器 + 127.0.0.1 回环 handoff） | **Go** | 严禁内嵌 webview 登录（钓鱼风险，调研 05 §1.1）；回环 HTTP 服务需原生网络栈；IdP 交互改由 Go 服务端登录中介（`chat.example.com`）完成 | v0 |
| desktop_session_id 安全存储 | **Go** | webview 无法安全访问 Windows Credential Manager；refresh_token 不出服务端，桌面只存不透明会话 ID | v0 |
| access_token 静默刷新（调登录中介 /api/desktop/refresh） | **Go** | 与会话生命周期耦合，集中在 Go 管理；client_secret 在服务端，桌面只传 session ID | v0 |
| 把有效 access_token 暴露给前端 | **Go→前端** | REST `Bearer` 与 WS `auth`/`reauth` 需要 | v0 |
| REST 调用（bootstrap/messages/channels/members/owner CRUD） | **前端** | 标准 fetch，token 由 Go 提供 | v0/v1 |
| WS 连接、鉴权握手、信令、实时消息/状态 | **前端** | 浏览器原生 WebSocket | v0 |
| WebRTC PeerConnection / SDP / ICE | **前端** | 浏览器内置 WebRTC，Go 不参与媒体 | v0 |
| getUserMedia / RNNoise / AudioWorklet / AnalyserNode / GainNode | **前端** | Web Audio API 仅在 webview 可用 | v0/v1 |
| 全局 PTT 热键（应用后台/游戏全屏仍生效） | **Go** | OS 级键盘钩子，webview 无法捕获失焦按键 | v1 |
| 系统托盘 + 菜单 + 最小化隐藏 | **Go** | OS 级 shell 集成 | v1 |
| 单实例锁 | **Go** | OS mutex | v1 |
| 开机自启（注册表 Run 键） | **Go** | 写 HKCU 注册表 | v1 |
| 自动更新（检查/下载/校验/安装） | **Go** | 替换运行中 exe、启动安装包 | v1 |

### 1.3 binding/Events 通信约定

**Bindings（前端调用 Go，有返回值与 error）**——绑定到 Wails 的 `App` 结构体（及拆分的子服务结构体，见 [§1.5](#15-go-侧目录结构)）。命名用 PascalCase 方法名，前端通过自动生成的 `wailsjs/go/...` 导入。

| Go binding 方法 | 入参 | 返回 | 用途 | 版本 |
|-----------------|------|------|------|------|
| `Login(ctx)` | — | `(LoginResult, error)` | 打开登录中介 `/desktop/login`、起 127.0.0.1 回环、换 token（系统浏览器 + handoff） | v0 |
| `Logout(ctx)` | — | `error` | 调登录中介 `/api/desktop/logout` + 清本地 `desktop_session_id` 与内存 token | v0 |
| `GetAccessToken(ctx)` | — | `(string, error)` | 取**当前有效**（必要时已向登录中介静默刷新）access_token | v0 |
| `GetBootstrapConfig(ctx)` | — | `(ClientConfig, error)` | 取客户端配置（web base、API base、WS url） | v0 |
| `IsLoggedIn(ctx)` | — | `(bool, error)` | 启动时判断是否已有有效 `desktop_session_id` | v0 |
| `SetAutoStart(ctx, enable)` | `bool` | `error` | 开关开机自启 | v1 |
| `IsAutoStartEnabled(ctx)` | — | `(bool, error)` | 查询自启状态 | v1 |
| `SetGlobalPTTKey(ctx, spec)` | `HotkeySpec` | `error` | 设置/重绑全局 PTT 热键（`HotkeySpec` 见 [§3.1](#31-全局-ptt-热键后台全屏游戏生效)「PTT 改键闭环」） | v1 |
| `SetFullscreenGameMode(ctx, on)` | `bool` | `error` | 切换低层钩子（全屏游戏兼容） | v1 |
| `SetTrayMuteChecked(ctx, muted)` | `bool` | `error` | 按前端自身 `muted` 驱动托盘"麦克风静音"勾选态（前端→Go 单一事实源回写，见 [§8.5](#85-远端逐人音量--本地静音某人调研-06-4)） | v1 |
| `CheckForUpdate(ctx, current)` | `string` | `(UpdateInfo, error)` | 检查新版本 | v1 |
| `DownloadAndInstallUpdate(ctx)` | — | `error` | 下载+校验+静默安装+退出 | v1 |

**Events（Go 主动推前端，无返回值，单向）**——用 `runtime.EventsEmit(ctx, name, payload)`，前端 `EventsOn(name, handler)` 订阅。事件名一律 `域:动作` 小写冒号分隔。

| 事件名 | 方向 | payload | 用途 | 版本 |
|--------|------|---------|------|------|
| `ptt:start` | Go→前端 | — | 全局 PTT 键**按下**（开门控） | v1 |
| `ptt:stop` | Go→前端 | — | 全局 PTT 键**松开**（关门控） | v1 |
| `token:refreshed` | Go→前端 | `{ access_token }` | token 已静默刷新（前端应对 WS 发 `reauth`） | v1 |
| `auth:expired` | Go→前端 | — | refresh 失败，需重新登录 | v0 |
| `tray:show` | Go→前端 | — | 托盘"显示主窗口"被点击（前端可恢复 UI 状态） | v1 |
| `tray:toggle_mute` | Go→前端 | — | 托盘"麦克风静音"菜单被点击，请求前端切换自静音（前端经 `voice.toggleSelfMute()` 消费，见 [§8.5](#85-远端逐人音量--本地静音某人调研-06-4)） | v1（可选） |
| `update:available` | Go→前端 | `UpdateInfo` | 后台检查发现新版本 | v1 |
| `update:progress` | Go→前端 | `{ percent }` | 下载进度 | v1 |
| `launchArgs` | Go→前端 | `string[]` | 第二实例启动参数转交 | v1 |

> **关键约定**：access_token **不通过 Event 主动广播明文**（除 `token:refreshed` 时为方便前端立即 `reauth`）。常规取 token 走 `GetAccessToken` binding（拉模型），保证前端拿到的总是最新有效值。前端**绝不**把 token 存入 localStorage 或写日志。

### 1.4 启动时序

```
应用启动
  │
  ├─ Go: SingleInstanceLock 检查（第二实例→转交参数后退出）        [v1]
  ├─ Go: 清理上次更新残留 .old 文件                                [v1]
  ├─ Go: wails.Run() 起 WebView2；OnStartup 回调：
  │        • 构造 TokenManager（凭 desktop_session_id 刷新）/ ProfileSync（本地缓存）
  │        • go registerPTT()（注册全局热键循环）                   [v1]
  │        • go startTray()（启动托盘 goroutine）                   [v1]
  │        • go backgroundUpdateCheck()（启动后延迟检查更新）       [v1]
  │
  ├─ 前端: onMount → 调 IsLoggedIn()
  │        ├─ 未登录 → 显示登录页 → 用户点击 → Login() binding
  │        └─ 已登录 → 直接进入主界面
  │
  ├─ 前端: GetAccessToken() → 建立 WS 连接 + 发 auth 首帧
  │        并行: GET /api/v1/bootstrap (Bearer)
  │
  └─ 前端: 渲染首屏（频道树/成员/语音快照）
```

### 1.5 Go 侧目录结构

遵循「小而聚焦」，按职责拆分多个包，每文件 < 400 行：

```
client/
├── main.go                      # wails.Run + options（窗口/单实例/HideWindowOnClose）
├── app.go                       # App 结构体（聚合各 service，bindings 入口）
├── wails.json                   # 构建配置（productVersion、NSIS）
├── auth/
│   ├── login.go                 # 系统浏览器 + 回环 handoff 委托登录中介 + /exchange (§2.2)
│   ├── tokenmanager.go          # 凭 desktop_session_id 调登录中介 /refresh 静默刷新 (§2.4)
│   ├── credstore.go             # wincred 存 desktop_session_id (§2.3)
│   └── profilesync.go           # /exchange profile 本地缓存（初始 UI）(§2.5)
├── desktop/
│   ├── hotkey.go                # golang.design/x/hotkey 全局 PTT (调研03 §1.4)
│   ├── hotkey_lowlevel.go       # robotn/gohook 全屏游戏模式 (调研03 §1.5)
│   ├── tray.go                  # energye/systray 托盘 (调研03 §2)
│   ├── autostart.go             # 注册表 Run 键 (调研03 §5)
│   └── singleinstance.go        # OnSecondInstanceLaunch 处理 (调研03 §4)
├── updater/
│   ├── manifest.go              # 拉取/解析 latest.json
│   ├── verify.go                # SHA256 + ed25519 校验 (调研04 §4)
│   └── install.go               # 下载 + 静默安装 (调研04 §5)
└── config/
    └── config.go                # 客户端配置（web_base/api_base/ws_url；不再含 issuer/client_id）
```

---

## 2. Go 后端：Web 中介登录与 token 管理

> 本节描述桌面客户端如何**委托登录中介（[`./web-design.md`](./web-design.md) §5「Web 中介登录（桌面）」）完成登录**，而不再自行对 IdP 跑 OAuth2/PKCE。登录中介现由 **Go 服务端承载（`chat.example.com`，与资源服务器同进程）**。桌面只负责三件原生能力：起 `127.0.0.1` 回环监听、用系统浏览器打开登录中介 `/desktop/login`、把回传的一次性 `handoff_code` 换成 `access_token` + `desktop_session_id`。**IdP issuer/client_id/scope/client_secret 全部移到 Go 服务端登录中介，桌面不再持有任何 IdP 客户端配置**。库版本：`github.com/pkg/browser`（开系统浏览器）、`github.com/danieljoos/wincred`（Windows Credential Manager，本项目仅 Windows）；不再依赖 `golang.org/x/oauth2`、`github.com/coreos/go-oidc/v3`。**本节代码为设计示例，客户端实现暂不纳入。**
>
> **宿主变更仅为 base URL**：与旧设计相比，登录中介端点全部 `example.com` → `chat.example.com`；**端点名/请求参数/响应字段/handoff 校验（`state`、`challenge=S256(handoff_verifier)`）逐字节不变**。因此桌面侧唯一需要改的是登录中介 base URL（`LUMEN_WEB_BASE_URL`）。

### 2.0 身份模型与安全边界（重设计）

- **登录中介是 confidential OIDC client**：`client_secret` 仅存在 Go 服务端（Coolify 加密环境变量 `LUMEN_OAUTH_CLIENT_SECRET`），**绝不下发桌面**。登录中介用 Authorization Code + PKCE 对 IdP 登录，请求 scope `openid profile email offline_access`，并令 access_token 的 `aud` 含 `lumen-api`（= Go 服务端 `LUMEN_OAUTH_AUDIENCE`）。
- **桌面不再内置 IdP issuer/client_id/scope，也不再自行对 IdP 跑 PKCE**：改为「委托登录中介登录」（系统浏览器 + 回环 handoff）。
- **refresh_token 永不落桌面**：存在 Go 服务端 Postgres（用独立密钥 `LUMEN_REFRESH_ENC_KEY` 静态加密）。桌面只持有不透明高熵 `desktop_session_id`（存 Windows 凭据库 DPAPI），`access_token` 仅在内存。
- **Go 服务端资源服务器职责不变**：仍只用 IdP 的 JWKS 本地验 `access_token`（JWT，验 `iss`/`aud`/`exp`）。登录中介（broker）作为**新增**能力落在同一 Go 服务端进程（commit `82f344e`：`internal/broker` + `internal/secure` + store broker 表 + `cmd/lumen-server/main.go`），与资源服务器共享部署。
- **登录中介端点**（**均在 `chat.example.com`**，详见 [`./web-design.md`](./web-design.md) §5）：`GET /desktop/login`、`GET /desktop/callback`、`POST /api/desktop/exchange`、`POST /api/desktop/refresh`、`POST /api/desktop/logout`（桌面用）；`GET /auth/login`、`GET /auth/callback`、`POST /auth/logout`、`GET /api/me`（Web 账户中心用，cookie 鉴权，区别于 Bearer 的 `/api/v1/me`）。服务端 Postgres 存 handoff/session/broker 表（`handoff_code` 一次性消费、TTL≈120s；`desktop_session_id → {refresh_token(加密), sub}`）。**桌面只关心 `/desktop/*` 与 `/api/desktop/*` 五个端点，且它们的线路契约逐字节不变，仅宿主 `example.com` → `chat.example.com`。**

### 2.1 客户端配置来源

桌面客户端**不再需要 issuer/client_id/scopes**（那些已移到 Go 服务端登录中介），只需指向登录中介与 Lumen 服务端的三个 base URL。登录中介现与资源服务器同宿主 `chat.example.com`（不同路径），配置仍为客户端内置默认 + 可选本地配置文件覆盖。

```go
// 文件: config/config.go
package config

// ClientConfig 是不可变配置，binding GetBootstrapConfig 返回给前端。
type ClientConfig struct {
	WebBaseURL string `json:"web_base_url"` // 登录中介，如 https://chat.example.com（含 /desktop/login、/api/desktop/*；现由 Go 服务端承载）
	APIBaseURL string `json:"api_base_url"` // Lumen REST，如 https://chat.example.com/api/v1
	WSURL      string `json:"ws_url"`       // Lumen WS，如 wss://chat.example.com/ws（bootstrap 也会回带，以 bootstrap 为准）
}

// 内置默认（环境变量名见 §9 / web-design §9）：
//   LUMEN_WEB_BASE_URL = https://chat.example.com   // 宿主变更：原 https://example.com → chat.example.com（仅 base URL 变，线路契约不变）
//   LUMEN_API_BASE_URL = https://chat.example.com/api/v1
//   LUMEN_WS_URL       = wss://chat.example.com/ws
```

> 桌面**不做** OIDC discovery、不持有 `client_secret`，也不构造任何 IdP 授权 URL。所有与 IdP 的交互（授权、换 token、刷新、撤销）都由 Go 服务端登录中介用 `client_secret` 在服务端完成。
>
> 官网 `example.com` 现为**纯静态 SPA**（EdgeOne Pages 只托管 `website/dist/`，无 Functions/KV/密钥），不再承载任何登录中介端点；桌面的 `LUMEN_WEB_BASE_URL` 因此指向 `chat.example.com` 而非 `example.com`。

### 2.2 Web 中介登录（系统浏览器 + 回环 handoff）

登录时序（与 [`./web-design.md`](./web-design.md) §5 严格对齐）：

```
1) 桌面起 127.0.0.1:<rand> 回环监听；生成 handoff_verifier(高熵) + state；
   系统浏览器打开 https://chat.example.com/desktop/login
       ?redirect_uri=http://127.0.0.1:<port>/cb
       &state=<state>
       &challenge=S256(handoff_verifier)
2) Go 服务端登录中介完成对 IdP 的 OIDC（Auth Code + PKCE，access_token aud=lumen-api），
   302 回 http://127.0.0.1:<port>/cb?handoff_code=<...>&state=<...>
3) 桌面校验 state → POST https://chat.example.com/api/desktop/exchange
       { handoff_code, handoff_verifier }
   → 得 { access_token, expires_in, desktop_session_id, profile:{display_name, avatar_url} }
4) 桌面把 desktop_session_id 存入 Windows 凭据库（§2.3）；access_token 仅内存。
   随后用 access_token 连 Go 服务端 WS/REST（契约不变）。
```

`access_token` **绝不进任何 URL**（仅出现在 `/exchange`、`/refresh` 的响应体）；`redirect_uri` **仅允许 `127.0.0.1` 回环**。

```go
// 文件: auth/login.go
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/pkg/browser"
)

// ExchangeResult 是登录中介 /api/desktop/exchange 的响应体。
type ExchangeResult struct {
	AccessToken      string  `json:"access_token"`
	ExpiresIn        int     `json:"expires_in"`         // 秒
	DesktopSessionID string  `json:"desktop_session_id"` // 不透明高熵串，存凭据库
	Profile          Profile `json:"profile"`            // {DisplayName, AvatarURL}（仅初始 UI）
}

// Profile 是 /exchange 返回的最小资料（权威资料由 WS auth_ok.user 负责）。
type Profile struct {
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

const loginTimeout = 3 * time.Minute // 登录中介+用户操作的总超时

// Login 走「系统浏览器 + 回环 handoff」委托 Go 服务端登录中介完成登录。
// webBaseURL 形如 https://chat.example.com（登录中介宿主；线路契约不变，仅 base URL 变）。
func Login(ctx context.Context, webBaseURL string) (ExchangeResult, error) {
	// 1) 起 127.0.0.1 回环监听（随机端口）
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("启动本地回环监听失败: %w", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/cb", port)

	// 2) 生成 handoff_verifier(高熵) 与 state；challenge = S256(verifier)
	verifier := randString(64)
	state := randString(32)
	challenge := s256(verifier)

	// 3) 系统浏览器打开登录中介 /desktop/login（绝不内嵌 webview，防钓鱼）
	loginURL := fmt.Sprintf("%s/desktop/login?%s", webBaseURL, url.Values{
		"redirect_uri": {redirectURI},
		"state":        {state},
		"challenge":    {challenge},
	}.Encode())
	if err := browser.OpenURL(loginURL); err != nil {
		return ExchangeResult{}, fmt.Errorf("打开系统浏览器失败: %w", err)
	}

	// 4) 等待登录中介 302 回 /cb?handoff_code=&state=
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/cb", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state { // 严格校验 state 防 CSRF
			http.Error(w, "state 不匹配", http.StatusBadRequest)
			errCh <- fmt.Errorf("state 校验失败")
			return
		}
		// 登录中介 /desktop/callback 失败时 302 回 ?error=<code>&state=（须先于 handoff_code 判断，
		// 否则失败回调因无 handoff_code 而不会触达成功路径、登录将一直挂到 loginTimeout）
		if e := q.Get("error"); e != "" {
			http.Error(w, "登录失败，可关闭此页面重试", http.StatusBadRequest)
			errCh <- fmt.Errorf("登录中介登录失败: %s", e)
			return
		}
		code := q.Get("handoff_code")
		if code == "" {
			http.Error(w, "缺少 handoff_code", http.StatusBadRequest)
			errCh <- fmt.Errorf("回调缺少 handoff_code")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<h3>登录成功，可关闭此页面返回 Lumen。</h3>"))
		codeCh <- code
	})
	srv.Handler = mux
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	var handoffCode string
	select {
	case handoffCode = <-codeCh:
	case err = <-errCh:
		return ExchangeResult{}, err
	case <-ctx.Done():
		return ExchangeResult{}, fmt.Errorf("登录超时或被取消: %w", ctx.Err())
	}

	// 5) 用 handoff_code + handoff_verifier 向登录中介换 token + desktop_session_id
	return exchange(ctx, webBaseURL, handoffCode, verifier)
}

// exchange 调 POST /api/desktop/exchange。
func exchange(ctx context.Context, webBaseURL, handoffCode, verifier string) (ExchangeResult, error) {
	body, _ := json.Marshal(map[string]string{
		"handoff_code":     handoffCode,
		"handoff_verifier": verifier,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		webBaseURL+"/api/desktop/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("换取令牌失败（登录中介不可用？）: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ExchangeResult{}, fmt.Errorf("换取令牌被拒绝（HTTP %d）", resp.StatusCode)
	}
	var out ExchangeResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ExchangeResult{}, fmt.Errorf("解析换取响应失败: %w", err)
	}
	return out, nil
}

// randString 返回 URL-safe 高熵随机串（用于 verifier / state）。
func randString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// s256 = BASE64URL(SHA256(s))，与登录中介校验 bound_challenge 的算法一致。
func s256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
```

binding 封装：

```go
// 文件: app.go（binding 入口节选）
package main

import (
	"context"
	"fmt"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"yourmod/auth"
	"yourmod/config"
)

type App struct {
	ctx     context.Context
	cfg     config.ClientConfig
	tokens  *auth.TokenManager // 见 §2.4
	profile *auth.ProfileSync  // 见 §2.5
	desk    *desktopServices   // 见 §3
	upd     *updater.Service   // 见 §4
}

// LoginResult 返回给前端（不含 token 明文，仅资料 + 成功标志）。
type LoginResult struct {
	Success     bool   `json:"success"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

// Login [v0]：打开登录中介 /desktop/login、起回环、换 token。
// 成功后把 desktop_session_id 存入凭据库，access_token 进内存 TokenManager。
func (a *App) Login(ctx context.Context) (LoginResult, error) {
	res, err := auth.Login(a.ctx, a.cfg.WebBaseURL)
	if err != nil {
		return LoginResult{}, fmt.Errorf("登录失败: %w", err)
	}
	// 1) 持久化 desktop_session_id（高熵不透明串，进凭据管理器；refresh_token 不出 Go 服务端）
	if err := a.tokens.Init(res); err != nil {
		return LoginResult{}, fmt.Errorf("保存登录状态失败: %w", err)
	}
	// 2) /exchange 返回的 profile 直接做初始 UI；权威资料后续由 WS auth_ok.user 补齐
	a.profile.SetInitial(res.Profile)
	return LoginResult{
		Success:     true,
		DisplayName: res.Profile.DisplayName,
		AvatarURL:   res.Profile.AvatarURL,
	}, nil
}

// Logout [v0]：调登录中介 /api/desktop/logout 删服务端会话，再清本地凭据库 + 重置内存 token。
func (a *App) Logout(ctx context.Context) error {
	if err := a.tokens.Logout(a.ctx); err != nil {
		runtime.LogWarningf(a.ctx, "登出登录中介会话失败（仍继续清本地）: %v", err)
	}
	return nil
}
```

### 2.3 凭据存储（仅存 desktop_session_id，仅 Windows）

本项目仅 Windows，用 `github.com/danieljoos/wincred` 封装 Windows Credential Manager（OS 层 DPAPI 静态加密，绑定用户登录态，调研 05 §3.1 方案 B）。与旧设计的关键差异：**不再存 refresh_token**（refresh_token 永不出 Go 服务端、Postgres 加密存储），**只存不透明高熵 `desktop_session_id`**；`access_token` 短命，仅内存持有。

```go
// 文件: auth/credstore.go
package auth

import (
	"fmt"

	"github.com/danieljoos/wincred"
)

const credTarget = "LumenDesktop/session" // 凭据管理器中的 target 名

// SaveSession 写入 desktop_session_id（DPAPI 绑定当前用户）。
func SaveSession(sessionID string) error {
	cred := wincred.NewGenericCredential(credTarget)
	cred.CredentialBlob = []byte(sessionID) // 不透明高熵串，无需额外 JSON 包裹
	cred.Persist = wincred.PersistLocalMachine // 跨登录会话持久；仍存于**当前 Windows 用户**凭据库（其他用户不可见），Persist 仅控生命周期而非可见范围
	if err := cred.Write(); err != nil {
		return fmt.Errorf("写入凭据管理器失败: %w", err)
	}
	return nil
}

// LoadSession 读取；不存在返回 ("", false, nil)。
func LoadSession() (string, bool, error) {
	cred, err := wincred.GetGenericCredential(credTarget)
	if err != nil {
		return "", false, nil // 视为未登录（首次启动）
	}
	return string(cred.CredentialBlob), true, nil
}

// DeleteSession 登出时清除。
func DeleteSession() error {
	cred, err := wincred.GetGenericCredential(credTarget)
	if err != nil {
		return nil // 已不存在
	}
	if err := cred.Delete(); err != nil {
		return fmt.Errorf("删除凭据失败: %w", err)
	}
	return nil
}
```

### 2.4 静默刷新 + 暴露有效 token（核心）

刷新不再走 IdP，而是用 `desktop_session_id` 调登录中介 `POST /api/desktop/refresh`（Go 服务端登录中介用 `client_secret` + Postgres 里加密的 refresh_token 向 IdP 刷新，IdP 轮换则更新 Postgres）。`TokenManager` 集中管理：内存缓存当前 `access_token` 及其到期时刻，临期/缺失时调登录中介刷新；对前端只暴露 `GetAccessToken`（拉模型）。

> **`expires_in` 防护（防刷新风暴）**：`expiresAt = now + max(expiresIn, 0) 秒`。正常按 `expiresAt − 60s` 提前刷新；当响应缺失或 `expiresIn ≤ 0` 时，视为立即过期并刷新一次，但用一个**最小刷新间隔**（如 30s，记录上次刷新时刻）兜底，避免登录中介/IdP 返回 `expires_in=0` 时陷入紧凑循环刷新（与 [web-design §5 `expires_in` 约定](./web-design.md#5-web-中介登录桌面)对齐）。`/api/desktop/refresh` 返回 `401 SESSION_INVALID` → 触发 `auth:expired` 重新登录。

```go
// 文件: auth/tokenmanager.go
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ErrSessionInvalid：登录中介返回 401 / SESSION_INVALID，需重新登录。
var ErrSessionInvalid = errors.New("desktop session 已失效，请重新登录")

// TokenManager 管理内存 access_token，凭 desktop_session_id 向登录中介刷新。线程安全。
type TokenManager struct {
	mu         sync.Mutex
	webBaseURL string
	sessionID  string    // desktop_session_id（也镜像在凭据库）
	access     string    // 当前 access_token，仅内存
	expiresAt  time.Time // access_token 到期时刻
	onRefresh  func(accessToken string) // 刷新回调（用于 EventsEmit token:refreshed）
	onExpired  func()                   // 会话失效回调（用于 EventsEmit auth:expired）
}

func NewTokenManager(webBaseURL string, onRefresh func(string), onExpired func()) *TokenManager {
	return &TokenManager{webBaseURL: webBaseURL, onRefresh: onRefresh, onExpired: onExpired}
}

const refreshSkew = 30 * time.Second // 提前刷新窗口

// Init 用 /exchange 结果初始化：存 session 到凭据库，缓存 access_token。
func (m *TokenManager) Init(res ExchangeResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := SaveSession(res.DesktopSessionID); err != nil {
		return err
	}
	m.sessionID = res.DesktopSessionID
	m.access = res.AccessToken
	m.expiresAt = time.Now().Add(time.Duration(res.ExpiresIn) * time.Second)
	return nil
}

// Restore 启动时从凭据管理器恢复 desktop_session_id（无凭据返回 false）。
// 不立即换 access_token，首次 AccessToken() 会触发刷新。
func (m *TokenManager) Restore(ctx context.Context) (bool, error) {
	sid, ok, err := LoadSession()
	if err != nil || !ok {
		return false, err
	}
	m.mu.Lock()
	m.sessionID = sid
	m.mu.Unlock()
	return true, nil
}

// AccessToken 返回当前有效 access_token（临期或缺失则调登录中介刷新）。binding 入口。
func (m *TokenManager) AccessToken() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessionID == "" {
		return "", fmt.Errorf("尚未登录")
	}
	if m.access != "" && time.Now().Before(m.expiresAt.Add(-refreshSkew)) {
		return m.access, nil
	}
	if err := m.refreshLocked(context.Background()); err != nil {
		return "", err
	}
	return m.access, nil
}

// refreshLocked 调 POST /api/desktop/refresh{desktop_session_id}（调用方须持锁）。
// 登录中介 401/SESSION_INVALID → 触发 onExpired 并返回 ErrSessionInvalid。
func (m *TokenManager) refreshLocked(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"desktop_session_id": m.sessionID})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		m.webBaseURL+"/api/desktop/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("刷新令牌失败（登录中介不可用？）: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized { // session 失效 → 重新登录
		m.access, m.expiresAt = "", time.Time{}
		if m.onExpired != nil {
			m.onExpired() // → EventsEmit("auth:expired")
		}
		return ErrSessionInvalid
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("刷新令牌被拒绝（HTTP %d）", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("解析刷新响应失败: %w", err)
	}
	m.access = out.AccessToken
	m.expiresAt = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	if m.onRefresh != nil {
		m.onRefresh(m.access) // → EventsEmit("token:refreshed")
	}
	return nil
}

// Logout 调登录中介 /api/desktop/logout 删服务端会话，再清本地凭据库与内存。
func (m *TokenManager) Logout(ctx context.Context) error {
	m.mu.Lock()
	sid := m.sessionID
	m.sessionID, m.access, m.expiresAt = "", "", time.Time{}
	m.mu.Unlock()

	_ = DeleteSession() // 先清本地，保证即使登录中介调用失败也回到未登录
	if sid == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{"desktop_session_id": sid})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		m.webBaseURL+"/api/desktop/logout", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("登出登录中介会话失败: %w", err)
	}
	defer resp.Body.Close()
	return nil // 登录中介返回 204
}
```

binding 封装与刷新事件：

```go
// 文件: app.go（节选）
func (a *App) GetAccessToken(ctx context.Context) (string, error) {
	return a.tokens.AccessToken()
}

// onTokenRefreshed：刷新后通知前端发 reauth。
func (a *App) onTokenRefreshed(accessToken string) {
	runtime.EventsEmit(a.ctx, "token:refreshed", map[string]string{"access_token": accessToken})
}

// onSessionExpired：desktop_session 失效，引导重新登录。
func (a *App) onSessionExpired() {
	runtime.EventsEmit(a.ctx, "auth:expired")
}
```

> **与契约的衔接**：刷新对资源服务器透明——`access_token` 仍是同一个 IdP 颁发的 JWT，Go 服务端（资源服务器）照旧用 JWKS 验 `iss`/`aud`/`exp`（契约 §2.6 规则 1）。前端收 `token:refreshed` 后，**在同一 WS 连接发 `reauth`**（不重连）。若 REST 收 `401`，前端调一次 `GetAccessToken`（内部已向登录中介刷新）后重试一次；若登录中介刷新本身返回 `401/SESSION_INVALID` → Go 触发 `auth:expired` 引导重新登录（契约 §2.6 规则 2）。

### 2.5 资料同步（用 /exchange 返回的 profile 做初始 UI）

桌面**不再直接拉 IdP userinfo**。登录时登录中介 `/exchange` 已返回最小 `profile:{display_name, avatar_url}`，桌面仅用它做**登录后即时 UI**；权威资料仍由服务端在 WS `auth`/`reauth` 时从 JWT claims 取并广播 `user_updated` 负责。`ProfileSync` 因此退化为本地缓存。

```go
// 文件: auth/profilesync.go
package auth

import "sync"

type ProfileSync struct {
	mu   sync.Mutex
	last Profile // 本地缓存，用于前端 UI 即时显示（来自 /exchange）
}

func NewProfileSync() *ProfileSync { return &ProfileSync{} }

// SetInitial 由登录流程用 /exchange 返回的 profile 填充初始 UI 资料。
func (s *ProfileSync) SetInitial(p Profile) {
	s.mu.Lock()
	s.last = p
	s.mu.Unlock()
}

// Current 返回本地缓存资料（前端 UI 即时显示用）。
func (s *ProfileSync) Current() Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}
```

> **职责边界（契约 §2.7）**：`/exchange` 的 `profile` 仅用于**本地 UI 即时显示**。**权威资料同步在服务端**：服务端在 WS `auth`/`reauth` 时从 access_token 的 JWT claims 取 `name`/`preferred_username`/`picture`，对比 DB，**有变化才 upsert 并广播 `user_updated`**。因此前端的资料最终以 WS `auth_ok.data.user` 与 `user_updated` 为准。头像直接用 OIDC `picture` URL（由服务端经 claims 下发），**桌面不直接调 IdP userinfo、不做本地存储**。

---

## 3. Go 后端：桌面集成（PTT/托盘/最小化/单实例/自启）

> 全部 `[v1]`。库选型与骨架直接采用调研 03。**CGO 取舍**：`energye/systray`（托盘）与 `robotn/gohook`（全屏游戏低层钩子）需 `CGO_ENABLED=1`（mingw/gcc）；`golang.design/x/hotkey`（默认 PTT）与 `golang.org/x/sys/windows/registry`（自启）无 CGO。本项目接受引入 CGO 以换取托盘与全屏 PTT。

### 3.1 全局 PTT 热键（后台/全屏游戏生效）

**双方案策略**（调研 03 §1）：
- **默认**：`golang.design/x/hotkey` v0.6.1（`RegisterHotKey`，无 CGO，轻量）。Windows 上 goroutine 直接注册（无 macOS 主线程坑）。`Keydown()`/`Keyup()` channel 天然适配 PTT。
- **全屏游戏兼容模式**（设置里开关）：切到 `robotn/gohook` 低层 `WH_KEYBOARD_LL` 钩子，能穿透独占全屏（Discord 同思路）。提示用户全屏不生效时以管理员身份运行。

```go
// 文件: desktop/hotkey.go（方案 A，调研03 §1.4）
package desktop

import (
	"context"
	"log"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.design/x/hotkey"
)

type PTTManager struct {
	ctx    context.Context
	mu     sync.Mutex
	hk     *hotkey.Hotkey
	stopCh chan struct{}
}

func NewPTTManager(ctx context.Context) *PTTManager {
	return &PTTManager{ctx: ctx}
}

// Register 注册 PTT 热键并起按下/松开循环。重绑时先 Unregister。
func (p *PTTManager) Register(mods []hotkey.Modifier, key hotkey.Key) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hk != nil {
		_ = p.hk.Unregister()
		close(p.stopCh)
	}
	p.hk = hotkey.New(mods, key)
	if err := p.hk.Register(); err != nil {
		return err // 同组合键被占用时 RegisterHotKey 失败
	}
	p.stopCh = make(chan struct{})
	go p.loop(p.hk, p.stopCh)
	log.Printf("PTT 热键已注册: %v", p.hk)
	return nil
}

func (p *PTTManager) loop(hk *hotkey.Hotkey, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-hk.Keydown():
			runtime.EventsEmit(p.ctx, "ptt:start") // 前端开门控
		}
		select {
		case <-stop:
			return
		case <-hk.Keyup():
			runtime.EventsEmit(p.ctx, "ptt:stop") // 前端关门控
		}
	}
}
```

全屏游戏模式（方案 B）见调研 03 §1.5（`robotn/gohook` 的 `hook.Register(hook.KeyDown/KeyUp, ...)` → `EventsEmit("ptt:start"/"ptt:stop")`，`hook.Start()` 阻塞放 goroutine）。`SetFullscreenGameMode(on)` binding 在两方案间切换（停掉一个、启另一个）。

> **PTT 与前端的契约**：Go 只负责"键被按下/松开"这一事实，**不知道**当前是否 PTT 模式。前端收到 `ptt:start`/`ptt:stop` 后，**仅在前端处于 PTT 模式时**才据此开关门控（VAD/常开模式忽略，见 [§8.3](#83-pttvad-门控切换状态机)）。

#### PTT 改键闭环（`HotkeySpec` 定义 + 字符串→枚举解析 + 失败回滚）

前端 `settings.pttKey`（[§12.3](#123-本地设置持久化)）变更 → 调 `SetGlobalPTTKey(spec)` binding → 解析为 hotkey 枚举 → 重绑**当前生效方案**。补齐入参类型、映射表、方法体与失败处理：

```go
// 文件: app.go（或 desktop/hotkey.go）
// HotkeySpec：前端 settings.pttKey 的 Go 镜像（json tag 用 snake_case，契约 §7.3）
type HotkeySpec struct {
    Mods []string `json:"mods"` // 如 ["ctrl","shift"]
    Key  string   `json:"key"`  // 如 "space"
}

// 字符串→枚举映射（依据调研 03 §1.3，Windows 语义 Mod1=Alt、Mod4=Win）
var modMap = map[string]hotkey.Modifier{
    "ctrl": hotkey.ModCtrl, "shift": hotkey.ModShift,
    "alt": hotkey.Mod1, "win": hotkey.Mod4,
}
var keyMap = map[string]hotkey.Key{
    "space": hotkey.KeySpace, "a": hotkey.KeyA, /* …KeyA-Z / Key0-9 / KeyF1-F20 / 方向键… */
}

// SetGlobalPTTKey：先保存 spec 为「当前键」（两方案唯一真相源），再对当前生效方案重绑。
// 未知修饰键/未知按键 fail-fast 返回 error（不静默忽略）。
func (a *App) SetGlobalPTTKey(ctx context.Context, spec HotkeySpec) error {
    mods := make([]hotkey.Modifier, 0, len(spec.Mods))
    for _, m := range spec.Mods {
        v, ok := modMap[strings.ToLower(m)]
        if !ok {
            return fmt.Errorf("不支持的修饰键: %s", m)
        }
        mods = append(mods, v)
    }
    k, ok := keyMap[strings.ToLower(spec.Key)]
    if !ok {
        return fmt.Errorf("不支持的按键: %s", spec.Key)
    }
    a.desk.currentPTTKey = spec // 单一真相源：方案 A/B 均从此读取目标键
    if a.desk.fullscreenGameMode {
        return a.desk.ptt.RestartLowLevel(spec) // 方案 B：重启 gohook 钩子以新键重绑
    }
    return a.desk.ptt.Register(mods, k) // 方案 A：内部失败（占用/不支持）冒泡为 error
}
```

**全屏游戏模式下改键的重绑路径（方案 A/B 共享「当前键」单一真相源）**：

1. 引入两方案共享的**当前 PTT 键**配置项（即 `settings.pttKey` / `HotkeySpec` 映射出的 `currentPTTKey`），作为方案 A（`golang.design/x/hotkey`）与方案 B（`robotn/gohook` 低层钩子）的唯一目标键来源；`PTTManager` 与低层钩子封装（`desktop/hotkey_lowlevel.go`）都从它读取，不各自硬编码（把方案 B 骨架里的 `const pttKey = "f13"` 改为从当前键配置读取的参数）。
2. `SetGlobalPTTKey(spec)` = 先保存 spec 为当前键，再对**当前生效方案**重绑：`fullscreenGameMode=off` → `PTTManager.Register(mods, key)`（方案 A）；`fullscreenGameMode=on` → 重启 gohook 钩子并以新键重绑——先停止旧钩子（`hook.End()` 结束当前 `hook.Process` 循环并退出其 goroutine），再用新键 `hook.Register(hook.KeyDown/KeyUp, []string{spec.key}, ...)` + `hook.Start()` 后重起阻塞 goroutine。
3. `SetFullscreenGameMode(on)` 的「两方案切换（停一个、启另一个）」同样读同一**当前键**启动新生效方案（`off→on` 启 gohook 用当前键；`on→off` 启 `PTTManager.Register` 用当前键），确保切换后立即以当前键生效。
4. 在 `desktop/hotkey_lowlevel.go` 封装中补出与 `PTTManager` 对称的生命周期方法（`Register`/`Unregister`/`Restart`），使方案 B 具备可重绑能力；[§1.5](#15-go-侧目录结构) 的 `hotkey_lowlevel.go` 即承担此对称重绑接口。

> **改键失败处理**：`SetGlobalPTTKey` 返回 error（不支持键/组合键被占用）时，前端 `toast.error("该快捷键无效或已被占用，请重设")` 并把 `settings.pttKey` 回滚到改动前值（不写 localStorage），见 [§11.1](#111-错误分类与提示策略) 改键失败行与 [§12.3](#123-本地设置持久化) 副作用说明。`pttKey` 与 `fullscreenGameMode` 的变更都最终驱动「当前生效方案以当前键重绑」，二者经同一当前键配置协调，避免改键只作用于未生效方案。

### 3.2 系统托盘 + 菜单

`energye/systray`（去 GTK 依赖的 fork，调研 03 §2.1），**goroutine 法**：在 `wails.Run` **之前** `go systray.Run(onReady, onExit)`，避免与 Wails 抢主线程（调研 03 §2.2）。

```go
// 文件: desktop/tray.go（调研03 §2.4）
package desktop

import (
	"context"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type Tray struct {
	ctx     context.Context
	iconICO []byte                 // //go:embed 的 .ico
	onQuit  func()
	mMute   *systray.MenuItem      // 提升为字段，供 SetMuteChecked 回写勾选态
}

func (t *Tray) Start() { go systray.Run(t.onReady, func() {}) }

// SetMuteChecked 由 binding SetTrayMuteChecked 驱动：以前端 voice store 的自身 muted 为单一事实源，
// 回写托盘勾选态，消除托盘 checkbox 与实际静音态漂移（desktop-2）。
func (t *Tray) SetMuteChecked(muted bool) {
	if t.mMute == nil {
		return
	}
	if muted {
		t.mMute.Check()
	} else {
		t.mMute.Uncheck()
	}
}

func (t *Tray) onReady() {
	systray.SetIcon(t.iconICO)
	systray.SetTitle("Lumen")
	systray.SetTooltip("Lumen")

	mShow := systray.AddMenuItem("显示主窗口", "")
	t.mMute = systray.AddMenuItemCheckbox("麦克风静音", "", false) // 可选：托盘快捷静音；勾选态由 SetMuteChecked 回写
	mMute := t.mMute
	mQuit := systray.AddMenuItem("退出 Lumen", "")

	systray.SetOnClick(func(_ systray.IMenu) { runtime.WindowShow(t.ctx) }) // 左键单击恢复

	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				runtime.WindowShow(t.ctx)
				runtime.EventsEmit(t.ctx, "tray:show")
			case <-mMute.ClickedCh:
				// 经 Event 通知前端切换静音；前端回写 mute_state（见 §8）
				runtime.EventsEmit(t.ctx, "tray:toggle_mute")
			case <-mQuit.ClickedCh:
				systray.Quit()
				if t.onQuit != nil {
					t.onQuit() // 真正退出（绕过 HideWindowOnClose）
				}
				runtime.Quit(t.ctx)
				return
			}
		}
	}()
}
```

### 3.3 最小化隐藏到托盘（关窗口不退出）

采用调研 03 §3 **方案 A**：`options.App{ HideWindowOnClose: true }`，点关闭按钮 → 隐藏到托盘而非退出；托盘"显示主窗口"用 `runtime.WindowShow` 还原。注意 `HideWindowOnClose` 与 `OnBeforeClose` 互斥（调研 03 §3.2），本项目选前者。真正退出只经托盘"退出"菜单（调 `runtime.Quit`）。

### 3.4 单实例锁 + 第二实例参数转交

`options.SingleInstanceLock` + 固定 UUID（调研 03 §4）。第二实例不会自动拉前台，需手动 `WindowUnminimise` + `Show`，并 `EventsEmit("launchArgs")` 转交参数。

```go
// 文件: desktop/singleinstance.go（调研03 §4.2）
package desktop

import (
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (s *Single) OnSecondInstanceLaunch(data options.SecondInstanceData) {
	if runtime.WindowIsMinimised(s.ctx) {
		runtime.WindowUnminimise(s.ctx)
	}
	runtime.Show(s.ctx)
	runtime.WindowShow(s.ctx)
	go runtime.EventsEmit(s.ctx, "launchArgs", data.Args)
}
```

`main.go` 装配（含 §3.3 的 HideWindowOnClose 与单实例）：

```go
// 文件: main.go（节选）
func main() {
	app := NewApp()
	_ = wails.Run(&options.App{
		Title:             "Lumen",
		Width:             1100,
		Height:            720,
		MinWidth:          900,
		MinHeight:         600,
		HideWindowOnClose: true, // §3.3：关闭=隐藏到托盘
		OnStartup:         app.startup,
		OnShutdown:        app.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "e3984e08-28dc-4e3d-b70a-45e961589cdc", // 固定 UUID
			OnSecondInstanceLaunch: app.desk.single.OnSecondInstanceLaunch,
		},
		Bind: []interface{}{app},
	})
}
```

### 3.5 开机自启（注册表 HKCU Run）

`golang.org/x/sys/windows/registry` 写 `HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\Run`（无需管理员、无 CGO，调研 03 §5）。值名=应用名，值数据=`os.Executable()` 绝对路径（含空格时 `strconv.Quote`）。骨架见调研 03 §5.3（`SetAutoStart`/`IsAutoStartEnabled`）。binding `SetAutoStart(enable)` / `IsAutoStartEnabled()` 暴露给设置页，**必须给用户清晰开关**（Run 键也是恶意软件持久化手段，合规要求）。

---

## 4. Go 后端：自动更新

> `[v1]`。Wails v2 无官方内置更新（调研 04 §1），采用 **路线 A：NSIS 安装包 + 自托管 JSON manifest**（最贴合现有 NSIS + WebView2 + Coolify 架构，调研 04 §6）。校验顺序：**SHA256（完整性）→ ed25519 签名（真实性）→ 才执行安装**，任一失败立即中止（不静默吞错）。

### 4.1 方案与流程

```
启动后延迟 N 秒 / 用户点"检查更新"
  │
  ├─ GET https://chat.example.com/updates/latest.json  （服务端 Go 进程 /updates/ 静态路由，HTTPS 经 Traefik，与主域同源复用证书）
  ├─ semver 比较 manifest.version 与内置 main.Version
  │     └─ 无更新 → 结束（或前端提示"已是最新"）
  ├─ 有更新 → EventsEmit("update:available", info) → 前端弹"发现 v1.4.2"对话框
  │
  └─ 用户确认 → DownloadAndInstallUpdate():
        ├─ 下载 platforms["windows/amd64"].url 的 NSIS 安装包（边下边算 SHA256）
        │     └─ EventsEmit("update:progress", {percent})
        ├─ 校验 SHA256 == manifest.sha256          （不匹配 → 中止）
        ├─ 校验 ed25519.Verify(pubkey, bin, sig)   （失败 → 中止）
        ├─ exec 安装包 "/S"（静默；安装包内 taskkill /F /IM Lumen.exe /T 杀旧进程+覆盖+重启）
        └─ os.Exit(0)（让安装包接管）
```

### 4.2 manifest 与校验

manifest JSON 结构、`CheckForUpdate`、`DownloadAndInstall` 代码骨架直接采用调研 04 §4/§5（`updater` 包：`fetchManifest` → `semver.Compare` → 下载 `io.MultiWriter(tmp, sha256)` → `ed25519.Verify` → `exec.Command(tmp, "/S")` + `detachAttr()` + `os.Exit(0)`）。要点：

- **公钥硬编码**进客户端（`updatePublicKey ed25519.PublicKey`），私钥离线保管（调研 04 §4 安全红线）。
- 版本号注入：`wails build -ldflags "-X main.Version=1.4.2"`，与 NSIS `productVersion` 一致。
- `detachAttr()`（`*_windows.go`）：`CreationFlags: DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`，防安装包随主进程退出被杀。
- 免 UAC：安装到 `%LOCALAPPDATA%\Lumen`（NSIS `InstallDir "$LOCALAPPDATA\Lumen"`），静默更新无需提权（调研 04 §6）。

### 4.3 与 Coolify 托管更新文件的衔接

- **唯一托管方案**：在现有 Go 服务端进程内加 `http.FileServer` 暴露 `/updates/` 静态路由（仅 GET、公开下载、无需鉴权），由 Coolify Persistent Storage 挂载 `updates/` 卷（如 `/app/updates`）。**不引入独立 Nginx/Caddy 容器、不使用独立域名**——更新文件与主域 `chat.example.com` 同源，复用 Traefik 证书，避免域名/证书归属歧义（服务端落地见 server-design §7「自动更新文件托管」）。托管 3 类文件：`latest.json` + `Lumen-Setup-x.y.z.exe` + 签名（内嵌 json 的 `signature` 字段）。
- 对外地址统一为 `https://chat.example.com/updates/latest.json`（与 §4.1 一致）；manifest 内 `platforms["windows/amd64"].url` 同步指向 `https://chat.example.com/updates/Lumen-Setup-x.y.z.exe`。
- HTTPS 由 Coolify Traefik 自动签发证书；`latest.json` 设 `Cache-Control: no-cache` + ETag 避免读到旧版，`*.exe`（文件名含版本号、不可变）设长缓存。
- 发布流水线（调研 04 §5）：bump semver → `wails build -nsis -ldflags "-X main.Version=$VER"` → `sha256sum` → 离线 ed25519 签名 → 生成 `latest.json` → 上传到服务端 `/app/updates` 卷。

### 4.4 NSIS 改动

`build/windows/installer/project.nsi` 安装前杀进程树（调研 04 §5）：

```nsis
Section "Install"
  nsExec::Exec 'taskkill /F /IM "Lumen.exe" /T'   ; 杀旧进程含 WebView2 子进程树
  Sleep 800
  SetOutPath "$INSTDIR"
  File "Lumen.exe"
  ; ... 快捷方式 / 卸载项 ...
  ${If} ${Silent}
    Exec '"$INSTDIR\Lumen.exe"'  ; 静默安装后自动重启
  ${EndIf}
SectionEnd
```

### 4.5 启动时清理残留

每次启动先清理上一次自替换的 `.old` 残留（若未来叠加路线 B 热替换通道则需要；纯路线 A 由安装包覆盖，无 `.old`）。本项目主用路线 A，此步为防御性（调研 04 §3）。

---

## 5. 前端结构（Svelte）

### 5.1 目录结构

```
frontend/
├── src/
│   ├── main.ts                      # 挂载根组件
│   ├── App.svelte                   # 路由壳：登录页 / 主界面
│   ├── lib/
│   │   ├── api/
│   │   │   ├── rest.ts              # REST 客户端（fetch + Bearer + 401 重试）[v0]
│   │   │   ├── ws.ts               # WS 客户端（连接/重连/信封编解码）[v0]
│   │   │   └── types.ts            # User/Channel/Message/VoiceState/MessagesPage/BootstrapData TS 类型（镜像契约 §3.4/§3.5）
│   │   ├── bridge/
│   │   │   └── wails.ts            # 封装 binding 调用 + EventsOn 订阅（ptt/token/tray/update）
│   │   ├── voice/
│   │   │   ├── pipeline.ts         # getUserMedia→RNNoise→PC 上行管线 [v0]
│   │   │   ├── denoise.ts          # RNNoise AudioWorklet 接入 [v1]
│   │   │   ├── speaking.ts         # AnalyserNode RMS 说话检测 [v0]
│   │   │   ├── gate.ts             # PTT/VAD 门控状态机 [v1]
│   │   │   ├── remote.ts           # 远端逐人 GainNode 音量/本地静音 [v1]
│   │   │   ├── devices.ts          # 设备枚举/切换 + 麦克风测试 [v1]
│   │   │   └── peer.ts             # RTCPeerConnection + 重协商 + 重连 [v0]
│   │   ├── stores/
│   │   │   ├── connection.ts       # WS/PC 连接状态 [v0]
│   │   │   ├── auth.ts             # 当前用户/登录态 [v0]
│   │   │   ├── channels.ts         # 频道列表 [v0]
│   │   │   ├── members.ts          # 成员列表 [v0]
│   │   │   ├── voice.ts            # 语音房在线成员/说话/静音 [v0]
│   │   │   ├── messages.ts         # 各频道消息+分页游标 [v0]
│   │   │   ├── ui.ts               # UI 选择态（当前选中文字频道 selectedTextChannelId）[v0]
│   │   │   └── settings.ts         # 本地设置（主题/设备/PTT 键/模式）[v1]
│   │   └── ui/
│   │       └── toast.ts            # 错误/通知提示 [v0]
│   ├── components/
│   │   ├── LoginView.svelte         # 登录页 [v0]
│   │   ├── Sidebar.svelte           # 频道侧栏 [v0]
│   │   ├── ChannelList.svelte       # 文字/语音频道树 [v0]
│   │   ├── TextChannel.svelte       # 文字区（历史+输入）[v0]
│   │   ├── MessageList.svelte       # 消息列表（分页加载更多）[v0]
│   │   ├── VoicePanel.svelte        # 语音成员区（头像/说话高亮/静音图标）[v0]
│   │   ├── MemberItem.svelte        # 单个成员（含逐人音量滑块）[v1]
│   │   ├── ControlBar.svelte        # 自静音/扬声静音/离开/设置入口 [v0]
│   │   └── settings/
│   │       ├── SettingsModal.svelte # 设置弹窗壳 [v1]
│   │       ├── AudioSettings.svelte # 设备选择/麦克风测试/降噪开关 [v1]
│   │       ├── VoiceMode.svelte     # PTT/VAD 切换 + PTT 键绑定 [v1]
│   │       └── AppSettings.svelte   # 主题/自启/全屏游戏模式/检查更新 [v1]
│   └── styles/
│       └── theme.css                # 深色主题（默认）变量 [v0]
├── wailsjs/                          # Wails 自动生成（go bindings + runtime）
└── package.json
```

### 5.2 技术栈与依赖

| 依赖 | 用途 | 版本 | 归属 |
|------|------|------|------|
| Svelte + Vite | UI 框架 + 打包（Wails v2 默认前端模板） | Svelte 4/5 | v0 |
| `@timephy/rnnoise-wasm` | RNNoise drop-in AudioWorkletNode（自带 worklet + polyfill，调研 06 §0/§1.2） | 0.2 fork | v1 |
| 原生 `WebSocket` / `RTCPeerConnection` / Web Audio | 浏览器内置，无需库 | — | v0 |
| `wailsjs/runtime` | `EventsOn`/`EventsEmit`/`WindowShow` 等（自动生成） | 随 Wails | v0 |

> Vite 取 worklet URL 用 `?worker&url`（调研 06 §1.2/§8）。WASM 须以 `application/wasm` MIME 提供（Wails 打包内嵌资源默认正确）。

### 5.3 路由与页面切换

无外部路由库；`App.svelte` 依据 `auth` store 的登录态在 `LoginView` 与主界面间切换：

```svelte
<!-- App.svelte（结构示意） -->
<script lang="ts">
  import { onMount } from "svelte";
  import { auth } from "$lib/stores/auth";
  import { bootstrap } from "$lib/bridge/wails";
  import LoginView from "./components/LoginView.svelte";
  import MainLayout from "./components/MainLayout.svelte";

  onMount(async () => {
    if (await bootstrap.isLoggedIn()) {
      await auth.initSession(); // 取 token → 建 WS → REST bootstrap
    }
  });
</script>

{#if $auth.loggedIn}
  <MainLayout />
{:else}
  <LoginView />
{/if}
```

---

## 6. 前端 stores 与状态模型

所有跨组件状态用 Svelte store（`writable`/`derived`），UI 响应式更新（调研 06 §8）。**遵循不可变更新**：每次产生新对象，不原地 mutate。

### 6.1 stores 一览

| store | 形状（TS） | 数据来源 | 归属 |
|-------|-----------|---------|------|
| `connection` | `{ wsState: 'connecting'|'open'|'reauth'⁽ᵛ¹⁾|'closed', pcState: RTCPeerConnectionState, retrying: bool }`（`reauth` 子态为 [v1]） | WS 客户端 + PC 事件 | v0 |
| `auth` | `{ loggedIn: bool, me: User \| null }` | binding `Login`/`IsLoggedIn` + WS `auth_ok.user` + `user_updated`⁽ᵛ¹⁾ | v0 |
| `channels` | `Channel[]`（按 `(position, id)` 升序，升序不变量由 [§6.2](#62-关键状态更新规则) channels 更新规则在 WS 增量路径上维持） | REST `bootstrap.channels` + WS `channel_created/updated/deleted` | v0 |
| `members` | `Map<userId, User>` | REST `bootstrap.members` + WS `user_updated`⁽ᵛ¹⁾ | v0 |
| `voice` | `{ currentChannelId: string\|null, byChannel: Map<channelId, Map<userId, VoiceState>> }` | `bootstrap.voice_states` + WS `user_joined/left/speaking_state/mute_state` | v0 |
| `messages` | `Map<channelId, { items: Message[], hasMore: bool, nextBefore: string\|null, loading: bool }>` | REST `messages` 分页 + WS `message` | v0 |
| `ui` | `{ selectedTextChannelId: string\|null }`（当前选中文字频道；选频道时更新，`channel_deleted` 命中时按 [§6.2](#62-关键状态更新规则)「频道删除收敛」(c) 切换/置空） | 用户选频道 + WS `channel_deleted` | v0 |
| `settings` | `LocalSettings`（见 [§12.3](#123-本地设置持久化)） | localStorage | v1 |

> ⁽ᵛ¹⁾ `user_updated` 为 [v1] 增量同步来源；**[v0]** 下 `auth`/`members` store 仅由 `auth_ok.user` / `bootstrap.members` 初始化，**不实现 `user_updated` 消费分支**（资料双向同步整体推迟 v1，与 [§7.2](#72-ws-客户端) 路由表、附录速查表、契约 §4.2/§4.5 对齐）。

### 6.2 关键状态更新规则

- **自身资料**：`auth.me` 取 WS `auth_ok.data.user` 与 REST `bootstrap.me` 中**先到者**初始化（二者均由服务端按已验签 claims 幂等 upsert 得到，契约 §2.3/§2.5/§3.4，故新用户首登无论哪条先到都有值），其后 `user_updated`（若 `user.id == me.id`）更新。两者都未到达前 UI 显示 loading，**不直接取 `me.id`**（防空引用）。
- **成员资料同步**（[v1]）：收 `user_updated` → **健壮 upsert**：若 `members` 中存在该 `user.id` 则不可变更新对应项，不存在则不可变**插入**（insert-if-absent，容忍任意顺序到达的资料更新，不丢弃未知 `user_id`）；若 `user.id == auth.me.id` 同步更新 `auth.me`。**语音区**经 voice store 关联 `members` 渲染、随之刷新。**消息区**：`MessageList` 渲染作者头像/昵称时一律以 `members` store 按 `message.author_id` 查活值为准（`members.get(message.author_id)`），内联 `Message.author` 仅作 `members` 未命中时的**回退/初始 seed**；收到 WS `message` 与 REST 历史分页时，应把其内联 `author` 快照 **upsert 进 `members`**（若该 `user_id` 不存在或更旧）。因此 `user_updated` 更新 `members` 后，消息区随之自动刷新头像/昵称，**无需改写已落地的 `messages.items`**（契约 §4.4/§2.7）。
- **语音状态**：`user_joined` → 往 `byChannel[channel_id]` 插入 `voice_state` + 缓存 `user` 快照到 `members`；`user_left` → 移除；`speaking_state`/`mute_state` → 仅更新对应 `VoiceState` 的 `speaking`/`muted`/`deafened` 字段（契约 §4.4）。
- **频道列表**（不可变插入/替换后按 `(position, id)` 重排，保证 bootstrap 与 WS 增量两路产出同一确定顺序）：
  - `channel_created` → 不可变插入新 `Channel` 后，对整列表按 `(position 升序, id 升序)` 稳定重排再写回：`$channels = [...$channels, ch].sort((a,b) => a.position - b.position || (a.id < b.id ? -1 : 1))`。
  - `channel_updated` → 以 `id` 定位并不可变**替换**对应项（替换而非合并，服务端回的是完整 `Channel`），随后同样按 `(position, id)` 升序重排（`position` 可能被 PATCH 改动需重新定位）。
  - `channel_deleted` → 见下方「频道删除收敛」。
  - 排序键与 tie-break `(position, id)` 与契约 §3.3/§5.2 索引对齐，消除跨客户端因到达顺序导致的不一致。
- **频道删除收敛**（收到 WS `channel_deleted{channel_id}`）：
  - (a) 从 `channels` store 不可变移除该 `channel_id` 条目（保持原有序，无需重排）；
  - (b) 从 `messages` store 删除该 `channel_id` 的整个桶（`Map.delete(channel_id)`）；
  - (c) 若被删频道 == 当前选中文字频道（`ui` store 的 `selectedTextChannelId`，见 [§6.1](#61-stores-一览)/[§5.3](#53-路由与页面切换)）：切到首个 `type=text` 频道（按 `position` 升序取首个），无可切则进入空态提示「频道已被删除」；
  - (d) 若被删频道 == `voice.currentChannelId`：等价**本地 `leaveVoice`**（复用 [§9.3](#93-加入离开语音频道) 的 `teardownVoice()`：`peer.destroy()` / `pipeline.ctx.close()` / `detector.destroy()` / 停麦克风 track / destroy 远端句柄），并 `voice.setCurrentChannel(null)`，**无需再发 `leave_channel`**（频道已不存在），toast「频道已被删除」。
  - 与 REST `NOT_FOUND`（被动 404）的区别：`channel_deleted` 为服务端主动广播；二者最终都收敛到「移除 + 导航/拆链」。
- **消息**：历史分页 `prepend` 到 `items` 顶部（契约 §3.4 返回升序，便于 append 到顶部）；WS `message` 实时 `append` 到底部；按 `channel_id` 分桶。
  - **未初始化桶策略**：收到 WS `message` 时若 `messages` Map 无对应 `channel_id` key：v0 直接**惰性建桶并 append**（桶 `hasMore=true`、`nextBefore=null`、`loading=false`，表示该频道尚未拉过历史首页）；用户首次进入该频道时仍照常 `GET /messages?limit=50`。
  - **WS 与历史首页去重/拼接**：执行 `GET /messages` 首页后，将返回的升序历史与桶内已有实时条目按 `message.id`（ULID 单调递增）做**并集去重并整体重排为升序**，避免 WS 早到条目与历史首页之间出现重复或空洞。
  - **分页响应到 store 的映射**（wire→store 解嵌套 + 改名，见 [§7.1](#71-rest-客户端) `MessagesPage` 定义）：`data.messages` → prepend 到 `items` 顶部；`data.meta.next_before` → `nextBefore`；`data.meta.has_more` → `hasMore`（wire 为嵌套 snake_case `meta`，store 为扁平 camelCase；解嵌套+改名在此一步完成）。下一页请求把 `nextBefore` 传给 `api.messages` 的 `before` 参数；`nextBefore === null` 时禁用「加载更多」。
- **进程重启语义**：重连后 `bootstrap.voice_states` 可能为空（内存态清空，契约 §5.4），`voice.currentChannelId` 重置为 null，需用户重新 `join_channel`；此外 `channel_deleted` 命中 `currentChannelId` 时亦按上方「频道删除收敛」(d) 复位。

### 6.3 消息作者渲染解析约定（资料同步一致性）

`MessageList`/消息行组件 **不直接读 `message.author` 渲染头像与昵称**，而是按 `members` store 的活值解析：

```ts
// 渲染作者头像/昵称：以 members 活值为准，内联 author 仅作未命中回退
const author = derived(members, ($m) => $m.get(message.author_id) ?? message.author);
```

这样 `user_updated` 更新 `members` 后，文字区历史消息（含本次会话刚收的实时消息）随之实时刷新头像/昵称，无需改写已落地的 `messages.items`（闭合 sync-1：内联快照与 `members` 脱钩导致的头像/昵称不刷新）。

### 6.4 登出重置规则

登出（[§7.4](#74-登出时序)）时对所有 store 做**不可变重置**：`auth = { loggedIn: false, me: null }`，`voice`/`messages`/`channels`/`members`/`connection`/`ui` 全部清空为初始值。`auth:expired`（[§11.1](#111-错误分类与提示策略)）复用同一套重置流程，确保两条回登录路径行为一致。

---

## 7. 前端交互层：REST 客户端与 WS 客户端

### 7.1 REST 客户端

封装 `fetch`：自动加 `Authorization: Bearer`（token 来自 `GetAccessToken` binding，保证最新有效），统一解析响应信封（契约 §3.2），`401` 触发一次刷新+重试（契约 §2.6 规则 2）。

```ts
// lib/api/rest.ts [v0]
import { GetAccessToken } from "../../wailsjs/go/main/App";
import { EventsEmit } from "../../wailsjs/runtime/runtime";

const BASE = ""; // 运行期由 GetBootstrapConfig 注入 api_base_url

type Envelope<T> = { success: boolean; data: T | null; error: ApiError | null };
export type ApiError = { code: string; message: string; details?: unknown };

async function request<T>(method: string, path: string, body?: unknown, retried = false): Promise<T> {
  const token = await GetAccessToken();
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: {
      "Authorization": `Bearer ${token}`,
      "Content-Type": "application/json; charset=utf-8",
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  // 401 → 刷新（GetAccessToken 内部已刷新）后重试一次（契约 §2.6 规则 2）
  if (res.status === 401 && !retried) {
    return request<T>(method, path, body, true);
  }

  const env = (await res.json()) as Envelope<T>;
  if (!env.success || env.error) {
    if (res.status === 401) {
      // 重试后仍 401 → 引导重新登录
      EventsEmit("auth:expired");
    }
    throw new ApiException(env.error ?? { code: "INTERNAL", message: "未知错误" }, res.status);
  }
  return env.data as T;
}

export class ApiException extends Error {
  constructor(public apiError: ApiError, public httpStatus: number) {
    super(apiError.message);
  }
}

// 端点封装（路径严格对齐契约 §3.3）
export const api = {
  bootstrap: () => request<BootstrapData>("GET", "/api/v1/bootstrap"),                                  // [v0]
  me: () => request<User>("GET", "/api/v1/me"),                                                          // [v0]
  channels: (type?: "text" | "voice") =>
    request<Channel[]>("GET", `/api/v1/channels${type ? `?type=${type}` : ""}`),                        // [v0]
  messages: (channelId: string, before?: string, limit = 50) =>
    request<MessagesPage>("GET",
      `/api/v1/channels/${channelId}/messages?limit=${limit}${before ? `&before=${before}` : ""}`),     // [v0]
  members: () => request<User[]>("GET", "/api/v1/members"),                                              // [v0]
  // owner 端点 [v1]
  createChannel: (b: { name: string; type: "text" | "voice"; position?: number }) =>
    request<Channel>("POST", "/api/v1/channels", b),
  updateChannel: (id: string, b: { name?: string; position?: number }) =>
    request<Channel>("PATCH", `/api/v1/channels/${id}`, b),
  deleteChannel: (id: string) => request<null>("DELETE", `/api/v1/channels/${id}`),
  kick: (userId: string, cooldownSeconds?: number) =>
    request<null>("POST", `/api/v1/members/${userId}/kick`,
      cooldownSeconds === undefined ? {} : { cooldown_seconds: cooldownSeconds }), // 省略=服务端默认3600；显式0=仅踢出不封禁
};
```

> TS 类型 `User`/`Channel`/`Message`/`VoiceState`/`BootstrapData`/`MessagesPage` 镜像契约 §3.4/§3.5，字段一律 `snake_case`（契约 §7.3）。**客户端容忍未知字段**（契约 §7.1）。

`MessagesPage` 与契约 §3.4 分页响应（嵌套 `meta`、snake_case）严格对齐，**线类型保持 snake_case，不在线类型上改名**；解嵌套+改 camelCase 在写入 `messages` store 时一步完成（见 [§6.2](#62-关键状态更新规则)「分页响应到 store 的映射」）：

```ts
// lib/api/types.ts（wire 线类型，镜像契约 §3.4）
export type MessagesPage = {
  messages: Message[];
  meta: { limit: number; has_more: boolean; next_before: string | null };
};
```

### 7.2 WS 客户端

封装 `WebSocket`：统一信封编解码（契约 §4.1）、首帧 `auth`、`reauth`（不重连）、按 `type` 分发、`id` 关联请求/响应、指数退避重连（调研 06 §7.2）。

```ts
// lib/api/ws.ts [v0]
import { GetAccessToken } from "../../wailsjs/go/main/App";
import { EventsEmit } from "../../wailsjs/runtime/runtime";

type Envelope = { type: string; data?: any; id?: string };
type Handler = (data: any, id?: string) => void;

export class WSClient {
  private ws: WebSocket | null = null;
  private url = "";
  private backoff = 500;
  private handlers = new Map<string, Set<Handler>>();
  private seq = 0;
  private authed = false;
  private intentionalClose = false;
  // 终态鉴权失败熔断：收到 auth_error{KICKED|TOKEN_INVALID} 时置 true，阻止 onclose 退避重连死循环。
  // HANDSHAKE_TIMEOUT 为瞬态，仍允许重连。connect() 重置为 false。
  private suppressReconnect = false;

  connect(url: string) {
    this.url = url;
    this.intentionalClose = false;
    this.suppressReconnect = false; // 用户主动连接时清除熔断
    this.open();
  }

  private open() {
    const ws = new WebSocket(this.url);
    this.ws = ws;
    ws.onopen = async () => {
      this.backoff = 500;
      try {
        // 首帧鉴权（契约 §2.4：token 仅在首帧消息体，不放 URL）
        const token = await GetAccessToken();
        this.send("auth", { access_token: token });
      } catch (e) {
        // desktop_session_id 为空（已登出）或登录中介刷新失败：不应触发重连，主动关闭并引导重新登录
        console.warn("WS onopen 取 token 失败:", e);
        this.suppressReconnect = true;
        this.intentionalClose = true;
        ws.close();
        EventsEmit("auth:expired");
      }
    };
    ws.onmessage = (e) => this.dispatch(JSON.parse(e.data) as Envelope);
    ws.onclose = () => {
      this.authed = false;
      // 非主动关闭 且 非终态鉴权失败 才退避重连（auth-1/kick-1/kick-2）
      if (!this.intentionalClose && !this.suppressReconnect) {
        setTimeout(() => this.open(), (this.backoff = Math.min(this.backoff * 2, 10000)));
      }
    };
    ws.onerror = () => ws.close();
  }

  // token 刷新后在同一连接更新会话（契约 §2.6 规则 1）；无需重连。
  reauth(accessToken: string) {
    this.send("reauth", { access_token: accessToken });
  }

  send(type: string, data?: any, id?: string): string {
    const env: Envelope = { type, data };
    if (id) env.id = id;
    this.ws?.send(JSON.stringify(env));
    return id ?? "";
  }

  // 带关联 id 的请求（如 webrtc_answer 须回带 offer 的 id）
  nextId(): string { return `c-${(++this.seq).toString(36)}`; }

  on(type: string, h: Handler) {
    if (!this.handlers.has(type)) this.handlers.set(type, new Set());
    this.handlers.get(type)!.add(h);
    return () => this.handlers.get(type)?.delete(h);
  }

  private dispatch(env: Envelope) {
    if (env.type === "auth_ok") this.authed = true;
    // 终态鉴权失败先行熔断（在转发给 handler 之前置位，确保紧随的 onclose 看到标志）
    if (env.type === "auth_error") {
      const code = env.data?.code;
      if (code === "KICKED" || code === "TOKEN_INVALID") {
        this.suppressReconnect = true; // 停止 onclose 的退避重连
      }
      // KICKED 为「冷却态(可恢复)」：到期后自动重连重发 auth（不引导重新登录）
      if (code === "KICKED" && typeof env.data?.retry_after === "number") {
        const ms = Math.max(0, env.data.retry_after * 1000);
        setTimeout(() => {
          this.suppressReconnect = false; // 解除熔断
          this.open();                    // 复用 §10.1 退避路径重连 + 重发 auth 首帧
        }, ms);
      }
    }
    this.handlers.get(env.type)?.forEach((h) => h(env.data, env.id));
  }

  close() { this.intentionalClose = true; this.suppressReconnect = true; this.ws?.close(); }
}
```

WS 消息处理路由（在 `auth` store / 各 store 注册），**类型名严格对齐契约 §4.2**：

| WS type | 处理 | store | 版本 |
|---------|------|-------|------|
| `auth_ok` | 设 `auth.me`、`connection.wsState='open'`；若 `data.reauth` 仅刷新会话 | auth/connection | v0 |
| `auth_error` | 据 `code` 处理（连接将被服务端关闭）：`TOKEN_INVALID`=**终态，抑制重连**（`suppressReconnect=true`）→ 关 WS + 重置 store + 回登录页（复用 [§7.4](#74-登出时序) 流程）；`KICKED`=**冷却态(可恢复)，抑制立即重连**，显示「约 N 分钟后可重新进入」（N 由 `data.retry_after` 推导），到期后自动按 [§10.1](#101-分层恢复调研-06-7) 退避重连重发 `auth`，**不**引导重新登录；`HANDSHAKE_TIMEOUT`=**瞬态**，允许重连 | ui/connection | v0 |
| `error` | 据 `code` 提示；`ref` 关联请求。当 `data.ref === join_channel 请求 id` 时执行**语音加入回滚**（拆链 + `voice.setCurrentChannel(null)` + toast，见 [§9.3](#93-加入离开语音频道)） | ui/voice | v0 |
| `message` | append 到 `messages[channel_id]`（无桶则惰性建桶，见 [§6.2](#62-关键状态更新规则)）；并把 `data.author` 快照 upsert 进 `members`（seed/回退用） | messages/members | v0 |
| `user_joined` / `user_left` | 更新 `voice.byChannel` | voice | v0 |
| `speaking_state` | 更新对应 `VoiceState.speaking` | voice | v0 |
| `mute_state` | 更新 `muted`/`deafened` | voice | v1 |
| `user_updated` | upsert `members`（存在则更新、**不存在则插入**，不丢弃未知 `user_id`）；若 `user.id == me.id` 同步 `auth.me`（见 [§6.2](#62-关键状态更新规则)） | members/auth | v1 |
| `channel_created/updated/deleted` | 按 [§6.2](#62-关键状态更新规则) 频道规则：插入+重排 / 替换+重排 / 移除+删消息桶+导航收敛 | channels | v1 |
| `webrtc_offer` | 交给 `peer.ts` 处理（见 §9） | voice | v0 |
| `ice_candidate` | `pc.addIceCandidate`（见 §9） | voice | v0 |

> **reauth 版本归属 [v1]**：`reauth` / `token:refreshed`→`reauth` / `auth_ok.data.reauth` / `wsState='reauth'` / WS 期间 `TOKEN_EXPIRED` 中途刷新 **整体为 [v1]**。**[v0]** 下 access_token 过期一律等价断线，由 WS 指数退避重连 + 重新 `auth` 首帧处理（不实现热 `reauth`）。

> **TOKEN_EXPIRED 处理（契约 §2.6 规则 3，[v1]）**：WS 期间收 `error` 且 `code=TOKEN_EXPIRED` 时，前端立即 `GetAccessToken()`（触发 Go 静默刷新）→ `ws.reauth(newToken)`；服务端给 30s 窗口。也可由 Go 的 `token:refreshed` 事件被动触发 `reauth`。

### 7.3 引导（首屏）时序（对齐契约 §6.1）

```
登录成功 / 已登录启动
  │
  ├─ 1) GetAccessToken() → WS connect + 首帧 auth
  │      └─ 收 auth_ok → auth.me = data.user（资料同步权威来源）
  ├─ 2) (并行) GET /api/v1/bootstrap
  │      └─ channels/members/voice_states/ws_url 填充各 store
  ├─ 3) 渲染首屏：频道树 + 成员 + 各语音频道在线快照
  └─ 4) 选中文字频道 → GET /messages?limit=50 按需拉历史
```

### 7.4 登出时序（与 §7.3 引导时序对称）

登出是 **Go + 前端协同的复合动作**，端到端步骤如下（消除「仅 Go 侧清会话而前端 WS 仍 open」导致的异常态）：

```
用户在 AppSettings（§5.1 / §12.1）点「退出登录」
  │
  ├─ 1) 前端先主动关闭信令：ws.close()（§7.2，置 intentionalClose=true，阻止 onclose 重连）
  ├─ 2) 若在语音频道：先 leaveVoice/teardownVoice 拆 PC + pipeline（复用 §9.3 拆链逻辑），避免悬空 PC
  ├─ 3) 调 Go Logout(ctx) binding（§1.3 / §2.4：调登录中介 /api/desktop/logout 删服务端会话 + DeleteSession 清 desktop_session_id + 清内存 token）
  ├─ 4) 重置所有 store（§6.4 登出重置规则）：auth({loggedIn:false, me:null}) +
  │      voice / messages / channels / members / connection / ui 全部清空（不可变更新）
  └─ 5) 经 auth.loggedIn=false 由 §5.3 路由壳自动切回 LoginView
```

> **与 `auth:expired` 一致**：`auth:expired`（refresh 失败 / REST 重试后仍 401 / WS onopen 取 token 失败）复用**同一套**「关 WS + 重置 store + 回 LoginView」流程（§6.4），避免两条回登录路径行为不一致。
>
> **`onopen` 取 token 防御**：WS `onopen` 内 `await GetAccessToken()` 必须 try/catch（§7.2 已实现）；`desktop_session_id` 为空（已登出）或登录中介刷新返回 `SESSION_INVALID` 时不触发重连，主动关闭并 `EventsEmit("auth:expired")`，防止未处理 rejection 与「登出后 onclose 仍重连且取 token 失败」的异常态。

---

## 8. 前端语音流水线

> 上行管线 `[v0]`（基础收发）；降噪/逐人音量/设备选择/麦克风测试/PTT-VAD `[v1]`。全部纯前端本地行为，**除 `speaking_state`/`mute_state` 外不经 WS**（契约 §4.2 备注）。骨架直接采用调研 06。

### 8.1 上行管线总览

```
getUserMedia(约束)                                          [v0 采集]
  echoCancellation:true, autoGainControl:true,
  noiseSuppression:false  ← 降噪交给 RNNoise（契约 §6.2）
        │
        ▼
MediaStreamSource ─▶ RNNoise AudioWorkletNode ─▶ GainNode(门控) ─▶ MediaStreamDestination   [v1 降噪+门控]
        │                                                                    │
        ├─▶ AnalyserNode (RMS 说话检测，旁路不接 destination)  [v0]            ▼
                                                                      processedTrack
                                                                            │
                                                                  pc.addTrack(processedTrack)  [v0]
                                                                            │ Opus 上行
                                                                            ▼ SFU 选择性转发
下行: pc.ontrack ─▶ (静音 sink audio 驱动) + MediaStreamSource ─▶ 逐人 GainNode ─▶ 出声 audio  [v1]
```

> v0 最小闭环可省略 RNNoise/门控，直接 `source → dest`（或直接把 raw track 加入 PC）；v1 插入降噪 worklet 与门控 GainNode。

### 8.2 采集 + RNNoise worklet（调研 06 §1）

采集约束严格对齐契约 §6.2 / 调研 06 §1.1：`echoCancellation:{ideal:true}`、`autoGainControl:{ideal:true}`、`noiseSuppression:{exact:false}`、`channelCount:{ideal:1}`、`sampleRate:{ideal:48000}`。用 `getSettings()` 核对实际生效值。

```ts
// lib/voice/pipeline.ts [v0 采集] + [v1 降噪/门控]
import { NoiseSuppressorWorklet_Name } from "@timephy/rnnoise-wasm";
import NoiseSuppressorWorklet from "@timephy/rnnoise-wasm/NoiseSuppressorWorklet?worker&url";

export const RAW_AUDIO_CONSTRAINTS: MediaStreamConstraints = {
  audio: {
    echoCancellation: { ideal: true },
    autoGainControl: { ideal: true },
    noiseSuppression: { exact: false }, // 关键：关浏览器降噪，避免与 RNNoise 叠加
    channelCount: { ideal: 1 },
    sampleRate: { ideal: 48000 },
  },
  video: false,
};

export type Pipeline = {
  ctx: AudioContext;
  source: MediaStreamAudioSourceNode;
  gate: GainNode;            // PTT/VAD 门控（§8.3）
  dest: MediaStreamAudioStreamDestination;
  processedTrack: MediaStreamTrack;
  denoise?: AudioWorkletNode;
};

export async function buildPipeline(deviceId?: string): Promise<Pipeline> {
  const constraints = structuredClone(RAW_AUDIO_CONSTRAINTS);
  if (deviceId) (constraints.audio as MediaTrackConstraints).deviceId = { exact: deviceId };

  const rawStream = await navigator.mediaDevices.getUserMedia(constraints);
  // 核对实际生效（约束 != 设置，调研 06 §1.1）
  const s = rawStream.getAudioTracks()[0].getSettings();
  console.debug("audio settings:", s.noiseSuppression, s.echoCancellation, s.autoGainControl);

  const ctx = new AudioContext({ sampleRate: 48000 });
  const source = ctx.createMediaStreamSource(rawStream);
  const gate = ctx.createGain();
  const dest = ctx.createMediaStreamDestination();

  // [v1] RNNoise：source → denoise → gate → dest（调研 06 §1.2）
  let denoise: AudioWorkletNode | undefined;
  try {
    await ctx.audioWorklet.addModule(NoiseSuppressorWorklet);
    denoise = new AudioWorkletNode(ctx, NoiseSuppressorWorklet_Name);
    source.connect(denoise).connect(gate).connect(dest);
  } catch (e) {
    // 降噪不可用时降级：source → gate → dest（不阻断通话）
    console.warn("RNNoise 初始化失败，降级为不降噪:", e);
    source.connect(gate).connect(dest);
  }

  return { ctx, source, gate, dest, processedTrack: dest.stream.getAudioTracks()[0], denoise };
}
```

### 8.3 PTT/VAD 门控切换状态机（调研 06 §2 + §3）

三种发声模式，统一经门控出口 `applyTransmit`：

```ts
// lib/voice/gate.ts [v1]
export type VoiceMode = "ptt" | "vad" | "open";

export function createGate(ctx: AudioContext, gate: GainNode) {
  // 平滑门控防爆音（调研 06 §2 方式 B）
  function setGate(open: boolean) {
    gate.gain.setTargetAtTime(open ? 1 : 0, ctx.currentTime, 0.02); // 20ms 时间常数
  }
  return { setGate };
}
```

```ts
// 控制器（在语音组件内组装）
import { EventsOn } from "../../wailsjs/runtime/runtime";

let mode: VoiceMode = "ptt";
let vadSpeaking = false;
let pttHeld = false;

function recompute() {
  // PTT: 按住才发；VAD: 检测到说话才发；OPEN: 始终发
  const send = mode === "ptt" ? pttHeld : mode === "vad" ? vadSpeaking : true;
  gateCtl.setGate(send);
}

// 全局 PTT 热键来自 Go（§3.1）；仅 PTT 模式响应（契约 §6.2）
EventsOn("ptt:start", () => { pttHeld = true;  if (mode === "ptt") recompute(); });
EventsOn("ptt:stop",  () => { pttHeld = false; if (mode === "ptt") recompute(); });

// VAD：由说话检测驱动（见 §8.4）
function onVadChange(speaking: boolean) { vadSpeaking = speaking; if (mode === "vad") recompute(); }
```

> 模式切换是纯前端本地逻辑，**不通知服务端**（契约 §6.2）。注意区分：门控（是否发声）与 `speaking_state`（说话指示广播）解耦——见 §8.4。

### 8.4 说话检测（AnalyserNode RMS）→ `speaking_state` 广播

直接采用调研 06 §3 的 `createSpeakingDetector`（带通 300–3000Hz → AnalyserNode → RMS → 双阈值滞回 ENTER/EXIT + onset 去抖 + hangover 挂起）。**检测对象是降噪后的 source（或 raw source）**，只在 `speaking` **翻转**时广播（契约 §4.4「仅在状态翻转时发送」）。

```ts
// 在语音组件内：把说话检测接到 WS speaking_state
import { createSpeakingDetector } from "$lib/voice/speaking";

const detector = createSpeakingDetector(pipeline.ctx, pipeline.source, (speaking) => {
  // 1) 本地头像高亮（更新 voice store 中自身的 speaking）
  voice.setSelfSpeaking(speaking);
  // 2) 广播给同频道其他成员（契约 §4.4，仅翻转时发，无 channel_id，服务端按会话补全）
  ws.send("speaking_state", { speaking });
  // 3) [v1] VAD 模式下同时驱动门控
  onVadChange(speaking);
});
```

> **说话指示驱动**：他人头像高亮完全由收到的 `speaking_state` 广播驱动（契约 §4.4），与媒体层解耦。自己头像高亮用本地检测结果即时反馈（不等服务端回环）。

### 8.5 远端逐人音量 + 本地静音某人（调研 06 §4）

每个远端 track 一套 `MediaStreamSource → GainNode → MediaStreamDestination`，并挂一个**静音 sink audio 元素驱动 Chrome 拉流**（调研 06 §4 关键坑）。逐人音量/本地静音改 `gain.value`，**纯本地，不经 WS**（契约 §4.2 备注）。

```ts
// lib/voice/remote.ts [v1]
// userId 来自远端 MediaStream.id —— 服务端把转发 track 的 StreamID 设为音源 user_id（契约 §4.6），
// 接收端据此把这路声音绑定到具体用户，逐人音量/本地静音才能落到正确音轨。
export function attachRemote(ctx: AudioContext, remoteStream: MediaStream, userId: string) {
  // (a) 静音 sink 驱动音频管线（反 Chrome bug，调研 06 §4）
  const sink = new Audio();
  sink.srcObject = remoteStream;
  sink.muted = true;
  sink.play().catch(() => {});

  // (b) source → gain → destination
  const track = remoteStream.getAudioTracks()[0];
  const src = ctx.createMediaStreamSource(new MediaStream([track]));
  const gain = ctx.createGain();
  const dst = ctx.createMediaStreamDestination();
  src.connect(gain).connect(dst);

  // (c) 真正出声的 audio
  const out = new Audio();
  out.srcObject = dst.stream;
  out.play().catch(() => {});

  return {
    setVolume: (v: number) => { gain.gain.value = Math.min(Math.max(v, 0), 4); }, // 钳制 0~4（>4 易破音）
    setMuted: (m: boolean) => { gain.gain.value = m ? 0 : 1; },      // 本地静音某人
    destroy: () => { src.disconnect(); gain.disconnect(); out.srcObject = null; sink.srcObject = null; },
  };
}
```

> **自静音(mute)/扬声静音(deafen)**：mute = 关闭上行门控（`gate.gain=0`）或 `processedTrack.enabled=false`；deafen = 把所有远端 `out` 静音 + 自动 mute。两者经 WS `mute_state` 广播（契约 §4.4，因为是"向他人展示的状态"）。**逐人音量与本地静音某人不广播**。

### 8.6 设备选择 + 麦克风测试（调研 06 §5）

- **枚举**：`enumerateDevices()` 过滤 `audioinput`/`audiooutput`（需先 getUserMedia 拿权限，否则 label 空）。监听 `devicechange` 热插拔。
- **切换输入**：通话中用 **重新 getUserMedia + `sender.replaceTrack(processedTrack)`**（免重协商，调研 06 §5.2 方式 B）——重建降噪链得到新 `processedTrack` 后 replace。
- **切换输出**：对各远端 `out` audio 元素调 `setSinkId(deviceId)`（webview/Chromium 支持）。
- **麦克风测试**（本地回听 + 音量条）：临时把 source 接到 `ctx.destination` 回听（调研 06 §5.5），同时复用 §8.4 的 RMS 渲染音量条。提示用户戴耳机防回声。
- 持久化的 `deviceId` 切换前须校验存在性（刷新后可能变，调研 06 §5 坑）。

---

## 9. SFU 多 track 重协商在前端的处理

> `[v0]`。服务端是 **SFU 且是 offerer**，重协商由服务端发起、客户端被动应答（契约 §4.6）。**复用同一个 PC，不每次新建**（调研 06 §6）。

### 9.1 角色模型

契约规定服务端主动 `CreateOffer`/下发 `webrtc_offer`，客户端只 `setRemoteDescription(offer)` → `createAnswer` → 回 `webrtc_answer`（回带 offer 的 `id`）。这是**单向 offerer**模型，比通用 Perfect Negotiation 简单——客户端**从不主动发 offer**（不依赖 `onnegotiationneeded` 发起），只处理收到的 offer。但仍保留滚动重协商能力（随时可能来新 offer，契约 §4.6「客户端必须实现滚动重协商，不得丢弃」）。

### 9.2 信令处理（对齐契约 §4.6 字段）

```ts
// lib/voice/peer.ts [v0]
export function createPeer(ws: WSClient, channelId: string, localTrack: MediaStreamTrack,
                           onRemote: (stream: MediaStream, userId: string) => void) {
  const pc = new RTCPeerConnection({
    // 通常无需 STUN/TURN（服务端 SetNAT1To1IPs 宣告 host 候选，契约 §4.6）
    iceServers: [],
  });

  // 上行：加入本地（已降噪）track
  pc.addTrack(localTrack, new MediaStream([localTrack]));

  // 下行：每条远端 track（每人一路）触发 ontrack
  // 服务端把转发 track 的 StreamID 设为音源 user_id（契约 §4.6），故 e.streams[0].id == user_id
  pc.ontrack = (e) => {
    const stream = e.streams[0] ?? new MediaStream([e.track]);
    onRemote(stream, stream.id); // stream.id = 音源 user_id，用于按人绑定远端句柄
  };

  // 本地 ICE 候选 → 上报（契约 §4.6 ice_candidate，双向）
  pc.onicecandidate = ({ candidate }) => {
    ws.send("ice_candidate", { channel_id: channelId, candidate: candidate ? candidate.toJSON() : null });
  };

  // 收服务端 offer（含重协商）→ answer（必须回带 offer 的 id，契约 §4.6）
  const offUnsub = ws.on("webrtc_offer", async (data, id) => {
    if (data.channel_id !== channelId) return;
    await pc.setRemoteDescription(data.sdp);          // {type:'offer', sdp:'...'}
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);
    ws.send("webrtc_answer", { channel_id: channelId, sdp: answer }, id); // 回带 offer 的 id；直接用 answer 对象
  });

  // 收远端 ICE 候选（candidate=null 表示结束，须容忍，契约 §4.6）
  const iceUnsub = ws.on("ice_candidate", async (data) => {
    if (data.channel_id !== channelId) return;
    if (data.candidate) {
      try { await pc.addIceCandidate(data.candidate); } catch (e) { console.warn("addIceCandidate:", e); }
    }
  });

  function destroy() { offUnsub(); iceUnsub(); pc.close(); }
  return { pc, destroy };
}
```

### 9.3 加入/离开语音频道

```ts
// 远端句柄注册表：userId → {setVolume,setMuted,destroy}（§8.5），供逐人音量/本地静音/拆链按人寻址
const remoteHandles = new Map<string, ReturnType<typeof attachRemote>>();

// createPeer 的 onRemote 回调：按 user_id（= stream.id，契约 §4.6）注册远端句柄
function attachRemoteFromStream(stream: MediaStream, userId: string) {
  remoteHandles.get(userId)?.destroy();                       // 同一人重协商重复 track 时先清旧
  remoteHandles.set(userId, attachRemote(pipeline!.ctx, stream, userId));
}

// 统一拆链：joinVoice 切换分支 / leaveVoice / §10.2 rebuildVoice 三处复用（单一实现，杜绝孤儿 PC/管线）
function teardownVoice() {
  peer?.destroy(); peer = null;
  detector?.destroy(); detector = null;
  remoteHandles.forEach((h) => h.destroy()); remoteHandles.clear(); // destroy 所有远端句柄
  pipeline?.source.mediaStream?.getTracks().forEach((t) => t.stop()); // 停麦克风采集，释放设备
  pipeline?.ctx.close(); pipeline = null;
}

// 加入：若已在别的语音频道，先显式 leave 旧频道（本地拆链 + 发 leave_channel）再 join；
// 与服务端隐式 leave（契约 §4.4）对账，不依赖服务端单边清理。
async function joinVoice(channelId: string) {
  const cur = get(voice).currentChannelId;
  if (cur === channelId) return;                              // 幂等：重复点同一频道
  if (cur) await leaveVoice(cur);                            // 切频道：先离旧（拆链+通知服务端）

  pipeline = await buildPipeline(settings.inputDeviceId);                          // §8.2
  detector = createSpeakingDetector(pipeline.ctx, pipeline.source, onSpeaking);    // §8.4
  peer = createPeer(ws, channelId, pipeline.processedTrack, attachRemoteFromStream); // §9.2

  // 带请求 id 发送；不立即置位 currentChannel（catalog-1：延迟到确认加入成功）
  const joinId = ws.nextId();
  ws.send("join_channel", { channel_id: channelId }, joinId);                      // 契约 §4.4

  // 成功路径：收到自身 user_joined（user.id==me.id 且频道匹配）才认定加入
  const offOk = ws.on("user_joined", (data) => {
    if (data.channel_id !== channelId || data.user?.id !== get(auth).me?.id) return;
    cleanup(); voice.setCurrentChannel(channelId);
  });
  // 失败路径：error.ref==joinId → 拆链 + 不置位 + 提示（契约 §4.7 / §4.4 失败回执）
  const offErr = ws.on("error", (data) => {
    if (data?.ref !== joinId) return;
    cleanup(); teardownVoice(); voice.setCurrentChannel(null);
    toast.error(data.code === "NOT_FOUND" ? "频道不存在或已删除"
              : data.code === "VALIDATION_ERROR" ? "该频道不可加入"
              : "无法加入语音频道");
  });
  // 超时兜底：5s 未见 user_joined/error → 回滚提示
  const timer = setTimeout(() => {
    cleanup(); teardownVoice(); voice.setCurrentChannel(null);
    toast.error("加入语音超时，请重试");
  }, 5000);
  function cleanup() { offOk(); offErr(); clearTimeout(timer); }
}

// 离开：通知服务端 + 本地统一拆链（契约 §6.3）
async function leaveVoice(channelId: string) {
  ws.send("leave_channel", { channel_id: channelId });
  teardownVoice();
  voice.setCurrentChannel(null);
}
```

> **滚动重协商**：有人进/出、开停麦导致服务端 track 集合变化 → 服务端重发 `webrtc_offer`（契约 §4.6），`peer.ts` 的 `webrtc_offer` handler 始终在线，立即 answer，无需特殊处理。新成员的下行 track 经 `pc.ontrack` 触发 `attachRemoteFromStream`，按 `stream.id`(=user_id) 注册到 `remoteHandles`（§8.5）；`§12.3 perUserVolume` 变更即按 userId 查 `remoteHandles` 调 `setVolume`。
> **加入的确认与回滚（catalog-1）**：`join_channel` 携带 `id`；服务端校验失败回 `error`（带 `ref`，§4.7）。客户端**延迟置位** `currentChannel`——仅在收到自身 `user_joined` 后认定加入；收到匹配 `ref` 的 `error` 或 5s 超时则 `teardownVoice()` 回滚并提示，绝不停留在「界面已进入但实际无 PC/无音频」的悬挂态。

---

## 10. WS 断线重连与 PC 重建策略

> `[v0]`。分层策略：临时抖动用 ICE restart 原地恢复，远端真消失/反复失败才整体重建 PC（调研 06 §7）。

### 10.1 分层恢复（调研 06 §7）

| 层级 | 触发 | 动作 |
|------|------|------|
| WS 断 | `ws.onclose`（非主动） | 指数退避重连（500ms→10s 上限），重连成功重发 `auth` 首帧（§7.2） |
| PC `iceConnectionState='failed'` | ICE 失败 | `pc.restartIce()`——但本项目客户端不主动发 offer，故 **改为通知服务端**（见 §10.2） |
| PC `connectionState='failed'` 反复 | ICE restart 多次失败 | 先确保 WS 活着 → 整体 teardown + 重建 PC + 重新 `join_channel` |
| 进程重启后 | 重连 `bootstrap.voice_states` 为空 | 提示用户重新加入语音（契约 §5.4） |

### 10.2 与单向 offerer 模型的适配

通用 Perfect Negotiation 假设客户端可主动发 offer 触发 `restartIce`。但本项目服务端是唯一 offerer（契约 §4.6），因此：

- **WS 重连后**：WS 客户端自动重连并重发 `auth`（§7.2）。若此前在语音频道，重连后**重新 `join_channel`** → 服务端重建 PC 侧状态并重发 `webrtc_offer`，前端 `peer.ts` 自动 answer。最稳妥是**重连恢复语音时整体重建 PC**（teardown 旧 PC → 新建 → join），避免半死 PC。
- **PC failed**：直接走"整体重建 PC + 重新 join"路径（teardown → `createPeer` → `join_channel`），不尝试客户端发起的 ICE restart（与单向 offerer 模型一致）。
- WS 意外断开服务端视为隐式 `leave_channel`（契约 §6.3），故重连后必须显式重新 `join_channel` 才能恢复语音。

```ts
// lib/voice/peer.ts（重建兜底，调研 06 §7.3 思路适配单向 offerer）
async function rebuildVoice(channelId: string) {
  teardownVoice();                      // 统一拆链（§9.3，含远端句柄/麦克风/PC/管线）
  await ensureWsOpen();                 // 先保证信令活着
  pipeline = await buildPipeline(settings.inputDeviceId);
  detector = createSpeakingDetector(pipeline.ctx, pipeline.source, onSpeaking);
  peer = createPeer(ws, channelId, pipeline.processedTrack, attachRemoteFromStream);
  ws.send("join_channel", { channel_id: channelId }); // 服务端重新下发 offer
}
```

### 10.3 连接状态可视化

`connection` store 反映 `wsState`/`pcState`/`retrying`，UI 顶部条显示：`connecting`（连接中）/`reauth`（刷新令牌中）/`reconnecting`（重连中，带退避倒计时）/`closed`（断开）。语音通话中断时在 `VoicePanel` 顶部提示"正在重连语音…"。

---

## 11. 错误处理与用户提示

> `[v0]`。遵循全局规范：显式处理每层错误、UI 友好提示、不静默吞错。

### 11.1 错误分类与提示策略

| 来源 | 错误码（契约 §7.2） | 用户提示 | 处理 |
|------|--------------------|---------|------|
| REST 401 重试后仍失败 | `UNAUTHENTICATED`/`TOKEN_INVALID` | "登录已过期，请重新登录" | `EventsEmit("auth:expired")` → 回登录页 |
| 登录（Go `Login`） | 登录中介不可用（`/desktop/login` 打不开 / `/exchange` 网络失败） | "无法连接登录服务，请检查网络后重试" | binding 返回 error → 登录页 toast；停留登录页可重试 |
| 登录（Go `Login`） | handoff 超时（用户未在 3 分钟内完成 / 未收到 `/cb` 回调） | "登录超时，请重新点击登录" | 回环监听超时关闭 → binding 返回 error → 停留登录页 |
| 登录（Go `Login`） | exchange 失败（`state` 不匹配 / `handoff_code` 失效 / HTTP ≠ 200） | "登录校验失败，请重新登录" | binding 返回 error → 登录页 toast；不写任何凭据 |
| 刷新（Go `refresh`） | 登录中介 401 / `SESSION_INVALID` | "登录已过期，请重新登录" | `EventsEmit("auth:expired")` → 清 `desktop_session_id` → 回登录页 |
| 刷新（Go `refresh`） | 登录中介不可用（网络失败 / HTTP ≠ 200/401） | "网络异常，正在重试" | `GetAccessToken` 返回 error → 前端按重连/重试处理（不强制登出） |
| REST/WS | `FORBIDDEN` | "需要 owner 权限" | toast；隐藏对应 owner UI |
| REST | `NOT_FOUND` | "频道不存在或已删除" | toast；从 store 移除 |
| REST/WS | `VALIDATION_ERROR` | 用 `error.message`（中文）+ `details` 字段级 | 表单内联标红 |
| WS `auth_error` | `KICKED` | "你已被移出服务器（冷却中）" | 回登录页/退出 |
| WS `auth_error` | `HANDSHAKE_TIMEOUT` | "连接超时，正在重试" | 触发重连 |
| WS `error` | `TOKEN_EXPIRED` | （无需打扰用户）后台 `reauth` | `GetAccessToken` → `ws.reauth`（§7.2） |
| WS `error` | `RATE_LIMITED` | "操作过于频繁，请稍后" | toast；禁用按钮短时 |
| 媒体 | getUserMedia 拒绝 | "无法访问麦克风，请检查权限" | 指引到设备设置 |
| 媒体 | RNNoise worklet 失败 | （静默降级）日志 | 降级为不降噪通话（§8.2） |
| 更新 | SHA256/签名校验失败 | "更新校验失败，已取消" | 中止安装（不静默执行） |

### 11.2 toast/通知组件

`lib/ui/toast.ts` 提供 `toast.error/warn/info(message)`，store 驱动渲染。CRITICAL（鉴权失效）用模态阻断；其余用非阻断 toast。错误 `message` 直接用服务端返回的中文（契约保证面向用户、不含敏感信息，§7.2）。

### 11.3 自身防御

- WS/PC 句柄在组件 `onDestroy` 显式清理（`close`/`disconnect`/`track.stop`，调研 06 §8），防泄漏。
- 所有 `await` 包 try/catch，binding 调用失败转 toast。
- access_token 绝不写日志/localStorage（仅内存）；`desktop_session_id` 仅存 Go 凭据管理器（DPAPI），不进前端；refresh_token 永不出 Go 服务端（Postgres 加密存储）、绝不下发桌面。

---

## 12. UI 布局、主题与本地设置持久化

### 12.1 整体布局（深色主题默认）

```
┌──────────┬──────────────────────────────────────┬─────────────────┐
│ Sidebar  │            主内容区                    │  成员/语音侧栏    │
│ (频道侧栏)│                                       │                 │
│          │  [文字频道] TextChannel.svelte         │  VoicePanel:    │
│ # 大厅   │   ┌────────────────────────────────┐  │  ● Nanako 🔊    │
│ # 公告   │   │ MessageList（向上加载更多）       │  │  ● Alice  🔇    │
│          │   │  ...历史消息（升序）...          │  │  ● Bob   (说话) │
│ 🔊 开黑1 │   │                                │  │   [音量滑块/人]  │
│ 🔊 开黑2 │   └────────────────────────────────┘  │                 │
│          │   [输入框........................发送] │  ── 成员列表 ──   │
│ [我 ⚙]   │                                       │  在线/全部成员    │
│ ControlBar│  [语音频道] VoicePanel（成员网格）     │                 │
│ 🎤 🔈 ⚙  │                                       │                 │
└──────────┴──────────────────────────────────────┴─────────────────┘
```

| 区域 | 组件 | 内容 | 版本 |
|------|------|------|------|
| 频道侧栏 | `Sidebar`/`ChannelList` | 文字(#)/语音(🔊)频道树，按 `position`；owner 见"+新建频道" | v0(列表)/v1(CRUD) |
| 文字区 | `TextChannel`/`MessageList` | 历史分页（顶部"加载更多"）+ 输入框 | v0 |
| 语音/成员侧栏 | `VoicePanel`/`MemberItem` | 语音频道在线成员头像（说话高亮、mute/deafen 图标）、逐人音量滑块；成员列表 | v0(基础)/v1(音量) |
| 底部控制条 | `ControlBar` | 自静音/扬声静音/离开语音/设置入口 | v0 |
| 设置 | `SettingsModal` 等 | 音频设备/麦克风测试/降噪/PTT-VAD/PTT 键/主题/自启/全屏游戏模式/检查更新 | v1 |

### 12.2 主题

深色主题为默认且唯一（v0/v1），CSS 变量集中在 `styles/theme.css`（背景/前景/强调色/说话高亮色等）。说话高亮 = 头像描边强调色 + 轻微发光，由 `voice` store 的 `speaking` 驱动。预留亮色主题切换位（设置项存在但 v1 可仅深色）。

### 12.3 本地设置持久化

设置纯本地（契约：设备选择/模式/音量等不入库、不经 WS），用 `localStorage`（webview 持久），`settings` store 启动时载入、变更时写回。

```ts
// lib/stores/settings.ts [v1]
export type LocalSettings = {
  theme: "dark";                       // v1 仅 dark
  voiceMode: "ptt" | "vad" | "open";   // §8.3
  pttKey: { mods: string[]; key: string }; // 同步给 Go SetGlobalPTTKey
  fullscreenGameMode: boolean;         // 同步给 Go SetFullscreenGameMode
  inputDeviceId: string | null;
  outputDeviceId: string | null;
  denoiseEnabled: boolean;             // RNNoise 开关
  vad: { enter: number; exit: number; hangoverMs: number }; // 说话检测阈值（§8.4）
  perUserVolume: Record<string, number>; // userId → 增益(0~4)，本地静音=0
  autoStart: boolean;                  // 镜像 Go IsAutoStartEnabled
};

const KEY = "lumen.settings.v1";
const DEFAULTS: LocalSettings = {
  theme: "dark", voiceMode: "ptt",
  pttKey: { mods: ["ctrl", "shift"], key: "space" },
  fullscreenGameMode: false,
  inputDeviceId: null, outputDeviceId: null, denoiseEnabled: true,
  vad: { enter: 0.04, exit: 0.02, hangoverMs: 250 },
  perUserVolume: {}, autoStart: false,
};
// load: JSON.parse(localStorage[KEY]) ?? DEFAULTS（容忍缺字段，合并 DEFAULTS）
// persist: 订阅 store 变更 → localStorage[KEY] = JSON.stringify(v)
```

> 设置变更副作用：`pttKey` 变 → 调 `SetGlobalPTTKey` binding（§3.1）；`fullscreenGameMode` 变 → `SetFullscreenGameMode`（§3.1）；`autoStart` 变 → `SetAutoStart`（§3.5）；`inputDeviceId` 变 → 重建管线 + `replaceTrack`（§8.6）；`perUserVolume` 变 → 对应远端 `setVolume`（§8.5）。

---

## 13. v2 对前端的影响概述（SFrame + Insertable Streams）

> `[v2]` 推迟实现，仅概述（契约附录 A）。v0/v1 仅依赖传输层 **DTLS-SRTP**，前端无需改动。

v2 在 SFU（仅转发、不解密）之外对音频负载做端到端加密，对前端的影响集中在语音流水线两端：

- **加解密注入点**：用浏览器 **Insertable Streams**（`RTCRtpScriptTransform`，webview/Chromium 支持）对每个 sender（上行加密）与 receiver（下行解密）的 encoded frame 逐帧处理 SFrame。注入位置在 `peer.ts` 创建 sender/receiver 后，挂 transform，**不改 SFU 转发逻辑**（密文原样转发）。
- **密钥管理**：房间共享对称密钥起步，成员进出时经新增 WS 信令 `e2e_key_update`（契约附录 A 预留，S→C/双向，含加密的房间密钥与 epoch）下发/轮换。前端需在 `voice` store 维护当前 epoch + 密钥，transform worker 据 epoch 选密钥。
- **worker 化**：SFrame 加解密放进专用 Worker（`RTCRtpScriptTransform` 的 worker 端），避免阻塞主线程；与 RNNoise worklet 互不影响（一个在 Web Audio 采集端，一个在 RTP encoded 端）。
- **兼容性前置检查**：v2 上线前确认目标 WebView2 版本支持 `RTCRtpScriptTransform`（契约附录 A 兼容性约束）。
- **对现有结构的侵入**：新增 `lib/voice/e2e.ts`（密钥状态 + transform 装配）与 `lib/voice/sframe.worker.ts`（逐帧加解密）；`peer.ts` 在 `addTrack`/`ontrack` 后调用 `attachTransform(sender/receiver, epochKey)`。其余管线（采集/降噪/门控/说话检测/逐人音量）**不受影响**，因为 SFrame 作用在 RTP encoded 层，Web Audio 仍处理明文 PCM。

详细密钥分发/轮换 epoch/与重协商交互见未来 `docs/design/e2e-design.md`（契约附录 A），本文不展开。

---

## 14. 附录：版本归属速查表

| 功能 | 模块/文件 | 版本 |
|------|----------|------|
| Web 中介登录（系统浏览器 + 回环 handoff + /exchange） | `auth/login.go` | v0 |
| desktop_session_id 凭据存储（wincred/DPAPI） | `auth/credstore.go` | v0 |
| 静默刷新（调登录中介 /refresh）+ 暴露 access_token | `auth/tokenmanager.go` | v0 |
| 资料缓存（/exchange profile，本地 UI） | `auth/profilesync.go` | v0 |
| REST 客户端（bootstrap/me/channels/messages/members） | `lib/api/rest.ts` | v0 |
| WS 客户端（auth/信令/实时） | `lib/api/ws.ts` | v0 |
| WebRTC PC + 单向 offerer 重协商 | `lib/voice/peer.ts` | v0 |
| 上行采集管线（基础） + 说话检测 | `lib/voice/pipeline.ts`/`speaking.ts` | v0 |
| 加入/离开语音、实时文字、状态广播渲染 | stores + components | v0 |
| 深色主题、基础布局、错误 toast | `styles/`/`lib/ui/` | v0 |
| WS 重连 + PC 重建兜底 | `lib/api/ws.ts`/`lib/voice/peer.ts` | v0 |
| RNNoise 降噪 worklet | `lib/voice/denoise.ts`/`pipeline.ts` | v1 |
| PTT/VAD 门控切换 | `lib/voice/gate.ts` | v1 |
| 逐人音量/本地静音某人 | `lib/voice/remote.ts` | v1 |
| 自静音/扬声静音 + `mute_state` 广播 | `ControlBar`/voice store | v1 |
| 设备选择/切换/麦克风测试 | `lib/voice/devices.ts` | v1 |
| reauth（token 刷新更新会话） | `lib/api/ws.ts` + `token:refreshed` | v1 |
| 资料双向同步渲染（`user_updated`） | members/auth store | v1 |
| 频道 CRUD / 踢人（owner UI + REST） | settings/sidebar + `rest.ts` | v1 |
| 全局 PTT 热键（后台/全屏游戏） | `desktop/hotkey.go`/`hotkey_lowlevel.go` | v1 |
| 系统托盘 + 菜单 | `desktop/tray.go` | v1 |
| 关窗口隐藏到托盘 | `main.go`（HideWindowOnClose） | v1 |
| 单实例锁 + 参数转交 | `desktop/singleinstance.go` | v1 |
| 开机自启（注册表） | `desktop/autostart.go` | v1 |
| 自动更新（manifest/校验/安装） | `updater/*.go` + NSIS | v1 |
| 本地设置持久化 | `lib/stores/settings.ts` | v1 |
| E2E（SFrame + Insertable Streams） | `lib/voice/e2e.ts`/`sframe.worker.ts` | v2 |

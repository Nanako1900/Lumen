# 桌面应用 OAuth2 Authorization Code + PKCE(Go) 与服务端 JWKS 验签 + 资料同步

> ⚠️ **登录架构已变更（决策更新）**：桌面端**不再直连 IdP 跑 PKCE**；改为「Web 中介登录」——由官网（Cloudflare Worker，confidential OIDC client）执行 Authorization Code + PKCE，桌面经本地回环 handoff 拿 token，刷新走官网 `/api/desktop/refresh`。因此本文「**桌面侧** PKCE/loopback/oauth2.TokenSource/直连刷新/凭据存 refresh_token」部分**已被取代**（桌面改存 `desktop_session_id`）。仍然有效的是：① Authorization Code + PKCE 机制本身（现在跑在**官网 Worker**侧）；② **服务端 JWKS 本地验签 access_token（keyfunc/v3 + golang-jwt/v5，验 iss/aud/exp）完全不变**；③ 资料同步语义（claims/userinfo）。以 [`docs/design/web-design.md §5`](../design/web-design.md) 与 [`client-design.md §2`](../design/client-design.md)、[`protocol-design.md §2`](../design/protocol-design.md) 为准。

> 调研日期: 2026-06-29
> 适用场景: Go 桌面客户端走 OAuth2 / OIDC 授权码 + PKCE 登录;Go 服务端用本地 JWKS 验签 access_token / id_token;登录与刷新时同步用户资料(name / preferred_username / picture)。
> 说明: 知识截止后版本以本文末检索结果为准。所有版本号均来自 2025-2026 年检索。

---

## 0. 版本基线(2025-2026 检索)

| 库 | 导入路径 | 推荐版本 | 备注 |
|----|----------|----------|------|
| oauth2 | `golang.org/x/oauth2` | **v0.30.0** (2025-04-30) | PKCE 三件套已稳定;x/ 仓库无传统 CHANGELOG |
| jwt | `github.com/golang-jwt/jwt/v5` | **v5.3.0** (2025-07-30) | v5 重写了 Claims 接口与 Validation Options;最低 Go 1.21 |
| keyfunc | `github.com/MicahParks/keyfunc/v3` | **v3.8.0** (最新非撤回) | v3 是 `jwkset` 的薄封装;**避开撤回版本** v3.0.0–v3.3.7(竞态/缓存问题) |
| jwkset | `github.com/MicahParks/jwkset` | 随 keyfunc/v3 传递依赖 (>= v0.11.0) | 自动缓存 + 轮换的 JWK Set HTTP 客户端 |
| go-oidc | `github.com/coreos/go-oidc/v3/oidc` | v3 系列 | OIDC Provider / IDTokenVerifier / UserInfo |
| keyring | `github.com/zalando/go-keyring` | master | 跨平台,Windows 后端走凭据管理器 |
| wincred | `github.com/danieljoos/wincred` | 最新 | 直接封装 Windows Credential Manager API(更细粒度) |
| browser | `github.com/pkg/browser` | 最新 | 打开系统默认浏览器(open/xdg-open/start) |

> **撤回版本警告**: keyfunc go.mod 显式 `retract` 了 `[v3.3.6, v3.3.7]`(竞态)、`v3.3.0`(返回类型错误)、`[v3.0.0, v3.3.5]`(刷新时只覆盖追加,不删除旧 kid)。务必用 **>= v3.5.0**,推荐 v3.8.0。

---

## 1. 客户端: Authorization Code + PKCE + Loopback 回调 (RFC 8252)

### 1.1 关键设计点(RFC 8252 / RFC 7636)

- **必须用系统浏览器,绝不用内嵌 WebView**:内嵌浏览器能读到凭据、cookie 和整个 DOM,构成钓鱼/凭据窃取风险。
- **Loopback 随机端口**:监听 `127.0.0.1:0`(端口 0 = 让 OS 分配空闲临时端口),回调 URI 形如 `http://127.0.0.1:<port>/callback`。授权服务器 MUST 允许请求时指定任意端口。
- **用 IP 字面量 `127.0.0.1`,不要用 `localhost`**:避免误监听其他网络接口,且更抗客户端防火墙/host 解析错配(RFC 8252 §7.3,localhost 为 NOT RECOMMENDED)。
- **尽量同时尝试 IPv4 与 IPv6**(`[::1]`),用可用的那个。
- **PKCE 强制**:loopback 回调可能被同机其它 app 监听拦截,故 PKCE 是必需的;授权服务器 SHOULD 拒绝不带 PKCE 的原生 app 请求。
- **Windows 额外加固**:创建 loopback socket 时设置 `SO_EXCLUSIVEADDRUSE`,防止其它 app 抢占同一 socket(Go 标准 `net` 默认行为通常已足够,如需可用原始 socketopt)。
- **state 防 CSRF**:生成随机 state,回调时严格比对。
- **Provider 注意**:Google 已对 native iOS/Android/Chrome 客户端类型弃用 loopback;桌面客户端登录前要确认目标 IdP 仍支持 loopback 重定向。

### 1.2 x/oauth2 的 PKCE 三件套(v0.30.0)

| 函数 | 用途 | 用在哪 |
|------|------|--------|
| `oauth2.GenerateVerifier() string` | 生成 32 字节随机 verifier,base64url 编码为 43 字节 URL-safe 串(RFC 7636);**每次授权都新生成** | 本地保存 |
| `oauth2.S256ChallengeOption(verifier string) AuthCodeOption` | 由 verifier 派生 S256 challenge | **仅** 传给 `Config.AuthCodeURL` / `Config.DeviceAuth` |
| `oauth2.VerifierOption(verifier string) AuthCodeOption` | 携带 verifier | **仅** 传给 `Config.Exchange` / `Config.DeviceAccessToken` |
| `oauth2.S256ChallengeFromVerifier(verifier string) string` | 直接返回 S256 challenge 字符串 | 辅助;能用 `S256ChallengeOption` 时优先它 |

### 1.3 客户端代码骨架

```go
// 文件: client/auth/login.go
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/pkg/browser"
	"golang.org/x/oauth2"
)

// OAuthConfig 是从配置/环境注入的不可变配置。
type OAuthConfig struct {
	ClientID    string   // 公共客户端,无 secret(桌面 app 不应内置 secret)
	AuthURL     string   // 授权端点
	TokenURL    string   // 令牌端点
	Scopes      []string // 例: {"openid", "profile", "email", "offline_access"}
}

// LoginResult 携带令牌(不可变返回值)。
type LoginResult struct {
	Token *oauth2.Token
}

// randomState 生成 CSRF 用随机 state。
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 state 失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Login 执行完整的 PKCE + loopback 授权码流程。
func Login(ctx context.Context, cfg OAuthConfig) (*LoginResult, error) {
	// 1) 监听 loopback 随机端口(优先 IPv4,失败回退 IPv6)。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ln, err = net.Listen("tcp", "[::1]:0")
		if err != nil {
			return nil, fmt.Errorf("绑定 loopback 失败: %w", err)
		}
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	oauthCfg := &oauth2.Config{
		ClientID:    cfg.ClientID,
		Scopes:      cfg.Scopes,
		RedirectURL: redirectURL,
		Endpoint: oauth2.Endpoint{
			AuthURL:  cfg.AuthURL,
			TokenURL: cfg.TokenURL,
		},
	}

	// 2) 生成 PKCE verifier 与 state。
	verifier := oauth2.GenerateVerifier() // 每次授权都新生成
	state, err := randomState()
	if err != nil {
		return nil, err
	}

	// 3) 构造授权 URL,携带 S256 challenge,打开系统浏览器。
	authCodeURL := oauthCfg.AuthCodeURL(
		state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.AccessTypeOffline, // 请求 refresh_token(等价 access_type=offline / prompt=consent,依 IdP 而定)
	)
	if err := browser.OpenURL(authCodeURL); err != nil {
		return nil, fmt.Errorf("打开系统浏览器失败: %w", err)
	}

	// 4) 本地 HTTP 服务器接收回调,取 code。
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			errCh <- fmt.Errorf("授权服务器返回错误: %s - %s", e, q.Get("error_description"))
			http.Error(w, "登录失败,可关闭此页", http.StatusBadRequest)
			return
		}
		if q.Get("state") != state { // 严格比对 state 防 CSRF
			errCh <- fmt.Errorf("state 不匹配,疑似 CSRF")
			http.Error(w, "state 校验失败", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		if code == "" {
			errCh <- fmt.Errorf("回调缺少 code")
			http.Error(w, "缺少授权码", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("<html><body>登录成功,请返回应用。</body></html>"))
		codeCh <- code
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// 5) 等待回调(带超时)。
	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		return nil, err
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("等待授权回调超时")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// 6) 用 code + verifier 交换令牌(VerifierOption 携带 PKCE verifier)。
	token, err := oauthCfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("换取令牌失败: %w", err)
	}
	return &LoginResult{Token: token}, nil
}

var _ = url.Values{} // 占位,避免未用导入(实际按需删除)
```

---

## 2. 客户端: refresh_token 静默刷新

### 2.1 标准做法 — 用 `oauth2.TokenSource`

`oauth2.Token` 结构关键字段:`AccessToken`、`RefreshToken`、`Expiry (time.Time)`、`TokenType`。
`Token.Valid()` 在 token 非空且未过期(留有约 10 秒提前量)时返回 true。

`Config.TokenSource(ctx, token)` 返回一个 **自动刷新** 的 `TokenSource`:每次调用 `.Token()` 时若当前 token 已过期,会用 `RefreshToken` 向 `TokenURL` 请求新 token。用 `oauth2.ReuseTokenSource` 包一层可在过期前复用缓存,避免每次都打网络。

注意:很多 IdP 在刷新响应里 **不回传** 新的 `RefreshToken`(滚动刷新除外),x/oauth2 会自动沿用旧的 refresh_token。要监听 token 变化以便持久化新值。

### 2.2 刷新代码骨架 + 持久化 hook

```go
// 文件: client/auth/tokensource.go
package auth

import (
	"context"
	"fmt"

	"golang.org/x/oauth2"
)

// persistingTokenSource 包装上游 TokenSource,token 变化时回调持久化。
type persistingTokenSource struct {
	base oauth2.TokenSource
	last *oauth2.Token
	save func(*oauth2.Token) error // 写入凭据存储(见第 3 节)
}

func (p *persistingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := p.base.Token() // 过期则用 refresh_token 自动刷新
	if err != nil {
		return nil, fmt.Errorf("刷新令牌失败: %w", err)
	}
	// AccessToken 变化(说明刚刷新过)→ 持久化新 token
	if p.last == nil || tok.AccessToken != p.last.AccessToken {
		if err := p.save(tok); err != nil {
			return nil, fmt.Errorf("持久化令牌失败: %w", err)
		}
		p.last = tok
	}
	return tok, nil
}

// NewAutoRefreshClient 返回一个会自动静默刷新并持久化 token 的 *http.Client。
func NewAutoRefreshClient(
	ctx context.Context,
	oauthCfg *oauth2.Config,
	stored *oauth2.Token,
	save func(*oauth2.Token) error,
) *oauth2.Token {
	src := oauthCfg.TokenSource(ctx, stored)       // 自动刷新
	reuse := oauth2.ReuseTokenSource(stored, src)  // 过期前复用缓存
	pts := &persistingTokenSource{base: reuse, last: stored, save: save}

	// oauth2.NewClient(ctx, pts) 可得到自动带 Bearer 头并自动刷新的 http.Client。
	_ = pts
	return stored
}
```

> 实战要点:把 `persistingTokenSource` 传给 `oauth2.NewClient(ctx, pts)` 得到的 `*http.Client`,所有出站请求都会自动带最新 Bearer 并在过期时静默刷新;`save` 回调把新 token 写回 OS 凭据存储。

---

## 3. Windows 凭据安全存储(refresh_token 等敏感数据)

### 3.1 两种选择

**方案 A — 跨平台首选: `github.com/zalando/go-keyring`**

- 接口: `Set(service, user, password string) error` / `Get(service, user string) (string, error)` / `Delete(service, user string) error` / `DeleteAll(service string) error`。
- Windows 后端走 **Windows Credential Manager**;target 名为 `service:username`。
- 大小限制(Windows): service 上限 32KiB,password 上限 **2560 字节**(refresh_token 通常没问题;若要存大 JWT 注意此限)。
- 测试: `keyring.MockInit()` 用内存实现替换 OS provider,便于 CI。
- 注意历史上 Windows `Get` 路径有偶发 nil 指针 panic(约 0.69%),用较新版本规避。

**方案 B — 仅 Windows、需更细控制: `github.com/danieljoos/wincred`**

- 直接封装 Windows Credential Manager API;`NewGenericCredential` / `Write` / `GetGenericCredential` / `Delete` / `List`。
- 可控持久化模式:`PersistSession`(仅当前登录会话) / `PersistLocalMachine`(本机所有会话) / `PersistEnterprise`(漫游)。
- 关于 DPAPI:Credential Manager 在 OS 层做静态加密(绑定用户登录态),**不需要** 自己调 `CryptProtectData`/`CryptUnprotectData`;wincred 只存原始字节。
- 若希望在凭据管理器 UI 正确显示,可对 blob 做 UTF-16(LE)编码。blob 上限同样 **2560 字节**。

> 建议:跨平台用 go-keyring(macOS Keychain / Linux Secret Service / Windows Credential Manager 一套 API);若桌面端只发 Windows 且要持久化模式控制,直接用 wincred。

### 3.2 存储代码骨架(go-keyring)

```go
// 文件: client/store/credstore.go
package store

import (
	"encoding/json"
	"fmt"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

const serviceName = "LumenDesktop" // 应用唯一标识

// SaveToken 把 oauth2.Token 序列化后存入 OS 凭据存储。
func SaveToken(account string, tok *oauth2.Token) error {
	data, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("序列化令牌失败: %w", err)
	}
	// 注意 Windows 密码上限 2560 字节;若 token 过大改用文件 + DPAPI/wincred 大字段方案。
	if err := keyring.Set(serviceName, account, string(data)); err != nil {
		return fmt.Errorf("写入凭据存储失败: %w", err)
	}
	return nil
}

// LoadToken 从 OS 凭据存储读取并反序列化。
func LoadToken(account string) (*oauth2.Token, error) {
	s, err := keyring.Get(serviceName, account)
	if err != nil {
		return nil, fmt.Errorf("读取凭据失败: %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(s), &tok); err != nil {
		return nil, fmt.Errorf("反序列化令牌失败: %w", err)
	}
	return &tok, nil
}

// DeleteToken 登出时清除。
func DeleteToken(account string) error {
	if err := keyring.Delete(serviceName, account); err != nil {
		return fmt.Errorf("删除凭据失败: %w", err)
	}
	return nil
}
```

---

## 4. 服务端: JWT + JWKS 本地验签(keyfunc/v3 + golang-jwt/jwt/v5)

### 4.1 关键点

- keyfunc/v3 = `jwkset` 的薄封装,核心入口:
  - `keyfunc.NewDefault([]string{jwksURL}) (keyfunc.Keyfunc, error)` — 自动从 JWKS URL 拉公钥,**后台 goroutine 自动刷新缓存**;遇到未知 `kid` 时会主动刷新远端,从而支持 **密钥轮换**。
  - `keyfunc.NewDefaultCtx(ctx, []string{jwksURL})` — 同上,但可用 ctx 结束刷新 goroutine(长生命周期服务推荐,避免 goroutine 泄漏)。
  - `k.Keyfunc` 实现 `jwt.Keyfunc`,直接喂给 `jwt.Parse` / `jwt.ParseWithClaims`。
- **安全红线**:
  - 永不在不验签的情况下解析 JWT;
  - 必须显式限定签名算法(如 `jwt.WithValidMethods([]string{"RS256"})`),避免 alg 混淆/`none` 攻击;
  - JWKS 端点必须 HTTPS。
- **校验 iss / aud / exp**:用 v5 的 Parser Options:`jwt.WithIssuer(...)`、`jwt.WithAudience(...)`、`jwt.WithExpirationRequired()`;exp/nbf/iat 由 v5 默认校验,可用 `jwt.WithLeeway(...)` 容忍时钟漂移。

### 4.2 服务端验签代码骨架

```go
// 文件: server/auth/verifier.go
package auth

import (
	"context"
	"fmt"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// Claims 是期望的 JWT 自定义声明。
type Claims struct {
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Picture           string `json:"picture"`
	jwt.RegisteredClaims          // 提供 Issuer / Audience / Subject / ExpiresAt 等
}

// Verifier 持有自动刷新的 JWKS keyfunc 与校验参数(创建后不可变)。
type Verifier struct {
	kf       keyfunc.Keyfunc
	issuer   string
	audience string
}

// NewVerifier 从 JWKS URL 构造验签器;用 ctx 控制后台刷新 goroutine 生命周期。
func NewVerifier(ctx context.Context, jwksURL, issuer, audience string) (*Verifier, error) {
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL}) // 自动拉取 + 缓存 + 轮换
	if err != nil {
		return nil, fmt.Errorf("初始化 JWKS keyfunc 失败: %w", err)
	}
	return &Verifier{kf: kf, issuer: issuer, audience: audience}, nil
}

// Verify 解析并校验 token 字符串,返回校验通过的 Claims。
func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		v.kf.Keyfunc, // 用 JWKS 公钥验签
		jwt.WithValidMethods([]string{"RS256"}), // 显式限定算法,防 alg 混淆
		jwt.WithIssuer(v.issuer),                // 校验 iss
		jwt.WithAudience(v.audience),            // 校验 aud
		jwt.WithExpirationRequired(),            // 要求 exp 存在(exp/nbf/iat 默认校验)
		jwt.WithLeeway(0),                       // 时钟漂移容忍,按需放宽如 30s
	)
	if err != nil {
		return nil, fmt.Errorf("JWT 校验失败: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("JWT 无效")
	}
	return claims, nil
}
```

```go
// 文件: server/middleware/auth_middleware.go
package middleware

import (
	"context"
	"net/http"
	"strings"

	"yourmod/server/auth"
)

type ctxKey string

const claimsKey ctxKey = "claims"

// RequireAuth 是校验 Bearer token 的 HTTP 中间件。
func RequireAuth(v *auth.Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			http.Error(w, "缺少 Bearer 令牌", http.StatusUnauthorized)
			return
		}
		raw := strings.TrimPrefix(h, "Bearer ")
		claims, err := v.Verify(raw)
		if err != nil {
			http.Error(w, "令牌校验失败", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

> 资源回收:服务关闭时调用 `NewVerifier` 传入的 ctx 的 cancel,以结束 keyfunc 的后台刷新 goroutine。

---

## 5. 资料同步: 从 userinfo / id_token 取 name / preferred_username / picture

### 5.1 两个来源 + 标准做法

OIDC 下用户资料有两个权威来源,均需先请求 **`profile`** scope(`email` 另需 `email` scope):

1. **id_token claims**:换码后取 `oauth2Token.Extra("id_token").(string)`,用 `provider.Verifier(...).Verify(ctx, raw)` 验签,再 `idToken.Claims(&dst)` 反序列化。优点:已经在手里,无需额外网络。
2. **UserInfo 端点**:`provider.UserInfo(ctx, oauth2.StaticTokenSource(token))`,然后 `userinfo.Claims(&dst)`。优点:最新、最全;某些 IdP 把 picture 等只放在 userinfo。

go-oidc 的内置 `UserInfo` 结构 **只含** `Subject / Profile / Email / EmailVerified`,**不含** `name / preferred_username / picture`,因此必须用 `Claims(&yourStruct)` 把原始 JSON 反序列化进自定义结构。

`profile` scope 覆盖的标准声明:`name, family_name, given_name, middle_name, nickname, preferred_username, profile, picture, website, gender, birthdate, zoneinfo, locale, updated_at`。

**保持同步的标准做法**:每次登录 **以及每次刷新后**,都重新拉一次 userinfo(或解析新 id_token),把 name/preferred_username/picture 合并写回本地用户记录。这样头像/昵称在 IdP 侧更新后,客户端下次登录或令牌刷新时即可同步。

### 5.2 资料同步代码骨架(go-oidc v3)

```go
// 文件: client/auth/profile.go
package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Profile 是要同步的用户资料(不可变值对象)。
type Profile struct {
	Subject           string `json:"sub"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Picture           string `json:"picture"`
	ProfileURL        string `json:"profile"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
}

// FetchProfile 在登录/刷新后拉取最新资料以保持同步。
// 优先用 UserInfo 端点(最全);可叠加 id_token claims 作为补充。
func FetchProfile(ctx context.Context, issuerURL string, token *oauth2.Token) (*Profile, error) {
	provider, err := oidc.NewProvider(ctx, issuerURL) // 自动发现 .well-known/openid-configuration
	if err != nil {
		return nil, fmt.Errorf("OIDC provider 发现失败: %w", err)
	}

	userInfo, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
	if err != nil {
		return nil, fmt.Errorf("拉取 userinfo 失败: %w", err)
	}

	var p Profile
	if err := userInfo.Claims(&p); err != nil { // 默认结构不含 name/preferred_username/picture
		return nil, fmt.Errorf("解析 userinfo claims 失败: %w", err)
	}
	return &p, nil
}

// ProfileFromIDToken 备用:直接从 id_token 取资料(无需额外网络)。
func ProfileFromIDToken(ctx context.Context, issuerURL, clientID string, token *oauth2.Token) (*Profile, error) {
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("令牌响应缺少 id_token")
	}
	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC provider 发现失败: %w", err)
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: clientID})
	idToken, err := verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("id_token 验签失败: %w", err)
	}
	var p Profile
	if err := idToken.Claims(&p); err != nil {
		return nil, fmt.Errorf("解析 id_token claims 失败: %w", err)
	}
	return &p, nil
}
```

> Scope 配置:客户端 `oauth2.Config.Scopes` 应含 `oidc.ScopeOpenID`(即 `"openid"`)、`"profile"`、`"email"`,以及若要离线刷新加 `"offline_access"`(具体取决于 IdP)。

---

## 6. 端到端串联(客户端)

1. `Login()`(第 1 节)→ 得到 `*oauth2.Token`(含 access/refresh/id_token)。
2. `FetchProfile()`(第 5 节)→ 同步 name/preferred_username/picture 到本地 UI。
3. `SaveToken()`(第 3 节)→ refresh_token 写入 OS 凭据存储。
4. 后续请求用 `persistingTokenSource`(第 2 节)→ 过期自动静默刷新,刷新后 **再次** `FetchProfile()` 保持资料同步,并 `SaveToken()` 回写新 token。
5. 服务端每个受保护请求经 `RequireAuth` 中间件(第 4 节)用 JWKS 本地验签,校验 iss/aud/exp。
6. 登出:`DeleteToken()` 清凭据。

---

## [参考链接]

- golang.org/x/oauth2 文档(PKCE 三件套): https://pkg.go.dev/golang.org/x/oauth2
- x/oauth2 PKCE 支持 issue: https://github.com/golang/go/issues/59835
- x/oauth2 example_test.go: https://github.com/golang/oauth2/blob/master/example_test.go
- x/oauth2 oauth2.go(Token/TokenSource): https://github.com/golang/oauth2/blob/master/oauth2.go
- x/oauth2 releases(v0.30.0): https://github.com/golang/oauth2/releases
- RFC 8252 OAuth 2.0 for Native Apps: https://www.rfc-editor.org/rfc/rfc8252.html
- RFC 7636 PKCE: https://www.rfc-editor.org/rfc/rfc7636.html
- Google OAuth2 for iOS & Desktop(loopback): https://developers.google.com/identity/protocols/oauth2/native-app
- Google Loopback 迁移指南: https://developers.google.com/identity/protocols/oauth2/resources/loopback-migration
- OAuth.com 原生 app 重定向 URL: https://www.oauth.com/oauth2-servers/oauth-native-apps/redirect-urls-for-native-apps/
- MicahParks/keyfunc README: https://github.com/MicahParks/keyfunc/blob/main/README.md
- keyfunc Keycloak 示例: https://github.com/MicahParks/keyfunc/blob/main/examples/keycloak/main.go
- keyfunc releases(v3.8.0): https://github.com/MicahParks/keyfunc/releases
- keyfunc/v3 文档: https://pkg.go.dev/github.com/MicahParks/keyfunc/v3
- MicahParks/jwkset(自动缓存/轮换): https://github.com/MicahParks/jwkset
- golang-jwt/jwt/v5 文档: https://pkg.go.dev/github.com/golang-jwt/jwt/v5
- golang-jwt/jwt releases(v5.3.0): https://github.com/golang-jwt/jwt/releases
- zalando/go-keyring README: https://github.com/zalando/go-keyring/blob/master/README.md
- zalando/go-keyring 文档: https://pkg.go.dev/github.com/zalando/go-keyring
- danieljoos/wincred: https://github.com/danieljoos/wincred
- danieljoos/wincred 文档: https://pkg.go.dev/github.com/danieljoos/wincred
- coreos/go-oidc/v3 文档(UserInfo/Verifier): https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc
- coreos/go-oidc 仓库: https://github.com/coreos/go-oidc
- OpenID Connect Core 标准声明(profile scope): https://openid.net/specs/openid-connect-core-1_0.html#StandardClaims

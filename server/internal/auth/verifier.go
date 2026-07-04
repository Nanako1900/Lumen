// Package auth is the authentication gateway (contract §2, server-design §2).
// It verifies IdP-issued JWT access tokens locally via JWKS, decides owner
// status from configuration, maps claims to profiles, and provides the REST
// Bearer middleware. Both channels (REST middleware, WS handshake) share one
// Verifier.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"lumen/internal/userinfo"
)

// leeway tolerates clock drift between the server and the IdP.
const leeway = 30 * time.Second

// Auth modes select how Verify validates a Bearer token.
const (
	// ModeJWKS validates a JWT access_token offline via JWKS (OIDC providers).
	ModeJWKS = "jwks"
	// ModeUserinfo validates an opaque access_token by calling the IdP userinfo
	// endpoint (plain-OAuth2 providers that issue non-JWT tokens, e.g. Nanako).
	ModeUserinfo = "userinfo"
)

const (
	// userinfoVerifyTimeout bounds the /userinfo call made per token validation.
	userinfoVerifyTimeout = 5 * time.Second
	// userinfoCacheTTL is how long a validated token's identity is trusted before
	// re-checking /userinfo. Short so a revoked token stops working promptly, long
	// enough that bootstrap/me/channels bursts don't hammer the IdP.
	userinfoCacheTTL = 60 * time.Second
	// userinfoCacheMax bounds the cache; on overflow it is reset wholesale (a
	// crude but safe bound for the small-scale single-guild deployment).
	userinfoCacheMax = 4096
)

// Claims are the expected access-token claims (contract §2.3 field mapping).
// RegisteredClaims supplies Subject (sub), Issuer, Audience and ExpiresAt.
type Claims struct {
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Picture           string `json:"picture"`
	jwt.RegisteredClaims
}

// Verifier validates Bearer access tokens. In ModeJWKS it verifies a JWT offline
// against an auto-refreshing JWKS; in ModeUserinfo it introspects an opaque token
// via the IdP userinfo endpoint (with a short-TTL cache). It is constructed once
// at startup and is immutable and concurrency-safe.
type Verifier struct {
	mode string

	// ModeJWKS fields.
	kf       keyfunc.Keyfunc
	issuer   string
	audience string
	leeway   time.Duration

	// ModeUserinfo fields.
	userinfoURL string
	httpClient  *http.Client
	cache       *tokenCache
	logger      *slog.Logger
}

// NewVerifier builds a ModeJWKS Verifier whose background JWKS refresh goroutine
// is bound to ctx (cancel to reclaim it). It fetches, caches and rotates keys
// automatically, refreshing on an unknown kid.
func NewVerifier(ctx context.Context, jwksURL, issuer, audience string) (*Verifier, error) {
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("初始化 JWKS keyfunc 失败: %w", err)
	}
	return &Verifier{mode: ModeJWKS, kf: kf, issuer: issuer, audience: audience, leeway: leeway}, nil
}

// NewUserinfoVerifier builds a ModeUserinfo Verifier that validates opaque tokens
// by calling userinfoURL with the token as a Bearer credential (plain-OAuth2 IdPs
// with no JWKS). Validated identities are cached for userinfoCacheTTL. The logger
// (nil → slog.Default) records the response's field names when a 200 body yields
// no recognizable subject, so an unexpected IdP schema is diagnosable.
func NewUserinfoVerifier(userinfoURL string, logger *slog.Logger) *Verifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Verifier{
		mode:        ModeUserinfo,
		userinfoURL: userinfoURL,
		httpClient:  &http.Client{Timeout: userinfoVerifyTimeout},
		cache:       newTokenCache(userinfoCacheTTL),
		logger:      logger,
		leeway:      leeway,
	}
}

// newVerifierWithKeyfunc lets tests inject an in-process keyfunc.
func newVerifierWithKeyfunc(kf keyfunc.Keyfunc, issuer, audience string) *Verifier {
	return &Verifier{mode: ModeJWKS, kf: kf, issuer: issuer, audience: audience, leeway: leeway}
}

// NewVerifierFromJWKSetJSON builds a Verifier from a static JWK Set JSON. It is
// primarily used by tests (and any offline-keys scenario); the ctx is accepted
// for API symmetry with NewVerifier though this path does no background refresh.
func NewVerifierFromJWKSetJSON(_ context.Context, jwkSetJSON []byte, issuer, audience string) (*Verifier, error) {
	kf, err := keyfunc.NewJWKSetJSON(jwkSetJSON)
	if err != nil {
		return nil, fmt.Errorf("从 JWK Set 构造验签器失败: %w", err)
	}
	return newVerifierWithKeyfunc(kf, issuer, audience), nil
}

// Verify validates a Bearer token and returns the derived Claims. ModeUserinfo
// introspects via the userinfo endpoint; ModeJWKS verifies the JWT offline.
func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	if v.mode == ModeUserinfo {
		return v.verifyUserinfo(tokenString)
	}
	return v.verifyJWT(tokenString)
}

// verifyJWT checks the signature and iss/aud/exp, returning the validated Claims.
// It enforces RS256 only (defeating alg-confusion / "none" attacks). Callers
// use IsExpired on the returned error to distinguish an expired token.
func (v *Verifier) verifyJWT(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(
		tokenString, claims, v.kf.Keyfunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(v.leeway),
	)
	if err != nil {
		return nil, fmt.Errorf("JWT 校验失败: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("JWT 无效")
	}
	return claims, nil
}

// IsExpired reports whether err denotes an expired token, so callers can map
// to TOKEN_EXPIRED rather than TOKEN_INVALID (server-design §2.1 table).
func IsExpired(err error) bool {
	return errors.Is(err, jwt.ErrTokenExpired)
}

// verifyUserinfo validates an opaque token by calling the userinfo endpoint with
// it as a Bearer credential. A 200 means the token is live: the response is
// mapped to Claims (Subject/Name/Picture) via the flexible userinfo parser. A
// 401/403 (or any non-200) means the token is invalid/expired. Results are cached
// for userinfoCacheTTL keyed by the token's SHA-256 so request bursts don't
// hammer the IdP. Unlike ModeJWKS this cannot distinguish expiry from revocation,
// so all rejections surface as TOKEN_INVALID.
func (v *Verifier) verifyUserinfo(token string) (*Claims, error) {
	if c := v.cache.get(token); c != nil {
		return c, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), userinfoVerifyTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.userinfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 userinfo 请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("userinfo 拒绝令牌（无效或已过期）")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo 返回非 200: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("读取 userinfo 响应失败: %w", err)
	}
	info, err := userinfo.Parse(body)
	if err != nil {
		return nil, err
	}
	if info.Subject == "" {
		// 200 but no recognizable id: surface the field names (not values) so an
		// unexpected IdP schema can be mapped without guesswork.
		v.logger.Warn("userinfo 返回 200 但未识别到用户唯一标识；请对照实际字段名",
			"present_keys", userinfo.Keys(body))
		return nil, errors.New("userinfo 响应缺少用户唯一标识（sub/id/user_id 等）")
	}

	claims := &Claims{
		Name:    info.DisplayName,
		Picture: info.AvatarURL,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: info.Subject,
		},
	}
	v.cache.put(token, claims)
	return claims, nil
}

// tokenCache is a small TTL cache of validated userinfo identities, keyed by the
// SHA-256 of the raw token so plaintext tokens are not held as map keys.
type tokenCache struct {
	mu  sync.Mutex
	m   map[string]cacheEntry
	ttl time.Duration
	now func() time.Time // injectable for tests
}

type cacheEntry struct {
	claims *Claims
	exp    time.Time
}

func newTokenCache(ttl time.Duration) *tokenCache {
	return &tokenCache{m: make(map[string]cacheEntry), ttl: ttl, now: time.Now}
}

func (c *tokenCache) key(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (c *tokenCache) get(token string) *Claims {
	k := c.key(token)
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok {
		return nil
	}
	if c.now().After(e.exp) {
		delete(c.m, k)
		return nil
	}
	return e.claims
}

func (c *tokenCache) put(token string, claims *Claims) {
	k := c.key(token)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= userinfoCacheMax {
		c.m = make(map[string]cacheEntry)
	}
	c.m[k] = cacheEntry{claims: claims, exp: c.now().Add(c.ttl)}
}

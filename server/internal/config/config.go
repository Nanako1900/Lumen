// Package config loads and validates all runtime configuration from
// environment variables (contract §1.3, server-design §6.1). Missing required
// values cause a fail-fast error listing every offending key.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"lumen/internal/secure"
)

// Default values for optional settings.
const (
	defaultUpdatesDir = "/app/updates"
	defaultLogLevel   = "info"
)

// Auth modes (LUMEN_AUTH_MODE) select how the resource server validates tokens.
const (
	// AuthModeJWKS (default) verifies a JWT access_token offline via JWKS — for
	// OIDC providers that issue signed JWTs and expose a JWKS endpoint.
	AuthModeJWKS = "jwks"
	// AuthModeUserinfo introspects an opaque access_token via the IdP userinfo
	// endpoint — for plain-OAuth2 providers (no openid scope / no JWKS), e.g.
	// Nanako OAuth.
	AuthModeUserinfo = "userinfo"
)

// Default OAuth scopes per mode. OIDC needs openid (+ offline_access for a
// desktop refresh_token); plain OAuth2 providers reject openid, so userinfo mode
// defaults to profile+email. Both are overridable via env.
const (
	defaultWebScopeOIDC       = "openid profile email"
	defaultDesktopScopeOIDC   = "openid profile email offline_access"
	defaultWebScopeOAuth2     = "profile email"
	defaultDesktopScopeOAuth2 = "profile email"
)

// Config is the immutable runtime configuration. Once loaded it is never
// mutated; callers treat it as read-only.
type Config struct {
	// AuthMode selects token validation (AuthModeJWKS default | AuthModeUserinfo).
	AuthMode string

	OAuthIssuer      string
	OAuthJWKSURL     string
	OAuthUserinfoURL string // optional in jwks mode; REQUIRED in userinfo mode
	OAuthAudience    string
	OwnerSubjects    []string

	// OAuth scopes sent to the IdP authorize endpoint. Defaults depend on
	// AuthMode (openid… for jwks, profile email for userinfo); env-overridable.
	OAuthWebScope     string
	OAuthDesktopScope string
	ListenAddr        string
	DatabaseURL       string
	PublicIP          string
	WebRTCUDPPort     int
	PublicWSURL       string // optional; derived from Host header when empty
	UpdatesDir        string
	LogLevel          string

	// --- Account center / desktop broker (web-design.md §5, §6, §9) ---
	// These back the account center / desktop login broker on the Go server. The confidential
	// OIDC client credentials and both AES keys are required; the authorize /
	// token / userinfo URLs are optional and derived from OIDC discovery when
	// empty (mirroring env.ts + oidc.ts).
	OAuthClientID        string
	OAuthClientSecret    string // secret: never logged or echoed
	OAuthAuthorizeURL    string // optional; derived from discovery when empty
	OAuthTokenURL        string // optional; derived from discovery when empty
	OAuthDesktopRedirect string
	OAuthWebRedirect     string
	WebBaseURL           string

	// Decoded 32-byte AES-256-GCM keys (validated at Load; distinct keys).
	// sessionEncKey seals the account-center cookies (lumen_auth_flow,
	// lumen_session); refreshEncKey encrypts refresh_token at rest.
	sessionEncKey []byte
	refreshEncKey []byte
}

// SessionEncKey returns a defensive copy of the decoded 32-byte session cookie
// encryption key (LUMEN_SESSION_ENC_KEY).
func (c Config) SessionEncKey() []byte {
	return cloneKey(c.sessionEncKey)
}

// RefreshEncKey returns a defensive copy of the decoded 32-byte refresh_token
// at-rest encryption key (LUMEN_REFRESH_ENC_KEY).
func (c Config) RefreshEncKey() []byte {
	return cloneKey(c.refreshEncKey)
}

func cloneKey(k []byte) []byte {
	if k == nil {
		return nil
	}
	out := make([]byte, len(k))
	copy(out, k)
	return out
}

// Load reads configuration from the environment and validates required keys.
// On any missing or invalid required value it returns an aggregated error
// (fail-fast) and a zero Config.
func Load() (Config, error) {
	var missing []string
	get := func(key string, required bool) string {
		v := strings.TrimSpace(os.Getenv(key))
		if required && v == "" {
			missing = append(missing, key)
		}
		return v
	}

	// Auth mode is read first: it decides which OIDC/OAuth2 keys are required and
	// which scope defaults apply. Unknown values are reported like a missing key.
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("LUMEN_AUTH_MODE")))
	if mode == "" {
		mode = AuthModeJWKS
	}
	if mode != AuthModeJWKS && mode != AuthModeUserinfo {
		missing = append(missing, "LUMEN_AUTH_MODE(无效: 取值 jwks|userinfo)")
	}
	userinfoMode := mode == AuthModeUserinfo

	c := Config{
		AuthMode: mode,
		// jwks mode verifies JWT via JWKS (needs issuer/jwks/aud). userinfo mode
		// introspects via the userinfo endpoint (needs userinfo/authorize/token
		// URLs explicitly, since a plain-OAuth2 IdP has no OIDC discovery).
		OAuthIssuer:      get("LUMEN_OAUTH_ISSUER", !userinfoMode),
		OAuthJWKSURL:     get("LUMEN_OAUTH_JWKS_URL", !userinfoMode),
		OAuthUserinfoURL: get("LUMEN_OAUTH_USERINFO_URL", userinfoMode),
		OAuthAudience:    get("LUMEN_OAUTH_AUDIENCE", !userinfoMode),
		OwnerSubjects:    splitCSV(get("LUMEN_OWNER_SUBJECTS", true)),
		ListenAddr:       get("LUMEN_LISTEN_ADDR", true),
		DatabaseURL:      get("LUMEN_DATABASE_URL", true),
		PublicIP:         get("LUMEN_PUBLIC_IP", true),
		PublicWSURL:      get("LUMEN_PUBLIC_WS_URL", false),
		UpdatesDir:       orDefault(get("LUMEN_UPDATES_DIR", false), defaultUpdatesDir),
		LogLevel:         orDefault(get("LUMEN_LOG_LEVEL", false), defaultLogLevel),

		// Account center / desktop broker. client_secret, both redirect URIs
		// and web base are always required. authorize/token URLs are derived from
		// OIDC discovery when empty in jwks mode, but REQUIRED in userinfo mode
		// (no discovery on a plain-OAuth2 IdP).
		OAuthClientID:        get("LUMEN_OAUTH_CLIENT_ID", true),
		OAuthClientSecret:    get("LUMEN_OAUTH_CLIENT_SECRET", true),
		OAuthAuthorizeURL:    get("LUMEN_OAUTH_AUTHORIZE_URL", userinfoMode),
		OAuthTokenURL:        get("LUMEN_OAUTH_TOKEN_URL", userinfoMode),
		OAuthDesktopRedirect: get("LUMEN_OAUTH_DESKTOP_REDIRECT_URI", true),
		OAuthWebRedirect:     get("LUMEN_OAUTH_WEB_REDIRECT_URI", true),
		WebBaseURL:           get("LUMEN_WEB_BASE_URL", true),
	}

	// Scope defaults depend on mode; env overrides win.
	c.OAuthWebScope = orDefault(get("LUMEN_OAUTH_WEB_SCOPE", false),
		pick(userinfoMode, defaultWebScopeOAuth2, defaultWebScopeOIDC))
	c.OAuthDesktopScope = orDefault(get("LUMEN_OAUTH_DESKTOP_SCOPE", false),
		pick(userinfoMode, defaultDesktopScopeOAuth2, defaultDesktopScopeOIDC))

	portStr := get("LUMEN_WEBRTC_UDP_PORT", true)
	if portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			missing = append(missing, "LUMEN_WEBRTC_UDP_PORT(无效)")
		} else {
			c.WebRTCUDPPort = port
		}
	}

	// AES keys: required, must base64-decode to exactly 32 bytes, and must be
	// distinct (decision 2). Validated with the same aggregation style so a
	// single Load reports every offending key at once.
	c.sessionEncKey = decodeAESKey(get("LUMEN_SESSION_ENC_KEY", true), "LUMEN_SESSION_ENC_KEY", &missing)
	c.refreshEncKey = decodeAESKey(get("LUMEN_REFRESH_ENC_KEY", true), "LUMEN_REFRESH_ENC_KEY", &missing)
	if c.sessionEncKey != nil && c.refreshEncKey != nil && bytesEqual(c.sessionEncKey, c.refreshEncKey) {
		missing = append(missing, "LUMEN_REFRESH_ENC_KEY(必须与 LUMEN_SESSION_ENC_KEY 不同)")
	}

	if len(missing) > 0 {
		return Config{}, fmt.Errorf("缺失/无效环境变量: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

// decodeAESKey validates a required base64 AES-256 key. An empty value is
// already recorded by the required get(); a non-empty but invalid value adds an
// "(无效)" entry. Returns nil on any failure so the caller can skip the
// distinctness check.
func decodeAESKey(value, key string, missing *[]string) []byte {
	if value == "" {
		return nil // already reported as missing by get(..., true)
	}
	b, err := secure.DecodeKey(value)
	if err != nil {
		*missing = append(*missing, key+"(无效: 必须 base64 解码为 32 字节)")
		return nil
	}
	return b
}

// bytesEqual reports byte-slice equality (small keys; constant time not
// required for a config-time distinctness check).
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// splitCSV splits a comma-separated list, trimming whitespace and dropping
// empty entries.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// orDefault returns v when non-empty, otherwise def.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// pick returns a when cond is true, else b (a tiny string ternary).
func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

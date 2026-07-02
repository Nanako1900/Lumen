package broker

import (
	"encoding/json"
	"net/http"
	"time"

	"lumen/internal/secure"
)

// Account-center cookies (decision 1). Both are host-only (NO Domain attribute),
// HttpOnly + Secure + SameSite=Lax + Path=/, sealed with AES-256-GCM under
// LUMEN_SESSION_ENC_KEY.
const (
	// sessionCookie holds the account-center web session (sub + profile).
	sessionCookie = "lumen_session"
	// authFlowCookie holds the short-lived OIDC login flow context (verifier +
	// state) for the web login (/auth/login → /auth/callback).
	authFlowCookie = "lumen_auth_flow"

	// sessionMaxAge is the web session lifetime (8h, mirrors session.ts).
	sessionMaxAge = 60 * 60 * 8
	// authFlowMaxAge is the login-flow context lifetime (10m, mirrors session.ts).
	authFlowMaxAge = 600
)

// webSession is the sealed account-center session payload (mirrors session.ts
// WebSession). exp is the server-authoritative expiry in epoch seconds.
type webSession struct {
	Sub         string `json:"sub"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
	Exp         int64  `json:"exp"`
}

// authFlowContext is the sealed OIDC login-flow context (mirrors session.ts
// AuthFlowContext). It carries the PKCE verifier and state across the redirect
// to the IdP and back.
type authFlowContext struct {
	Verifier string `json:"verifier"`
	State    string `json:"state"`
	Exp      int64  `json:"exp"`
}

// nowSeconds returns the current epoch second (server-authoritative time).
func nowSeconds() int64 { return time.Now().Unix() }

// --- account-center session (lumen_session) ---

// sealSession seals a webSession into a cookie value (mirrors session.ts
// sealSession).
func sealSession(sealer *secure.Sealer, s webSession) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return sealer.Seal(raw)
}

// openSession opens and validates a sealed session cookie value, returning nil
// on any malformed / tampered / expired value (mirrors session.ts openSession
// returning null).
func openSession(sealer *secure.Sealer, value string) *webSession {
	plain, err := sealer.Open(value)
	if err != nil {
		return nil
	}
	var s webSession
	if err := json.Unmarshal(plain, &s); err != nil {
		return nil
	}
	if s.Exp <= nowSeconds() {
		return nil
	}
	return &s
}

// readSessionCookie returns the raw sealed value of the session cookie, or ""
// (mirrors session.ts readSessionCookie).
func readSessionCookie(r *http.Request) string {
	return readCookie(r, sessionCookie)
}

// buildSessionCookie constructs a host-only Set-Cookie for the session
// (HttpOnly + Secure + SameSite=Lax + Path=/, NO Domain), mirroring session.ts
// buildSessionCookie.
func buildSessionCookie(value string) *http.Cookie {
	return newHostCookie(sessionCookie, value, sessionMaxAge)
}

// clearSessionCookie constructs a host-only Set-Cookie that clears the session
// (Max-Age=0), mirroring session.ts clearSessionCookie.
func clearSessionCookie() *http.Cookie {
	return newHostCookie(sessionCookie, "", 0)
}

// defaultSessionExp returns the authoritative session expiry (now + 8h).
func defaultSessionExp() int64 { return nowSeconds() + sessionMaxAge }

// --- OIDC login flow context (lumen_auth_flow) ---

// sealAuthFlow seals an authFlowContext into a cookie value (mirrors session.ts
// sealAuthFlow, which reuses sealSession).
func sealAuthFlow(sealer *secure.Sealer, ctx authFlowContext) (string, error) {
	raw, err := json.Marshal(ctx)
	if err != nil {
		return "", err
	}
	return sealer.Seal(raw)
}

// openAuthFlow opens a sealed auth-flow cookie value, returning nil on any
// malformed / tampered / expired value or when verifier/state are absent
// (mirrors session.ts openAuthFlow). The expiry guard reuses the same exp field
// as the session payload.
func openAuthFlow(sealer *secure.Sealer, value string) *authFlowContext {
	plain, err := sealer.Open(value)
	if err != nil {
		return nil
	}
	var ctx authFlowContext
	if err := json.Unmarshal(plain, &ctx); err != nil {
		return nil
	}
	if ctx.Verifier == "" || ctx.State == "" {
		return nil
	}
	if ctx.Exp <= nowSeconds() {
		return nil
	}
	return &ctx
}

// readAuthFlowCookie returns the raw sealed value of the auth-flow cookie, or ""
// (mirrors session.ts readAuthFlowCookie).
func readAuthFlowCookie(r *http.Request) string {
	return readCookie(r, authFlowCookie)
}

// buildAuthFlowCookie constructs a host-only Set-Cookie for the login flow
// (mirrors session.ts buildAuthFlowCookie).
func buildAuthFlowCookie(value string) *http.Cookie {
	return newHostCookie(authFlowCookie, value, authFlowMaxAge)
}

// clearAuthFlowCookie constructs a host-only Set-Cookie that clears the login
// flow (mirrors session.ts clearAuthFlowCookie).
func clearAuthFlowCookie() *http.Cookie {
	return newHostCookie(authFlowCookie, "", 0)
}

// defaultAuthFlowExp returns the login-flow context expiry (now + 10m).
func defaultAuthFlowExp() int64 { return nowSeconds() + authFlowMaxAge }

// --- shared cookie helpers ---

// newHostCookie builds a host-only cookie (decision 1): HttpOnly + Secure +
// SameSite=Lax + Path=/ and, crucially, NO Domain attribute so it is never sent
// to sibling subdomains. A negative maxAge (via Max-Age=0) clears the cookie.
func newHostCookie(name, value string, maxAge int) *http.Cookie {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		// Domain intentionally left empty: host-only cookie (never Domain=.x.com).
	}
	if maxAge > 0 {
		c.MaxAge = maxAge
	} else {
		// Max-Age=0 clears the cookie. http.Cookie encodes MaxAge<0 as
		// "Max-Age=0", which is the exact clear semantics we want.
		c.MaxAge = -1
	}
	return c
}

// readCookie returns the value of the named cookie, or "" when absent.
func readCookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

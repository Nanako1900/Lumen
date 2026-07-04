package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	goidc "github.com/coreos/go-oidc/v3/oidc"

	"lumen/internal/store"
	"lumen/internal/userinfo"
)

// oidcTimeout bounds every outbound IdP call (token / userinfo) so a slow IdP
// never blocks a login (mirrors the userinfo timeout in internal/auth).
const oidcTimeout = 10 * time.Second

// oidcConfig is the immutable subset of settings the OIDC client needs. It is
// populated from config.Config; the authorize/token/userinfo URLs are filled
// from OIDC discovery when the corresponding env override is empty (decision 9,
// mirrors env.ts + oidc.ts).
type oidcConfig struct {
	AuthorizeURL string
	TokenURL     string
	UserinfoURL  string
	ClientID     string
	ClientSecret string // secret: only ever placed in outbound form bodies
	Audience     string
}

// oidcClient performs OIDC interactions: authorize URL construction,
// authorization_code / refresh_token exchange, userinfo, and JWT payload parsing
// (mirrors _lib/oidc.ts). client_secret is used only inside this type and never
// appears in any response or URL.
type oidcClient struct {
	cfg  oidcConfig
	http *http.Client
}

// newOIDCClient builds an oidcClient, filling any empty endpoint from OIDC
// discovery against issuer. Discovery is only attempted when at least one
// endpoint is missing, so a fully-configured deployment needs no network at
// startup (mirrors env.ts: URLs optional, derived from discovery when empty).
func newOIDCClient(ctx context.Context, issuer string, cfg oidcConfig) (*oidcClient, error) {
	if cfg.AuthorizeURL == "" || cfg.TokenURL == "" || cfg.UserinfoURL == "" {
		provider, err := goidc.NewProvider(ctx, issuer)
		if err != nil {
			return nil, fmt.Errorf("OIDC discovery 失败: %w", err)
		}
		ep := provider.Endpoint()
		if cfg.AuthorizeURL == "" {
			cfg.AuthorizeURL = ep.AuthURL
		}
		if cfg.TokenURL == "" {
			cfg.TokenURL = ep.TokenURL
		}
		if cfg.UserinfoURL == "" {
			cfg.UserinfoURL = provider.UserInfoEndpoint()
		}
	}
	return &oidcClient{cfg: cfg, http: &http.Client{Timeout: oidcTimeout}}, nil
}

// newOIDCClientRaw builds an oidcClient from fully-specified endpoints without
// discovery. It is used by tests (httptest IdP stubs) and by callers that always
// configure explicit URLs.
func newOIDCClientRaw(cfg oidcConfig) *oidcClient {
	return &oidcClient{cfg: cfg, http: &http.Client{Timeout: oidcTimeout}}
}

// tokenResponse is the IdP token endpoint response (mirrors oidc.ts
// TokenResponse). Only access_token is required.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    any    `json:"expires_in"` // number|string per IdP; normalized later
	TokenType    string `json:"token_type"`
}

// authorizeParams are the inputs to buildAuthorizeURL (mirrors oidc.ts
// AuthorizeParams).
type authorizeParams struct {
	CodeChallenge string // S256(oidc_verifier)
	State         string // oidc_state
	RedirectURI   string
	Scope         string
	Audience      string // optional; set aud=lumen-api on the access_token
}

// buildAuthorizeURL constructs the IdP /authorize 302 target (Auth Code + PKCE
// S256), mirroring oidc.ts buildAuthorizeUrl. When audience is set it is sent as
// both `audience` and `resource` (Auth0/Logto style; Keycloak ignores them and
// uses an audience mapper instead).
func (c *oidcClient) buildAuthorizeURL(p authorizeParams) (string, error) {
	u, err := url.Parse(c.cfg.AuthorizeURL)
	if err != nil {
		return "", fmt.Errorf("authorize URL 无效: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", c.cfg.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", p.Scope)
	q.Set("state", p.State)
	q.Set("code_challenge", p.CodeChallenge)
	q.Set("code_challenge_method", "S256")
	if p.Audience != "" {
		q.Set("audience", p.Audience)
		q.Set("resource", p.Audience)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// exchangeAuthCode swaps an authorization code for tokens using client_secret +
// the PKCE verifier (grant_type=authorization_code), mirroring oidc.ts
// exchangeAuthCode. Returns nil on any network error, non-2xx, or malformed
// response so the caller can degrade to an error redirect.
func (c *oidcClient) exchangeAuthCode(ctx context.Context, code, codeVerifier, redirectURI string) *tokenResponse {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"code_verifier": {codeVerifier},
	}
	return c.postToken(ctx, form)
}

// refresh swaps a refresh_token for fresh tokens (grant_type=refresh_token),
// mirroring oidc.ts refreshWithIdp.
func (c *oidcClient) refresh(ctx context.Context, refreshToken string) *tokenResponse {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
	}
	return c.postToken(ctx, form)
}

// postToken POSTs a form to the token endpoint and decodes the response,
// mirroring oidc.ts postToken: nil on network error, non-2xx, malformed JSON, or
// an empty access_token.
func (c *oidcClient) postToken(ctx context.Context, form url.Values) *tokenResponse {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil // network error
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil // IdP rejected (4xx/5xx)
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil
	}
	if tr.AccessToken == "" {
		return nil
	}
	return &tr
}

// subjectFrom parses sub from the first JWT whose payload carries one, preferring
// the id_token over the access_token (mirrors oidc.ts subjectFrom). The payload
// is parsed WITHOUT signature verification (decision 8): it comes directly from
// the IdP over TLS with client_secret, and the resource-server path keeps using
// the internal/auth Verifier. Returns "" when no sub is found.
func subjectFrom(jwts ...string) string {
	for _, j := range jwts {
		if sub := subFromJWT(j); sub != "" {
			return sub
		}
	}
	return ""
}

func subFromJWT(jwt string) string {
	claims := claimsFromJWT(jwt)
	if claims == nil {
		return ""
	}
	if sub, ok := claims["sub"].(string); ok {
		return sub
	}
	return ""
}

// claimsFromJWT decodes a JWT payload segment to a claims map WITHOUT verifying
// the signature (decision 8). Returns nil on any malformed input.
func claimsFromJWT(jwt string) map[string]any {
	if jwt == "" {
		return nil
	}
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return nil
	}
	// JWT segments are base64url without padding (RawURLEncoding), but some IdPs
	// emit padded segments; accept both like the TS atob-with-repad path.
	seg := parts[1]
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		if raw, err = base64.URLEncoding.DecodeString(padBase64(seg)); err != nil {
			return nil
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil
	}
	return claims
}

// padBase64 re-adds '=' padding for the std URL alphabet decoder.
func padBase64(s string) string {
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return s
}

// profileFromJWT extracts a display profile from the first JWT whose claims yield
// one (mirrors oidc.ts profileFromJwt). Returns the zero profile when none do.
func profileFromJWT(jwts ...string) store.DesktopProfile {
	for _, j := range jwts {
		claims := claimsFromJWT(j)
		if claims == nil {
			continue
		}
		p := profileFromClaims(claims)
		if p.DisplayName != "" || p.AvatarURL != "" {
			return p
		}
	}
	return store.DesktopProfile{}
}

// profileFromClaims normalizes OIDC standard claims into {display_name,
// avatar_url} (mirrors oidc.ts profileFromClaims): display_name from name →
// preferred_username → nickname; avatar_url from picture.
func profileFromClaims(claims map[string]any) store.DesktopProfile {
	str := func(k string) string {
		if v, ok := claims[k].(string); ok {
			return v
		}
		return ""
	}
	name := str("name")
	if name == "" {
		name = str("preferred_username")
	}
	if name == "" {
		name = str("nickname")
	}
	return store.DesktopProfile{DisplayName: name, AvatarURL: str("picture")}
}

// fetchIdentity resolves both the subject and the display profile for a token
// pair. It prefers JWT claims (OIDC id_token / JWT access_token); when the token
// carries no parseable sub (plain-OAuth2 opaque tokens, e.g. Nanako OAuth) it
// derives sub + profile from /userinfo via the flexible parser. Returns an empty
// sub only when neither source yields one — the caller decides how to handle it.
func (c *oidcClient) fetchIdentity(ctx context.Context, accessToken, idToken string) (string, store.DesktopProfile) {
	if sub := subjectFrom(idToken, accessToken); sub != "" {
		p := profileFromJWT(idToken, accessToken)
		if p.DisplayName != "" && p.AvatarURL != "" {
			return sub, p
		}
		u := c.fetchUserinfoInfo(ctx, accessToken)
		return sub, store.DesktopProfile{
			DisplayName: firstNonEmpty(p.DisplayName, u.DisplayName),
			AvatarURL:   firstNonEmpty(p.AvatarURL, u.AvatarURL),
		}
	}
	// No JWT subject: opaque token — everything comes from userinfo.
	u := c.fetchUserinfoInfo(ctx, accessToken)
	return u.Subject, store.DesktopProfile{DisplayName: u.DisplayName, AvatarURL: u.AvatarURL}
}

// fetchUserinfoInfo calls the userinfo endpoint and returns the flexibly-parsed
// identity (sub/name/avatar/email), or a zero Info on any failure or when no
// userinfo URL is configured.
func (c *oidcClient) fetchUserinfoInfo(ctx context.Context, accessToken string) userinfo.Info {
	if c.cfg.UserinfoURL == "" {
		return userinfo.Info{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.UserinfoURL, nil)
	if err != nil {
		return userinfo.Info{}
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return userinfo.Info{}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return userinfo.Info{}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return userinfo.Info{}
	}
	info, err := userinfo.Parse(body)
	if err != nil {
		return userinfo.Info{}
	}
	return info
}

// fetchProfile resolves the display profile, preferring id_token/access_token
// claims and falling back to /userinfo (mirrors oidc.ts fetchProfile). Any
// failure degrades to empty fields rather than blocking login.
func (c *oidcClient) fetchProfile(ctx context.Context, accessToken, idToken string) store.DesktopProfile {
	fromJWT := profileFromJWT(idToken, accessToken)
	if fromJWT.DisplayName != "" && fromJWT.AvatarURL != "" {
		return fromJWT
	}
	fromUserinfo := c.fetchUserinfo(ctx, accessToken)
	return store.DesktopProfile{
		DisplayName: firstNonEmpty(fromJWT.DisplayName, fromUserinfo.DisplayName),
		AvatarURL:   firstNonEmpty(fromJWT.AvatarURL, fromUserinfo.AvatarURL),
	}
}

// fetchUserinfo calls the userinfo endpoint with the access token, returning the
// zero profile on any failure (mirrors oidc.ts fetchUserinfo). No-op when no
// userinfo URL is configured.
func (c *oidcClient) fetchUserinfo(ctx context.Context, accessToken string) store.DesktopProfile {
	if c.cfg.UserinfoURL == "" {
		return store.DesktopProfile{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.UserinfoURL, nil)
	if err != nil {
		return store.DesktopProfile{}
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return store.DesktopProfile{}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return store.DesktopProfile{}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return store.DesktopProfile{}
	}
	var claims map[string]any
	if err := json.Unmarshal(body, &claims); err != nil {
		return store.DesktopProfile{}
	}
	return profileFromClaims(claims)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

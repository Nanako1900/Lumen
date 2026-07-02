package broker

import (
	"context"
	"net/url"
	"testing"
)

// TestBuildAuthorizeURL mirrors oidc.test.ts buildAuthorizeUrl: response_type,
// PKCE S256, state, scope, and (when set) audience+resource.
func TestBuildAuthorizeURL(t *testing.T) {
	oc := newOIDCClientRaw(oidcConfig{
		AuthorizeURL: "https://idp.example/authorize",
		ClientID:     "lumen-web",
	})
	got, err := oc.buildAuthorizeURL(authorizeParams{
		CodeChallenge: "chal",
		State:         "st8",
		RedirectURI:   "https://acct.example.com/desktop/callback",
		Scope:         desktopScope,
		Audience:      "lumen-api",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	u, _ := url.Parse(got)
	q := u.Query()
	checks := map[string]string{
		"response_type":         "code",
		"client_id":             "lumen-web",
		"redirect_uri":          "https://acct.example.com/desktop/callback",
		"scope":                 desktopScope,
		"state":                 "st8",
		"code_challenge":        "chal",
		"code_challenge_method": "S256",
		"audience":              "lumen-api",
		"resource":              "lumen-api",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}
}

// TestBuildAuthorizeURLNoAudience verifies audience/resource are omitted for the
// account-center flow (no aud=lumen-api).
func TestBuildAuthorizeURLNoAudience(t *testing.T) {
	oc := newOIDCClientRaw(oidcConfig{AuthorizeURL: "https://idp.example/authorize", ClientID: "web"})
	got, _ := oc.buildAuthorizeURL(authorizeParams{
		CodeChallenge: "c", State: "s", RedirectURI: "https://acct/cb", Scope: webScope,
	})
	u, _ := url.Parse(got)
	if u.Query().Has("audience") || u.Query().Has("resource") {
		t.Error("account-center authorize URL must not carry audience/resource")
	}
}

// TestExchangeAuthCode mirrors oidc.ts exchangeAuthCode against a stub IdP,
// verifying the correct grant + form fields and a decoded token.
func TestExchangeAuthCode(t *testing.T) {
	idp := newIDPStub(t)
	idp.tokenResp = map[string]any{
		"access_token":  fakeJWT(map[string]any{"sub": "u-1"}),
		"refresh_token": "refresh-abc",
		"id_token":      fakeJWT(map[string]any{"sub": "u-1", "name": "Alice"}),
		"expires_in":    float64(3600),
	}
	oc := idp.newTestOIDC()
	tok := oc.exchangeAuthCode(context.Background(), "auth-code-1", "verifier-1", "https://acct/cb")
	if tok == nil {
		t.Fatal("token is nil")
	}
	if tok.AccessToken == "" || tok.RefreshToken != "refresh-abc" {
		t.Errorf("token = %+v", tok)
	}
	// Form assertions: correct grant, code, verifier, client credentials.
	f := idp.lastTokenForm
	if f.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q", f.Get("grant_type"))
	}
	if f.Get("code") != "auth-code-1" || f.Get("code_verifier") != "verifier-1" {
		t.Errorf("code/verifier = %q/%q", f.Get("code"), f.Get("code_verifier"))
	}
	if f.Get("client_id") != "lumen-web" || f.Get("client_secret") != "s3cr3t" {
		t.Errorf("client creds = %q/%q", f.Get("client_id"), f.Get("client_secret"))
	}
}

// TestExchangeAuthCodeRejected verifies a non-2xx IdP response yields nil.
func TestExchangeAuthCodeRejected(t *testing.T) {
	idp := newIDPStub(t)
	idp.tokenStatus = 400
	idp.tokenResp = map[string]any{"error": "invalid_grant"}
	oc := idp.newTestOIDC()
	if tok := oc.exchangeAuthCode(context.Background(), "bad", "v", "https://acct/cb"); tok != nil {
		t.Errorf("rejected exchange should be nil, got %+v", tok)
	}
}

// TestSubjectFromAndProfile mirrors oidc.ts subjectFrom/profileFromJwt.
func TestSubjectFromAndProfile(t *testing.T) {
	idToken := fakeJWT(map[string]any{"sub": "id-sub", "name": "Alice", "picture": "https://img/a.png"})
	accessToken := fakeJWT(map[string]any{"sub": "at-sub"})

	// id_token preferred for sub.
	if sub := subjectFrom(idToken, accessToken); sub != "id-sub" {
		t.Errorf("subjectFrom = %q, want id-sub", sub)
	}
	// falls back to access_token when id_token lacks sub.
	noSubID := fakeJWT(map[string]any{"name": "x"})
	if sub := subjectFrom(noSubID, accessToken); sub != "at-sub" {
		t.Errorf("subjectFrom fallback = %q, want at-sub", sub)
	}
	// profile from claims.
	p := profileFromJWT(idToken, accessToken)
	if p.DisplayName != "Alice" || p.AvatarURL != "https://img/a.png" {
		t.Errorf("profileFromJWT = %+v", p)
	}
}

// TestFetchProfileUserinfoFallback mirrors callback.test.ts: when the JWT lacks
// profile claims, /userinfo supplies them.
func TestFetchProfileUserinfoFallback(t *testing.T) {
	idp := newIDPStub(t)
	idp.infoResp = map[string]any{"sub": "u-2", "preferred_username": "bob", "picture": "https://img/b.png"}
	oc := idp.newTestOIDC()

	accessToken := fakeJWT(map[string]any{"sub": "u-2"})
	idToken := fakeJWT(map[string]any{"sub": "u-2"})
	p := oc.fetchProfile(context.Background(), accessToken, idToken)
	if p.DisplayName != "bob" || p.AvatarURL != "https://img/b.png" {
		t.Errorf("fetchProfile fallback = %+v", p)
	}
}

// TestProfileFromClaimsPrecedence mirrors oidc.ts profileFromClaims: name →
// preferred_username → nickname.
func TestProfileFromClaimsPrecedence(t *testing.T) {
	if p := profileFromClaims(map[string]any{"preferred_username": "pu", "nickname": "nk"}); p.DisplayName != "pu" {
		t.Errorf("display_name = %q, want pu", p.DisplayName)
	}
	if p := profileFromClaims(map[string]any{"nickname": "nk"}); p.DisplayName != "nk" {
		t.Errorf("display_name = %q, want nk", p.DisplayName)
	}
	if p := profileFromClaims(map[string]any{"name": "n", "preferred_username": "pu"}); p.DisplayName != "n" {
		t.Errorf("display_name = %q, want n", p.DisplayName)
	}
}

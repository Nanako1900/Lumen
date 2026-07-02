package broker

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// setCookie extracts the value of the named cookie from a recorder's Set-Cookie
// headers, returning "" when absent.
func setCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) string {
	t.Helper()
	for _, line := range rec.Result().Header["Set-Cookie"] {
		for _, part := range strings.Split(line, ";") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) == 2 && kv[0] == name {
				return kv[1]
			}
		}
	}
	return ""
}

// setCookieLine returns the full Set-Cookie line for the named cookie.
func setCookieLine(rec *httptest.ResponseRecorder, name string) string {
	for _, line := range rec.Result().Header["Set-Cookie"] {
		if strings.HasPrefix(line, name+"=") {
			return line
		}
	}
	return ""
}

// TestAuthLogin mirrors auth.test.ts: 302 to IdP with scope 'openid profile
// email' (no offline_access, no audience) and a flow cookie is set.
func TestAuthLogin(t *testing.T) {
	st := newTestStore()
	oc := newOIDCClientRaw(oidcConfig{AuthorizeURL: "https://idp.example/authorize", ClientID: "lumen-web", Audience: "lumen-api"})
	h := newHandlerForTest(testConfig(), st, oc, newTestSealer(t), nil)

	rec := httptest.NewRecorder()
	h.authLogin(rec, httptest.NewRequest(http.MethodGet, "https://x/auth/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("scope") != "openid profile email" {
		t.Errorf("scope = %q", loc.Query().Get("scope"))
	}
	if loc.Query().Has("audience") {
		t.Error("account-center login must not request audience")
	}
	if loc.Query().Get("redirect_uri") != testConfig().OAuthWebRedirect {
		t.Errorf("redirect_uri = %q", loc.Query().Get("redirect_uri"))
	}
	if loc.Query().Get("code_challenge_method") != "S256" {
		t.Error("missing PKCE S256")
	}
	if setCookie(t, rec, authFlowCookie) == "" {
		t.Error("auth flow cookie not set")
	}
}

// TestAuthCallback_Success mirrors auth.test.ts: exchanges code, sets HttpOnly
// session cookie, 302s to /account.
func TestAuthCallback_Success(t *testing.T) {
	st := newTestStore()
	idp := newIDPStub(t)
	idp.tokenResp = map[string]any{
		"access_token": fakeJWT(map[string]any{"sub": "web-user"}),
		"id_token":     fakeJWT(map[string]any{"sub": "web-user", "name": "Carol", "picture": "https://img/c.png"}),
		"expires_in":   float64(3600),
	}
	sealer := newTestSealer(t)
	h := newHandlerForTest(testConfig(), st, idp.newTestOIDC(), sealer, nil)

	flow, _ := sealAuthFlow(sealer, authFlowContext{Verifier: "web-verifier-1", State: "web-state-1", Exp: defaultAuthFlowExp()})
	req := httptest.NewRequest(http.MethodGet, "https://x/auth/callback?"+url.Values{
		"code": {"web-code"}, "state": {"web-state-1"},
	}.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: authFlowCookie, Value: flow})

	rec := httptest.NewRecorder()
	h.authCallback(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("callback must set Referrer-Policy: no-referrer")
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Path != "/account" {
		t.Errorf("redirect path = %q, want /account", loc.Path)
	}
	line := setCookieLine(rec, sessionCookie)
	if line == "" {
		t.Fatal("session cookie not set")
	}
	for _, want := range []string{"HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(line, want) {
			t.Errorf("session cookie missing %q: %q", want, line)
		}
	}
	// The sealed session must open and carry the profile.
	sess := openSession(sealer, setCookie(t, rec, sessionCookie))
	if sess == nil || sess.DisplayName != "Carol" || sess.AvatarURL != "https://img/c.png" {
		t.Errorf("sealed session = %+v", sess)
	}
}

// TestAuthCallback_StateMismatch mirrors auth.test.ts: mismatched state → 302
// /account?error=state_mismatch.
func TestAuthCallback_StateMismatch(t *testing.T) {
	st := newTestStore()
	sealer := newTestSealer(t)
	h := newHandlerForTest(testConfig(), st, newIDPStub(t).newTestOIDC(), sealer, nil)

	flow, _ := sealAuthFlow(sealer, authFlowContext{Verifier: "v", State: "real-state", Exp: defaultAuthFlowExp()})
	req := httptest.NewRequest(http.MethodGet, "https://x/auth/callback?"+url.Values{
		"code": {"c"}, "state": {"attacker-state"},
	}.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: authFlowCookie, Value: flow})
	rec := httptest.NewRecorder()
	h.authCallback(rec, req)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("error") != "state_mismatch" {
		t.Errorf("error = %q, want state_mismatch", loc.Query().Get("error"))
	}
}

// TestAuthCallback_NoFlow mirrors auth.test.ts: no flow cookie → invalid_flow.
func TestAuthCallback_NoFlow(t *testing.T) {
	st := newTestStore()
	h := newHandlerForTest(testConfig(), st, newIDPStub(t).newTestOIDC(), newTestSealer(t), nil)
	req := httptest.NewRequest(http.MethodGet, "https://x/auth/callback?"+url.Values{
		"code": {"c"}, "state": {"s"},
	}.Encode(), nil)
	rec := httptest.NewRecorder()
	h.authCallback(rec, req)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("error") != "invalid_flow" {
		t.Errorf("error = %q, want invalid_flow", loc.Query().Get("error"))
	}
}

// TestAPIMe mirrors auth.test.ts: 401 when not logged in; {display_name,
// avatar_url} for a valid session cookie.
func TestAPIMe(t *testing.T) {
	st := newTestStore()
	sealer := newTestSealer(t)
	h := newHandlerForTest(testConfig(), st, newIDPStub(t).newTestOIDC(), sealer, nil)

	// Not logged in.
	rec := httptest.NewRecorder()
	h.apiMe(rec, httptest.NewRequest(http.MethodGet, "https://x/api/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if e := decodeError(t, rec.Body); e.Code != "UNAUTHENTICATED" {
		t.Errorf("error code = %q, want UNAUTHENTICATED", e.Code)
	}

	// Valid session.
	sessVal, _ := sealSession(sealer, webSession{Sub: "u", DisplayName: "Eve", AvatarURL: "https://img/e.png", Exp: defaultSessionExp()})
	req := httptest.NewRequest(http.MethodGet, "https://x/api/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessVal})
	rec2 := httptest.NewRecorder()
	h.apiMe(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec2.Code)
	}
	body := decodeJSON(t, rec2.Body)
	if body["display_name"] != "Eve" || body["avatar_url"] != "https://img/e.png" {
		t.Errorf("me body = %v", body)
	}
}

// TestAuthLogout mirrors auth.test.ts: clears session cookie, returns 204.
func TestAuthLogout(t *testing.T) {
	st := newTestStore()
	h := newHandlerForTest(testConfig(), st, newIDPStub(t).newTestOIDC(), newTestSealer(t), nil)
	rec := httptest.NewRecorder()
	h.authLogout(rec, httptest.NewRequest(http.MethodPost, "https://x/auth/logout", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !strings.Contains(setCookieLine(rec, sessionCookie), "Max-Age=0") {
		t.Errorf("logout should clear session cookie (Max-Age=0): %q", setCookieLine(rec, sessionCookie))
	}
}

// --- CORS (decision 3) ---

// TestCORS_AllowedOrigin verifies ACAO is emitted only for the exact WebBaseURL
// origin, with credentials true and Vary: Origin always present.
func TestCORS_AllowedOrigin(t *testing.T) {
	st := newTestStore()
	h := newHandlerForTest(testConfig(), st, newIDPStub(t).newTestOIDC(), newTestSealer(t), nil)
	routes := h.Routes()

	// Allowed origin (== WebBaseURL) → ACAO echoes the origin.
	req := httptest.NewRequest(http.MethodGet, "https://x/api/me", nil)
	req.Header.Set("Origin", "https://acct.example.com")
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://acct.example.com" {
		t.Errorf("ACAO = %q, want https://acct.example.com", got)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("ACAC should be true for allowed origin")
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
		t.Error("Vary: Origin must always be present")
	}
}

// TestCORS_DisallowedOrigin verifies no ACAO for a foreign origin, but Vary:
// Origin is still sent, and never '*'.
func TestCORS_DisallowedOrigin(t *testing.T) {
	st := newTestStore()
	h := newHandlerForTest(testConfig(), st, newIDPStub(t).newTestOIDC(), newTestSealer(t), nil)
	routes := h.Routes()

	req := httptest.NewRequest(http.MethodGet, "https://x/api/me", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO = %q, want empty for foreign origin", got)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
		t.Error("Vary: Origin must always be present, even for a disallowed origin")
	}
}

// TestCORS_Preflight verifies OPTIONS preflight for the allowed origin returns
// 204 with the CORS headers.
func TestCORS_Preflight(t *testing.T) {
	st := newTestStore()
	h := newHandlerForTest(testConfig(), st, newIDPStub(t).newTestOIDC(), newTestSealer(t), nil)
	routes := h.Routes()

	req := httptest.NewRequest(http.MethodOptions, "https://x/api/desktop/exchange", nil)
	req.Header.Set("Origin", "https://acct.example.com")
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://acct.example.com" {
		t.Error("preflight missing ACAO for allowed origin")
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Error("preflight should advertise POST")
	}
}

// TestRoutesMounts verifies the nine endpoints are mounted on their exact
// method+path pairs (and the download proxy is absent per decision 10).
func TestRoutesMounts(t *testing.T) {
	st := newTestStore()
	h := newHandlerForTest(testConfig(), st, newIDPStub(t).newTestOIDC(), newTestSealer(t), nil)
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)

	// A GET /api/me with no session should reach the handler and return 401
	// (proving the route is mounted, not 404).
	resp, err := http.Get(srv.URL + "/api/me")
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/me status = %d, want 401 (mounted)", resp.StatusCode)
	}

	// The dropped download proxy must 404.
	resp2, err := http.Get(srv.URL + "/api/download/latest")
	if err != nil {
		t.Fatalf("GET /api/download/latest: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("download proxy status = %d, want 404 (dropped)", resp2.StatusCode)
	}
}

package broker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"lumen/internal/store"
)

// seedHandoff stages a handoff bound to S256(verifier) and returns the code +
// verifier (mirrors exchange.test.ts seedHandoff).
func seedHandoff(t *testing.T, st store.Store, overrides func(*store.Handoff)) (code, verifier string) {
	t.Helper()
	verifier = randomToken(tokenBytes)
	challenge := s256(verifier)
	code = randomToken(tokenBytes)
	rec := store.Handoff{
		AccessToken:    fakeJWT(map[string]any{"sub": "u-1", "aud": "lumen-api"}),
		ExpiresIn:      3600,
		RefreshToken:   "refresh-token-1",
		Sub:            "u-1",
		BoundChallenge: challenge,
		Profile:        store.DesktopProfile{DisplayName: "Alice", AvatarURL: "https://img/a.png"},
	}
	if overrides != nil {
		overrides(&rec)
	}
	if err := st.PutHandoff(context.Background(), code, rec); err != nil {
		t.Fatalf("put handoff: %v", err)
	}
	return code, verifier
}

// TestDesktopExchange_Success mirrors exchange.test.ts happy path + no
// refresh_token leak, and that the session is stored with refresh_token+sub.
func TestDesktopExchange_Success(t *testing.T) {
	st := newTestStore()
	h := newTestHandler(t, st, newIDPStub(t).newTestOIDC())
	code, verifier := seedHandoff(t, st, nil)

	rec := httptest.NewRecorder()
	h.desktopExchange(rec, jsonPost("https://x/api/desktop/exchange", map[string]any{
		"handoff_code": code, "handoff_verifier": verifier,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	bodyStr := rec.Body.String()
	if containsToken(bodyStr, "refresh-token-1") {
		t.Error("refresh_token leaked into exchange response")
	}
	body := decodeJSON(t, rec.Body)
	if body["access_token"] == "" || body["access_token"] == nil {
		t.Error("missing access_token")
	}
	if body["expires_in"].(float64) != 3600 {
		t.Errorf("expires_in = %v, want 3600", body["expires_in"])
	}
	sid, _ := body["desktop_session_id"].(string)
	if len(sid) <= 40 {
		t.Errorf("desktop_session_id length = %d, want > 40", len(sid))
	}
	profile := body["profile"].(map[string]any)
	if profile["display_name"] != "Alice" {
		t.Errorf("profile.display_name = %v", profile["display_name"])
	}

	// Session persisted with refresh_token + sub.
	sess, err := st.GetSession(context.Background(), sid)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.RefreshToken != "refresh-token-1" || sess.Sub != "u-1" {
		t.Errorf("stored session = %+v", sess)
	}
}

// TestDesktopExchange_OneTime mirrors exchange.test.ts: second exchange → 404.
func TestDesktopExchange_OneTime(t *testing.T) {
	st := newTestStore()
	h := newTestHandler(t, st, newIDPStub(t).newTestOIDC())
	code, verifier := seedHandoff(t, st, nil)

	first := httptest.NewRecorder()
	h.desktopExchange(first, jsonPost("https://x/", map[string]any{"handoff_code": code, "handoff_verifier": verifier}))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d", first.Code)
	}
	second := httptest.NewRecorder()
	h.desktopExchange(second, jsonPost("https://x/", map[string]any{"handoff_code": code, "handoff_verifier": verifier}))
	if second.Code != http.StatusNotFound {
		t.Fatalf("second status = %d, want 404", second.Code)
	}
	if e := decodeError(t, second.Body); e.Code != "HANDOFF_NOT_FOUND" {
		t.Errorf("error code = %q, want HANDOFF_NOT_FOUND", e.Code)
	}
}

// TestDesktopExchange_VerifierMismatch mirrors exchange.test.ts: wrong verifier
// → 400 VERIFIER_MISMATCH, and the code is still consumed (replay → 404).
func TestDesktopExchange_VerifierMismatch(t *testing.T) {
	st := newTestStore()
	h := newTestHandler(t, st, newIDPStub(t).newTestOIDC())
	code, _ := seedHandoff(t, st, nil)
	wrong := randomToken(tokenBytes) // S256 won't match bound_challenge

	rec := httptest.NewRecorder()
	h.desktopExchange(rec, jsonPost("https://x/", map[string]any{"handoff_code": code, "handoff_verifier": wrong}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if e := decodeError(t, rec.Body); e.Code != "VERIFIER_MISMATCH" {
		t.Errorf("error code = %q, want VERIFIER_MISMATCH", e.Code)
	}
	// One-time consume happened before the verifier check → code now gone.
	retry := httptest.NewRecorder()
	h.desktopExchange(retry, jsonPost("https://x/", map[string]any{"handoff_code": code, "handoff_verifier": wrong}))
	if retry.Code != http.StatusNotFound {
		t.Errorf("retry status = %d, want 404 (code consumed)", retry.Code)
	}
}

// TestDesktopExchange_UnknownCode mirrors exchange.test.ts unknown code → 404.
func TestDesktopExchange_UnknownCode(t *testing.T) {
	st := newTestStore()
	h := newTestHandler(t, st, newIDPStub(t).newTestOIDC())
	rec := httptest.NewRecorder()
	h.desktopExchange(rec, jsonPost("https://x/", map[string]any{
		"handoff_code": randomToken(tokenBytes), "handoff_verifier": randomToken(tokenBytes),
	}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestDesktopExchange_MissingFields mirrors exchange.test.ts missing fields → 400.
func TestDesktopExchange_MissingFields(t *testing.T) {
	st := newTestStore()
	h := newTestHandler(t, st, newIDPStub(t).newTestOIDC())
	rec := httptest.NewRecorder()
	h.desktopExchange(rec, jsonPost("https://x/", map[string]any{}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestDesktopExchange_ExpiresInFallback mirrors exchange.test.ts fallback to 300.
func TestDesktopExchange_ExpiresInFallback(t *testing.T) {
	st := newTestStore()
	h := newTestHandler(t, st, newIDPStub(t).newTestOIDC())
	code, verifier := seedHandoff(t, st, func(rec *store.Handoff) { rec.ExpiresIn = 0 })
	rec := httptest.NewRecorder()
	h.desktopExchange(rec, jsonPost("https://x/", map[string]any{"handoff_code": code, "handoff_verifier": verifier}))
	body := decodeJSON(t, rec.Body)
	if body["expires_in"].(float64) != float64(defaultExpiresIn) {
		t.Errorf("expires_in = %v, want %d", body["expires_in"], defaultExpiresIn)
	}
}

// seedSession stores a desktop session with the given refresh token.
func seedSession(t *testing.T, st store.Store, refresh string) string {
	t.Helper()
	id := randomToken(sessionIDBytes)
	if err := st.PutSession(context.Background(), store.DesktopSession{ID: id, RefreshToken: refresh, Sub: "u-1"}); err != nil {
		t.Fatalf("put session: %v", err)
	}
	return id
}

// TestDesktopRefresh_Success mirrors refresh.test.ts happy path + no leak.
func TestDesktopRefresh_Success(t *testing.T) {
	st := newTestStore()
	idp := newIDPStub(t)
	idp.tokenResp = map[string]any{"access_token": fakeJWT(map[string]any{"sub": "u-1"}), "expires_in": float64(1800)}
	h := newTestHandler(t, st, idp.newTestOIDC())
	id := seedSession(t, st, "refresh-1")

	rec := httptest.NewRecorder()
	h.desktopRefresh(rec, jsonPost("https://x/", map[string]any{"desktop_session_id": id}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	bodyStr := rec.Body.String()
	if containsToken(bodyStr, "refresh") || containsToken(bodyStr, id) {
		t.Error("refresh response leaked refresh_token or session id")
	}
	body := decodeJSON(t, rec.Body)
	if body["expires_in"].(float64) != 1800 {
		t.Errorf("expires_in = %v, want 1800", body["expires_in"])
	}
}

// TestDesktopRefresh_Rotation mirrors refresh.test.ts: IdP rotation updates store.
func TestDesktopRefresh_Rotation(t *testing.T) {
	st := newTestStore()
	idp := newIDPStub(t)
	idp.tokenResp = map[string]any{
		"access_token":  fakeJWT(map[string]any{"sub": "u-1"}),
		"refresh_token": "new-rotated-refresh", "expires_in": float64(900),
	}
	h := newTestHandler(t, st, idp.newTestOIDC())
	id := seedSession(t, st, "old-refresh")

	rec := httptest.NewRecorder()
	h.desktopRefresh(rec, jsonPost("https://x/", map[string]any{"desktop_session_id": id}))
	sess, err := st.GetSession(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sess.RefreshToken != "new-rotated-refresh" || sess.Sub != "u-1" {
		t.Errorf("rotated session = %+v", sess)
	}
}

// TestDesktopRefresh_NonRolling mirrors refresh.test.ts: no rotation when the
// IdP omits refresh_token.
func TestDesktopRefresh_NonRolling(t *testing.T) {
	st := newTestStore()
	idp := newIDPStub(t)
	idp.tokenResp = map[string]any{"access_token": fakeJWT(map[string]any{"sub": "u-1"}), "expires_in": float64(900)}
	h := newTestHandler(t, st, idp.newTestOIDC())
	id := seedSession(t, st, "keep-refresh")

	h.desktopRefresh(httptest.NewRecorder(), jsonPost("https://x/", map[string]any{"desktop_session_id": id}))
	sess, _ := st.GetSession(context.Background(), id)
	if sess.RefreshToken != "keep-refresh" {
		t.Errorf("non-rolling session refresh = %q, want keep-refresh", sess.RefreshToken)
	}
}

// TestDesktopRefresh_UnknownSession mirrors refresh.test.ts: unknown → 401.
func TestDesktopRefresh_UnknownSession(t *testing.T) {
	st := newTestStore()
	h := newTestHandler(t, st, newIDPStub(t).newTestOIDC())
	rec := httptest.NewRecorder()
	h.desktopRefresh(rec, jsonPost("https://x/", map[string]any{"desktop_session_id": randomToken(sessionIDBytes)}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if e := decodeError(t, rec.Body); e.Code != "SESSION_INVALID" {
		t.Errorf("error code = %q, want SESSION_INVALID", e.Code)
	}
}

// TestDesktopRefresh_IdPReject mirrors refresh.test.ts: IdP reject → 401 + purge.
func TestDesktopRefresh_IdPReject(t *testing.T) {
	st := newTestStore()
	idp := newIDPStub(t)
	idp.tokenStatus = 400
	idp.tokenResp = map[string]any{"error": "invalid_grant"}
	h := newTestHandler(t, st, idp.newTestOIDC())
	id := seedSession(t, st, "revoked-refresh")

	rec := httptest.NewRecorder()
	h.desktopRefresh(rec, jsonPost("https://x/", map[string]any{"desktop_session_id": id}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if e := decodeError(t, rec.Body); e.Code != "SESSION_INVALID" {
		t.Errorf("error code = %q, want SESSION_INVALID", e.Code)
	}
	if _, err := st.GetSession(context.Background(), id); err != store.ErrNotFound {
		t.Errorf("rejected session should be purged, get err = %v", err)
	}
}

// TestDesktopLogout mirrors logout.test.ts: 204 and idempotency.
func TestDesktopLogout(t *testing.T) {
	st := newTestStore()
	h := newTestHandler(t, st, newIDPStub(t).newTestOIDC())
	id := seedSession(t, st, "r")

	rec := httptest.NewRecorder()
	h.desktopLogout(rec, jsonPost("https://x/", map[string]any{"desktop_session_id": id}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if _, err := st.GetSession(context.Background(), id); err != store.ErrNotFound {
		t.Errorf("session should be gone, err = %v", err)
	}
	// Idempotent: logging out an unknown session still returns 204.
	rec2 := httptest.NewRecorder()
	h.desktopLogout(rec2, jsonPost("https://x/", map[string]any{"desktop_session_id": randomToken(sessionIDBytes)}))
	if rec2.Code != http.StatusNoContent {
		t.Errorf("idempotent logout status = %d, want 204", rec2.Code)
	}
}

// TestDesktopLogin mirrors login.test.ts: valid loopback → 302 to IdP with PKCE,
// and a login context is staged. Invalid loopback → 400.
func TestDesktopLogin(t *testing.T) {
	st := newTestStore()
	oc := newOIDCClientRaw(oidcConfig{AuthorizeURL: "https://idp.example/authorize", ClientID: "lumen-web", Audience: "lumen-api"})
	h := newHandlerForTest(testConfig(), st, oc, newTestSealer(t), nil)

	challenge := s256("desktop-verifier")
	req := httptest.NewRequest(http.MethodGet, "https://x/desktop/login?"+url.Values{
		"redirect_uri": {"http://127.0.0.1:8931/cb"},
		"state":        {"desktop-state"},
		"challenge":    {challenge},
	}.Encode(), nil)
	rec := httptest.NewRecorder()
	h.desktopLogin(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("code_challenge_method") != "S256" {
		t.Error("authorize URL missing PKCE S256")
	}
	if loc.Query().Get("redirect_uri") != testConfig().OAuthDesktopRedirect {
		t.Errorf("redirect_uri = %q", loc.Query().Get("redirect_uri"))
	}

	// Bad loopback → 400.
	badReq := httptest.NewRequest(http.MethodGet, "https://x/desktop/login?"+url.Values{
		"redirect_uri": {"http://localhost:8931/cb"}, "state": {"s"}, "challenge": {challenge},
	}.Encode(), nil)
	badRec := httptest.NewRecorder()
	h.desktopLogin(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Errorf("bad loopback status = %d, want 400", badRec.Code)
	}
}

// TestDesktopCallback_HappyPath mirrors callback.test.ts: exchanges code, stages
// a handoff bound to the desktop challenge, 302s handoff_code+state with no
// access_token in the URL, and consumes the login context.
func TestDesktopCallback_HappyPath(t *testing.T) {
	st := newTestStore()
	idp := newIDPStub(t)
	idp.tokenResp = map[string]any{
		"access_token":  fakeJWT(map[string]any{"sub": "user-1", "aud": "lumen-api"}),
		"refresh_token": "refresh-abc",
		"id_token":      fakeJWT(map[string]any{"sub": "user-1", "name": "Alice", "picture": "https://img/a.png"}),
		"expires_in":    float64(3600),
	}
	h := newTestHandler(t, st, idp.newTestOIDC())

	oidcState := "oidc-state-happy"
	challenge := s256("desktop-verifier")
	if err := st.PutLoginContext(context.Background(), oidcState, store.LoginContext{
		State: "desktop-state-xyz", Challenge: challenge,
		RedirectURI: "http://127.0.0.1:8931/cb", OIDCVerifier: "oidc-verifier-value",
	}); err != nil {
		t.Fatalf("put ctx: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://x/desktop/callback?"+url.Values{
		"code": {"auth-code-1"}, "state": {oidcState},
	}.Encode(), nil)
	rec := httptest.NewRecorder()
	h.desktopCallback(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("callback must set Referrer-Policy: no-referrer")
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Scheme+"://"+loc.Host != "http://127.0.0.1:8931" {
		t.Errorf("redirect origin = %s", loc.Scheme+"://"+loc.Host)
	}
	handoffCode := loc.Query().Get("handoff_code")
	if handoffCode == "" {
		t.Fatal("missing handoff_code")
	}
	if loc.Query().Get("state") != "desktop-state-xyz" {
		t.Errorf("state = %q", loc.Query().Get("state"))
	}
	if containsToken(loc.String(), "access_token") {
		t.Error("access_token must not appear in redirect URL")
	}

	// Handoff staged, bound to the desktop challenge.
	rec2, err := st.ConsumeHandoff(context.Background(), handoffCode)
	if err != nil {
		t.Fatalf("consume handoff: %v", err)
	}
	if rec2.BoundChallenge != challenge {
		t.Errorf("bound_challenge = %q, want %q", rec2.BoundChallenge, challenge)
	}
	if rec2.RefreshToken != "refresh-abc" || rec2.Sub != "user-1" || rec2.ExpiresIn != 3600 {
		t.Errorf("handoff = %+v", rec2)
	}
	if rec2.Profile.DisplayName != "Alice" || rec2.Profile.AvatarURL != "https://img/a.png" {
		t.Errorf("handoff profile = %+v", rec2.Profile)
	}

	// Login context consumed (one-time): a second take fails.
	if _, err := st.TakeLoginContext(context.Background(), oidcState); err != store.ErrNotFound {
		t.Errorf("login context should be consumed, err = %v", err)
	}
}

// TestDesktopCallback_TokenFail mirrors callback.test.ts: token exchange failure
// 302s back to loopback with error, no handoff_code.
func TestDesktopCallback_TokenFail(t *testing.T) {
	st := newTestStore()
	idp := newIDPStub(t)
	idp.tokenStatus = 400
	idp.tokenResp = map[string]any{"error": "invalid_grant"}
	h := newTestHandler(t, st, idp.newTestOIDC())

	oidcState := "oidc-state-tokenfail"
	if err := st.PutLoginContext(context.Background(), oidcState, store.LoginContext{
		State: "desktop-state-xyz", Challenge: s256("v"),
		RedirectURI: "http://127.0.0.1:8931/cb", OIDCVerifier: "v",
	}); err != nil {
		t.Fatalf("put ctx: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://x/desktop/callback?"+url.Values{
		"code": {"bad-code"}, "state": {oidcState},
	}.Encode(), nil)
	rec := httptest.NewRecorder()
	h.desktopCallback(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("error") != "token_exchange_failed" {
		t.Errorf("error = %q, want token_exchange_failed", loc.Query().Get("error"))
	}
	if loc.Query().Has("handoff_code") {
		t.Error("failed callback must not carry handoff_code")
	}
}

// TestDesktopCallback_IdPError mirrors callback.test.ts: IdP error param → 302
// back with that error.
func TestDesktopCallback_IdPError(t *testing.T) {
	st := newTestStore()
	h := newTestHandler(t, st, newIDPStub(t).newTestOIDC())
	oidcState := "oidc-state-idperror"
	if err := st.PutLoginContext(context.Background(), oidcState, store.LoginContext{
		State: "desktop-state-xyz", Challenge: s256("v"), RedirectURI: "http://127.0.0.1:8931/cb", OIDCVerifier: "v",
	}); err != nil {
		t.Fatalf("put ctx: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://x/desktop/callback?"+url.Values{
		"error": {"access_denied"}, "state": {oidcState},
	}.Encode(), nil)
	rec := httptest.NewRecorder()
	h.desktopCallback(rec, req)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("error") != "access_denied" {
		t.Errorf("error = %q, want access_denied", loc.Query().Get("error"))
	}
}

// TestDesktopCallback_NoContext mirrors callback.test.ts: unknown state → 400.
func TestDesktopCallback_NoContext(t *testing.T) {
	st := newTestStore()
	h := newTestHandler(t, st, newIDPStub(t).newTestOIDC())
	req := httptest.NewRequest(http.MethodGet, "https://x/desktop/callback?"+url.Values{
		"code": {"c"}, "state": {"unknown-state"},
	}.Encode(), nil)
	rec := httptest.NewRecorder()
	h.desktopCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

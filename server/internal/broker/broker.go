package broker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"lumen/internal/config"
	"lumen/internal/secure"
	"lumen/internal/store"
)

// OIDC scopes (mirrors desktop/login.ts + auth/login.ts). The desktop flow adds
// offline_access (for a refresh_token) and requests aud=lumen-api; the account
// center does not (it never calls the Lumen API).
const (
	desktopScope = "openid profile email offline_access"
	webScope     = "openid profile email"
)

// Handler serves the account-center + desktop broker endpoints (decision 10) on
// the Go server. It is immutable after construction and safe for concurrent use.
type Handler struct {
	cfg    config.Config
	store  store.Store
	oidc   *oidcClient
	sealer *secure.Sealer // seals the two account-center cookies (session key)
	logger *slog.Logger
}

// NewHandler builds a broker Handler. It derives the OIDC endpoints from
// config (with discovery fallback when the authorize/token/userinfo URLs are
// empty) and builds the session-cookie sealer from LUMEN_SESSION_ENC_KEY. The
// store must have been built with the refresh sealer (store.NewWithSealer) so
// desktop_sessions.refresh_token is encrypted at rest (decision 2).
func NewHandler(ctx context.Context, cfg config.Config, st store.Store, logger *slog.Logger) (*Handler, error) {
	if logger == nil {
		logger = slog.Default()
	}
	sealer, err := secure.NewSealer(cfg.SessionEncKey())
	if err != nil {
		return nil, err
	}
	oc, err := newOIDCClient(ctx, cfg.OAuthIssuer, oidcConfig{
		AuthorizeURL: cfg.OAuthAuthorizeURL,
		TokenURL:     cfg.OAuthTokenURL,
		UserinfoURL:  cfg.OAuthUserinfoURL,
		ClientID:     cfg.OAuthClientID,
		ClientSecret: cfg.OAuthClientSecret,
		Audience:     cfg.OAuthAudience,
	})
	if err != nil {
		return nil, err
	}
	return &Handler{cfg: cfg, store: st, oidc: oc, sealer: sealer, logger: logger}, nil
}

// newHandlerForTest wires a Handler from already-built collaborators, skipping
// discovery. Tests use it with an httptest IdP stub and the in-memory store.
func newHandlerForTest(cfg config.Config, st store.Store, oc *oidcClient, sealer *secure.Sealer, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{cfg: cfg, store: st, oidc: oc, sealer: sealer, logger: logger}
}

// Register mounts the nine broker endpoints on the exact method+path pairs the
// desktop client and account-center SPA expect (decision 10; the download proxy
// is intentionally dropped — the SPA fetches /updates/latest.json directly). All
// routes are PUBLIC: the broker manages its own auth via handoff codes and
// sealed cookies, so no RequireAuth wraps them. CORS is applied ONCE around the
// whole server mux by the caller (rest.NewRouter), not here — Register adds no
// middleware so it can share the root mux with the /api/v1/* routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /desktop/login", h.desktopLogin)
	mux.HandleFunc("GET /desktop/callback", h.desktopCallback)
	mux.HandleFunc("POST /api/desktop/exchange", h.desktopExchange)
	mux.HandleFunc("POST /api/desktop/refresh", h.desktopRefresh)
	mux.HandleFunc("POST /api/desktop/logout", h.desktopLogout)
	mux.HandleFunc("GET /auth/login", h.authLogin)
	mux.HandleFunc("GET /auth/callback", h.authCallback)
	mux.HandleFunc("POST /auth/logout", h.authLogout)
	mux.HandleFunc("GET /api/me", h.apiMe)
}

// Routes returns a standalone http.Handler for the nine broker endpoints wrapped
// in the broker's own CORS middleware (decision 3). It is retained for tests and
// standalone use; the production server mounts the broker via Register on the
// shared root mux and applies CORS once at the top (rest.NewRouter).
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	h.Register(mux)
	return h.withCORS(mux)
}

// --- desktop login broker (web-design.md §5.1) ---

// desktopLogin (GET /desktop/login) validates the loopback redirect_uri, stages
// the login context, and 302s to the IdP /authorize (mirrors desktop/login.ts).
func (h *Handler) desktopLogin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	challenge := q.Get("challenge")

	if !isLoopbackRedirectURI(redirectURI) {
		badRequest(w, "redirect_uri must be an http://127.0.0.1:<port>/... loopback URI")
		return
	}
	if state == "" || len(state) > 512 {
		badRequest(w, "missing or invalid state")
		return
	}
	if !isBase64Url(challenge) {
		badRequest(w, "challenge must be a base64url S256 value")
		return
	}

	oidcVerifier := randomToken(tokenBytes)
	oidcChallenge := s256(oidcVerifier)
	oidcState := randomToken(tokenBytes)

	if err := h.store.PutLoginContext(r.Context(), oidcState, store.LoginContext{
		State:        state,
		Challenge:    challenge,
		RedirectURI:  redirectURI,
		OIDCVerifier: oidcVerifier,
	}); err != nil {
		h.logger.Error("stage login context failed", "err", err)
		badRequest(w, "failed to stage login context", "INTERNAL")
		return
	}

	authorizeURL, err := h.oidc.buildAuthorizeURL(authorizeParams{
		CodeChallenge: oidcChallenge,
		State:         oidcState,
		RedirectURI:   h.cfg.OAuthDesktopRedirect,
		Scope:         desktopScope,
		Audience:      h.cfg.OAuthAudience,
	})
	if err != nil {
		h.logger.Error("build authorize url failed", "err", err)
		badRequest(w, "failed to build authorize url", "INTERNAL")
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// desktopCallback (GET /desktop/callback) exchanges the code for tokens, stages a
// one-time handoff bound to the desktop challenge, and 302s back to the loopback
// with handoff_code + state (mirrors desktop/callback.ts). access_token never
// enters the URL. Sets Referrer-Policy: no-referrer (decision 8).
func (h *Handler) desktopCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	q := r.URL.Query()
	code := q.Get("code")
	oidcState := q.Get("state")
	idpError := q.Get("error")

	if oidcState == "" {
		badRequest(w, "invalid or expired state")
		return
	}
	ctx, err := h.store.TakeLoginContext(r.Context(), oidcState)
	if err != nil {
		// No valid context: we cannot know the loopback address, so 400.
		badRequest(w, "invalid or expired state")
		return
	}

	back := safeURL(ctx.RedirectURI)
	if back == nil {
		badRequest(w, "stored redirect_uri is invalid")
		return
	}

	if idpError != "" {
		redirectError(w, r, back, idpError, ctx.State)
		return
	}
	if code == "" {
		redirectError(w, r, back, "missing_code", ctx.State)
		return
	}

	token := h.oidc.exchangeAuthCode(r.Context(), code, ctx.OIDCVerifier, h.cfg.OAuthDesktopRedirect)
	if token == nil {
		redirectError(w, r, back, "token_exchange_failed", ctx.State)
		return
	}

	sub := subjectFrom(token.IDToken, token.AccessToken)
	profile := h.oidc.fetchProfile(r.Context(), token.AccessToken, token.IDToken)

	handoffCode := randomToken(tokenBytes)
	if err := h.store.PutHandoff(r.Context(), handoffCode, store.Handoff{
		AccessToken:    token.AccessToken,
		ExpiresIn:      normalizeExpiresIn(token.ExpiresIn),
		RefreshToken:   token.RefreshToken,
		Sub:            sub,
		BoundChallenge: ctx.Challenge,
		Profile:        profile,
	}); err != nil {
		h.logger.Error("stage handoff failed", "err", err)
		redirectError(w, r, back, "handoff_stage_failed", ctx.State)
		return
	}

	qb := back.Query()
	qb.Set("handoff_code", handoffCode)
	qb.Set("state", ctx.State)
	back.RawQuery = qb.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

// desktopExchange (POST /api/desktop/exchange) swaps handoff_code +
// handoff_verifier for an access_token and a desktop_session_id, enforcing the
// handoff binding (decision 6) and one-time consume (mirrors exchange.ts).
func (h *Handler) desktopExchange(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSON(r, defaultReadJSONMaxBytes)
	if !ok {
		badRequest(w, "missing handoff_code or handoff_verifier")
		return
	}
	handoffCode, okC := readStringField(body, "handoff_code", 4096)
	handoffVerifier, okV := readStringField(body, "handoff_verifier", 4096)
	if !okC || !okV {
		badRequest(w, "missing handoff_code or handoff_verifier")
		return
	}
	if !isBase64Url(handoffCode) || !isBase64Url(handoffVerifier) {
		badRequest(w, "handoff_code and handoff_verifier must be base64url")
		return
	}

	// One-time consume: read-and-delete regardless of the outcome below.
	record, err := h.store.ConsumeHandoff(r.Context(), handoffCode)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			notFound(w, "HANDOFF_NOT_FOUND", "handoff_code not found, already used, or expired")
			return
		}
		h.logger.Error("consume handoff failed", "err", err)
		notFound(w, "HANDOFF_NOT_FOUND", "handoff_code not found, already used, or expired")
		return
	}

	// Verify S256(handoff_verifier) == bound_challenge (constant time).
	if !constantTimeEqual(s256(handoffVerifier), record.BoundChallenge) {
		badRequest(w, "handoff_verifier does not match bound_challenge", "VERIFIER_MISMATCH")
		return
	}

	desktopSessionID := randomToken(sessionIDBytes)
	if err := h.store.PutSession(r.Context(), store.DesktopSession{
		ID:           desktopSessionID,
		RefreshToken: record.RefreshToken,
		Sub:          record.Sub,
	}); err != nil {
		h.logger.Error("create desktop session failed", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to create session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":       record.AccessToken,
		"expires_in":         normalizeExpiresIn(record.ExpiresIn),
		"desktop_session_id": desktopSessionID,
		"profile":            record.Profile,
	})
}

// desktopRefresh (POST /api/desktop/refresh) refreshes an access_token from a
// desktop_session_id, rotating the stored refresh_token when the IdP returns a
// new one and revoking the session on rejection (mirrors refresh.ts).
func (h *Handler) desktopRefresh(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSON(r, defaultReadJSONMaxBytes)
	if !ok {
		badRequest(w, "missing desktop_session_id")
		return
	}
	sessionID, okS := readStringField(body, "desktop_session_id", 4096)
	if !okS {
		badRequest(w, "missing desktop_session_id")
		return
	}
	if !isBase64Url(sessionID) {
		badRequest(w, "desktop_session_id must be base64url")
		return
	}

	sess, err := h.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "SESSION_INVALID", "session expired or revoked")
		return
	}

	token := h.oidc.refresh(r.Context(), sess.RefreshToken)
	if token == nil {
		// IdP rejected refresh → delete the dead session, force re-login.
		_ = h.store.DeleteSession(r.Context(), sessionID)
		writeError(w, http.StatusUnauthorized, "SESSION_INVALID", "refresh rejected by identity provider")
		return
	}

	// Rotate the stored refresh_token when the IdP issued a new one.
	if token.RefreshToken != "" && token.RefreshToken != sess.RefreshToken {
		rotated := sess
		rotated.RefreshToken = token.RefreshToken
		if err := h.store.PutSession(r.Context(), rotated); err != nil {
			h.logger.Error("rotate refresh token failed", "err", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token.AccessToken,
		"expires_in":   normalizeExpiresIn(token.ExpiresIn),
	})
}

// desktopLogout (POST /api/desktop/logout) deletes the desktop session and
// returns 204, idempotently (mirrors logout.ts).
func (h *Handler) desktopLogout(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSON(r, defaultReadJSONMaxBytes)
	if !ok {
		badRequest(w, "missing desktop_session_id")
		return
	}
	sessionID, okS := readStringField(body, "desktop_session_id", 4096)
	if !okS {
		badRequest(w, "missing desktop_session_id")
		return
	}
	if !isBase64Url(sessionID) {
		badRequest(w, "desktop_session_id must be base64url")
		return
	}
	// Idempotent: delete whether or not it exists (missing is a no-op).
	if err := h.store.DeleteSession(r.Context(), sessionID); err != nil {
		h.logger.Error("delete desktop session failed", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- account center (web-design.md §6) ---

// authLogin (GET /auth/login) stages the OIDC flow in a sealed short-lived
// cookie and 302s to the IdP (mirrors auth/login.ts). No offline_access, no
// aud=lumen-api (the account center never calls the Lumen API).
func (h *Handler) authLogin(w http.ResponseWriter, r *http.Request) {
	verifier := randomToken(tokenBytes)
	challenge := s256(verifier)
	state := randomToken(tokenBytes)

	flow, err := sealAuthFlow(h.sealer, authFlowContext{
		Verifier: verifier,
		State:    state,
		Exp:      defaultAuthFlowExp(),
	})
	if err != nil {
		h.logger.Error("seal auth flow failed", "err", err)
		badRequest(w, "failed to start login", "INTERNAL")
		return
	}

	authorizeURL, err := h.oidc.buildAuthorizeURL(authorizeParams{
		CodeChallenge: challenge,
		State:         state,
		RedirectURI:   h.cfg.OAuthWebRedirect,
		Scope:         webScope,
		// no audience: account center does not request aud=lumen-api
	})
	if err != nil {
		h.logger.Error("build authorize url failed", "err", err)
		badRequest(w, "failed to build authorize url", "INTERNAL")
		return
	}
	http.SetCookie(w, buildAuthFlowCookie(flow))
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// authCallback (GET /auth/callback) validates state, exchanges the code, builds
// the sealed web session, and 302s to /account (mirrors auth/callback.ts). Sets
// Referrer-Policy: no-referrer (decision 8).
func (h *Handler) authCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	q := r.URL.Query()
	code := q.Get("code")
	state := q.Get("state")
	idpError := q.Get("error")

	flowVal := readAuthFlowCookie(r)
	var flow *authFlowContext
	if flowVal != "" {
		flow = openAuthFlow(h.sealer, flowVal)
	}
	if flow == nil {
		h.redirectAccount(w, r, "invalid_flow")
		return
	}
	if idpError != "" {
		h.redirectAccount(w, r, idpError)
		return
	}
	if code == "" || !constantTimeEqual(state, flow.State) {
		h.redirectAccount(w, r, "state_mismatch")
		return
	}

	token := h.oidc.exchangeAuthCode(r.Context(), code, flow.Verifier, h.cfg.OAuthWebRedirect)
	if token == nil {
		h.redirectAccount(w, r, "token_exchange_failed")
		return
	}

	sub := subjectFrom(token.IDToken, token.AccessToken)
	profile := h.oidc.fetchProfile(r.Context(), token.AccessToken, token.IDToken)

	sessionVal, err := sealSession(h.sealer, webSession{
		Sub:         sub,
		DisplayName: profile.DisplayName,
		AvatarURL:   profile.AvatarURL,
		Exp:         defaultSessionExp(),
	})
	if err != nil {
		h.logger.Error("seal session failed", "err", err)
		h.redirectAccount(w, r, "session_failed")
		return
	}

	http.SetCookie(w, buildSessionCookie(sessionVal))
	http.SetCookie(w, clearAuthFlowCookie()) // clear the used flow cookie
	http.Redirect(w, r, h.accountURL(""), http.StatusFound)
}

// authLogout (POST /auth/logout) clears the stateless session cookie and returns
// 204 (mirrors auth/logout.ts). Nothing is stored server-side to delete.
func (h *Handler) authLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, clearSessionCookie())
	w.WriteHeader(http.StatusNoContent)
}

// apiMe (GET /api/me) returns {display_name, avatar_url} from the sealed session
// cookie, or 401 UNAUTHENTICATED (mirrors api/me.ts). It never calls the Lumen
// API and echoes only the OIDC-sourced profile in the session.
func (h *Handler) apiMe(w http.ResponseWriter, r *http.Request) {
	cookieVal := readSessionCookie(r)
	var sess *webSession
	if cookieVal != "" {
		sess = openSession(h.sealer, cookieVal)
	}
	if sess == nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "not logged in")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"display_name": sess.DisplayName,
		"avatar_url":   sess.AvatarURL,
	})
}

// --- redirect helpers ---

// redirectError 302s back to the desktop loopback with error + state, never
// leaking a token/code (mirrors desktop/callback.ts redirectError). It carries
// the no-referrer policy already set on the response.
func redirectError(w http.ResponseWriter, r *http.Request, back *url.URL, errorCode, state string) {
	target := *back
	q := target.Query()
	q.Set("error", errorCode)
	q.Set("state", state)
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// redirectAccount 302s to /account?error= and clears the flow cookie (mirrors
// auth/callback.ts redirectAccount).
func (h *Handler) redirectAccount(w http.ResponseWriter, r *http.Request, errorCode string) {
	http.SetCookie(w, clearAuthFlowCookie())
	http.Redirect(w, r, h.accountURL(errorCode), http.StatusFound)
}

// accountURL builds an absolute /account URL under WEB_BASE_URL, optionally with
// an ?error= query.
func (h *Handler) accountURL(errorCode string) string {
	u, err := url.Parse(h.cfg.WebBaseURL)
	if err != nil {
		// WebBaseURL is validated at config load; fall back to a relative path.
		if errorCode != "" {
			return "/account?error=" + url.QueryEscape(errorCode)
		}
		return "/account"
	}
	u.Path = "/account"
	if errorCode != "" {
		q := u.Query()
		q.Set("error", errorCode)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

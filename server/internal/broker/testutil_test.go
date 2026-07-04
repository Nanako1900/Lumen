package broker

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lumen/internal/config"
	"lumen/internal/secure"
	"lumen/internal/store"
	"lumen/internal/storefake"
)

// testSessionKey is the 32-byte AES key used by tests to seal cookies. A
// distinct refresh key backs the store's at-rest encryption.
var (
	testSessionKeyBytes = bytes.Repeat([]byte{0x2a}, 32)
	testRefreshKeyBytes = bytes.Repeat([]byte{0x11}, 32)
)

// newTestSealer builds the session-cookie sealer.
func newTestSealer(t *testing.T) *secure.Sealer {
	t.Helper()
	s, err := secure.NewSealer(testSessionKeyBytes)
	if err != nil {
		t.Fatalf("session sealer: %v", err)
	}
	return s
}

// newTestStore builds an in-memory store (storefake) for handler tests.
func newTestStore() store.Store { return storefake.New() }

// idpStub is an httptest server standing in for the IdP token + userinfo
// endpoints. Handlers point their oidcClient at it via newTestOIDC.
type idpStub struct {
	server   *httptest.Server
	tokenURL string
	infoURL  string

	// tokenResp/tokenStatus configure the /token response.
	tokenResp   map[string]any
	tokenStatus int
	// infoResp configures the /userinfo response.
	infoResp   map[string]any
	infoStatus int

	// lastTokenForm captures the last form posted to /token for assertions.
	lastTokenForm url.Values
}

// newIDPStub starts a stub IdP. The token/userinfo bodies default to empty and
// are set by the caller before the request under test.
func newIDPStub(t *testing.T) *idpStub {
	t.Helper()
	s := &idpStub{tokenStatus: http.StatusOK, infoStatus: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.lastTokenForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.tokenStatus)
		_ = json.NewEncoder(w).Encode(s.tokenResp)
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.infoStatus)
		_ = json.NewEncoder(w).Encode(s.infoResp)
	})
	s.server = httptest.NewServer(mux)
	s.tokenURL = s.server.URL + "/token"
	s.infoURL = s.server.URL + "/userinfo"
	t.Cleanup(s.server.Close)
	return s
}

// newTestOIDC builds an oidcClient wired to the stub IdP (no discovery).
func (s *idpStub) newTestOIDC() *oidcClient {
	return newOIDCClientRaw(oidcConfig{
		AuthorizeURL: "https://idp.example/authorize",
		TokenURL:     s.tokenURL,
		UserinfoURL:  s.infoURL,
		ClientID:     "lumen-web",
		ClientSecret: "s3cr3t",
		Audience:     "lumen-api",
	})
}

// testConfig returns a minimal Config for handler construction. Endpoints that
// matter for redirects are set; the enc keys are not read by the handler
// directly (the sealer is injected).
func testConfig() config.Config {
	return config.Config{
		OAuthAudience:        "lumen-api",
		OAuthClientID:        "lumen-web",
		OAuthClientSecret:    "s3cr3t",
		OAuthDesktopRedirect: "https://acct.example.com/desktop/callback",
		OAuthWebRedirect:     "https://acct.example.com/auth/callback",
		WebBaseURL:           "https://acct.example.com",
		OAuthWebScope:        "openid profile email",
		OAuthDesktopScope:    "openid profile email offline_access",
	}
}

// newTestHandler builds a Handler around the stub IdP and in-memory store.
func newTestHandler(t *testing.T, st store.Store, oc *oidcClient) *Handler {
	t.Helper()
	return newHandlerForTest(testConfig(), st, oc, newTestSealer(t), nil)
}

// fakeJWT builds an unsigned JWT (header.payload.) whose payload is the given
// claims. subjectFrom/profileFrom parse the payload without verification, so a
// dummy signature is fine (mirrors testutil.ts fakeJwt).
func fakeJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + ".sig"
}

// jsonPost builds a JSON POST request to path with the given body.
func jsonPost(path string, body any) *http.Request {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// decodeError decodes a broker error envelope from a response body.
func decodeError(t *testing.T, body io.Reader) errorBody {
	t.Helper()
	var e brokerError
	if err := json.NewDecoder(body).Decode(&e); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return e.Error
}

// decodeJSON decodes an arbitrary JSON object from a response body.
func decodeJSON(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(body).Decode(&m); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	return m
}

// containsToken reports whether s contains needle (used to assert no secret
// leaks into responses/URLs).
func containsToken(s, needle string) bool { return strings.Contains(s, needle) }

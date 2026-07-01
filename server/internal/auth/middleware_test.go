package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/golang-jwt/jwt/v5"
)

// signerFor builds a Verifier + token signer without external services.
func signerFor(t *testing.T) (*Verifier, func(sub string, exp time.Time) string) {
	t.Helper()
	s := newTestSigner(t)
	sign := func(sub string, exp time.Time) string {
		return s.sign(t, Claims{RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			ExpiresAt: jwt.NewNumericDate(exp),
		}})
	}
	return s.verifier, sign
}

// captureErr is an ErrorWriter that records the last error.
type captureErr struct {
	status  int
	code    string
	message string
	called  bool
}

func (c *captureErr) write(w http.ResponseWriter, status int, code, message string) {
	c.status, c.code, c.message, c.called = status, code, message, true
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
}

func TestRequireAuth_PassesClaimsThrough(t *testing.T) {
	v, sign := signerFor(t)
	token := sign("sub-1", time.Now().Add(time.Hour))

	var gotSub string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if c := ClaimsFromContext(r.Context()); c != nil {
			gotSub = c.Subject
		}
	})
	ce := &captureErr{}
	h := RequireAuth(v, ce.write, next)

	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), r)

	if ce.called {
		t.Fatalf("unexpected error: %s", ce.code)
	}
	if gotSub != "sub-1" {
		t.Errorf("claims subject = %q, want sub-1", gotSub)
	}
}

func TestRequireAuth_MissingBearer(t *testing.T) {
	v, _ := signerFor(t)
	ce := &captureErr{}
	h := RequireAuth(v, ce.write, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !ce.called || ce.status != http.StatusUnauthorized || ce.code != "UNAUTHENTICATED" {
		t.Errorf("missing bearer: called=%v status=%d code=%s", ce.called, ce.status, ce.code)
	}
}

func TestRequireAuth_ExpiredMapsToTokenExpired(t *testing.T) {
	v, sign := signerFor(t)
	token := sign("sub-1", time.Now().Add(-time.Hour))
	ce := &captureErr{}
	h := RequireAuth(v, ce.write, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if ce.code != "TOKEN_EXPIRED" {
		t.Errorf("code = %s, want TOKEN_EXPIRED", ce.code)
	}
}

func TestRequireAuth_InvalidMapsToTokenInvalid(t *testing.T) {
	v, _ := signerFor(t)
	ce := &captureErr{}
	h := RequireAuth(v, ce.write, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("Authorization", "Bearer garbage")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if ce.code != "TOKEN_INVALID" {
		t.Errorf("code = %s, want TOKEN_INVALID", ce.code)
	}
}

func TestRequireOwner_AllowsOwnerBlocksOthers(t *testing.T) {
	owners := NewOwnerSet([]string{"sub-owner"})

	// Build a handler chain that seeds claims then applies RequireOwner.
	makeChain := func(sub string) (*captureErr, http.Handler) {
		ce := &captureErr{}
		passed := false
		inner := RequireOwner(owners, ce.write, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			passed = true
		}))
		seed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := contextWithClaims(r.Context(), &Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: sub}})
			inner.ServeHTTP(w, r.WithContext(ctx))
		})
		_ = passed
		return ce, seed
	}

	ceOwner, ownerChain := makeChain("sub-owner")
	ownerChain.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/x", nil))
	if ceOwner.called {
		t.Errorf("owner should pass, got error %s", ceOwner.code)
	}

	cePlain, plainChain := makeChain("sub-plain")
	plainChain.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/x", nil))
	if !cePlain.called || cePlain.status != http.StatusForbidden || cePlain.code != "FORBIDDEN" {
		t.Errorf("non-owner: called=%v status=%d code=%s, want 403 FORBIDDEN", cePlain.called, cePlain.status, cePlain.code)
	}
}

func TestRequireOwner_NilClaimsForbidden(t *testing.T) {
	owners := NewOwnerSet([]string{"sub-owner"})
	ce := &captureErr{}
	h := RequireOwner(owners, ce.write, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	// No claims in context.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/x", nil))
	if !ce.called || ce.code != "FORBIDDEN" {
		t.Errorf("nil claims should be FORBIDDEN, got called=%v code=%s", ce.called, ce.code)
	}
}

func TestNewVerifierFromJWKSetJSON(t *testing.T) {
	// Build a JWK set JSON and construct a verifier from it (exercises the
	// exported offline constructor).
	s := newTestSigner(t)
	jwk, err := jwkset.NewJWKFromKey(s.priv.Public(), jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{ALG: jwkset.AlgRS256, KID: testKID, USE: jwkset.UseSig},
	})
	if err != nil {
		t.Fatalf("jwk: %v", err)
	}
	setJSON, _ := json.Marshal(jwkset.JWKSMarshal{Keys: []jwkset.JWKMarshal{jwk.Marshal()}})
	v, err := NewVerifierFromJWKSetJSON(nil, setJSON, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewVerifierFromJWKSetJSON: %v", err)
	}
	token := s.sign(t, Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "sub-1"}})
	if _, err := v.Verify(token); err != nil {
		t.Errorf("verify with offline verifier failed: %v", err)
	}
}

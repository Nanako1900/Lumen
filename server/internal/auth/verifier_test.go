package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://auth.test/realms/lumen"
	testAudience = "lumen-api"
	testKID      = "test-key-1"
)

// testSigner bundles an RSA key and a Verifier that trusts its public half via
// a locally-built JWK set (no external IdP, no HTTP).
type testSigner struct {
	priv     *rsa.PrivateKey
	verifier *Verifier
}

func newTestSigner(t *testing.T) *testSigner {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	jwk, err := jwkset.NewJWKFromKey(priv.Public(), jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{
			ALG: jwkset.AlgRS256,
			KID: testKID,
			USE: jwkset.UseSig,
		},
	})
	if err != nil {
		t.Fatalf("build JWK: %v", err)
	}
	setJSON, err := json.Marshal(jwkset.JWKSMarshal{
		Keys: []jwkset.JWKMarshal{jwk.Marshal()},
	})
	if err != nil {
		t.Fatalf("marshal JWK set: %v", err)
	}
	kf, err := keyfunc.NewJWKSetJSON(setJSON)
	if err != nil {
		t.Fatalf("build keyfunc: %v", err)
	}
	return &testSigner{priv: priv, verifier: newVerifierWithKeyfunc(kf, testIssuer, testAudience)}
}

// sign issues an RS256 token with the given claims, applying sensible defaults.
func (s *testSigner) sign(t *testing.T, claims Claims) string {
	t.Helper()
	if claims.Issuer == "" {
		claims.Issuer = testIssuer
	}
	if len(claims.Audience) == 0 {
		claims.Audience = jwt.ClaimStrings{testAudience}
	}
	if claims.ExpiresAt == nil {
		claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Hour))
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = testKID
	signed, err := tok.SignedString(s.priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func TestVerify_ValidToken(t *testing.T) {
	s := newTestSigner(t)
	token := s.sign(t, Claims{
		Name:    "Nanako",
		Picture: "https://cdn.test/a.png",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "sub-abc",
		},
	})

	claims, err := s.verifier.Verify(token)
	if err != nil {
		t.Fatalf("expected valid token, got: %v", err)
	}
	if claims.Subject != "sub-abc" {
		t.Errorf("Subject = %q, want sub-abc", claims.Subject)
	}
	if claims.Name != "Nanako" {
		t.Errorf("Name = %q, want Nanako", claims.Name)
	}
}

func TestVerify_Expired(t *testing.T) {
	s := newTestSigner(t)
	token := s.sign(t, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "sub-abc",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	})

	_, err := s.verifier.Verify(token)
	if err == nil {
		t.Fatal("expected expired token to be rejected")
	}
	if !IsExpired(err) {
		t.Errorf("IsExpired should be true for expired token, err: %v", err)
	}
}

func TestVerify_WrongAudience(t *testing.T) {
	s := newTestSigner(t)
	token := s.sign(t, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  "sub-abc",
			Audience: jwt.ClaimStrings{"some-other-api"},
		},
	})

	_, err := s.verifier.Verify(token)
	if err == nil {
		t.Fatal("expected wrong-audience token to be rejected")
	}
	if IsExpired(err) {
		t.Error("wrong audience should not be classified as expired")
	}
}

func TestVerify_WrongIssuer(t *testing.T) {
	s := newTestSigner(t)
	token := s.sign(t, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "sub-abc",
			Issuer:  "https://evil.test/realms/lumen",
		},
	})

	if _, err := s.verifier.Verify(token); err == nil {
		t.Fatal("expected wrong-issuer token to be rejected")
	}
}

func TestVerify_RejectsNoneAlg(t *testing.T) {
	s := newTestSigner(t)
	// Forge an unsigned "none" token; must be rejected by WithValidMethods.
	claims := Claims{RegisteredClaims: jwt.RegisteredClaims{
		Subject:   "sub-abc",
		Issuer:    testIssuer,
		Audience:  jwt.ClaimStrings{testAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	unsigned, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("build none token: %v", err)
	}
	if _, err := s.verifier.Verify(unsigned); err == nil {
		t.Fatal("expected 'none' alg token to be rejected (alg confusion defence)")
	}
}

func TestVerify_GarbageToken(t *testing.T) {
	s := newTestSigner(t)
	if _, err := s.verifier.Verify("not.a.jwt"); err == nil {
		t.Fatal("expected garbage token to be rejected")
	}
}

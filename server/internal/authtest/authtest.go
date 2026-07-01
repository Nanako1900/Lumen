// Package authtest provides helpers to build a real auth.Verifier backed by an
// in-process RSA key and locally-built JWK set, plus a token signer. It lets
// other packages' tests exercise authenticated paths without an external IdP.
package authtest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/golang-jwt/jwt/v5"

	"lumen/internal/auth"
)

// Default issuer/audience for test tokens.
const (
	Issuer   = "https://auth.test/realms/lumen"
	Audience = "lumen-api"
	kid      = "authtest-key"
)

// Signer bundles an RSA key with a Verifier that trusts its public half.
type Signer struct {
	priv     *rsa.PrivateKey
	Verifier *auth.Verifier
}

// NewSigner builds a Signer with a fresh RSA key and a matching Verifier.
func NewSigner(t *testing.T) *Signer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	jwk, err := jwkset.NewJWKFromKey(priv.Public(), jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{ALG: jwkset.AlgRS256, KID: kid, USE: jwkset.UseSig},
	})
	if err != nil {
		t.Fatalf("build JWK: %v", err)
	}
	setJSON, err := json.Marshal(jwkset.JWKSMarshal{Keys: []jwkset.JWKMarshal{jwk.Marshal()}})
	if err != nil {
		t.Fatalf("marshal JWK set: %v", err)
	}
	v, err := auth.NewVerifierFromJWKSetJSON(context.Background(), setJSON, Issuer, Audience)
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	return &Signer{priv: priv, Verifier: v}
}

// Token issues an RS256 token for the given subject with optional name/picture.
func (s *Signer) Token(t *testing.T, subject, name, picture string) string {
	t.Helper()
	claims := auth.Claims{
		Name:    name,
		Picture: picture,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    Issuer,
			Audience:  jwt.ClaimStrings{Audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	return s.signClaims(t, claims)
}

// ExpiredToken issues an already-expired RS256 token for a subject.
func (s *Signer) ExpiredToken(t *testing.T, subject string) string {
	t.Helper()
	claims := auth.Claims{RegisteredClaims: jwt.RegisteredClaims{
		Subject:   subject,
		Issuer:    Issuer,
		Audience:  jwt.ClaimStrings{Audience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
	}}
	return s.signClaims(t, claims)
}

func (s *Signer) signClaims(t *testing.T, claims auth.Claims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(s.priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

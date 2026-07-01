// Package auth is the authentication gateway (contract §2, server-design §2).
// It verifies IdP-issued JWT access tokens locally via JWKS, decides owner
// status from configuration, maps claims to profiles, and provides the REST
// Bearer middleware. Both channels (REST middleware, WS handshake) share one
// Verifier.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// leeway tolerates clock drift between the server and the IdP.
const leeway = 30 * time.Second

// Claims are the expected access-token claims (contract §2.3 field mapping).
// RegisteredClaims supplies Subject (sub), Issuer, Audience and ExpiresAt.
type Claims struct {
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Picture           string `json:"picture"`
	jwt.RegisteredClaims
}

// Verifier holds the auto-refreshing JWKS keyfunc and validation parameters.
// It is constructed once at startup and is immutable and concurrency-safe.
type Verifier struct {
	kf       keyfunc.Keyfunc
	issuer   string
	audience string
	leeway   time.Duration
}

// NewVerifier builds a Verifier whose background JWKS refresh goroutine is
// bound to ctx (cancel to reclaim it). It fetches, caches and rotates keys
// automatically, refreshing on an unknown kid.
func NewVerifier(ctx context.Context, jwksURL, issuer, audience string) (*Verifier, error) {
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("初始化 JWKS keyfunc 失败: %w", err)
	}
	return &Verifier{kf: kf, issuer: issuer, audience: audience, leeway: leeway}, nil
}

// newVerifierWithKeyfunc lets tests inject an in-process keyfunc.
func newVerifierWithKeyfunc(kf keyfunc.Keyfunc, issuer, audience string) *Verifier {
	return &Verifier{kf: kf, issuer: issuer, audience: audience, leeway: leeway}
}

// NewVerifierFromJWKSetJSON builds a Verifier from a static JWK Set JSON. It is
// primarily used by tests (and any offline-keys scenario); the ctx is accepted
// for API symmetry with NewVerifier though this path does no background refresh.
func NewVerifierFromJWKSetJSON(_ context.Context, jwkSetJSON []byte, issuer, audience string) (*Verifier, error) {
	kf, err := keyfunc.NewJWKSetJSON(jwkSetJSON)
	if err != nil {
		return nil, fmt.Errorf("从 JWK Set 构造验签器失败: %w", err)
	}
	return newVerifierWithKeyfunc(kf, issuer, audience), nil
}

// Verify checks the signature and iss/aud/exp, returning the validated Claims.
// It enforces RS256 only (defeating alg-confusion / "none" attacks). Callers
// use IsExpired on the returned error to distinguish an expired token.
func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(
		tokenString, claims, v.kf.Keyfunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(v.leeway),
	)
	if err != nil {
		return nil, fmt.Errorf("JWT 校验失败: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("JWT 无效")
	}
	return claims, nil
}

// IsExpired reports whether err denotes an expired token, so callers can map
// to TOKEN_EXPIRED rather than TOKEN_INVALID (server-design §2.1 table).
func IsExpired(err error) bool {
	return errors.Is(err, jwt.ErrTokenExpired)
}

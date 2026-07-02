// Package broker is the Go port of the EdgeOne Pages Functions that implement
// the account-center OIDC login and the desktop login broker (web-design.md §5,
// §6). It runs in parallel with the still-live TypeScript functions and mirrors
// their behavior 1:1, backed by the config, store and secure packages from
// stage A.
//
// This file mirrors website/functions/_lib/pkce.ts. The heavy lifting (entropy,
// base64url, S256, constant-time compare) lives in the shared secure package so
// the Go server and the TS functions stay byte-identical; the aliases below give
// the broker the same vocabulary as the reference TS without re-implementing the
// primitives.
package broker

import "lumen/internal/secure"

// PKCE / handoff token entropy sizes (web-design.md §5.5). oidc verifier/state,
// handoff_code and challenge material use >=32 bytes; a desktop_session_id uses
// 48 bytes. These re-export the secure package constants so the broker reads
// like pkce.ts (randomToken defaults, randomToken(48) for the session id).
const (
	tokenBytes     = secure.DefaultTokenBytes // 32
	sessionIDBytes = secure.SessionIDBytes    // 48
)

// randomToken returns a high-entropy base64url token of byteLen bytes from
// crypto/rand (mirrors pkce.ts randomToken). byteLen<=0 falls back to 32.
func randomToken(byteLen int) string {
	return secure.RandomToken(byteLen)
}

// s256 returns base64url(SHA-256(verifier)) — the PKCE code_challenge and the
// handoff bound_challenge (mirrors pkce.ts s256).
func s256(verifier string) string {
	return secure.S256(verifier)
}

// isBase64Url reports whether value is a non-empty URL-safe base64 string with
// no padding (mirrors pkce.ts isBase64Url: /^[A-Za-z0-9_-]+$/ and length > 0).
func isBase64Url(value string) bool {
	return secure.IsBase64URL(value)
}

// constantTimeEqual compares two strings in constant time (mirrors pkce.ts
// timingSafeEqual → crypto/subtle.ConstantTimeCompare). Unequal lengths return
// false, matching the TS length short-circuit.
func constantTimeEqual(a, b string) bool {
	return secure.ConstantTimeEqualString(a, b)
}

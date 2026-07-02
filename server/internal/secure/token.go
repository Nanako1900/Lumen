// Package secure holds the shared cryptographic primitives for the account
// center / desktop broker flow (web-design.md §5.5, §6.2, §8). It mirrors the
// EdgeOne functions library 1:1 so the Go server and the TypeScript Pages
// Functions stay behaviorally identical while both run in parallel:
//
//	website/functions/_lib/pkce.ts   -> Base64URLEncode / Base64URLDecode /
//	                                    IsBase64URL / RandomToken / S256 /
//	                                    ConstantTimeEqualString
//	website/functions/_lib/session.ts -> Seal / Open (AES-256-GCM cookies)
//
// Entropy comes from crypto/rand only (never math/rand). All encodings use
// base64url without padding (RFC 7636).
package secure

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// Token byte sizes (web-design.md §5.5). oidc verifier/state, handoff_code and
// challenge material use >=32 bytes; the desktop session id uses 48 bytes.
const (
	// DefaultTokenBytes is the entropy for verifier/state/handoff_code.
	DefaultTokenBytes = 32
	// SessionIDBytes is the entropy for a desktop_session_id.
	SessionIDBytes = 48
)

// Base64URLEncode encodes bytes as base64url without padding (mirrors
// pkce.ts base64urlEncode).
func Base64URLEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// Base64URLDecode decodes a base64url string without padding, rejecting any
// input that is not valid base64url (mirrors pkce.ts base64urlDecode, which
// first calls isBase64Url).
func Base64URLDecode(s string) ([]byte, error) {
	if !IsBase64URL(s) {
		return nil, fmt.Errorf("invalid base64url input")
	}
	return base64.RawURLEncoding.DecodeString(s)
}

// IsBase64URL reports whether value is a non-empty URL-safe base64 string with
// no padding (mirrors pkce.ts isBase64Url: /^[A-Za-z0-9_-]+$/ and length > 0).
func IsBase64URL(value string) bool {
	if len(value) == 0 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}

// RandomToken returns a high-entropy base64url token of the given byte length
// using crypto/rand (mirrors pkce.ts randomToken). It panics only if the
// system CSPRNG fails, which is unrecoverable.
func RandomToken(byteLen int) string {
	if byteLen <= 0 {
		byteLen = DefaultTokenBytes
	}
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("secure: crypto/rand failed: %v", err))
	}
	return Base64URLEncode(b)
}

// S256 returns base64url(SHA-256(verifier)), the PKCE code_challenge / handoff
// challenge (mirrors pkce.ts s256).
func S256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return Base64URLEncode(sum[:])
}

// ConstantTimeEqualString compares two strings in constant time (mirrors
// pkce.ts timingSafeEqual, which is the TS analogue of
// crypto/subtle.ConstantTimeCompare). Unequal lengths return false, matching
// the TS length short-circuit while still avoiding early content leaks.
func ConstantTimeEqualString(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

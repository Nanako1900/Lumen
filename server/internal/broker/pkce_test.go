package broker

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// TestS256MatchesRawSHA256 mirrors pkce.test.ts: s256(verifier) ==
// base64url(SHA-256(verifier)) so Go and TS PKCE challenges are byte-identical.
func TestS256MatchesRawSHA256(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got := s256(verifier); got != want {
		t.Errorf("s256 = %q, want %q", got, want)
	}
}

// TestIsBase64Url mirrors pkce.test.ts isBase64Url cases.
func TestIsBase64Url(t *testing.T) {
	valid := []string{"abc", "ABC123", "a-b_c", "x"}
	invalid := []string{"", "a+b", "a/b", "a=b", "a b", "π"}
	for _, v := range valid {
		if !isBase64Url(v) {
			t.Errorf("isBase64Url(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if isBase64Url(v) {
			t.Errorf("isBase64Url(%q) = true, want false", v)
		}
	}
}

// TestRandomTokenSizes mirrors pkce.test.ts randomToken entropy/size behavior.
func TestRandomTokenSizes(t *testing.T) {
	// default 32-byte token is base64url.
	tok := randomToken(tokenBytes)
	if !isBase64Url(tok) {
		t.Fatalf("token %q not base64url", tok)
	}
	// 48-byte session id decodes to exactly 48 bytes → length > 40 like the TS
	// exchange assertion.
	sid := randomToken(sessionIDBytes)
	if len(sid) <= 40 {
		t.Errorf("session id length = %d, want > 40", len(sid))
	}
	dec, err := base64.RawURLEncoding.DecodeString(sid)
	if err != nil {
		t.Fatalf("decode session id: %v", err)
	}
	if len(dec) != sessionIDBytes {
		t.Errorf("session id decodes to %d bytes, want %d", len(dec), sessionIDBytes)
	}
	// uniqueness over many draws.
	seen := make(map[string]struct{}, 512)
	for i := 0; i < 512; i++ {
		x := randomToken(tokenBytes)
		if _, dup := seen[x]; dup {
			t.Fatalf("duplicate token at %d", i)
		}
		seen[x] = struct{}{}
	}
}

// TestConstantTimeEqual mirrors pkce timingSafeEqual behavior.
func TestConstantTimeEqual(t *testing.T) {
	if !constantTimeEqual("abc123", "abc123") {
		t.Error("equal strings should compare equal")
	}
	if constantTimeEqual("abc", "abcd") {
		t.Error("different-length strings must not be equal")
	}
	if constantTimeEqual("abc", "abd") {
		t.Error("different strings must not be equal")
	}
}

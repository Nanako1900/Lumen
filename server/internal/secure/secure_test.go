package secure

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestBase64URLRoundTrip(t *testing.T) {
	cases := [][]byte{
		{},
		{0x00},
		{0xff, 0xfe, 0xfd},
		[]byte("hello world"),
		bytes.Repeat([]byte{0xab}, 48),
	}
	for _, in := range cases {
		enc := Base64URLEncode(in)
		if strings.ContainsAny(enc, "+/=") {
			t.Errorf("encoding %x contains non-url-safe or padding chars: %q", in, enc)
		}
		if len(in) == 0 {
			continue // empty decodes fail IsBase64URL (matches TS length>0 rule)
		}
		out, err := Base64URLDecode(enc)
		if err != nil {
			t.Fatalf("decode %q: %v", enc, err)
		}
		if !bytes.Equal(in, out) {
			t.Errorf("round trip mismatch: %x -> %q -> %x", in, enc, out)
		}
	}
}

func TestBase64URLDecodeRejectsInvalid(t *testing.T) {
	for _, bad := range []string{"", "abc=", "a+b", "a/b", "has space", "pad=="} {
		if _, err := Base64URLDecode(bad); err == nil {
			t.Errorf("Base64URLDecode(%q) should error", bad)
		}
	}
}

func TestIsBase64URL(t *testing.T) {
	valid := []string{"abc", "ABC123", "a-b_c", "x"}
	invalid := []string{"", "a+b", "a/b", "a=b", "a b", "π"}
	for _, v := range valid {
		if !IsBase64URL(v) {
			t.Errorf("IsBase64URL(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if IsBase64URL(v) {
			t.Errorf("IsBase64URL(%q) = true, want false", v)
		}
	}
}

func TestRandomTokenEntropyAndAlphabet(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 1000; i++ {
		tok := RandomToken(DefaultTokenBytes)
		if !IsBase64URL(tok) {
			t.Fatalf("token %q is not base64url", tok)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("duplicate token %q at iter %d", tok, i)
		}
		seen[tok] = struct{}{}
	}
	// 48-byte session id decodes to exactly 48 bytes.
	sid := RandomToken(SessionIDBytes)
	dec, err := Base64URLDecode(sid)
	if err != nil {
		t.Fatalf("decode session id: %v", err)
	}
	if len(dec) != SessionIDBytes {
		t.Errorf("session id decodes to %d bytes, want %d", len(dec), SessionIDBytes)
	}
	// Zero/negative byteLen falls back to DefaultTokenBytes.
	if dec, _ := Base64URLDecode(RandomToken(0)); len(dec) != DefaultTokenBytes {
		t.Errorf("RandomToken(0) fell back to %d bytes, want %d", len(dec), DefaultTokenBytes)
	}
}

func TestS256MatchesRawSHA256(t *testing.T) {
	// s256(verifier) must equal base64url(SHA-256(verifier)) so the Go and TS
	// PKCE challenges are byte-identical for the same verifier.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got := S256(verifier); got != want {
		t.Errorf("S256 = %q, want %q", got, want)
	}
}

func TestConstantTimeEqualString(t *testing.T) {
	if !ConstantTimeEqualString("abc123", "abc123") {
		t.Error("equal strings should compare equal")
	}
	if ConstantTimeEqualString("abc", "abcd") {
		t.Error("different-length strings must not be equal")
	}
	if ConstantTimeEqualString("abc", "abd") {
		t.Error("different strings must not be equal")
	}
}

func newTestSealer(t *testing.T) *Sealer {
	t.Helper()
	key := bytes.Repeat([]byte{0x2a}, 32)
	s, err := NewSealer(key)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

func TestNewSealerRejectsWrongKeyLen(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := NewSealer(bytes.Repeat([]byte{1}, n)); err == nil {
			t.Errorf("NewSealer with %d-byte key should error", n)
		}
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	s := newTestSealer(t)
	plain := []byte(`{"sub":"user-1","display_name":"Alice"}`)
	sealed, err := s.Seal(plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Format is iv.ciphertext, both base64url.
	if !strings.Contains(sealed, ".") {
		t.Fatalf("sealed value missing dot separator: %q", sealed)
	}
	opened, err := s.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(plain, opened) {
		t.Errorf("round trip mismatch: %q vs %q", plain, opened)
	}
	// Fresh IV each call: two seals of the same plaintext differ.
	sealed2, _ := s.Seal(plain)
	if sealed == sealed2 {
		t.Error("two seals of identical plaintext should differ (random IV)")
	}
}

func TestOpenRejectsTamperAndMalformed(t *testing.T) {
	s := newTestSealer(t)
	sealed, _ := s.Seal([]byte("secret"))

	// Flip a byte in the ciphertext portion -> GCM auth failure.
	dot := strings.IndexByte(sealed, '.')
	tampered := sealed[:dot+1] + flipFirstChar(sealed[dot+1:])
	if _, err := s.Open(tampered); err == nil {
		t.Error("tampered ciphertext should fail to open")
	}

	// Wrong key cannot open.
	other, _ := NewSealer(bytes.Repeat([]byte{0x01}, 32))
	if _, err := other.Open(sealed); err == nil {
		t.Error("sealed value should not open under a different key")
	}

	for _, bad := range []string{"", "nodot", ".", "a.", ".b", "!!!.???"} {
		if _, err := s.Open(bad); err == nil {
			t.Errorf("Open(%q) should error", bad)
		}
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	s := newTestSealer(t)
	plain := []byte("refresh-token-value-xyz")
	enc, err := s.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Ciphertext is longer than plaintext (12-byte IV + 16-byte tag).
	if len(enc) < len(plain)+nonceSize {
		t.Errorf("ciphertext too short: %d", len(enc))
	}
	dec, err := s.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(plain, dec) {
		t.Errorf("round trip mismatch: %q vs %q", plain, dec)
	}
	// Tamper detection.
	enc[len(enc)-1] ^= 0xff
	if _, err := s.Decrypt(enc); err == nil {
		t.Error("tampered bytea should fail to decrypt")
	}
	// Too short.
	if _, err := s.Decrypt([]byte{1, 2, 3}); err == nil {
		t.Error("short ciphertext should fail")
	}
}

func TestDecodeKey(t *testing.T) {
	raw := bytes.Repeat([]byte{0x7f}, 32)
	for _, enc := range []string{
		base64.StdEncoding.EncodeToString(raw),
		base64.RawStdEncoding.EncodeToString(raw),
		base64.URLEncoding.EncodeToString(raw),
		base64.RawURLEncoding.EncodeToString(raw),
	} {
		got, err := DecodeKey(enc)
		if err != nil {
			t.Fatalf("DecodeKey(%q): %v", enc, err)
		}
		if !bytes.Equal(got, raw) {
			t.Errorf("DecodeKey(%q) mismatch", enc)
		}
	}
	// Wrong length and garbage fail.
	if _, err := DecodeKey(base64.StdEncoding.EncodeToString(raw[:16])); err == nil {
		t.Error("16-byte key should fail")
	}
	if _, err := DecodeKey("not base64 @@@"); err == nil {
		t.Error("non-base64 should fail")
	}
	if _, err := DecodeKey(""); err == nil {
		t.Error("empty should fail")
	}
}

func flipFirstChar(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	return string(b)
}

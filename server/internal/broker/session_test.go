package broker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSessionSealOpenRoundTrip mirrors session.test.ts round-trip.
func TestSessionSealOpenRoundTrip(t *testing.T) {
	sealer := newTestSealer(t)
	sealed, err := sealSession(sealer, webSession{
		Sub:         "user-1",
		DisplayName: "Alice",
		AvatarURL:   "https://img/a.png",
		Exp:         defaultSessionExp(),
	})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	opened := openSession(sealer, sealed)
	if opened == nil {
		t.Fatal("opened session is nil")
	}
	if opened.Sub != "user-1" || opened.DisplayName != "Alice" || opened.AvatarURL != "https://img/a.png" {
		t.Errorf("opened = %+v", opened)
	}
}

// TestSessionRejectsTamper mirrors session.test.ts tamper rejection. It flips a
// base64url character in the INTERIOR of the ciphertext segment (not the final
// character): RawURLEncoding's last character only encodes the trailing 2/4 bits
// of a non-3-multiple length, so several distinct final characters decode to the
// same bytes — flipping only the last char can leave the ciphertext unchanged
// and would spuriously "open". An interior character maps to a full 6 bits, so
// changing it always alters the decoded ciphertext and must fail GCM auth.
func TestSessionRejectsTamper(t *testing.T) {
	sealer := newTestSealer(t)
	sealed, _ := sealSession(sealer, webSession{Sub: "u", DisplayName: "d", AvatarURL: "a", Exp: defaultSessionExp()})
	dot := strings.IndexByte(sealed, '.')
	if dot < 0 || dot >= len(sealed)-1 {
		t.Fatalf("unexpected sealed format: %q", sealed)
	}
	// Pick an interior index within the ciphertext segment (well before its
	// final character) so the mutation always changes real ciphertext bytes.
	idx := dot + 1
	orig := sealed[idx]
	repl := byte('A')
	if orig == 'A' {
		repl = 'B'
	}
	tampered := sealed[:idx] + string(repl) + sealed[idx+1:]
	if tampered == sealed {
		t.Fatal("tampered value should differ from the original")
	}
	if openSession(sealer, tampered) != nil {
		t.Error("tampered ciphertext should not open")
	}
}

// TestSessionRejectsExpired mirrors session.test.ts expiry rejection.
func TestSessionRejectsExpired(t *testing.T) {
	sealer := newTestSealer(t)
	sealed, _ := sealSession(sealer, webSession{Sub: "u", DisplayName: "d", AvatarURL: "a", Exp: nowSeconds() - 10})
	if openSession(sealer, sealed) != nil {
		t.Error("expired session should not open")
	}
}

// TestSessionRejectsMalformed mirrors session.test.ts malformed rejection.
func TestSessionRejectsMalformed(t *testing.T) {
	sealer := newTestSealer(t)
	for _, bad := range []string{"garbage", "a.b", ""} {
		if openSession(sealer, bad) != nil {
			t.Errorf("openSession(%q) should be nil", bad)
		}
	}
}

// TestSessionCookieAttributes verifies host-only cookie attributes (decision 1):
// HttpOnly + Secure + SameSite=Lax + Path=/ and NO Domain attribute.
func TestSessionCookieAttributes(t *testing.T) {
	c := buildSessionCookie("value123")
	got := c.String()
	for _, want := range []string{"lumen_session=value123", "HttpOnly", "Secure", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(got, want) {
			t.Errorf("cookie %q missing %q", got, want)
		}
	}
	if strings.Contains(strings.ToLower(got), "domain=") {
		t.Errorf("session cookie must be host-only (no Domain): %q", got)
	}
}

// TestClearSessionCookie verifies the clear cookie sets Max-Age=0.
func TestClearSessionCookie(t *testing.T) {
	got := clearSessionCookie().String()
	if !strings.Contains(got, "Max-Age=0") {
		t.Errorf("clear cookie should set Max-Age=0: %q", got)
	}
}

// TestReadSessionCookie verifies reading the session value from a Cookie header.
func TestReadSessionCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://x/", nil)
	req.Header.Set("Cookie", "other=1; lumen_session=abc.def; more=2")
	if got := readSessionCookie(req); got != "abc.def" {
		t.Errorf("readSessionCookie = %q, want abc.def", got)
	}
	if got := readSessionCookie(httptest.NewRequest(http.MethodGet, "https://x/", nil)); got != "" {
		t.Errorf("readSessionCookie(no cookie) = %q, want empty", got)
	}
}

// TestAuthFlowRoundTrip verifies the auth-flow cookie seals verifier+state and
// rejects a payload missing them.
func TestAuthFlowRoundTrip(t *testing.T) {
	sealer := newTestSealer(t)
	sealed, err := sealAuthFlow(sealer, authFlowContext{Verifier: "v", State: "s", Exp: defaultAuthFlowExp()})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	opened := openAuthFlow(sealer, sealed)
	if opened == nil || opened.Verifier != "v" || opened.State != "s" {
		t.Errorf("opened auth flow = %+v", opened)
	}

	// A session payload without verifier/state must not open as an auth flow.
	sessSealed, _ := sealSession(sealer, webSession{Sub: "u", Exp: defaultSessionExp()})
	if openAuthFlow(sealer, sessSealed) != nil {
		t.Error("session payload should not open as auth flow (missing verifier/state)")
	}
}

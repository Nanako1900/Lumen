package broker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFetchIdentity_OpaqueToken covers the plain-OAuth2 path (Nanako OAuth): the
// token pair carries no parseable JWT subject, so fetchIdentity derives both the
// subject and the profile from /userinfo using the flexible field parser.
func TestFetchIdentity_OpaqueToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer opaque-xyz" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":6789,"username":"nanako","avatar":"https://x/n.png"}`))
	}))
	defer srv.Close()

	oc := newOIDCClientRaw(oidcConfig{UserinfoURL: srv.URL})

	// idToken empty and access_token opaque (not a JWT) → subjectFrom yields "".
	sub, profile := oc.fetchIdentity(context.Background(), "opaque-xyz", "")
	if sub != "6789" {
		t.Errorf("sub = %q, want 6789 (from userinfo id)", sub)
	}
	if profile.DisplayName != "nanako" {
		t.Errorf("display_name = %q, want nanako", profile.DisplayName)
	}
	if profile.AvatarURL != "https://x/n.png" {
		t.Errorf("avatar_url = %q, want https://x/n.png", profile.AvatarURL)
	}
}

// TestFetchIdentity_JWTSubjectWins verifies the OIDC path still prefers the JWT
// subject when the token carries one (no userinfo dependency for sub).
func TestFetchIdentity_JWTSubjectWins(t *testing.T) {
	oc := newOIDCClientRaw(oidcConfig{}) // no userinfo URL configured
	idToken := fakeJWT(map[string]any{"sub": "jwt-sub", "name": "Alice", "picture": "https://x/a.png"})

	sub, profile := oc.fetchIdentity(context.Background(), "at", idToken)
	if sub != "jwt-sub" {
		t.Errorf("sub = %q, want jwt-sub", sub)
	}
	if profile.DisplayName != "Alice" || profile.AvatarURL != "https://x/a.png" {
		t.Errorf("profile = %+v, want {Alice, https://x/a.png}", profile)
	}
}

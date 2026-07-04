package auth

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newUserinfoStub returns a userinfo endpoint that answers 200 with body for the
// token "good", 401 for anything else, and counts how many times it was hit.
func newUserinfoStub(t *testing.T, body string) (*httptest.Server, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestUserinfoVerify_Success(t *testing.T) {
	srv, _ := newUserinfoStub(t, `{"id":42,"username":"nanako","avatar":"https://x/a.png"}`)
	v := NewUserinfoVerifier(srv.URL, nil)

	claims, err := v.Verify("good")
	if err != nil {
		t.Fatalf("Verify(good) error: %v", err)
	}
	if claims.Subject != "42" {
		t.Errorf("Subject = %q, want 42", claims.Subject)
	}
	if claims.Name != "nanako" {
		t.Errorf("Name = %q, want nanako", claims.Name)
	}
	if claims.Picture != "https://x/a.png" {
		t.Errorf("Picture = %q, want https://x/a.png", claims.Picture)
	}
}

func TestUserinfoVerify_RejectsBadToken(t *testing.T) {
	srv, _ := newUserinfoStub(t, `{"id":1}`)
	v := NewUserinfoVerifier(srv.URL, nil)

	if _, err := v.Verify("bad"); err == nil {
		t.Fatal("Verify(bad) = nil error, want rejection")
	}
	if IsExpired(nil) {
		t.Fatal("IsExpired(nil) must be false")
	}
}

func TestUserinfoVerify_MissingSubjectIsError(t *testing.T) {
	srv, _ := newUserinfoStub(t, `{"username":"noid"}`)
	v := NewUserinfoVerifier(srv.URL, nil)

	if _, err := v.Verify("good"); err == nil {
		t.Fatal("Verify with no subject field = nil error, want error")
	}
}

func TestUserinfoVerify_CachesWithinTTL(t *testing.T) {
	srv, hits := newUserinfoStub(t, `{"sub":"s1","name":"A"}`)
	v := NewUserinfoVerifier(srv.URL, nil)

	for i := 0; i < 3; i++ {
		if _, err := v.Verify("good"); err != nil {
			t.Fatalf("Verify #%d error: %v", i, err)
		}
	}
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Errorf("userinfo hits = %d, want 1 (cached after first)", got)
	}
}

func TestUserinfoVerify_RefetchesAfterTTL(t *testing.T) {
	srv, hits := newUserinfoStub(t, `{"sub":"s1","name":"A"}`)
	v := NewUserinfoVerifier(srv.URL, nil)

	base := time.Unix(1_700_000_000, 0)
	cur := base
	v.cache.now = func() time.Time { return cur }

	if _, err := v.Verify("good"); err != nil {
		t.Fatalf("first Verify error: %v", err)
	}
	// Advance past the TTL so the cached entry is considered stale.
	cur = base.Add(userinfoCacheTTL + time.Second)
	if _, err := v.Verify("good"); err != nil {
		t.Fatalf("second Verify error: %v", err)
	}
	if got := atomic.LoadInt64(hits); got != 2 {
		t.Errorf("userinfo hits = %d, want 2 (re-fetch after TTL)", got)
	}
}

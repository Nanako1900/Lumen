package storefake

import (
	"context"
	"testing"

	"lumen/internal/store"
)

// These logic-level tests exercise the broker semantics (single-use consume,
// expiry guard, wrong-kind isolation) against the in-memory Fake, matching the
// contract the real pgStore enforces via DELETE ... RETURNING + expires_at.

func TestFakeLoginContext_OneTimeConsume(t *testing.T) {
	f := New()
	ctx := context.Background()
	lc := store.LoginContext{State: "st", Challenge: "ch", RedirectURI: "http://127.0.0.1:1234/cb", OIDCVerifier: "v"}

	if err := f.PutLoginContext(ctx, "oidc-state", lc); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := f.TakeLoginContext(ctx, "oidc-state")
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if got != lc {
		t.Errorf("take = %+v, want %+v", got, lc)
	}
	// Second take: gone (single-use).
	if _, err := f.TakeLoginContext(ctx, "oidc-state"); err != store.ErrNotFound {
		t.Errorf("second take err = %v, want ErrNotFound", err)
	}
}

func TestFakeHandoff_OneTimeConsumeAndWrongKind(t *testing.T) {
	f := New()
	ctx := context.Background()
	h := store.Handoff{
		AccessToken:    "at",
		ExpiresIn:      300,
		RefreshToken:   "rt",
		Sub:            "sub-1",
		BoundChallenge: "bc",
		Profile:        store.DesktopProfile{DisplayName: "Alice", AvatarURL: "https://cdn/a.png"},
	}
	if err := f.PutHandoff(ctx, "code", h); err != nil {
		t.Fatalf("put: %v", err)
	}
	// A handoff key cannot be consumed as a login_ctx (wrong kind).
	if _, err := f.TakeLoginContext(ctx, "code"); err != store.ErrNotFound {
		t.Errorf("wrong-kind take err = %v, want ErrNotFound", err)
	}
	got, err := f.ConsumeHandoff(ctx, "code")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got != h {
		t.Errorf("consume = %+v, want %+v", got, h)
	}
	if _, err := f.ConsumeHandoff(ctx, "code"); err != store.ErrNotFound {
		t.Errorf("second consume err = %v, want ErrNotFound", err)
	}
}

func TestFakeBroker_ExpiryGuard(t *testing.T) {
	f := New()
	ctx := context.Background()
	// Force an already-expired row by writing directly with a past expiry.
	f.mu.Lock()
	f.broker["expired"] = brokerRow{kind: "handoff", payload: []byte(`{"sub":"x"}`)}
	f.mu.Unlock()

	if _, err := f.ConsumeHandoff(ctx, "expired"); err != store.ErrNotFound {
		t.Errorf("expired consume err = %v, want ErrNotFound", err)
	}
	// Consuming an expired row also removes it (janitor need not run).
	f.mu.Lock()
	_, present := f.broker["expired"]
	f.mu.Unlock()
	if present {
		t.Error("expired row should be deleted on read")
	}
}

func TestFakeDeleteExpiredBrokerStates(t *testing.T) {
	f := New()
	ctx := context.Background()
	// One live, one expired.
	if err := f.PutHandoff(ctx, "live", store.Handoff{Sub: "a"}); err != nil {
		t.Fatalf("put live: %v", err)
	}
	f.mu.Lock()
	f.broker["dead"] = brokerRow{kind: "login_ctx", payload: []byte(`{}`)} // zero expiry = expired
	f.mu.Unlock()

	n, err := f.DeleteExpiredBrokerStates(ctx)
	if err != nil {
		t.Fatalf("janitor: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d, want 1", n)
	}
	// Live row survives.
	if _, err := f.ConsumeHandoff(ctx, "live"); err != nil {
		t.Errorf("live row should survive janitor: %v", err)
	}
}

func TestFakeSessions_CRUD(t *testing.T) {
	f := New()
	ctx := context.Background()
	sess := store.DesktopSession{ID: "sid", RefreshToken: "rt", Sub: "sub-1"}
	if err := f.PutSession(ctx, sess); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := f.GetSession(ctx, "sid")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RefreshToken != "rt" || got.Sub != "sub-1" {
		t.Errorf("get = %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at should be defaulted on put")
	}
	// Rotate refresh_token.
	got.RefreshToken = "rt2"
	if err := f.PutSession(ctx, got); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	after, _ := f.GetSession(ctx, "sid")
	if after.RefreshToken != "rt2" {
		t.Errorf("rotated refresh = %q, want rt2", after.RefreshToken)
	}
	// Delete then missing.
	if err := f.DeleteSession(ctx, "sid"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.GetSession(ctx, "sid"); err != store.ErrNotFound {
		t.Errorf("get after delete err = %v, want ErrNotFound", err)
	}
	// Deleting a missing session is a no-op.
	if err := f.DeleteSession(ctx, "missing"); err != nil {
		t.Errorf("delete missing err = %v, want nil", err)
	}
}

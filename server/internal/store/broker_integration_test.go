package store

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"lumen/internal/secure"
)

// newBrokerStore connects to LUMEN_TEST_DATABASE_URL, migrates, truncates the
// broker tables, and wraps the pool with a refresh sealer so desktop-session
// encryption is exercised. It returns the raw *sql.DB alongside the Store so
// tests can age rows / inspect the bytea column directly. Skips when no test DB
// is wired (mirrors the existing integration harness).
func newBrokerStore(t *testing.T) (Store, *sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("LUMEN_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LUMEN_TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sealer, err := secure.NewSealer(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	st := NewWithSealer(db, sealer)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE broker_states, desktop_sessions`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return st, db, ctx
}

func TestIntegration_LoginContext_OneTimeAndExpiry(t *testing.T) {
	st, h, ctx := newBrokerStore(t)
	lc := LoginContext{State: "st", Challenge: "ch", RedirectURI: "http://127.0.0.1:5678/cb", OIDCVerifier: "verif"}

	if err := st.PutLoginContext(ctx, "oidc-1", lc); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := st.TakeLoginContext(ctx, "oidc-1")
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if got != lc {
		t.Errorf("take = %+v, want %+v", got, lc)
	}
	// Single-use: gone.
	if _, err := st.TakeLoginContext(ctx, "oidc-1"); err != ErrNotFound {
		t.Errorf("second take err = %v, want ErrNotFound", err)
	}

	// Expiry guard: write a row with a past expires_at directly, then Take must
	// treat it as absent.
	if err := st.PutLoginContext(ctx, "oidc-exp", lc); err != nil {
		t.Fatalf("put exp: %v", err)
	}
	if _, err := h.ExecContext(ctx,
		`UPDATE broker_states SET expires_at = now() - interval '1 second' WHERE key = $1`, "oidc-exp"); err != nil {
		t.Fatalf("age row: %v", err)
	}
	if _, err := st.TakeLoginContext(ctx, "oidc-exp"); err != ErrNotFound {
		t.Errorf("expired take err = %v, want ErrNotFound", err)
	}
}

func TestIntegration_Handoff_OneTimeAndWrongKind(t *testing.T) {
	st, _, ctx := newBrokerStore(t)
	h := Handoff{
		AccessToken:    "at",
		ExpiresIn:      300,
		RefreshToken:   "rt",
		Sub:            "sub-1",
		BoundChallenge: "bc",
		Profile:        DesktopProfile{DisplayName: "Alice", AvatarURL: "https://cdn/a.png"},
	}
	if err := st.PutHandoff(ctx, "code-1", h); err != nil {
		t.Fatalf("put: %v", err)
	}
	// Wrong kind: a handoff row cannot be consumed as login_ctx.
	if _, err := st.TakeLoginContext(ctx, "code-1"); err != ErrNotFound {
		t.Errorf("wrong-kind take err = %v, want ErrNotFound", err)
	}
	got, err := st.ConsumeHandoff(ctx, "code-1")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got != h {
		t.Errorf("consume = %+v, want %+v", got, h)
	}
	if _, err := st.ConsumeHandoff(ctx, "code-1"); err != ErrNotFound {
		t.Errorf("second consume err = %v, want ErrNotFound", err)
	}
	// Missing code.
	if _, err := st.ConsumeHandoff(ctx, "nope"); err != ErrNotFound {
		t.Errorf("missing consume err = %v, want ErrNotFound", err)
	}
}

func TestIntegration_DeleteExpiredBrokerStates(t *testing.T) {
	st, h, ctx := newBrokerStore(t)
	if err := st.PutHandoff(ctx, "live", Handoff{Sub: "a"}); err != nil {
		t.Fatalf("put live: %v", err)
	}
	if err := st.PutHandoff(ctx, "dead", Handoff{Sub: "b"}); err != nil {
		t.Fatalf("put dead: %v", err)
	}
	if _, err := h.ExecContext(ctx,
		`UPDATE broker_states SET expires_at = now() - interval '1 minute' WHERE key = $1`, "dead"); err != nil {
		t.Fatalf("age dead: %v", err)
	}
	n, err := st.DeleteExpiredBrokerStates(ctx)
	if err != nil {
		t.Fatalf("janitor: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d, want 1", n)
	}
	if _, err := st.ConsumeHandoff(ctx, "live"); err != nil {
		t.Errorf("live should survive: %v", err)
	}
}

func TestIntegration_DesktopSessions_EncryptedAtRest(t *testing.T) {
	st, h, ctx := newBrokerStore(t)
	sess := DesktopSession{ID: "sid-1", RefreshToken: "super-secret-refresh", Sub: "sub-1", CreatedAt: time.Now().UTC()}
	if err := st.PutSession(ctx, sess); err != nil {
		t.Fatalf("put: %v", err)
	}
	// The plaintext refresh_token must NOT appear in the bytea column.
	var enc []byte
	if err := h.QueryRowContext(ctx,
		`SELECT refresh_token_enc FROM desktop_sessions WHERE id = $1`, "sid-1").Scan(&enc); err != nil {
		t.Fatalf("read enc: %v", err)
	}
	if bytes.Contains(enc, []byte("super-secret-refresh")) {
		t.Error("refresh_token stored in plaintext at rest")
	}

	got, err := st.GetSession(ctx, "sid-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RefreshToken != "super-secret-refresh" || got.Sub != "sub-1" {
		t.Errorf("get = %+v", got)
	}
	// Rotate.
	got.RefreshToken = "rotated-refresh"
	if err := st.PutSession(ctx, got); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	after, _ := st.GetSession(ctx, "sid-1")
	if after.RefreshToken != "rotated-refresh" {
		t.Errorf("rotated = %q", after.RefreshToken)
	}
	// Delete then missing.
	if err := st.DeleteSession(ctx, "sid-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetSession(ctx, "sid-1"); err != ErrNotFound {
		t.Errorf("get after delete err = %v, want ErrNotFound", err)
	}
	if err := st.DeleteSession(ctx, "missing"); err != nil {
		t.Errorf("delete missing err = %v, want nil", err)
	}
}

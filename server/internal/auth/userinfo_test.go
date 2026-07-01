package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnrich_FillsMissingFromUserinfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Real Name","picture":"https://cdn/p.png"}`))
	}))
	defer srv.Close()

	e := &ProfileEnricher{userinfoURL: srv.URL, httpClient: srv.Client()}
	// display_name currently equals subject (missing) and avatar empty.
	in := Profile{Subject: "sub-1", DisplayName: "sub-1", AvatarURL: ""}
	out := e.Enrich(context.Background(), "tok-123", in)
	if out.DisplayName != "Real Name" {
		t.Errorf("DisplayName = %q, want Real Name", out.DisplayName)
	}
	if out.AvatarURL != "https://cdn/p.png" {
		t.Errorf("AvatarURL = %q, want the userinfo picture", out.AvatarURL)
	}
}

func TestEnrich_NoOpWhenComplete(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := &ProfileEnricher{userinfoURL: srv.URL, httpClient: srv.Client()}
	in := Profile{Subject: "sub-1", DisplayName: "Alice", AvatarURL: "https://cdn/a.png"}
	out := e.Enrich(context.Background(), "tok", in)
	if out != in {
		t.Errorf("complete profile should be unchanged, got %+v", out)
	}
	if called {
		t.Error("userinfo should not be called when profile is already complete")
	}
}

func TestEnrich_DegradesOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := &ProfileEnricher{userinfoURL: srv.URL, httpClient: srv.Client()}
	in := Profile{Subject: "sub-1", DisplayName: "sub-1", AvatarURL: ""}
	out := e.Enrich(context.Background(), "tok", in)
	// On failure it returns the input unchanged (does not block login).
	if out.DisplayName != "sub-1" {
		t.Errorf("on error should keep input, got %q", out.DisplayName)
	}
}

func TestEnrich_NilEnricherIsNoOp(t *testing.T) {
	var e *ProfileEnricher
	in := Profile{Subject: "s", DisplayName: "s"}
	if got := e.Enrich(context.Background(), "tok", in); got != in {
		t.Errorf("nil enricher should be a no-op, got %+v", got)
	}
}

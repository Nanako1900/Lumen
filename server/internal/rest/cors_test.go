package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCanonicalOrigin verifies the configured web base URL is normalized to a
// bare scheme://host[:port] so a trailing slash or path still matches the
// browser Origin header (which never carries either).
func TestCanonicalOrigin(t *testing.T) {
	cases := map[string]string{
		"https://example.com":      "https://example.com",
		"https://example.com/":     "https://example.com",
		"https://example.com/app/": "https://example.com",
		"https://example.com:8443": "https://example.com:8443",
		"http://127.0.0.1:5173":    "http://127.0.0.1:5173",
		"":                         "",
		"   ":                      "",
	}
	for in, want := range cases {
		if got := canonicalOrigin(in); got != want {
			t.Errorf("canonicalOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWithCORS covers decision 3: ACAO echoed only for the exact allowed origin
// (tolerating a configured trailing slash), never "*", credentials + Vary set,
// disallowed origins get no ACAO, and OPTIONS preflight returns 204.
func TestWithCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := withCORS("https://example.com/", next) // configured WITH a trailing slash

	// Allowed origin -> ACAO echoed exactly, credentials + Vary present.
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("ACAO = %q, want https://example.com", got)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("missing Access-Control-Allow-Credentials: true")
	}
	if rec.Header().Get("Vary") == "" {
		t.Error("missing Vary: Origin")
	}

	// Disallowed origin -> no ACAO at all (and never "*").
	req2 := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req2.Header.Set("Origin", "https://evil.example")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO for disallowed origin = %q, want empty", got)
	}

	// Preflight OPTIONS from an allowed origin -> 204.
	req3 := httptest.NewRequest(http.MethodOptions, "/api/me", nil)
	req3.Header.Set("Origin", "https://example.com")
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec3.Code)
	}
}

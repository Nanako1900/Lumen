package rest

import (
	"net/http"
	"net/url"
	"strings"
)

// CORS constants (decision 3). Methods/headers are kept minimal — exactly what
// the account-center SPA and desktop client send. Credentials are always allowed
// (the SPA relies on the sealed session cookie). These mirror the broker's own
// CORS constants; the single top-level middleware here is what wraps the shared
// production mux (the broker's withCORS is retained only for its standalone
// Routes()/tests).
const (
	corsAllowMethods = "GET, POST, OPTIONS"
	corsAllowHeaders = "Content-Type"
	corsMaxAge       = "600"
)

// withCORS is the single CORS middleware wrapping the whole server mux
// (decision 3). It emits Access-Control-Allow-Origin ONLY when the request
// Origin string-equals allowedOrigin exactly (scheme+host+port); otherwise it
// sends no ACAO header. It always sends Vary: Origin (so shared caches key on
// Origin and never serve an ACAO'd body to a different origin) and never uses
// "*". Credentials are allowed; the OPTIONS preflight is answered here with 204.
//
// allowedOrigin is cfg.WebBaseURL. An empty allowedOrigin disables CORS entirely
// (no ACAO ever emitted), which is the safe default.
func withCORS(allowedOrigin string, next http.Handler) http.Handler {
	// Normalize the configured origin once to a bare scheme://host[:port]
	// (no path, no trailing slash) so a LUMEN_WEB_BASE_URL written as
	// "https://example.com/" still matches the browser Origin header, which
	// never carries a path or trailing slash.
	allowed := canonicalOrigin(allowedOrigin)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always vary on Origin: the response differs per allowed origin.
		w.Header().Add("Vary", "Origin")

		origin := r.Header.Get("Origin")
		if origin != "" && allowed != "" && origin == allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", corsAllowMethods)
			w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
			w.Header().Set("Access-Control-Max-Age", corsMaxAge)
		}

		// Preflight: answer OPTIONS here (headers above are already set for an
		// allowed origin). A disallowed origin still gets a bare 204 with no
		// ACAO, which the browser treats as a CORS failure.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// canonicalOrigin normalizes a configured web base URL to a bare
// scheme://host[:port] origin (no path, no trailing slash) for exact-match
// comparison against the browser-sent Origin header. An empty value yields ""
// (CORS disabled); an unparseable value falls back to a trailing-slash-trimmed
// best effort.
func canonicalOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	return u.Scheme + "://" + u.Host
}

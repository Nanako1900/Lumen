package broker

import "net/http"

// CORS constants (decision 3). Methods/headers are kept minimal — exactly what
// the account-center SPA and desktop client send. Credentials are always
// allowed (the SPA relies on the sealed session cookie).
const (
	corsAllowMethods = "GET, POST, OPTIONS"
	corsAllowHeaders = "Content-Type"
	corsMaxAge       = "600"
)

// withCORS is the single CORS middleware (decision 3). It emits
// Access-Control-Allow-Origin ONLY when the request Origin string-equals
// cfg.WebBaseURL exactly (scheme+host+port); otherwise it sends no ACAO header.
// It always sends Vary: Origin (so caches key on Origin) and never uses "*".
// Credentials are allowed; OPTIONS preflight is answered with 204.
func (h *Handler) withCORS(next http.Handler) http.Handler {
	allowed := h.cfg.WebBaseURL
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always vary on Origin: the response differs per allowed origin, and a
		// shared cache must not serve an ACAO'd body to a different origin.
		w.Header().Add("Vary", "Origin")

		origin := r.Header.Get("Origin")
		if origin != "" && origin == allowed {
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

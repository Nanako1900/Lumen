package auth

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey string

// claimsKey stores the validated *Claims in the request context.
const claimsKey ctxKey = "lumen.claims"

// ErrorWriter writes a uniform error envelope with the given HTTP status,
// machine code and user-facing message. The rest package supplies it, keeping
// auth independent of the envelope shape.
type ErrorWriter func(w http.ResponseWriter, status int, code, message string)

// ClaimsFromContext returns the validated claims placed by RequireAuth, or nil.
func ClaimsFromContext(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsKey).(*Claims)
	return c
}

// contextWithClaims returns a child context carrying claims (also used by the
// bearer-extraction helper for WS-side reuse if needed).
func contextWithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

// BearerToken extracts the raw token from an Authorization header, returning
// ("", false) when the header is missing or malformed.
func BearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if tok == "" {
		return "", false
	}
	return tok, true
}

// RequireAuth verifies the Bearer token and injects *Claims into the request
// context. On failure it writes the appropriate error envelope
// (UNAUTHENTICATED / TOKEN_INVALID / TOKEN_EXPIRED) and stops the chain.
func RequireAuth(v *Verifier, writeErr ErrorWriter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := BearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeErr(w, http.StatusUnauthorized, "UNAUTHENTICATED", "缺少 Bearer 令牌")
			return
		}
		claims, err := v.Verify(raw)
		if err != nil {
			code := "TOKEN_INVALID"
			if IsExpired(err) {
				code = "TOKEN_EXPIRED"
			}
			writeErr(w, http.StatusUnauthorized, code, "令牌校验失败")
			return
		}
		next.ServeHTTP(w, r.WithContext(contextWithClaims(r.Context(), claims)))
	})
}

// RequireOwner is chained after RequireAuth: it enforces sub ∈ ownerSet,
// writing 403 FORBIDDEN otherwise (contract §3.1 owner endpoints).
func RequireOwner(owners *OwnerSet, writeErr ErrorWriter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil || !owners.IsOwner(claims.Subject) {
			writeErr(w, http.StatusForbidden, "FORBIDDEN", "需要 owner 权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

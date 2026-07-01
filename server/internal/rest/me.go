package rest

import (
	"context"
	"net/http"

	"lumen/internal/auth"
	"lumen/internal/protocol"
	"lumen/internal/store"
)

// upsertCurrentUser idempotently upserts the caller from validated claims and
// returns the wire DTO (contract §2.3 首登竞态修复). REST upserts do NOT
// broadcast user_updated (that is WS-only, contract §2.7). The optional
// enricher fills missing name/picture via userinfo.
func upsertCurrentUser(ctx context.Context, st store.Store, owners *auth.OwnerSet, enricher *auth.ProfileEnricher, rawToken string, claims *auth.Claims) (protocol.User, error) {
	profile := auth.ProfileFromClaims(claims)
	if enricher != nil {
		profile = enricher.Enrich(ctx, rawToken, profile)
	}
	u, _, err := st.UpsertUser(ctx, profile.Subject, profile.DisplayName, profile.AvatarURL)
	if err != nil {
		return protocol.User{}, err
	}
	return auth.ToDTO(u, owners), nil
}

// me handles GET /api/v1/me (contract §3.4 端点 2). It idempotently upserts the
// caller so a brand-new user's first REST call still returns them.
func me(st store.Store, owners *auth.OwnerSet, enricher *auth.ProfileEnricher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if claims == nil {
			writeErr(w, http.StatusUnauthorized, "UNAUTHENTICATED", "缺少身份信息")
			return
		}
		raw, _ := auth.BearerToken(r.Header.Get("Authorization"))
		dto, err := upsertCurrentUser(r.Context(), st, owners, enricher, raw, claims)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "获取资料失败")
			return
		}
		writeOK(w, http.StatusOK, dto)
	}
}

package rest

import (
	"log/slog"
	"net/http"

	"lumen/internal/auth"
	"lumen/internal/config"
	"lumen/internal/sfu"
	"lumen/internal/signaling"
	"lumen/internal/store"
)

// Deps bundles the dependencies the REST router needs (server-design §5.4).
type Deps struct {
	Verifier *auth.Verifier
	Owners   *auth.OwnerSet
	Enricher *auth.ProfileEnricher // optional userinfo enrichment
	Store    store.Store
	Rooms    *sfu.RoomManager // for bootstrap.voice_states
	Hub      signaling.Broadcaster
	Config   config.Config
	Logger   *slog.Logger
}

// NewRouter builds the REST handler tree (contract §3.3). It maps the member
// and owner endpoints, the public health check, and the static update file
// server, then wraps everything with panic recovery and access logging.
func NewRouter(d Deps) http.Handler {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()

	// public
	mux.Handle("GET /api/v1/healthz", http.HandlerFunc(health))

	// Static hosting of auto-update files ([v1], loop-6/desktop-5): public, GET
	// only. Served as https://<host>/updates/ (same FQDN, Traefik cert).
	mux.Handle("GET /updates/", http.StripPrefix("/updates/",
		http.FileServer(http.Dir(d.Config.UpdatesDir))))

	// member endpoints (RequireAuth)
	authed := func(h http.HandlerFunc) http.Handler {
		return auth.RequireAuth(d.Verifier, writeErr, h)
	}
	mux.Handle("GET /api/v1/bootstrap", authed(bootstrap(d.Store, d.Owners, d.Enricher, d.Rooms, d.Config)))
	mux.Handle("GET /api/v1/me", authed(me(d.Store, d.Owners, d.Enricher)))
	mux.Handle("GET /api/v1/channels", authed(listChannels(d.Store)))
	mux.Handle("GET /api/v1/channels/{channelId}/messages", authed(listMessages(d.Store, d.Owners)))
	mux.Handle("GET /api/v1/members", authed(listMembers(d.Store, d.Owners)))

	// owner endpoints (RequireAuth → RequireOwner) — [v1]
	owner := func(h http.HandlerFunc) http.Handler {
		return auth.RequireAuth(d.Verifier, writeErr,
			auth.RequireOwner(d.Owners, writeErr, h))
	}
	mux.Handle("POST /api/v1/channels", owner(createChannel(d.Store, d.Hub)))
	mux.Handle("PATCH /api/v1/channels/{channelId}", owner(updateChannel(d.Store, d.Hub)))
	mux.Handle("DELETE /api/v1/channels/{channelId}", owner(deleteChannel(d.Store, d.Hub)))
	mux.Handle("POST /api/v1/members/{userId}/kick", owner(kickMember(d.Store, d.Owners, d.Hub)))

	return withRecover(logger, withLogging(logger, mux))
}

package rest

import (
	"net/http"
	"time"

	"lumen/internal/auth"
	"lumen/internal/config"
	"lumen/internal/protocol"
	"lumen/internal/sfu"
	"lumen/internal/store"
)

// bootstrapResp is the bootstrap payload (contract §3.4 端点 1).
type bootstrapResp struct {
	Me          protocol.User         `json:"me"`
	Channels    []protocol.Channel    `json:"channels"`
	Members     []protocol.User       `json:"members"`
	VoiceStates []protocol.VoiceState `json:"voice_states"`
	WSURL       string                `json:"ws_url"`
	ServerTime  string                `json:"server_time"`
}

// bootstrap handles GET /api/v1/bootstrap (contract §3.4 端点 1): one-shot
// first-screen data. It idempotently upserts the caller (first-login race fix,
// contract §2.3) without broadcasting user_updated.
func bootstrap(st store.Store, owners *auth.OwnerSet, enricher *auth.ProfileEnricher, rooms *sfu.RoomManager, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if claims == nil {
			writeErr(w, http.StatusUnauthorized, "UNAUTHENTICATED", "缺少身份信息")
			return
		}
		raw, _ := auth.BearerToken(r.Header.Get("Authorization"))
		me, err := upsertCurrentUser(r.Context(), st, owners, enricher, raw, claims)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "引导数据加载失败")
			return
		}

		channels, err := st.ListChannels(r.Context(), "")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "引导数据加载失败")
			return
		}
		channelDTOs := make([]protocol.Channel, 0, len(channels))
		for _, c := range channels {
			channelDTOs = append(channelDTOs, channelToDTO(c))
		}

		users, err := st.ListUsers(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "引导数据加载失败")
			return
		}
		memberDTOs := make([]protocol.User, 0, len(users))
		for _, u := range users {
			memberDTOs = append(memberDTOs, auth.ToDTO(u, owners))
		}

		writeOK(w, http.StatusOK, bootstrapResp{
			Me:          me,
			Channels:    channelDTOs,
			Members:     memberDTOs,
			VoiceStates: rooms.Snapshot(),
			WSURL:       wsURL(cfg, r),
			ServerTime:  time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// wsURL derives the client-facing WS URL: the configured LUMEN_PUBLIC_WS_URL,
// or wss://<Host>/ws from the request Host header (Traefik terminates TLS, so
// the external scheme is wss). (server-design §5.4)
func wsURL(cfg config.Config, r *http.Request) string {
	if cfg.PublicWSURL != "" {
		return cfg.PublicWSURL
	}
	host := r.Host
	if host == "" {
		return ""
	}
	return "wss://" + host + "/ws"
}

package rest

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"lumen/internal/auth"
	"lumen/internal/protocol"
	"lumen/internal/store"
)

// messagesResp is the paginated messages payload (contract §3.4 端点 4).
type messagesResp struct {
	Messages []protocol.Message `json:"messages"`
	Meta     messagesMeta       `json:"meta"`
}

// messagesMeta is the pagination metadata.
type messagesMeta struct {
	Limit      int     `json:"limit"`
	HasMore    bool    `json:"has_more"`
	NextBefore *string `json:"next_before"`
}

// listMessages handles GET /api/v1/channels/{channelId}/messages (contract
// §3.4 端点 4). The channel must be text; cursor pagination goes backwards via
// before. Author snapshots are inlined for direct rendering.
func listMessages(st store.Store, owners *auth.OwnerSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channelID := r.PathValue("channelId")

		ch, err := st.GetChannel(r.Context(), channelID)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "频道不存在")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "获取消息失败")
			return
		}
		if ch.Type != "text" {
			writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", "该频道非文字频道")
			return
		}

		limit := parseLimit(r.URL.Query().Get("limit"))
		before := r.URL.Query().Get("before")

		msgs, hasMore, err := st.ListMessages(r.Context(), channelID, before, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "获取消息失败")
			return
		}

		// Inline author snapshots, resolved once per distinct author.
		authorCache := map[string]*protocol.User{}
		out := make([]protocol.Message, 0, len(msgs))
		for _, m := range msgs {
			dto := protocol.Message{
				ID:        m.ID,
				ChannelID: m.ChannelID,
				AuthorID:  m.AuthorID,
				Content:   m.Content,
				CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
			}
			dto.Author = resolveAuthor(r, st, owners, authorCache, m.AuthorID)
			out = append(out, dto)
		}

		var nextBefore *string
		if len(out) > 0 {
			nb := out[0].ID // earliest in the ascending page
			nextBefore = &nb
		}
		writeOK(w, http.StatusOK, messagesResp{
			Messages: out,
			Meta: messagesMeta{
				Limit:      limit,
				HasMore:    hasMore,
				NextBefore: nextBefore,
			},
		})
	}
}

// resolveAuthor fetches (and caches) an author DTO, returning nil on lookup
// failure so the message still renders (author is a best-effort snapshot).
func resolveAuthor(r *http.Request, st store.Store, owners *auth.OwnerSet, cache map[string]*protocol.User, authorID string) *protocol.User {
	if cached, ok := cache[authorID]; ok {
		return cached
	}
	u, err := st.GetUserByID(r.Context(), authorID)
	if err != nil {
		cache[authorID] = nil
		return nil
	}
	dto := auth.ToDTO(u, owners)
	cache[authorID] = &dto
	return &dto
}

// parseLimit parses and clamps the limit query to 1..100, defaulting to 50.
func parseLimit(raw string) int {
	if raw == "" {
		return store.DefaultMessageLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return store.DefaultMessageLimit
	}
	if n < 1 {
		return 1
	}
	if n > store.MaxMessageLimit {
		return store.MaxMessageLimit
	}
	return n
}

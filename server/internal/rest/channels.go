package rest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"lumen/internal/protocol"
	"lumen/internal/signaling"
	"lumen/internal/store"
)

// Channel name bounds (contract §3.4 端点 6).
const (
	minChannelName = 1
	maxChannelName = 64
)

// channelToDTO converts a store.Channel to a wire protocol.Channel.
func channelToDTO(c store.Channel) protocol.Channel {
	return protocol.Channel{
		ID:        c.ID,
		Name:      c.Name,
		Type:      c.Type,
		Position:  c.Position,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: c.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// listChannels handles GET /api/v1/channels (contract §3.4 端点 3). The
// optional type query filters text|voice.
func listChannels(st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		typeFilter := strings.TrimSpace(r.URL.Query().Get("type"))
		if typeFilter != "" && typeFilter != "text" && typeFilter != "voice" {
			writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", "type 需为 text 或 voice")
			return
		}
		channels, err := st.ListChannels(r.Context(), typeFilter)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "获取频道失败")
			return
		}
		out := make([]protocol.Channel, 0, len(channels))
		for _, c := range channels {
			out = append(out, channelToDTO(c))
		}
		writeOK(w, http.StatusOK, out)
	}
}

// createChannelReq is the POST body (contract §3.4 端点 6).
type createChannelReq struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Position *int   `json:"position"`
}

// createChannel handles POST /api/v1/channels ([v1] owner). It broadcasts the
// full new Channel via channel_created so clients reorder by (position, id).
func createChannel(st store.Store, hub signaling.Broadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createChannelReq
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", "请求体无效")
			return
		}
		name := strings.TrimSpace(req.Name)
		if n := utf8.RuneCountInString(name); n < minChannelName || n > maxChannelName {
			writeErrDetails(w, http.StatusBadRequest, "VALIDATION_ERROR", "频道名长度需 1~64",
				[]fieldError{{Field: "name", Reason: "长度需 1~64"}})
			return
		}
		if req.Type != "text" && req.Type != "voice" {
			writeErrDetails(w, http.StatusBadRequest, "VALIDATION_ERROR", "type 需为 text 或 voice",
				[]fieldError{{Field: "type", Reason: "需为 text 或 voice"}})
			return
		}

		ch, err := st.CreateChannel(r.Context(), name, req.Type, req.Position)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "创建频道失败")
			return
		}
		dto := channelToDTO(ch)
		hub.BroadcastAll(protocol.NewEnvelope("channel_created", dto))
		writeOK(w, http.StatusOK, dto)
	}
}

// updateChannelReq is the PATCH body (contract §3.4 端点 7).
type updateChannelReq struct {
	Name     *string `json:"name"`
	Position *int    `json:"position"`
}

// updateChannel handles PATCH /api/v1/channels/{channelId} ([v1] owner).
func updateChannel(st store.Store, hub signaling.Broadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("channelId")
		var req updateChannelReq
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", "请求体无效")
			return
		}
		if req.Name == nil && req.Position == nil {
			writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", "至少提供一个字段")
			return
		}
		if req.Name != nil {
			name := strings.TrimSpace(*req.Name)
			if n := utf8.RuneCountInString(name); n < minChannelName || n > maxChannelName {
				writeErrDetails(w, http.StatusBadRequest, "VALIDATION_ERROR", "频道名长度需 1~64",
					[]fieldError{{Field: "name", Reason: "长度需 1~64"}})
				return
			}
			req.Name = &name
		}

		ch, err := st.UpdateChannel(r.Context(), id, req.Name, req.Position)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "频道不存在")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "更新频道失败")
			return
		}
		dto := channelToDTO(ch)
		hub.BroadcastAll(protocol.NewEnvelope("channel_updated", dto))
		writeOK(w, http.StatusOK, dto)
	}
}

// deleteChannel handles DELETE /api/v1/channels/{channelId} ([v1] owner). For a
// voice channel it closes the room (broadcasting user_left) before deleting;
// then broadcasts channel_deleted (contract §3.4 端点 8).
func deleteChannel(st store.Store, hub signaling.Broadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("channelId")

		ch, err := st.GetChannel(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "频道不存在")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "删除频道失败")
			return
		}

		if ch.Type == "voice" {
			hub.CloseVoiceChannel(id) // closes room + broadcasts user_left
		}
		if err := st.DeleteChannel(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "NOT_FOUND", "频道不存在")
				return
			}
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "删除频道失败")
			return
		}
		hub.BroadcastAll(protocol.NewEnvelope("channel_deleted",
			protocol.ChannelDeletedData{ChannelID: id}))
		writeOK(w, http.StatusOK, nil)
	}
}

// fieldError is a field-level validation detail (contract §7.2).
type fieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// decodeJSON decodes a request body, rejecting unknown fields is intentionally
// not enforced (clients must tolerate extra fields, contract §7.1). An empty
// body decodes to the zero value.
func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		// An empty body (io.EOF) decodes to the zero value, not an error.
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

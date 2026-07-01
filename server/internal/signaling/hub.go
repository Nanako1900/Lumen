// Package signaling is the WebSocket hub (contract §4, server-design §3). It
// owns connection lifecycle, the auth/reauth handshake, message routing, and
// broadcasting. It holds the SFU RoomManager and implements the SFU
// RoomEventSink so voice events become broadcasts, and exposes a narrow
// Broadcaster interface for the rest layer.
package signaling

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"lumen/internal/auth"
	"lumen/internal/protocol"
	"lumen/internal/sfu"
	"lumen/internal/store"
)

// Broadcaster is the narrow interface the rest layer depends on (server-design
// §1.3). It lets REST owner endpoints trigger WS side effects without importing
// the whole hub.
type Broadcaster interface {
	// BroadcastAll sends a message to every authenticated connection.
	BroadcastAll(msg protocol.Envelope)
	// DisconnectUser drops all of a user's connections (kick): each active
	// connection is sent auth_error{code: reasonCode, ...} before closing, then
	// the user is removed from all voice rooms.
	DisconnectUser(userID string, reasonCode string)
	// CloseVoiceChannel closes a voice room (delete channel) and broadcasts
	// user_left for each member.
	CloseVoiceChannel(channelID string)
}

// Hub holds all authenticated connections and provides broadcast / targeted
// disconnect. Concurrency-safe. The mutex guards only the registry maps; no
// network IO happens while holding it.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
	byUser  map[string][]*Client

	rooms    *sfu.RoomManager
	store    store.Store
	verifier *auth.Verifier
	owners   *auth.OwnerSet
	enricher *auth.ProfileEnricher
	logger   *slog.Logger
}

// NewHub constructs a Hub with injected dependencies. enricher and logger may
// be nil (a nil logger is replaced with a discard logger).
func NewHub(st store.Store, v *auth.Verifier, owners *auth.OwnerSet, rooms *sfu.RoomManager, enricher *auth.ProfileEnricher, logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Hub{
		clients:  make(map[*Client]struct{}),
		byUser:   make(map[string][]*Client),
		rooms:    rooms,
		store:    st,
		verifier: v,
		owners:   owners,
		enricher: enricher,
		logger:   logger,
	}
}

// register adds an authenticated client to the registry.
func (h *Hub) register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
	h.byUser[c.userID] = append(h.byUser[c.userID], c)
}

// unregister removes a client from the registry. Returns whether the user has
// any remaining connections afterwards.
func (h *Hub) unregister(c *Client) (userHasOthers bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; !ok {
		return h.userConnCountLocked(c.userID) > 0
	}
	delete(h.clients, c)
	conns := h.byUser[c.userID]
	kept := conns[:0]
	for _, existing := range conns {
		if existing != c {
			kept = append(kept, existing)
		}
	}
	if len(kept) == 0 {
		delete(h.byUser, c.userID)
	} else {
		h.byUser[c.userID] = kept
	}
	return len(kept) > 0
}

// userConnCountLocked returns the number of connections for a user. Caller
// holds h.mu.
func (h *Hub) userConnCountLocked(userID string) int {
	return len(h.byUser[userID])
}

// UserConnCount returns the number of live connections for a user.
func (h *Hub) UserConnCount(userID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.userConnCountLocked(userID)
}

// BroadcastAll sends msg to every authenticated connection (contract: text
// messages, channel_*, user_updated). It snapshots targets under the lock, then
// enqueues outside it.
func (h *Hub) BroadcastAll(msg protocol.Envelope) {
	h.mu.RLock()
	targets := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		targets = append(targets, c)
	}
	h.mu.RUnlock()
	for _, c := range targets {
		c.enqueue(msg)
	}
}

// BroadcastToChannel sends msg to the members of a voice channel, optionally
// excluding one user (for "broadcast to others").
func (h *Hub) BroadcastToChannel(channelID string, msg protocol.Envelope, excludeUserID string) {
	memberIDs := h.rooms.MemberIDs(channelID)
	if len(memberIDs) == 0 {
		return
	}
	memberSet := make(map[string]struct{}, len(memberIDs))
	for _, id := range memberIDs {
		if id != excludeUserID {
			memberSet[id] = struct{}{}
		}
	}
	h.mu.RLock()
	targets := make([]*Client, 0)
	for id := range memberSet {
		targets = append(targets, h.byUser[id]...)
	}
	h.mu.RUnlock()
	for _, c := range targets {
		c.enqueue(msg)
	}
}

// SendToUser sends msg to all of a user's connections.
func (h *Hub) SendToUser(userID string, msg protocol.Envelope) {
	h.mu.RLock()
	targets := append([]*Client(nil), h.byUser[userID]...)
	h.mu.RUnlock()
	for _, c := range targets {
		c.enqueue(msg)
	}
}

// DisconnectUser drops all of a user's connections (kick, kick-1/kick-4). Each
// active connection is sent auth_error{code: reasonCode, ...} synchronously
// before closing, so the client learns the real reason; then the user is
// removed from all voice rooms and user_left is broadcast.
func (h *Hub) DisconnectUser(userID string, reasonCode string) {
	h.mu.RLock()
	conns := append([]*Client(nil), h.byUser[userID]...)
	h.mu.RUnlock()

	errMsg := h.kickedAuthError(userID, reasonCode)
	for _, c := range conns {
		c.sendNow(errMsg)
		c.closeConn()
	}
	// Remove from all rooms (user granularity) and broadcast user_left.
	for _, channelID := range h.rooms.LeaveAll(userID) {
		h.UserLeft(channelID, userID)
	}
}

// CloseVoiceChannel closes a voice room (delete channel) and broadcasts
// user_left for each member.
func (h *Hub) CloseVoiceChannel(channelID string) {
	for _, userID := range h.rooms.CloseRoom(channelID) {
		h.BroadcastToChannel(channelID, protocol.NewEnvelope("user_left",
			protocol.UserLeftData{ChannelID: channelID, UserID: userID}), "")
		// Also notify the departed member directly (their conn may still be up).
		h.SendToUser(userID, protocol.NewEnvelope("user_left",
			protocol.UserLeftData{ChannelID: channelID, UserID: userID}))
	}
}

// kickedAuthError builds the auth_error payload for a disconnect. For
// reasonCode == "KICKED" with a future kicked_until it includes
// kicked_until/retry_after; otherwise those are omitted (contract §4.3).
func (h *Hub) kickedAuthError(userID, reasonCode string) protocol.Envelope {
	data := protocol.AuthErrorData{Code: reasonCode, Message: "你已被移出服务器"}
	if reasonCode == "KICKED" {
		if u, err := h.store.GetUserByID(context.Background(), userID); err == nil &&
			u.KickedUntil != nil && u.KickedUntil.After(time.Now()) {
			until := u.KickedUntil.UTC()
			retry := int(time.Until(until).Seconds())
			data.KickedUntil = until.Format(time.RFC3339)
			data.RetryAfter = &retry
		}
	}
	return protocol.NewEnvelope("auth_error", data)
}

// CloseAll closes every connection (graceful shutdown).
func (h *Hub) CloseAll() {
	h.mu.RLock()
	conns := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		c.closeConn()
	}
}

// --- sfu.RoomEventSink implementation (server-design §4.6) ---

// UserJoined broadcasts user_joined to the other members of a channel. The User
// snapshot is assembled here via store lookup + ToDTO (loop-4 / voice-5).
func (h *Hub) UserJoined(channelID string, vs protocol.VoiceState) {
	userDTO, ok := h.userDTO(vs.UserID)
	if !ok {
		return
	}
	msg := protocol.NewEnvelope("user_joined", protocol.UserJoinedData{
		ChannelID:  channelID,
		VoiceState: vs,
		User:       userDTO,
	})
	h.BroadcastToChannel(channelID, msg, vs.UserID)
}

// UserLeft broadcasts user_left to the members of a channel.
func (h *Hub) UserLeft(channelID, userID string) {
	msg := protocol.NewEnvelope("user_left",
		protocol.UserLeftData{ChannelID: channelID, UserID: userID})
	h.BroadcastToChannel(channelID, msg, "")
}

// memberVoiceState returns a member's current VoiceState from the room manager.
func (h *Hub) memberVoiceState(channelID, userID string) protocol.VoiceState {
	return h.rooms.MemberVoiceState(channelID, userID)
}

// userDTO fetches a user and converts to a wire DTO with is_owner injected.
func (h *Hub) userDTO(userID string) (protocol.User, bool) {
	return h.userDTOCtx(context.Background(), userID)
}

// userDTOCtx is userDTO with an explicit context.
func (h *Hub) userDTOCtx(ctx context.Context, userID string) (protocol.User, bool) {
	u, err := h.store.GetUserByID(ctx, userID)
	if err != nil {
		return protocol.User{}, false
	}
	return auth.ToDTO(u, h.owners), true
}

// discardWriter is an io.Writer that drops everything (nil-logger fallback).
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

package sfu

import (
	"sync"

	"github.com/pion/webrtc/v4"

	"lumen/internal/protocol"
)

// RoomManager owns all voice rooms, keyed by channel_id (server-design §4.3).
// Rooms are created lazily on first join and destroyed when empty.
type RoomManager struct {
	mu    sync.Mutex
	api   *webrtc.API
	rooms map[string]*Room
	sink  RoomEventSink
}

// NewRoomManager constructs a manager over the shared *webrtc.API. The sink is
// injected later via SetSink (the signaling Hub implements it).
func NewRoomManager(api *webrtc.API) *RoomManager {
	return &RoomManager{
		api:   api,
		rooms: make(map[string]*Room),
	}
}

// SetSink injects the room-event sink (signaling Hub) after construction,
// breaking the sfu↔signaling import cycle.
func (m *RoomManager) SetSink(sink RoomEventSink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sink = sink
	for _, r := range m.rooms {
		r.onEvent = sink
	}
}

// getOrCreate returns the room for channelID, creating it if absent.
func (m *RoomManager) getOrCreate(channelID string) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rooms[channelID]
	if !ok {
		r = newRoom(channelID, m.api, m.sink)
		m.rooms[channelID] = r
	}
	return r
}

// get returns the room for channelID, or (nil, false).
func (m *RoomManager) get(channelID string) (*Room, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rooms[channelID]
	return r, ok
}

// dropIfEmpty removes an empty room from the manager.
func (m *RoomManager) dropIfEmpty(channelID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rooms[channelID]; ok && r.isEmpty() {
		delete(m.rooms, channelID)
	}
}

// Join adds a user to a voice channel and returns their VoiceState plus the
// user IDs of the OTHER existing members (for user_joined replay). activeClient
// identifies the owning connection; send delivers offers/ICE to it.
//
// If a residual member for the same userID exists (reconnect / endpoint switch,
// voice-3 / loop-2), it is removed first (its PC closed) with no user_left, then
// the new connection takes over. Signalling to build offers happens after the
// lock is released.
func (m *RoomManager) Join(channelID, userID string, activeClient any, send func(protocol.Envelope)) (protocol.VoiceState, []string, error) {
	room := m.getOrCreate(channelID)

	room.mu.Lock()
	if _, exists := room.members[userID]; exists {
		room.removeMember(userID) // close stale PC, drop its tracks
	}
	// Collect existing OTHER members for replay before adding the newcomer.
	others := make([]string, 0, len(room.members))
	for id := range room.members {
		if id != userID {
			others = append(others, id)
		}
	}
	mem, err := room.addMember(userID, activeClient, send)
	if err != nil {
		room.mu.Unlock()
		m.dropIfEmpty(channelID)
		return protocol.VoiceState{}, nil, err
	}
	vs := mem.state
	room.mu.Unlock()

	room.signalPeerConnections()
	return vs, others, nil
}

// ActiveClientOf returns the owning connection handle for a user in a channel,
// or (nil, false). Used by signaling to decide multi-endpoint cleanup.
func (m *RoomManager) ActiveClientOf(channelID, userID string) (any, bool) {
	room, ok := m.get(channelID)
	if !ok {
		return nil, false
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	mem, ok := room.members[userID]
	if !ok {
		return nil, false
	}
	return mem.activeClient, true
}

// Leave removes a user from a channel and renegotiates the remaining peers.
// Returns true if the user was present and removed. The caller is responsible
// for broadcasting user_left (only when the user's last connection left).
func (m *RoomManager) Leave(channelID, userID string) bool {
	room, ok := m.get(channelID)
	if !ok {
		return false
	}
	room.mu.Lock()
	_, present := room.members[userID]
	if present {
		room.removeMember(userID)
	}
	room.mu.Unlock()

	if present {
		room.signalPeerConnections()
		m.dropIfEmpty(channelID)
	}
	return present
}

// LeaveAll removes a user from every room they are in and returns the channel
// IDs from which they were removed (for user_left broadcast). Used on kick /
// disconnect (user granularity).
func (m *RoomManager) LeaveAll(userID string) []string {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()

	var left []string
	for _, room := range rooms {
		room.mu.Lock()
		_, present := room.members[userID]
		if present {
			room.removeMember(userID)
		}
		room.mu.Unlock()
		if present {
			room.signalPeerConnections()
			left = append(left, room.channelID)
			m.dropIfEmpty(room.channelID)
		}
	}
	return left
}

// FindUserChannel returns the channel a user is currently in, or ("", false).
func (m *RoomManager) FindUserChannel(userID string) (string, bool) {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()

	for _, room := range rooms {
		room.mu.Lock()
		_, present := room.members[userID]
		room.mu.Unlock()
		if present {
			return room.channelID, true
		}
	}
	return "", false
}

// HandleAnswer routes an SDP answer to the user's PeerConnection.
func (m *RoomManager) HandleAnswer(channelID, userID string, sdp webrtc.SessionDescription) error {
	room, ok := m.get(channelID)
	if !ok {
		return ErrRoomNotFound
	}
	return room.handleAnswer(userID, sdp)
}

// HandleICE routes an ICE candidate to the user's PeerConnection.
func (m *RoomManager) HandleICE(channelID, userID string, cand webrtc.ICECandidateInit) error {
	room, ok := m.get(channelID)
	if !ok {
		return ErrRoomNotFound
	}
	return room.handleICE(userID, cand)
}

// SetSpeaking updates a member's speaking state; returns the state and whether
// the member exists.
func (m *RoomManager) SetSpeaking(channelID, userID string, speaking bool) (protocol.VoiceState, bool) {
	room, ok := m.get(channelID)
	if !ok {
		return protocol.VoiceState{}, false
	}
	return room.setSpeaking(userID, speaking)
}

// SetMute updates a member's mute/deafen state; returns the state and whether
// the member exists.
func (m *RoomManager) SetMute(channelID, userID string, muted, deafened bool) (protocol.VoiceState, bool) {
	room, ok := m.get(channelID)
	if !ok {
		return protocol.VoiceState{}, false
	}
	return room.setMute(userID, muted, deafened)
}

// MemberVoiceState returns the current VoiceState of a member, or a zero-valued
// state (with channel/user set) when absent.
func (m *RoomManager) MemberVoiceState(channelID, userID string) protocol.VoiceState {
	room, ok := m.get(channelID)
	if !ok {
		return protocol.VoiceState{ChannelID: channelID, UserID: userID}
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	if mem, ok := room.members[userID]; ok {
		return mem.state
	}
	return protocol.VoiceState{ChannelID: channelID, UserID: userID}
}

// Snapshot returns the VoiceStates of all members across all rooms (for
// bootstrap.voice_states).
func (m *RoomManager) Snapshot() []protocol.VoiceState {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()

	out := make([]protocol.VoiceState, 0)
	for _, room := range rooms {
		out = append(out, room.snapshot()...)
	}
	return out
}

// MemberIDs returns the user IDs currently in a channel (for delete-channel
// user_left broadcasts).
func (m *RoomManager) MemberIDs(channelID string) []string {
	room, ok := m.get(channelID)
	if !ok {
		return nil
	}
	return room.memberIDs()
}

// CloseRoom closes a voice room (delete channel): closes every PC and removes
// the room. Returns the user IDs that were present (for user_left broadcasts).
func (m *RoomManager) CloseRoom(channelID string) []string {
	room, ok := m.get(channelID)
	if !ok {
		return nil
	}
	room.mu.Lock()
	ids := make([]string, 0, len(room.members))
	for id := range room.members {
		ids = append(ids, id)
	}
	room.closeAll()
	room.mu.Unlock()

	m.mu.Lock()
	delete(m.rooms, channelID)
	m.mu.Unlock()
	return ids
}

// CloseAllRooms closes every room's PeerConnections (graceful shutdown).
func (m *RoomManager) CloseAllRooms() {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.rooms = make(map[string]*Room)
	m.mu.Unlock()

	for _, room := range rooms {
		room.mu.Lock()
		room.closeAll()
		room.mu.Unlock()
	}
}

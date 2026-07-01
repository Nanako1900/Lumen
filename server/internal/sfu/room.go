package sfu

import (
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"lumen/internal/protocol"
)

// rtpReadBuffer is the reusable read buffer size for the RTP forward loop.
const rtpReadBuffer = 1500

// signalMaxAttempts / signalRetryDelay bound the renegotiation retry loop
// (contract §4.6, research 01 §3.3).
const (
	signalMaxAttempts = 25
	signalRetryDelay  = 3 * time.Second
)

// RoomEventSink is implemented by the signaling layer and injected into the
// SFU. The SFU calls it on room events; signaling turns them into broadcasts.
// The User snapshot for user_joined is assembled by signaling (store lookup +
// ToDTO), so only VoiceState is passed here (loop-4 / voice-5).
type RoomEventSink interface {
	UserJoined(channelID string, vs protocol.VoiceState)
	UserLeft(channelID, userID string)
}

// member holds one member's full runtime state inside a Room. activeClient is
// an opaque identity handle (the owning signaling connection) used to detect
// multi-endpoint switches; send delivers offers/ICE to that connection.
type member struct {
	userID       string
	activeClient any
	pc           *webrtc.PeerConnection
	state        protocol.VoiceState
	send         func(env protocol.Envelope)
}

// Room is the in-memory room for a single voice channel. One mutex guards all
// mutable state (research 01 §3.3). trackLocals is keyed by track ID; the
// forwarding track's StreamID carries the uploader user_id (voice-1).
type Room struct {
	channelID   string
	mu          sync.Mutex
	members     map[string]*member
	trackLocals map[string]*webrtc.TrackLocalStaticRTP
	api         *webrtc.API
	onEvent     RoomEventSink
}

// newRoom constructs an empty Room for a channel.
func newRoom(channelID string, api *webrtc.API, sink RoomEventSink) *Room {
	return &Room{
		channelID:   channelID,
		members:     make(map[string]*member),
		trackLocals: make(map[string]*webrtc.TrackLocalStaticRTP),
		api:         api,
		onEvent:     sink,
	}
}

// addTrack creates a forwarding TrackLocalStaticRTP for a remote track, storing
// it under the source track ID and setting the StreamID to the uploader
// user_id so the downstream msid carries the source user_id (voice-1). It then
// triggers renegotiation so other peers subscribe.
func (r *Room) addTrack(remote *webrtc.TrackRemote, uploaderUserID string) *webrtc.TrackLocalStaticRTP {
	r.mu.Lock()
	local, err := webrtc.NewTrackLocalStaticRTP(
		remote.Codec().RTPCodecCapability, remote.ID(), uploaderUserID)
	if err != nil {
		r.mu.Unlock()
		return nil
	}
	r.trackLocals[remote.ID()] = local
	r.mu.Unlock()

	r.signalPeerConnections()
	return local
}

// removeTrack deletes a forwarding track and renegotiates.
func (r *Room) removeTrack(t *webrtc.TrackLocalStaticRTP) {
	if t == nil {
		return
	}
	r.mu.Lock()
	delete(r.trackLocals, t.ID())
	r.mu.Unlock()

	r.signalPeerConnections()
}

// forwardRTP reads a remote track and writes each packet to its forwarding
// local track until the remote closes (research 01 §3.2). Extension bits are
// cleared, matching the sfu-ws forwarding approach.
func (r *Room) forwardRTP(remote *webrtc.TrackRemote, local *webrtc.TrackLocalStaticRTP) {
	buf := make([]byte, rtpReadBuffer)
	pkt := &rtp.Packet{}
	for {
		n, _, err := remote.Read(buf)
		if err != nil {
			return
		}
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			return
		}
		pkt.Extension = false
		pkt.Extensions = nil
		if err := local.WriteRTP(pkt); err != nil {
			return
		}
	}
}

// snapshot returns the VoiceStates of all current members (for bootstrap).
func (r *Room) snapshot() []protocol.VoiceState {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]protocol.VoiceState, 0, len(r.members))
	for _, m := range r.members {
		out = append(out, m.state)
	}
	return out
}

// memberIDs returns the user IDs currently in the room.
func (r *Room) memberIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.members))
	for id := range r.members {
		out = append(out, id)
	}
	return out
}

// isEmpty reports whether the room has no members.
func (r *Room) isEmpty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.members) == 0
}

// setSpeaking updates a member's speaking flag; returns the member's voice
// state and whether the member exists.
func (r *Room) setSpeaking(userID string, speaking bool) (protocol.VoiceState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.members[userID]
	if !ok {
		return protocol.VoiceState{}, false
	}
	m.state.Speaking = speaking
	return m.state, true
}

// setMute updates a member's mute/deafen flags; returns the state and whether
// the member exists.
func (r *Room) setMute(userID string, muted, deafened bool) (protocol.VoiceState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.members[userID]
	if !ok {
		return protocol.VoiceState{}, false
	}
	m.state.Muted = muted
	m.state.Deafened = deafened
	return m.state, true
}

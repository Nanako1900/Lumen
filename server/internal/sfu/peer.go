package sfu

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"

	"lumen/internal/protocol"
)

// renegoCounter provides monotonically increasing renegotiation offer IDs.
var renegoCounter atomic.Uint64

// nextRenegoID returns a fresh offer correlation id (e.g. "s-renego-12").
func nextRenegoID() string {
	n := renegoCounter.Add(1)
	return "s-renego-" + itoa(n)
}

// itoa is a tiny uint64→string helper (avoids strconv import churn).
func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// addMember creates a PeerConnection for a joining user and registers the
// member. It must be called with r.mu held. If a residual member for the same
// userID exists (reconnect / endpoint switch), the caller removes it first
// (voice-3 / loop-2). The PC lifecycle callbacks and forward loop are wired
// here; signalPeerConnections is triggered by the caller after unlocking.
func (r *Room) addMember(userID string, activeClient any, send func(protocol.Envelope)) (*member, error) {
	pc, err := r.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	if _, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv}); err != nil {
		_ = pc.Close()
		return nil, err
	}

	// Trickle ICE: forward local candidates to the client (contract §4.6).
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		payload := protocol.ICEPayload{ChannelID: r.channelID}
		if cand != nil {
			init := cand.ToJSON()
			payload.Candidate = &protocol.ICECandidate{
				Candidate:     init.Candidate,
				SDPMid:        deref(init.SDPMid),
				SDPMLineIndex: derefU16(init.SDPMLineIndex),
				UsernameFrag:  deref(init.UsernameFragment),
			}
		} // cand == nil → end-of-candidates, Candidate stays nil
		send(protocol.NewEnvelope("ice_candidate", payload))
	})

	// Lifecycle cleanup (research 01 §5.1).
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		switch s {
		case webrtc.PeerConnectionStateFailed:
			_ = pc.Close()
		case webrtc.PeerConnectionStateClosed:
			r.signalPeerConnections() // let other peers drop the departed tracks
		}
	})

	// Receive uplink → build a forwarding local track → forward RTP.
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		local := r.addTrack(remote, userID)
		if local == nil {
			return
		}
		defer r.removeTrack(local)
		r.forwardRTP(remote, local)
	})

	m := &member{
		userID:       userID,
		activeClient: activeClient,
		pc:           pc,
		state:        protocol.VoiceState{ChannelID: r.channelID, UserID: userID},
		send:         send,
	}
	r.members[userID] = m
	return m, nil
}

// removeMember closes a member's PeerConnection, deletes its forwarding tracks,
// removes it from the room, and (via signalPeerConnections) makes other peers
// drop its tracks. Must be called with r.mu held. It does NOT emit user_left;
// callers decide that based on multi-endpoint rules.
func (r *Room) removeMember(userID string) {
	m, ok := r.members[userID]
	if !ok {
		return
	}
	delete(r.members, userID)
	if m.pc != nil {
		_ = m.pc.Close() // idempotent
	}
	// Drop forwarding tracks whose StreamID (== uploader user_id) is this user.
	for id, local := range r.trackLocals {
		if local.StreamID() == userID {
			delete(r.trackLocals, id)
		}
	}
}

// signalPeerConnections synchronises every member's senders with trackLocals
// and renegotiates (server is offerer). It follows the sfu-ws pattern: clean
// closed peers, remove stale senders, add missing tracks, create+send an offer.
// One mutex guards the whole pass; up to signalMaxAttempts retries, then it
// unlocks and reschedules after signalRetryDelay to avoid deadlock (contract
// §4.6, research 01 §3.3). Members whose PC closed are removed and reported via
// the sink (user_left).
func (r *Room) signalPeerConnections() {
	r.mu.Lock()
	defer r.mu.Unlock()

	attemptSync := func() (tryAgain bool) {
		// 1) Remove closed peers and notify.
		for id, m := range r.members {
			if m.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
				delete(r.members, id)
				for tid, local := range r.trackLocals {
					if local.StreamID() == id {
						delete(r.trackLocals, tid)
					}
				}
				if r.onEvent != nil {
					r.onEvent.UserLeft(r.channelID, id)
				}
			}
		}

		for _, m := range r.members {
			existing := map[string]bool{}
			// 2) Remove senders whose track is no longer in the room.
			for _, sender := range m.pc.GetSenders() {
				if sender.Track() == nil {
					continue
				}
				tid := sender.Track().ID()
				if _, ok := r.trackLocals[tid]; !ok {
					if err := m.pc.RemoveTrack(sender); err != nil {
						return true
					}
				} else {
					existing[tid] = true
				}
			}
			// Never echo a peer's own uplink back to itself.
			for _, recv := range m.pc.GetReceivers() {
				if recv.Track() != nil {
					existing[recv.Track().ID()] = true
				}
			}
			// 3) Add missing tracks.
			for tid, local := range r.trackLocals {
				if existing[tid] {
					continue
				}
				if _, err := m.pc.AddTrack(local); err != nil {
					return true
				}
			}
			// 4) Renegotiate: create and send an offer (contract webrtc_offer).
			offer, err := m.pc.CreateOffer(nil)
			if err != nil {
				return true
			}
			if err := m.pc.SetLocalDescription(offer); err != nil {
				return true
			}
			m.send(protocol.NewEnvelopeWithID("webrtc_offer",
				protocol.WebRTCSDP{
					ChannelID: r.channelID,
					SDP:       protocol.SessionDescription{Type: "offer", SDP: offer.SDP},
				}, nextRenegoID()))
		}
		return false
	}

	for attempt := 0; ; attempt++ {
		if attempt == signalMaxAttempts {
			go func() {
				time.Sleep(signalRetryDelay)
				r.signalPeerConnections()
			}()
			return
		}
		if !attemptSync() {
			return
		}
	}
}

// handleAnswer applies a client's SDP answer to its PeerConnection.
func (r *Room) handleAnswer(userID string, sdp webrtc.SessionDescription) error {
	r.mu.Lock()
	m, ok := r.members[userID]
	r.mu.Unlock()
	if !ok {
		return errors.New("成员不在房间")
	}
	return m.pc.SetRemoteDescription(sdp)
}

// handleICE adds a client's ICE candidate. A nil/end-of-candidates payload is
// tolerated (skipped).
func (r *Room) handleICE(userID string, cand webrtc.ICECandidateInit) error {
	r.mu.Lock()
	m, ok := r.members[userID]
	r.mu.Unlock()
	if !ok {
		return errors.New("成员不在房间")
	}
	return m.pc.AddICECandidate(cand)
}

// closeAll closes every member's PeerConnection (graceful shutdown / delete
// channel). Must be called with r.mu held.
func (r *Room) closeAll() {
	for id, m := range r.members {
		if m.pc != nil {
			_ = m.pc.Close()
		}
		delete(r.members, id)
	}
	r.trackLocals = make(map[string]*webrtc.TrackLocalStaticRTP)
}

// deref returns the pointed-to string or "".
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// derefU16 returns the pointed-to uint16 or 0.
func derefU16(v *uint16) uint16 {
	if v == nil {
		return 0
	}
	return *v
}

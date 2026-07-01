package signaling

import (
	"github.com/pion/webrtc/v4"

	"lumen/internal/protocol"
)

// handleAnswer processes webrtc_answer (contract §4.6): apply the client's SDP
// answer to its server-side PeerConnection.
func (c *Client) handleAnswer(env protocol.Envelope) {
	var d protocol.WebRTCSDP
	if err := decodeData(env.Data, &d); err != nil {
		c.enqueue(wsError("VALIDATION_ERROR", "SDP 无效", env.ID))
		return
	}
	if d.ChannelID == "" || d.SDP.Type != "answer" || d.SDP.SDP == "" {
		c.enqueue(wsError("VALIDATION_ERROR", "SDP 无效", env.ID))
		return
	}
	sdp := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: d.SDP.SDP}
	if err := c.hub.rooms.HandleAnswer(d.ChannelID, c.userID, sdp); err != nil {
		c.enqueue(wsError("INTERNAL", "应用 answer 失败", env.ID))
	}
}

// handleICE processes ice_candidate (contract §4.6): add the client's ICE
// candidate. A nil candidate (end-of-candidates) is tolerated and ignored.
func (c *Client) handleICE(env protocol.Envelope) {
	var d protocol.ICEPayload
	if err := decodeData(env.Data, &d); err != nil {
		return
	}
	if d.ChannelID == "" || d.Candidate == nil {
		return // end-of-candidates or malformed: nothing to add
	}
	idx := d.Candidate.SDPMLineIndex
	mid := d.Candidate.SDPMid
	init := webrtc.ICECandidateInit{
		Candidate:     d.Candidate.Candidate,
		SDPMid:        &mid,
		SDPMLineIndex: &idx,
	}
	if d.Candidate.UsernameFrag != "" {
		uf := d.Candidate.UsernameFrag
		init.UsernameFragment = &uf
	}
	_ = c.hub.rooms.HandleICE(d.ChannelID, c.userID, init)
}

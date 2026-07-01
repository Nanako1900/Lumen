package signaling

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lumen/internal/protocol"
	"lumen/internal/store"
)

func TestLeaveChannel_BroadcastsUserLeft(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "v1", Name: "voice", Type: "voice"})

	a := h.authenticate(t, "sub-a", "Alice")
	b := h.authenticate(t, "sub-b", "Bob")

	writeEnv(t, a, protocol.NewEnvelopeWithID("join_channel", protocol.ChannelRef{ChannelID: "v1"}, "j-a"))
	writeEnv(t, b, protocol.NewEnvelopeWithID("join_channel", protocol.ChannelRef{ChannelID: "v1"}, "j-b"))
	// Let both settle in the room.
	if !waitForType(a, "user_joined", 3*time.Second) {
		t.Fatal("Alice did not see Bob join")
	}

	// Bob leaves; Alice should get user_left for Bob.
	writeEnv(t, b, protocol.NewEnvelopeWithID("leave_channel", protocol.ChannelRef{ChannelID: "v1"}, "l-b"))
	if !waitForType(a, "user_left", 3*time.Second) {
		t.Error("Alice should receive user_left when Bob leaves")
	}
}

func TestLeaveChannel_NotInChannel(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "v1", Name: "voice", Type: "voice"})
	a := h.authenticate(t, "sub-a", "Alice")

	// Leave a channel we never joined -> VALIDATION_ERROR.
	writeEnv(t, a, protocol.NewEnvelopeWithID("leave_channel", protocol.ChannelRef{ChannelID: "v1"}, "l-1"))
	env := readEnv(t, a)
	if env.Type != "error" {
		t.Fatalf("type = %s, want error", env.Type)
	}
	var d protocol.ErrorData
	_ = json.Unmarshal(env.Data, &d)
	if d.Code != "VALIDATION_ERROR" || d.Ref != "l-1" {
		t.Errorf("error = %+v, want VALIDATION_ERROR ref l-1", d)
	}
}

func TestLeaveChannel_ChannelNotFound(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")
	writeEnv(t, a, protocol.NewEnvelopeWithID("leave_channel", protocol.ChannelRef{ChannelID: "ghost"}, "l-2"))
	env := readEnv(t, a)
	var d protocol.ErrorData
	_ = json.Unmarshal(env.Data, &d)
	if env.Type != "error" || d.Code != "NOT_FOUND" {
		t.Errorf("error = %s/%+v, want error/NOT_FOUND", env.Type, d)
	}
}

func TestSpeakingState_ForwardedToOthers(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "v1", Name: "voice", Type: "voice"})

	a := h.authenticate(t, "sub-a", "Alice")
	b := h.authenticate(t, "sub-b", "Bob")
	writeEnv(t, a, protocol.NewEnvelopeWithID("join_channel", protocol.ChannelRef{ChannelID: "v1"}, "j-a"))
	writeEnv(t, b, protocol.NewEnvelopeWithID("join_channel", protocol.ChannelRef{ChannelID: "v1"}, "j-b"))
	if !waitForType(a, "user_joined", 3*time.Second) {
		t.Fatal("join not observed")
	}

	// Alice reports speaking; Bob should receive speaking_state for Alice.
	writeEnv(t, a, protocol.NewEnvelope("speaking_state", protocol.SpeakingStateIn{Speaking: true}))
	if !waitForType(b, "speaking_state", 3*time.Second) {
		t.Error("Bob should receive Alice's speaking_state")
	}
}

func TestMuteState_ForwardedToOthers(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "v1", Name: "voice", Type: "voice"})

	a := h.authenticate(t, "sub-a", "Alice")
	b := h.authenticate(t, "sub-b", "Bob")
	writeEnv(t, a, protocol.NewEnvelopeWithID("join_channel", protocol.ChannelRef{ChannelID: "v1"}, "j-a"))
	writeEnv(t, b, protocol.NewEnvelopeWithID("join_channel", protocol.ChannelRef{ChannelID: "v1"}, "j-b"))
	if !waitForType(a, "user_joined", 3*time.Second) {
		t.Fatal("join not observed")
	}

	writeEnv(t, a, protocol.NewEnvelope("mute_state", protocol.MuteStateIn{Muted: true, Deafened: false}))
	if !waitForType(b, "mute_state", 3*time.Second) {
		t.Error("Bob should receive Alice's mute_state")
	}
}

func TestWebRTCAnswer_InvalidRejected(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")

	// Answer with wrong sdp type -> VALIDATION_ERROR.
	writeEnv(t, a, protocol.NewEnvelopeWithID("webrtc_answer",
		protocol.WebRTCSDP{ChannelID: "v1", SDP: protocol.SessionDescription{Type: "offer", SDP: "x"}}, "w-1"))
	env := readEnv(t, a)
	var d protocol.ErrorData
	_ = json.Unmarshal(env.Data, &d)
	if env.Type != "error" || d.Code != "VALIDATION_ERROR" {
		t.Errorf("error = %s/%+v, want error/VALIDATION_ERROR", env.Type, d)
	}
}

func TestICECandidate_EndOfCandidatesTolerated(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")

	// A nil candidate (end-of-candidates) must not error or crash.
	writeEnv(t, a, protocol.NewEnvelope("ice_candidate",
		protocol.ICEPayload{ChannelID: "v1", Candidate: nil}))

	// Follow with a valid request to confirm the connection is still alive.
	writeEnv(t, a, protocol.NewEnvelopeWithID("leave_channel", protocol.ChannelRef{ChannelID: "ghost"}, "probe"))
	env := readEnv(t, a)
	if env.Type != "error" {
		t.Errorf("connection should still respond after nil ICE, got %s", env.Type)
	}
}

func TestReauth_UpdatesSession(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")

	// Send reauth with a fresh token; server replies auth_ok with reauth=true.
	writeEnv(t, a, protocol.NewEnvelope("reauth",
		protocol.AuthData{AccessToken: h.signer.Token(t, "sub-a", "Alice", "")}))
	env := readEnv(t, a)
	if env.Type != "auth_ok" {
		t.Fatalf("type = %s, want auth_ok on reauth", env.Type)
	}
	var d protocol.AuthOKData
	_ = json.Unmarshal(env.Data, &d)
	if !d.Reauth {
		t.Error("reauth response should have reauth=true")
	}
}

func TestKick_SendsAuthErrorThenCloses(t *testing.T) {
	h := newWSHarness(t)
	victim := h.authenticate(t, "sub-victim", "Vic")

	// Find the victim's user id (created via UpsertUser at auth).
	u, err := h.st.GetUserBySubject(context.Background(), "sub-victim")
	if err != nil {
		t.Fatalf("get victim: %v", err)
	}
	// Set a ban so the auth_error carries kicked_until/retry_after.
	_ = h.st.SetKickedUntil(context.Background(), u.ID, time.Now().Add(time.Hour))

	h.hub.DisconnectUser(u.ID, "KICKED")

	env := readEnv(t, victim)
	if env.Type != "auth_error" {
		t.Fatalf("type = %s, want auth_error", env.Type)
	}
	var d protocol.AuthErrorData
	_ = json.Unmarshal(env.Data, &d)
	if d.Code != "KICKED" {
		t.Errorf("code = %s, want KICKED", d.Code)
	}
	if d.KickedUntil == "" || d.RetryAfter == nil {
		t.Errorf("banned kick should carry kicked_until/retry_after, got %+v", d)
	}
}

func TestCloseVoiceChannel_BroadcastsUserLeft(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "v1", Name: "voice", Type: "voice"})
	a := h.authenticate(t, "sub-a", "Alice")

	writeEnv(t, a, protocol.NewEnvelopeWithID("join_channel", protocol.ChannelRef{ChannelID: "v1"}, "j-a"))
	// Let the join land (Alice gets webrtc_offer).
	if !waitForType(a, "webrtc_offer", 3*time.Second) {
		t.Fatal("join did not produce an offer")
	}

	h.hub.CloseVoiceChannel("v1")
	if !waitForType(a, "user_left", 3*time.Second) {
		t.Error("closing the voice channel should broadcast user_left to members")
	}
}

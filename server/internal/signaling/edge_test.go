package signaling

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lumen/internal/protocol"
	"lumen/internal/store"
)

func TestSendMessage_ChannelNotFound(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")
	writeEnv(t, a, protocol.NewEnvelopeWithID("send_message",
		protocol.SendMessageData{ChannelID: "ghost", Content: "hi"}, "m-1"))
	env := readEnv(t, a)
	var d protocol.ErrorData
	_ = json.Unmarshal(env.Data, &d)
	if env.Type != "error" || d.Code != "NOT_FOUND" || d.Ref != "m-1" {
		t.Errorf("error = %s/%+v, want error/NOT_FOUND ref m-1", env.Type, d)
	}
}

func TestSpeakingState_IgnoredWhenNotInVoice(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")
	b := h.authenticate(t, "sub-b", "Bob")

	// Alice is not in any voice channel; speaking_state is a no-op (no crash,
	// nothing forwarded). We assert by sending a follow-up that DOES produce a
	// response, proving the connection is healthy and nothing errored.
	writeEnv(t, a, protocol.NewEnvelope("speaking_state", protocol.SpeakingStateIn{Speaking: true}))
	writeEnv(t, a, protocol.NewEnvelopeWithID("send_message",
		protocol.SendMessageData{ChannelID: "ghost", Content: "x"}, "probe"))
	env := readEnv(t, a)
	if env.Type != "error" {
		t.Fatalf("connection unhealthy after speaking no-op: %s", env.Type)
	}
	// Bob should not have received a speaking_state (best-effort: short wait).
	if waitForType(b, "speaking_state", 500*time.Millisecond) {
		t.Error("no speaking_state should be forwarded when sender is not in voice")
	}
}

func TestMuteState_IgnoredWhenNotInVoice(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")
	writeEnv(t, a, protocol.NewEnvelope("mute_state", protocol.MuteStateIn{Muted: true}))
	// Follow-up proves the connection is alive and no crash occurred.
	writeEnv(t, a, protocol.NewEnvelopeWithID("send_message",
		protocol.SendMessageData{ChannelID: "ghost", Content: "x"}, "probe"))
	if readEnv(t, a).Type != "error" {
		t.Error("connection unhealthy after mute no-op")
	}
}

func TestMalformedJoinData_ValidationError(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")
	// join_channel with empty channel_id.
	writeEnv(t, a, protocol.NewEnvelopeWithID("join_channel", protocol.ChannelRef{ChannelID: ""}, "j-x"))
	env := readEnv(t, a)
	var d protocol.ErrorData
	_ = json.Unmarshal(env.Data, &d)
	if env.Type != "error" || d.Code != "VALIDATION_ERROR" {
		t.Errorf("error = %s/%+v, want error/VALIDATION_ERROR", env.Type, d)
	}
}

func TestReauth_MissingTokenClosesWithError(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")
	// reauth with empty token -> auth_error (TOKEN_INVALID) and close.
	writeEnv(t, a, protocol.NewEnvelope("reauth", protocol.AuthData{AccessToken: ""}))
	env := readEnv(t, a)
	if env.Type != "auth_error" {
		t.Fatalf("type = %s, want auth_error", env.Type)
	}
}

func TestReauth_KickedDuringCooldownRejected(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")

	// Ban the user, then reauth: should be rejected with KICKED.
	u, _ := h.st.GetUserBySubject(context.Background(), "sub-a")
	_ = h.st.SetKickedUntil(context.Background(), u.ID, time.Now().Add(time.Hour))

	writeEnv(t, a, protocol.NewEnvelope("reauth",
		protocol.AuthData{AccessToken: h.signer.Token(t, "sub-a", "Alice", "")}))
	env := readEnv(t, a)
	var d protocol.AuthErrorData
	_ = json.Unmarshal(env.Data, &d)
	if env.Type != "auth_error" || d.Code != "KICKED" {
		t.Errorf("reauth during cooldown = %s/%+v, want auth_error/KICKED", env.Type, d)
	}
}

func TestCloseAll_ClosesConnections(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")

	h.hub.CloseAll()
	// The pump should observe the connection closing.
	select {
	case _, ok := <-a.incoming:
		if ok {
			// A close frame may arrive as an error in pump -> channel closes; if
			// we got a message first, drain until closed.
			for range a.incoming {
			}
		}
	case <-time.After(2 * time.Second):
		t.Error("connection did not close after CloseAll")
	}
}

func TestUserUpdated_BroadcastOnProfileChange(t *testing.T) {
	h := newWSHarness(t)
	// Pre-seed the user with an old name so re-auth detects a change.
	h.st.AddUser(store.User{ID: "u1", OAuthSubject: "sub-a", DisplayName: "OldName"})

	// Another connection observes the broadcast.
	observer := h.authenticate(t, "sub-obs", "Observer")

	// Alice authenticates with a NEW name -> changed -> user_updated broadcast.
	a := h.dial(t)
	writeEnv(t, a, authFrame(h.signer.Token(t, "sub-a", "NewName", "")))
	if readEnv(t, a).Type != "auth_ok" {
		t.Fatal("expected auth_ok")
	}
	if !waitForType(observer, "user_updated", 3*time.Second) {
		t.Error("profile change should broadcast user_updated to online members")
	}
}

func TestHubUserJoinedSink_BroadcastsToChannelMembers(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "v1", Name: "voice", Type: "voice"})

	a := h.authenticate(t, "sub-a", "Alice")
	b := h.authenticate(t, "sub-b", "Bob")

	// Put both in the room so BroadcastToChannel has targets.
	writeEnv(t, a, protocol.NewEnvelopeWithID("join_channel", protocol.ChannelRef{ChannelID: "v1"}, "j-a"))
	writeEnv(t, b, protocol.NewEnvelopeWithID("join_channel", protocol.ChannelRef{ChannelID: "v1"}, "j-b"))
	if !waitForType(a, "user_joined", 3*time.Second) {
		t.Fatal("setup: join not observed")
	}

	// Directly invoke the SFU sink method with Bob's voice state; Alice (the
	// other member) should receive a user_joined with Bob's User snapshot.
	uBob, _ := h.st.GetUserBySubject(context.Background(), "sub-b")
	h.hub.UserJoined("v1", protocol.VoiceState{ChannelID: "v1", UserID: uBob.ID})
	if !waitForType(a, "user_joined", 3*time.Second) {
		t.Error("Hub.UserJoined sink should broadcast user_joined to other members")
	}
}

func TestHandleICE_ValidCandidateAccepted(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "v1", Name: "voice", Type: "voice"})
	a := h.authenticate(t, "sub-a", "Alice")

	writeEnv(t, a, protocol.NewEnvelopeWithID("join_channel", protocol.ChannelRef{ChannelID: "v1"}, "j-a"))
	if !waitForType(a, "webrtc_offer", 3*time.Second) {
		t.Fatal("setup: no offer after join")
	}

	// Send a syntactically valid ICE candidate; AddICECandidate is exercised.
	// It may or may not be usable, but must not crash or error the connection.
	writeEnv(t, a, protocol.NewEnvelope("ice_candidate", protocol.ICEPayload{
		ChannelID: "v1",
		Candidate: &protocol.ICECandidate{
			Candidate:     "candidate:1 1 udp 2130706431 127.0.0.1 40000 typ host",
			SDPMid:        "0",
			SDPMLineIndex: 0,
		},
	}))

	// Health probe.
	writeEnv(t, a, protocol.NewEnvelopeWithID("leave_channel", protocol.ChannelRef{ChannelID: "ghost"}, "probe"))
	if readEnv(t, a).Type == "" {
		t.Error("connection should remain responsive after a valid ICE candidate")
	}
}

func TestSendMessage_MalformedDataValidationError(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")
	// Hand-craft an envelope whose data is not a valid SendMessageData object.
	raw := json.RawMessage(`"not-an-object"`)
	writeEnv(t, a, protocol.Envelope{Type: "send_message", Data: raw, ID: "bad"})
	env := readEnv(t, a)
	var d protocol.ErrorData
	_ = json.Unmarshal(env.Data, &d)
	if env.Type != "error" || d.Code != "VALIDATION_ERROR" || d.Ref != "bad" {
		t.Errorf("malformed send_message = %s/%+v, want error/VALIDATION_ERROR ref bad", env.Type, d)
	}
}

func TestFirstLogin_NoUserUpdatedBroadcast(t *testing.T) {
	h := newWSHarness(t)
	observer := h.authenticate(t, "sub-obs", "Observer")

	// Brand-new user first login -> INSERT -> NOT changed -> no user_updated.
	a := h.dial(t)
	writeEnv(t, a, authFrame(h.signer.Token(t, "sub-new", "Newbie", "")))
	if readEnv(t, a).Type != "auth_ok" {
		t.Fatal("expected auth_ok")
	}
	if waitForType(observer, "user_updated", 700*time.Millisecond) {
		t.Error("first-login INSERT must NOT broadcast user_updated")
	}
}

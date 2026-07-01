package signaling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"lumen/internal/auth"
	"lumen/internal/authtest"
	"lumen/internal/protocol"
	"lumen/internal/sfu"
	"lumen/internal/store"
	"lumen/internal/storefake"
)

// wsHarness bundles a running hub server with a signer and store.
type wsHarness struct {
	signer *authtest.Signer
	st     *storefake.Fake
	hub    *Hub
	server *httptest.Server
}

func newWSHarness(t *testing.T, ownerSubjects ...string) *wsHarness {
	t.Helper()
	signer := authtest.NewSigner(t)
	st := storefake.New()
	api, err := sfu.NewAPI(0, "")
	if err != nil {
		t.Fatalf("sfu.NewAPI: %v", err)
	}
	rooms := sfu.NewRoomManager(api)
	hub := NewHub(st, signer.Verifier, auth.NewOwnerSet(ownerSubjects), rooms, nil, nil)
	rooms.SetSink(hub)

	srv := httptest.NewServer(Mount(http.NotFoundHandler(), hub))
	t.Cleanup(srv.Close)
	return &wsHarness{signer: signer, st: st, hub: hub, server: srv}
}

// wsURL returns the ws:// dial URL for the harness.
func (h *wsHarness) wsURL() string {
	return "ws" + strings.TrimPrefix(h.server.URL, "http") + "/ws"
}

// wsClient wraps a connection with a background read pump feeding a channel, so
// per-message timeouts never cancel a Read context (coder/websocket closes the
// whole connection when a Read ctx expires).
type wsClient struct {
	conn     *websocket.Conn
	incoming chan protocol.Envelope
}

// dial opens a raw WS connection with a background read pump.
func (h *wsHarness) dial(t *testing.T) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, h.wsURL(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	wc := &wsClient{conn: conn, incoming: make(chan protocol.Envelope, 128)}
	go wc.pump()
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	return wc
}

// pump reads frames forever (using context.Background so no per-read timeout
// closes the socket) and forwards decoded envelopes until the conn closes.
func (wc *wsClient) pump() {
	for {
		_, data, err := wc.conn.Read(context.Background())
		if err != nil {
			close(wc.incoming)
			return
		}
		var env protocol.Envelope
		if json.Unmarshal(data, &env) == nil {
			wc.incoming <- env
		}
	}
}

// writeEnv sends an envelope on a connection.
func writeEnv(t *testing.T, wc *wsClient, env protocol.Envelope) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, _ := json.Marshal(env)
	if err := wc.conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// readEnv returns the next envelope, failing on timeout.
func readEnv(t *testing.T, wc *wsClient) protocol.Envelope {
	t.Helper()
	select {
	case env, ok := <-wc.incoming:
		if !ok {
			t.Fatal("connection closed before a message arrived")
		}
		return env
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a message")
		return protocol.Envelope{}
	}
}

// authFrame builds an auth envelope for a token.
func authFrame(token string) protocol.Envelope {
	return protocol.NewEnvelope("auth", protocol.AuthData{AccessToken: token})
}

// authenticate dials and completes the handshake, returning the client.
func (h *wsHarness) authenticate(t *testing.T, subject, name string) *wsClient {
	t.Helper()
	wc := h.dial(t)
	writeEnv(t, wc, authFrame(h.signer.Token(t, subject, name, "")))
	env := readEnv(t, wc)
	if env.Type != "auth_ok" {
		t.Fatalf("expected auth_ok, got %s", env.Type)
	}
	return wc
}

func TestHandshake_AuthOK(t *testing.T) {
	h := newWSHarness(t, "sub-owner")
	conn := h.dial(t)
	writeEnv(t, conn, authFrame(h.signer.Token(t, "sub-owner", "Boss", "https://cdn/x.png")))

	env := readEnv(t, conn)
	if env.Type != "auth_ok" {
		t.Fatalf("type = %s, want auth_ok", env.Type)
	}
	var d protocol.AuthOKData
	_ = json.Unmarshal(env.Data, &d)
	if d.User.OAuthSubject != "sub-owner" || !d.User.IsOwner {
		t.Errorf("auth_ok user = %+v, want owner sub-owner", d.User)
	}
	if d.ServerTime == "" {
		t.Error("auth_ok should carry server_time")
	}
	if d.Reauth {
		t.Error("first auth should have reauth=false")
	}
}

func TestHandshake_GarbageTokenAuthError(t *testing.T) {
	h := newWSHarness(t)
	conn := h.dial(t)
	writeEnv(t, conn, authFrame("not.a.jwt"))

	env := readEnv(t, conn)
	if env.Type != "auth_error" {
		t.Fatalf("type = %s, want auth_error", env.Type)
	}
	var d protocol.AuthErrorData
	_ = json.Unmarshal(env.Data, &d)
	if d.Code != "TOKEN_INVALID" {
		t.Errorf("code = %s, want TOKEN_INVALID", d.Code)
	}
}

func TestHandshake_NonAuthFirstFrameRejected(t *testing.T) {
	h := newWSHarness(t)
	conn := h.dial(t)
	// Send a non-auth frame first.
	writeEnv(t, conn, protocol.NewEnvelope("send_message",
		protocol.SendMessageData{ChannelID: "x", Content: "hi"}))

	env := readEnv(t, conn)
	if env.Type != "auth_error" {
		t.Fatalf("type = %s, want auth_error for pre-auth non-auth frame", env.Type)
	}
}

func TestHandshake_ExpiredTokenAuthError(t *testing.T) {
	h := newWSHarness(t)
	conn := h.dial(t)
	writeEnv(t, conn, authFrame(h.signer.ExpiredToken(t, "sub-1")))

	env := readEnv(t, conn)
	if env.Type != "auth_error" {
		t.Fatalf("type = %s, want auth_error", env.Type)
	}
	var d protocol.AuthErrorData
	_ = json.Unmarshal(env.Data, &d)
	if d.Code != "TOKEN_EXPIRED" {
		t.Errorf("code = %s, want TOKEN_EXPIRED", d.Code)
	}
}

func TestHandshake_KickedRejected(t *testing.T) {
	h := newWSHarness(t)
	// Pre-create a banned user.
	h.st.AddUser(store.User{ID: "u1", OAuthSubject: "sub-banned", DisplayName: "B"})
	future := time.Now().Add(time.Hour)
	_ = h.st.SetKickedUntil(context.Background(), "u1", future)

	conn := h.dial(t)
	writeEnv(t, conn, authFrame(h.signer.Token(t, "sub-banned", "B", "")))
	env := readEnv(t, conn)
	if env.Type != "auth_error" {
		t.Fatalf("type = %s, want auth_error", env.Type)
	}
	var d protocol.AuthErrorData
	_ = json.Unmarshal(env.Data, &d)
	if d.Code != "KICKED" {
		t.Errorf("code = %s, want KICKED", d.Code)
	}
	if d.KickedUntil == "" || d.RetryAfter == nil {
		t.Errorf("KICKED auth_error should carry kicked_until and retry_after, got %+v", d)
	}
}

func TestHandshake_Timeout(t *testing.T) {
	h := newWSHarness(t)
	wc := h.dial(t)
	// Do not send any frame; the server should time out (~5s), send
	// HANDSHAKE_TIMEOUT, then close.
	select {
	case env, ok := <-wc.incoming:
		if !ok {
			t.Fatal("connection closed before auth_error arrived")
		}
		if env.Type != "auth_error" {
			t.Fatalf("type = %s, want auth_error (HANDSHAKE_TIMEOUT)", env.Type)
		}
		var d protocol.AuthErrorData
		_ = json.Unmarshal(env.Data, &d)
		if d.Code != "HANDSHAKE_TIMEOUT" {
			t.Errorf("code = %s, want HANDSHAKE_TIMEOUT", d.Code)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for HANDSHAKE_TIMEOUT auth_error")
	}
}

func TestSendMessage_BroadcastsToAll(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "t1", Name: "general", Type: "text"})

	a := h.authenticate(t, "sub-a", "Alice")
	b := h.authenticate(t, "sub-b", "Bob")

	writeEnv(t, a, protocol.NewEnvelopeWithID("send_message",
		protocol.SendMessageData{ChannelID: "t1", Content: "hello"}, "c-1"))

	// Both A (echo) and B receive the message broadcast.
	for _, conn := range []*wsClient{a, b} {
		env := readEnv(t, conn)
		if env.Type != "message" {
			t.Fatalf("type = %s, want message", env.Type)
		}
		var m protocol.Message
		_ = json.Unmarshal(env.Data, &m)
		if m.Content != "hello" || m.ChannelID != "t1" {
			t.Errorf("message = %+v, want content hello chan t1", m)
		}
		if m.Author == nil || m.Author.DisplayName != "Alice" {
			t.Errorf("message.author should inline Alice, got %+v", m.Author)
		}
	}
}

func TestSendMessage_EmptyContentError(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "t1", Name: "general", Type: "text"})
	a := h.authenticate(t, "sub-a", "Alice")

	writeEnv(t, a, protocol.NewEnvelopeWithID("send_message",
		protocol.SendMessageData{ChannelID: "t1", Content: "   "}, "c-9"))

	env := readEnv(t, a)
	if env.Type != "error" {
		t.Fatalf("type = %s, want error", env.Type)
	}
	var d protocol.ErrorData
	_ = json.Unmarshal(env.Data, &d)
	if d.Code != "VALIDATION_ERROR" || d.Ref != "c-9" {
		t.Errorf("error = %+v, want VALIDATION_ERROR ref c-9", d)
	}
}

func TestSendMessage_VoiceChannelRejected(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "v1", Name: "voice", Type: "voice"})
	a := h.authenticate(t, "sub-a", "Alice")

	writeEnv(t, a, protocol.NewEnvelopeWithID("send_message",
		protocol.SendMessageData{ChannelID: "v1", Content: "hi"}, "c-5"))

	env := readEnv(t, a)
	if env.Type != "error" {
		t.Fatalf("type = %s, want error", env.Type)
	}
	var d protocol.ErrorData
	_ = json.Unmarshal(env.Data, &d)
	if d.Code != "VALIDATION_ERROR" {
		t.Errorf("code = %s, want VALIDATION_ERROR", d.Code)
	}
}

func TestUnknownType_ValidationError(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")

	writeEnv(t, a, protocol.NewEnvelopeWithID("bogus_type", map[string]string{}, "c-x"))
	env := readEnv(t, a)
	if env.Type != "error" {
		t.Fatalf("type = %s, want error", env.Type)
	}
	var d protocol.ErrorData
	_ = json.Unmarshal(env.Data, &d)
	if d.Code != "VALIDATION_ERROR" || d.Ref != "c-x" {
		t.Errorf("error = %+v, want VALIDATION_ERROR ref c-x", d)
	}
}

func TestJoinChannel_NotFound(t *testing.T) {
	h := newWSHarness(t)
	a := h.authenticate(t, "sub-a", "Alice")

	writeEnv(t, a, protocol.NewEnvelopeWithID("join_channel",
		protocol.ChannelRef{ChannelID: "ghost"}, "c-j"))
	env := readEnv(t, a)
	if env.Type != "error" {
		t.Fatalf("type = %s, want error", env.Type)
	}
	var d protocol.ErrorData
	_ = json.Unmarshal(env.Data, &d)
	if d.Code != "NOT_FOUND" || d.Ref != "c-j" {
		t.Errorf("error = %+v, want NOT_FOUND ref c-j", d)
	}
}

func TestJoinChannel_TextChannelRejected(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "t1", Name: "general", Type: "text"})
	a := h.authenticate(t, "sub-a", "Alice")

	writeEnv(t, a, protocol.NewEnvelopeWithID("join_channel",
		protocol.ChannelRef{ChannelID: "t1"}, "c-j2"))
	env := readEnv(t, a)
	if env.Type != "error" {
		t.Fatalf("type = %s, want error", env.Type)
	}
	var d protocol.ErrorData
	_ = json.Unmarshal(env.Data, &d)
	if d.Code != "VALIDATION_ERROR" {
		t.Errorf("code = %s, want VALIDATION_ERROR", d.Code)
	}
}

func TestJoinChannel_BroadcastsUserJoinedToOthers(t *testing.T) {
	h := newWSHarness(t)
	h.st.AddChannel(store.Channel{ID: "v1", Name: "voice", Type: "voice"})

	a := h.authenticate(t, "sub-a", "Alice")
	b := h.authenticate(t, "sub-b", "Bob")

	// Alice joins first (no others to hear).
	writeEnv(t, a, protocol.NewEnvelopeWithID("join_channel",
		protocol.ChannelRef{ChannelID: "v1"}, "c-a"))

	// Bob joins; Alice should receive a user_joined for Bob (amid renegotiation
	// offers). The background pump keeps the connection alive throughout.
	writeEnv(t, b, protocol.NewEnvelopeWithID("join_channel",
		protocol.ChannelRef{ChannelID: "v1"}, "c-b"))

	if !waitForType(a, "user_joined", 3*time.Second) {
		t.Error("Alice should receive user_joined when Bob joins")
	}
}

// waitForType consumes pumped messages until one of the given type arrives or
// the timeout elapses.
func waitForType(wc *wsClient, want string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case env, ok := <-wc.incoming:
			if !ok {
				return false
			}
			if env.Type == want {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

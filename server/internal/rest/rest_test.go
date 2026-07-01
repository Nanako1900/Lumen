package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"lumen/internal/auth"
	"lumen/internal/authtest"
	"lumen/internal/config"
	"lumen/internal/protocol"
	"lumen/internal/sfu"
	"lumen/internal/store"
	"lumen/internal/storefake"
)

// fakeBroadcaster records broadcasts and disconnects for assertions.
type fakeBroadcaster struct {
	mu           sync.Mutex
	broadcasts   []protocol.Envelope
	disconnected []string
	closedVoice  []string
}

func (f *fakeBroadcaster) BroadcastAll(msg protocol.Envelope) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broadcasts = append(f.broadcasts, msg)
}

func (f *fakeBroadcaster) DisconnectUser(userID string, _ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnected = append(f.disconnected, userID)
}

func (f *fakeBroadcaster) CloseVoiceChannel(channelID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closedVoice = append(f.closedVoice, channelID)
}

func (f *fakeBroadcaster) types() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.broadcasts))
	for _, b := range f.broadcasts {
		out = append(out, b.Type)
	}
	return out
}

// testEnv wires a router with a fake store and broadcaster and a real verifier.
type testEnv struct {
	signer     *authtest.Signer
	st         *storefake.Fake
	bc         *fakeBroadcaster
	rooms      *sfu.RoomManager
	router     http.Handler
	updatesDir string
}

func newTestEnv(t *testing.T, ownerSubjects ...string) *testEnv {
	t.Helper()
	signer := authtest.NewSigner(t)
	st := storefake.New()
	bc := &fakeBroadcaster{}
	api, err := sfu.NewAPI(0, "")
	if err != nil {
		t.Fatalf("sfu.NewAPI: %v", err)
	}
	rooms := sfu.NewRoomManager(api)
	updatesDir := t.TempDir()

	router := NewRouter(Deps{
		Verifier: signer.Verifier,
		Owners:   auth.NewOwnerSet(ownerSubjects),
		Store:    st,
		Rooms:    rooms,
		Hub:      bc,
		Config:   config.Config{UpdatesDir: updatesDir},
	})
	return &testEnv{signer: signer, st: st, bc: bc, rooms: rooms, router: router, updatesDir: updatesDir}
}

// do performs an authenticated request and returns the decoded envelope.
func (e *testEnv) do(t *testing.T, method, path, token, body string) (int, Envelope) {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, r)

	var env Envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return rec.Code, env
}

func TestHealthz_Public(t *testing.T) {
	e := newTestEnv(t)
	code, env := e.do(t, http.MethodGet, "/api/v1/healthz", "", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !env.Success {
		t.Error("healthz should return success:true")
	}
}

func TestBearerMiddleware_MissingToken(t *testing.T) {
	e := newTestEnv(t)
	code, env := e.do(t, http.MethodGet, "/api/v1/me", "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
	if env.Error == nil || env.Error.Code != "UNAUTHENTICATED" {
		t.Errorf("error = %+v, want UNAUTHENTICATED", env.Error)
	}
}

func TestBearerMiddleware_ExpiredToken(t *testing.T) {
	e := newTestEnv(t)
	token := e.signer.ExpiredToken(t, "sub-1")
	code, env := e.do(t, http.MethodGet, "/api/v1/me", token, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
	if env.Error == nil || env.Error.Code != "TOKEN_EXPIRED" {
		t.Errorf("error = %+v, want TOKEN_EXPIRED", env.Error)
	}
}

func TestBearerMiddleware_GarbageToken(t *testing.T) {
	e := newTestEnv(t)
	code, env := e.do(t, http.MethodGet, "/api/v1/me", "garbage.token.here", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
	if env.Error == nil || env.Error.Code != "TOKEN_INVALID" {
		t.Errorf("error = %+v, want TOKEN_INVALID", env.Error)
	}
}

func TestMe_UpsertsAndReturnsUser(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	token := e.signer.Token(t, "sub-owner", "Boss", "https://cdn/b.png")
	code, env := e.do(t, http.MethodGet, "/api/v1/me", token, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	var u protocol.User
	remarshal(t, env.Data, &u)
	if u.OAuthSubject != "sub-owner" || u.DisplayName != "Boss" {
		t.Errorf("user = %+v, want subject sub-owner name Boss", u)
	}
	if !u.IsOwner {
		t.Error("configured owner should have is_owner=true")
	}
}

func TestBootstrap_ReturnsAllSections(t *testing.T) {
	e := newTestEnv(t)
	if err := e.st.SeedDefaultChannels(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	token := e.signer.Token(t, "sub-1", "Alice", "")
	code, env := e.do(t, http.MethodGet, "/api/v1/bootstrap", token, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	var resp bootstrapResp
	remarshal(t, env.Data, &resp)
	if resp.Me.OAuthSubject != "sub-1" {
		t.Errorf("me.subject = %q, want sub-1", resp.Me.OAuthSubject)
	}
	if len(resp.Channels) != 2 {
		t.Errorf("channels = %d, want 2 (seeded)", len(resp.Channels))
	}
	if len(resp.Members) != 1 {
		t.Errorf("members = %d, want 1 (just me)", len(resp.Members))
	}
	if resp.VoiceStates == nil {
		t.Error("voice_states should be non-nil (empty array)")
	}
	if resp.ServerTime == "" {
		t.Error("server_time should be set")
	}
}

func TestListChannels_TypeFilterValidation(t *testing.T) {
	e := newTestEnv(t)
	e.st.SeedDefaultChannels(context.Background())
	token := e.signer.Token(t, "sub-1", "A", "")

	// Bad type filter -> 400.
	code, env := e.do(t, http.MethodGet, "/api/v1/channels?type=bogus", token, "")
	if code != http.StatusBadRequest || env.Error == nil || env.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("bad type: code=%d err=%+v, want 400 VALIDATION_ERROR", code, env.Error)
	}

	// voice filter -> only voice channels.
	code, env = e.do(t, http.MethodGet, "/api/v1/channels?type=voice", token, "")
	if code != http.StatusOK {
		t.Fatalf("voice filter status = %d", code)
	}
	var channels []protocol.Channel
	remarshal(t, env.Data, &channels)
	if len(channels) != 1 || channels[0].Type != "voice" {
		t.Errorf("voice channels = %+v, want single voice", channels)
	}
}

func TestListMessages_VoiceChannelRejected(t *testing.T) {
	e := newTestEnv(t)
	e.st.SeedDefaultChannels(context.Background())
	token := e.signer.Token(t, "sub-1", "A", "")

	code, env := e.do(t, http.MethodGet, "/api/v1/channels/seed-voice/messages", token, "")
	if code != http.StatusBadRequest || env.Error == nil || env.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("voice messages: code=%d err=%+v, want 400 VALIDATION_ERROR", code, env.Error)
	}
}

func TestListMessages_ChannelNotFound(t *testing.T) {
	e := newTestEnv(t)
	token := e.signer.Token(t, "sub-1", "A", "")
	code, env := e.do(t, http.MethodGet, "/api/v1/channels/nope/messages", token, "")
	if code != http.StatusNotFound || env.Error == nil || env.Error.Code != "NOT_FOUND" {
		t.Errorf("missing channel: code=%d err=%+v, want 404 NOT_FOUND", code, env.Error)
	}
}

func TestListMessages_PaginationEnvelope(t *testing.T) {
	e := newTestEnv(t)
	e.st.AddChannel(store.Channel{ID: "t1", Name: "general", Type: "text"})
	// Insert 3 messages.
	for i := 0; i < 3; i++ {
		if _, err := e.st.InsertMessage(context.Background(), "t1", "author", "hi"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	token := e.signer.Token(t, "sub-1", "A", "")

	code, env := e.do(t, http.MethodGet, "/api/v1/channels/t1/messages?limit=2", token, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var resp messagesResp
	remarshal(t, env.Data, &resp)
	if len(resp.Messages) != 2 {
		t.Errorf("messages = %d, want 2 (limit)", len(resp.Messages))
	}
	if !resp.Meta.HasMore {
		t.Error("has_more should be true (3 messages, limit 2)")
	}
	if resp.Meta.NextBefore == nil {
		t.Error("next_before should be set when there are messages")
	}
	// Ascending order: first id < last id.
	if resp.Messages[0].ID >= resp.Messages[1].ID {
		t.Error("messages should be ascending by id")
	}
}

func TestOwnerGuard_NonOwnerForbidden(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	token := e.signer.Token(t, "sub-plain", "Plain", "") // not an owner
	code, env := e.do(t, http.MethodPost, "/api/v1/channels", token,
		`{"name":"新频道","type":"text"}`)
	if code != http.StatusForbidden || env.Error == nil || env.Error.Code != "FORBIDDEN" {
		t.Errorf("non-owner create: code=%d err=%+v, want 403 FORBIDDEN", code, env.Error)
	}
}

func TestOwnerCreateChannel_BroadcastsChannelCreated(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	token := e.signer.Token(t, "sub-owner", "Boss", "")
	code, env := e.do(t, http.MethodPost, "/api/v1/channels", token,
		`{"name":"开黑2","type":"voice"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; err=%+v", code, env.Error)
	}
	if !contains(e.bc.types(), "channel_created") {
		t.Errorf("broadcasts = %v, want channel_created", e.bc.types())
	}
}

func TestOwnerCreateChannel_Validation(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	token := e.signer.Token(t, "sub-owner", "Boss", "")
	// Empty name.
	code, env := e.do(t, http.MethodPost, "/api/v1/channels", token, `{"name":"","type":"text"}`)
	if code != http.StatusBadRequest || env.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("empty name: code=%d err=%+v", code, env.Error)
	}
	// Bad type.
	code, env = e.do(t, http.MethodPost, "/api/v1/channels", token, `{"name":"ok","type":"audio"}`)
	if code != http.StatusBadRequest || env.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("bad type: code=%d err=%+v", code, env.Error)
	}
}

func TestOwnerDeleteVoiceChannel_ClosesRoomAndBroadcasts(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	e.st.AddChannel(store.Channel{ID: "v1", Name: "voice", Type: "voice"})
	token := e.signer.Token(t, "sub-owner", "Boss", "")

	code, _ := e.do(t, http.MethodDelete, "/api/v1/channels/v1", token, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !contains(e.bc.closedVoice, "v1") {
		t.Errorf("closedVoice = %v, want v1", e.bc.closedVoice)
	}
	if !contains(e.bc.types(), "channel_deleted") {
		t.Errorf("broadcasts = %v, want channel_deleted", e.bc.types())
	}
}

func TestKick_CannotKickSelf(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	// Register owner in store so GetUserBySubject resolves to an id.
	e.st.AddUser(store.User{ID: "owner-id", OAuthSubject: "sub-owner", DisplayName: "Boss"})
	token := e.signer.Token(t, "sub-owner", "Boss", "")

	code, env := e.do(t, http.MethodPost, "/api/v1/members/owner-id/kick", token, `{}`)
	if code != http.StatusBadRequest || env.Error == nil || env.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("self-kick: code=%d err=%+v, want 400 VALIDATION_ERROR", code, env.Error)
	}
}

func TestKick_DisconnectsTargetAndBans(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	e.st.AddUser(store.User{ID: "owner-id", OAuthSubject: "sub-owner", DisplayName: "Boss"})
	e.st.AddUser(store.User{ID: "victim-id", OAuthSubject: "sub-victim", DisplayName: "Vic"})
	token := e.signer.Token(t, "sub-owner", "Boss", "")

	code, _ := e.do(t, http.MethodPost, "/api/v1/members/victim-id/kick", token, `{"cooldown_seconds":60}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !contains(e.bc.disconnected, "victim-id") {
		t.Errorf("disconnected = %v, want victim-id", e.bc.disconnected)
	}
	// kicked_until must be written (ban).
	u, _ := e.st.GetUserByID(context.Background(), "victim-id")
	if u.KickedUntil == nil {
		t.Error("kicked_until should be set for cooldown>0")
	}
}

func TestKick_CooldownZeroNoBan(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	e.st.AddUser(store.User{ID: "owner-id", OAuthSubject: "sub-owner"})
	e.st.AddUser(store.User{ID: "victim-id", OAuthSubject: "sub-victim"})
	token := e.signer.Token(t, "sub-owner", "Boss", "")

	code, _ := e.do(t, http.MethodPost, "/api/v1/members/victim-id/kick", token, `{"cooldown_seconds":0}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !contains(e.bc.disconnected, "victim-id") {
		t.Error("cooldown=0 should still disconnect")
	}
	u, _ := e.st.GetUserByID(context.Background(), "victim-id")
	if u.KickedUntil != nil {
		t.Error("kicked_until should NOT be set for cooldown=0")
	}
}

func TestListMembers_OrderedWithIsOwner(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	e.st.AddUser(store.User{ID: "u1", OAuthSubject: "sub-owner", DisplayName: "Zed"})
	e.st.AddUser(store.User{ID: "u2", OAuthSubject: "sub-plain", DisplayName: "Amy"})
	token := e.signer.Token(t, "sub-owner", "Zed", "")

	code, env := e.do(t, http.MethodGet, "/api/v1/members", token, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	var members []protocol.User
	remarshal(t, env.Data, &members)
	// Ordered by display_name: Amy before Zed.
	if len(members) != 2 || members[0].DisplayName != "Amy" || members[1].DisplayName != "Zed" {
		t.Fatalf("members order wrong: %+v", members)
	}
	if members[1].OAuthSubject == "sub-owner" && !members[1].IsOwner {
		t.Error("owner member should have is_owner=true")
	}
	if members[0].IsOwner {
		t.Error("non-owner member should have is_owner=false")
	}
}

func TestUpdateChannel_BroadcastsAndReturns(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	e.st.AddChannel(store.Channel{ID: "c1", Name: "old", Type: "text", Position: 0})
	token := e.signer.Token(t, "sub-owner", "Boss", "")

	code, env := e.do(t, http.MethodPatch, "/api/v1/channels/c1", token,
		`{"name":"new","position":3}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; err=%+v", code, env.Error)
	}
	var ch protocol.Channel
	remarshal(t, env.Data, &ch)
	if ch.Name != "new" || ch.Position != 3 {
		t.Errorf("updated channel = %+v, want new/3", ch)
	}
	if !contains(e.bc.types(), "channel_updated") {
		t.Errorf("broadcasts = %v, want channel_updated", e.bc.types())
	}
}

func TestUpdateChannel_NotFound(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	token := e.signer.Token(t, "sub-owner", "Boss", "")
	code, env := e.do(t, http.MethodPatch, "/api/v1/channels/ghost", token, `{"name":"x"}`)
	if code != http.StatusNotFound || env.Error == nil || env.Error.Code != "NOT_FOUND" {
		t.Errorf("code=%d err=%+v, want 404 NOT_FOUND", code, env.Error)
	}
}

func TestUpdateChannel_NoFields(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	e.st.AddChannel(store.Channel{ID: "c1", Name: "old", Type: "text"})
	token := e.signer.Token(t, "sub-owner", "Boss", "")
	code, env := e.do(t, http.MethodPatch, "/api/v1/channels/c1", token, `{}`)
	if code != http.StatusBadRequest || env.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("empty patch: code=%d err=%+v, want 400 VALIDATION_ERROR", code, env.Error)
	}
}

func TestDeleteChannel_NotFound(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	token := e.signer.Token(t, "sub-owner", "Boss", "")
	code, env := e.do(t, http.MethodDelete, "/api/v1/channels/ghost", token, "")
	if code != http.StatusNotFound || env.Error.Code != "NOT_FOUND" {
		t.Errorf("code=%d err=%+v, want 404 NOT_FOUND", code, env.Error)
	}
}

func TestDeleteTextChannel_NoRoomClose(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	e.st.AddChannel(store.Channel{ID: "t1", Name: "text", Type: "text"})
	token := e.signer.Token(t, "sub-owner", "Boss", "")
	code, _ := e.do(t, http.MethodDelete, "/api/v1/channels/t1", token, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(e.bc.closedVoice) != 0 {
		t.Error("deleting a text channel should not close any voice room")
	}
	if !contains(e.bc.types(), "channel_deleted") {
		t.Error("delete should broadcast channel_deleted")
	}
}

func TestKick_TargetNotFound(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	e.st.AddUser(store.User{ID: "owner-id", OAuthSubject: "sub-owner"})
	token := e.signer.Token(t, "sub-owner", "Boss", "")
	code, env := e.do(t, http.MethodPost, "/api/v1/members/ghost/kick", token, `{}`)
	if code != http.StatusNotFound || env.Error.Code != "NOT_FOUND" {
		t.Errorf("code=%d err=%+v, want 404 NOT_FOUND", code, env.Error)
	}
}

func TestKick_NegativeCooldownRejected(t *testing.T) {
	e := newTestEnv(t, "sub-owner")
	e.st.AddUser(store.User{ID: "owner-id", OAuthSubject: "sub-owner"})
	e.st.AddUser(store.User{ID: "victim-id", OAuthSubject: "sub-victim"})
	token := e.signer.Token(t, "sub-owner", "Boss", "")
	code, env := e.do(t, http.MethodPost, "/api/v1/members/victim-id/kick", token, `{"cooldown_seconds":-5}`)
	if code != http.StatusBadRequest || env.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("negative cooldown: code=%d err=%+v, want 400 VALIDATION_ERROR", code, env.Error)
	}
}

func TestUpdatesFileServer_Serves(t *testing.T) {
	e := newTestEnv(t)
	// Write a file into the updates dir configured for this env.
	dir := e.updatesDir
	if err := os.WriteFile(dir+"/latest.json", []byte(`{"version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write update file: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/updates/latest.json", nil)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "1.0.0") {
		t.Errorf("body = %q, want update json", rec.Body.String())
	}
}

// remarshal round-trips a decoded map into a typed struct.
func remarshal(t *testing.T, data any, dst any) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("remarshal decode: %v", err)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

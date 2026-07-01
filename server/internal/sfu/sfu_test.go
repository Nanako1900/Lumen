package sfu

import (
	"errors"
	"sync"
	"testing"

	"github.com/pion/webrtc/v4"

	"lumen/internal/protocol"
)

// recordingSink captures user_joined / user_left events for assertions.
type recordingSink struct {
	mu     sync.Mutex
	joined []string
	left   []string
}

func (s *recordingSink) UserJoined(_ string, vs protocol.VoiceState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.joined = append(s.joined, vs.UserID)
}

func (s *recordingSink) UserLeft(_ string, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.left = append(s.left, userID)
}

// newTestManager builds a RoomManager over a real shared API. Using udpPort 0
// lets the OS pick a free port; no ICE completion happens in these tests.
func newTestManager(t *testing.T) (*RoomManager, *recordingSink) {
	t.Helper()
	api, err := NewAPI(0, "")
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	sink := &recordingSink{}
	mgr := NewRoomManager(api)
	mgr.SetSink(sink)
	return mgr, sink
}

func discard(protocol.Envelope) {}

func TestNewAPI_Succeeds(t *testing.T) {
	if _, err := NewAPI(0, "203.0.113.10"); err != nil {
		t.Fatalf("NewAPI with public IP: %v", err)
	}
}

func TestJoin_ReturnsOtherMembersExcludingSelf(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()

	ch := "chan-1"
	// First joiner sees no others.
	_, others, err := mgr.Join(ch, "user-a", "conn-a", discard)
	if err != nil {
		t.Fatalf("join a: %v", err)
	}
	if len(others) != 0 {
		t.Errorf("first joiner others = %v, want empty", others)
	}

	// Second joiner sees user-a but not itself.
	_, others, err = mgr.Join(ch, "user-b", "conn-b", discard)
	if err != nil {
		t.Fatalf("join b: %v", err)
	}
	if len(others) != 1 || others[0] != "user-a" {
		t.Errorf("second joiner others = %v, want [user-a]", others)
	}
}

func TestJoin_VoiceStateHasChannelAndUser(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()

	vs, _, err := mgr.Join("chan-x", "user-1", "conn-1", discard)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if vs.ChannelID != "chan-x" || vs.UserID != "user-1" {
		t.Errorf("VoiceState = %+v, want channel chan-x user user-1", vs)
	}
	if vs.Muted || vs.Deafened || vs.Speaking {
		t.Errorf("new VoiceState flags should be false, got %+v", vs)
	}
}

func TestSnapshot_ReflectsMembers(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()

	mgr.Join("c1", "u1", "x1", discard)
	mgr.Join("c1", "u2", "x2", discard)
	mgr.Join("c2", "u3", "x3", discard)

	snap := mgr.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snapshot len = %d, want 3", len(snap))
	}
	byUser := map[string]protocol.VoiceState{}
	for _, vs := range snap {
		byUser[vs.UserID] = vs
	}
	if byUser["u1"].ChannelID != "c1" || byUser["u3"].ChannelID != "c2" {
		t.Errorf("snapshot channel mapping wrong: %+v", byUser)
	}
}

func TestLeave_RemovesMember(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()

	mgr.Join("c1", "u1", "x1", discard)
	mgr.Join("c1", "u2", "x2", discard)

	if !mgr.Leave("c1", "u1") {
		t.Error("Leave should report present=true for existing member")
	}
	if mgr.Leave("c1", "u1") {
		t.Error("Leave should report present=false for already-removed member")
	}

	ch, ok := mgr.FindUserChannel("u2")
	if !ok || ch != "c1" {
		t.Errorf("FindUserChannel(u2) = (%q, %v), want (c1, true)", ch, ok)
	}
	if _, ok := mgr.FindUserChannel("u1"); ok {
		t.Error("u1 should no longer be found after leaving")
	}
}

func TestLeaveAll_AcrossRooms(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()

	// A user can only be in one channel at a time in practice, but LeaveAll
	// must handle removing them wherever present.
	mgr.Join("c1", "u1", "x1", discard)
	mgr.Join("c2", "u2", "x2", discard)

	left := mgr.LeaveAll("u1")
	if len(left) != 1 || left[0] != "c1" {
		t.Errorf("LeaveAll(u1) = %v, want [c1]", left)
	}
	if _, ok := mgr.FindUserChannel("u1"); ok {
		t.Error("u1 should be gone after LeaveAll")
	}
}

func TestJoin_SameUserSwitchEndpoint_NoDuplicate(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()

	ch := "c1"
	mgr.Join(ch, "u1", "conn-old", discard)
	// Re-join with a new connection handle: residual member replaced, no dup.
	_, others, err := mgr.Join(ch, "u1", "conn-new", discard)
	if err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	if len(others) != 0 {
		t.Errorf("rejoin others = %v, want empty (only self in room)", others)
	}
	// activeClient should now be the new handle.
	ac, ok := mgr.ActiveClientOf(ch, "u1")
	if !ok || ac != "conn-new" {
		t.Errorf("activeClientOf = (%v, %v), want (conn-new, true)", ac, ok)
	}
	// Exactly one member.
	if ids := mgr.MemberIDs(ch); len(ids) != 1 {
		t.Errorf("member count = %d, want 1 after endpoint switch", len(ids))
	}
}

func TestCloseRoom_ReturnsMembers(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()

	mgr.Join("c1", "u1", "x1", discard)
	mgr.Join("c1", "u2", "x2", discard)

	ids := mgr.CloseRoom("c1")
	if len(ids) != 2 {
		t.Errorf("CloseRoom returned %d ids, want 2", len(ids))
	}
	if _, ok := mgr.get("c1"); ok {
		t.Error("room should be removed after CloseRoom")
	}
}

func TestSetSpeakingAndMute(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()

	mgr.Join("c1", "u1", "x1", discard)

	vs, ok := mgr.SetSpeaking("c1", "u1", true)
	if !ok || !vs.Speaking {
		t.Errorf("SetSpeaking = (%+v, %v), want speaking true", vs, ok)
	}
	vs, ok = mgr.SetMute("c1", "u1", true, false)
	if !ok || !vs.Muted || vs.Deafened {
		t.Errorf("SetMute = (%+v, %v), want muted true deafened false", vs, ok)
	}
	// Non-member.
	if _, ok := mgr.SetSpeaking("c1", "ghost", true); ok {
		t.Error("SetSpeaking for non-member should report ok=false")
	}
}

func TestNextRenegoID_Monotonic(t *testing.T) {
	a := nextRenegoID()
	b := nextRenegoID()
	if a == b {
		t.Errorf("renego IDs should differ: %q == %q", a, b)
	}
}

func TestHandleAnswer_UnknownRoom(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()
	err := mgr.HandleAnswer("ghost", "u1", webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: "x"})
	if !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("HandleAnswer unknown room err = %v, want ErrRoomNotFound", err)
	}
}

func TestHandleICE_UnknownRoom(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()
	err := mgr.HandleICE("ghost", "u1", webrtc.ICECandidateInit{Candidate: "x"})
	if !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("HandleICE unknown room err = %v, want ErrRoomNotFound", err)
	}
}

func TestMemberVoiceState_AbsentReturnsZero(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()
	vs := mgr.MemberVoiceState("nope", "u1")
	if vs.ChannelID != "nope" || vs.UserID != "u1" || vs.Speaking || vs.Muted {
		t.Errorf("absent member state = %+v, want zeroed with channel/user set", vs)
	}
}

func TestMemberIDs_UnknownChannelNil(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()
	if ids := mgr.MemberIDs("nope"); ids != nil {
		t.Errorf("MemberIDs unknown channel = %v, want nil", ids)
	}
}

func TestFindUserChannel_NotPresent(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()
	if _, ok := mgr.FindUserChannel("ghost"); ok {
		t.Error("FindUserChannel should report not found for absent user")
	}
}

func TestHandleAnswer_MemberNotInRoom(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()
	// Create the room with one member, then answer for a DIFFERENT user.
	mgr.Join("v1", "present", "c1", discard)
	err := mgr.HandleAnswer("v1", "absent", webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: "x"})
	if err == nil {
		t.Error("HandleAnswer for a non-member should error")
	}
}

func TestHandleICE_MemberNotInRoom(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()
	mgr.Join("v1", "present", "c1", discard)
	err := mgr.HandleICE("v1", "absent", webrtc.ICECandidateInit{Candidate: "x"})
	if err == nil {
		t.Error("HandleICE for a non-member should error")
	}
}

func TestMemberVoiceState_PresentReturnsState(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()
	mgr.Join("v1", "u1", "c1", discard)
	mgr.SetMute("v1", "u1", true, true)
	vs := mgr.MemberVoiceState("v1", "u1")
	if !vs.Muted || !vs.Deafened || vs.ChannelID != "v1" || vs.UserID != "u1" {
		t.Errorf("present member state = %+v, want muted+deafened for v1/u1", vs)
	}
}

func TestActiveClientOf_AbsentUser(t *testing.T) {
	mgr, _ := newTestManager(t)
	defer mgr.CloseAllRooms()
	mgr.Join("v1", "u1", "c1", discard)
	if _, ok := mgr.ActiveClientOf("v1", "ghost"); ok {
		t.Error("ActiveClientOf should be false for an absent user")
	}
	if _, ok := mgr.ActiveClientOf("ghost", "u1"); ok {
		t.Error("ActiveClientOf should be false for an absent channel")
	}
}

func TestItoa(t *testing.T) {
	cases := map[uint64]string{0: "0", 7: "7", 12: "12", 100: "100", 9999: "9999"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}

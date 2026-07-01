package protocol

import "encoding/json"

// Envelope is the uniform WebSocket message wrapper (contract §4.1).
//
// Data is left as a raw JSON message on inbound decode so each handler can
// unmarshal into its own typed payload; on outbound encode it may hold any
// value. ID is an optional client-request correlation id echoed back on
// acks / errors; server-initiated broadcasts omit it.
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
	ID   string          `json:"id,omitempty"`
}

// NewEnvelope builds an outbound envelope, marshalling data into Data.
// Marshalling errors yield a nil Data; callers pass plain structs that
// always marshal, so this stays infallible at the call site.
func NewEnvelope(msgType string, data any) Envelope {
	raw, err := json.Marshal(data)
	if err != nil {
		raw = nil
	}
	return Envelope{Type: msgType, Data: raw}
}

// NewEnvelopeWithID is NewEnvelope with a correlation id (e.g. renegotiation).
func NewEnvelopeWithID(msgType string, data any, id string) Envelope {
	e := NewEnvelope(msgType, data)
	e.ID = id
	return e
}

// --- Auth (contract §4.3) ---

// AuthData is the payload of an inbound auth / reauth frame.
type AuthData struct {
	AccessToken string `json:"access_token"`
}

// AuthOKData is the payload of auth_ok (contract §4.3).
type AuthOKData struct {
	User       User   `json:"user"`
	ServerTime string `json:"server_time"`
	Reauth     bool   `json:"reauth"`
}

// AuthErrorData is the payload of auth_error (contract §4.3). KickedUntil /
// RetryAfter appear only when Code == "KICKED"; they are omitted otherwise.
type AuthErrorData struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	KickedUntil string `json:"kicked_until,omitempty"`
	RetryAfter  *int   `json:"retry_after,omitempty"`
}

// --- Generic error (contract §4.7) ---

// ErrorData is the payload of a non-fatal error message. Ref correlates the
// error back to the triggering client request id when present.
type ErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Ref     string `json:"ref,omitempty"`
}

// --- Voice join/leave & presence (contract §4.4) ---

// ChannelRef is the payload of join_channel / leave_channel.
type ChannelRef struct {
	ChannelID string `json:"channel_id"`
}

// UserJoinedData is the payload of user_joined (contract §4.4).
type UserJoinedData struct {
	ChannelID  string     `json:"channel_id"`
	VoiceState VoiceState `json:"voice_state"`
	User       User       `json:"user"`
}

// UserLeftData is the payload of user_left (contract §4.4).
type UserLeftData struct {
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
}

// SpeakingStateIn is the inbound speaking_state report (variation sink only).
type SpeakingStateIn struct {
	Speaking bool `json:"speaking"`
}

// SpeakingStateOut is the broadcast speaking_state (contract §4.4).
type SpeakingStateOut struct {
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	Speaking  bool   `json:"speaking"`
}

// MuteStateIn is the inbound mute_state report ([v1], contract §4.4).
type MuteStateIn struct {
	Muted    bool `json:"muted"`
	Deafened bool `json:"deafened"`
}

// MuteStateOut is the broadcast mute_state ([v1], contract §4.4).
type MuteStateOut struct {
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	Muted     bool   `json:"muted"`
	Deafened  bool   `json:"deafened"`
}

// --- Text messages (contract §4.5) ---

// SendMessageData is the payload of send_message.
type SendMessageData struct {
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
}

// --- Channel management broadcasts ([v1], contract §4.5) ---

// ChannelDeletedData is the payload of channel_deleted.
type ChannelDeletedData struct {
	ChannelID string `json:"channel_id"`
}

// --- WebRTC signalling (contract §4.6) ---

// SessionDescription mirrors the browser RTCSessionDescription shape. Native
// SDP field names are preserved (contract §7.3).
type SessionDescription struct {
	Type string `json:"type"` // "offer" | "answer"
	SDP  string `json:"sdp"`
}

// WebRTCSDP is the payload of webrtc_offer / webrtc_answer.
type WebRTCSDP struct {
	ChannelID string             `json:"channel_id"`
	SDP       SessionDescription `json:"sdp"`
}

// ICECandidate mirrors the browser RTCIceCandidateInit shape. Native field
// names are preserved (contract §7.3). A nil candidate means end-of-candidates.
type ICECandidate struct {
	Candidate     string `json:"candidate"`
	SDPMid        string `json:"sdpMid"`
	SDPMLineIndex uint16 `json:"sdpMLineIndex"`
	UsernameFrag  string `json:"usernameFragment"`
}

// ICEPayload is the payload of ice_candidate. Candidate is nil for
// end-of-candidates.
type ICEPayload struct {
	ChannelID string        `json:"channel_id"`
	Candidate *ICECandidate `json:"candidate"`
}

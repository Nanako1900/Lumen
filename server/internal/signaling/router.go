package signaling

import (
	"context"
	"encoding/json"

	"lumen/internal/protocol"
)

// dispatch routes an authenticated inbound message by type (contract §4.2).
// [v0] handlers plus the [v1] reauth/mute handlers are wired; unknown types get
// a VALIDATION_ERROR echoing the request id.
func (c *Client) dispatch(ctx context.Context, env protocol.Envelope) {
	switch env.Type {
	case "reauth": // [v1]
		_ = c.applyAuth(ctx, env, true)
	case "join_channel": // [v0]
		c.handleJoin(ctx, env)
	case "leave_channel": // [v0]
		c.handleLeave(env)
	case "send_message": // [v0]
		c.handleSendMessage(ctx, env)
	case "speaking_state": // [v0]
		c.handleSpeaking(env)
	case "mute_state": // [v1]
		c.handleMute(env)
	case "webrtc_answer": // [v0]
		c.handleAnswer(env)
	case "ice_candidate": // [v0]
		c.handleICE(env)
	default:
		c.enqueue(wsError("VALIDATION_ERROR", "未知消息类型", env.ID))
	}
}

// wsError builds a generic error envelope, echoing the triggering request id in
// ref when present (contract §4.7).
func wsError(code, message, ref string) protocol.Envelope {
	return protocol.NewEnvelope("error", protocol.ErrorData{Code: code, Message: message, Ref: ref})
}

// decodeData unmarshals an envelope's raw data into dst. A nil/empty payload
// decodes to the zero value without error.
func decodeData(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// currentUserDTO returns the wire DTO for this connection's user, looked up
// fresh from the store so profile changes are reflected.
func (c *Client) currentUserDTO(ctx context.Context) (protocol.User, bool) {
	return c.hub.userDTOCtx(ctx, c.userID)
}

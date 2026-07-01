package signaling

import (
	"context"
	"errors"

	"lumen/internal/protocol"
	"lumen/internal/store"
)

// handleJoin processes join_channel (contract §4.4, server-design §3.6).
//
// Validation failures reply with a generic error carrying the request id in
// ref. On success it: converges any prior voice connection of this user
// (implicit leave of another channel, and endpoint-switch dedup), joins the
// room, broadcasts user_joined to others, replays existing members' user_joined
// to the joiner (excluding self), and triggers SFU renegotiation.
func (c *Client) handleJoin(ctx context.Context, env protocol.Envelope) {
	var d protocol.ChannelRef
	if err := decodeData(env.Data, &d); err != nil || d.ChannelID == "" {
		c.enqueue(wsError("VALIDATION_ERROR", "缺少频道 ID", env.ID))
		return
	}

	ch, err := c.hub.store.GetChannel(ctx, d.ChannelID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.enqueue(wsError("NOT_FOUND", "频道不存在", env.ID))
			return
		}
		c.enqueue(wsError("INTERNAL", "加入语音失败", env.ID))
		return
	}
	if ch.Type != "voice" {
		c.enqueue(wsError("VALIDATION_ERROR", "该频道非语音频道", env.ID))
		return
	}

	// If the user is already in a DIFFERENT channel, implicitly leave it and
	// broadcast user_left there (a real departure).
	if prev, ok := c.hub.rooms.FindUserChannel(c.userID); ok && prev != d.ChannelID {
		if c.hub.rooms.Leave(prev, c.userID) {
			c.hub.UserLeft(prev, c.userID)
		}
	}

	// Join. RoomManager replaces any residual same-user member (endpoint
	// switch) without emitting user_left (person did not leave, just switched).
	vs, others, err := c.hub.rooms.Join(d.ChannelID, c.userID, any(c), c.enqueue)
	if err != nil {
		c.enqueue(wsError("INTERNAL", "加入语音失败", env.ID))
		return
	}
	c.setVoiceChannel(d.ChannelID)

	// Broadcast user_joined to the OTHER members (User snapshot from store).
	if joinerDTO, ok := c.currentUserDTO(ctx); ok {
		c.hub.BroadcastToChannel(d.ChannelID, protocol.NewEnvelope("user_joined",
			protocol.UserJoinedData{ChannelID: d.ChannelID, VoiceState: vs, User: joinerDTO}),
			c.userID)
	}

	// Replay existing members' user_joined to the joiner, excluding self.
	for _, memberID := range others {
		if memberID == c.userID {
			continue
		}
		memberDTO, ok := c.hub.userDTOCtx(ctx, memberID)
		if !ok {
			continue
		}
		c.enqueue(protocol.NewEnvelope("user_joined", protocol.UserJoinedData{
			ChannelID:  d.ChannelID,
			VoiceState: c.hub.memberVoiceState(d.ChannelID, memberID),
			User:       memberDTO,
		}))
	}
}

// handleLeave processes leave_channel (contract §4.4). Failures reply with a
// generic error carrying the request id.
func (c *Client) handleLeave(env protocol.Envelope) {
	var d protocol.ChannelRef
	if err := decodeData(env.Data, &d); err != nil || d.ChannelID == "" {
		c.enqueue(wsError("VALIDATION_ERROR", "缺少频道 ID", env.ID))
		return
	}
	// Determine presence for the failure contract.
	cur, inChannel := c.hub.rooms.FindUserChannel(c.userID)
	if !inChannel || cur != d.ChannelID {
		// Distinguish "channel does not exist" from "user not in it".
		if _, err := c.hub.store.GetChannel(context.Background(), d.ChannelID); errors.Is(err, store.ErrNotFound) {
			c.enqueue(wsError("NOT_FOUND", "频道不存在", env.ID))
			return
		}
		c.enqueue(wsError("VALIDATION_ERROR", "你不在该语音频道", env.ID))
		return
	}

	// Only broadcast user_left when the user's last connection has left.
	if c.hub.rooms.Leave(d.ChannelID, c.userID) {
		c.setVoiceChannel("")
		if c.hub.UserConnCount(c.userID) <= 1 {
			c.hub.UserLeft(d.ChannelID, c.userID)
		}
	}
}

// handleSpeaking processes speaking_state (contract §4.4): update in-memory
// state and forward to the other members of the channel.
func (c *Client) handleSpeaking(env protocol.Envelope) {
	var d protocol.SpeakingStateIn
	if err := decodeData(env.Data, &d); err != nil {
		return
	}
	channelID := c.currentVoiceChannel()
	if channelID == "" {
		return
	}
	if _, ok := c.hub.rooms.SetSpeaking(channelID, c.userID, d.Speaking); !ok {
		return
	}
	c.hub.BroadcastToChannel(channelID, protocol.NewEnvelope("speaking_state",
		protocol.SpeakingStateOut{ChannelID: channelID, UserID: c.userID, Speaking: d.Speaking}),
		c.userID)
}

// handleMute processes mute_state ([v1], contract §4.4): update in-memory state
// and forward to the other members of the channel.
func (c *Client) handleMute(env protocol.Envelope) {
	var d protocol.MuteStateIn
	if err := decodeData(env.Data, &d); err != nil {
		return
	}
	channelID := c.currentVoiceChannel()
	if channelID == "" {
		return
	}
	if _, ok := c.hub.rooms.SetMute(channelID, c.userID, d.Muted, d.Deafened); !ok {
		return
	}
	c.hub.BroadcastToChannel(channelID, protocol.NewEnvelope("mute_state",
		protocol.MuteStateOut{ChannelID: channelID, UserID: c.userID, Muted: d.Muted, Deafened: d.Deafened}),
		c.userID)
}

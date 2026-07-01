package signaling

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"lumen/internal/protocol"
	"lumen/internal/store"
)

// maxMessageRunes is the content length cap after trimming (contract §4.5).
const maxMessageRunes = 4000

// handleSendMessage validates, persists, and broadcasts a text message
// (contract §4.5). Failures reply with a generic error echoing the request id.
func (c *Client) handleSendMessage(ctx context.Context, env protocol.Envelope) {
	var d protocol.SendMessageData
	if err := decodeData(env.Data, &d); err != nil {
		c.enqueue(wsError("VALIDATION_ERROR", "请求体无效", env.ID))
		return
	}

	content := strings.TrimSpace(d.Content)
	if content == "" || utf8.RuneCountInString(content) > maxMessageRunes {
		c.enqueue(wsError("VALIDATION_ERROR", "content 不能为空且 ≤ 4000 字符", env.ID))
		return
	}

	ch, err := c.hub.store.GetChannel(ctx, d.ChannelID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.enqueue(wsError("NOT_FOUND", "频道不存在", env.ID))
			return
		}
		c.enqueue(wsError("INTERNAL", "保存失败", env.ID))
		return
	}
	if ch.Type != "text" {
		c.enqueue(wsError("VALIDATION_ERROR", "该频道非文字频道", env.ID))
		return
	}

	msg, err := c.hub.store.InsertMessage(ctx, d.ChannelID, c.userID, content)
	if err != nil {
		c.enqueue(wsError("INTERNAL", "保存失败", env.ID))
		return
	}

	dto := protocol.Message{
		ID:        msg.ID,
		ChannelID: msg.ChannelID,
		AuthorID:  msg.AuthorID,
		Content:   msg.Content,
		CreatedAt: msg.CreatedAt.UTC().Format(time.RFC3339),
	}
	if author, ok := c.currentUserDTO(ctx); ok {
		dto.Author = &author
	}
	c.hub.BroadcastAll(protocol.NewEnvelope("message", dto))
}

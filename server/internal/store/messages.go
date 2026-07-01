package store

import (
	"context"
	"fmt"
	"time"
)

// Pagination bounds (contract §3.4).
const (
	// DefaultMessageLimit is the page size when the client omits limit.
	DefaultMessageLimit = 50
	// MaxMessageLimit is the upper clamp for a single page.
	MaxMessageLimit = 100
)

// InsertMessage persists a text message with a freshly generated monotonic
// ULID and created_at (contract §4.5). It returns the stored row.
func (s *pgStore) InsertMessage(ctx context.Context, channelID, authorID, content string) (Message, error) {
	now := time.Now().UTC()
	var m Message
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO messages (id, channel_id, author_id, content, created_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, channel_id, author_id, content, created_at`,
		NewID(), channelID, authorID, content, now).
		Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.Content, &m.CreatedAt)
	if err != nil {
		return Message{}, fmt.Errorf("保存消息失败: %w", err)
	}
	return m, nil
}

// ListMessages returns a page of messages ordered oldest→newest, plus whether
// more (older) messages exist (contract §3.4 cursor semantics).
//
// Internally it fetches limit+1 rows by id DESC (hitting idx_messages_channel_id),
// where id is a strictly increasing ULID equivalent to time order. The extra
// row determines hasMore; the page is then reversed to ascending so the client
// can append it above existing entries. When before is empty this is the first
// (most recent) page; otherwise it returns rows strictly older than before.
func (s *pgStore) ListMessages(ctx context.Context, channelID, before string, limit int) ([]Message, bool, error) {
	if limit < 1 {
		limit = DefaultMessageLimit
	}
	if limit > MaxMessageLimit {
		limit = MaxMessageLimit
	}

	query := `SELECT id, channel_id, author_id, content, created_at
	            FROM messages WHERE channel_id = $1
	            ORDER BY id DESC LIMIT $2`
	args := []any{channelID, limit + 1}
	if before != "" {
		query = `SELECT id, channel_id, author_id, content, created_at
		           FROM messages WHERE channel_id = $1 AND id < $2
		           ORDER BY id DESC LIMIT $3`
		args = []any{channelID, before, limit + 1}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("查询消息失败: %w", err)
	}
	defer rows.Close()

	out := make([]Message, 0, limit+1)
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.Content, &m.CreatedAt); err != nil {
			return nil, false, fmt.Errorf("扫描消息失败: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("查询消息失败: %w", err)
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit] // drop the extra probe row
	}
	reverseMessages(out) // DESC -> ASC (old -> new)
	return out, hasMore, nil
}

// reverseMessages reverses a slice in place.
func reverseMessages(m []Message) {
	for i, j := 0, len(m)-1; i < j; i, j = i+1, j-1 {
		m[i], m[j] = m[j], m[i]
	}
}

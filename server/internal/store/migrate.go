package store

import (
	"context"
	"fmt"
)

// schemaDDL is the idempotent schema, copied verbatim from contract §5.2. No
// columns are added or removed. Executed once at startup.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    oauth_subject TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL DEFAULT '',
    avatar_url    TEXT NOT NULL DEFAULT '',
    kicked_until  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS channels (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    type       TEXT NOT NULL
                 CHECK (type IN ('text', 'voice')),
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_channels_position
    ON channels (position, id);

CREATE TABLE IF NOT EXISTS messages (
    id         TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL,
    author_id  TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (channel_id) REFERENCES channels (id) ON DELETE CASCADE,
    FOREIGN KEY (author_id)  REFERENCES users (id)    ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_messages_channel_id
    ON messages (channel_id, id DESC);
`

// Migrate executes the idempotent schema DDL (contract §5.2).
func (s *pgStore) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("迁移失败: %w", err)
	}
	return nil
}

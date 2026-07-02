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

-- Account center / desktop broker state (web-design.md §5.4 / §8.3).
-- broker_states holds two single-use kinds keyed by an opaque token:
--   login_ctx : oidc verifier/state/challenge staged by /desktop/login,
--               consumed once by /desktop/callback (TTL ~600s).
--   handoff   : the token set + bound_challenge staged by /desktop/callback,
--               consumed once by /api/desktop/exchange (TTL ~120s).
-- Every read guards WHERE expires_at > now(); consume is a one-shot
-- DELETE ... RETURNING inside a transaction (login_ctx and handoff are
-- single-use). payload is JSONB. access_token lives only inside a handoff row
-- for the ~120s handoff window and is never persisted anywhere else.
CREATE TABLE IF NOT EXISTS broker_states (
    key        TEXT PRIMARY KEY,
    kind       TEXT NOT NULL
                 CHECK (kind IN ('login_ctx', 'handoff')),
    payload    JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_broker_states_expires_at
    ON broker_states (expires_at);

-- desktop_sessions holds long-lived desktop sessions. refresh_token is stored
-- AES-256-GCM encrypted as bytea (LUMEN_REFRESH_ENC_KEY); access_token is never
-- persisted here. There is no expires_at: sessions live until logout or an IdP
-- refresh rejection deletes them (web-design.md §5.1 端点4/5).
CREATE TABLE IF NOT EXISTS desktop_sessions (
    id                TEXT PRIMARY KEY,
    refresh_token_enc BYTEA NOT NULL,
    sub               TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL
);
`

// Migrate executes the idempotent schema DDL (contract §5.2).
func (s *pgStore) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("迁移失败: %w", err)
	}
	return nil
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Broker TTLs (web-design.md §8.3), mirroring kv.ts LOGIN_CTX_TTL / HANDOFF_TTL.
const (
	// LoginContextTTL is the lifetime of a staged login_ctx (~600s).
	LoginContextTTL = 600 * time.Second
	// HandoffTTL is the lifetime of a handoff record (~120s).
	HandoffTTL = 120 * time.Second
)

// broker_states.kind values.
const (
	kindLoginCtx = "login_ctx"
	kindHandoff  = "handoff"
)

// PutLoginContext stages the OIDC login context keyed by the internal
// oidc_state, with a ~600s TTL (mirrors kv.ts putLoginContext). The payload is
// stored as JSONB. Overwrites any existing row for the same key.
func (s *pgStore) PutLoginContext(ctx context.Context, oidcState string, lc LoginContext) error {
	return s.putBrokerState(ctx, oidcState, kindLoginCtx, lc, LoginContextTTL)
}

// TakeLoginContext atomically reads and deletes the login context for
// oidcState, enforcing single-use and the expiry guard in one transaction
// (mirrors kv.ts takeLoginContext). Missing/expired returns ErrNotFound.
func (s *pgStore) TakeLoginContext(ctx context.Context, oidcState string) (LoginContext, error) {
	var lc LoginContext
	if err := s.consumeBrokerState(ctx, oidcState, kindLoginCtx, &lc); err != nil {
		return LoginContext{}, err
	}
	return lc, nil
}

// PutHandoff stages the handoff record keyed by handoff_code, with a ~120s TTL
// (mirrors kv.ts putHandoff). The access_token inside lives only for this
// window and is never persisted elsewhere.
func (s *pgStore) PutHandoff(ctx context.Context, handoffCode string, h Handoff) error {
	return s.putBrokerState(ctx, handoffCode, kindHandoff, h, HandoffTTL)
}

// ConsumeHandoff atomically reads and deletes the handoff for handoffCode,
// enforcing single-use and the expiry guard in one transaction (mirrors kv.ts
// consumeHandoff). Missing/expired/used returns ErrNotFound so the handler can
// map it to HANDOFF_NOT_FOUND (decision 6).
func (s *pgStore) ConsumeHandoff(ctx context.Context, handoffCode string) (Handoff, error) {
	var h Handoff
	if err := s.consumeBrokerState(ctx, handoffCode, kindHandoff, &h); err != nil {
		return Handoff{}, err
	}
	return h, nil
}

// putBrokerState upserts a broker_states row. payload is marshaled to JSONB and
// expires_at = now + ttl. Both broker kinds are single-use so an ON CONFLICT
// overwrite is harmless (a fresh flow simply replaces a stale one).
func (s *pgStore) putBrokerState(ctx context.Context, key, kind string, payload any, ttl time.Duration) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化 broker 状态失败: %w", err)
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO broker_states (key, kind, payload, expires_at, created_at)
		 VALUES ($1, $2, $3::jsonb, $4, $5)
		 ON CONFLICT (key) DO UPDATE
		     SET kind = EXCLUDED.kind,
		         payload = EXCLUDED.payload,
		         expires_at = EXCLUDED.expires_at,
		         created_at = EXCLUDED.created_at`,
		key, kind, string(raw), now.Add(ttl), now)
	if err != nil {
		return fmt.Errorf("写入 broker 状态失败: %w", err)
	}
	return nil
}

// consumeBrokerState atomically deletes a non-expired broker_states row of the
// given kind and unmarshals its payload into out. It uses DELETE ... RETURNING
// inside a transaction, guarded by expires_at > now(), so read+delete is a
// single one-shot step (no TOCTOU, no replay). ErrNotFound when absent, of the
// wrong kind, or expired.
func (s *pgStore) consumeBrokerState(ctx context.Context, key, kind string, out any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var raw []byte
	err = tx.QueryRowContext(ctx,
		`DELETE FROM broker_states
		  WHERE key = $1 AND kind = $2 AND expires_at > now()
		 RETURNING payload`,
		key, kind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("消费 broker 状态失败: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("反序列化 broker 状态失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// DeleteExpiredBrokerStates removes every expired login_ctx / handoff row and
// reports how many were deleted (decision 4 janitor). The expiry guard on reads
// already hides expired rows; this reclaims space and is run every 60s.
func (s *pgStore) DeleteExpiredBrokerStates(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM broker_states WHERE expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("清理过期 broker 状态失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("清理过期 broker 状态失败: %w", err)
	}
	return n, nil
}

// PutSession creates or replaces a desktop session, encrypting refresh_token at
// rest as bytea via the refresh sealer (decision 2). Mirrors kv.ts putSession.
func (s *pgStore) PutSession(ctx context.Context, sess DesktopSession) error {
	if s.refreshSealer == nil {
		return ErrNoSealer
	}
	enc, err := s.refreshSealer.Encrypt([]byte(sess.RefreshToken))
	if err != nil {
		return fmt.Errorf("加密 refresh_token 失败: %w", err)
	}
	createdAt := sess.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO desktop_sessions (id, refresh_token_enc, sub, created_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (id) DO UPDATE
		     SET refresh_token_enc = EXCLUDED.refresh_token_enc,
		         sub = EXCLUDED.sub`,
		sess.ID, enc, sess.Sub, createdAt.UTC())
	if err != nil {
		return fmt.Errorf("写入桌面会话失败: %w", err)
	}
	return nil
}

// GetSession reads a desktop session by id, decrypting refresh_token from the
// bytea column (mirrors kv.ts getSession). Missing returns ErrNotFound.
func (s *pgStore) GetSession(ctx context.Context, id string) (DesktopSession, error) {
	if s.refreshSealer == nil {
		return DesktopSession{}, ErrNoSealer
	}
	var (
		sess DesktopSession
		enc  []byte
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, refresh_token_enc, sub, created_at
		   FROM desktop_sessions WHERE id = $1`, id).
		Scan(&sess.ID, &enc, &sess.Sub, &sess.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DesktopSession{}, ErrNotFound
	}
	if err != nil {
		return DesktopSession{}, fmt.Errorf("查询桌面会话失败: %w", err)
	}
	plain, err := s.refreshSealer.Decrypt(enc)
	if err != nil {
		return DesktopSession{}, fmt.Errorf("解密 refresh_token 失败: %w", err)
	}
	sess.RefreshToken = string(plain)
	return sess, nil
}

// DeleteSession removes a desktop session (logout / refresh-failure cleanup,
// mirrors kv.ts deleteSession). Deleting a missing session is a no-op (no
// error), matching the KV delete semantics.
func (s *pgStore) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM desktop_sessions WHERE id = $1`, id); err != nil {
		return fmt.Errorf("删除桌面会话失败: %w", err)
	}
	return nil
}

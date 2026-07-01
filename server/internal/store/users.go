package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UpsertUser inserts or updates a user by oauth_subject and reports whether an
// existing row actually changed (contract §2.3 / §2.7, server-design §5.3).
//
// changed semantics (sync-3): changed == (ON CONFLICT DO UPDATE hit an
// existing row AND display_name or avatar_url actually differed). A first-time
// INSERT is never changed. The WHERE guard on the UPDATE skips no-op updates,
// and the returned xmax (0 for a fresh insert, non-zero for an updated row)
// distinguishes insert from update.
func (s *pgStore) UpsertUser(ctx context.Context, subject, displayName, avatarURL string) (User, bool, error) {
	now := time.Now().UTC()
	var (
		u          User
		xmax       string // "0" for INSERT, non-zero txid for an UPDATE
		kickedNull sql.NullTime
	)
	// The WHERE clause on DO UPDATE means a no-op (values unchanged) does not
	// update the row, so RETURNING yields nothing in that case. We therefore
	// run the upsert, then read back the row, comparing to decide changed.
	//
	// Single-statement approach: use a CTE so we get the row and xmax in one
	// round trip. On a no-op conflict the UPDATE ... WHERE returns no row, so
	// we fall back to selecting the existing row with changed=false.
	err := s.db.QueryRowContext(ctx,
		`WITH upsert AS (
		    INSERT INTO users (id, oauth_subject, display_name, avatar_url, created_at, updated_at)
		    VALUES ($1, $2, $3, $4, $5, $5)
		    ON CONFLICT (oauth_subject) DO UPDATE
		        SET display_name = EXCLUDED.display_name,
		            avatar_url   = EXCLUDED.avatar_url,
		            updated_at   = EXCLUDED.updated_at
		      WHERE users.display_name IS DISTINCT FROM EXCLUDED.display_name
		         OR users.avatar_url   IS DISTINCT FROM EXCLUDED.avatar_url
		    RETURNING id, oauth_subject, display_name, avatar_url, kicked_until,
		              created_at, updated_at, xmax::text
		 )
		 SELECT id, oauth_subject, display_name, avatar_url, kicked_until,
		        created_at, updated_at, xmax FROM upsert
		 UNION ALL
		 SELECT id, oauth_subject, display_name, avatar_url, kicked_until,
		        created_at, updated_at, '-1'
		   FROM users
		  WHERE oauth_subject = $2
		    AND NOT EXISTS (SELECT 1 FROM upsert)
		 LIMIT 1`,
		NewID(), subject, displayName, avatarURL, now).
		Scan(&u.ID, &u.OAuthSubject, &u.DisplayName, &u.AvatarURL, &kickedNull,
			&u.CreatedAt, &u.UpdatedAt, &xmax)
	if err != nil {
		return User{}, false, fmt.Errorf("upsert 用户失败: %w", err)
	}
	if kickedNull.Valid {
		t := kickedNull.Time
		u.KickedUntil = &t
	}
	// changed only when the CTE UPDATE branch fired (xmax non-zero, non-"-1").
	// xmax == "0" -> fresh INSERT (not changed); xmax == "-1" -> no-op fallback.
	changed := xmax != "0" && xmax != "-1"
	return u, changed, nil
}

// GetUserByID fetches a user by internal id, returning ErrNotFound if absent.
func (s *pgStore) GetUserByID(ctx context.Context, id string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, oauth_subject, display_name, avatar_url, kicked_until, created_at, updated_at
		   FROM users WHERE id = $1`, id))
}

// GetUserBySubject fetches a user by oauth_subject, returning ErrNotFound if
// absent.
func (s *pgStore) GetUserBySubject(ctx context.Context, subject string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, oauth_subject, display_name, avatar_url, kicked_until, created_at, updated_at
		   FROM users WHERE oauth_subject = $1`, subject))
}

// ListUsers returns all users ordered by display_name ascending (contract
// §3.4 端点 5).
func (s *pgStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, oauth_subject, display_name, avatar_url, kicked_until, created_at, updated_at
		   FROM users ORDER BY display_name ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询成员失败: %w", err)
	}
	defer rows.Close()

	out := make([]User, 0)
	for rows.Next() {
		var (
			u          User
			kickedNull sql.NullTime
		)
		if err := rows.Scan(&u.ID, &u.OAuthSubject, &u.DisplayName, &u.AvatarURL,
			&kickedNull, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("扫描成员失败: %w", err)
		}
		if kickedNull.Valid {
			t := kickedNull.Time
			u.KickedUntil = &t
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetKickedUntil writes users.kicked_until (contract §3.4 端点 9). The handler
// converts cooldown_seconds into an absolute time; this layer just persists it
// as TIMESTAMPTZ (data-2).
func (s *pgStore) SetKickedUntil(ctx context.Context, userID string, until time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET kicked_until = $2 WHERE id = $1`, userID, until.UTC())
	if err != nil {
		return fmt.Errorf("写入封禁时间失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("写入封禁时间失败: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// scanUser scans a single user row, mapping sql.ErrNoRows to ErrNotFound.
func (s *pgStore) scanUser(row *sql.Row) (User, error) {
	var (
		u          User
		kickedNull sql.NullTime
	)
	err := row.Scan(&u.ID, &u.OAuthSubject, &u.DisplayName, &u.AvatarURL,
		&kickedNull, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("查询用户失败: %w", err)
	}
	if kickedNull.Valid {
		t := kickedNull.Time
		u.KickedUntil = &t
	}
	return u, nil
}

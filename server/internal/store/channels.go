package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Deterministic seed channel IDs (contract §5.2.1). Fixed constants guarantee
// idempotent seeding across restarts via ON CONFLICT DO NOTHING. Both are
// valid Crockford base32 ULIDs.
const (
	seedTextChannelID  = "01HZY0000000000000000TEXT0" // text 大厅
	seedVoiceChannelID = "01HZY0000000000000000V01C1" // voice 开黑1
)

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("资源不存在")

// SeedDefaultChannels inserts the default channels only when the channels
// table is empty (contract §5.2.1). Deterministic IDs + ON CONFLICT DO
// NOTHING keep it idempotent across restarts, so a pure-v0 deployment has a
// text and voice channel without needing the [v1] owner CRUD.
func (s *pgStore) SeedDefaultChannels(ctx context.Context) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO channels (id, name, type, position, created_at, updated_at)
		 SELECT * FROM (VALUES
		   ($1, '大厅', 'text', 0, $3::timestamptz, $3::timestamptz),
		   ($2, '开黑1', 'voice', 1, $3::timestamptz, $3::timestamptz)
		 ) AS v
		 WHERE NOT EXISTS (SELECT 1 FROM channels)
		 ON CONFLICT (id) DO NOTHING`,
		seedTextChannelID, seedVoiceChannelID, now)
	if err != nil {
		return fmt.Errorf("种子频道失败: %w", err)
	}
	return nil
}

// ListChannels returns channels ordered by (position, id) ascending. An empty
// typeFilter returns all; otherwise it filters by type (contract §3.3 端点 3).
func (s *pgStore) ListChannels(ctx context.Context, typeFilter string) ([]Channel, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if typeFilter == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, name, type, position, created_at, updated_at
			   FROM channels ORDER BY position ASC, id ASC`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, name, type, position, created_at, updated_at
			   FROM channels WHERE type = $1 ORDER BY position ASC, id ASC`, typeFilter)
	}
	if err != nil {
		return nil, fmt.Errorf("查询频道失败: %w", err)
	}
	defer rows.Close()

	out := make([]Channel, 0)
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.Position, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("扫描频道失败: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetChannel fetches a single channel by id, returning ErrNotFound if absent.
func (s *pgStore) GetChannel(ctx context.Context, id string) (Channel, error) {
	var c Channel
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, type, position, created_at, updated_at
		   FROM channels WHERE id = $1`, id).
		Scan(&c.ID, &c.Name, &c.Type, &c.Position, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, fmt.Errorf("查询频道失败: %w", err)
	}
	return c, nil
}

// CreateChannel inserts a new channel ([v1] owner). When position is nil the
// channel is appended to the end within the same transaction via
// COALESCE(MAX(position), -1) + 1, avoiding several nil-position channels all
// landing on the default 0 (contract §3.4 端点 6).
func (s *pgStore) CreateChannel(ctx context.Context, name, ctype string, position *int) (Channel, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Channel{}, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	pos := 0
	if position != nil {
		pos = *position
	} else {
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(position), -1) + 1 FROM channels`).Scan(&pos); err != nil {
			return Channel{}, fmt.Errorf("计算频道位置失败: %w", err)
		}
	}

	now := time.Now().UTC()
	var c Channel
	err = tx.QueryRowContext(ctx,
		`INSERT INTO channels (id, name, type, position, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $5)
		 RETURNING id, name, type, position, created_at, updated_at`,
		NewID(), name, ctype, pos, now).
		Scan(&c.ID, &c.Name, &c.Type, &c.Position, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Channel{}, fmt.Errorf("创建频道失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Channel{}, fmt.Errorf("提交事务失败: %w", err)
	}
	return c, nil
}

// UpdateChannel updates name and/or position ([v1] owner). At least one field
// should be provided; updated_at is always refreshed. Returns ErrNotFound if
// the channel does not exist (contract §3.4 端点 7).
func (s *pgStore) UpdateChannel(ctx context.Context, id string, name *string, position *int) (Channel, error) {
	now := time.Now().UTC()
	var c Channel
	// COALESCE keeps the existing value when a field pointer is nil.
	err := s.db.QueryRowContext(ctx,
		`UPDATE channels
		    SET name = COALESCE($2, name),
		        position = COALESCE($3, position),
		        updated_at = $4
		  WHERE id = $1
		  RETURNING id, name, type, position, created_at, updated_at`,
		id, name, position, now).
		Scan(&c.ID, &c.Name, &c.Type, &c.Position, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, fmt.Errorf("更新频道失败: %w", err)
	}
	return c, nil
}

// DeleteChannel removes a channel; its messages cascade (contract §5.2 ON
// DELETE CASCADE). Returns ErrNotFound if the channel does not exist.
func (s *pgStore) DeleteChannel(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM channels WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("删除频道失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("删除频道失败: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

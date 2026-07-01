// Package store is the PostgreSQL persistence layer (contract §5,
// server-design §5). It exposes a small Store interface over three tables —
// users, channels, messages — accessed through database/sql with the pgx
// stdlib driver (CGO_ENABLED=0). All queries use $N placeholders.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers database/sql driver "pgx"
)

// Connection-pool sizing for the small-scale single-guild deployment.
const (
	maxOpenConns    = 10
	maxIdleConns    = 5
	connMaxLifetime = time.Hour
	connMaxIdleTime = 10 * time.Minute
	pingTimeout     = 5 * time.Second
)

// User is the store-layer row for a member. It holds only DB columns; the
// computed is_owner field is injected during DTO assembly, not here.
// KickedUntil is nil when the user is not soft-banned.
type User struct {
	ID           string
	OAuthSubject string
	DisplayName  string
	AvatarURL    string
	KickedUntil  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Channel is the store-layer row for a channel.
type Channel struct {
	ID        string
	Name      string
	Type      string
	Position  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Message is the store-layer row for a text message. The inline author
// snapshot is a DTO concern and is not part of this struct.
type Message struct {
	ID        string
	ChannelID string
	AuthorID  string
	Content   string
	CreatedAt time.Time
}

// Store is the persistence contract consumed by the rest and signaling layers.
type Store interface {
	// Lifecycle / schema.
	Migrate(ctx context.Context) error
	SeedDefaultChannels(ctx context.Context) error
	Close() error

	// users (contract §5.2).
	UpsertUser(ctx context.Context, subject, displayName, avatarURL string) (user User, changed bool, err error)
	GetUserByID(ctx context.Context, id string) (User, error)
	GetUserBySubject(ctx context.Context, subject string) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	SetKickedUntil(ctx context.Context, userID string, until time.Time) error

	// channels (contract §5.2).
	ListChannels(ctx context.Context, typeFilter string) ([]Channel, error)
	GetChannel(ctx context.Context, id string) (Channel, error)
	CreateChannel(ctx context.Context, name, ctype string, position *int) (Channel, error)
	UpdateChannel(ctx context.Context, id string, name *string, position *int) (Channel, error)
	DeleteChannel(ctx context.Context, id string) error

	// messages (contract §5.2 / §3.4 pagination).
	InsertMessage(ctx context.Context, channelID, authorID, content string) (Message, error)
	ListMessages(ctx context.Context, channelID, before string, limit int) (msgs []Message, hasMore bool, err error)
}

// pgStore is the concrete PostgreSQL implementation of Store.
type pgStore struct {
	db *sql.DB
}

// Open connects to PostgreSQL and configures the connection pool
// (server-design §5.1). dsn is LUMEN_DATABASE_URL.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("连接数据库失败（检查 LUMEN_DATABASE_URL/网络/sslmode）: %w", err)
	}
	return db, nil
}

// New wraps an open *sql.DB as a Store.
func New(db *sql.DB) Store {
	return &pgStore{db: db}
}

// Close releases the underlying connection pool.
func (s *pgStore) Close() error {
	return s.db.Close()
}

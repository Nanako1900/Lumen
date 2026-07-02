// Package store is the PostgreSQL persistence layer (contract §5,
// server-design §5). It exposes a small Store interface over three tables —
// users, channels, messages — accessed through database/sql with the pgx
// stdlib driver (CGO_ENABLED=0). All queries use $N placeholders.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers database/sql driver "pgx"

	"lumen/internal/secure"
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

// --- Account center / desktop broker rows (web-design.md §5.4) ---

// LoginContext is the staged OIDC login state (broker_states.kind='login_ctx'),
// keyed by the internal oidc_state. It mirrors kv.ts LoginContext. Single-use:
// staged by /desktop/login, consumed once by /desktop/callback.
type LoginContext struct {
	State        string `json:"state"`         // desktop-side opaque state (loopback check)
	Challenge    string `json:"challenge"`     // S256(handoff_verifier), desktop PKCE challenge
	RedirectURI  string `json:"redirect_uri"`  // validated 127.0.0.1 loopback address
	OIDCVerifier string `json:"oidc_verifier"` // our OIDC PKCE verifier
}

// DesktopProfile is the normalized display profile (mirrors kv.ts).
type DesktopProfile struct {
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

// Handoff is the one-time handoff record (broker_states.kind='handoff'), keyed
// by handoff_code. It mirrors kv.ts HandoffRecord. access_token lives here only
// for the ~120s handoff window and is never persisted elsewhere. Single-use:
// staged by /desktop/callback, consumed once by /api/desktop/exchange.
type Handoff struct {
	AccessToken    string         `json:"access_token"`
	ExpiresIn      int            `json:"expires_in"`
	RefreshToken   string         `json:"refresh_token"`
	Sub            string         `json:"sub"`
	BoundChallenge string         `json:"bound_challenge"` // = desktop challenge
	Profile        DesktopProfile `json:"profile"`
}

// DesktopSession is a long-lived desktop session (desktop_sessions row). The
// RefreshToken field is plaintext at the store boundary; the store encrypts it
// (AES-256-GCM) before writing and decrypts on read, so it never leaves the
// process in plaintext at rest (decision 2). Mirrors kv.ts SessionRecord.
type DesktopSession struct {
	ID           string
	RefreshToken string // plaintext at the API boundary; encrypted at rest
	Sub          string
	CreatedAt    time.Time
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

	// broker: login context (web-design.md §5.1 端点1/2). Single-use, TTL~600s.
	PutLoginContext(ctx context.Context, oidcState string, lc LoginContext) error
	TakeLoginContext(ctx context.Context, oidcState string) (LoginContext, error)

	// broker: handoff (web-design.md §5.1 端点2/3). Single-use, TTL~120s.
	PutHandoff(ctx context.Context, handoffCode string, h Handoff) error
	ConsumeHandoff(ctx context.Context, handoffCode string) (Handoff, error)

	// broker: desktop sessions (web-design.md §5.1 端点3/4/5). No TTL.
	PutSession(ctx context.Context, s DesktopSession) error
	GetSession(ctx context.Context, id string) (DesktopSession, error)
	DeleteSession(ctx context.Context, id string) error

	// broker: janitor — delete expired login_ctx/handoff rows. Returns the
	// number of rows removed. Run every 60s from main.go (decision 4).
	DeleteExpiredBrokerStates(ctx context.Context) (int64, error)
}

// pgStore is the concrete PostgreSQL implementation of Store.
type pgStore struct {
	db *sql.DB
	// refreshSealer encrypts/decrypts desktop_sessions.refresh_token_enc at rest
	// (AES-256-GCM, LUMEN_REFRESH_ENC_KEY). It is nil when the store is built
	// without broker support (e.g. tests that never touch desktop sessions);
	// desktop-session methods then return ErrNoSealer.
	refreshSealer *secure.Sealer
}

// ErrNoSealer is returned by desktop-session methods when the store was built
// without a refresh-token sealer (see NewWithSealer).
var ErrNoSealer = errors.New("store: refresh-token sealer not configured")

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

// New wraps an open *sql.DB as a Store without broker refresh-token support.
// Desktop-session methods will return ErrNoSealer; the other methods (including
// login_ctx / handoff broker states, which store no secrets at rest) work.
func New(db *sql.DB) Store {
	return &pgStore{db: db}
}

// NewWithSealer wraps an open *sql.DB as a Store that can encrypt/decrypt
// desktop_sessions.refresh_token_enc at rest. The sealer must wrap the
// 32-byte LUMEN_REFRESH_ENC_KEY (see config.Config.RefreshEncKey).
func NewWithSealer(db *sql.DB, refreshSealer *secure.Sealer) Store {
	return &pgStore{db: db, refreshSealer: refreshSealer}
}

// Close releases the underlying connection pool.
func (s *pgStore) Close() error {
	return s.db.Close()
}

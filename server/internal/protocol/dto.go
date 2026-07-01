// Package protocol holds the shared data-transfer objects and WebSocket
// envelope definitions exchanged between the Lumen server and clients.
//
// It is the lowest layer in the dependency graph: it depends only on the
// standard library and carries no business logic. Field names follow the
// authoritative contract (protocol-design.md §3.5 / §4): JSON uses
// snake_case, booleans are affirmative, and times are RFC3339 UTC strings.
package protocol

// User is the wire representation of a member (contract §3.5).
//
// is_owner is a computed field injected by the server from configuration
// (LUMEN_OWNER_SUBJECTS); it is never persisted. avatar_url may be an empty
// string when the identity provider supplies no picture.
type User struct {
	ID           string `json:"id"`
	OAuthSubject string `json:"oauth_subject"`
	DisplayName  string `json:"display_name"`
	AvatarURL    string `json:"avatar_url"`
	IsOwner      bool   `json:"is_owner"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// Channel is the wire representation of a text or voice channel (contract §3.5).
type Channel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // "text" | "voice"
	Position  int    `json:"position"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Message is the wire representation of a text message (contract §3.5).
//
// The id is a monotonic ULID (strictly increasing) that doubles as the
// pagination cursor. Author is an optional inlined snapshot for convenient
// rendering; the authoritative reference is AuthorID.
type Message struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	AuthorID  string `json:"author_id"`
	Author    *User  `json:"author,omitempty"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// VoiceState is the in-memory presence of a member inside a voice room
// (contract §3.5). It is never persisted and is cleared on process restart.
type VoiceState struct {
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	Muted     bool   `json:"muted"`
	Deafened  bool   `json:"deafened"`
	Speaking  bool   `json:"speaking"`
}

// Package storefake is an in-memory store.Store implementation for tests. It
// mirrors the real store's contract closely enough to exercise the REST and
// signaling layers without a database.
package storefake

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"lumen/internal/store"
)

// Fake is an in-memory Store. Safe for concurrent use.
type Fake struct {
	mu       sync.Mutex
	users    map[string]store.User // by id
	bySub    map[string]string     // subject -> id
	channels map[string]store.Channel
	messages []store.Message
	seq      int

	// Broker state (account center / desktop). login_ctx and handoff share one
	// map keyed by token, with a per-row absolute expiry mirroring the DB
	// expires_at guard; desktop sessions have no expiry.
	broker   map[string]brokerRow
	sessions map[string]store.DesktopSession

	// UpsertHook, when set, is invoked at the start of UpsertUser (to simulate
	// errors or observe calls).
	UpsertHook func(subject string) error
}

// brokerRow is an in-memory broker_states row.
type brokerRow struct {
	kind      string
	payload   []byte // JSON, matching the JSONB column
	expiresAt time.Time
}

// New builds an empty Fake.
func New() *Fake {
	return &Fake{
		users:    make(map[string]store.User),
		bySub:    make(map[string]string),
		channels: make(map[string]store.Channel),
		broker:   make(map[string]brokerRow),
		sessions: make(map[string]store.DesktopSession),
	}
}

func (f *Fake) nextID(prefix string) string {
	f.seq++
	return prefix + itoa(f.seq)
}

// Migrate is a no-op for the fake.
func (f *Fake) Migrate(context.Context) error { return nil }

// SeedDefaultChannels inserts a text and a voice channel if empty.
func (f *Fake) SeedDefaultChannels(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.channels) > 0 {
		return nil
	}
	now := time.Now().UTC()
	f.channels["seed-text"] = store.Channel{ID: "seed-text", Name: "大厅", Type: "text", Position: 0, CreatedAt: now, UpdatedAt: now}
	f.channels["seed-voice"] = store.Channel{ID: "seed-voice", Name: "开黑1", Type: "voice", Position: 1, CreatedAt: now, UpdatedAt: now}
	return nil
}

// Close is a no-op for the fake.
func (f *Fake) Close() error { return nil }

// AddChannel is a test helper to insert a channel directly.
func (f *Fake) AddChannel(c store.Channel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channels[c.ID] = c
}

// AddUser is a test helper to insert a user directly.
func (f *Fake) AddUser(u store.User) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[u.ID] = u
	f.bySub[u.OAuthSubject] = u.ID
}

// UpsertUser inserts or updates by subject, reporting changed like the real one.
func (f *Fake) UpsertUser(_ context.Context, subject, displayName, avatarURL string) (store.User, bool, error) {
	if f.UpsertHook != nil {
		if err := f.UpsertHook(subject); err != nil {
			return store.User{}, false, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	if id, ok := f.bySub[subject]; ok {
		u := f.users[id]
		changed := u.DisplayName != displayName || u.AvatarURL != avatarURL
		if changed {
			u.DisplayName = displayName
			u.AvatarURL = avatarURL
			u.UpdatedAt = now
			f.users[id] = u
		}
		return u, changed, nil
	}
	id := f.nextID("user-")
	u := store.User{ID: id, OAuthSubject: subject, DisplayName: displayName, AvatarURL: avatarURL, CreatedAt: now, UpdatedAt: now}
	f.users[id] = u
	f.bySub[subject] = id
	return u, false, nil // first INSERT is never "changed"
}

// GetUserByID returns a user by id.
func (f *Fake) GetUserByID(_ context.Context, id string) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return u, nil
}

// GetUserBySubject returns a user by subject.
func (f *Fake) GetUserBySubject(_ context.Context, subject string) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.bySub[subject]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return f.users[id], nil
}

// ListUsers returns users ordered by display_name.
func (f *Fake) ListUsers(context.Context) ([]store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.User, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DisplayName == out[j].DisplayName {
			return out[i].ID < out[j].ID
		}
		return out[i].DisplayName < out[j].DisplayName
	})
	return out, nil
}

// SetKickedUntil sets the ban time.
func (f *Fake) SetKickedUntil(_ context.Context, userID string, until time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return store.ErrNotFound
	}
	t := until.UTC()
	u.KickedUntil = &t
	f.users[userID] = u
	return nil
}

// ListChannels returns channels ordered by (position, id), optionally filtered.
func (f *Fake) ListChannels(_ context.Context, typeFilter string) ([]store.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.Channel, 0, len(f.channels))
	for _, c := range f.channels {
		if typeFilter == "" || c.Type == typeFilter {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position == out[j].Position {
			return out[i].ID < out[j].ID
		}
		return out[i].Position < out[j].Position
	})
	return out, nil
}

// GetChannel returns a channel by id.
func (f *Fake) GetChannel(_ context.Context, id string) (store.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.channels[id]
	if !ok {
		return store.Channel{}, store.ErrNotFound
	}
	return c, nil
}

// CreateChannel inserts a channel, appending to the end when position is nil.
func (f *Fake) CreateChannel(_ context.Context, name, ctype string, position *int) (store.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pos := 0
	if position != nil {
		pos = *position
	} else {
		max := -1
		for _, c := range f.channels {
			if c.Position > max {
				max = c.Position
			}
		}
		pos = max + 1
	}
	now := time.Now().UTC()
	id := f.nextID("chan-")
	c := store.Channel{ID: id, Name: name, Type: ctype, Position: pos, CreatedAt: now, UpdatedAt: now}
	f.channels[id] = c
	return c, nil
}

// UpdateChannel updates name/position.
func (f *Fake) UpdateChannel(_ context.Context, id string, name *string, position *int) (store.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.channels[id]
	if !ok {
		return store.Channel{}, store.ErrNotFound
	}
	if name != nil {
		c.Name = *name
	}
	if position != nil {
		c.Position = *position
	}
	c.UpdatedAt = time.Now().UTC()
	f.channels[id] = c
	return c, nil
}

// DeleteChannel removes a channel and cascades its messages.
func (f *Fake) DeleteChannel(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.channels[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.channels, id)
	kept := f.messages[:0]
	for _, m := range f.messages {
		if m.ChannelID != id {
			kept = append(kept, m)
		}
	}
	f.messages = kept
	return nil
}

// InsertMessage appends a message with a monotonic id.
func (f *Fake) InsertMessage(_ context.Context, channelID, authorID, content string) (store.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	id := f.nextID("msg-")
	m := store.Message{ID: id, ChannelID: channelID, AuthorID: authorID, Content: content, CreatedAt: now}
	f.messages = append(f.messages, m)
	return m, nil
}

// ListMessages returns a page oldest→newest plus hasMore, honoring before.
func (f *Fake) ListMessages(_ context.Context, channelID, before string, limit int) ([]store.Message, bool, error) {
	if limit < 1 {
		limit = store.DefaultMessageLimit
	}
	if limit > store.MaxMessageLimit {
		limit = store.MaxMessageLimit
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	// Collect matching messages sorted by id descending.
	all := make([]store.Message, 0)
	for _, m := range f.messages {
		if m.ChannelID != channelID {
			continue
		}
		if before != "" && !(m.ID < before) {
			continue
		}
		all = append(all, m)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })

	hasMore := len(all) > limit
	if hasMore {
		all = all[:limit]
	}
	// reverse to ascending
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return all, hasMore, nil
}

// --- Broker: login context / handoff / desktop sessions ---

// PutLoginContext stages a login_ctx row with the ~600s TTL.
func (f *Fake) PutLoginContext(_ context.Context, oidcState string, lc store.LoginContext) error {
	return f.putBroker(oidcState, "login_ctx", lc, store.LoginContextTTL)
}

// TakeLoginContext one-time consumes a login_ctx row (expiry guarded).
func (f *Fake) TakeLoginContext(_ context.Context, oidcState string) (store.LoginContext, error) {
	var lc store.LoginContext
	if err := f.consumeBroker(oidcState, "login_ctx", &lc); err != nil {
		return store.LoginContext{}, err
	}
	return lc, nil
}

// PutHandoff stages a handoff row with the ~120s TTL.
func (f *Fake) PutHandoff(_ context.Context, handoffCode string, h store.Handoff) error {
	return f.putBroker(handoffCode, "handoff", h, store.HandoffTTL)
}

// ConsumeHandoff one-time consumes a handoff row (expiry guarded).
func (f *Fake) ConsumeHandoff(_ context.Context, handoffCode string) (store.Handoff, error) {
	var h store.Handoff
	if err := f.consumeBroker(handoffCode, "handoff", &h); err != nil {
		return store.Handoff{}, err
	}
	return h, nil
}

func (f *Fake) putBroker(key, kind string, payload any, ttl time.Duration) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broker[key] = brokerRow{kind: kind, payload: raw, expiresAt: time.Now().UTC().Add(ttl)}
	return nil
}

func (f *Fake) consumeBroker(key, kind string, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.broker[key]
	if !ok || row.kind != kind {
		return store.ErrNotFound
	}
	if !time.Now().UTC().Before(row.expiresAt) {
		delete(f.broker, key) // expired: consume-on-read cleanup
		return store.ErrNotFound
	}
	delete(f.broker, key) // one-time consume
	return json.Unmarshal(row.payload, out)
}

// DeleteExpiredBrokerStates drops expired broker rows, returning the count.
func (f *Fake) DeleteExpiredBrokerStates(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	var n int64
	for k, row := range f.broker {
		if !now.Before(row.expiresAt) {
			delete(f.broker, k)
			n++
		}
	}
	return n, nil
}

// PutSession stores a desktop session (refresh_token kept in-memory as-is; the
// fake does not exercise at-rest encryption).
func (f *Fake) PutSession(_ context.Context, s store.DesktopSession) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	f.sessions[s.ID] = s
	return nil
}

// GetSession returns a desktop session by id.
func (f *Fake) GetSession(_ context.Context, id string) (store.DesktopSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return store.DesktopSession{}, store.ErrNotFound
	}
	return s, nil
}

// DeleteSession removes a desktop session (missing is a no-op).
func (f *Fake) DeleteSession(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, id)
	return nil
}

// itoa is a tiny int→string helper avoiding strconv import.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b strings.Builder
	digits := make([]byte, 0, 12)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	if neg {
		b.WriteByte('-')
	}
	for i := len(digits) - 1; i >= 0; i-- {
		b.WriteByte(digits[i])
	}
	return b.String()
}

// Ensure Fake satisfies the Store interface.
var _ store.Store = (*Fake)(nil)

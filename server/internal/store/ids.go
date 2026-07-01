package store

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// The monotonic entropy source is not concurrency safe, so all ID generation
// is serialised with entMu (contract §5.5 / server-design §5.3.1). This
// guarantees strictly increasing ULIDs within a sequence, so messages.id
// doubles as a pagination cursor (lexicographic order == generation order).
var (
	entMu   sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// NewID generates a strictly monotonic ULID string (contract §5.5). On
// same-millisecond entropy overflow ulid.New returns an error; we bump the
// timestamp and retry so ordering is never violated.
func NewID() string {
	entMu.Lock()
	defer entMu.Unlock()
	ts := ulid.Timestamp(time.Now().UTC())
	for {
		id, err := ulid.New(ts, entropy)
		if err == nil {
			return id.String()
		}
		ts++
	}
}

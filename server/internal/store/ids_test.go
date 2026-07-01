package store

import (
	"sync"
	"testing"

	"github.com/oklog/ulid/v2"
)

func TestNewID_ValidULID(t *testing.T) {
	id := NewID()
	if _, err := ulid.ParseStrict(id); err != nil {
		t.Fatalf("NewID produced invalid ULID %q: %v", id, err)
	}
	if len(id) != 26 {
		t.Errorf("ULID length = %d, want 26", len(id))
	}
}

func TestNewID_StrictlyIncreasing(t *testing.T) {
	const n = 10000
	prev := NewID()
	for i := 0; i < n; i++ {
		cur := NewID()
		if cur <= prev {
			t.Fatalf("ULID not strictly increasing at %d: prev=%q cur=%q", i, prev, cur)
		}
		prev = cur
	}
}

func TestNewID_ConcurrentUnique(t *testing.T) {
	const goroutines = 20
	const perG = 500
	var (
		mu  sync.Mutex
		set = make(map[string]struct{}, goroutines*perG)
		wg  sync.WaitGroup
	)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			local := make([]string, 0, perG)
			for i := 0; i < perG; i++ {
				local = append(local, NewID())
			}
			mu.Lock()
			for _, id := range local {
				set[id] = struct{}{}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(set) != goroutines*perG {
		t.Errorf("got %d unique IDs, want %d (duplicates under concurrency)", len(set), goroutines*perG)
	}
}

func TestSeedChannelIDs_ValidULIDs(t *testing.T) {
	for _, id := range []string{seedTextChannelID, seedVoiceChannelID} {
		if _, err := ulid.ParseStrict(id); err != nil {
			t.Errorf("seed channel id %q is not a valid ULID: %v", id, err)
		}
	}
	if seedTextChannelID == seedVoiceChannelID {
		t.Error("seed channel IDs must be distinct")
	}
}

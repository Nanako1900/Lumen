package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// newIntegrationStore connects to the Postgres named by LUMEN_TEST_DATABASE_URL,
// migrates, and truncates the tables so each test starts clean. Tests skip when
// the env var is unset (no DB available).
func newIntegrationStore(t *testing.T) (Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("LUMEN_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LUMEN_TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st := New(db)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Clean slate: truncate in FK-safe order.
	if _, err := db.ExecContext(ctx, `TRUNCATE messages, channels, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return st, ctx
}

func TestIntegration_MigrateIdempotent(t *testing.T) {
	st, ctx := newIntegrationStore(t)
	// Running migrate again must not error.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestIntegration_SeedDefaultChannels(t *testing.T) {
	st, ctx := newIntegrationStore(t)

	if err := st.SeedDefaultChannels(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	channels, err := st.ListChannels(ctx, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("channels = %d, want 2 (大厅 + 开黑1)", len(channels))
	}
	if channels[0].Name != "大厅" || channels[0].Type != "text" {
		t.Errorf("channel[0] = %+v, want 大厅/text", channels[0])
	}
	if channels[1].Name != "开黑1" || channels[1].Type != "voice" {
		t.Errorf("channel[1] = %+v, want 开黑1/voice", channels[1])
	}

	// Seeding again is a no-op (idempotent).
	if err := st.SeedDefaultChannels(ctx); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	channels, _ = st.ListChannels(ctx, "")
	if len(channels) != 2 {
		t.Errorf("after re-seed channels = %d, want 2 (idempotent)", len(channels))
	}
}

func TestIntegration_SeedSkippedWhenNotEmpty(t *testing.T) {
	st, ctx := newIntegrationStore(t)
	// Pre-create a channel so the table is non-empty.
	if _, err := st.CreateChannel(ctx, "existing", "text", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.SeedDefaultChannels(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	channels, _ := st.ListChannels(ctx, "")
	if len(channels) != 1 {
		t.Errorf("channels = %d, want 1 (seed skipped when non-empty)", len(channels))
	}
}

func TestIntegration_UpsertUser_ChangedSemantics(t *testing.T) {
	st, ctx := newIntegrationStore(t)

	// First insert: never "changed".
	u1, changed, err := st.UpsertUser(ctx, "sub-1", "Alice", "https://cdn/a.png")
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	if changed {
		t.Error("first INSERT must report changed=false")
	}
	if u1.DisplayName != "Alice" {
		t.Errorf("display_name = %q, want Alice", u1.DisplayName)
	}

	// Same values: no-op, changed=false.
	_, changed, err = st.UpsertUser(ctx, "sub-1", "Alice", "https://cdn/a.png")
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if changed {
		t.Error("unchanged values must report changed=false")
	}

	// Changed display_name: changed=true.
	u3, changed, err := st.UpsertUser(ctx, "sub-1", "Alice2", "https://cdn/a.png")
	if err != nil {
		t.Fatalf("upsert 3: %v", err)
	}
	if !changed {
		t.Error("changed display_name must report changed=true")
	}
	if u3.ID != u1.ID {
		t.Errorf("upsert must keep same id: %q vs %q", u3.ID, u1.ID)
	}
	if u3.DisplayName != "Alice2" {
		t.Errorf("display_name = %q, want Alice2", u3.DisplayName)
	}

	// Changed avatar only: changed=true.
	_, changed, err = st.UpsertUser(ctx, "sub-1", "Alice2", "https://cdn/b.png")
	if err != nil {
		t.Fatalf("upsert 4: %v", err)
	}
	if !changed {
		t.Error("changed avatar must report changed=true")
	}
}

func TestIntegration_UserQueries(t *testing.T) {
	st, ctx := newIntegrationStore(t)
	ua, _, _ := st.UpsertUser(ctx, "sub-b", "Bob", "")
	_, _, _ = st.UpsertUser(ctx, "sub-a", "Alice", "")

	byID, err := st.GetUserByID(ctx, ua.ID)
	if err != nil || byID.OAuthSubject != "sub-b" {
		t.Errorf("GetUserByID = (%+v, %v)", byID, err)
	}
	bySub, err := st.GetUserBySubject(ctx, "sub-a")
	if err != nil || bySub.DisplayName != "Alice" {
		t.Errorf("GetUserBySubject = (%+v, %v)", bySub, err)
	}
	if _, err := st.GetUserByID(ctx, "missing"); err != ErrNotFound {
		t.Errorf("missing user err = %v, want ErrNotFound", err)
	}

	// ListUsers ordered by display_name.
	users, err := st.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 2 || users[0].DisplayName != "Alice" || users[1].DisplayName != "Bob" {
		t.Errorf("ListUsers order wrong: %+v", users)
	}
}

func TestIntegration_SetKickedUntil(t *testing.T) {
	st, ctx := newIntegrationStore(t)
	u, _, _ := st.UpsertUser(ctx, "sub-1", "A", "")
	until := time.Now().UTC().Add(time.Hour)
	if err := st.SetKickedUntil(ctx, u.ID, until); err != nil {
		t.Fatalf("set kicked: %v", err)
	}
	got, _ := st.GetUserByID(ctx, u.ID)
	if got.KickedUntil == nil {
		t.Fatal("kicked_until should be set")
	}
	if got.KickedUntil.Sub(until).Abs() > time.Second {
		t.Errorf("kicked_until = %v, want ~%v", got.KickedUntil, until)
	}
	if err := st.SetKickedUntil(ctx, "missing", until); err != ErrNotFound {
		t.Errorf("kick missing user err = %v, want ErrNotFound", err)
	}
}

func TestIntegration_ChannelCRUD_PositionAppend(t *testing.T) {
	st, ctx := newIntegrationStore(t)

	// nil position appends to the end (COALESCE(MAX(position),-1)+1).
	c0, err := st.CreateChannel(ctx, "first", "text", nil)
	if err != nil {
		t.Fatalf("create c0: %v", err)
	}
	if c0.Position != 0 {
		t.Errorf("c0.position = %d, want 0", c0.Position)
	}
	c1, _ := st.CreateChannel(ctx, "second", "voice", nil)
	if c1.Position != 1 {
		t.Errorf("c1.position = %d, want 1 (appended)", c1.Position)
	}

	// Explicit position honoured.
	pos := 5
	c2, _ := st.CreateChannel(ctx, "third", "text", &pos)
	if c2.Position != 5 {
		t.Errorf("c2.position = %d, want 5", c2.Position)
	}

	// Update name + position.
	newName := "renamed"
	newPos := 2
	upd, err := st.UpdateChannel(ctx, c0.ID, &newName, &newPos)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Name != "renamed" || upd.Position != 2 {
		t.Errorf("updated = %+v, want renamed/2", upd)
	}

	// Update missing -> ErrNotFound.
	if _, err := st.UpdateChannel(ctx, "missing", &newName, nil); err != ErrNotFound {
		t.Errorf("update missing err = %v, want ErrNotFound", err)
	}

	// Type filter.
	textChannels, _ := st.ListChannels(ctx, "text")
	for _, c := range textChannels {
		if c.Type != "text" {
			t.Errorf("type filter leaked %s", c.Type)
		}
	}

	// Delete + cascade.
	if err := st.DeleteChannel(ctx, c1.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetChannel(ctx, c1.ID); err != ErrNotFound {
		t.Errorf("deleted channel still found")
	}
	if err := st.DeleteChannel(ctx, "missing"); err != ErrNotFound {
		t.Errorf("delete missing err = %v, want ErrNotFound", err)
	}
}

func TestIntegration_MessagesCursorPagination(t *testing.T) {
	st, ctx := newIntegrationStore(t)
	ch, _ := st.CreateChannel(ctx, "general", "text", nil)
	u, _, _ := st.UpsertUser(ctx, "sub-1", "A", "")

	// Insert 5 messages; ids are strictly increasing.
	var ids []string
	for i := 0; i < 5; i++ {
		m, err := st.InsertMessage(ctx, ch.ID, u.ID, "msg")
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		ids = append(ids, m.ID)
	}
	// Strictly increasing.
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("message ids not strictly increasing: %v", ids)
		}
	}

	// First page limit=2: newest 2, ascending, hasMore true.
	page, hasMore, err := st.ListMessages(ctx, ch.ID, "", 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page) != 2 || !hasMore {
		t.Fatalf("page1 len=%d hasMore=%v, want 2/true", len(page), hasMore)
	}
	// Ascending within the page.
	if page[0].ID >= page[1].ID {
		t.Error("page not ascending")
	}
	// The two returned are the two newest (ids[3], ids[4]).
	if page[0].ID != ids[3] || page[1].ID != ids[4] {
		t.Errorf("page1 = [%s,%s], want [%s,%s]", page[0].ID, page[1].ID, ids[3], ids[4])
	}

	// Next page before=page[0].ID (ids[3]) -> ids[1], ids[2].
	page2, hasMore, err := st.ListMessages(ctx, ch.ID, page[0].ID, 2)
	if err != nil {
		t.Fatalf("list2: %v", err)
	}
	if len(page2) != 2 || !hasMore {
		t.Fatalf("page2 len=%d hasMore=%v, want 2/true", len(page2), hasMore)
	}
	if page2[0].ID != ids[1] || page2[1].ID != ids[2] {
		t.Errorf("page2 = [%s,%s], want [%s,%s]", page2[0].ID, page2[1].ID, ids[1], ids[2])
	}

	// Last page: before=ids[1] -> just ids[0], hasMore false.
	page3, hasMore, err := st.ListMessages(ctx, ch.ID, ids[1], 2)
	if err != nil {
		t.Fatalf("list3: %v", err)
	}
	if len(page3) != 1 || hasMore {
		t.Fatalf("page3 len=%d hasMore=%v, want 1/false", len(page3), hasMore)
	}
	if page3[0].ID != ids[0] {
		t.Errorf("page3 = [%s], want [%s]", page3[0].ID, ids[0])
	}

	// Limit clamping.
	all, _, _ := st.ListMessages(ctx, ch.ID, "", 1000)
	if len(all) != 5 {
		t.Errorf("clamped-large-limit returned %d, want all 5", len(all))
	}
}

func TestIntegration_MessagesCascadeAndFKRestrict(t *testing.T) {
	st, ctx := newIntegrationStore(t)
	ch, _ := st.CreateChannel(ctx, "general", "text", nil)
	u, _, _ := st.UpsertUser(ctx, "sub-1", "A", "")
	if _, err := st.InsertMessage(ctx, ch.ID, u.ID, "hi"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// FK RESTRICT on author: cannot delete a user that still has messages.
	deleteUserErr := deleteUserRaw(t, st, u.ID)
	if deleteUserErr == nil {
		t.Error("deleting a user with messages should fail (ON DELETE RESTRICT)")
	}

	// Deleting the channel cascades its messages.
	if err := st.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	msgs, _, _ := st.ListMessages(ctx, ch.ID, "", 50)
	if len(msgs) != 0 {
		t.Errorf("messages should cascade-delete with channel, got %d", len(msgs))
	}
}

// deleteUserRaw attempts a raw user delete to exercise the FK RESTRICT path.
func deleteUserRaw(t *testing.T, st Store, userID string) error {
	t.Helper()
	pg, ok := st.(*pgStore)
	if !ok {
		t.Fatal("expected *pgStore")
	}
	_, err := pg.db.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, userID)
	return err
}

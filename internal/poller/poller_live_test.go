package poller

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ricardo-duarte-av/vinted-notifier/internal/store"
	"github.com/ricardo-duarte-av/vinted-notifier/internal/vinted"
)

// recorder captures what the poller would post to the room.
type recorder struct {
	events []store.Event
	alerts []string
}

func (r *recorder) Notify(_ context.Context, _ store.Search, ev []store.Event) {
	r.events = append(r.events, ev...)
}

func (r *recorder) Alert(_ context.Context, text string) { r.alerts = append(r.alerts, text) }

// TestLivePollNotifies runs the real poll path against Vinted: it seeds a
// search, forgets a few stored items so they read as new, polls again and
// checks those come back as events enriched from their item pages.
//
//	go test ./internal/poller -run TestLive -v
//	VINTED_DOMAIN=www.vinted.pt go test ./internal/poller -run TestLive -v
func TestLivePollNotifies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live network test in -short mode")
	}
	domain := os.Getenv("VINTED_DOMAIN")
	if domain == "" {
		domain = "www.vinted.fr"
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	id, err := st.AddSearch(vinted.SearchParams{Query: "iphone"}, "@test:example.org")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	rec := &recorder{}
	p := New(st, vinted.NewClient(domain), rec, time.Minute, 0.1, 1)

	s, _, _ := st.GetSearch(id)
	if err := p.pollOne(ctx, s); err != nil {
		t.Fatalf("seed poll: %v", err)
	}
	if len(rec.events) != 0 {
		t.Fatalf("seeding emitted %d event(s)", len(rec.events))
	}

	// Forget the newest two items (Vinted ids grow over time) through a
	// second connection, so the next poll treats them as new.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM seen_items WHERE search_id = ? AND item_id IN
		(SELECT item_id FROM seen_items WHERE search_id = ? ORDER BY item_id DESC LIMIT 2)`, id, id); err != nil {
		t.Fatal(err)
	}
	s, _, _ = st.GetSearch(id)
	if err := p.pollOne(ctx, s); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if len(rec.events) < 2 {
		t.Fatalf("expected >= 2 new events, got %d", len(rec.events))
	}
	for _, e := range rec.events {
		t.Logf("%s — %s — %s — %d photo(s) — desc %d chars",
			e.Item.Title, e.Item.PriceLabel(), e.Item.Details(), len(e.Item.Photos), len(e.Item.Description))
		if len(e.Item.Photos) == 0 || e.Item.Photos[0].Width == 0 {
			t.Errorf("item %d not enriched with gallery photos", e.Item.ID)
		}
	}
}

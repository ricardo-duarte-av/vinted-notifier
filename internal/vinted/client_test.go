package vinted

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastClient is a Client pointed at srv with the pacing and backoff delays
// shrunk so the tests stay offline and quick.
func fastClient(srv *httptest.Server) *Client {
	c := NewClient("unused")
	c.base = srv.URL
	c.minGap = 5 * time.Millisecond
	c.backoffBase = 5 * time.Millisecond
	return c
}

// TestRetriesThrottled checks a 403 is retried and eventually succeeds.
func TestRetriesThrottled(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) <= 2 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	body, err := fastClient(srv).get(context.Background(), srv.URL)
	if err != nil || string(body) != "ok" {
		t.Fatalf("get = %q, %v", body, err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("hits = %d, want 3", got)
	}
}

// TestGivesUpAfterMaxAttempts checks a permanently throttled endpoint stops.
func TestGivesUpAfterMaxAttempts(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if _, err := fastClient(srv).get(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&hits); got != maxAttempts {
		t.Fatalf("hits = %d, want %d", got, maxAttempts)
	}
}

// TestNoRetryOnClientError checks a 404 fails fast instead of burning retries.
func TestNoRetryOnClientError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := fastClient(srv).get(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("hits = %d, want 1", got)
	}
}

// TestDetectsBotProtection checks a Cloudflare challenge is reported as an
// edge block rather than a site error.
func TestDetectsBotProtection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("<title>Just a moment...</title>"))
	}))
	defer srv.Close()

	_, err := fastClient(srv).get(context.Background(), srv.URL)
	var se *StatusError
	if !errors.As(err, &se) || !se.EdgeBlocked() || se.Blocker != "Cloudflare" {
		t.Fatalf("err = %v, want Cloudflare edge block", err)
	}
	if se.Hint() == "" {
		t.Error("edge block should carry a hint")
	}
}

// TestSearchPaginates serves the catalog fixture and checks the query string,
// pagination stop and dedup across pages.
func TestSearchPaginates(t *testing.T) {
	page := fixture(t, "catalog.html")
	var hits int32
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		lastQuery = r.URL.RawQuery
		w.Write(page) // same items every page: all duplicates after page 1
	}))
	defer srv.Close()

	min, max, cat := 100, 300, 3661
	items, err := fastClient(srv).Search(context.Background(),
		SearchParams{Query: "iphone 13", MinPrice: &min, MaxPrice: &max, CatalogID: &cat}, 3)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(items) != 4 {
		t.Errorf("items = %d, want 4 (deduped)", len(items))
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("hits = %d, want 3 (maxPages)", got)
	}
	for _, want := range []string{"search_text=iphone+13", "price_from=100", "price_to=300",
		"catalog%5B%5D=3661", "order=newest_first", "page=3"} {
		if !strings.Contains(lastQuery, want) {
			t.Errorf("query %q missing %q", lastQuery, want)
		}
	}
}

func TestFetchDetails(t *testing.T) {
	page := fixture(t, "item.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/items/10340430106" {
			http.NotFound(w, r)
			return
		}
		w.Write(page)
	}))
	defer srv.Close()

	it := Item{ID: 10340430106, Photos: []Photo{{URL: "thumb"}}}
	if err := fastClient(srv).FetchDetails(context.Background(), &it); err != nil {
		t.Fatalf("details: %v", err)
	}
	if it.Description == "" || len(it.Photos) != 4 {
		t.Errorf("item = %+v", it)
	}
}

// TestLiveSearch hits the real site. Run explicitly with:
//
//	go test ./internal/vinted -run TestLive -v
//	VINTED_DOMAIN=www.vinted.pt go test ./internal/vinted -run TestLive -v
//
// Skipped in -short mode so normal test runs stay offline.
func TestLiveSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live network test in -short mode")
	}
	domain := os.Getenv("VINTED_DOMAIN")
	if domain == "" {
		domain = "www.vinted.fr"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c := NewClient(domain)
	min, max := 100, 300
	items, err := c.Search(ctx, SearchParams{Query: "iphone", MinPrice: &min, MaxPrice: &max}, 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(items) < 100 {
		t.Fatalf("expected two pages of items, got %d", len(items))
	}
	for _, it := range items {
		p, ok := it.PriceCents()
		if !ok {
			t.Errorf("item %d has no price", it.ID)
			continue
		}
		if p < min*100 || p > max*100 {
			t.Errorf("item %d price %s outside filter", it.ID, it.PriceLabel())
		}
	}

	it := items[0]
	if err := c.FetchDetails(ctx, &it); err != nil {
		t.Fatalf("details: %v", err)
	}
	t.Logf("%d items; first: %s — %s — %s — %d photo(s) — %q",
		len(items), it.Title, it.PriceLabel(), it.Details(), len(it.Photos), it.CleanDescription())

	cats, err := c.FetchCategories(ctx)
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	t.Logf("%d categories, top-level: %d", len(cats), len(TopLevelCategories(cats)))
}

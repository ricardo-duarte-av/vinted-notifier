package vinted

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

	// perPage is the catalog page size; 96 is the most the site serves.
	perPage = 96

	// minRequestGap is the smallest spacing between two Vinted requests. All
	// requests (poller and chat commands) funnel through one pacer so polling
	// many searches never reads as a burst.
	minRequestGap = 1500 * time.Millisecond

	// maxAttempts counts the first try plus retries for a throttled request.
	maxAttempts = 4

	// backoffBase is the first retry delay; it doubles per attempt.
	backoffBase = 2 * time.Second

	// maxPageBytes caps a page download; catalog pages are ~4 MiB uncompressed.
	maxPageBytes = 16 << 20
)

// StatusError is returned when Vinted answers with a non-200 status. It notes
// whether bot protection (Cloudflare or DataDome) answered rather than the
// site itself, because both surface as a bare HTTP 403.
type StatusError struct {
	Code   int
	URL    string
	Server string // "cloudflare" for everything Vinted serves
	CFRay  string // Cloudflare request id, useful when asking for support

	// Blocker names the bot protection that rejected the request ("Cloudflare"
	// or "DataDome"), or "" when the response came from Vinted itself.
	Blocker string

	retryAfter time.Duration // from the Retry-After header, if any
}

// EdgeBlocked reports whether bot protection rejected the request, so it is
// about this host (IP reputation, request rate) rather than one search.
func (e *StatusError) EdgeBlocked() bool { return e.Blocker != "" }

func (e *StatusError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "vinted returned HTTP %d", e.Code)
	if e.EdgeBlocked() {
		fmt.Fprintf(&b, ": blocked by %s bot protection — not about this search's query or filters", e.Blocker)
	}
	var meta []string
	if e.CFRay != "" {
		meta = append(meta, "cf-ray="+e.CFRay)
	}
	if e.retryAfter > 0 {
		meta = append(meta, "retry-after="+e.retryAfter.String())
	}
	if len(meta) > 0 {
		fmt.Fprintf(&b, " [%s]", strings.Join(meta, " "))
	}
	return b.String()
}

// Hint returns a one-line operator hint for a bot-protection block, or "" when
// the error needs no explaining.
func (e *StatusError) Hint() string {
	if !e.EdgeBlocked() {
		return ""
	}
	return "likely cause: this host's source IP (reputation, e.g. a datacenter range) or the request rate; " +
		"try a longer poll.interval_seconds, fewer searches or pages, or check whether the same page loads from another network"
}

// Throttled reports whether the status looks like rate limiting or bot
// detection rather than a permanent problem with the request itself.
func (e *StatusError) Throttled() bool {
	return e.Code == http.StatusForbidden || e.Code == http.StatusTooManyRequests || e.Code >= 500
}

// detectBlocker identifies a bot-protection response from its headers and the
// start of its body.
func detectBlocker(code int, h http.Header, body []byte) string {
	switch {
	case h.Get("X-Datadome") != "" || bytes.Contains(body, []byte("captcha-delivery.com")):
		return "DataDome"
	case h.Get("Cf-Mitigated") != "":
		return "Cloudflare"
	case code == http.StatusForbidden || code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable:
		if bytes.Contains(body, []byte("Just a moment")) || bytes.Contains(body, []byte("Attention Required")) ||
			bytes.Contains(body, []byte("challenge-platform")) {
			return "Cloudflare"
		}
	}
	return ""
}

// Client fetches Vinted catalog and item pages for one site.
type Client struct {
	http *http.Client
	base string // e.g. "https://www.vinted.fr"

	// Tunables, overridden in tests to keep them fast.
	minGap      time.Duration
	backoffBase time.Duration

	mu   sync.Mutex
	last time.Time // when the previous request was issued
}

// NewClient returns a Client for the given site host, e.g. "www.vinted.fr".
func NewClient(domain string) *Client {
	return &Client{
		http:        &http.Client{Timeout: 45 * time.Second},
		base:        "https://" + domain,
		minGap:      minRequestGap,
		backoffBase: backoffBase,
	}
}

// Base returns the site origin, e.g. "https://www.vinted.fr".
func (c *Client) Base() string { return c.base }

// Search fetches the newest listings matching params, paginating up to
// maxPages and deduplicating by item ID. Results come back newest-first.
func (c *Client) Search(ctx context.Context, p SearchParams, maxPages int) ([]Item, error) {
	if maxPages < 1 {
		maxPages = 1
	}

	seen := make(map[int64]struct{})
	var out []Item

	for page := 1; page <= maxPages; page++ {
		res, err := c.searchPage(ctx, p, page)
		if err != nil {
			// Pages already collected are still usable; a mid-pagination
			// failure shouldn't discard them and re-notify later. Say so
			// though, so a partial result is never mistaken for a full one.
			if len(out) > 0 {
				log.Printf("vinted: page %d of %q failed, continuing with %d item(s) from %d page(s): %v",
					page, p.Query, len(out), page-1, err)
				return out, nil
			}
			return nil, err
		}

		for _, it := range res.Items {
			if _, dup := seen[it.ID]; dup {
				continue
			}
			seen[it.ID] = struct{}{}
			out = append(out, it)
		}

		if len(res.Items) == 0 || page >= res.Pagination.TotalPages {
			break
		}
	}

	return out, nil
}

func (c *Client) searchPage(ctx context.Context, p SearchParams, page int) (catalogResult, error) {
	body, err := c.get(ctx, c.catalogURL(p, page))
	if err != nil {
		return catalogResult{}, err
	}
	return parseCatalog(body, c.base)
}

// FetchDetails fetches an item's page and fills in its description and full
// photo gallery. On error the item is left as it was.
func (c *Client) FetchDetails(ctx context.Context, it *Item) error {
	body, err := c.get(ctx, itemURL(c.base, it.ID))
	if err != nil {
		return err
	}
	d, err := parseItemPage(body)
	if err != nil {
		return err
	}
	if d.Description != "" {
		it.Description = d.Description
	}
	if len(d.Photos) > 0 {
		it.Photos = d.Photos
	}
	return nil
}

// FetchSeller fetches a seller's profile page for their location.
func (c *Client) FetchSeller(ctx context.Context, id int64) (Seller, error) {
	body, err := c.get(ctx, c.base+"/member/"+strconv.FormatInt(id, 10))
	if err != nil {
		return Seller{}, err
	}
	return parseMemberPage(body, id)
}

// catalogURL builds a newest-first catalog search URL.
func (c *Client) catalogURL(p SearchParams, page int) string {
	q := url.Values{}
	q.Set("order", "newest_first")
	q.Set("per_page", strconv.Itoa(perPage))
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if p.Query != "" {
		q.Set("search_text", p.Query)
	}
	if p.MinPrice != nil {
		q.Set("price_from", strconv.Itoa(*p.MinPrice))
	}
	if p.MaxPrice != nil {
		q.Set("price_to", strconv.Itoa(*p.MaxPrice))
	}
	if p.CatalogID != nil {
		q.Set("catalog[]", strconv.Itoa(*p.CatalogID))
	}
	return c.base + "/catalog?" + q.Encode()
}

// pace blocks until at least minGap has passed since the previous request, so
// concurrent callers (poller plus chat commands) share one budget.
func (c *Client) pace(ctx context.Context) error {
	c.mu.Lock()
	wait := time.Until(c.last.Add(c.minGap))
	if wait < 0 {
		wait = 0
	}
	c.last = time.Now().Add(wait)
	c.mu.Unlock()
	return sleepCtx(ctx, wait)
}

// get fetches a page, pacing every attempt and retrying throttled responses
// with backoff.
func (c *Client) get(ctx context.Context, reqURL string) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, c.backoff(attempt, lastErr)); err != nil {
				return nil, err
			}
		}
		if err := c.pace(ctx); err != nil {
			return nil, err
		}

		body, err := c.doGet(ctx, reqURL)
		if err == nil {
			if attempt > 0 {
				log.Printf("vinted: recovered after %d attempt(s); previous error: %v", attempt+1, lastErr)
			}
			return body, nil
		}
		lastErr = err

		var se *StatusError
		if !errors.As(err, &se) || !se.Throttled() {
			return nil, err
		}
	}

	return nil, fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}

func (c *Client) doGet(ctx context.Context, reqURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	setBrowserHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vinted request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// A slice of the error page is enough to recognise a challenge, and
		// reading it lets the connection be reused.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, &StatusError{
			Code:       resp.StatusCode,
			URL:        reqURL,
			Server:     resp.Header.Get("Server"),
			CFRay:      resp.Header.Get("Cf-Ray"),
			Blocker:    detectBlocker(resp.StatusCode, resp.Header, body),
			retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return nil, fmt.Errorf("read vinted page: %w", err)
	}
	return body, nil
}

// setBrowserHeaders makes the request look like a browser navigation. Accept-
// Encoding is left to net/http, which then transparently decompresses gzip.
func setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en;q=0.9,*;q=0.5")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
}

// backoff returns how long to wait before the given attempt (1-based retry
// number), honouring Retry-After when Vinted sent one.
func (c *Client) backoff(attempt int, err error) time.Duration {
	var se *StatusError
	if errors.As(err, &se) && se.retryAfter > 0 {
		return se.retryAfter
	}
	d := time.Duration(1<<uint(attempt-1)) * c.backoffBase // 1x, 2x, 4x
	return d + time.Duration(rand.Int63n(int64(d/4+1)))
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

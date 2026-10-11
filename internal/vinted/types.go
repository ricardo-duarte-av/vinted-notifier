// Package vinted is a small client for Vinted's public catalog pages.
//
// Vinted's JSON API (/api/v2/catalog/items) is no longer reachable
// anonymously, but the server-rendered catalog and item pages embed the same
// data in their Next.js payload, which this package extracts.
package vinted

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// SearchParams describes a single Vinted search: a free-text query plus
// optional price bounds (whole currency units) and a catalog (category)
// filter. Nil pointers mean "no filter".
type SearchParams struct {
	Query     string
	MinPrice  *int
	MaxPrice  *int
	CatalogID *int
}

// Item is the subset of a Vinted listing we care about. Catalog results carry
// only the main photo and no description; FetchDetails fills those in.
type Item struct {
	ID        int64
	Title     string
	URL       string // absolute
	Price     Money
	Brand     string // e.g. "Apple iPhone 12" (the catalog card's first line)
	Condition string // e.g. "Très bon état" (localised to the site)
	Promoted  bool
	Photos    []Photo

	Description string
}

// Money is a price as Vinted serves it: a decimal string plus ISO currency.
type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currencyCode"`
}

// Photo is one listing image. Width/Height are 0 when unknown (catalog cards).
type Photo struct {
	URL    string
	Width  int
	Height int
}

// PriceCents returns the item price in cents and whether one was found.
func (it Item) PriceCents() (int, bool) {
	if it.Price.Amount == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(it.Price.Amount, 64)
	if err != nil {
		return 0, false
	}
	return int(math.Round(f * 100)), true
}

// PriceLabel returns the human-readable price, e.g. "12.50 €".
func (it Item) PriceLabel() string {
	cents, ok := it.PriceCents()
	if !ok {
		return ""
	}
	return FormatPrice(cents, it.Price.Currency)
}

// Details returns the "brand · condition" line shown under the title,
// omitting any missing part.
func (it Item) Details() string {
	var parts []string
	for _, s := range []string{it.Brand, it.Condition} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

var currencySymbols = map[string]string{
	"EUR": "€", "GBP": "£", "USD": "$", "PLN": "zł", "CZK": "Kč",
	"SEK": "kr", "DKK": "kr", "HUF": "Ft", "RON": "lei", "AUD": "A$",
}

// FormatPrice renders cents as e.g. "300 €" or "12.50 €", dropping zero cents.
func FormatPrice(cents int, currency string) string {
	sym := currencySymbols[currency]
	if sym == "" {
		sym = currency
	}
	if cents%100 == 0 {
		return fmt.Sprintf("%d %s", cents/100, sym)
	}
	return fmt.Sprintf("%d.%02d %s", cents/100, cents%100, sym)
}

var blankRe = regexp.MustCompile(`\n{3,}`)

// CleanDescription returns the description with trimmed lines and runs of
// blank lines collapsed. Vinted descriptions are already plain text.
func (it Item) CleanDescription() string {
	s := strings.ReplaceAll(it.Description, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	s = blankRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return strings.TrimSpace(s)
}

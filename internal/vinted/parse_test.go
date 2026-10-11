package vinted

import (
	"errors"
	"os"
	"strings"
	"testing"
)

const testBase = "https://www.vinted.fr"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The fixture splits the payload across two flight chunks, so this also checks
// that chunks are joined before searching.
func TestParseCatalog(t *testing.T) {
	res, err := parseCatalog(fixture(t, "catalog.html"), testBase)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Items) != 4 {
		t.Fatalf("items = %d, want 4", len(res.Items))
	}
	if res.Pagination.TotalPages != 10 || res.Pagination.PerPage != 96 {
		t.Errorf("pagination = %+v", res.Pagination)
	}

	it := res.Items[0]
	if it.ID != 10340499220 || it.Title != "Iphone 12 mauve 128gb" {
		t.Errorf("first item = %d %q", it.ID, it.Title)
	}
	if it.URL != testBase+"/items/10340499220-iphone-12-mauve-128gb" {
		t.Errorf("url = %q", it.URL)
	}
	if p, ok := it.PriceCents(); !ok || p != 10000 {
		t.Errorf("price = %d, %v; want 10000 cents", p, ok)
	}
	if it.PriceLabel() != "100 €" {
		t.Errorf("price label = %q", it.PriceLabel())
	}
	if it.Details() != "Apple iPhone 12 · Très bon état" {
		t.Errorf("details = %q", it.Details())
	}
	if len(it.Photos) != 1 || !strings.HasPrefix(it.Photos[0].URL, "https://images1.vinted.net/") {
		t.Errorf("photos = %+v", it.Photos)
	}
	if it.Promoted || !res.Items[3].Promoted {
		t.Errorf("promoted flags wrong: %v %v", it.Promoted, res.Items[3].Promoted)
	}
}

func TestParseCatalogEmpty(t *testing.T) {
	res, err := parseCatalog(fixture(t, "catalog_empty.html"), testBase)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Items) != 0 {
		t.Fatalf("items = %d, want 0", len(res.Items))
	}
}

func TestParseCatalogLayoutChange(t *testing.T) {
	_, err := parseCatalog([]byte("<html><body>nothing here</body></html>"), testBase)
	if !errors.Is(err, ErrLayout) {
		t.Fatalf("err = %v, want ErrLayout", err)
	}
}

func TestParseItemPage(t *testing.T) {
	d, err := parseItemPage(fixture(t, "item.html"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Description != "iphone 13 pro 128gb oro in ottime condizioni" {
		t.Errorf("description = %q", d.Description)
	}
	if len(d.Photos) != 4 {
		t.Fatalf("photos = %d, want 4", len(d.Photos))
	}
	if p := d.Photos[0]; p.Width != 1200 || p.Height != 1600 || !strings.Contains(p.URL, "/f800/") {
		t.Errorf("first photo = %+v", p)
	}
}

func TestParseCategories(t *testing.T) {
	cats, err := parseCategories(fixture(t, "catalog.html"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	top := TopLevelCategories(cats)
	if len(top) != 3 {
		t.Fatalf("top-level = %d, want 3: %+v", len(top), top)
	}
	women, ok := CategoryByID(cats, 1904)
	if !ok || women.Name != "Femmes" || women.Level != 1 {
		t.Fatalf("1904 = %+v, %v", women, ok)
	}
	kids := ChildCategories(cats, 1904)
	if len(kids) == 0 {
		t.Fatal("Femmes has no children")
	}
	for _, k := range kids {
		if k.Level != 2 || !strings.HasPrefix(k.Path, "Femmes › ") {
			t.Errorf("child %+v", k)
		}
	}
	if m := SearchCategories(cats, "vêtements", 5); len(m) == 0 || m[0].Name != "Vêtements" {
		t.Errorf("search vêtements = %+v", m)
	}
}

func TestFormatPrice(t *testing.T) {
	cases := []struct {
		cents int
		cur   string
		want  string
	}{
		{30000, "EUR", "300 €"},
		{1250, "EUR", "12.50 €"},
		{999, "GBP", "9.99 £"},
		{500, "XYZ", "5 XYZ"},
	}
	for _, c := range cases {
		if got := FormatPrice(c.cents, c.cur); got != c.want {
			t.Errorf("FormatPrice(%d, %s) = %q, want %q", c.cents, c.cur, got, c.want)
		}
	}
}

func TestCleanDescription(t *testing.T) {
	it := Item{Description: "  line one \r\n\r\n\r\n\r\n  line two  \n"}
	if got := it.CleanDescription(); got != "line one\n\nline two" {
		t.Errorf("got %q", got)
	}
}

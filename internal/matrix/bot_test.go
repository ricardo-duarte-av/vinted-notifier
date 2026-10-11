package matrix

import (
	"strings"
	"testing"

	"github.com/ricardo-duarte-av/vinted-notifier/internal/store"
	"github.com/ricardo-duarte-av/vinted-notifier/internal/vinted"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		err  bool
	}{
		{`!vinted add "iphone 13 pro" 100 200 5407`, []string{"!vinted", "add", "iphone 13 pro", "100", "200", "5407"}, false},
		{`!vinted add "bike" - - -`, []string{"!vinted", "add", "bike", "-", "-", "-"}, false},
		{`!vinted list`, []string{"!vinted", "list"}, false},
		{`!vinted add "empty" ""`, []string{"!vinted", "add", "empty", ""}, false},
		{`!vinted add "oops`, nil, true},
	}
	for _, c := range cases {
		got, err := tokenize(c.in)
		if c.err {
			if err == nil {
				t.Errorf("%q: expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: token %d got %q want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestOptInt(t *testing.T) {
	if p, err := optInt("-"); err != nil || p != nil {
		t.Errorf(`optInt("-") = %v, %v`, p, err)
	}
	if p, err := optInt(""); err != nil || p != nil {
		t.Errorf(`optInt("") = %v, %v`, p, err)
	}
	if p, err := optInt("150"); err != nil || p == nil || *p != 150 {
		t.Errorf(`optInt("150") = %v, %v`, p, err)
	}
	if _, err := optInt("abc"); err == nil {
		t.Error(`optInt("abc") expected error`)
	}
}

func TestIsCommand(t *testing.T) {
	for in, want := range map[string]bool{
		"!vinted":           true,
		"!vinted list":      true,
		"!vintedlist":       false,
		"!olx list":         false,
		"hello !vinted add": false,
	} {
		if got := isCommand(in); got != want {
			t.Errorf("isCommand(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFormatEvent(t *testing.T) {
	s := store.Search{ID: 3, Owner: "@alice:example.org"}
	it := vinted.Item{
		ID:          1,
		Title:       "iPhone 13 <Pro>",
		URL:         "https://www.vinted.fr/items/1-iphone",
		Price:       vinted.Money{Amount: "249.50", Currency: "EUR"},
		Brand:       "Apple iPhone 13",
		Condition:   "Très bon état",
		Description: "Comme neuf",
		Country:     "France",
		City:        "Lyon",
	}

	plain, htmlBody := formatEvent(s, store.Event{Type: store.EventNew, Item: it})
	wantHead := "🆕 [#3] iPhone 13 <Pro> — 249.50 € · Apple iPhone 13 · Très bon état · 📍 Lyon, France\nhttps://www.vinted.fr/items/1-iphone"
	if !strings.HasPrefix(plain, wantHead) {
		t.Errorf("plain head:\n%s\nwant prefix:\n%s", plain, wantHead)
	}
	if !strings.Contains(plain, "Comme neuf") || !strings.HasSuffix(plain, "🔔 @alice:example.org") {
		t.Errorf("plain body:\n%s", plain)
	}
	if !strings.Contains(htmlBody, "iPhone 13 &lt;Pro&gt;") {
		t.Errorf("html not escaped:\n%s", htmlBody)
	}

	old := 30000
	plain, _ = formatEvent(s, store.Event{Type: store.EventPriceChange, Item: it, OldPrice: &old})
	if !strings.HasPrefix(plain, "📉 [#3] iPhone 13 <Pro> — 300 € → 249.50 €") {
		t.Errorf("price change:\n%s", plain)
	}
}

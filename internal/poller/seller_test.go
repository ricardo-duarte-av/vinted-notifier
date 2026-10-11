package poller

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ricardo-duarte-av/vinted-notifier/internal/store"
	"github.com/ricardo-duarte-av/vinted-notifier/internal/vinted"
)

// The sellers are pre-cached, so no request is made (the client is nil).
func TestFilterBySeller(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, se := range []vinted.Seller{
		{ID: 1, CountryCode: "PT", Country: "Portugal", City: "Porto"},
		{ID: 2, CountryCode: "IT", Country: "Itália"},
	} {
		if err := st.PutSeller(se); err != nil {
			t.Fatal(err)
		}
	}
	events := func() []store.Event {
		return []store.Event{
			{Item: vinted.Item{ID: 10, SellerID: 1}},
			{Item: vinted.Item{ID: 11, SellerID: 2}},
		}
	}

	p := &Poller{store: st, countries: []string{"PT"}}
	got := p.filterBySeller(context.Background(), store.Search{}, events())
	if len(got) != 1 || got[0].Item.ID != 10 {
		t.Fatalf("PT filter kept %+v", got)
	}
	if got[0].Item.LocationLabel() != "Porto, Portugal" {
		t.Errorf("location = %q", got[0].Item.LocationLabel())
	}

	p.countries = nil // all countries
	got = p.filterBySeller(context.Background(), store.Search{}, events())
	if len(got) != 2 || got[1].Item.Country != "Itália" {
		t.Fatalf("no filter kept %+v", got)
	}

	// An unknown seller id fails the lookup; the event is kept, not lost.
	p.countries = []string{"PT"}
	got = p.filterBySeller(context.Background(), store.Search{}, []store.Event{{Item: vinted.Item{ID: 12}}})
	if len(got) != 1 {
		t.Fatalf("failed lookup dropped the event: %+v", got)
	}
}

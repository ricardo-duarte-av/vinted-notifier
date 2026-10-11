package vinted

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrLayout marks a page that loaded fine but no longer has the shape this
// package expects, i.e. Vinted changed its frontend.
var ErrLayout = errors.New("unexpected Vinted page layout")

// flightRe matches one Next.js flight-data chunk:
// self.__next_f.push([1,"<JSON-escaped string>"]).
var flightRe = regexp.MustCompile(`self\.__next_f\.push\(\[1,("(?:[^"\\]|\\.)*")\]\)`)

// flightPayload concatenates the page's Next.js flight chunks into the raw
// payload text, in which the page data appears as plain JSON fragments.
func flightPayload(page []byte) (string, error) {
	ms := flightRe.FindAllSubmatch(page, -1)
	if len(ms) == 0 {
		return "", fmt.Errorf("%w: no Next.js payload on page", ErrLayout)
	}
	var b strings.Builder
	for _, m := range ms {
		var s string
		if err := json.Unmarshal(m[1], &s); err != nil {
			return "", fmt.Errorf("%w: unquote payload chunk: %v", ErrLayout, err)
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

// decodeAt decodes the JSON value starting at payload[i:] into v, ignoring
// whatever follows it.
func decodeAt(payload string, i int, v any) error {
	return json.NewDecoder(strings.NewReader(payload[i:])).Decode(v)
}

// defined drops the "$undefined" (and other "$..." reference) placeholders
// Next.js serialises in place of missing values.
func defined(s string) string {
	if strings.HasPrefix(s, "$") {
		return ""
	}
	return s
}

// catalogItem mirrors one entry of the catalog page's item list.
type catalogItem struct {
	ID          int64 `json:"id"`
	ProductItem *struct {
		Title      string `json:"title"`
		URL        string `json:"url"`
		Price      *Money `json:"price"`
		IsPromoted bool   `json:"isPromoted"`
		Photos     []struct {
			URL    string `json:"url"`
			IsMain bool   `json:"isMain"`
		} `json:"photos"`
		ItemBox struct {
			FirstLine  string `json:"firstLine"`
			SecondLine string `json:"secondLine"`
		} `json:"itemBox"`
	} `json:"productItem"`
}

type pagination struct {
	CurrentPage  int `json:"current_page"`
	PerPage      int `json:"per_page"`
	TotalEntries int `json:"total_entries"`
	TotalPages   int `json:"total_pages"`
}

// catalogResult is one parsed catalog page.
type catalogResult struct {
	Items      []Item
	Pagination pagination
}

// itemsAnchor precedes the search results object on a catalog page.
const itemsAnchor = `"items":{"items":[`

// parseCatalog extracts the listings from a catalog (search) page. base is the
// site origin (e.g. "https://www.vinted.fr") used to make item URLs absolute.
func parseCatalog(page []byte, base string) (catalogResult, error) {
	payload, err := flightPayload(page)
	if err != nil {
		return catalogResult{}, err
	}
	i := strings.Index(payload, itemsAnchor)
	if i < 0 {
		return catalogResult{}, fmt.Errorf("%w: search results not found on catalog page", ErrLayout)
	}

	var raw struct {
		Items      []catalogItem `json:"items"`
		Pagination pagination    `json:"pagination"`
	}
	if err := decodeAt(payload, i+len(`"items":`), &raw); err != nil {
		return catalogResult{}, fmt.Errorf("%w: decode search results: %v", ErrLayout, err)
	}

	out := catalogResult{Pagination: raw.Pagination}
	for _, ci := range raw.Items {
		p := ci.ProductItem
		if p == nil || ci.ID == 0 {
			continue // not a listing (e.g. an ad slot)
		}
		it := Item{
			ID:        ci.ID,
			Title:     p.Title,
			URL:       absURL(base, p.URL),
			Brand:     defined(p.ItemBox.FirstLine),
			Condition: defined(p.ItemBox.SecondLine),
			Promoted:  p.IsPromoted,
		}
		if p.Price != nil {
			it.Price = *p.Price
		}
		for _, ph := range p.Photos {
			if ph.URL != "" {
				it.Photos = append(it.Photos, Photo{URL: ph.URL})
			}
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

// itemDetails is what an item page adds on top of a catalog card.
type itemDetails struct {
	Description string
	Photos      []Photo
}

// pluginAnchor starts each data block ("plugin") on an item page:
// {"data":{...},"exposures":[],"name":"description","section":...}.
const pluginAnchor = `{"data":`

// parseItemPage extracts the description and full photo gallery from an item
// page.
func parseItemPage(page []byte) (itemDetails, error) {
	payload, err := flightPayload(page)
	if err != nil {
		return itemDetails{}, err
	}

	var out itemDetails
	var haveDesc, haveGallery bool
	for off := 0; !(haveDesc && haveGallery); {
		i := strings.Index(payload[off:], pluginAnchor)
		if i < 0 {
			break
		}
		i += off
		off = i + len(pluginAnchor)

		var plugin struct {
			Name string          `json:"name"`
			Data json.RawMessage `json:"data"`
		}
		if decodeAt(payload, i, &plugin) != nil {
			continue
		}
		switch plugin.Name {
		case "description":
			var d struct {
				Description string `json:"description"`
			}
			if json.Unmarshal(plugin.Data, &d) == nil {
				out.Description = defined(d.Description)
				haveDesc = true
			}
		case "gallery":
			var g struct {
				Photos []struct {
					URL    string `json:"url"`
					Width  int    `json:"width"`
					Height int    `json:"height"`
				} `json:"photos"`
			}
			if json.Unmarshal(plugin.Data, &g) == nil {
				for _, p := range g.Photos {
					if p.URL != "" {
						out.Photos = append(out.Photos, Photo{URL: p.URL, Width: p.Width, Height: p.Height})
					}
				}
				haveGallery = true
			}
		}
	}

	if !haveDesc && !haveGallery {
		return itemDetails{}, fmt.Errorf("%w: no description or gallery on item page", ErrLayout)
	}
	return out, nil
}

func absURL(base, path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return base + path
}

// itemURL is the canonical item page URL; Vinted redirects /items/<id> to the
// slugged URL.
func itemURL(base string, id int64) string {
	return base + "/items/" + strconv.FormatInt(id, 10)
}

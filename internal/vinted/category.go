package vinted

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Category is a node in the Vinted catalog tree. Names are localised to the
// configured site (e.g. "Femmes" on vinted.fr, "Mulher" on vinted.pt).
type Category struct {
	ID       int
	Name     string
	ParentID int    // 0 for top-level sections
	Path     string // e.g. "Femmes › Vêtements › Manteaux et vestes"
	Level    int    // 1 for top-level sections
}

// catalogNode mirrors one node of the embedded catalogTree.
type catalogNode struct {
	ID       int           `json:"id"`
	Title    string        `json:"title"`
	Catalogs []catalogNode `json:"catalogs"`
}

const catalogTreeAnchor = `"catalogTree":`

// FetchCategories downloads and flattens the site's catalog tree, which every
// catalog page embeds.
func (c *Client) FetchCategories(ctx context.Context) ([]Category, error) {
	body, err := c.get(ctx, c.base+"/catalog")
	if err != nil {
		return nil, fmt.Errorf("fetch categories: %w", err)
	}
	return parseCategories(body)
}

func parseCategories(page []byte) ([]Category, error) {
	payload, err := flightPayload(page)
	if err != nil {
		return nil, err
	}
	i := strings.Index(payload, catalogTreeAnchor)
	if i < 0 {
		return nil, fmt.Errorf("%w: catalog tree not found on page", ErrLayout)
	}
	var roots []catalogNode
	if err := decodeAt(payload, i+len(catalogTreeAnchor), &roots); err != nil {
		return nil, fmt.Errorf("%w: decode catalog tree: %v", ErrLayout, err)
	}

	var out []Category
	var walk func(nodes []catalogNode, parent int, path string, level int)
	walk = func(nodes []catalogNode, parent int, path string, level int) {
		for _, n := range nodes {
			p := n.Title
			if path != "" {
				p = path + " › " + n.Title
			}
			out = append(out, Category{ID: n.ID, Name: n.Title, ParentID: parent, Path: p, Level: level})
			walk(n.Catalogs, n.ID, p, level+1)
		}
	}
	walk(roots, 0, "", topLevel)

	if len(out) == 0 {
		return nil, fmt.Errorf("%w: catalog tree empty", ErrLayout)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// topLevel is the tree depth of the root sections.
const topLevel = 1

// TopLevelCategories returns the root sections, sorted by name.
func TopLevelCategories(cats []Category) []Category {
	var out []Category
	for _, c := range cats {
		if c.Level == topLevel {
			out = append(out, c)
		}
	}
	sortByName(out)
	return out
}

// ChildCategories returns the direct children of parentID, sorted by name.
func ChildCategories(cats []Category, parentID int) []Category {
	var out []Category
	for _, c := range cats {
		if c.ParentID == parentID {
			out = append(out, c)
		}
	}
	sortByName(out)
	return out
}

// CategoryByID returns the category with the given id, if present.
func CategoryByID(cats []Category, id int) (Category, bool) {
	for _, c := range cats {
		if c.ID == id {
			return c, true
		}
	}
	return Category{}, false
}

func sortByName(cats []Category) {
	sort.SliceStable(cats, func(i, j int) bool { return cats[i].Name < cats[j].Name })
}

// SearchCategories returns categories matching term (case-insensitive), ranked
// by how closely the name matches, then path, capped at limit results.
func SearchCategories(cats []Category, term string, limit int) []Category {
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		return nil
	}

	type scored struct {
		cat   Category
		score int
	}
	var matches []scored
	for _, c := range cats {
		name := strings.ToLower(c.Name)
		path := strings.ToLower(c.Path)
		switch {
		case name == term:
			matches = append(matches, scored{c, 0})
		case strings.HasPrefix(name, term):
			matches = append(matches, scored{c, 1})
		case strings.Contains(name, term):
			matches = append(matches, scored{c, 2})
		case strings.Contains(path, term):
			matches = append(matches, scored{c, 3})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		if matches[i].cat.Level != matches[j].cat.Level {
			return matches[i].cat.Level < matches[j].cat.Level
		}
		return matches[i].cat.Name < matches[j].cat.Name
	})

	out := make([]Category, 0, limit)
	for _, m := range matches {
		if len(out) >= limit {
			break
		}
		out = append(out, m.cat)
	}
	return out
}

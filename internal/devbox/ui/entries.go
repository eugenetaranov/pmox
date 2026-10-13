package ui

import "github.com/eugenetaranov/pmox/internal/devbox/catalog"

// CatalogEntries are the picker rows of the catalog's visible items.
func CatalogEntries(c *catalog.Catalog) []*Entry {
	var out []*Entry
	for _, it := range c.Items {
		if it.Hidden() {
			continue
		}
		out = append(out, &Entry{Tab: it.Tab, ID: it.ID, Label: it.Label, Source: it.Describe()})
	}
	return out
}

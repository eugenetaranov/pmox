// Package plan turns picks into the ordered list of items to install:
// every pick, everything it needs (mise for mise items), in catalog order
// with each item after what it needs.
package plan

import (
	"fmt"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

// MiseID is the hidden item that installs mise itself.
const MiseID = "mise"

// Resolve returns picks plus everything they need, in install order. A
// pick that isn't in the catalog is an error. extraMise says whether
// extra mise tools are to be installed (which needs mise too).
func Resolve(c *catalog.Catalog, picks []string, extraMise bool) ([]*catalog.Item, error) {
	want := map[string]bool{}
	var add func(id string) error
	add = func(id string) error {
		if want[id] {
			return nil
		}
		it := c.Item(id)
		if it == nil {
			return fmt.Errorf("%q is not in the catalog", id)
		}
		want[id] = true
		for _, n := range it.Needs {
			if err := add(n); err != nil {
				return err
			}
		}
		if it.UsesMise() && c.Item(MiseID) != nil {
			return add(MiseID)
		}
		return nil
	}
	for _, p := range picks {
		if err := add(p); err != nil {
			return nil, err
		}
	}
	if extraMise && c.Item(MiseID) != nil {
		if err := add(MiseID); err != nil {
			return nil, err
		}
	}

	// Catalog order, except that an item goes after everything it needs
	// (the catalog lists tabs in picker order, not install order).
	var out []*catalog.Item
	placed := map[string]bool{}
	var place func(it *catalog.Item)
	place = func(it *catalog.Item) {
		if placed[it.ID] {
			return
		}
		placed[it.ID] = true
		for _, n := range it.Needs {
			place(c.Item(n))
		}
		for _, n := range it.After {
			if want[n] {
				place(c.Item(n))
			}
		}
		if it.UsesMise() && it.ID != MiseID && c.Item(MiseID) != nil {
			place(c.Item(MiseID))
		}
		out = append(out, it)
	}
	// Hidden helpers first when wanted (as the bash installer did), then
	// the rest in catalog order.
	for _, it := range c.Items {
		if want[it.ID] && it.Hidden() {
			place(it)
		}
	}
	for _, it := range c.Items {
		if want[it.ID] {
			place(it)
		}
	}
	return out, nil
}

// IDs returns the items' ids.
func IDs(items []*catalog.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

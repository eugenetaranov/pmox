// Package catalog is devbox-setup's catalog: the tabs and items the picker
// offers, each with the steps that install it. It is TOML data, embedded
// in the binary (data/*.toml), plus local additions from
// /etc/devbox-setup/catalog.d/*.toml.
package catalog

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed data/*.toml
var data embed.FS

// HiddenTab is the tab of helper items that other items pull in through
// needs but the picker doesn't show (mise, Ruby's build dependencies).
const HiddenTab = "-"

// LocalDir is where local catalog additions are read from.
const LocalDir = "/etc/devbox-setup/catalog.d"

// Tab is one picker tab.
type Tab struct {
	ID    string `toml:"id"`
	Title string `toml:"title"`
}

// Item is one thing the picker offers.
type Item struct {
	ID    string   `toml:"id"`
	Tab   string   `toml:"tab"`
	Label string   `toml:"label"`
	Needs []string `toml:"needs"`
	// After orders the item after these when they are installed too,
	// without pulling them in (registries after the GitHub login, whose
	// token ghcr.io can use).
	After []string `toml:"after"`
	// Source says how the item is installed, as the picker shows it
	// (e.g. "mise use -g terraform"). Optional: Describe derives one.
	Source string `toml:"source"`
	// Scope is "system" (machine-wide: its pick is kept in system.json)
	// or "user" (kept in the user's own answers). Default: system for the
	// System tab, else user.
	Scope string `toml:"scope"`
	Steps []Step `toml:"step"`

	// From is the file the item came from (for error messages).
	From string `toml:"-"`
}

// System reports whether the item is machine-wide (see Scope).
func (it *Item) System() bool {
	if it.Scope != "" {
		return it.Scope == "system"
	}
	return it.Tab == "system"
}

// Hidden reports whether the picker leaves the item out.
func (it *Item) Hidden() bool { return it.Tab == HiddenTab }

// Describe is the item's install source as the picker shows it.
func (it *Item) Describe() string {
	if it.Source != "" {
		return it.Source
	}
	var parts []string
	for _, s := range it.Steps {
		switch s.Type {
		case "apt":
			parts = append(parts, "apt install "+strings.Join(s.Packages, " "))
		case "mise":
			tools := make([]string, len(s.Tools))
			for i, t := range s.Tools {
				tools[i] = strings.TrimSuffix(t, "@latest")
			}
			parts = append(parts, "mise use -g "+strings.Join(tools, " "))
		}
	}
	return strings.Join(parts, " · ")
}

// UsesMise reports whether any of the item's steps install with mise.
func (it *Item) UsesMise() bool {
	for _, s := range it.Steps {
		if s.Type == "mise" {
			return true
		}
	}
	return false
}

// Catalog is the loaded, validated catalog.
type Catalog struct {
	Tabs  []Tab
	Items []*Item // file order: tabs.toml's tabs, then items as the files list them
	byID  map[string]*Item
	files fs.FS
}

// Item returns the item with id, or nil.
func (c *Catalog) Item(id string) *Item { return c.byID[id] }

// Files is the file tree `file` and `template` steps read from.
func (c *Catalog) Files() fs.FS { return c.files }

// TabItems returns the visible items of tab, in catalog order.
func (c *Catalog) TabItems(tab string) []*Item {
	var out []*Item
	for _, it := range c.Items {
		if it.Tab == tab {
			out = append(out, it)
		}
	}
	return out
}

type fileDoc struct {
	Tabs  []Tab   `toml:"tab"`
	Items []*Item `toml:"item"`
}

// Load reads the embedded catalog, then every *.toml in local (nil to skip),
// and validates the result. files is the tree `file`/`template` steps name
// their src in; nil skips that check.
func Load(local fs.FS, files fs.FS) (*Catalog, error) {
	c := &Catalog{byID: map[string]*Item{}, files: files}
	if err := c.addDir(data, "data", false); err != nil {
		return nil, err
	}
	if local != nil {
		if err := c.addDir(local, ".", true); err != nil {
			return nil, err
		}
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Catalog) addDir(fsys fs.FS, dir string, local bool) error {
	names, err := fs.Glob(fsys, path.Join(dir, "*.toml"))
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		label := name
		if local {
			label = path.Join(LocalDir, name)
		}
		if err := c.addFile(label, b, local); err != nil {
			return err
		}
	}
	return nil
}

func (c *Catalog) addFile(name string, b []byte, local bool) error {
	var doc fileDoc
	md, err := toml.NewDecoder(bytes.NewReader(b)).Decode(&doc)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		return fmt.Errorf("%s: unknown key %s", name, und[0])
	}
	if local && len(doc.Tabs) > 0 {
		return fmt.Errorf("%s: local catalog files can't add tabs", name)
	}
	c.Tabs = append(c.Tabs, doc.Tabs...)
	for _, it := range doc.Items {
		it.From = name
		if old, ok := c.byID[it.ID]; ok {
			if !local {
				return fmt.Errorf("%s: duplicate id %q (also in %s)", name, it.ID, old.From)
			}
			*old = *it // a local item replaces the embedded one, in place
			continue
		}
		c.byID[it.ID] = it
		c.Items = append(c.Items, it)
	}
	return nil
}

func (c *Catalog) validate() error {
	var errs []error
	fail := func(it *Item, format string, a ...any) {
		errs = append(errs, fmt.Errorf("%s: item %q: %s", it.From, it.ID, fmt.Sprintf(format, a...)))
	}
	tabs := map[string]bool{HiddenTab: true}
	for _, t := range c.Tabs {
		if t.ID == "" || t.Title == "" || tabs[t.ID] {
			errs = append(errs, fmt.Errorf("tab %q: needs a unique id and a title", t.ID))
		}
		tabs[t.ID] = true
	}
	for _, it := range c.Items {
		switch {
		case it.ID == "" || strings.ContainsAny(it.ID, " \t|"):
			fail(it, "bad id")
		case !tabs[it.Tab]:
			fail(it, "unknown tab %q", it.Tab)
		case it.Label == "" && !it.Hidden():
			fail(it, "no label")
		case len(it.Steps) == 0:
			fail(it, "no steps")
		case it.Scope != "" && it.Scope != "system" && it.Scope != "user":
			fail(it, "scope %q: want system or user", it.Scope)
		}
		for _, n := range it.Needs {
			if c.byID[n] == nil {
				fail(it, "needs %q, which is not an item", n)
			}
		}
		for _, n := range it.After {
			if c.byID[n] == nil {
				fail(it, "after %q, which is not an item", n)
			}
		}
		for i, s := range it.Steps {
			if err := s.validate(c.files); err != nil {
				fail(it, "step %d (%s): %v", i+1, s.Type, err)
			}
		}
	}
	if cyc := c.cycle(); cyc != nil {
		errs = append(errs, fmt.Errorf("needs/after cycle: %s", strings.Join(cyc, " → ")))
	}
	return errors.Join(errs...)
}

// cycle returns a needs cycle, or nil.
func (c *Catalog) cycle() []string {
	const (
		unseen = iota
		active
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(id string) []string
	visit = func(id string) []string {
		switch state[id] {
		case active:
			for i, s := range stack {
				if s == id {
					return append(append([]string{}, stack[i:]...), id)
				}
			}
		case done:
			return nil
		}
		state[id] = active
		stack = append(stack, id)
		if it := c.byID[id]; it != nil {
			for _, n := range append(append([]string{}, it.Needs...), it.After...) {
				if cyc := visit(n); cyc != nil {
					return cyc
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = done
		return nil
	}
	for _, it := range c.Items {
		if cyc := visit(it.ID); cyc != nil {
			return cyc
		}
	}
	return nil
}

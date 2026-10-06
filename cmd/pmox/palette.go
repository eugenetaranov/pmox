package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/tui/palette"
)

// The interactive command palette behind bare 'pmox' and bare nouns
// ('pmox vm'): menus are built from the cobra tree, so it always matches
// --help.

// paletteRunFn is a seam so tests never start a program.
var paletteRunFn = palette.Run

// sectionTitles names the root help groups and the vm subgroups.
var sectionTitles = map[string]string{
	groupStart:       "Get started",
	groupCommon:      "Common",
	groupResources:   "Resources",
	groupMaintenance: "Maintenance",
	"vm-lifecycle":   "Lifecycle",
	"vm-access":      "Access & files",
}

// paletteMenu lists cmd's runnable children in help order: grouped
// sections first (in the order the command declares its groups), then
// ungrouped commands. Hidden/deprecated commands and help/completion are
// left out.
func paletteMenu(cmd *cobra.Command) palette.Menu {
	order := []string{}
	for _, g := range cmd.Groups() {
		order = append(order, g.ID)
	}
	order = append(order, "")
	byGroup := map[string][]*cobra.Command{}
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		byGroup[c.GroupID] = append(byGroup[c.GroupID], c)
	}
	var m palette.Menu
	for _, g := range order {
		title := sectionTitles[g]
		if title == "" {
			title = "Commands"
		}
		for _, c := range byGroup[g] {
			desc := c.Short
			if i := strings.LastIndex(desc, "  ("); i > 0 && strings.HasSuffix(desc, ")") {
				desc = desc[:i] // the "(vm list)" suffix becomes the hint
			}
			var hints []string
			if canon := c.Annotations[canonicalAnnotation]; canon != "" {
				hints = append(hints, "pmox "+canon)
			}
			if len(c.Aliases) > 0 {
				hints = append(hints, "alias "+strings.Join(c.Aliases, ", "))
			}
			m.Items = append(m.Items, palette.Item{
				Key:     c.Name(),
				Desc:    desc,
				Hint:    strings.Join(hints, " · "),
				Section: title,
				Sub:     c.HasAvailableSubCommands(),
			})
		}
	}
	return m
}

// paletteResolver resolves a path to its menu under root.
func paletteResolver(root *cobra.Command) palette.Resolver {
	return func(path []string) palette.Menu {
		cmd := root
		if len(path) > 0 {
			if c, _, err := root.Find(path); err == nil {
				cmd = c
			}
		}
		return paletteMenu(cmd)
	}
}

// currentContextLabel names the context commands will target, for the
// palette header ("" when unknown or ambiguous).
func currentContextLabel() string {
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	contexts := cfg.Contexts()
	for _, c := range contexts {
		if c.Current {
			return c.Name
		}
	}
	if len(contexts) == 1 {
		return contexts[0].Name
	}
	return ""
}

// runPalette opens the palette at start under cmd's root and runs the
// chosen command as if typed with no further arguments.
func runPalette(cmd *cobra.Command, start []string) error {
	root := cmd.Root()
	path, err := paletteRunFn(start, paletteResolver(root), palette.Options{Title: "pmox", Context: currentContextLabel()})
	if err != nil {
		return err
	}
	root.SetArgs(path)
	return root.ExecuteContext(cmd.Context())
}

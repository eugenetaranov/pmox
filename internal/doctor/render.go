package doctor

import (
	"fmt"
	"io"
)

// groupOrder is the fixed print order for check groups; it mirrors the
// setup flow (config first, then connectivity, storage, SSH, template)
// with local tooling last. Unknown groups print after these in first-seen
// order.
var groupOrder = []string{"config", "api", "storage", "ssh", "template", "tooling"}

var groupTitle = map[string]string{
	"config":   "Config",
	"api":      "Proxmox API",
	"storage":  "Storage",
	"ssh":      "Node SSH",
	"template": "Template",
	"tooling":  "Local tooling",
}

const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiDim    = "\033[2m"
)

func symbol(s Status) string {
	switch s {
	case Pass:
		return "✓"
	case Warn:
		return "!"
	case Fail:
		return "✗"
	default:
		return "-"
	}
}

func colorFor(s Status) string {
	switch s {
	case Pass:
		return ansiGreen
	case Warn:
		return ansiYellow
	case Fail:
		return ansiRed
	default:
		return ansiDim
	}
}

// RenderText writes the human-readable report to w. When verbose is
// false, all-pass groups collapse to a single summary line and only
// warn/fail/info checks are expanded. color enables ANSI styling.
func RenderText(w io.Writer, r Report, verbose, color bool) {
	paint := func(s Status, text string) string {
		if !color {
			return text
		}
		return colorFor(s) + text + ansiReset
	}

	if r.Server != "" {
		src := r.ServerSource
		if src != "" {
			src = " (" + src + ")"
		}
		fmt.Fprintf(w, "Checking %s%s\n\n", r.Server, src)
	}

	byGroup := map[string][]Check{}
	var order []string
	seen := map[string]bool{}
	for _, c := range r.Checks {
		if !seen[c.Group] {
			seen[c.Group] = true
			order = append(order, c.Group)
		}
		byGroup[c.Group] = append(byGroup[c.Group], c)
	}

	// Print known groups first in the canonical order, then any extras.
	var printOrder []string
	for _, g := range groupOrder {
		if seen[g] {
			printOrder = append(printOrder, g)
		}
	}
	for _, g := range order {
		found := false
		for _, k := range groupOrder {
			if k == g {
				found = true
				break
			}
		}
		if !found {
			printOrder = append(printOrder, g)
		}
	}

	for _, g := range printOrder {
		checks := byGroup[g]
		title := groupTitle[g]
		if title == "" {
			title = g
		}

		allPass := true
		for _, c := range checks {
			if c.Status != Pass {
				allPass = false
				break
			}
		}

		if allPass && !verbose {
			fmt.Fprintf(w, "%s %s (%d/%d ok)\n", paint(Pass, symbol(Pass)), title, len(checks), len(checks))
			continue
		}

		fmt.Fprintf(w, "%s\n", title)
		for _, c := range checks {
			if c.Status == Pass && !verbose {
				continue
			}
			fmt.Fprintf(w, "  %s %s\n", paint(c.Status, symbol(c.Status)), c.Message)
			if c.Remediation != "" && (c.Status == Warn || c.Status == Fail) {
				fmt.Fprintf(w, "      %s %s\n", paint(Info, "→ fix:"), c.Remediation)
			}
		}
	}

	fmt.Fprintln(w)
	switch {
	case r.Ready && r.Summary.Warn == 0:
		fmt.Fprintln(w, paint(Pass, "✓ pmox is ready — try: pmox launch <name>"))
	case r.Ready:
		fmt.Fprintln(w, paint(Pass, fmt.Sprintf("✓ pmox is ready (%d warning(s)) — try: pmox launch <name>", r.Summary.Warn)))
	default:
		n := r.Summary.Fail
		suffix := "issue"
		if r.Strict && r.Summary.Fail == 0 {
			n = r.Summary.Warn
			suffix = "warning (--strict)"
		}
		if n != 1 {
			suffix += "s"
		}
		fmt.Fprintln(w, paint(Fail, fmt.Sprintf("✗ NOT READY — %d %s to fix (see above)", n, suffix)))
	}
}

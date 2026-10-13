package run

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/steps"
)

// DryRun prints what installing items would do, item by item, and changes
// nothing.
func DryRun(w io.Writer, e *steps.Env, items []*catalog.Item, extraMise []string) error {
	fmt.Fprintf(w, "For user: %s\n", e.User)
	for _, x := range build(items) {
		if x.err != nil {
			return fmt.Errorf("%s: %w", x.item.ID, x.err)
		}
		fmt.Fprintf(w, "%s\n", label(x.item))
		for _, st := range x.steps {
			for _, a := range st.Plan(e) {
				fmt.Fprintf(w, "  %-6s %s\n", a.Kind, a.Target)
			}
		}
	}
	if len(extraMise) > 0 {
		fmt.Fprintf(w, "Extra mise tools\n  %-6s %s\n", "mise", strings.Join(extraMise, " "))
	}
	return nil
}

// Status is one item's verify result.
type Status struct {
	Item *catalog.Item
	// State is "ok", "missing" or "unchecked" (an item still run by the
	// bash installer, which has no check).
	State  string
	Detail string // the first step not done
}

// Verify checks each item's steps without changing anything.
func Verify(ctx context.Context, e *steps.Env, items []*catalog.Item) []Status {
	var out []Status
	for _, x := range build(items) {
		st := Status{Item: x.item, State: "ok"}
		if x.err != nil {
			st.State, st.Detail = "missing", x.err.Error()
		}
		for _, s := range x.steps {
			if _, ok := s.(steps.LegacyStep); ok {
				st.State = "unchecked"
				continue
			}
			if f, ok := s.(steps.Follower); ok && f.Follows() {
				continue
			}
			if a, ok := s.(steps.Always); ok && a.Always() {
				continue
			}
			done, err := s.Check(ctx, e)
			if err != nil || !done {
				st.State = "missing"
				if acts := s.Plan(e); len(acts) > 0 {
					st.Detail = acts[0].String()
				}
				break
			}
		}
		out = append(out, st)
	}
	return out
}

// Summary writes what is installed, grouped by how, with how to manage it
// (~/devbox-setup.txt).
func Summary(w io.Writer, items []*catalog.Item, extraMise []string, now time.Time) {
	var miseL, aptL, otherL []string
	row := func(it *catalog.Item) string { return fmt.Sprintf("  %-40s %s", label(it), it.Describe()) }
	for _, it := range items {
		if it.Hidden() {
			continue
		}
		kinds := map[string]bool{}
		for _, s := range it.Steps {
			kinds[s.Type] = true
		}
		switch {
		case len(kinds) == 1 && kinds["mise"]:
			miseL = append(miseL, row(it))
		case len(kinds) == 1 && kinds["apt"], strings.HasPrefix(it.Describe(), "apt install "):
			aptL = append(aptL, row(it))
		default:
			otherL = append(otherL, row(it))
		}
	}
	if len(extraMise) > 0 {
		miseL = append(miseL, fmt.Sprintf("  %-40s mise use -g %s", "Extra mise tools", strings.Join(extraMise, " ")))
	}
	fmt.Fprintf(w, "devbox-setup — what is installed and how to manage it (updated %s)\n\n", now.UTC().Format("2006-01-02"))
	if len(miseL) > 0 {
		fmt.Fprintln(w, "Installed with mise (per user, ~/.config/mise/config.toml):")
		fmt.Fprintln(w, strings.Join(miseL, "\n"))
		fmt.Fprintln(w, "  update all: mise up · list: mise ls · pin: mise use -g <tool>@<version> · remove: mise rm -g <tool>")
		fmt.Fprintln(w)
	}
	if len(aptL) > 0 {
		fmt.Fprintln(w, "Installed with apt (system):")
		fmt.Fprintln(w, strings.Join(aptL, "\n"))
		fmt.Fprintln(w, "  update: sudo apt update && sudo apt upgrade · remove: sudo apt remove <package>")
		fmt.Fprintln(w)
	}
	if len(otherL) > 0 {
		fmt.Fprintln(w, "Vendor repos, release binaries, scripts and settings:")
		fmt.Fprintln(w, strings.Join(otherL, "\n"))
		fmt.Fprintln(w, "  vendor apt repos update with apt upgrade; claude and omp update themselves.")
		fmt.Fprintln(w)
	}
}

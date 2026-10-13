package answers

import (
	"fmt"
	"regexp"
	"strings"
)

// Lock pins versions: a profile's [lock], written by `export --lock`.
// Mise maps a mise tool (as `mise ls` names it) to its version, Apt a
// package to its Debian version. A lock applies to the run that loads the
// profile; it isn't saved with the answers (mise keeps the pinned version
// in the user's config, and apt doesn't upgrade on a re-run).
type Lock struct {
	Mise map[string]string `toml:"mise,omitempty"`
	Apt  map[string]string `toml:"apt,omitempty"`
}

var lockVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+~:_-]*$`)

// Check rejects names and versions that aren't single words.
func (l *Lock) Check() error {
	for kind, m := range map[string]map[string]string{"mise": l.Mise, "apt": l.Apt} {
		for k, v := range m {
			if k == "" || strings.ContainsAny(k, " \t\n=") {
				return fmt.Errorf("lock.%s: bad name %q", kind, k)
			}
			if !lockVersion.MatchString(v) {
				return fmt.Errorf("lock.%s.%s: bad version %q", kind, k, v)
			}
		}
	}
	return nil
}

// Add merges o into l; o's versions win.
func (l *Lock) Add(o *Lock) {
	if o == nil {
		return
	}
	for _, p := range []struct {
		dst *map[string]string
		src map[string]string
	}{{&l.Mise, o.Mise}, {&l.Apt, o.Apt}} {
		for k, v := range p.src {
			if *p.dst == nil {
				*p.dst = map[string]string{}
			}
			(*p.dst)[k] = v
		}
	}
}

// Empty reports whether l pins nothing (nil included).
func (l *Lock) Empty() bool { return l == nil || len(l.Mise)+len(l.Apt) == 0 }

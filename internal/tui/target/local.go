package target

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Local asks for a local path.
type Local struct {
	Title    string
	Default  string // used on an empty Enter; "." when unset
	DirsOnly bool   // complete and accept directories only

	in     input
	hint   string
	err    string
	result string
}

// Result returns the chosen path (valid after the form ends).
func (l *Local) Result() string { return l.result }

func (l *Local) title() string  { return l.Title }
func (l *Local) answer() string { return l.result }

func (l *Local) def() string {
	if l.Default == "" {
		return "."
	}
	return l.Default
}

func (l *Local) focus(context.Context, []string) { l.err, l.hint = "", "" }

func (l *Local) ghost() string {
	if l.in.str() == "" {
		return l.def()
	}
	return ""
}

func (l *Local) update(msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	switch k.Type {
	case tea.KeyEnter:
		return nil, l.submit()
	case tea.KeyTab:
		l.complete()
		return nil, false
	case tea.KeyRight, tea.KeyEnd:
		if l.in.atEnd() && l.ghost() != "" {
			l.in.set(l.ghost())
			return nil, false
		}
	}
	if l.in.edit(k) {
		l.err, l.hint = "", ""
	}
	return nil, false
}

// expand resolves a leading "~/" for filesystem access.
func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func (l *Local) complete() {
	v := l.in.str()
	if v == "" {
		v = l.def()
		if !strings.HasSuffix(v, "/") {
			l.in.set(v + "/")
			return
		}
	}
	dir, base := "", v
	if i := strings.LastIndex(v, "/"); i >= 0 {
		dir, base = v[:i+1], v[i+1:]
	}
	listDir := dir
	if listDir == "" {
		listDir = "."
	}
	ents, err := os.ReadDir(expand(listDir))
	if err != nil {
		l.hint = fmt.Sprintf("can't list %s: %v", listDir, err)
		return
	}
	var m []string
	for _, e := range ents {
		name := e.Name()
		if !strings.HasPrefix(name, base) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".")) {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(expand(listDir), name)); err == nil {
				isDir = st.IsDir()
			}
		}
		if l.DirsOnly && !isDir {
			continue
		}
		if isDir {
			name += "/"
		}
		m = append(m, name)
	}
	sort.Strings(m)
	switch {
	case len(m) == 0:
		l.hint = fmt.Sprintf("nothing in %s starts with %q", listDir, base)
	case len(m) == 1:
		l.in.set(dir + m[0])
	default:
		if lcp := longestCommonPrefix(m); len(lcp) > len(base) {
			l.in.set(dir + lcp)
			return
		}
		shown := m
		if len(shown) > 8 {
			shown = append(shown[:8:8], "…")
		}
		l.hint = strings.Join(shown, "  ")
	}
}

func (l *Local) submit() bool {
	v := strings.TrimSpace(l.in.str())
	if v == "" {
		v = l.def()
	}
	if l.DirsOnly {
		st, err := os.Stat(expand(v))
		switch {
		case err != nil:
			l.err = fmt.Sprintf("%s: no such directory", v)
			return false
		case !st.IsDir():
			l.err = fmt.Sprintf("%s is not a directory", v)
			return false
		}
	}
	l.result = v
	return true
}

func (l *Local) view(int) string {
	s := l.in.view(l.ghost(), 0)
	if l.err != "" {
		s += "\n" + errStyle.Render("  "+l.err)
	} else if l.hint != "" {
		s += "\n" + dimStyle.Render("  "+l.hint)
	}
	return s
}

func (l *Local) help() string { return "tab complete · enter confirm · esc back" }

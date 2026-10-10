package target

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// VM is one pmox VM the target field offers.
type VM struct {
	Name   string
	VMID   int
	Node   string
	Status string
	IP     string
}

func (v VM) running() bool { return v.Status == "running" }

// Lister lists dir on vm (dir "" is the login home). Entries are base
// names; directories end in "/".
type Lister func(ctx context.Context, vm VM, dir string) ([]string, error)

// ListTimeout bounds one remote directory listing.
var ListTimeout = 2 * time.Second

// maxRows is how many VMs the list under the field shows.
const maxRows = 6

// Remote asks for "<vm>:<path>".
type Remote struct {
	Title string
	VMs   []VM
	// DefaultPath returns the path suggested after "<vm>:", given the
	// answers of the fields before this one ("" for none).
	DefaultPath func(prev []string) string
	// List completes remote paths (nil: no remote completion).
	List Lister

	ctx     context.Context
	in      input
	def     string
	hi      int      // highlighted VM, index into VMs
	cycle   []string // VM names Tab is cycling through
	cycleI  int
	hint    string
	err     string
	cache   map[string][]string
	pending string // cache key of the listing in flight

	vm   VM
	path string
}

// Result returns the chosen VM and path (valid after the form ends).
func (r *Remote) Result() (VM, string) { return r.vm, r.path }

type listMsg struct {
	key     string
	entries []string
	err     error
}

func (r *Remote) title() string { return r.Title }

func (r *Remote) answer() string { return r.vm.Name + ":" + r.path }

func (r *Remote) single() bool { return len(r.VMs) == 1 }

func (r *Remote) focus(ctx context.Context, prev []string) {
	r.ctx = ctx
	if r.cache == nil {
		r.cache = map[string][]string{}
	}
	r.def = ""
	if r.DefaultPath != nil {
		r.def = r.DefaultPath(prev)
	}
	r.err, r.hint = "", ""
	if r.in.str() == "" && r.single() {
		r.in.set(r.VMs[0].Name + ":")
	}
}

// split returns the VM part, the path part and whether there is a ":".
func (r *Remote) split() (vmPart, path string, colon bool) {
	v := r.in.str()
	i := strings.Index(v, ":")
	if i < 0 {
		return v, "", false
	}
	return v[:i], v[i+1:], true
}

// matches returns the VMs whose name or VMID starts with prefix.
func (r *Remote) matches(prefix string) []VM {
	var out []VM
	for _, v := range r.VMs {
		if strings.HasPrefix(v.Name, prefix) || strings.HasPrefix(strconv.Itoa(v.VMID), prefix) {
			out = append(out, v)
		}
	}
	return out
}

// exact returns the VM named (or numbered) s.
func (r *Remote) exact(s string) (VM, bool) {
	for _, v := range r.VMs {
		if v.Name == s || strconv.Itoa(v.VMID) == s {
			return v, true
		}
	}
	return VM{}, false
}

// visible is the list under the field: VMs matching the VM part while
// it is being typed, every VM once it is complete (has a ":").
func (r *Remote) visible() []VM {
	vmPart, _, colon := r.split()
	if colon || vmPart == "" {
		return r.VMs
	}
	return r.matches(vmPart)
}

// showList reports whether to draw the VM list: hidden for a single VM
// until the user edits its name.
func (r *Remote) showList() bool {
	if !r.single() {
		return true
	}
	vmPart, _, colon := r.split()
	return !colon || vmPart != r.VMs[0].Name
}

// syncHighlight points the highlight at the VM the value names.
func (r *Remote) syncHighlight() {
	if len(r.VMs) == 0 {
		return
	}
	vmPart, _, _ := r.split()
	for i, v := range r.VMs {
		if v.Name == vmPart {
			r.hi = i
			return
		}
	}
	if vis := r.visible(); len(vis) > 0 {
		for i, v := range r.VMs {
			if v.Name == vis[0].Name {
				r.hi = i
				return
			}
		}
	}
}

// completion is the value Tab (or → at the end) would produce without
// a remote listing; "" when there is none.
func (r *Remote) completion() string {
	vmPart, path, colon := r.split()
	if !colon {
		if vmPart == "" && len(r.VMs) > 0 {
			return r.VMs[r.hi].Name + ":" + r.def
		}
		m := r.matches(vmPart)
		if len(m) == 1 {
			return m[0].Name + ":" + r.def
		}
		if v, ok := r.exact(vmPart); ok {
			return v.Name + ":" + r.def
		}
		names := make([]string, len(m))
		for i, v := range m {
			names[i] = v.Name
		}
		return longestCommonPrefix(names)
	}
	if path == "" {
		return vmPart + ":" + r.def
	}
	return ""
}

func (r *Remote) ghost() string {
	c, v := r.completion(), r.in.str()
	if strings.HasPrefix(c, v) {
		return c[len(v):]
	}
	return ""
}

func (r *Remote) update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case listMsg:
		if msg.key != r.pending {
			return nil, false
		}
		r.pending = ""
		if msg.err != nil {
			r.hint = "no remote completion: " + msg.err.Error()
			return nil, false
		}
		r.cache[msg.key] = msg.entries
		r.hint = ""
		r.applyListing(msg.entries)
		return nil, false
	case tea.KeyMsg:
		return r.key(msg)
	}
	return nil, false
}

func (r *Remote) key(k tea.KeyMsg) (tea.Cmd, bool) {
	if k.Type != tea.KeyTab {
		r.cycle = nil
	}
	switch k.Type {
	case tea.KeyEnter:
		return nil, r.submit()
	case tea.KeyTab:
		return r.tab(), false
	case tea.KeyUp, tea.KeyDown:
		r.pick(k.Type == tea.KeyDown)
		return nil, false
	case tea.KeyRight, tea.KeyEnd:
		if r.in.atEnd() {
			if g := r.ghost(); g != "" {
				r.in.set(r.in.str() + g)
				r.syncHighlight()
				return nil, false
			}
		}
	}
	if r.in.edit(k) {
		r.err, r.hint = "", ""
		r.syncHighlight()
	}
	return nil, false
}

// pick moves the highlight and puts that VM in the value, keeping the
// path typed so far.
func (r *Remote) pick(down bool) {
	if len(r.VMs) == 0 {
		return
	}
	vis := r.visible()
	if len(vis) == 0 {
		vis = r.VMs
	}
	cur := 0
	for i, v := range vis {
		if v.Name == r.VMs[r.hi].Name {
			cur = i
		}
	}
	if down {
		cur = (cur + 1) % len(vis)
	} else {
		cur = (cur - 1 + len(vis)) % len(vis)
	}
	_, path, _ := r.split()
	r.in.set(vis[cur].Name + ":" + path)
	r.err, r.hint = "", ""
	r.syncHighlight()
}

func (r *Remote) tab() tea.Cmd {
	vmPart, path, colon := r.split()
	r.hint = ""
	if !colon {
		if r.cycle != nil {
			r.cycleI = (r.cycleI + 1) % len(r.cycle)
			r.in.set(r.cycle[r.cycleI])
			r.syncHighlight()
			return nil
		}
		if vmPart == "" && len(r.VMs) > 0 {
			r.in.set(r.VMs[r.hi].Name + ":")
			return nil
		}
		m := r.matches(vmPart)
		switch {
		case len(m) == 0:
			r.hint = fmt.Sprintf("no pmox VM starts with %q", vmPart)
		case len(m) == 1:
			r.in.set(m[0].Name + ":")
		default:
			names := make([]string, len(m))
			for i, v := range m {
				names[i] = v.Name
			}
			if lcp := longestCommonPrefix(names); len(lcp) > len(vmPart) {
				r.in.set(lcp)
			} else {
				r.cycle, r.cycleI = names, 0
				r.in.set(names[0])
			}
		}
		r.syncHighlight()
		return nil
	}
	if path == "" {
		if r.def != "" {
			r.in.set(vmPart + ":" + r.def)
		}
		return nil
	}
	return r.completePath()
}

// pathDir splits the path part into the directory being listed (up to
// and including the last "/", "" for the home directory) and the
// partial name after it.
func (r *Remote) pathDir() (vm VM, ok bool, dir, base string) {
	vmPart, path, _ := r.split()
	vm, ok = r.exact(vmPart)
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return vm, ok, path[:i+1], path[i+1:]
	}
	return vm, ok, "", path
}

func (r *Remote) completePath() tea.Cmd {
	vm, ok, dir, _ := r.pathDir()
	switch {
	case !ok:
		r.hint = "pick a VM first"
		return nil
	case r.List == nil:
		r.hint = "remote completion is unavailable"
		return nil
	case !vm.running():
		r.hint = fmt.Sprintf("no remote completion: %s is %s", vm.Name, vm.Status)
		return nil
	}
	key := vm.Name + "\x00" + dir
	if entries, ok := r.cache[key]; ok {
		r.applyListing(entries)
		return nil
	}
	r.pending = key
	r.hint = "listing " + dirLabel(dir) + "…"
	ctx, list := r.ctx, r.List
	return func() tea.Msg {
		lctx, cancel := context.WithTimeout(ctx, ListTimeout)
		defer cancel()
		entries, err := list(lctx, vm, dir)
		return listMsg{key: key, entries: entries, err: err}
	}
}

func dirLabel(dir string) string {
	if dir == "" {
		return "~"
	}
	return dir
}

// applyListing completes the partial name from a directory listing.
func (r *Remote) applyListing(entries []string) {
	vm, _, dir, base := r.pathDir()
	var m []string
	for _, e := range entries {
		if strings.HasPrefix(e, base) {
			m = append(m, e)
		}
	}
	switch {
	case len(m) == 0:
		r.hint = fmt.Sprintf("nothing in %s starts with %q", dirLabel(dir), base)
	case len(m) == 1:
		r.in.set(vm.Name + ":" + dir + m[0])
	default:
		if lcp := longestCommonPrefix(m); len(lcp) > len(base) {
			r.in.set(vm.Name + ":" + dir + lcp)
			return
		}
		shown := m
		if len(shown) > 8 {
			shown = append(shown[:8:8], "…")
		}
		r.hint = strings.Join(shown, "  ")
	}
}

// submit validates the value and records the result.
func (r *Remote) submit() bool {
	vmPart, path, colon := r.split()
	var (
		vm VM
		ok bool
	)
	switch {
	case vmPart == "" && !colon && len(r.VMs) > 0:
		vm, ok = r.VMs[r.hi], true
	case vmPart == "":
		r.err = "pick a VM"
		return false
	default:
		if vm, ok = r.exact(vmPart); !ok && !colon {
			if m := r.matches(vmPart); len(m) == 1 {
				vm, ok = m[0], true
			}
		}
	}
	if !ok {
		r.err = fmt.Sprintf("no pmox VM named %s", vmPart)
		return false
	}
	if path == "" {
		path = r.def
	}
	if path == "" {
		r.err = "a remote path is required"
		return false
	}
	r.vm, r.path = vm, path
	return true
}

func (r *Remote) view(width int) string {
	var b strings.Builder
	dimTo := 0
	if vmPart, _, colon := r.split(); r.single() && colon && vmPart == r.VMs[0].Name {
		dimTo = len([]rune(vmPart)) + 1
	}
	b.WriteString(r.in.view(r.ghost(), dimTo))
	b.WriteString("\n")
	if r.err != "" {
		b.WriteString(errStyle.Render("  "+r.err) + "\n")
	} else if r.hint != "" {
		b.WriteString(dimStyle.Render("  "+r.hint) + "\n")
	}
	if r.showList() && len(r.VMs) > 0 {
		vis := r.visible()
		for i, v := range vis {
			if i == maxRows {
				b.WriteString(dimStyle.Render(fmt.Sprintf("    … %d more", len(vis)-maxRows)) + "\n")
				break
			}
			row := fmt.Sprintf("%-20s %-6d %-8s %s", v.Name, v.VMID, v.Status, v.IP)
			if width > 6 && len(row) > width-4 {
				row = row[:width-4]
			}
			if v.Name == r.VMs[r.hi].Name {
				b.WriteString(accentStyle.Render("  › "+row) + "\n")
			} else {
				b.WriteString("    " + row + "\n")
			}
		}
		if len(vis) == 0 {
			b.WriteString(dimStyle.Render("    no matching VM") + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *Remote) help() string {
	if r.showList() {
		return "tab complete · ↑/↓ pick VM · enter confirm · esc back"
	}
	return "tab complete · enter confirm · esc back"
}

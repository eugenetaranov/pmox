// Package answers reads and writes devbox-setup's saved answers,
// /var/lib/devbox-setup/answers.json: everything ever picked, and the plain
// settings that go with it. Never a secret. The file is data: it is only
// parsed, never run.
package answers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Version is the answers.json format this package reads and writes.
const Version = 1

// File and Legacy are the answer files' names in the state directory.
// Legacy is the shell-format file devbox-setup wrote before v0.39; it is
// converted to File once and kept as Legacy+".old".
const (
	File   = "answers.json"
	Legacy = "answers"
)

// Answers is answers.json. The JSON shape is shared with the bash
// installer (picker.py), so both can read what the other wrote.
type Answers struct {
	Version    int      `json:"version"`
	User       string   `json:"user"`
	Picks      []string `json:"picks"`
	MiseExtra  []string `json:"mise_extra"`
	MCPListen  string   `json:"mcp_listen"`
	MCPServers []string `json:"mcp_servers"`
	// WireAgents: nil means never asked.
	WireAgents *bool  `json:"wire_agents"`
	Git        Git    `json:"git"`
	TZ         string `json:"tz"`
	// DevPorts are the ports ufw opens for dev servers ("3000",
	// "8000-8010", "5353/udp").
	DevPorts []string `json:"dev_ports,omitempty"`
	// Proxy is the corporate proxy (no credentials) and the hosts that
	// bypass it; CA where an internal CA bundle comes from.
	Proxy      Proxy    `json:"proxy,omitzero"`
	CA         CA       `json:"ca,omitzero"`
	GitHub     GitHub   `json:"github"`
	NPM        NPM      `json:"npm"`
	Registries []string `json:"registries"`
	// Dotfiles is the user's dotfiles repo (cloned to ~/.dotfiles).
	Dotfiles string `json:"dotfiles,omitempty"`
	// GitKeyHosts are the git hosts the git SSH key (gitkey) is used for.
	GitKeyHosts []string `json:"gitkey_hosts,omitempty"`
	// Layers are the picks each named profile set, by its name. Applying
	// a profile with that name again replaces its layer (SetLayer).
	Layers map[string][]string `json:"layers,omitempty"`
	// Hand are picks made by hand, or by an unnamed profile, that a named
	// layer also lists: no layer drops them. A pick in no layer is a hand
	// pick too, so this stays short.
	Hand []string `json:"hand,omitempty"`
	// MCPPort is the user's MCPJungle port (kept in system.json).
	MCPPort int `json:"-"`
}

// Git is the git identity.
type Git struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Proxy is an HTTP(S) proxy for apt, Docker, mise and shells.
type Proxy struct {
	URL     string `json:"url"`
	NoProxy string `json:"no_proxy"`
}

// CA is an internal CA bundle: a path on the VM, or an https URL with
// its SHA-256.
type CA struct {
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
}

// GitHub holds how the VM reaches GitHub: agent, key or token ("" = key).
type GitHub struct {
	Mode string `json:"mode"`
}

// NPM is the private npm registry's settings (its token is never saved).
type NPM struct {
	Scope    string `json:"scope"`
	Registry string `json:"registry"`
}

// PickMap maps catalog ids renamed, merged or removed since v0.29 to what
// replaced them.
var PickMap = map[string][]string{
	"kubectl": {"k8s"}, "kubectx": {"k8s"}, "helm": {"k8s"}, "k9s": {"k8s"},
	"devbox":     {"essentials", "cli", "editors", "tmux", "zellij"},
	"shell":      {"zsh", "zshcfg", "p10k"}, // split into three in v0.38
	"typescript": {"node"},
	"kotlin":     {"java"},
	"editors":    {"editors", "neovim"}, // Neovim moved to its own (mise) item
	"chrony":     {},                    // pmox sets the clock up at launch since v0.39
}

// pickNotes explain a migration to the user.
var pickNotes = map[string]string{
	"chrony": "chrony is now set up by pmox at launch (clock sync); this VM keeps the chrony it has.",
}

// MigratePicks applies PickMap, keeping order and dropping duplicates. It
// returns the notes for picks that went away.
func MigratePicks(picks []string) (out, notes []string) {
	seen := map[string]bool{}
	noted := map[string]bool{}
	for _, p := range picks {
		repl, ok := PickMap[p]
		if !ok {
			repl = []string{p}
		}
		for _, q := range repl {
			if !seen[q] {
				seen[q] = true
				out = append(out, q)
			}
		}
		if n, ok := pickNotes[p]; ok && !noted[n] {
			noted[n] = true
			notes = append(notes, n)
		}
	}
	return out, notes
}

// Read loads the saved answers from state, migrated. A missing file is
// empty answers. A legacy shell-format file is decoded (never run),
// converted to answers.json and kept as answers.old when state is
// writable. Warnings (skipped lines, migration notes) come back in notes.
func Read(state string) (a *Answers, notes []string, err error) {
	path := filepath.Join(state, File)
	legacy := filepath.Join(state, Legacy)
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		a = &Answers{}
		if err := json.Unmarshal(b, a); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		if a.Version != Version {
			return nil, nil, fmt.Errorf("%s: version %d; this devbox-setup reads %d", path, a.Version, Version)
		}
	case errors.Is(err, fs.ErrNotExist):
		lb, lerr := os.ReadFile(legacy)
		if errors.Is(lerr, fs.ErrNotExist) {
			return &Answers{Version: Version}, nil, nil
		}
		if lerr != nil {
			return nil, nil, lerr
		}
		a, notes = decodeLegacy(legacy, string(lb))
		if writable(state) {
			if err := Write(state, a); err != nil {
				return nil, nil, err
			}
			if err := os.Rename(legacy, legacy+".old"); err != nil {
				return nil, nil, err
			}
		}
	default:
		return nil, nil, err
	}
	a.clean()
	var more []string
	a.Picks, more = MigratePicks(a.Picks)
	a.Hand, _ = MigratePicks(a.Hand)
	for n, l := range a.Layers {
		a.Layers[n], _ = MigratePicks(l)
	}
	return a, append(notes, more...), nil
}

// clean drops list entries that aren't single words (the bash side splits
// them on spaces).
func (a *Answers) clean() {
	for _, l := range []*[]string{&a.Picks, &a.MiseExtra, &a.MCPServers, &a.Registries, &a.DevPorts} {
		out := (*l)[:0]
		for _, s := range *l {
			if s != "" && !strings.ContainsAny(s, " \t\n") {
				out = append(out, s)
			}
		}
		*l = out
	}
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".w")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// Write saves a to state/answers.json (0600, atomically), creating state
// (0700) if needed.
func Write(state string, a *Answers) error {
	if err := os.MkdirAll(state, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(state, 0o700); err != nil {
		return err
	}
	a.Version = Version
	for _, l := range []*[]string{&a.Picks, &a.MiseExtra, &a.MCPServers, &a.Registries} {
		if *l == nil {
			*l = []string{}
		}
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(state, File), append(b, '\n'), 0o600)
}

// writeAtomic writes b to path through a temp file, with mode.
func writeAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Merge returns a's items followed by b's new ones (choices accumulate
// across runs).
func Merge(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, l := range [][]string{a, b} {
		for _, s := range l {
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// Pick adds picks made by hand (or by an unnamed profile): no named
// profile drops them later.
func (a *Answers) Pick(ids []string) {
	for _, id := range ids {
		if a.inLayer(id, "") {
			a.Hand = Merge(a.Hand, []string{id})
		}
	}
	a.Picks = Merge(a.Picks, ids)
}

// SetLayer makes ids the named profile's picks, replacing what it set
// before. It returns the picks the profile no longer lists that nothing
// else keeps (hand picks and other layers' picks stay); they leave Picks,
// so they stop being re-applied, but stay installed.
func (a *Answers) SetLayer(name string, ids []string) (dropped []string) {
	for _, id := range ids {
		// Picked before any layer claimed it: it's the user's own.
		if slices.Contains(a.Picks, id) && !a.inLayer(id, "") {
			a.Hand = Merge(a.Hand, []string{id})
		}
	}
	old := a.Layers[name]
	if a.Layers == nil {
		a.Layers = map[string][]string{}
	}
	a.Layers[name] = Merge(nil, ids)
	for _, id := range old {
		if !slices.Contains(ids, id) && !slices.Contains(a.Hand, id) && !a.inLayer(id, name) {
			dropped = append(dropped, id)
		}
	}
	a.Picks = slices.DeleteFunc(Merge(a.Picks, ids), func(id string) bool { return slices.Contains(dropped, id) })
	return dropped
}

// inLayer reports whether a named layer other than except lists id.
func (a *Answers) inLayer(id, except string) bool {
	for n, l := range a.Layers {
		if n != except && slices.Contains(l, id) {
			return true
		}
	}
	return false
}

var portRe = regexp.MustCompile(`^([0-9]{1,5})(-([0-9]{1,5}))?(/(tcp|udp))?$`)

// ValidPort reports whether p is a dev port spec: "3000", "8000-8010",
// either with /tcp or /udp.
func ValidPort(p string) bool {
	m := portRe.FindStringSubmatch(p)
	if m == nil {
		return false
	}
	lo, _ := strconv.Atoi(m[1])
	hi := lo
	if m[3] != "" {
		hi, _ = strconv.Atoi(m[3])
	}
	return lo >= 1 && hi <= 65535 && lo <= hi
}

// UFWRule is ufw's rule for a dev port spec ("8000-8010" → "8000:8010/tcp").
func UFWRule(p string) string {
	proto := "tcp"
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p, proto = p[:i], p[i+1:]
	}
	return "allow " + strings.Replace(p, "-", ":", 1) + "/" + proto
}

// CheckProxy checks a proxy URL: http(s)://host[:port], without
// credentials (answers never hold secrets).
func CheckProxy(u string) error {
	p, err := url.Parse(u)
	switch {
	case err != nil:
		return err
	case p.Scheme != "http" && p.Scheme != "https", p.Host == "":
		return errors.New("want http://host:port or https://host:port")
	case p.User != nil:
		return errors.New("no credentials in the proxy URL: devbox-setup never saves secrets (use a proxy that authenticates the VM another way)")
	case strings.ContainsAny(u, " \t\n\"'\\$`"):
		return errors.New("unexpected characters")
	}
	return nil
}

// CheckNoProxy checks a no-proxy list: comma-separated hosts, domains,
// IPs and CIDRs.
func CheckNoProxy(s string) error {
	if strings.ContainsAny(s, " \t\n\"'\\$`;") {
		return errors.New("want comma-separated hosts, like localhost,127.0.0.1,.internal")
	}
	return nil
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// CheckCA checks where a CA bundle comes from: an absolute path, or an
// https URL with a 64-hex-digit SHA-256.
func CheckCA(c CA) error {
	switch {
	case c.Source == "":
		return nil
	case strings.HasPrefix(c.Source, "https://"):
		if !sha256Re.MatchString(c.SHA256) {
			return errors.New("a CA bundle from a URL needs its sha256 (64 lowercase hex digits)")
		}
	case !filepath.IsAbs(c.Source):
		return errors.New("want an absolute path on the VM or an https URL")
	}
	return nil
}

var repoRe = regexp.MustCompile(`^(https://[^\s'"\\$` + "`" + `;]+|ssh://[^\s'"\\$` + "`" + `;]+|[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9._/~-]+)$`)

// CheckRepo checks a git repo URL: https://, ssh:// or user@host:path.
func CheckRepo(u string) error {
	if !repoRe.MatchString(u) || strings.HasPrefix(u, "-") {
		return errors.New("want an https://, ssh:// or git@host:path repo URL")
	}
	return nil
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)

// ValidHost reports whether h is a host name (with an optional :port) for
// ~/.ssh/config.
func ValidHost(h string) bool { return hostRe.MatchString(h) }

// Package profile reads and writes devbox-setup profiles: a TOML file of
// picks and plain settings that one machine exports and another applies.
// A profile is data only (no commands) and holds no secrets.
package profile

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

// Profile is one profile file.
type Profile struct {
	// Name makes the profile a layer: applying a profile with the same
	// name again replaces the picks it set before.
	Name string `toml:"name,omitempty"`
	// User is the account to set up (created if missing).
	User      string   `toml:"user,omitempty"`
	Picks     []string `toml:"picks"`
	MiseExtra []string `toml:"mise_extra"`
	Git       *Git     `toml:"git"`
	System    *System  `toml:"system"`
	MCP       *MCP     `toml:"mcp"`
	// Dotfiles is the dotfiles item's repo (a personal profile's).
	Dotfiles *Dotfiles `toml:"dotfiles,omitempty"`
	// Lock pins versions (export --lock).
	Lock *answers.Lock `toml:"lock,omitempty"`
}

// Git is the git identity.
type Git struct {
	Name  string `toml:"name"`
	Email string `toml:"email"`
}

// System holds machine settings.
type System struct {
	TZ string `toml:"tz"`
	// DevPorts are opened by ufw ("3000", "8000-8010", "5353/udp").
	DevPorts []string `toml:"dev_ports,omitempty"`
	// Proxy and NoProxy (proxy item); CABundle and CASHA256 (corpca).
	Proxy    string `toml:"proxy,omitempty"`
	NoProxy  string `toml:"no_proxy,omitempty"`
	CABundle string `toml:"ca_bundle,omitempty"`
	CASHA256 string `toml:"ca_sha256,omitempty"`
}

// Dotfiles is a dotfiles repo.
type Dotfiles struct {
	Repo string `toml:"repo"`
}

// MCP holds the MCPJungle settings.
type MCP struct {
	Servers    []string `toml:"servers"`
	Listen     string   `toml:"listen"`
	WireAgents *bool    `toml:"wire_agents"`
}

// Parse reads a profile. An unknown key is an error naming it, so a typo
// doesn't quietly do nothing.
func Parse(name string, b []byte) (*Profile, error) {
	var p Profile
	md, err := toml.NewDecoder(bytes.NewReader(b)).Decode(&p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
			if secretKey(k[len(k)-1]) {
				return nil, fmt.Errorf("%s: %s looks like a secret: profiles never hold secrets (devbox-setup asks for them, or leaves them for you to fill in)", name, k)
			}
		}
		return nil, fmt.Errorf("%s: unknown key %s", name, strings.Join(keys, ", "))
	}
	if p.MCP != nil && p.MCP.Listen != "" && p.MCP.Listen != "127.0.0.1" && p.MCP.Listen != "0.0.0.0" && p.MCP.Listen != "tailscale" {
		return nil, fmt.Errorf("%s: mcp.listen %q: want 127.0.0.1, tailscale or 0.0.0.0", name, p.MCP.Listen)
	}
	if p.System != nil {
		if p.System.Proxy != "" {
			if err := answers.CheckProxy(p.System.Proxy); err != nil {
				return nil, fmt.Errorf("%s: system.proxy: %w", name, err)
			}
		}
		if err := answers.CheckNoProxy(p.System.NoProxy); err != nil {
			return nil, fmt.Errorf("%s: system.no_proxy: %w", name, err)
		}
		if err := answers.CheckCA(answers.CA{Source: p.System.CABundle, SHA256: p.System.CASHA256}); err != nil {
			return nil, fmt.Errorf("%s: system.ca_bundle: %w", name, err)
		}
		for _, port := range p.System.DevPorts {
			if !answers.ValidPort(port) {
				return nil, fmt.Errorf("%s: system.dev_ports: %q: want a port, a range (8000-8010), or either with /udp", name, port)
			}
		}
	}
	if p.Dotfiles != nil && p.Dotfiles.Repo != "" {
		if err := answers.CheckRepo(p.Dotfiles.Repo); err != nil {
			return nil, fmt.Errorf("%s: dotfiles.repo: %w", name, err)
		}
	}
	if p.Lock != nil {
		if err := p.Lock.Check(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	if p.Name != "" && !layerName.MatchString(p.Name) {
		return nil, fmt.Errorf("%s: name %q: want letters, digits, '.', '_' or '-'", name, p.Name)
	}
	if p.User != "" && !ValidUser(p.User) {
		return nil, fmt.Errorf("%s: user %q is not a valid user name", name, p.User)
	}
	return &p, nil
}

var (
	layerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	userName  = regexp.MustCompile(`^[a-z][-a-z0-9_]{0,31}$`)
)

// ValidUser reports whether name is a user name devbox-setup will create
// (Debian's default NAME_REGEX, without the trailing '$').
func ValidUser(name string) bool { return userName.MatchString(name) }

// Apply adds the profile to the saved answers: picks and extra mise tools
// accumulate, settings replace. Picks are migrated (old ids) and the ones
// not in the catalog are skipped with a warning. A named profile replaces
// its earlier layer: dropped are the picks it no longer lists, which
// leave the answers (they stay installed).
func (p *Profile) Apply(a *answers.Answers, c *catalog.Catalog) (dropped, warnings []string) {
	picks, notes := answers.MigratePicks(p.Picks)
	warnings = append(warnings, notes...)
	var keep []string
	for _, id := range picks {
		if it := c.Item(id); it == nil || it.Hidden() {
			warnings = append(warnings, fmt.Sprintf("profile: %q is not a devbox-setup item; skipped", id))
			continue
		}
		keep = append(keep, id)
	}
	if p.Name != "" {
		dropped = a.SetLayer(p.Name, keep)
	} else {
		a.Pick(keep)
	}
	if p.User != "" {
		a.User = p.User
	}
	if p.Dotfiles != nil && p.Dotfiles.Repo != "" {
		a.Dotfiles = p.Dotfiles.Repo
	}
	a.MiseExtra = answers.Merge(a.MiseExtra, p.MiseExtra)
	if p.Git != nil {
		if p.Git.Name != "" {
			a.Git.Name = p.Git.Name
		}
		if p.Git.Email != "" {
			a.Git.Email = p.Git.Email
		}
	}
	if p.System != nil && p.System.TZ != "" {
		a.TZ = p.System.TZ
	}
	if p.System != nil {
		a.DevPorts = answers.Merge(a.DevPorts, p.System.DevPorts)
		if p.System.Proxy != "" {
			a.Proxy = answers.Proxy{URL: p.System.Proxy, NoProxy: p.System.NoProxy}
		}
		if p.System.CABundle != "" {
			a.CA = answers.CA{Source: p.System.CABundle, SHA256: p.System.CASHA256}
		}
	}
	if p.MCP != nil {
		a.MCPServers = answers.Merge(a.MCPServers, p.MCP.Servers)
		if p.MCP.Listen != "" {
			a.MCPListen = p.MCP.Listen
		}
		if p.MCP.WireAgents != nil {
			v := *p.MCP.WireAgents
			a.WireAgents = &v
		}
	}
	return dropped, warnings
}

// Export writes the saved answers as a profile, with lock's versions when
// it pins any.
func Export(a *answers.Answers, lock *answers.Lock) []byte {
	p := Profile{Picks: a.Picks, MiseExtra: a.MiseExtra}
	if !lock.Empty() {
		p.Lock = lock
	}
	if a.Git.Name != "" || a.Git.Email != "" {
		p.Git = &Git{Name: a.Git.Name, Email: a.Git.Email}
	}
	if a.TZ != "" || len(a.DevPorts) > 0 || a.Proxy.URL != "" || a.CA.Source != "" {
		p.System = &System{TZ: a.TZ, DevPorts: a.DevPorts, Proxy: a.Proxy.URL, NoProxy: a.Proxy.NoProxy, CABundle: a.CA.Source, CASHA256: a.CA.SHA256}
	}
	if a.Dotfiles != "" {
		p.Dotfiles = &Dotfiles{Repo: a.Dotfiles}
	}
	if len(a.MCPServers) > 0 || a.MCPListen != "" || a.WireAgents != nil {
		p.MCP = &MCP{Servers: a.MCPServers, Listen: a.MCPListen, WireAgents: a.WireAgents}
	}
	var buf bytes.Buffer
	buf.WriteString("# devbox profile v1: apply with `sudo devbox-setup --profile <file> --yes`.\n")
	buf.WriteString("# Data only: picks and settings, never secrets.\n")
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if p.Picks == nil {
		p.Picks = []string{}
	}
	_ = enc.Encode(p)
	return buf.Bytes()
}

// secretKey reports whether a key name looks like it holds a secret.
func secretKey(k string) bool {
	k = strings.ToUpper(k)
	for _, w := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "APIKEY", "API_KEY", "AUTHKEY", "AUTH_KEY", "PRIVATE"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return strings.HasSuffix(k, "_KEY") || strings.HasSuffix(k, "_KEY_ID")
}

// Equal reports whether two answers pick and set the same (for tests and
// --verify against a profile).
func Equal(a, b *answers.Answers) bool {
	return slices.Equal(a.Picks, b.Picks) && slices.Equal(a.MiseExtra, b.MiseExtra) &&
		a.Git == b.Git && a.TZ == b.TZ && slices.Equal(a.MCPServers, b.MCPServers) && a.MCPListen == b.MCPListen
}

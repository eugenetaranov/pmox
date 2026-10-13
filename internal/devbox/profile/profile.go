// Package profile reads and writes devbox-setup profiles: a TOML file of
// picks and plain settings that one machine exports and another applies.
// A profile is data only (no commands) and holds no secrets.
package profile

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

// Profile is one profile file.
type Profile struct {
	Picks     []string `toml:"picks"`
	MiseExtra []string `toml:"mise_extra"`
	Git       *Git     `toml:"git"`
	System    *System  `toml:"system"`
	MCP       *MCP     `toml:"mcp"`
}

// Git is the git identity.
type Git struct {
	Name  string `toml:"name"`
	Email string `toml:"email"`
}

// System holds machine settings.
type System struct {
	TZ string `toml:"tz"`
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
		}
		return nil, fmt.Errorf("%s: unknown key %s", name, strings.Join(keys, ", "))
	}
	if p.MCP != nil && p.MCP.Listen != "" && p.MCP.Listen != "127.0.0.1" && p.MCP.Listen != "0.0.0.0" {
		return nil, fmt.Errorf("%s: mcp.listen %q: want 127.0.0.1 or 0.0.0.0", name, p.MCP.Listen)
	}
	return &p, nil
}

// Apply adds the profile to the saved answers: picks and extra mise tools
// accumulate, settings replace. Picks are migrated (old ids) and the ones
// not in the catalog are dropped with a warning.
func (p *Profile) Apply(a *answers.Answers, c *catalog.Catalog) (warnings []string) {
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
	a.Picks = answers.Merge(a.Picks, keep)
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
	return warnings
}

// Export writes the saved answers as a profile.
func Export(a *answers.Answers) []byte {
	p := Profile{Picks: a.Picks, MiseExtra: a.MiseExtra}
	if a.Git.Name != "" || a.Git.Email != "" {
		p.Git = &Git{Name: a.Git.Name, Email: a.Git.Email}
	}
	if a.TZ != "" {
		p.System = &System{TZ: a.TZ}
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

// Equal reports whether two answers pick and set the same (for tests and
// --verify against a profile).
func Equal(a, b *answers.Answers) bool {
	return slices.Equal(a.Picks, b.Picks) && slices.Equal(a.MiseExtra, b.MiseExtra) &&
		a.Git == b.Git && a.TZ == b.TZ && slices.Equal(a.MCPServers, b.MCPServers) && a.MCPListen == b.MCPListen
}

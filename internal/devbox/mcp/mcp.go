// Package mcp reads the MCP server specs devbox-setup offers (the shell
// files mcp-sync uses: DESCRIPTION, IMAGE or URL, REQUIRED, and a
// skeleton() that prints the env file template).
package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Spec is one MCP server.
type Spec struct {
	Name, Description, Image, URL string
	Required                      []string
	// Keys are the skeleton's active KEY= lines; Optional its commented
	// #KEY= ones. Defaults has a value the skeleton sets.
	Keys, Optional []string
	Defaults       map[string]string
}

// Dirs are where the specs are, first match wins: the installed catalog,
// then the copies devbox-setup ships (tack-roles', then pmox's own).
func Dirs(share string) []string {
	return []string{"/usr/local/share/mcp-catalog", filepath.Join(share, "mcp-catalog"), filepath.Join(share, "conf", "mcp-catalog")}
}

// Names lists every spec name in dirs, sorted.
func Names(dirs []string) []string {
	seen := map[string]bool{}
	for _, d := range dirs {
		m, _ := filepath.Glob(filepath.Join(d, "*.spec"))
		for _, f := range m {
			seen[strings.TrimSuffix(filepath.Base(f), ".spec")] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Load reads spec name from the first dir that has it (bash sources it).
func Load(ctx context.Context, dirs []string, name string) (*Spec, error) {
	path := ""
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, name+".spec")); err == nil {
			path = filepath.Join(d, name+".spec")
			break
		}
	}
	if path == "" {
		return nil, fmt.Errorf("no MCP spec %q", name)
	}
	const script = `set -eu
. "$1"
printf '%s\0%s\0%s\0%s\0' "${DESCRIPTION:-}" "${IMAGE:-}" "${URL:-}" "${REQUIRED:-}"
skeleton 2>/dev/null || true`
	out, err := exec.CommandContext(ctx, "bash", "-c", script, "_", path).Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	f := strings.SplitN(string(out), "\x00", 5)
	if len(f) < 5 {
		return nil, fmt.Errorf("%s: unreadable", path)
	}
	s := &Spec{Name: name, Description: f[0], Image: f[1], URL: f[2], Required: strings.Fields(f[3]), Defaults: map[string]string{}}
	for _, line := range strings.Split(f[4], "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || k == "" {
			continue
		}
		if opt, isOpt := strings.CutPrefix(k, "#"); isOpt {
			if validKey(opt) {
				s.Optional = append(s.Optional, opt)
			}
			continue
		}
		if validKey(k) {
			s.Keys = append(s.Keys, k)
			if v != "" {
				s.Defaults[k] = v
			}
		}
	}
	return s, nil
}

func validKey(k string) bool {
	if k == "" || k[0] < 'A' || k[0] > 'Z' {
		return false
	}
	for _, c := range k {
		if c != '_' && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// IsSecret says whether a key holds a secret (read without echo).
func IsSecret(k string) bool {
	return strings.Contains(k, "TOKEN") || strings.Contains(k, "SECRET") || strings.Contains(k, "PASSWORD") ||
		strings.HasSuffix(k, "_KEY") || strings.HasSuffix(k, "_KEY_ID") || strings.Contains(k, "APIKEY")
}

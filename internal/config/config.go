// Package config loads, saves, and canonicalizes pmox's server configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/eugenetaranov/pmox/internal/atomicfile"
	"github.com/eugenetaranov/pmox/internal/paths"
)

// Server is the persisted per-server configuration block.
//
// SSHPubkey is the path to a local public key file that pmox injects
// into cloud-init's ssh_authorized_keys on launch/clone. It has no
// relation to the NodeSSH block — that one is the *private* key used to
// SSH into the Proxmox node itself for snippet upload.
type Server struct {
	// Name is the kubectl-style context name for this server. When empty,
	// a name is derived from the host (see Config.Contexts). use-context
	// and rename-context materialize an explicit name here.
	Name           string   `yaml:"name,omitempty"`
	TokenID        string   `yaml:"token_id"`
	Node           string   `yaml:"node,omitempty"`
	Template       string   `yaml:"template,omitempty"`
	Storage        string   `yaml:"storage,omitempty"`
	SnippetStorage string   `yaml:"snippet_storage,omitempty"`
	Bridge         string   `yaml:"bridge,omitempty"`
	SSHPubkey      string   `yaml:"ssh_pubkey,omitempty"`
	User           string   `yaml:"user,omitempty"`
	Insecure       bool     `yaml:"insecure"`
	NodeSSH        *NodeSSH `yaml:"node_ssh,omitempty"`

	// TLSPinSHA256 is the SHA-256 fingerprint (hex) of the server's leaf
	// TLS certificate, pinned on the first insecure (unverified) connect.
	// On later connects a mismatch is treated as a possible MITM. Only
	// meaningful when Insecure is true.
	TLSPinSHA256 string `yaml:"tls_pin_sha256,omitempty"`
}

// NodeSSHAuth is the node-SSH authentication mode persisted in config.
type NodeSSHAuth string

// Node-SSH authentication modes. The empty value means "unconfigured".
const (
	AuthPassword NodeSSHAuth = "password"
	AuthKey      NodeSSHAuth = "key"
)

// defaultNodeSSHUser is the user pmox SSHes into the node as when the
// node_ssh block leaves user empty.
const defaultNodeSSHUser = "root"

// NodeSSH holds the SSH credentials pmox uses to reach the PVE node
// itself (for snippet upload during create-template). Password and key
// passphrase live in the keyring, not this struct.
type NodeSSH struct {
	User    string      `yaml:"user"`               // see EffectiveUser
	Auth    NodeSSHAuth `yaml:"auth"`               // AuthPassword | AuthKey
	KeyPath string      `yaml:"key_path,omitempty"` // private key path when Auth == AuthKey
}

// EffectiveUser returns the configured node SSH user, defaulting to
// "root" when unset.
func (n *NodeSSH) EffectiveUser() string {
	if n == nil || n.User == "" {
		return defaultNodeSSHUser
	}
	return n.User
}

// Config is the top-level YAML shape on disk.
type Config struct {
	Servers map[string]*Server `yaml:"servers"`
	// CurrentContext is the kubectl-style name of the context (server)
	// commands target when neither --server/--context nor the matching
	// env vars are set. Empty means "no current context" (fall back to
	// the single-configured / picker rules).
	CurrentContext string   `yaml:"current_context,omitempty"`
	MountExcludes  []string `yaml:"mount_excludes,omitempty"`
}

// Context is a named server entry (kubectl-style).
type Context struct {
	Name    string
	URL     string
	Current bool
}

// Contexts returns the configured servers as named contexts, sorted by
// URL. A server's explicit Name wins; otherwise a name is derived from
// the host, disambiguated by port only when two un-named servers share a
// host. The Current flag marks the one matching CurrentContext (by name
// or, for older configs, by URL).
func (c *Config) Contexts() []Context {
	urls := c.ServerURLs()
	// Count host frequency among servers without an explicit name so we
	// only append :port when a bare host would be ambiguous.
	hostCount := map[string]int{}
	for _, u := range urls {
		if c.Servers[u].Name == "" {
			hostCount[hostOnly(u)]++
		}
	}
	out := make([]Context, 0, len(urls))
	for _, u := range urls {
		name := c.Servers[u].Name
		if name == "" {
			if h := hostOnly(u); hostCount[h] > 1 {
				name = hostPort(u)
			} else {
				name = h
			}
		}
		out = append(out, Context{
			Name:    name,
			URL:     u,
			Current: c.CurrentContext != "" && (name == c.CurrentContext || u == c.CurrentContext),
		})
	}
	return out
}

// ContextByName looks up a context by its (effective) name. It also
// accepts a raw context name that matches an explicit Server.Name.
func (c *Config) ContextByName(name string) (Context, bool) {
	for _, ctx := range c.Contexts() {
		if ctx.Name == name {
			return ctx, true
		}
	}
	return Context{}, false
}

// hostOnly returns the hostname of a canonical URL (no port).
func hostOnly(canonicalURL string) string {
	if u, err := url.Parse(canonicalURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return canonicalURL
}

// hostPort returns host:port of a canonical URL.
func hostPort(canonicalURL string) string {
	if u, err := url.Parse(canonicalURL); err == nil && u.Host != "" {
		return u.Host
	}
	return canonicalURL
}

// Path returns the absolute path to the pmox config file.
// It respects $XDG_CONFIG_HOME, falling back to $HOME/.config.
func Path() (string, error) {
	dir, err := paths.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// Load reads the config file. A missing file returns an empty Config.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{Servers: map[string]*Server{}}, nil
		}
		return nil, fmt.Errorf("read config %s: %w", p, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", p, err)
	}
	if cfg.Servers == nil {
		cfg.Servers = map[string]*Server{}
	}
	// Validate is deliberately not enforced here: a bad node_ssh block
	// must not break commands that never use node SSH. The resolver
	// records it for node-SSH users, and doctor reports it.
	return &cfg, nil
}

// Validate checks semantic constraints yaml decoding cannot express. It is
// deliberately lenient: only values pmox could never act on are rejected.
// Load does not call it (so one bad block can't break unrelated
// commands); `pmox doctor` reports its result.
func (c *Config) Validate() error {
	for _, u := range c.ServerURLs() {
		srv := c.Servers[u]
		if srv == nil || srv.NodeSSH == nil {
			continue
		}
		switch srv.NodeSSH.Auth {
		case "", AuthPassword, AuthKey:
		default:
			return fmt.Errorf("server %s: node_ssh.auth %q is not one of %q, %q",
				u, srv.NodeSSH.Auth, AuthPassword, AuthKey)
		}
	}
	return nil
}

// Save writes the config file atomically with mode 0600.
// The parent directory is created with mode 0700 if missing.
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := atomicfile.Write(p, data, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", p, err)
	}
	// Tighten a pre-existing config dir.
	_ = os.Chmod(filepath.Dir(p), 0o700)
	return nil
}

// AddServer adds or replaces the server entry keyed by url.
// The URL must already be canonicalized.
func (c *Config) AddServer(url string, s *Server) {
	if c.Servers == nil {
		c.Servers = map[string]*Server{}
	}
	c.Servers[url] = s
}

// RemoveServer removes the server entry and reports whether one was present.
func (c *Config) RemoveServer(url string) bool {
	if _, ok := c.Servers[url]; !ok {
		return false
	}
	delete(c.Servers, url)
	return true
}

// ServerURLs returns the configured canonical URLs, sorted.
func (c *Config) ServerURLs() []string {
	out := make([]string, 0, len(c.Servers))
	for k := range c.Servers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CanonicalizeURL normalizes a user-entered PVE API URL to a single
// canonical form: https://<lowercase-host>:<port>/api2/json.
//
// It is deliberately lenient about input so the common cases just work:
// a bare IP or hostname, host:port, an IPv6 literal ([::1]:8006), or a
// full URL (including a pasted web-UI address with a #fragment) are all
// accepted. A missing scheme becomes https; a missing port becomes 8006;
// an explicit port is honored. An http:// scheme is upgraded to https
// (PVE serves its API over TLS); any other scheme is an error.
func CanonicalizeURL(raw string) (string, error) {
	c, _, err := canonicalizeURL(raw)
	return c, err
}

// CanonicalizeURLVerbose is like CanonicalizeURL but also reports whether
// the scheme was upgraded from http to https, so interactive callers can
// print a one-line note.
func CanonicalizeURLVerbose(raw string) (canonical string, upgradedFromHTTP bool, err error) {
	return canonicalizeURL(raw)
}

func canonicalizeURL(raw string) (canonical string, upgradedFromHTTP bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, errors.New("url is empty")
	}
	// url.Parse treats a scheme-less "10.0.0.5" (or "pve.lan:8006") as a
	// path, leaving Host empty. Prepend https:// so the host/port parse.
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("parse url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "https":
	case "http":
		upgradedFromHTTP = true
	default:
		return "", false, fmt.Errorf("unsupported scheme %q; pmox requires https", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false, errors.New("url is missing host")
	}
	port := u.Port()
	if port == "" {
		port = "8006"
	}
	// Ignore any path, query, or fragment — pmox always targets /api2/json.
	// This lets users paste the web UI URL (e.g. https://host:8006/#v1:0:...)
	// or any other variant without needing to trim it first.
	// net.JoinHostPort re-adds brackets for IPv6 literals.
	return fmt.Sprintf("https://%s/api2/json", net.JoinHostPort(host, port)), upgradedFromHTTP, nil
}

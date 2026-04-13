// Package config loads, saves, and canonicalizes pmox's server configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Server is the persisted per-server configuration block.
//
// SSHPubkey is the path to a local public key file that pmox injects
// into cloud-init's ssh_authorized_keys on launch/clone. It has no
// relation to the NodeSSH block — that one is the *private* key used to
// SSH into the Proxmox node itself for snippet upload.
type Server struct {
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
}

// NodeSSH holds the SSH credentials pmox uses to reach the PVE node
// itself (for snippet upload during create-template). Password and key
// passphrase live in the keyring, not this struct.
type NodeSSH struct {
	User    string `yaml:"user"`              // default "root"
	Auth    string `yaml:"auth"`              // "password" | "key"
	KeyPath string `yaml:"key_path,omitempty"` // private key path when Auth == "key"
}

// Config is the top-level YAML shape on disk.
type Config struct {
	Servers       map[string]*Server `yaml:"servers"`
	MountExcludes []string           `yaml:"mount_excludes,omitempty"`
}

// Path returns the absolute path to the pmox config file.
// It respects $XDG_CONFIG_HOME, falling back to $HOME/.config.
func Path() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "pmox", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "pmox", "config.yaml"), nil
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
	return &cfg, nil
}

// Save writes the config file atomically with mode 0600.
// The parent directory is created with mode 0700 if missing.
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}
	// Ensure existing dir has the right mode.
	_ = os.Chmod(dir, 0o700)

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
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
func CanonicalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("url is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" {
		return "", fmt.Errorf("pmox requires https; got %s://...", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("url is missing host")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		port = "8006"
	}
	// Ignore any path, query, or fragment — pmox always targets /api2/json.
	// This lets users paste the web UI URL (e.g. https://host:8006/#v1:0:...)
	// or any other variant without needing to trim it first.
	return fmt.Sprintf("https://%s:%s/api2/json", host, port), nil
}

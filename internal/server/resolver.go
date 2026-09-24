// Package server resolves which configured Proxmox server a pmox
// command should target.
//
// Resolve implements a fixed precedence ladder:
//
//  1. --server <name|url> flag   (highest — explicit user intent)
//  2. --context <name> flag
//  3. PMOX_SERVER env var        (context name or URL)
//  4. PMOX_CONTEXT env var       (context name)
//  5. current context            (set via `pmox config use-context`)
//  6. exactly one configured server  (obvious default)
//  7. interactive picker             (TTY only; Options.Pick)
//  8. error                          (non-TTY + ambiguous)
//
// --server and PMOX_SERVER accept either a context name or a server URL;
// --context and PMOX_CONTEXT accept a context name only. A URL is
// canonicalized via config.CanonicalizeURL (https:// prepended when the
// scheme is missing). Prefix / substring matching is not supported.
//
// On success, Resolve returns a Resolved bundle containing the canonical
// URL, the *config.Server block, and the token secret fetched from the
// system keychain. A missing keychain entry for an otherwise-valid
// server surfaces as a resolver error, not a partial success.
package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pvessh"
)

// Options bundles the inputs Resolve needs. Everything is explicit
// (no implicit os.Stdin / os.Getenv) so tests can run hermetically.
type Options struct {
	Cfg        *config.Config
	Flag       string // value of --server (URL or context name), empty if unset
	Context    string // value of --context (context name), empty if unset
	Env        string // value of PMOX_SERVER (URL or context name)
	ContextEnv string // value of PMOX_CONTEXT (context name)

	// Pick draws the interactive context picker (rung 7) and returns the
	// chosen Choice.Value. Callers set it only when a picker may be shown
	// (TTY, input not disabled); nil falls through to the ambiguity
	// error. A non-nil error (e.g. the user aborted) is returned as-is —
	// Resolve never substitutes a default.
	Pick func(title string, choices []Choice) (string, error)
}

// Choice is one entry offered to Options.Pick.
type Choice struct {
	Label string
	Value string
}

// Resolved is the bundle returned on successful resolution.
type Resolved struct {
	URL    string
	Server *config.Server
	Secret string
	// Source is a human-readable label naming which rung of the
	// precedence ladder selected this server. One of:
	// "--server flag", "--context flag", "PMOX_SERVER env var",
	// "PMOX_CONTEXT env var", "current context", "single configured",
	// "interactive picker".
	Source string

	// Node-SSH credentials, populated from config + keyring when the
	// server record has a node_ssh block. Commands that don't need SSH
	// (everything except create-template) can ignore these fields.
	NodeSSHUser          string
	NodeSSHAuth          config.NodeSSHAuth // AuthPassword | AuthKey | "" (unconfigured)
	NodeSSHPassword      string             // populated when NodeSSHAuth == AuthPassword
	NodeSSHKeyPath       string             // populated when NodeSSHAuth == AuthKey
	NodeSSHKeyPassphrase string             // only when the key is passphrase-protected
}

// HasNodeSSH reports whether this server has SSH credentials resolved
// and is ready for snippet upload via pvessh.
func (r *Resolved) HasNodeSSH() bool {
	if r == nil || r.NodeSSHAuth == "" || r.NodeSSHUser == "" {
		return false
	}
	switch r.NodeSSHAuth {
	case config.AuthPassword:
		return r.NodeSSHPassword != ""
	case config.AuthKey:
		return r.NodeSSHKeyPath != ""
	}
	return false
}

// NodeSSHConfig builds the pvessh.Config for this server's PVE node: the
// API URL's hostname on port 22, the resolved node-SSH credentials, and
// the pmox-managed known_hosts file. insecure skips host-key checking.
func (r *Resolved) NodeSSHConfig(insecure bool) (pvessh.Config, error) {
	host, err := pvessh.HostFromURL(r.URL)
	if err != nil {
		return pvessh.Config{}, err
	}
	kh, err := pvessh.KnownHostsPath()
	if err != nil {
		return pvessh.Config{}, err
	}
	return pvessh.Config{
		Host:       host,
		User:       r.NodeSSHUser,
		Password:   r.NodeSSHPassword,
		KeyPath:    r.NodeSSHKeyPath,
		KeyPass:    r.NodeSSHKeyPassphrase,
		Insecure:   insecure,
		KnownHosts: kh,
	}, nil
}

// Resolve runs the precedence ladder and returns the resolved server.
func Resolve(ctx context.Context, opts Options) (*Resolved, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Cfg == nil {
		return nil, fmt.Errorf("%w: no server configured; run 'pmox init' to add one", exitcode.ErrNotFound)
	}

	// Rung 1: --server flag (context name or URL)
	if opts.Flag != "" {
		url, srv, err := matchNameOrURL(opts.Flag, opts.Cfg)
		if err != nil {
			return nil, err
		}
		return hydrate(url, srv, "--server flag")
	}

	// Rung 2: --context flag (context name)
	if opts.Context != "" {
		url, srv, err := matchName(opts.Context, opts.Cfg)
		if err != nil {
			return nil, err
		}
		return hydrate(url, srv, "--context flag")
	}

	// Rung 3: PMOX_SERVER env var (context name or URL)
	if opts.Env != "" {
		url, srv, err := matchNameOrURL(opts.Env, opts.Cfg)
		if err != nil {
			return nil, err
		}
		return hydrate(url, srv, "PMOX_SERVER env var")
	}

	// Rung 4: PMOX_CONTEXT env var (context name)
	if opts.ContextEnv != "" {
		url, srv, err := matchName(opts.ContextEnv, opts.Cfg)
		if err != nil {
			return nil, err
		}
		return hydrate(url, srv, "PMOX_CONTEXT env var")
	}

	// Rung 5: current context (pmox config use-context). A stale current
	// context (naming a server that no longer exists) is ignored so the
	// ladder falls through rather than hard-failing.
	if cur := opts.Cfg.CurrentContext; cur != "" {
		if c, ok := opts.Cfg.ContextByName(cur); ok {
			return hydrate(c.URL, opts.Cfg.Servers[c.URL], "current context")
		}
	}

	urls := opts.Cfg.ServerURLs()

	// Rung 6: single / zero configured
	switch len(urls) {
	case 0:
		return nil, fmt.Errorf("%w: no server configured; run 'pmox init' to add one", exitcode.ErrNotFound)
	case 1:
		return hydrate(urls[0], opts.Cfg.Servers[urls[0]], "single configured")
	}

	// Rung 7: interactive picker (only when the caller supplied one —
	// otherwise fall through to the error).
	if opts.Pick != nil {
		contexts := opts.Cfg.Contexts()
		choices := make([]Choice, 0, len(contexts))
		for _, c := range contexts {
			choices = append(choices, Choice{Label: fmt.Sprintf("%s (%s)", c.Name, c.URL), Value: c.URL})
		}
		selected, err := opts.Pick("Select context", choices)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		srv, ok := opts.Cfg.Servers[selected]
		if !ok {
			return nil, fmt.Errorf("picker returned unknown context %q", selected)
		}
		return hydrate(selected, srv, "interactive picker")
	}

	// Rung 8: non-TTY ambiguity
	return nil, fmt.Errorf("%w: multiple contexts configured; pick one with --context/--server, PMOX_CONTEXT/PMOX_SERVER, or set one with 'pmox config use-context'\n%s",
		exitcode.ErrUserInput, candidateList(contextLabels(opts.Cfg)))
}

// matchNameOrURL resolves input as a context name first, then as a URL.
func matchNameOrURL(input string, cfg *config.Config) (string, *config.Server, error) {
	in := strings.TrimSpace(input)
	if c, ok := cfg.ContextByName(in); ok {
		return c.URL, cfg.Servers[c.URL], nil
	}
	return matchInput(in, cfg)
}

// matchName resolves input strictly as a context name.
func matchName(input string, cfg *config.Config) (string, *config.Server, error) {
	in := strings.TrimSpace(input)
	c, ok := cfg.ContextByName(in)
	if !ok {
		return "", nil, fmt.Errorf("%w: no context named %q\n%s",
			exitcode.ErrUserInput, in, candidateList(contextLabels(cfg)))
	}
	return c.URL, cfg.Servers[c.URL], nil
}

// contextLabels renders "name (url)" for each configured context, for
// error messages.
func contextLabels(cfg *config.Config) []string {
	contexts := cfg.Contexts()
	out := make([]string, 0, len(contexts))
	for _, c := range contexts {
		out = append(out, fmt.Sprintf("%s (%s)", c.Name, c.URL))
	}
	return out
}

// matchInput canonicalizes raw input (prepending https:// if no scheme
// is present) and performs an exact lookup against the config map.
func matchInput(input string, cfg *config.Config) (string, *config.Server, error) {
	raw := strings.TrimSpace(input)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	canonical, err := config.CanonicalizeURL(raw)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %q is not a known context name or valid server URL: %w", exitcode.ErrUserInput, input, err)
	}
	srv, ok := cfg.Servers[canonical]
	if !ok {
		return "", nil, fmt.Errorf("%w: no configured context or server matches %q\n%s",
			exitcode.ErrUserInput, input, candidateList(contextLabels(cfg)))
	}
	return canonical, srv, nil
}

// hydrate fetches the token secret from the keychain and builds a
// *Resolved bundle. A missing keychain entry is a hard error.
func hydrate(url string, srv *config.Server, source string) (*Resolved, error) {
	secret, err := credstore.Get(url)
	if err != nil {
		if errors.Is(err, credstore.ErrNotFound) {
			return nil, fmt.Errorf("%w: secret for %s not found in keychain; re-run 'pmox init'", exitcode.ErrNotFound, url)
		}
		return nil, fmt.Errorf("load secret for %s: %w", url, err)
	}
	r := &Resolved{URL: url, Server: srv, Secret: secret, Source: source}
	if err := hydrateNodeSSH(r); err != nil {
		return nil, err
	}
	return r, nil
}

// hydrateNodeSSH loads node_ssh fields from config and the keyring into
// the resolved bundle. A server record without a node_ssh block leaves
// the fields empty — create-template is the only caller that cares and
// checks HasNodeSSH() before attempting to use them.
func hydrateNodeSSH(r *Resolved) error {
	if r.Server == nil || r.Server.NodeSSH == nil {
		return nil
	}
	ns := r.Server.NodeSSH
	r.NodeSSHUser = ns.EffectiveUser()
	r.NodeSSHAuth = ns.Auth
	switch ns.Auth {
	case "":
		// Block present but no auth mode: treated as unconfigured
		// (HasNodeSSH reports false).
	case config.AuthPassword:
		pw, err := credstore.GetNodeSSHPassword(r.URL)
		if err != nil {
			if errors.Is(err, credstore.ErrNotFound) {
				return fmt.Errorf("%w: node SSH password for %s missing from keychain; re-run 'pmox init'", exitcode.ErrNotFound, r.URL)
			}
			return fmt.Errorf("load node SSH password for %s: %w", r.URL, err)
		}
		r.NodeSSHPassword = pw
	case config.AuthKey:
		r.NodeSSHKeyPath = ns.KeyPath
		// Passphrase is optional — absent keyring entry is fine.
		pp, err := credstore.GetNodeSSHKeyPassphrase(r.URL)
		if err == nil {
			r.NodeSSHKeyPassphrase = pp
		} else if !errors.Is(err, credstore.ErrNotFound) {
			return fmt.Errorf("load node SSH key passphrase for %s: %w", r.URL, err)
		}
	default:
		return fmt.Errorf("%w: server %s has unknown node_ssh.auth %q (want %q or %q); re-run 'pmox init'",
			exitcode.ErrUserInput, r.URL, ns.Auth, config.AuthPassword, config.AuthKey)
	}
	return nil
}

// candidateList formats a sorted list of configured URLs for inclusion
// in error messages.
func candidateList(urls []string) string {
	if len(urls) == 0 {
		return "no servers configured"
	}
	var b strings.Builder
	b.WriteString("configured:\n")
	for i, u := range urls {
		b.WriteString("  - ")
		b.WriteString(u)
		if i < len(urls)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

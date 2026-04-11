// Package server resolves which configured Proxmox server a pmox
// command should target.
//
// Resolve implements a fixed five-step precedence ladder:
//
//  1. --server <url> flag  (highest — explicit user intent)
//  2. PMOX_SERVER env var  (shell session default)
//  3. exactly one configured server  (obvious default)
//  4. interactive picker            (TTY only)
//  5. error                         (non-TTY + ambiguous)
//
// Input supplied via the flag or env var is canonicalized via
// config.CanonicalizeURL before being matched. A bare hostname or
// hostname:port is accepted — https:// is prepended if the scheme is
// missing. Prefix / substring matching is deliberately not supported.
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
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// Options bundles the inputs Resolve needs. Everything is explicit
// (no implicit os.Stdin / os.Getenv) so tests can run hermetically.
type Options struct {
	Cfg    *config.Config
	Flag   string   // value of --server, empty if unset
	Env    string   // value of PMOX_SERVER, empty if unset
	Stdin  *os.File // for TTY detection + picker; os.Stdin in prod
	Stdout io.Writer
	Stderr io.Writer
}

// Resolved is the bundle returned on successful resolution.
type Resolved struct {
	URL    string
	Server *config.Server
	Secret string
}

// Resolve runs the precedence ladder and returns the resolved server.
func Resolve(ctx context.Context, opts Options) (*Resolved, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Cfg == nil {
		return nil, fmt.Errorf("%w: no server configured; run 'pmox configure' to add one", exitcode.ErrNotFound)
	}

	// Rung 1: --server flag
	if opts.Flag != "" {
		url, srv, err := matchInput(opts.Flag, opts.Cfg)
		if err != nil {
			return nil, err
		}
		return hydrate(url, srv)
	}

	// Rung 2: PMOX_SERVER env var
	if opts.Env != "" {
		url, srv, err := matchInput(opts.Env, opts.Cfg)
		if err != nil {
			return nil, err
		}
		return hydrate(url, srv)
	}

	urls := opts.Cfg.ServerURLs()

	// Rung 3: single / zero configured
	switch len(urls) {
	case 0:
		return nil, fmt.Errorf("%w: no server configured; run 'pmox configure' to add one", exitcode.ErrNotFound)
	case 1:
		return hydrate(urls[0], opts.Cfg.Servers[urls[0]])
	}

	// Rung 4: interactive picker (TTY only)
	if opts.Stdin != nil && term.IsTerminal(int(opts.Stdin.Fd())) {
		options := make([]huh.Option[string], 0, len(urls))
		for _, u := range urls {
			options = append(options, huh.NewOption(u, u))
		}
		selected := tui.SelectOne("Select server", options, urls[0])
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return hydrate(selected, opts.Cfg.Servers[selected])
	}

	// Rung 5: non-TTY ambiguity
	return nil, fmt.Errorf("%w: multiple servers configured; pick one with --server or PMOX_SERVER\n%s",
		exitcode.ErrUserInput, candidateList(urls))
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
		return "", nil, fmt.Errorf("%w: invalid --server/PMOX_SERVER value %q: %v", exitcode.ErrUserInput, input, err)
	}
	srv, ok := cfg.Servers[canonical]
	if !ok {
		return "", nil, fmt.Errorf("%w: no configured server matches %q\n%s",
			exitcode.ErrUserInput, input, candidateList(cfg.ServerURLs()))
	}
	return canonical, srv, nil
}

// hydrate fetches the token secret from the keychain and builds a
// *Resolved bundle. A missing keychain entry is a hard error.
func hydrate(url string, srv *config.Server) (*Resolved, error) {
	secret, err := credstore.Get(url)
	if err != nil {
		if errors.Is(err, credstore.ErrNotFound) {
			return nil, fmt.Errorf("%w: secret for %s not found in keychain; re-run 'pmox configure'", exitcode.ErrNotFound, url)
		}
		return nil, fmt.Errorf("load secret for %s: %w", url, err)
	}
	return &Resolved{URL: url, Server: srv, Secret: secret}, nil
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

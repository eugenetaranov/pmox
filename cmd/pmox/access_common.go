package main

import (
	"context"
	"fmt"
	"os"
	"os/user"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/accessreg"
	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// openRegistryFn opens the access registry on the cluster behind r, over
// the node SSH connection pmox already uses for snippet uploads. It is a
// seam so tests can substitute an in-memory filesystem.
var openRegistryFn = func(ctx context.Context, r *server.Resolved) (accessreg.FS, func(), error) {
	if err := r.RequireNodeSSH("the access registry"); err != nil {
		return nil, nil, err
	}
	c, err := dialPvessh(ctx, r)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to the node for %s: %w", r.URL, err)
	}
	return c, func() { _ = c.Close() }, nil
}

// localUsername is the registry name a person publishes under by default.
var localUsername = func() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// targetLabel names a resolved server for output: its context name
// (and host when that differs).
func targetLabel(cfg *config.Config, r *server.Resolved) string {
	return contextLabelFor(cfg, r.URL)
}

// selectContexts picks the clusters a key/access command acts on:
//   - --all-contexts: every configured server;
//   - --context / --server / env, or a single configured server: that one;
//   - otherwise, on a terminal, a multi-select of all contexts
//     (preselecting the current one); without a terminal, an error.
func selectContexts(ctx context.Context, all bool, multi bool) (*config.Config, []*server.Resolved, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	urls := cfg.ServerURLs()
	if len(urls) == 0 {
		return nil, nil, fmt.Errorf("%w: no server configured; run 'pmox init' first", exitcode.ErrNotFound)
	}
	resolveURL := func(u string) (*server.Resolved, error) {
		return server.Resolve(ctx, server.Options{Cfg: cfg, Flag: u})
	}

	explicit := serverFlag != "" || contextFlag != "" || os.Getenv("PMOX_SERVER") != "" || os.Getenv("PMOX_CONTEXT") != ""
	switch {
	case all:
		var out []*server.Resolved
		for _, u := range urls {
			r, err := resolveURL(u)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, r)
		}
		return cfg, out, nil
	case explicit || len(urls) == 1 || !multi:
		r, err := server.Resolve(ctx, server.Options{
			Cfg: cfg, Flag: serverFlag, Context: contextFlag,
			Env: os.Getenv("PMOX_SERVER"), ContextEnv: os.Getenv("PMOX_CONTEXT"),
			Pick: contextPicker(os.Stdin),
		})
		if err != nil {
			return nil, nil, err
		}
		return cfg, []*server.Resolved{r}, nil
	}

	if !tui.Interactive() {
		return nil, nil, fmt.Errorf("%w: %d servers are configured; pass --context <name> or --all-contexts", exitcode.ErrUserInput, len(urls))
	}
	opts := make([]huh.Option[string], 0, len(urls))
	for _, c := range cfg.Contexts() {
		label := fmt.Sprintf("%s  (%s)", c.Name, hostOnlyURL(c.URL))
		opts = append(opts, huh.NewOption(label, c.URL).Selected(c.Current))
	}
	picked, err := selectContextsFn("Which Proxmox hosts?", opts)
	if err != nil {
		return nil, nil, err
	}
	var out []*server.Resolved
	for _, u := range picked {
		r, err := resolveURL(u)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, r)
	}
	return cfg, out, nil
}

// selectContextsFn is a seam over the multi-select.
var selectContextsFn = tui.SelectMulti

func hostOnlyURL(u string) string { return hostPort(u) }

// addContextsFlag adds --all-contexts to a key/access command.
func addContextsFlag(cmd *cobra.Command, all *bool) {
	cmd.Flags().BoolVar(all, "all-contexts", false, "act on every configured server instead of picking")
}

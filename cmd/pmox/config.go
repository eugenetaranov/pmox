package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
)

// newConfigCmd is the `pmox config` group: kubectl-style management of
// contexts (configured servers) and the current context. Interactive
// setup stays under `pmox configure`; this group is the scriptable
// surface for listing, switching, renaming, and removing contexts.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage contexts (configured servers) and the current context",
		Long: `Manage pmox's contexts and which one commands target by default,
kubectl-style. A context is a configured server (URL + token + defaults +
node SSH), addressed by a short name.

Interactive setup lives in 'pmox configure'. Use this group to list,
switch, rename, and remove contexts. Set a current context so multi-server
setups don't need --context (or --server) every time.

Examples:
  pmox config get-contexts
  pmox config use-context prod
  pmox config current-context
  pmox config rename-context 192.168.0.185 prod
  pmox config delete-context lab`,
	}
	cmd.AddCommand(
		newGetContextsCmd(),
		newUseContextCmd(),
		newCurrentContextCmd(),
		newRenameContextCmd(),
		newDeleteContextCmd(),
		newConfigPathCmd(),
	)
	return cmd
}

func newGetContextsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get-contexts",
		Aliases: []string{"list", "ls"},
		Short:   "List contexts (the current one is marked *)",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return runGetContexts(cmd) },
	}
}

func runGetContexts(cmd *cobra.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	contexts := cfg.Contexts()
	w := cmd.OutOrStdout()

	if outputMode == "json" {
		type row struct {
			Name    string `json:"name"`
			URL     string `json:"server"`
			Current bool   `json:"current"`
		}
		out := struct {
			Contexts       []row  `json:"contexts"`
			CurrentContext string `json:"current_context,omitempty"`
		}{CurrentContext: cfg.CurrentContext}
		for _, c := range contexts {
			out.Contexts = append(out.Contexts, row{c.Name, c.URL, c.Current})
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(contexts) == 0 {
		fmt.Fprintln(w, "no contexts configured — run 'pmox configure'")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "CURRENT\tNAME\tSERVER")
	for _, c := range contexts {
		cur := ""
		if c.Current {
			cur = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", cur, c.Name, c.URL)
	}
	_ = tw.Flush()
	if cfg.CurrentContext == "" && len(contexts) > 1 {
		fmt.Fprintln(w, "\nNo current context — set one with 'pmox config use-context <name>'.")
	}
	return nil
}

func newUseContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "use-context <name>",
		Aliases: []string{"use"},
		Short:   "Set the current context that commands target",
		Args:    exactArgs(1, "pmox config use-context <name>", "pmox config use-context prod"),
		RunE:    func(cmd *cobra.Command, args []string) error { return runUseContext(cmd, args[0]) },
	}
}

func runUseContext(cmd *cobra.Command, name string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	c, ok := cfg.ContextByName(name)
	if !ok {
		return fmt.Errorf("%w: no context named %q (see 'pmox config get-contexts')", exitcode.ErrNotFound, name)
	}
	// Materialize the (possibly host-derived) name onto the server so the
	// current-context reference stays stable if other servers change.
	cfg.Servers[c.URL].Name = c.Name
	cfg.CurrentContext = c.Name
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "switched to context %q (%s)\n", c.Name, c.URL)
	return nil
}

func newCurrentContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "current-context",
		Aliases: []string{"current"},
		Short:   "Show the current context",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return runCurrentContext(cmd) },
	}
}

func runCurrentContext(cmd *cobra.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if cfg.CurrentContext != "" {
		if c, ok := cfg.ContextByName(cfg.CurrentContext); ok {
			fmt.Fprintln(w, c.Name)
			return nil
		}
	}
	contexts := cfg.Contexts()
	switch len(contexts) {
	case 0:
		fmt.Fprintln(w, "no contexts configured — run 'pmox configure'")
	case 1:
		fmt.Fprintf(w, "%s (only context; used automatically)\n", contexts[0].Name)
	default:
		fmt.Fprintf(w, "no current context set (%d configured) — run 'pmox config use-context <name>'\n", len(contexts))
	}
	return nil
}

func newRenameContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename-context <old-name> <new-name>",
		Short: "Give a context a new name",
		Args:  exactArgs(2, "pmox config rename-context <old-name> <new-name>", "pmox config rename-context 192.168.0.185 prod"),
		RunE:  func(cmd *cobra.Command, args []string) error { return runRenameContext(cmd, args[0], args[1]) },
	}
}

func runRenameContext(cmd *cobra.Command, oldName, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("%w: new context name must not be empty", exitcode.ErrUserInput)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	c, ok := cfg.ContextByName(oldName)
	if !ok {
		return fmt.Errorf("%w: no context named %q (see 'pmox config get-contexts')", exitcode.ErrNotFound, oldName)
	}
	if newName != c.Name {
		if _, exists := cfg.ContextByName(newName); exists {
			return fmt.Errorf("%w: a context named %q already exists", exitcode.ErrUserInput, newName)
		}
	}
	cfg.Servers[c.URL].Name = newName
	if cfg.CurrentContext == c.Name || cfg.CurrentContext == oldName {
		cfg.CurrentContext = newName
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "renamed context %q to %q\n", c.Name, newName)
	return nil
}

func newDeleteContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete-context <name>",
		Aliases: []string{"remove", "rm"},
		Short:   "Remove a context (server) and its stored secrets",
		Args:    exactArgs(1, "pmox config delete-context <name>", "pmox config delete-context lab"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			c, ok := cfg.ContextByName(args[0])
			if !ok {
				return fmt.Errorf("%w: no context named %q (see 'pmox config get-contexts')", exitcode.ErrNotFound, args[0])
			}
			return runRemove(newStdPrompter(ctx), c.URL)
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the path to the pmox config file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := config.Path()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), p)
			return nil
		},
	}
}

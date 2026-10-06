package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// newConfigCmd is the `pmox config` group: editing what's configured.
// Context management moved to 'pmox context'; the old kubectl-style
// '*-context' verbs stay here as deprecated forms for the deprecation
// window.
func newConfigCmd() *cobra.Command {
	g := nounGroup("config", "Edit configuration", `Edit pmox's configuration.

First-time setup lives in 'pmox init'; switching between servers in
'pmox context'.

Examples:
  pmox config edit prod
  pmox config path
  pmox config cloud-init --regenerate`,
		newEditContextCmd(),
		newConfigPathCmd(),
		newConfigCloudInitCmd(),
	)
	g.AddCommand(
		deprecated(newGetContextsCmd(), "pmox context list"),
		deprecated(newUseContextCmd(), "pmox context use"),
		deprecated(newCurrentContextCmd(), "pmox context current"),
		deprecated(newRenameContextCmd(), "pmox context rename"),
		deprecated(newDeleteContextCmd(), "pmox context delete"),
	)
	return g
}

// newConfigCloudInitCmd shows, or with --regenerate rewrites, the
// per-server cloud-init template (formerly 'pmox init --regen-cloud-init').
func newConfigCloudInitCmd() *cobra.Command {
	var regenerate bool
	cmd := &cobra.Command{
		Use:   "cloud-init",
		Short: "Show or regenerate the cloud-init template",
		Long: `Print the path of the current server's cloud-init template. With
--regenerate, rewrite it from the stored user and SSH public key (on a
terminal you can pick a different key first); existing edits are lost,
so it asks before overwriting.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if regenerate {
				return runRegenCloudInit(newStdPrompter(cmd.Context()))
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			urls := cfg.ServerURLs()
			if len(urls) == 0 {
				return fmt.Errorf("%w: no server configured; run 'pmox init'", exitcode.ErrNotFound)
			}
			for _, u := range urls {
				p, err := config.CloudInitPath(u)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", contextLabelFor(cfg, u), p)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&regenerate, "regenerate", false, "rewrite the template from the stored user and SSH key")
	return cmd
}

func newGetContextsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get-contexts",
		Aliases: []string{"list", "ls"},
		Short:   "List configured servers",
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
		return printJSON(w, out)
	}

	if len(contexts) == 0 {
		fmt.Fprintln(w, "no contexts configured — run 'pmox init'")
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
		fmt.Fprintln(w, "\nNo current context — set one with 'pmox context use <name>'.")
	}
	return nil
}

func newUseContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "use-context [name]",
		Aliases: []string{"use"},
		Short:   "Choose the server commands target",
		Long: `Set the current context. With no argument, pick one interactively
from the configured contexts (on a terminal).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runUseContext(cmd, name)
		},
	}
}

func runUseContext(cmd *cobra.Command, name string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if name == "" {
		picked, err := pickContext(cfg)
		if err != nil {
			return err
		}
		name = picked
	}
	c, ok := cfg.ContextByName(name)
	if !ok {
		return fmt.Errorf("%w: no context named %q (see 'pmox context list')", exitcode.ErrNotFound, name)
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
		fmt.Fprintln(w, "no contexts configured — run 'pmox init'")
	case 1:
		fmt.Fprintf(w, "%s (only context; used automatically)\n", contexts[0].Name)
	default:
		fmt.Fprintf(w, "no current context set (%d configured) — run 'pmox context use <name>'\n", len(contexts))
	}
	return nil
}

func newRenameContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename-context <old-name> <new-name>",
		Short: "Give a context a new name",
		Args:  exactArgs(2, "pmox context rename <old-name> <new-name>", "pmox context rename 192.168.0.185 prod"),
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
		return fmt.Errorf("%w: no context named %q (see 'pmox context list')", exitcode.ErrNotFound, oldName)
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
		Short:   "Forget a server and its secrets",
		Args:    exactArgs(1, "pmox context delete <name>", "pmox context delete lab"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			c, ok := cfg.ContextByName(args[0])
			if !ok {
				return fmt.Errorf("%w: no context named %q (see 'pmox context list')", exitcode.ErrNotFound, args[0])
			}
			return runRemove(newStdPrompter(ctx), c.URL)
		},
	}
}

func newEditContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit [context]",
		Short: "Change a configured server's settings",
		Long: `Reopens the 'pmox init' wizard for an already-configured context,
landing straight on the Review screen instead of redoing the whole
connection/token setup. The stored token is reused as-is; reachability
and the token are re-verified first, with nothing re-typed — if either
is broken, this points you at 'pmox init' to fix the connection
instead, since that isn't what edit is for.

From Review you can jump back to Defaults (node/template/storage/
snippet-storage/bridge) or Access (SSH key/user/node SSH) and change
anything; every field starts pre-filled with its current value instead
of a blank re-discovery. Nothing is written until you confirm.

With no argument, picks the context interactively when more than one
is configured. Requires an interactive terminal.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigEdit(cmd, args)
		},
	}
}

func runConfigEdit(cmd *cobra.Command, args []string) error {
	if !interactiveFn() {
		return fmt.Errorf("%w: 'pmox config edit' requires an interactive terminal", exitcode.ErrUserInput)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	name := ""
	if len(args) == 1 {
		name = args[0]
	} else {
		name, err = pickContext(cfg)
		if err != nil {
			return err
		}
	}
	c, ok := cfg.ContextByName(name)
	if !ok {
		return fmt.Errorf("%w: no context named %q (see 'pmox context list')", exitcode.ErrNotFound, name)
	}
	ctx := cmd.Context()
	return runEditForm(ctx, newStdPrompter(ctx), cfg, c.URL)
}

// pickContext resolves a context name interactively, honoring the
// non-obtrusive rules: an explicit name (handled by the caller) always
// wins; with no name and no interactivity available, it errors with the
// list of contexts rather than prompting.
func pickContext(cfg *config.Config) (string, error) {
	contexts := cfg.Contexts()
	switch len(contexts) {
	case 0:
		return "", fmt.Errorf("%w: no contexts configured — run 'pmox init'", exitcode.ErrNotFound)
	case 1:
		return contexts[0].Name, nil
	}
	if !tui.Interactive() {
		var b strings.Builder
		for _, c := range contexts {
			fmt.Fprintf(&b, "  - %s (%s)\n", c.Name, c.URL)
		}
		return "", fmt.Errorf("%w: pass a context name — no terminal for the interactive picker. contexts:\n%s", exitcode.ErrUserInput, strings.TrimRight(b.String(), "\n"))
	}
	opts := make([]huh.Option[string], 0, len(contexts))
	for _, c := range contexts {
		label := fmt.Sprintf("%s (%s)", c.Name, c.URL)
		if c.Current {
			label += " (current)"
		}
		opts = append(opts, huh.NewOption(label, c.Name))
	}
	return tui.Select("Select a context", opts)
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the config file path",
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

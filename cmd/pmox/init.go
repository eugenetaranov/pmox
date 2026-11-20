package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/setup"
)

// initFlags holds 'pmox init's flags. --list / --remove /
// --regen-cloud-init are deprecated modes kept working for the
// deprecation window (see 'pmox context' and 'pmox config cloud-init').
type initFlags struct {
	list       bool
	remove     string
	regenCloud bool
}

func newInitCmd() *cobra.Command {
	f := &initFlags{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up a Proxmox connection",
		Long: `Set up a Proxmox VE server for pmox: its API URL and token, the default
node, template, storage, snippet storage and bridge, your SSH public
key, and SSH access to the node (used to upload cloud-init snippets).

On a terminal this is a single-screen wizard (Connection › Defaults ›
Access › Review); nothing is written until you confirm on the Review
page. Esc goes back a page, Ctrl-C quits without saving. Without a
terminal, or with --no-input, the same questions are asked as plain
prompts.

The URL can be a bare IP or hostname, host:port, or the address copied
from the web UI. For the token, paste one made in the web UI
(Datacenter → Permissions → API Tokens), or let pmox create it: enter
user@realm and a password, which is used once to log in and never
stored.

Secrets go to the system keychain; everything else to
~/.config/pmox/config.yaml. On a machine that is already set up, init
offers to edit, add or remove a server instead of starting over.`,
		RunE: func(cmd *cobra.Command, args []string) error { return runInit(cmd, f) },
	}
	cmd.Flags().BoolVar(&f.list, "list", false, "List configured server URLs")
	cmd.Flags().StringVar(&f.remove, "remove", "", "Remove a configured server by URL")
	cmd.Flags().BoolVar(&f.regenCloud, "regen-cloud-init", false, "Rewrite the per-server cloud-init template with stored user+pubkey")
	// Deprecated modes: hidden from help, still working, with a note on
	// stderr (see runInit).
	for _, name := range []string{"list", "remove", "regen-cloud-init"} {
		_ = cmd.Flags().MarkHidden(name)
	}
	return cmd
}

func runInit(cmd *cobra.Command, f *initFlags) error {
	ctx := cmd.Context()
	// Checked here rather than with cobra's MarkFlagsMutuallyExclusive:
	// cobra validates flag groups before RunE, so its error can't carry
	// exitcode.ErrUserInput.
	modes := 0
	for _, set := range []bool{f.list, f.remove != "", f.regenCloud} {
		if set {
			modes++
		}
	}
	if modes > 1 {
		return fmt.Errorf("%w: --list, --remove, and --regen-cloud-init are mutually exclusive", exitcode.ErrUserInput)
	}
	deprecatedNote := func(old, replacement string) {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: 'pmox init %s' is deprecated and will be removed — use '%s'\n", old, replacement)
	}
	if f.list {
		deprecatedNote("--list", "pmox context list")
		return runList(newStdPrompter(ctx))
	}
	if f.remove != "" {
		deprecatedNote("--remove", "pmox context delete <name>")
		return runRemove(newStdPrompter(ctx), f.remove)
	}
	if f.regenCloud {
		deprecatedNote("--regen-cloud-init", "pmox config cloud-init --regenerate")
		return runRegenCloudInit(newStdPrompter(ctx))
	}
	p := newStdPrompter(ctx)
	if interactiveFn() {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if len(cfg.Servers) > 0 {
			return runInitConfigured(ctx, p, cfg)
		}
	}
	return runInteractive(ctx, p)
}

func runList(p prompter) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	urls := cfg.ServerURLs()
	if len(urls) == 0 {
		p.Printf("no servers configured\n")
		return nil
	}
	for _, u := range urls {
		p.Printf("%s\n", u)
	}
	return nil
}

func runRemove(p prompter, rawURL string) error {
	canonical, err := setup.RemoveServer(rawURL)
	if err != nil {
		return err
	}
	p.Printf("removed %s\n", canonical)
	return nil
}

// runInteractive dispatches to the form-based flow on a terminal, or the
// linear prompt flow when input is non-interactive (pipes/CI/--no-input).
func runInteractive(ctx context.Context, p prompter) error {
	if interactiveFn() {
		return runInteractiveForm(ctx, p)
	}
	return runInteractiveLinear(ctx, p)
}

func runInteractiveLinear(ctx context.Context, p prompter) error {
	// Step 1: URL + reachability probe (before any credential prompt). The
	// probe's TLS decision (strict vs insecure) is reused below so the user
	// is never warned or handshaked twice.
	canonical, insecure, err := promptReachableURL(ctx, p)
	if err != nil {
		return err
	}

	// Step 2: check overwrite
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if _, exists := cfg.Servers[canonical]; exists {
		ans, err := p.Prompt(fmt.Sprintf("Server %s is already configured. Overwrite? [y/N]: ", canonical))
		if err != nil {
			return err
		}
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			p.Printf("aborted; no changes\n")
			return nil
		}
	}

	// Re-configuring a pinned server: every credentialed connection below
	// is checked against the stored pin (empty on a first-ever connect),
	// or against a new certificate the user re-pinned — decided before
	// any credential is prompted for or sent.
	pin, err := resolveInitPin(ctx, p, cfg, canonical, insecure, "")
	if err != nil {
		return err
	}

	// Steps 3–4: acquire an API token — paste an existing one or log in
	// and generate one.
	tokenID, secret, err := acquireToken(ctx, p, canonical, insecure, pin)
	if err != nil {
		return err
	}

	// Step 5: validate credentials, reusing the probe's TLS decision.
	insecure, err = validateCredentials(ctx, p, canonical, tokenID, secret, insecure, pin)
	if err != nil {
		return err
	}

	// Steps 7–10: auto-discovery pickers
	client := newInitClient(canonical, tokenID, secret, insecure, pin)
	defs, err := discoverDefaults(ctx, p, client, defaultsAnswers{})
	if err != nil {
		return err
	}

	// Step 11: SSH key
	sshKey, err := promptSSHKey(p, "")
	if err != nil {
		return err
	}

	// Step 12: default user — pre-filled with the value already
	// configured for this server, if any, instead of always suggesting
	// "ubuntu" on a reconfigure.
	user, err := promptDefaultUser(p, configuredUser(cfg, canonical))
	if err != nil {
		return err
	}

	// Step 12.5: node SSH credentials for snippet upload.
	nodeSSH, sshPassword, sshKeyPass, err := promptNodeSSH(ctx, p, canonical)
	if err != nil {
		return err
	}

	return persistServer(ctx, p, cfg, persistInput{
		canonical: canonical, tokenID: tokenID, secret: secret, insecure: insecure, pin: pin,
		node: defs.node, template: defs.template, storage: defs.storage,
		snippetStorage: defs.snippetStorage, bridge: defs.bridge,
		sshKey: sshKey, user: user, nodeSSH: nodeSSH, sshPassword: sshPassword, sshKeyPass: sshKeyPass,
	})
}

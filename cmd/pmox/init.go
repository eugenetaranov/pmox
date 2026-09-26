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

var (
	configureList         bool
	configureRemove       string
	configureRegenCloudCI bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize pmox: configure a Proxmox VE server",
	Long: `Interactively configure credentials and defaults for a Proxmox VE server.

Walks through API URL, token, credential validation against /version, and
auto-discovery of node, template, storage, snippet storage, and bridge,
then collects SSH credentials for the PVE node (used by 'pmox create-template'
to upload cloud-init snippets via SFTP) and validates them with a live
handshake. Snippet storage is picked separately from VM disk storage —
if no storage on the cluster has the 'snippets' content type, configure
offers to enable it on an existing directory-backed storage.
Secrets are stored in the system keychain; everything else is written to
$XDG_CONFIG_HOME/pmox/config.yaml (or ~/.config/pmox/config.yaml).

Prompts:
  Proxmox API URL   base URL of the PVE host, e.g. https://192.168.0.185:8006
                    (the web UI URL with '#v1:...' also works — the path is
                    stripped automatically)
  API token ID      in the form 'user@realm!tokenname', e.g. 'root@pam!pmox'
                    or 'pmox@pve!mytoken'. Create one in the PVE web UI under
                    Datacenter → Permissions → API Tokens → Add.
  API token secret  the UUID shown once when the token is created.
  Node SSH user     Linux user pmox SSHs into on the PVE node (default: root).
  Node SSH auth     'p' for password or 'k' for a private key file.
  Node SSH secret   password or key passphrase, stored in the OS keyring.
                    First-time connections prompt to pin the host key into
                    ~/.config/pmox/known_hosts (bypass with --ssh-insecure).`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().BoolVar(&configureList, "list", false, "List configured server URLs")
	initCmd.Flags().StringVar(&configureRemove, "remove", "", "Remove a configured server by URL")
	initCmd.Flags().BoolVar(&configureRegenCloudCI, "regen-cloud-init", false, "Rewrite the per-server cloud-init template with stored user+pubkey")
	// Registration (and help grouping) happens in main.go's init so all
	// command wiring lives in one place.
}

func runInit(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	// Checked here rather than with cobra's MarkFlagsMutuallyExclusive:
	// cobra validates flag groups before RunE, so its error can't carry
	// exitcode.ErrUserInput.
	modes := 0
	for _, set := range []bool{configureList, configureRemove != "", configureRegenCloudCI} {
		if set {
			modes++
		}
	}
	if modes > 1 {
		return fmt.Errorf("%w: --list, --remove, and --regen-cloud-init are mutually exclusive", exitcode.ErrUserInput)
	}
	if configureList {
		return runList(newStdPrompter(ctx))
	}
	if configureRemove != "" {
		return runRemove(newStdPrompter(ctx), configureRemove)
	}
	if configureRegenCloudCI {
		return runRegenCloudInit(newStdPrompter(ctx))
	}
	return runInteractive(ctx, newStdPrompter(ctx))
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
	defs, err := discoverDefaults(ctx, p, client)
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

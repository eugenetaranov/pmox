package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/setup"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// persistInput bundles everything init collects for a server, ready to
// write to config + the secret store.
type persistInput struct {
	canonical      string
	tokenID        string
	secret         string
	insecure       bool
	pin            string // stored TLS pin the connection was verified against ("" = none)
	node           string
	template       string
	storage        string
	snippetStorage string
	bridge         string
	sshKey         string
	user           string
	nodeSSH        *config.NodeSSH
	sshPassword    string
	sshKeyPass     string
}

// persistServer writes the collected server config, stores its secrets,
// and writes the starter cloud-init template. Used by the linear init
// flow; the interactive wizard composes the same cores (persistCore,
// ensureCloudInit, regenCloudInit) with in-app dialogs instead.
func persistServer(ctx context.Context, p prompter, cfg *config.Config, in persistInput) error {
	srv, notices, err := persistCore(cfg, in)
	if err != nil {
		return err
	}
	printNotices(p, notices)
	// Starter cloud-init is non-fatal — creds are saved; user can rerun
	// with --regen-cloud-init.
	writeInitialCloudInit(p, in.canonical, in.user, in.sshKey)

	if in.template == createTemplateSentinel {
		if err := offerBuiltTemplate(ctx, p, in, srv); err != nil {
			p.Errf("warning: building the template failed: %v\n", err)
			p.Errf("no default template is set — run 'pmox create-template' when ready, then set one with a fresh 'pmox init' (or edit the config file's 'template:' field).\n")
		}
	}
	return nil
}

// persistCore saves the server config and its secrets and returns the
// saved server plus the "configured server" / "config saved to" lines.
// It never prompts and never builds a template.
func persistCore(cfg *config.Config, in persistInput) (*config.Server, []notice, error) {
	// The picker's "build a new template now" choice isn't a real
	// template value — leave the field unset until offerBuiltTemplate
	// either patches in the built template's real VMID or, on failure,
	// leaves it unset with a clear warning. The sentinel must never land
	// in the saved config: anything that later reads srv.Template as a
	// real VMID/name (e.g. launch's resolveTemplate) would just fail
	// confusingly instead of saying "no template set".
	template := in.template
	if template == createTemplateSentinel {
		template = ""
	}
	srv := &config.Server{
		TokenID:        in.tokenID,
		Node:           in.node,
		Template:       template,
		Storage:        in.storage,
		SnippetStorage: in.snippetStorage,
		Bridge:         in.bridge,
		SSHPubkey:      in.sshKey,
		User:           in.user,
		Insecure:       in.insecure,
		NodeSSH:        in.nodeSSH,
		// Keep a pin that this run verified against; a strict (CA-verified)
		// connection drops it, as does a first-ever connect (TOFU later).
		TLSPinSHA256: setup.PinOptions(in.insecure, in.pin).PinSHA256,
	}
	if err := setup.SaveServer(cfg, in.canonical, srv, setup.Secrets{
		Token:                in.secret,
		NodeSSHPassword:      in.sshPassword,
		NodeSSHKeyPassphrase: in.sshKeyPass,
	}); err != nil {
		return nil, nil, err
	}

	notices := []notice{infoNotice("configured server " + in.canonical)}
	if path, perr := config.Path(); perr == nil {
		home, _ := os.UserHomeDir()
		notices = append(notices, infoNotice("config saved to "+displayPath(path, home)))
	}
	return srv, notices, nil
}

// writeInitialCloudInit renders and writes the per-server cloud-init
// starter on first configure, asking before regenerating a file that
// authorizes a different key. Errors are warned to stderr but never
// returned.
func writeInitialCloudInit(p prompter, canonicalURL, user, sshKeyPath string) {
	r := ensureCloudInit(canonicalURL, user, sshKeyPath)
	printNotices(p, r.notices)
	if !r.drift {
		return
	}
	// Only offer to regenerate when the selected key isn't already
	// authorized — so re-running configure with the same key never
	// nags and never clobbers user edits silently.
	ans, _ := p.Prompt("Regenerate it now with the selected user + key? (existing edits will be lost) [y/N]: ")
	printNotices(p, r.resolveDrift(strings.EqualFold(strings.TrimSpace(ans), "y")))
}

// cloudInitResult is the outcome of trying to write the starter
// cloud-init file. When drift is true the existing file authorizes a
// different key and the caller must ask whether to regenerate it
// (resolveDrift).
type cloudInitResult struct {
	notices []notice
	drift   bool
	path    string
	user    string
	pubkey  string
}

// ensureCloudInit writes the starter cloud-init file unless one exists,
// without prompting. See cloudInitResult.
func ensureCloudInit(canonicalURL, user, sshKeyPath string) cloudInitResult {
	path, err := config.CloudInitPath(canonicalURL)
	if err != nil {
		return cloudInitResult{notices: []notice{warnNotice(fmt.Sprintf("warning: could not resolve cloud-init path: %v", err))}}
	}
	pubkeyContent, err := readSSHKey(sshKeyPath)
	if err != nil {
		return cloudInitResult{notices: []notice{warnNotice(fmt.Sprintf("warning: could not read ssh pubkey %s: %v", sshKeyPath, err))}}
	}
	home, _ := os.UserHomeDir()
	r := cloudInitResult{path: path, user: user, pubkey: pubkeyContent}
	switch err := config.EnsureStarterCloudInit(path, user, pubkeyContent); {
	case err == nil:
		r.notices = []notice{infoNotice(fmt.Sprintf("wrote cloud-init template to %s — edit it to customize packages, users, runcmd", displayPath(path, home)))}
	case errors.Is(err, config.ErrCloudInitKeyDrift):
		r.drift = true
		r.notices = []notice{infoNotice(fmt.Sprintf("cloud-init at %s authorizes a different SSH key than the one you selected.", displayPath(path, home)))}
	case errors.Is(err, config.ErrCloudInitExists):
		r.notices = []notice{infoNotice(fmt.Sprintf("cloud-init template already exists at %s — not overwriting", displayPath(path, home)))}
	default:
		r.notices = []notice{warnNotice(fmt.Sprintf("warning: could not write cloud-init template to %s: %v", path, err))}
	}
	return r
}

// resolveDrift applies the user's regenerate decision for a drifted
// cloud-init file and returns the lines describing what happened.
func (r cloudInitResult) resolveDrift(regenerate bool) []notice {
	home, _ := os.UserHomeDir()
	if !regenerate {
		return []notice{infoNotice(fmt.Sprintf("cloud-init template already exists at %s — not overwriting", displayPath(r.path, home)))}
	}
	if err := config.WriteCloudInit(r.path, r.user, r.pubkey); err != nil {
		return []notice{warnNotice(fmt.Sprintf("warning: could not regenerate cloud-init: %v", err))}
	}
	return []notice{infoNotice(fmt.Sprintf("regenerated cloud-init at %s — relaunch existing VMs to apply the new key", displayPath(r.path, home)))}
}

// runRegenCloudInit rewrites the per-server cloud-init template from
// the stored user+pubkey. If more than one server is configured, it
// prompts the user to pick one. If the target file already exists, it
// prompts for overwrite confirmation before clobbering user edits.
func runRegenCloudInit(p prompter) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	urls := cfg.ServerURLs()
	if len(urls) == 0 {
		return fmt.Errorf("no servers configured; run 'pmox init' first")
	}

	var canonical string
	switch len(urls) {
	case 1:
		canonical = urls[0]
	default:
		opts := make([]huh.Option[string], 0, len(urls))
		for _, u := range urls {
			opts = append(opts, huh.NewOption(u, u))
		}
		canonical, err = tui.SelectOne("Select server", opts, urls[0])
		if err != nil {
			return err
		}
		if canonical == "" {
			return fmt.Errorf("%w: no server selected", exitcode.ErrUserInput)
		}
	}

	srv := cfg.Servers[canonical]
	if srv == nil {
		return fmt.Errorf("server %s not found in config", canonical)
	}
	user := srv.User
	if user == "" {
		user = "ubuntu"
	}

	// On a terminal, let the operator pick a (possibly different) key,
	// defaulting to the currently-configured one — so `--regen-cloud-init`
	// is the one-stop "change the key + rewrite cloud-init" command. A
	// changed key is persisted so future launches use it too.
	keyPath := srv.SSHPubkey
	if tui.Interactive() {
		picked, perr := promptSSHKey(p, srv.SSHPubkey)
		if perr != nil {
			return perr
		}
		keyPath = picked
	}
	if keyPath == "" {
		return fmt.Errorf("server %s has no ssh_pubkey configured; run 'pmox init' to set one", canonical)
	}
	if keyPath != srv.SSHPubkey {
		srv.SSHPubkey = keyPath
		if serr := cfg.Save(); serr != nil {
			return fmt.Errorf("save config: %w", serr)
		}
		p.Printf("updated ssh_pubkey for %s to %s\n", canonical, keyPath)
	}
	pubkeyContent, err := readSSHKey(keyPath)
	if err != nil {
		return fmt.Errorf("read ssh pubkey %s: %w", keyPath, err)
	}

	path, err := config.CloudInitPath(canonical)
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(path); statErr == nil {
		ans, err := p.Prompt(fmt.Sprintf("cloud-init template %s already exists — overwrite? [y/N]: ", path))
		if err != nil {
			return err
		}
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			p.Printf("aborted; %s not modified\n", path)
			return nil
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, statErr)
	}

	if err := config.WriteCloudInit(path, user, pubkeyContent); err != nil {
		return fmt.Errorf("write cloud-init template: %w", err)
	}
	home, _ := os.UserHomeDir()
	p.Printf("wrote cloud-init template to %s\n", displayPath(path, home))
	return nil
}

func displayPath(path, home string) string {
	if home != "" && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

package main

import (
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
// and writes the starter cloud-init template. Shared by the linear and
// form init flows.
func persistServer(p prompter, cfg *config.Config, in persistInput) error {
	srv := &config.Server{
		TokenID:        in.tokenID,
		Node:           in.node,
		Template:       in.template,
		Storage:        in.storage,
		SnippetStorage: in.snippetStorage,
		Bridge:         in.bridge,
		SSHPubkey:      in.sshKey,
		User:           in.user,
		Insecure:       in.insecure,
		NodeSSH:        in.nodeSSH,
	}
	if err := setup.SaveServer(cfg, in.canonical, srv, setup.Secrets{
		Token:                in.secret,
		NodeSSHPassword:      in.sshPassword,
		NodeSSHKeyPassphrase: in.sshKeyPass,
	}); err != nil {
		return err
	}

	p.Printf("configured server %s\n", in.canonical)
	if path, perr := config.Path(); perr == nil {
		home, _ := os.UserHomeDir()
		p.Printf("config saved to %s\n", displayPath(path, home))
	}
	// Starter cloud-init is non-fatal — creds are saved; user can rerun
	// with --regen-cloud-init.
	writeInitialCloudInit(p, in.canonical, in.user, in.sshKey)
	return nil
}

// writeInitialCloudInit renders and writes the per-server cloud-init
// starter on first configure. It reads the SSH pubkey file named by
// sshKeyPath, calls WriteStarterCloudInit, and prints a human message
// for each outcome. Errors are warned to stderr but never returned.
func writeInitialCloudInit(p prompter, canonicalURL, user, sshKeyPath string) {
	path, err := config.CloudInitPath(canonicalURL)
	if err != nil {
		p.Errf("warning: could not resolve cloud-init path: %v\n", err)
		return
	}
	pubkeyContent, err := readSSHKey(sshKeyPath)
	if err != nil {
		p.Errf("warning: could not read ssh pubkey %s: %v\n", sshKeyPath, err)
		return
	}
	home, _ := os.UserHomeDir()
	switch err := config.EnsureStarterCloudInit(path, user, pubkeyContent); {
	case err == nil:
		p.Printf("wrote cloud-init template to %s — edit it to customize packages, users, runcmd\n", displayPath(path, home))
	case errors.Is(err, config.ErrCloudInitKeyDrift):
		// Only offer to regenerate when the selected key isn't already
		// authorized — so re-running configure with the same key never
		// nags and never clobbers user edits silently.
		p.Printf("cloud-init at %s authorizes a different SSH key than the one you selected.\n", displayPath(path, home))
		ans, _ := p.Prompt("Regenerate it now with the selected user + key? (existing edits will be lost) [y/N]: ")
		if !strings.EqualFold(strings.TrimSpace(ans), "y") {
			p.Printf("cloud-init template already exists at %s — not overwriting\n", displayPath(path, home))
			return
		}
		if werr := config.WriteCloudInit(path, user, pubkeyContent); werr != nil {
			p.Errf("warning: could not regenerate cloud-init: %v\n", werr)
		} else {
			p.Printf("regenerated cloud-init at %s — relaunch existing VMs to apply the new key\n", displayPath(path, home))
		}
	case errors.Is(err, config.ErrCloudInitExists):
		p.Printf("cloud-init template already exists at %s — not overwriting\n", displayPath(path, home))
	default:
		p.Errf("warning: could not write cloud-init template to %s: %v\n", path, err)
	}
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

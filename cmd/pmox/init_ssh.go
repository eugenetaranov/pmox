package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pvessh"
	"github.com/eugenetaranov/pmox/internal/sshkey"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// Test seams for SSH validation and host-key pinning. In production
// these delegate to pvessh; tests replace them with in-process stubs.
var (
	sshValidateFn = func(ctx context.Context, cfg pvessh.Config) error {
		c, err := pvessh.Dial(ctx, cfg)
		if err != nil {
			return err
		}
		defer func() { _ = c.Close() }()
		return c.Ping(ctx)
	}
	sshPinHostKeyFn = func(ctx context.Context, host, knownHosts string, w io.Writer, r io.Reader) error {
		return pvessh.PromptAndPinHostKey(ctx, host, w, r, knownHosts)
	}
	sshKnownHostsPathFn = pvessh.KnownHostsPath
)

// promptNodeSSH collects SSH user, auth mode and secret, pins the host
// key on first use (unless --ssh-insecure), then validates via dial+ping
// before returning. Returns (NodeSSH block for YAML, password secret,
// key passphrase secret).
func promptNodeSSH(ctx context.Context, p prompter, canonicalURL string) (*config.NodeSSH, string, string, error) {
	host, err := pvessh.HostFromURL(canonicalURL)
	if err != nil {
		return nil, "", "", err
	}

	// Host-key pin (skipped under --ssh-insecure).
	if !SSHInsecure() {
		kh, err := sshKnownHostsPathFn()
		if err != nil {
			return nil, "", "", err
		}
		if pinned, err := pvessh.KnownHostsHas(kh, host); err != nil {
			return nil, "", "", err
		} else if !pinned {
			if err := sshPinHostKeyFn(ctx, host, kh, p.Out(), p.In()); err != nil {
				return nil, "", "", fmt.Errorf("pin host key for %s: %w", host, err)
			}
		}
	}

	for attempt := 0; attempt < 3; attempt++ {
		userAns, err := p.Prompt("Proxmox node SSH username [root]: ")
		if err != nil {
			return nil, "", "", err
		}
		userAns = strings.TrimSpace(userAns)
		if userAns == "" {
			userAns = "root"
		}

		authAns, err := promptSSHAuthMethod(p)
		if err != nil {
			return nil, "", "", err
		}

		cfg := pvessh.Config{
			Host:     host,
			User:     userAns,
			Insecure: SSHInsecure(),
		}
		if !cfg.Insecure {
			kh, kerr := sshKnownHostsPathFn()
			if kerr != nil {
				return nil, "", "", kerr
			}
			cfg.KnownHosts = kh
		}

		var (
			password string
			keyPath  string
			keyPass  string
		)
		switch authAns {
		case "p", "password":
			pw, err := p.PromptSecret("Password: ")
			if err != nil {
				return nil, "", "", err
			}
			if pw == "" {
				p.Errf("password cannot be empty\n")
				continue
			}
			password = pw
			cfg.Password = pw
		case "k", "key":
			kp, err := p.Prompt("Path to SSH private key: ")
			if err != nil {
				return nil, "", "", err
			}
			kp = strings.TrimSpace(sshkey.ExpandHome(kp))
			if kp == "" {
				p.Errf("key path cannot be empty\n")
				continue
			}
			keyPath = kp
			cfg.KeyPath = kp

			yn, err := p.Prompt("Key is passphrase-protected? [y/N]: ")
			if err != nil {
				return nil, "", "", err
			}
			if strings.ToLower(strings.TrimSpace(yn)) == "y" {
				kpass, err := p.PromptSecret("Key passphrase: ")
				if err != nil {
					return nil, "", "", err
				}
				keyPass = kpass
				cfg.KeyPass = kpass
			}
		default:
			p.Errf("answer 'p' for password or 'k' for key file\n")
			continue
		}

		p.Printf("Verifying SSH connectivity to %s... ", host)
		if err := sshValidateFn(ctx, cfg); err != nil {
			p.Printf("failed\n")
			p.Errf("%v\n", err)
			continue
		}
		p.Printf("ok\n")

		ns := &config.NodeSSH{User: userAns}
		if password != "" {
			ns.Auth = config.AuthPassword
		} else {
			ns.Auth = config.AuthKey
			ns.KeyPath = keyPath
		}
		return ns, password, keyPass, nil
	}
	return nil, "", "", fmt.Errorf("%w: too many failed SSH credential attempts", exitcode.ErrUserInput)
}

// promptSSHAuthMethod asks how to authenticate the node SSH connection.
// Interactively it's a themed picker; non-interactively (the linear
// fallback, no TTY) it falls back to a plain "p/k" text prompt.
func promptSSHAuthMethod(p prompter) (string, error) {
	if !interactiveFn() {
		ans, err := p.Prompt("Authenticate with (p)assword or (k)ey file? [p]: ")
		if err != nil {
			return "", err
		}
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans == "" {
			ans = "p"
		}
		return ans, nil
	}

	choice := "password"
	err := huh.NewSelect[string]().
		Title("Authenticate with").
		Options(
			huh.NewOption("Password", "password"),
			huh.NewOption("SSH key file", "key"),
		).
		Value(&choice).
		Filtering(false).
		WithTheme(tui.Theme()).
		Run()
	if err != nil {
		return "", tui.AbortErr(err)
	}
	return choice, nil
}

// promptSSHKey resolves the SSH public key pmox injects into cloud-init.
// Interactively it leads with a top-level choice — generate a new
// dedicated bootstrap key, pick an existing one, or browse the filesystem.
// Non-interactively it falls back to a plain-text path prompt defaulting
// to the suggested key.
func promptSSHKey(p prompter, current string) (string, error) {
	home, _ := os.UserHomeDir()
	sshDir := filepath.Join(home, ".ssh")
	suggest := sshkey.DefaultSuggestion(current, sshDir)

	if !interactiveFn() {
		return sshKeyTextFallback(p, home, suggest)
	}

	choice, err := chooseSSHKeyAction(suggest)
	if err != nil {
		return "", tui.AbortErr(err)
	}
	switch choice {
	case "generate":
		return generateBootstrapKey(p, sshDir, home)
	case "browse":
		if path, ok := browseForKey(home); ok {
			p.Printf("Default SSH public key: %s\n", displayPath(path, home))
			return path, nil
		}
		// Cancelled browse → fall back to picking an existing key.
		return selectExistingKey(p, sshDir, home, suggest)
	default: // "existing"
		return selectExistingKey(p, sshDir, home, suggest)
	}
}

// chooseSSHKeyAction shows the generate/existing/browse menu and returns
// the selected action key. The default lands on "existing" when a key is
// already available, otherwise "generate".
func chooseSSHKeyAction(suggest string) (string, error) {
	choice := "existing"
	if suggest == "" {
		choice = "generate"
	}
	fmt.Println()
	err := huh.NewSelect[string]().
		Title("SSH key for VM bootstrap").
		Options(
			huh.NewOption("Generate a new dedicated key", "generate"),
			huh.NewOption("Use an existing key", "existing"),
			huh.NewOption("Browse for a key…", "browse"),
		).
		Value(&choice).
		Filtering(false).
		WithTheme(tui.Theme()).
		Run()
	return choice, err
}

// generateBootstrapKey creates (or reuses) a dedicated pmox ed25519 key at
// ~/.ssh/pmox_ed25519 and returns its public-key path.
func generateBootstrapKey(p prompter, sshDir, home string) (string, error) {
	pub, reused, err := sshkey.EnsureBootstrap(sshDir, sshkey.DefaultComment())
	switch {
	case errors.Is(err, sshkey.ErrPubKeyMissing):
		priv := filepath.Join(sshDir, sshkey.BootstrapKeyName)
		return "", fmt.Errorf("%w: %s exists but %s is missing; remove it or pick another key",
			exitcode.ErrUserInput, displayPath(priv, home), displayPath(priv+".pub", home))
	case err != nil:
		return "", err
	case reused:
		p.Printf("reusing existing pmox key: %s\n", displayPath(pub, home))
	default:
		p.Printf("generated new SSH key: %s\n", displayPath(pub, home))
	}
	return pub, nil
}

// browseForKey opens a filesystem picker rooted at home. It returns the
// resolved public-key path and ok=true on selection, or ok=false if the
// user cancels.
func browseForKey(home string) (string, bool) {
	var selected string
	fmt.Println()
	err := huh.NewFilePicker().
		Title("Select an SSH key file").
		CurrentDirectory(home).
		ShowHidden(true).
		FileAllowed(true).
		DirAllowed(false).
		Value(&selected).
		WithTheme(tui.Theme()).
		Run()
	if err != nil || selected == "" {
		return "", false
	}
	return sshkey.ResolvePubKey(selected), true
}

// selectExistingKey shows a picker of ~/.ssh/*.pub, falling back to a
// plain-text path prompt.
func selectExistingKey(p prompter, sshDir, home, suggest string) (string, error) {
	pubKeys := sshkey.FindPubKeys(sshDir)
	if len(pubKeys) > 0 {
		fmt.Println()
		opts := make([]huh.Option[string], 0, len(pubKeys))
		for _, k := range pubKeys {
			label := k
			if rel, rerr := filepath.Rel(sshDir, k); rerr == nil {
				label = rel
			}
			opts = append(opts, huh.NewOption(label, k))
		}
		picked := suggest
		if picked == "" {
			picked = pubKeys[0]
		}
		err := huh.NewSelect[string]().
			Title("Default SSH public key").
			Options(opts...).
			Value(&picked).
			Filtering(false).
			WithTheme(tui.Theme()).
			Run()
		if err == nil && picked != "" {
			if _, rErr := os.ReadFile(picked); rErr == nil {
				p.Printf("Default SSH public key: %s\n", displayPath(picked, home))
				return picked, nil
			}
			p.Errf("cannot read %s\n", displayPath(picked, home))
		} else if errors.Is(err, huh.ErrUserAborted) {
			return "", tui.ErrAborted
		}
	}
	return sshKeyTextFallback(p, home, suggest)
}

// sshKeyTextFallback is a plain-text path prompt with ~ expansion and
// retries; blank input accepts the suggested default. It is also the
// non-interactive path.
func sshKeyTextFallback(p prompter, home, suggest string) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		label := "Default SSH public key path"
		if suggest != "" {
			label = fmt.Sprintf("%s [%s]", label, suggest)
		}
		ans, err := p.Prompt(label + ": ")
		if err != nil {
			return "", err
		}
		ans = strings.TrimSpace(ans)
		if ans == "" {
			ans = suggest
		}
		if ans == "" {
			p.Errf("ssh key path is required\n")
			continue
		}
		expanded := ans
		if strings.HasPrefix(expanded, "~/") {
			expanded = filepath.Join(home, expanded[2:])
		}
		if _, err := os.ReadFile(expanded); err != nil {
			p.Errf("cannot read %s: %v\n", ans, err)
			continue
		}
		return ans, nil
	}
	return "", fmt.Errorf("%w: too many invalid ssh key attempts", exitcode.ErrUserInput)
}

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/paths"
	"github.com/eugenetaranov/pmox/internal/sshkey"
	"github.com/eugenetaranov/pmox/internal/tack"
	"github.com/eugenetaranov/pmox/internal/tackprofile"
	"github.com/eugenetaranov/pmox/internal/vm"
)

type applyFlags struct {
	playbook string
	check    bool
	tags     []string
	skipTags []string
	force    bool
	yes      bool
	user     string
	identity string
	initCfg  bool
}

func newApplyCmd() *cobra.Command {
	f := &applyFlags{}
	cmd := &cobra.Command{
		Use:   "apply [name|vmid] [profile]",
		Short: "Apply a tack playbook to a VM",
		Long: `Run a tack playbook (github.com/tackhq/tack) against a pmox VM,
reusing the SSH user and key pmox already knows.

Playbooks live under ~/.config/pmox/tack/. The playbook is resolved in
order: --playbook <path>, then a named profile argument
(~/.config/pmox/tack/<profile>.yaml), then the profile last used for that
VM, then the default ~/.config/pmox/tack/playbook.yaml. The chosen
profile is remembered per VM so a later bare 'pmox apply <vm>' reuses it.

The VM is auto-started if stopped. tack's own plan/apply confirmation is
shown; pass -y to auto-approve, or --check to plan only. --output json
also auto-approves (there is no terminal to confirm on), so a scripted
caller relying on -y alone for that gate should know --output json has
the same effect on its own. tack verifies the host key against
~/.ssh/known_hosts (independently of pmox's own known_hosts_guests);
apply pins an unknown key there itself before invoking tack, the same
trust-on-first-connect model every other pmox SSH command already
applies, so this is normally invisible. --ssh-insecure skips both tack's
own verification and this pinning.

Every pmox-managed VM has passwordless sudo (the cloud-init template
grants it) and key-based SSH, so tack is told (via TACK_SUDO_NO_PROMPT /
TACK_SSH_NO_PROMPT — env, not a flag, so nothing sensitive ever lands in
argv) to never block waiting on a password it doesn't need.

Run 'pmox apply --init' to scaffold a starter ~/.config/pmox/tack/.`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runApply(cmd, args, f)
		},
	}
	cmd.Flags().StringVar(&f.playbook, "playbook", "", "explicit playbook path (overrides profile/default resolution)")
	cmd.Flags().BoolVar(&f.check, "check", false, "plan only; show changes without applying (tack --check)")
	cmd.Flags().StringSliceVarP(&f.tags, "tags", "t", nil, "only run tasks with these tags (repeatable/comma-separated)")
	cmd.Flags().StringSliceVar(&f.skipTags, "skip-tags", nil, "skip tasks with these tags")
	cmd.Flags().BoolVarP(&f.force, "force", "f", false, "bypass the pmox tag check")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "auto-approve tack's plan (env: PMOX_ASSUME_YES; --output json also auto-approves)")
	cmd.Flags().StringVarP(&f.user, "user", "u", "", "SSH login user (defaults to server config 'user', then 'pmox')")
	cmd.Flags().StringVarP(&f.identity, "identity", "i", "", "path to SSH private key")
	cmd.Flags().BoolVar(&f.initCfg, "init", false, "scaffold a starter ~/.config/pmox/tack/ and exit")
	return cmd
}

func runApply(cmd *cobra.Command, args []string, f *applyFlags) error {
	ctx := cmd.Context()

	if f.initCfg {
		return runApplyInit(cmd)
	}

	// Onboarding: if there is no tack config at all and the user didn't
	// point at an explicit playbook, guide them to --init before touching
	// tack, the cluster, or a picker.
	if f.playbook == "" {
		dir, err := tackDir()
		if err != nil {
			return err
		}
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return fmt.Errorf("%w: no tack playbooks yet — run 'pmox apply --init' to scaffold %s", exitcode.ErrUserInput, dir)
		}
	}

	if err := tack.Available(); err != nil {
		return err
	}

	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	srv := resolved.Server

	// args: [vm] [profile] — both optional (vm falls back to picker).
	var vmArgs []string
	var profileArg string
	if len(args) >= 1 {
		vmArgs = []string{args[0]}
	}
	if len(args) == 2 {
		profileArg = args[1]
	}

	arg, err := resolveTargetArg(ctx, client, vmArgs, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	ref, err := vm.Resolve(ctx, client, arg)
	if err != nil {
		return err
	}
	if err := ref.RequirePMOXTag("apply to", f.force); err != nil {
		return err
	}

	playbook, recordProfile, source, err := resolvePlaybook(f, profileArg, resolved.URL, ref.VMID)
	if err != nil {
		return err
	}

	ip, err := getOrStartVM(ctx, cmd, client, ref)
	if err != nil {
		return err
	}
	key, err := resolveIdentityKey(f.identity, srv.SSHPubkey)
	if err != nil {
		return err
	}

	// Say up front which playbook is about to run and against what — the
	// only other way to answer "what will a bare 'pmox apply <vm>' run?"
	// is opening the tack-profile state file by hand.
	fmt.Fprintf(cmd.ErrOrStderr(), "Applying %s (%s) to vm %d at %s\n", playbook, source, ref.VMID, ip)

	// tack's own SSH client checks ~/.ssh/known_hosts and has no
	// equivalent of ssh's -o UserKnownHostsFile, so a VM only pmox has
	// ever SSHed to (via its own, separately-managed guest known_hosts)
	// still looks brand-new to tack. Pin it here with the same
	// trust-on-first-connect model guestHostKeyOpts already applies
	// everywhere else, so a first 'pmox apply' doesn't require a manual
	// ssh-keyscan. Best-effort: a pinning failure (e.g. host down) just
	// falls through to tack's own, more specific connection error.
	if pinned, err := tack.PinHostKey(ctx, ip, SSHInsecure()); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not pre-pin SSH host key: %v\n", err)
	} else if pinned {
		fmt.Fprintf(cmd.ErrOrStderr(), "pinned new SSH host key for %s into ~/.ssh/known_hosts\n", ip)
	}

	opts := tack.Options{
		Playbook:    playbook,
		User:        firstNonEmpty(f.user, srv.User, defaultUser),
		IP:          ip,
		KeyPath:     key,
		Insecure:    SSHInsecure(),
		Check:       f.check,
		AutoApprove: f.yes || envBool("PMOX_ASSUME_YES") || outputMode == "json",
		Tags:        f.tags,
		SkipTags:    f.skipTags,
		OutputJSON:  outputMode == "json",
	}
	if err := tack.Run(ctx, opts, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
		return &tackRunError{err: err}
	}

	// Remember the profile whenever one was explicitly named, whether or
	// not this run was --check: it reflects the last *selected* profile,
	// not just the last applied one, so a --check-then-apply two-step
	// doesn't silently fall back to the default on the real run. An
	// explicit --playbook never updates the memory (it's not a profile).
	if recordProfile != "" {
		if err := rememberTackProfile(resolved.URL, ref.VMID, recordProfile); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not remember profile: %v\n", err)
		}
	}
	return nil
}

// tackRunError marks a failed tack playbook run with the same exit code
// (ExitHook) that a failed `pmox launch --tack` hook uses, so a script
// can tell "the playbook failed on the guest" apart from any other apply
// error by exit code alone, whichever command produced it.
type tackRunError struct{ err error }

func (e *tackRunError) Error() string { return fmt.Sprintf("tack run failed: %v", e.err) }
func (e *tackRunError) Unwrap() error { return e.err }
func (e *tackRunError) ExitCode() int { return exitcode.ExitHook }

// resolvePlaybook implements the resolution ladder. It returns the
// playbook path, the profile name to remember ("" = do not record), and
// a human-readable description of where the choice came from (for the
// "Applying ..." status line).
func resolvePlaybook(f *applyFlags, profileArg, serverURL string, vmid int) (playbook, recordProfile, source string, err error) {
	switch {
	case f.playbook != "":
		playbook = sshkey.ExpandHome(f.playbook)
		source = "explicit --playbook"
	default:
		dir, dirErr := tackDir()
		if dirErr != nil {
			return "", "", "", dirErr
		}
		switch {
		case profileArg != "":
			playbook = filepath.Join(dir, profileArg+".yaml")
			recordProfile = profileArg
			source = fmt.Sprintf("profile %q", profileArg)
		default:
			stateDir, stateErr := tackStateDir()
			if stateErr != nil {
				return "", "", "", stateErr
			}
			if prof, ok, _ := tackprofile.Get(stateDir, serverURL, vmid); ok {
				playbook = filepath.Join(dir, prof+".yaml")
				source = fmt.Sprintf("remembered profile %q", prof)
			} else {
				playbook = filepath.Join(dir, "playbook.yaml")
				source = "default playbook"
			}
		}
	}

	// Checked for every branch — including an explicit --playbook typo,
	// which used to sail through here and only fail after the VM was
	// already started and SSH-ready, wasting a boot cycle.
	if _, statErr := os.Stat(playbook); statErr != nil {
		return "", "", "", fmt.Errorf("%w: playbook %s not found — create it or run 'pmox apply --init' to scaffold ~/.config/pmox/tack/", exitcode.ErrUserInput, playbook)
	}
	return playbook, recordProfile, source, nil
}

// rememberTackProfile records profile as the last-used tack profile for
// the VM.
func rememberTackProfile(serverURL string, vmid int, profile string) error {
	stateDir, err := tackStateDir()
	if err != nil {
		return err
	}
	return tackprofile.Set(stateDir, serverURL, vmid, profile)
}

func runApplyInit(cmd *cobra.Command) error {
	dir, err := tackDir()
	if err != nil {
		return err
	}
	rolesDir := filepath.Join(dir, "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", rolesDir, err)
	}
	playbook := filepath.Join(dir, "playbook.yaml")
	if _, err := os.Stat(playbook); err == nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s already exists; left unchanged\n", playbook)
		return nil
	}
	if err := os.WriteFile(playbook, []byte(starterPlaybook), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", playbook, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "scaffolded %s\n", playbook)
	fmt.Fprintf(cmd.OutOrStdout(), "edit it, then run: pmox apply <vm>\n")
	return nil
}

const starterPlaybook = `# pmox tack starter playbook — https://github.com/tackhq/tack
# Applied by 'pmox apply <vm>'. Roles may be local (./roles/<name>) or
# remote (https://github.com/tackhq/tack-roles.git//roles/<name> — note
# the roles/ path segment, since that's where tack-roles.git keeps them).
name: pmox bootstrap
hosts: all
# The docker role installs packages and manages a systemd service, both
# of which need root — sudo: true is inherited by every task below.
sudo: true

roles:
  - role: https://github.com/tackhq/tack-roles.git//roles/docker
    tags: [docker]

tasks:
  - name: Show host facts
    debug:
      msg: "configured {{ facts.hostname }} ({{ facts.os_family }})"
`

// tackDir returns ~/.config/pmox/tack (XDG-aware).
func tackDir() (string, error) {
	dir, err := paths.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tack"), nil
}

// tackStateDir returns ~/.local/state/pmox/tack (XDG-aware), mirroring the
// mount state dir.
func tackStateDir() (string, error) {
	dir, err := paths.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tack"), nil
}

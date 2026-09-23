package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

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
shown; pass -y to auto-approve, or --check to plan only. tack verifies
the host key against ~/.ssh/known_hosts; on the first apply to a brand-new
VM you may need to scan it (ssh-keyscan -H <ip> >> ~/.ssh/known_hosts) or
pass --ssh-insecure.

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
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "auto-approve tack's plan (env: PMOX_ASSUME_YES)")
	cmd.Flags().StringVarP(&f.user, "user", "u", "", "SSH login user (defaults to server config 'user', then 'pmox')")
	cmd.Flags().StringVarP(&f.identity, "identity", "i", "", "path to SSH private key")
	cmd.Flags().BoolVar(&f.initCfg, "init", false, "scaffold a starter ~/.config/pmox/tack/ and exit")
	return cmd
}

func runApply(cmd *cobra.Command, args []string, f *applyFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if f.initCfg {
		return runApplyInit(cmd)
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
	if !f.force && !vm.HasPMOXTag(ref.Tags) {
		return fmt.Errorf("refusing to apply to VM %q (vmid %d): not tagged \"pmox\" — pass --force to override", ref.Name, ref.VMID)
	}

	playbook, recordProfile, err := resolvePlaybook(f, profileArg, resolved.URL, ref.VMID)
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
		return fmt.Errorf("tack run failed: %w", err)
	}

	// Remember the profile only when one was explicitly named and the run
	// succeeded; an explicit --playbook never updates the memory.
	if recordProfile != "" && !f.check {
		if err := tackprofile.Set(tackStateDir(), resolved.URL, ref.VMID, recordProfile); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not remember profile: %v\n", err)
		}
	}
	return nil
}

// resolvePlaybook implements the resolution ladder. It returns the
// playbook path and the profile name to remember ("" = do not record).
func resolvePlaybook(f *applyFlags, profileArg, serverURL string, vmid int) (playbook, recordProfile string, err error) {
	dir := tackDir()

	switch {
	case f.playbook != "":
		return expandHome(f.playbook), "", nil
	case profileArg != "":
		playbook = filepath.Join(dir, profileArg+".yaml")
		recordProfile = profileArg
	default:
		if prof, ok, _ := tackprofile.Get(tackStateDir(), serverURL, vmid); ok {
			playbook = filepath.Join(dir, prof+".yaml")
		} else {
			playbook = filepath.Join(dir, "playbook.yaml")
		}
	}

	if _, statErr := os.Stat(playbook); statErr != nil {
		return "", "", fmt.Errorf("playbook %s not found — create it or run 'pmox apply --init' to scaffold ~/.config/pmox/tack/", playbook)
	}
	return playbook, recordProfile, nil
}

func runApplyInit(cmd *cobra.Command) error {
	dir := tackDir()
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
# remote (https://github.com/tackhq/tack-roles.git//<name>).
name: pmox bootstrap
hosts: all

roles:
  - role: https://github.com/tackhq/tack-roles.git//docker
    tags: [docker]

tasks:
  - name: Show host facts
    debug:
      msg: "configured {{ facts.hostname }} ({{ facts.os_family }})"
`

// tackDir returns ~/.config/pmox/tack (XDG-aware).
func tackDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "pmox", "tack")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "pmox", "tack")
}

// tackStateDir returns ~/.local/state/pmox/tack (XDG-aware), mirroring the
// mount state dir.
func tackStateDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "pmox", "tack")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "pmox", "tack")
}

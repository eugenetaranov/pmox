package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/accessreg"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/tui"
)

type keyFlags struct {
	name    string
	all     bool
	replace bool
}

func newKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Publish your SSH public key so others can grant you VM access",
		Long: `Manage your entry in the cluster's access registry
(/etc/pve/pmox/keys on the Proxmox cluster).

Publishing puts your SSH PUBLIC key on the cluster under your local
username. A cluster admin can then give you access to VMs with
'pmox access'. Private keys and API tokens never leave your machine.`,
	}
	cmd.AddCommand(newKeyPublishCmd(), newKeyUnpublishCmd(), newKeyShowCmd())
	return cmd
}

func newKeyPublishCmd() *cobra.Command {
	f := &keyFlags{}
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish your public key to the cluster's access registry",
		Example: `  pmox key publish                      # as your local username
  pmox key publish --name bob --all-contexts`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runKeyPublish(cmd, f) },
	}
	cmd.Flags().StringVar(&f.name, "name", "", "registry name (default: your local username)")
	cmd.Flags().BoolVar(&f.replace, "replace", false, "replace a different key already published under the name")
	addContextsFlag(cmd, &f.all)
	return cmd
}

func newKeyUnpublishCmd() *cobra.Command {
	f := &keyFlags{}
	cmd := &cobra.Command{
		Use:   "unpublish",
		Short: "Remove your public key from the cluster's access registry",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runKeyUnpublish(cmd, f) },
	}
	cmd.Flags().StringVar(&f.name, "name", "", "registry name (default: your local username)")
	addContextsFlag(cmd, &f.all)
	return cmd
}

func newKeyShowCmd() *cobra.Command {
	f := &keyFlags{}
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show your public key and where it is published",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runKeyShow(cmd, f) },
	}
	cmd.Flags().StringVar(&f.name, "name", "", "registry name (default: your local username)")
	addContextsFlag(cmd, &f.all)
	return cmd
}

func registryName(flag string) (string, error) {
	name := flag
	if name == "" {
		u, err := localUsername()
		if err != nil {
			return "", fmt.Errorf("determine local username (pass --name): %w", err)
		}
		name = u
	}
	if err := accessreg.ValidName(name); err != nil {
		return "", fmt.Errorf("%w: %w — pass --name", exitcode.ErrUserInput, err)
	}
	return name, nil
}

// localPublishedKey builds the key name would publish for r: the public
// key configured for that server, plus audit metadata.
func localPublishedKey(r *server.Resolved, name string) (accessreg.PublishedKey, error) {
	if r.Server.SSHPubkey == "" {
		return accessreg.PublishedKey{}, fmt.Errorf("%w: no ssh_pubkey configured for %s; run 'pmox init'", exitcode.ErrNotFound, r.URL)
	}
	line, err := readSSHKey(r.Server.SSHPubkey)
	if err != nil {
		return accessreg.PublishedKey{}, err
	}
	k, err := accessreg.NewPublishedKey(name, line)
	if err != nil {
		return accessreg.PublishedKey{}, fmt.Errorf("%s: %w", r.Server.SSHPubkey, err)
	}
	k.Host, _ = os.Hostname()
	k.UID = strconv.Itoa(os.Getuid())
	k.TokenUser = r.Server.TokenID
	k.PublishedAt = time.Now()
	return k, nil
}

func runKeyPublish(cmd *cobra.Command, f *keyFlags) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	name, err := registryName(f.name)
	if err != nil {
		return err
	}
	cfg, targets, err := selectContexts(ctx, f.all, true)
	if err != nil {
		return err
	}
	var failed int
	for _, r := range targets {
		label := targetLabel(cfg, r)
		if err := publishTo(cmd, r, label, name, f.replace); err != nil {
			failed++
			fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", tui.Warnf(fmt.Sprintf("✗ %s: %v", label, err)))
		}
	}
	if failed > 0 {
		return fmt.Errorf("publishing failed on %d of %d servers", failed, len(targets))
	}
	fmt.Fprintf(out, "\nA cluster admin can now grant you access:  pmox access grant <vm> --to %s\n", name)
	return nil
}

// confirmReplaceFn asks before replacing a different published key.
var confirmReplaceFn = tui.Confirm

func publishTo(cmd *cobra.Command, r *server.Resolved, label, name string, replace bool) error {
	ctx := cmd.Context()
	k, err := localPublishedKey(r, name)
	if err != nil {
		return err
	}
	fs, closeFS, err := openRegistryFn(ctx, r)
	if err != nil {
		return err
	}
	defer closeFS()

	existing, err := accessreg.GetKey(ctx, fs, name)
	switch {
	case errors.Is(err, accessreg.ErrNotPublished):
	case err != nil:
		return err
	case !accessreg.SameKey(existing, k):
		if !replace {
			if !tui.Interactive() {
				return fmt.Errorf("%w: a different key is already published as %q (%s, from %s); pass --replace to overwrite it",
					exitcode.ErrUserInput, name, existing.Fingerprint, existing.Host)
			}
			ok, cerr := confirmReplaceFn(fmt.Sprintf("%q on %s already has a different key:\n  published: %s (from %s)\n  yours:     %s\nReplace it?",
				name, label, existing.Fingerprint, existing.Host, k.Fingerprint), false)
			if cerr != nil {
				return cerr
			}
			if !ok {
				return fmt.Errorf("%w: kept the existing key", tui.ErrAborted)
			}
		}
	}
	if err := accessreg.PublishKey(ctx, fs, k); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "✓ published %s (%s) to %s → %s/%s.pub\n", name, k.Fingerprint, label, accessreg.KeysDir, name)
	return nil
}

func runKeyUnpublish(cmd *cobra.Command, f *keyFlags) error {
	ctx := cmd.Context()
	name, err := registryName(f.name)
	if err != nil {
		return err
	}
	cfg, targets, err := selectContexts(ctx, f.all, true)
	if err != nil {
		return err
	}
	for _, r := range targets {
		fs, closeFS, err := openRegistryFn(ctx, r)
		if err != nil {
			return err
		}
		err = accessreg.UnpublishKey(ctx, fs, name)
		closeFS()
		label := targetLabel(cfg, r)
		switch {
		case errors.Is(err, accessreg.ErrNotPublished):
			fmt.Fprintf(cmd.OutOrStdout(), "= %s: nothing published as %s\n", label, name)
		case err != nil:
			return err
		default:
			fmt.Fprintf(cmd.OutOrStdout(), "✓ unpublished %s from %s\n", name, label)
		}
	}
	return nil
}

func runKeyShow(cmd *cobra.Command, f *keyFlags) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	name, err := registryName(f.name)
	if err != nil {
		return err
	}
	cfg, targets, err := selectContexts(ctx, f.all, false)
	if err != nil {
		return err
	}
	for _, r := range targets {
		k, err := localPublishedKey(r, name)
		if err != nil {
			return err
		}
		label := targetLabel(cfg, r)
		fmt.Fprintf(out, "%s\n  name:   %s\n  key:    %s (%s)\n", label, name, r.Server.SSHPubkey, k.Fingerprint)
		printPublishStatus(cmd, out, r, name, k)
	}
	return nil
}

func printPublishStatus(cmd *cobra.Command, out io.Writer, r *server.Resolved, name string, k accessreg.PublishedKey) {
	fs, closeFS, err := openRegistryFn(cmd.Context(), r)
	if err != nil {
		fmt.Fprintf(out, "  status: unknown (%v)\n", err)
		return
	}
	defer closeFS()
	existing, err := accessreg.GetKey(cmd.Context(), fs, name)
	switch {
	case errors.Is(err, accessreg.ErrNotPublished):
		fmt.Fprintf(out, "  status: not published — run 'pmox key publish'\n")
	case err != nil:
		fmt.Fprintf(out, "  status: unknown (%v)\n", err)
	case accessreg.SameKey(existing, k):
		fmt.Fprintf(out, "  status: published %s from %s\n", existing.PublishedAt.Local().Format("2006-01-02"), existing.Host)
	default:
		fmt.Fprintf(out, "  status: a DIFFERENT key is published as %s (%s, from %s)\n", name, existing.Fingerprint, existing.Host)
	}
}

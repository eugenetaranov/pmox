package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvessh"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/template"
	"github.com/eugenetaranov/pmox/internal/tui"
)

type createTemplateFlags struct {
	node   string
	bridge string
	wait   time.Duration
}

// isTTYFunc is overridable so tests can force the non-TTY branch.
var isTTYFunc = func(fd uintptr) bool { return term.IsTerminal(int(fd)) }

func newCreateTemplateCmd() *cobra.Command {
	f := &createTemplateFlags{}
	cmd := &cobra.Command{
		Use:   "create-template",
		Short: "Build an Ubuntu cloud-image Proxmox template",
		Long: `Interactively build a Proxmox template from an Ubuntu cloud image.

Fetches the latest Ubuntu images from Canonical's simplestreams feed,
lets you pick one, downloads it to a storage via PVE's download-url,
boots a throw-away VM with a cloud-init snippet that installs
qemu-guest-agent, waits for shutdown, detaches cloud-init, and
converts the result to a template in the 9000–9099 VMID range.

Requires PVE 8.0+ and an interactive TTY.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreateTemplate(cmd, f)
		},
	}
	cmd.Flags().StringVar(&f.node, "node", "", "cluster node to build on (falls back to configured default)")
	cmd.Flags().StringVar(&f.bridge, "bridge", "", "network bridge for the build VM (default vmbr0)")
	cmd.Flags().DurationVar(&f.wait, "wait", 10*time.Minute, "budget for waiting for the bake shutdown")
	return cmd
}

func runCreateTemplate(cmd *cobra.Command, f *createTemplateFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Enforce interactive TTY — the flow has picker prompts that
	// cannot be driven from a pipe or file.
	if !isTTYFunc(os.Stdin.Fd()) {
		return fmt.Errorf("%w: interactive TTY required for pmox create-template", exitcode.ErrUserInput)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resolved, err := server.Resolve(ctx, server.Options{
		Cfg:    cfg,
		Flag:   serverFlag,
		Env:    os.Getenv("PMOX_SERVER"),
		Stdin:  os.Stdin,
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	})
	if err != nil {
		return err
	}

	if !resolved.HasNodeSSH() {
		return fmt.Errorf("%w: create-template needs SSH access to the Proxmox node (for snippet upload); run 'pmox configure' to add SSH credentials", exitcode.ErrUserInput)
	}

	srv := resolved.Server
	client := pveclient.New(resolved.URL, srv.TokenID, resolved.Secret, srv.Insecure)
	node := firstNonEmpty(f.node, srv.Node)
	if node == "" {
		return fmt.Errorf("%w: no node configured; pass --node or run 'pmox configure'", exitcode.ErrNotFound)
	}
	bridge := firstNonEmpty(f.bridge, srv.Bridge, "vmbr0")

	// Lazily dial SSH: only phase 5 (upload snippet) actually needs it,
	// so failures in earlier phases surface cleanly without paying the
	// SSH handshake cost or its failure modes.
	var sshClient *pvessh.Client
	defer func() {
		if sshClient != nil {
			_ = sshClient.Close()
		}
	}()
	upload := func(ctx context.Context, storagePath, filename string, content []byte) error {
		if sshClient == nil {
			c, err := dialPvessh(ctx, resolved)
			if err != nil {
				return fmt.Errorf("ssh to %s: %w", resolved.URL, err)
			}
			sshClient = c
		}
		return sshClient.UploadSnippet(ctx, storagePath, filename, content)
	}

	return runCreateTemplateWithClient(ctx, cmd, client, resolved.URL, resolved.Source, node, bridge, f.wait, upload)
}

// dialPvessh opens an SSH+SFTP session to the PVE node named in the
// resolved server record. The SSH host is derived from the API URL's
// hostname on port 22.
func dialPvessh(ctx context.Context, resolved *server.Resolved) (*pvessh.Client, error) {
	u, err := url.Parse(resolved.URL)
	if err != nil {
		return nil, fmt.Errorf("parse server url: %w", err)
	}
	host := u.Hostname() + ":22"
	kh, err := pvessh.KnownHostsPath()
	if err != nil {
		return nil, err
	}
	return pvessh.Dial(ctx, pvessh.Config{
		Host:       host,
		User:       resolved.NodeSSHUser,
		Password:   resolved.NodeSSHPassword,
		KeyPath:    resolved.NodeSSHKeyPath,
		KeyPass:    resolved.NodeSSHKeyPassphrase,
		Insecure:   SSHInsecure(),
		KnownHosts: kh,
	})
}

// runCreateTemplateWithClient runs everything from the verbose log
// line onward. Extracted so tests can drive it with a fake PVE server
// and without touching config loading.
func runCreateTemplateWithClient(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, resolvedURL, resolvedSource, node, bridge string, wait time.Duration, upload func(context.Context, string, string, []byte) error) error {
	if verbose {
		fmt.Fprintf(cmd.ErrOrStderr(), "using server %s (%s)\n", resolvedURL, resolvedSource)
	}

	opts := template.Options{
		Client:   client,
		Node:     node,
		Bridge:   bridge,
		Wait:     wait,
		Stderr:   cmd.ErrOrStderr(),
		Verbose:  verbose,
		Progress: newTemplateProgress(cmd.ErrOrStderr()),
		PickImage: func(entries []template.ImageEntry) int {
			options := make([]huh.Option[string], 0, len(entries))
			for i, e := range entries {
				options = append(options, huh.NewOption(e.Label, strconv.Itoa(i)))
			}
			fallback := strconv.Itoa(0)
			picked := tui.SelectOne("Ubuntu image", options, fallback)
			idx, _ := strconv.Atoi(picked)
			return idx
		},
		PickTargetStorage: func(pools []pveclient.Storage) int {
			options := make([]huh.Option[string], 0, len(pools))
			for i, s := range pools {
				options = append(options, huh.NewOption(fmt.Sprintf("%s (%s)", s.Storage, s.Type), strconv.Itoa(i)))
			}
			picked := tui.SelectOne("Target storage for the template disk", options, "0")
			idx, _ := strconv.Atoi(picked)
			return idx
		},
		PickSnippetsStorage: func(pools []pveclient.Storage) int {
			options := make([]huh.Option[string], 0, len(pools))
			for i, s := range pools {
				options = append(options, huh.NewOption(fmt.Sprintf("%s (%s)", s.Storage, s.Type), strconv.Itoa(i)))
			}
			picked := tui.SelectOne("Snippets storage", options, "0")
			idx, _ := strconv.Atoi(picked)
			return idx
		},
		UploadSnippet: upload,
	}

	r, err := template.Run(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "created template %s (vmid=%d); launch with: pmox launch <name> --template %d\n", r.Name, r.VMID, r.VMID)
	return nil
}


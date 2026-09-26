package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"golang.org/x/term"

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

	// Enforce interactive TTY — the flow has picker prompts that
	// cannot be driven from a pipe or file.
	if !isTTYFunc(os.Stdin.Fd()) {
		return fmt.Errorf("%w: interactive TTY required for pmox create-template", exitcode.ErrUserInput)
	}

	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}

	if err := resolved.RequireNodeSSH("create-template"); err != nil {
		return err
	}

	srv := resolved.Server
	node := firstNonEmpty(f.node, srv.Node)
	if node == "" {
		return fmt.Errorf("%w: no node configured; pass --node or run 'pmox init'", exitcode.ErrNotFound)
	}
	bridge := firstNonEmpty(f.bridge, srv.Bridge, "vmbr0")

	// Lazily dial SSH: only phase 5 (upload snippet) actually needs it.
	upload, closeUpload := newSnippetUploader(resolved)
	defer closeUpload()

	return runCreateTemplateWithClient(ctx, cmd, client, node, bridge, f.wait, upload)
}

// dialPvessh opens an SSH+SFTP session to the PVE node named in the
// resolved server record. The SSH host is derived from the API URL's
// hostname on port 22.
func dialPvessh(ctx context.Context, resolved *server.Resolved) (*pvessh.Client, error) {
	cfg, err := resolved.NodeSSHConfig(SSHInsecure())
	if err != nil {
		return nil, err
	}
	return pvessh.Dial(ctx, cfg)
}

// pickIndex shows labels in a picker and returns the chosen index,
// defaulting to 0. An aborted picker returns tui.ErrAborted.
func pickIndex(title string, labels []string) (int, error) {
	options := make([]huh.Option[string], 0, len(labels))
	for i, l := range labels {
		options = append(options, huh.NewOption(l, strconv.Itoa(i)))
	}
	picked, err := tui.SelectOne(title, options, "0")
	if err != nil {
		return 0, err
	}
	idx, _ := strconv.Atoi(picked)
	return idx, nil
}

// storageLabels renders storageLabel picker labels for pools.
func storageLabels(pools []pveclient.Storage) []string {
	labels := make([]string, 0, len(pools))
	for _, s := range pools {
		labels = append(labels, storageLabel(s))
	}
	return labels
}

// storageLabel renders a picker label for a single storage pool: name,
// type, and — when PVE reported live capacity for it (Total > 0; 0 for
// inactive storage) — free/total space, so a storage pick doesn't need
// a separate look at the PVE UI to know what's actually available.
func storageLabel(s pveclient.Storage) string {
	if s.Total <= 0 {
		return fmt.Sprintf("%s (%s)", s.Storage, s.Type)
	}
	return fmt.Sprintf("%s (%s, %s free / %s total)", s.Storage, s.Type, formatGiB(s.Avail), formatGiB(s.Total))
}

// formatGiB renders a byte count as whole GiB (PVE reports storage
// capacity in bytes; GiB is what --disk/--storage sizing already uses
// throughout pmox's own flags and prompts).
func formatGiB(bytes int64) string {
	const gib = 1024 * 1024 * 1024
	return fmt.Sprintf("%.0f GiB", float64(bytes)/gib)
}

// runCreateTemplateWithClient runs everything after server resolution
// (buildClient already emitted the --verbose server line). Extracted so
// tests can drive it with a fake PVE server and without touching config
// loading.
func runCreateTemplateWithClient(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, node, bridge string, wait time.Duration, upload func(context.Context, string, string, []byte) error) error {
	r, err := templateRunFn(ctx, buildTemplateOptions(cmd, client, node, bridge, wait, upload))
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "created template %s (vmid=%d); launch with: pmox launch <name> --template %d\n", r.Name, r.VMID, r.VMID)
	return nil
}

// templateRunFn is a test seam over template.Run.
var templateRunFn = template.Run

// buildTemplateOptions assembles the template.Options shared by
// 'pmox create-template' and 'pmox doctor --fix's template-rebuild fix:
// interactive pickers for image/target storage/snippets storage, wired
// to the given client/node/bridge/wait/upload.
func buildTemplateOptions(cmd *cobra.Command, client *pveclient.Client, node, bridge string, wait time.Duration, upload func(context.Context, string, string, []byte) error) template.Options {
	return template.Options{
		Client:   client,
		Node:     node,
		Bridge:   bridge,
		Wait:     wait,
		Progress: newTemplateProgress(cmd.ErrOrStderr()),
		PickImage: func(entries []template.ImageEntry) (int, error) {
			labels := make([]string, 0, len(entries))
			for _, e := range entries {
				labels = append(labels, e.Label)
			}
			return pickIndex("Ubuntu image", labels)
		},
		PickTargetStorage: func(pools []pveclient.Storage) (int, error) {
			return pickIndex("Target storage for the template disk", storageLabels(pools))
		},
		PickSnippetsStorage: func(pools []pveclient.Storage) (int, error) {
			return pickIndex("Snippets storage", storageLabels(pools))
		},
		UploadSnippet: upload,
	}
}

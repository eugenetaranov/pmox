// Package template implements `pmox create-template`: the
// simplestreams catalogue fetch, interactive picker plumbing,
// cloud-init bake snippet, VMID allocation in the 9000–9099 range,
// and the top-to-bottom state machine that turns an Ubuntu cloud
// image into a ready-to-launch Proxmox template.
package template

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// Options bundles everything Run needs. Interactive pickers are
// injected as function fields so tests can return a fixed choice
// without driving a TTY. UploadSnippet is an interface seam so tests
// can replace the pvessh SCP path with an in-process stub.
type Options struct {
	Client  *pveclient.Client
	Node    string
	Bridge  string
	Wait    time.Duration
	Stderr  io.Writer
	Verbose bool

	// CatalogueURL overrides the default Canonical simplestreams
	// feed. Tests set it to a local httptest.Server; production
	// leaves it empty (defaultCatalogueURL).
	CatalogueURL string

	PickImage           func([]ImageEntry) int
	PickTargetStorage   func([]pveclient.Storage) int
	PickSnippetsStorage func([]pveclient.Storage) int

	// UploadSnippet writes the bake snippet to the PVE node's snippets
	// directory via SFTP. Called once per Run, after the storage path
	// has been resolved via GET /storage/{storage}.
	UploadSnippet func(ctx context.Context, storagePath, filename string, content []byte) error
}

// Result is returned to the caller on success.
type Result struct {
	VMID  int
	Name  string
	Image ImageEntry
}

const (
	downloadTimeout = 30 * time.Minute
	createTimeout   = 5 * time.Minute
	startTimeout    = 2 * time.Minute
	defaultWait     = 10 * time.Minute
)

// pollInterval is a var (not const) so tests can shrink it to keep
// the integration suite fast. Production uses 5s per design D8.
var pollInterval = 5 * time.Second

// Run walks the 13-phase template-build state machine end-to-end.
// Any pre-CreateVM failure is a clean abort with no cleanup needed;
// failures after CreateVM leave a visible VM on the cluster that the
// user can delete via `pmox delete`.
func Run(ctx context.Context, opts Options) (*Result, error) {
	// Phase 0 — pve version check.
	if err := checkVersion(ctx, opts.Client); err != nil {
		return nil, err
	}

	// Phase 1 — fetch catalogue.
	catalogueURL := opts.CatalogueURL
	if catalogueURL == "" {
		catalogueURL = defaultCatalogueURL
	}
	entries, err := fetchCatalogue(ctx, catalogueURL)
	if err != nil {
		return nil, fmt.Errorf("fetch ubuntu catalogue: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("fetch ubuntu catalogue: no amd64 disk1.img entries found")
	}

	// Phase 2 — pick image.
	if opts.PickImage == nil {
		return nil, fmt.Errorf("pick image: no picker supplied")
	}
	idx := opts.PickImage(entries)
	if idx < 0 || idx >= len(entries) {
		return nil, fmt.Errorf("pick image: picker returned out-of-range index %d", idx)
	}
	img := entries[idx]

	// Phase 3 — pick target storage (where the VM disk lands).
	targetStorage, err := pickTargetStorage(ctx, opts)
	if err != nil {
		return nil, err
	}

	// Phase 4 — pick a dir-capable snippets storage.
	snippetsStorage, err := pickSnippetsStorage(ctx, opts)
	if err != nil {
		return nil, err
	}

	// Phase 4.5 — resolve its on-disk path via PVE API.
	storagePath, err := opts.Client.GetStoragePath(ctx, snippetsStorage)
	if err != nil {
		return nil, fmt.Errorf("resolve snippets storage path: %w", err)
	}

	// Phase 5 — upload the bake snippet via SFTP.
	if opts.UploadSnippet == nil {
		return nil, fmt.Errorf("upload bake snippet: no UploadSnippet injected")
	}
	if err := opts.UploadSnippet(ctx, storagePath, bakeSnippetFilename, bakeSnippet); err != nil {
		return nil, fmt.Errorf("upload bake snippet: %w", err)
	}

	// Phase 6 — reserve a VMID in the 9000–9099 range.
	vmid, err := reserveVMID(ctx, opts.Client, opts.Node)
	if err != nil {
		return nil, fmt.Errorf("reserve vmid: %w", err)
	}

	// Phase 7 — download the cloud image via PVE's download-url.
	imgFilename := stableImageFilename(img)
	downloadParams := map[string]string{
		"url":                img.URL,
		"content":            "iso",
		"filename":           imgFilename,
		"checksum":           img.SHA256,
		"checksum-algorithm": "sha256",
	}
	upid, err := opts.Client.DownloadURL(ctx, opts.Node, targetStorage, downloadParams)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", img.URL, err)
	}
	if err := opts.Client.WaitTask(ctx, opts.Node, upid, downloadTimeout); err != nil {
		return nil, fmt.Errorf("download %s: %w", img.URL, err)
	}

	// Phase 8 — create the VM with importfrom pointing at the
	// just-downloaded image.
	name := templateName(img, vmid)
	kv := buildCreateKV(opts, img, vmid, name, targetStorage, snippetsStorage, imgFilename)
	createUPID, err := opts.Client.CreateVM(ctx, opts.Node, vmid, kv)
	if err != nil {
		return nil, fmt.Errorf("create vm %d: %w", vmid, err)
	}
	if err := opts.Client.WaitTask(ctx, opts.Node, createUPID, createTimeout); err != nil {
		return nil, fmt.Errorf("create vm %d: %w", vmid, err)
	}

	// Phase 9 — start the VM.
	startUPID, err := opts.Client.Start(ctx, opts.Node, vmid)
	if err != nil {
		return nil, fmt.Errorf("start vm %d: %w (run pmox delete %d)", vmid, err, vmid)
	}
	if err := opts.Client.WaitTask(ctx, opts.Node, startUPID, startTimeout); err != nil {
		return nil, fmt.Errorf("start vm %d: %w (run pmox delete %d)", vmid, err, vmid)
	}

	// Phase 10 — poll until the guest powers itself off.
	waitBudget := opts.Wait
	if waitBudget <= 0 {
		waitBudget = defaultWait
	}
	if err := waitStopped(ctx, opts.Client, opts.Node, vmid, waitBudget); err != nil {
		return nil, fmt.Errorf("wait for vm %d to stop: %w (run pmox delete %d)", vmid, err, vmid)
	}

	// Phase 11 — detach the cloud-init drive so clones regenerate.
	if err := opts.Client.SetConfig(ctx, opts.Node, vmid, map[string]string{"delete": "ide2"}); err != nil {
		return nil, fmt.Errorf("detach cloud-init drive from vm %d: %w (run pmox delete %d)", vmid, err, vmid)
	}

	// Phase 12 — convert to template.
	if err := opts.Client.ConvertToTemplate(ctx, opts.Node, vmid); err != nil {
		return nil, fmt.Errorf("convert vm %d to template: %w (run pmox delete %d)", vmid, err, vmid)
	}

	return &Result{VMID: vmid, Name: name, Image: img}, nil
}

func checkVersion(ctx context.Context, c *pveclient.Client) error {
	v, err := c.GetVersion(ctx)
	if err != nil {
		return fmt.Errorf("check pve version: %w", err)
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 1 {
		return fmt.Errorf("check pve version: unrecognized version %q", v)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("check pve version: unrecognized version %q", v)
	}
	if major < 8 {
		return fmt.Errorf("PVE 8.0 or later required (found %s)", v)
	}
	return nil
}

func pickTargetStorage(ctx context.Context, opts Options) (string, error) {
	pools, err := opts.Client.ListStorage(ctx, opts.Node)
	if err != nil {
		return "", fmt.Errorf("pick target storage: %w", err)
	}
	usable := make([]pveclient.Storage, 0, len(pools))
	for _, s := range pools {
		if s.Active == 1 && s.Enabled == 1 && s.SupportsVMDisks() {
			usable = append(usable, s)
		}
	}
	if len(usable) == 0 {
		return "", fmt.Errorf("pick target storage: no active, enabled, images-capable storage found on node %s", opts.Node)
	}
	if opts.PickTargetStorage == nil {
		return "", fmt.Errorf("pick target storage: no picker supplied")
	}
	idx := opts.PickTargetStorage(usable)
	if idx < 0 || idx >= len(usable) {
		return "", fmt.Errorf("pick target storage: picker returned out-of-range index %d", idx)
	}
	return usable[idx].Storage, nil
}

// stableImageFilename builds a reproducible, PVE-friendly filename
// for the downloaded cloud image. Using a per-release stable name
// means repeated runs of the same release share one download and
// avoid storage bloat.
func stableImageFilename(img ImageEntry) string {
	codename := strings.ToLower(img.Codename)
	if codename == "" {
		codename = "ubuntu"
	}
	return fmt.Sprintf("ubuntu-%s-cloudimg-amd64.img", codename)
}

// buildCreateKV constructs the POST /qemu body used to create the
// template-build VM. The scsi0 importfrom parameter is the PVE 8.0+
// equivalent of `qm importdisk` — it tells PVE to import the
// downloaded image file as the VM's boot disk in a single API call.
func buildCreateKV(opts Options, img ImageEntry, vmid int, name, targetStorage, snippetsStorage, imgFilename string) map[string]string {
	bridge := opts.Bridge
	if bridge == "" {
		bridge = "vmbr0"
	}
	// importfrom references the downloaded image by
	// <storage>:iso/<filename> — matching the download-url phase.
	importFrom := fmt.Sprintf("%s:iso/%s", targetStorage, imgFilename)
	cicustom := fmt.Sprintf("user=%s:snippets/%s", snippetsStorage, bakeSnippetFilename)
	return map[string]string{
		"name":      name,
		"memory":    "2048",
		"cores":     "2",
		"cpu":       "host",
		"net0":      fmt.Sprintf("virtio,bridge=%s", bridge),
		"scsi0":     fmt.Sprintf("%s:0,importfrom=%s", targetStorage, importFrom),
		"scsihw":    "virtio-scsi-single",
		"ide2":      fmt.Sprintf("%s:cloudinit", targetStorage),
		"cicustom":  cicustom,
		"agent":     "1",
		"serial0":   "socket",
		"vga":       "serial0",
		"boot":      "order=scsi0",
		"ipconfig0": "ip=dhcp",
	}
}

// waitStopped polls GetStatus every pollInterval until the VM
// reports status=stopped, the context is cancelled, or the wait
// budget elapses.
func waitStopped(ctx context.Context, c *pveclient.Client, node string, vmid int, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for {
		st, err := c.GetStatus(ctx, node, vmid)
		if err != nil {
			return err
		}
		if st.Status == "stopped" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: vm %d still running after %s", pveclient.ErrTimeout, vmid, budget)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

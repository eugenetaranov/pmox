// Package launch implements the 9-step state machine that turns a
// Proxmox template into a running, reachable VM. It owns the clone →
// tag → resize → config → start → wait-IP → wait-SSH sequence, the
// snippet upload, the IP-picking heuristic, and the SSH reachability
// wait.
package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/eugenetaranov/pmox/internal/hook"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/snippet"
)

// HookError wraps a hook execution failure. It is the sentinel type
// exitcode.From maps to ExitHook via errors.As.
type HookError struct {
	Hook string
	Err  error
}

func (e *HookError) Error() string { return fmt.Sprintf("%s hook failed: %v", e.Hook, e.Err) }
func (e *HookError) Unwrap() error { return e.Err }

// IsHookError is a marker method used by internal/exitcode to detect
// HookError via a local interface. Using an interface avoids an import
// cycle between exitcode and launch.
func (e *HookError) IsHookError() {}

// Progress receives phase-level UI callbacks. A nil Progress is valid —
// Run checks and no-ops. Start is called before a phase begins; Done is
// called after the phase completes (err is nil on success). Implementations
// must be safe to call from a single goroutine in order.
type Progress interface {
	Start(step string)
	Done(err error)
}

// Options bundles everything Run needs to launch a VM.
type Options struct {
	Client         *pveclient.Client
	Node           string
	Name           string
	TemplateName   string
	TemplateID     int
	CPU            int
	MemMB          int
	DiskSize       string
	Storage        string
	SnippetStorage string
	Bridge         string
	Wait           time.Duration
	NoWaitSSH      bool
	CloudInitPath  string
	// Hook is an optional post-SSH-ready hook (--post-create, --tack,
	// --ansible). Nil means no hook phase.
	Hook hook.Hook
	// StrictHooks upgrades hook failure from a stderr warning (Run
	// returns nil) to a fatal error returned as *HookError.
	StrictHooks bool
	// User is the SSH login user passed to hooks via PMOX_USER and to
	// tack/ansible as --user / -u.
	User string
	// SSHKeyPath is the private-key path passed to ansible as
	// --private-key. Empty if unknown.
	SSHKeyPath string
	// WaitForSSHFn is a test seam. When non-nil, the launch state
	// machine calls it instead of the real WaitForSSH so hook tests
	// can run without a live SSH endpoint. Production code leaves
	// it nil.
	WaitForSSHFn func(ctx context.Context, ip string, timeout time.Duration) error
	// UploadSnippet writes the cloud-init snippet to the PVE node's
	// snippets/ directory via SFTP. PVE's HTTP /upload endpoint
	// rejects content=snippets, so the launcher cannot use the API
	// path. The CLI layer injects a closure that lazily dials pvessh.
	UploadSnippet func(ctx context.Context, storagePath, filename string, content []byte) error
	Stderr        io.Writer
	Verbose       bool
	Progress      Progress
}

func (o Options) pStart(step string) {
	if o.Progress != nil {
		o.Progress.Start(step)
	}
}

func (o Options) pDone(err error) {
	if o.Progress != nil {
		o.Progress.Done(err)
	}
}

// Result is the launch state-machine success payload.
type Result struct {
	VMID int
	IP   string
}

// Run walks the 9-step launch state machine and returns the allocated
// VMID and discovered IPv4. Any failure after step 2 (clone) leaves
// the VM on the cluster tagged with `pmox` — no automatic rollback.
func Run(ctx context.Context, opts Options) (*Result, error) {
	// Phase 0 — read and validate the per-server cloud-init file
	// BEFORE the first PVE API call so a bad or missing file fails
	// fast without leaving an orphan VM on the cluster.
	if opts.CloudInitPath == "" {
		return nil, errors.New("cloud-init path is empty; this is a programming bug — the CLI layer must populate Options.CloudInitPath from config.CloudInitPath(canonicalURL)")
	}
	cloudInitBytes, err := os.ReadFile(opts.CloudInitPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read cloud-init file %s: %w\n  hint: run 'pmox configure --regen-cloud-init' to write a fresh default, or create the file manually", opts.CloudInitPath, err)
		}
		return nil, fmt.Errorf("read cloud-init file %s: %w", opts.CloudInitPath, err)
	}
	if err := snippet.ValidateContent(cloudInitBytes); err != nil {
		return nil, fmt.Errorf("validate cloud-init file %s: %w", opts.CloudInitPath, err)
	}
	if opts.UploadSnippet == nil {
		return nil, errors.New("UploadSnippet is nil; this is a programming bug — the CLI layer must inject an SFTP upload closure")
	}
	if err := snippet.ValidateStorage(ctx, opts.Client, opts.Node, opts.SnippetStorage); err != nil {
		return nil, err
	}
	snippetStoragePath, err := opts.Client.GetStoragePath(ctx, opts.SnippetStorage)
	if err != nil {
		return nil, fmt.Errorf("resolve snippet storage path for %q: %w", opts.SnippetStorage, err)
	}
	if !snippet.HasSSHKeys(cloudInitBytes) && opts.Stderr != nil {
		fmt.Fprintf(opts.Stderr, "warning: cloud-init file %s has no ssh_authorized_keys; you may not be able to SSH in\n", opts.CloudInitPath)
	}

	// Phase 1 — allocate VMID.
	opts.pStart("Allocating VMID")
	vmid, err := opts.Client.NextID(ctx)
	opts.pDone(err)
	if err != nil {
		return nil, fmt.Errorf("allocate vmid: %w", err)
	}

	// Phase 2 — clone template.
	opts.pStart(fmt.Sprintf("Cloning template %d → vm %d", opts.TemplateID, vmid))
	upid, err := opts.Client.Clone(ctx, opts.Node, opts.TemplateID, vmid, opts.Name)
	if err != nil {
		opts.pDone(err)
		return nil, fmt.Errorf("clone template: %w", err)
	}
	if err := opts.Client.WaitTask(ctx, opts.Node, upid, 120*time.Second); err != nil {
		opts.pDone(err)
		return nil, fmt.Errorf("wait for clone task: %w", err)
	}
	opts.pDone(nil)

	// Phase 3 — tag BEFORE resize, per D-T1. Any later failure leaves
	// the VM tagged and cleanable via `pmox delete`.
	opts.pStart(fmt.Sprintf("Tagging vm %d as pmox", vmid))
	if err := opts.Client.SetConfig(ctx, opts.Node, vmid, map[string]string{"tags": "pmox"}); err != nil {
		opts.pDone(err)
		return nil, fmt.Errorf("tag vm %d: %w (vm exists on cluster, run pmox delete %d)", vmid, err, vmid)
	}
	opts.pDone(nil)

	// Phase 4 — resize disk.
	opts.pStart(fmt.Sprintf("Resizing disk to %s", opts.DiskSize))
	if err := opts.Client.Resize(ctx, opts.Node, vmid, "scsi0", opts.DiskSize); err != nil {
		opts.pDone(err)
		return nil, fmt.Errorf("resize disk on vm %d: %w (run pmox delete %d)", vmid, err, vmid)
	}
	opts.pDone(nil)

	// Phase 5 — upload snippet via SFTP, push cloud-init + resource
	// config. SFTP (not the PVE HTTP upload endpoint) because PVE's
	// /upload rejects content=snippets with a hardcoded 400.
	opts.pStart("Pushing cloud-init config")
	if err := opts.UploadSnippet(ctx, snippetStoragePath, snippet.Filename(vmid), cloudInitBytes); err != nil {
		opts.pDone(err)
		return nil, fmt.Errorf("upload cloud-init snippet for vm %d: %w (run pmox delete %d)", vmid, err, vmid)
	}
	kv := BuildCustomKV(opts, vmid)
	if err := opts.Client.SetConfig(ctx, opts.Node, vmid, kv); err != nil {
		opts.pDone(err)
		return nil, fmt.Errorf("push cloud-init config on vm %d: %w (run pmox delete %d)", vmid, err, vmid)
	}
	opts.pDone(nil)

	// Phase 6 — start + wait for start task.
	opts.pStart(fmt.Sprintf("Starting vm %d", vmid))
	startUPID, err := opts.Client.Start(ctx, opts.Node, vmid)
	if err != nil {
		opts.pDone(err)
		return nil, fmt.Errorf("start vm %d: %w (run pmox delete %d)", vmid, err, vmid)
	}
	if err := opts.Client.WaitTask(ctx, opts.Node, startUPID, 60*time.Second); err != nil {
		opts.pDone(err)
		return nil, fmt.Errorf("wait for start task on vm %d: %w (run pmox delete %d)", vmid, err, vmid)
	}
	opts.pDone(nil)

	// Phase 7 — wait for the guest agent to report a usable IPv4.
	waitBudget := opts.Wait
	if waitBudget <= 0 {
		waitBudget = 3 * time.Minute
	}
	overallDeadline := time.Now().Add(waitBudget)
	opts.pStart("Waiting for guest agent to report IP")
	ip, err := WaitForIP(ctx, opts.Client, opts.Node, vmid, waitBudget)
	opts.pDone(err)
	if err != nil {
		return nil, fmt.Errorf("%w (run pmox delete %d)", err, vmid)
	}

	// Phase 8 — wait for sshd to complete a handshake, unless skipped.
	if !opts.NoWaitSSH {
		opts.pStart(fmt.Sprintf("Waiting for ssh on %s", ip))
		waitFn := opts.WaitForSSHFn
		if waitFn == nil {
			waitFn = WaitForSSH
		}
		err := waitFn(ctx, ip, waitBudget)
		opts.pDone(err)
		if err != nil {
			return nil, fmt.Errorf("%w (run pmox delete %d)", err, vmid)
		}
	}

	// Phase 10 — run the post-SSH hook if configured.
	if opts.Hook != nil {
		if opts.NoWaitSSH {
			if opts.Stderr != nil {
				fmt.Fprintln(opts.Stderr, "warning: --no-wait-ssh set; hook will not run")
			}
		} else {
			hookBudget := time.Until(overallDeadline)
			if hookBudget < 30*time.Second {
				hookBudget = 30 * time.Second
			}
			hookCtx, cancel := context.WithTimeout(ctx, hookBudget)
			env := hook.Env{
				IP:     ip,
				Name:   opts.Name,
				VMID:   vmid,
				User:   opts.User,
				Node:   opts.Node,
				SSHKey: opts.SSHKeyPath,
			}
			opts.pStart(fmt.Sprintf("Running %s hook", opts.Hook.Name()))
			hookErr := opts.Hook.Run(hookCtx, env, os.Stdout, os.Stderr)
			cancel()
			opts.pDone(hookErr)
			if hookErr != nil {
				if opts.Stderr != nil {
					fmt.Fprintf(opts.Stderr, "warning: %s hook failed: %v\n", opts.Hook.Name(), hookErr)
				}
				if opts.StrictHooks {
					return nil, &HookError{Hook: opts.Hook.Name(), Err: hookErr}
				}
			}
		}
	}

	// Phase 9 — done.
	return &Result{VMID: vmid, IP: ip}, nil
}

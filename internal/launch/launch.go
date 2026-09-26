// Package launch implements the 9-step state machine that turns a
// Proxmox template into a running, reachable VM. It owns the clone →
// tag → resize → config → start → wait-IP → wait-SSH sequence and the
// snippet upload. The IP-picking heuristic and the IP/SSH waits live
// in internal/vmwait.
package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/hook"
	"github.com/eugenetaranov/pmox/internal/progress"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/snippet"
	"github.com/eugenetaranov/pmox/internal/vm"
	"github.com/eugenetaranov/pmox/internal/vmwait"
)

// HookError wraps a hook execution failure. It implements
// exitcode.Coder, so exitcode.From maps it to ExitHook.
type HookError struct {
	Hook string
	Err  error
}

func (e *HookError) Error() string { return fmt.Sprintf("%s hook failed: %v", e.Hook, e.Err) }
func (e *HookError) Unwrap() error { return e.Err }

// ExitCode implements exitcode.Coder: a failed hook exits with ExitHook.
func (e *HookError) ExitCode() int { return exitcode.ExitHook }

// Progress receives phase-level UI callbacks. A nil Progress is valid —
// Run checks and no-ops. See progress.Reporter.
type Progress = progress.Reporter

// Options bundles everything Run needs to launch a VM.
type Options struct {
	Client         *pveclient.Client
	Node           string
	Name           string
	TemplateID     int
	CPU            int
	MemMB          int
	DiskSize       string
	Storage        string
	SnippetStorage string
	// Bridge, when non-empty, replaces the bridge= parameter of the
	// cloned VM's net0 (model, MAC and other params are preserved).
	Bridge string
	// Wait is the total budget for the wait-IP and wait-SSH phases
	// combined. Zero means 3 minutes.
	Wait          time.Duration
	NoWaitSSH     bool
	CloudInitPath string
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
	// SSHInsecure skips SSH host-key verification for the hook (tack).
	SSHInsecure bool
	// ServerURL is the canonical Proxmox server URL, passed to the hook
	// as hook.Env.ServerURL. A TackHook uses it to remember the playbook
	// it ran (see internal/tackprofile) so a later bare `pmox apply <vm>`
	// reuses it instead of silently falling back to the default
	// playbook. Empty is safe — the hook simply won't remember.
	ServerURL string
	// WaitForSSHFn is a test seam. When non-nil, the launch state
	// machine calls it instead of the real vmwait.WaitForSSH so hook
	// tests can run without a live SSH endpoint. Production code
	// leaves it nil.
	WaitForSSHFn func(ctx context.Context, ip string, timeout time.Duration) error
	// PollInterval overrides vmwait.DefaultPollInterval for the
	// wait-IP / wait-SSH phases. Test seam; production leaves it zero.
	PollInterval time.Duration
	// UploadSnippet writes the cloud-init snippet to the PVE node's
	// snippets/ directory via SFTP. PVE's HTTP /upload endpoint
	// rejects content=snippets, so the launcher cannot use the API
	// path. The CLI layer injects a closure that lazily dials pvessh.
	UploadSnippet func(ctx context.Context, storagePath, filename string, content []byte) error
	// Stderr receives launch warnings; nil suppresses them. Hook
	// stderr also goes here, falling back to os.Stderr when nil.
	Stderr io.Writer
	// Stdout receives hook stdout; nil means os.Stdout.
	Stdout   io.Writer
	Progress Progress
}

func (o Options) pStart(step string) { progress.Start(o.Progress, step) }
func (o Options) pDone(err error)    { progress.Done(o.Progress, err) }

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
			return nil, fmt.Errorf("read cloud-init file %s: %w\n  hint: run 'pmox init --regen-cloud-init' to write a fresh default, or create the file manually", opts.CloudInitPath, err)
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
		// The clone task may already have created vm %d server-side when
		// the wait fails or is interrupted. Tagging is the next phase and
		// hasn't run yet, so the VM is NOT tagged "pmox" — the cleanup
		// hint must use --force, which every later phase's hint can omit
		// because they run after tagging.
		return nil, fmt.Errorf("wait for clone task: %w (vm %d may exist on the cluster untagged, run pmox delete --force %d)", err, vmid, vmid)
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
	if opts.Bridge != "" {
		net0, err := bridgedNet0(ctx, opts, vmid)
		if err != nil {
			opts.pDone(err)
			return nil, fmt.Errorf("set bridge on vm %d: %w (run pmox delete %d)", vmid, err, vmid)
		}
		if net0 != "" {
			kv["net0"] = net0
		}
	}
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

	// Phases 7–8 share a single --wait budget: the SSH wait gets
	// whatever the IP wait left over, so the worst case is Wait, not
	// 2×Wait.
	waitBudget := opts.Wait
	if waitBudget <= 0 {
		waitBudget = 3 * time.Minute
	}
	overallDeadline := time.Now().Add(waitBudget)
	var waitOpts []vmwait.Option
	if opts.PollInterval > 0 {
		waitOpts = append(waitOpts, vmwait.WithPollInterval(opts.PollInterval))
	}

	// Phase 7 — wait for the guest agent to report a usable IPv4.
	opts.pStart("Waiting for guest agent to report IP")
	ip, err := vmwait.WaitForIP(ctx, opts.Client, opts.Node, vmid, waitBudget, waitOpts...)
	opts.pDone(err)
	if err != nil {
		return nil, fmt.Errorf("%w (run pmox delete %d)", err, vmid)
	}

	// Phase 8 — wait for sshd to complete a handshake, unless skipped.
	if !opts.NoWaitSSH {
		opts.pStart(fmt.Sprintf("Waiting for ssh on %s", ip))
		waitFn := opts.WaitForSSHFn
		if waitFn == nil {
			waitFn = func(ctx context.Context, ip string, timeout time.Duration) error {
				return vmwait.WaitForSSH(ctx, ip, timeout, waitOpts...)
			}
		}
		err := waitFn(ctx, ip, time.Until(overallDeadline))
		opts.pDone(err)
		if err != nil {
			return nil, fmt.Errorf("%w (run pmox delete %d)", err, vmid)
		}
	}

	// Phase 9 — mark ready: the VM is cloned, tagged, resized, cloud-init
	// pushed, started, and confirmed reachable. Set before any hook, so
	// a hook failure (which --strict-hooks can turn into a launch
	// failure) never un-ready an otherwise fully working VM. Best-effort:
	// a failure here doesn't undo one either.
	if err := opts.Client.SetConfig(ctx, opts.Node, vmid, map[string]string{"tags": "pmox;" + vm.ReadyTag}); err != nil && opts.Stderr != nil {
		fmt.Fprintf(opts.Stderr, "warning: could not mark vm %d ready (harmless: 'pmox cleanup's opt-in vm category may flag it): %v\n", vmid, err)
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
				IP:        ip,
				Name:      opts.Name,
				VMID:      vmid,
				User:      opts.User,
				Node:      opts.Node,
				SSHKey:    opts.SSHKeyPath,
				Insecure:  opts.SSHInsecure,
				ServerURL: opts.ServerURL,
			}
			stdout, stderr := opts.Stdout, opts.Stderr
			if stdout == nil {
				stdout = os.Stdout
			}
			if stderr == nil {
				stderr = os.Stderr
			}
			opts.pStart(fmt.Sprintf("Running %s hook", opts.Hook.Name()))
			hookErr := opts.Hook.Run(hookCtx, env, stdout, stderr)
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

// bridgedNet0 reads the cloned VM's net0 and returns it with its
// bridge set to opts.Bridge. It returns "" when net0 already uses that
// bridge, so the config push leaves the NIC untouched.
func bridgedNet0(ctx context.Context, opts Options, vmid int) (string, error) {
	cfg, err := opts.Client.GetConfig(ctx, opts.Node, vmid)
	if err != nil {
		return "", fmt.Errorf("read net0: %w", err)
	}
	cur := cfg["net0"]
	next := setNet0Bridge(cur, opts.Bridge)
	if next == cur {
		return "", nil
	}
	return next, nil
}

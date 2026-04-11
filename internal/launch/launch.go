// Package launch implements the 9-step state machine that turns a
// Proxmox template into a running, reachable VM. It owns the clone →
// tag → resize → config → start → wait-IP → wait-SSH sequence, the
// built-in cloud-init key set, the IP-picking heuristic, and the SSH
// reachability wait.
package launch

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

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
	Client       *pveclient.Client
	Node         string
	Name         string
	User         string
	SSHPubKey    string
	TemplateName string
	TemplateID   int
	CPU          int
	MemMB        int
	DiskSize     string
	Storage      string
	Bridge       string
	Wait         time.Duration
	NoWaitSSH    bool
	Stderr       io.Writer
	Verbose      bool
	Progress     Progress
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

	// Phase 5 — push cloud-init + resource config.
	opts.pStart("Pushing cloud-init config")
	if err := opts.Client.SetConfig(ctx, opts.Node, vmid, BuildBuiltinKV(opts, vmid)); err != nil {
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
	opts.pStart("Waiting for guest agent to report IP")
	ip, err := WaitForIP(ctx, opts.Client, opts.Node, vmid, waitBudget)
	opts.pDone(err)
	if err != nil {
		return nil, fmt.Errorf("%w (run pmox delete %d)", err, vmid)
	}

	// Phase 8 — wait for sshd to complete a handshake, unless skipped.
	if !opts.NoWaitSSH {
		opts.pStart(fmt.Sprintf("Waiting for ssh on %s", ip))
		err := WaitForSSH(ctx, ip, waitBudget)
		opts.pDone(err)
		if err != nil {
			return nil, fmt.Errorf("%w (run pmox delete %d)", err, vmid)
		}
	}

	// Phase 9 — done.
	return &Result{VMID: vmid, IP: ip}, nil
}

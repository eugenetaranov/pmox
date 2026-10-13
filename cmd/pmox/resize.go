package main

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// The disk pmox VMs boot from (launch resizes the same one).
const resizeDisk = "scsi0"

func newResizeCmd() *cobra.Command {
	return nounGroup("resize", "Change a VM's memory, CPU cores or disk size (ram, cpu, disk)",
		`Change a VM's memory (ram), CPU cores (cpu) or disk size (disk).

Memory and cores take effect when the VM is stopped and started (a reboot
from inside isn't enough): on a running VM pmox asks to do that now,
--restart does it without asking, and --no-restart leaves it for later.
A disk only grows (Proxmox can't shrink one); on a running VM pmox grows
the partition and filesystem too, with no restart.`,
		newResizeRAMCmd(), newResizeCPUCmd(), newResizeDiskCmd(),
	)
}

type resizeFlags struct {
	ssh       sshFlags
	restart   bool
	noRestart bool
}

// resizeArgs splits [vm] <value>: the VM is optional (the only pmox VM,
// or a picker).
func resizeArgs(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, args []string) (vmArg, value string, err error) {
	value = args[len(args)-1]
	vmArg, err = resolveTargetArg(ctx, client, args[:len(args)-1], cmd.ErrOrStderr())
	return vmArg, value, err
}

func addRestartFlags(cmd *cobra.Command, f *resizeFlags) {
	cmd.Flags().BoolVar(&f.restart, "restart", false, "stop and start a running VM now, without asking, so the change applies")
	cmd.Flags().BoolVar(&f.noRestart, "no-restart", false, "leave a running VM as it is; the change applies at its next stop and start")
	cmd.MarkFlagsMutuallyExclusive("restart", "no-restart")
}

func newResizeRAMCmd() *cobra.Command {
	f := &resizeFlags{}
	cmd := &cobra.Command{
		Use:     "ram [name|vmid] <GiB>",
		Aliases: []string{"mem", "memory"},
		Short:   "Set a VM's memory (GiB, e.g. 8 or 1.5)",
		Example: `  pmox vm resize ram web1 8
  pmox vm resize ram web1 16 --restart`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResizeSetting(cmd, args, f, "memory")
		},
	}
	addRestartFlags(cmd, f)
	return cmd
}

func newResizeCPUCmd() *cobra.Command {
	f := &resizeFlags{}
	cmd := &cobra.Command{
		Use:     "cpu [name|vmid] <cores>",
		Aliases: []string{"cores"},
		Short:   "Set a VM's CPU cores",
		Example: `  pmox vm resize cpu web1 4`,
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResizeSetting(cmd, args, f, "cores")
		},
	}
	addRestartFlags(cmd, f)
	return cmd
}

// parseGiB reads a memory size in GiB ("8", "1.5") as MiB.
func parseGiB(s string) (int, error) {
	g, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(strings.ToUpper(s), "B"), "G"), 64)
	if err != nil || g <= 0 || math.IsInf(g, 0) {
		return 0, fmt.Errorf("%w: memory %q: want GiB, like 8 or 1.5", exitcode.ErrUserInput, s)
	}
	mib := int(math.Round(g * 1024))
	if mib < 512 {
		return 0, fmt.Errorf("%w: memory %q: at least 0.5 GiB", exitcode.ErrUserInput, s)
	}
	return mib, nil
}

func gib(mib int) string { return strconv.FormatFloat(float64(mib)/1024, 'f', -1, 64) + " GiB" }

// resizeRestartFn stops and starts the VM so the change applies. A seam
// for tests.
var resizeRestartFn = func(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, arg string) error {
	if err := executeStop(ctx, cmd, client, arg, &stopFlags{}); err != nil {
		return err
	}
	return executeStart(ctx, cmd, client, arg, &startFlags{wait: defaultWait})
}

// resizeConfirmFn asks whether to restart now. A seam for tests.
var resizeConfirmFn = func(q string) (bool, error) { return tui.Confirm(q, false) }

func runResizeSetting(cmd *cobra.Command, args []string, f *resizeFlags, key string) error {
	ctx := cmd.Context()
	client, _, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	vmArg, value, err := resizeArgs(ctx, cmd, client, args)
	if err != nil {
		return err
	}
	var want int
	if key == "memory" {
		if want, err = parseGiB(value); err != nil {
			return err
		}
	} else if want, err = strconv.Atoi(value); err != nil || want < 1 || want > 512 {
		return fmt.Errorf("%w: cores %q: want a whole number, 1 or more", exitcode.ErrUserInput, value)
	}
	return executeResizeSetting(ctx, cmd, client, vmArg, key, want, f)
}

func executeResizeSetting(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, vmArg, key string, want int, f *resizeFlags) error {
	ref, err := vm.Resolve(ctx, client, vmArg)
	if err != nil {
		return err
	}
	if err := ref.RequirePMOXTag("resize", f.ssh.force); err != nil {
		return err
	}
	cfg, err := client.GetConfig(ctx, ref.Node, ref.VMID)
	if err != nil {
		return fmt.Errorf("read vm %d config: %w", ref.VMID, err)
	}
	cur, _ := strconv.Atoi(strings.TrimSpace(cfg[key]))
	show := func(v int) string {
		if key == "memory" {
			return gib(v)
		}
		return strconv.Itoa(v)
	}
	what := map[string]string{"memory": "memory", "cores": "cores"}[key]
	out := cmd.OutOrStdout()
	if cur == want {
		fmt.Fprintf(out, "%s%s already has %s %s\n", doneMark(true), ref.Name, what, show(want))
		return nil
	}
	kv := map[string]string{key: strconv.Itoa(want)}
	if key == "memory" {
		// A balloon above the new size would be refused; follow it.
		if b, err := strconv.Atoi(cfg["balloon"]); err == nil && b > want {
			kv["balloon"] = strconv.Itoa(want)
		}
	}
	if err := client.SetConfig(ctx, ref.Node, ref.VMID, kv); err != nil {
		return fmt.Errorf("set %s on vm %d: %w", what, ref.VMID, err)
	}
	fmt.Fprintf(out, "%s%s: %s %s → %s\n", doneMark(true), ref.Name, what, show(cur), show(want))

	st, err := client.GetStatus(ctx, ref.Node, ref.VMID)
	if err != nil || !st.IsRunning() {
		return nil // a stopped VM gets it at its next start
	}
	restart := f.restart
	if !f.restart && !f.noRestart && tui.Interactive() {
		if restart, err = resizeConfirmFn(fmt.Sprintf("%s is running: stop and start it now so the new %s applies?", ref.Name, what)); err != nil {
			return err
		}
	}
	if !restart {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s applies when %s is stopped and started: pmox stop %s && pmox start %s\n", what, ref.Name, ref.Name, ref.Name)
		return nil
	}
	return resizeRestartFn(ctx, cmd, client, ref.Name)
}

func newResizeDiskCmd() *cobra.Command {
	f := &resizeFlags{}
	cmd := &cobra.Command{
		Use:   "disk [name|vmid] <+GiB | GiB>",
		Short: "Grow a VM's disk (+20 grows by 20 GiB; 60 grows it to 60 GiB)",
		Long: `Grow the VM's disk: +N grows it by N GiB, N grows it to N GiB in all.
Proxmox can't shrink a disk, so a size below the current one is refused.
On a running VM pmox then grows the partition and the filesystem over SSH
(no restart); a stopped VM's grow at its next boot (cloud-init).`,
		Example: `  pmox vm resize disk web1 +20
  pmox vm resize disk web1 60`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResizeDisk(cmd, args, f)
		},
	}
	addSSHFlags(cmd, &f.ssh)
	return cmd
}

var diskSizeRe = regexp.MustCompile(`^(\+?)([0-9]+)(?:G|GB|GiB)?$`)

// diskGiB reads a disk size from Proxmox ("local-lvm:vm-101-disk-0,size=32G").
func diskGiB(spec string) (float64, bool) {
	for _, p := range strings.Split(spec, ",") {
		v, ok := strings.CutPrefix(p, "size=")
		if !ok || v == "" {
			continue
		}
		n, err := strconv.ParseFloat(v[:len(v)-1], 64)
		if err != nil {
			return 0, false
		}
		switch v[len(v)-1] {
		case 'T':
			return n * 1024, true
		case 'G':
			return n, true
		case 'M':
			return n / 1024, true
		case 'K':
			return n / 1024 / 1024, true
		}
	}
	return 0, false
}

// growFSCommand grows the root filesystem's partition and the filesystem
// (ext4 or xfs) over SSH. growpart says NOCHANGE (exit 1) when there's
// nothing to grow.
const growFSCommand = `sudo -n sh -c 'src=$(findmnt -no SOURCE /); dev=${src#/dev/}; part=$(cat /sys/class/block/$dev/partition 2>/dev/null || true); disk=/dev/$(lsblk -no PKNAME "$src"); if [ -n "$part" ]; then growpart "$disk" "$part" || true; fi; case $(findmnt -no FSTYPE /) in xfs) xfs_growfs / ;; *) resize2fs "$src" ;; esac'`

func runResizeDisk(cmd *cobra.Command, args []string, f *resizeFlags) error {
	ctx := cmd.Context()
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	vmArg, value, err := resizeArgs(ctx, cmd, client, args)
	if err != nil {
		return err
	}
	return executeResizeDisk(ctx, cmd, client, resolved, vmArg, value, f)
}

func executeResizeDisk(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, resolved *server.Resolved, vmArg, value string, f *resizeFlags) error {
	m := diskSizeRe.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return fmt.Errorf("%w: disk size %q: want +N (grow by N GiB) or N (grow to N GiB)", exitcode.ErrUserInput, value)
	}
	n, _ := strconv.Atoi(m[2])
	grow := m[1] == "+"
	if n < 1 {
		return fmt.Errorf("%w: disk size %q: nothing to grow", exitcode.ErrUserInput, value)
	}
	ref, err := vm.Resolve(ctx, client, vmArg)
	if err != nil {
		return err
	}
	if err := ref.RequirePMOXTag("resize", f.ssh.force); err != nil {
		return err
	}
	cfg, err := client.GetConfig(ctx, ref.Node, ref.VMID)
	if err != nil {
		return fmt.Errorf("read vm %d config: %w", ref.VMID, err)
	}
	cur, known := diskGiB(cfg[resizeDisk])
	size := strconv.Itoa(n) + "G"
	target := float64(n)
	if grow {
		size = "+" + size
		target = cur + float64(n)
	} else if known && target <= cur {
		if target == cur {
			fmt.Fprintf(cmd.OutOrStdout(), "%s%s's disk is already %d GiB\n", doneMark(true), ref.Name, n)
			return nil
		}
		return fmt.Errorf("%w: %s's disk is %s GiB; Proxmox can't shrink a disk (grow it, or make a new VM)", exitcode.ErrUserInput, ref.Name, strconv.FormatFloat(cur, 'f', -1, 64))
	}
	sp := startSpin(fmt.Sprintf("Growing %s's disk to %s GiB…", ref.Name, strconv.FormatFloat(target, 'f', -1, 64)))
	err = client.Resize(ctx, ref.Node, ref.VMID, resizeDisk, size)
	sp.Stop()
	if err != nil {
		return fmt.Errorf("resize disk on vm %d: %w", ref.VMID, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s%s: disk %s → %s GiB\n", doneMark(true), ref.Name,
		map[bool]string{true: strconv.FormatFloat(cur, 'f', -1, 64), false: "?"}[known], strconv.FormatFloat(target, 'f', -1, 64))

	st, err := client.GetStatus(ctx, ref.Node, ref.VMID)
	if err != nil || !st.IsRunning() {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: the filesystem grows when %s next boots\n", ref.Name)
		return nil
	}
	target2, err := resolveSSHTarget(ctx, cmd, client, ref.Name, &f.ssh, resolved.URL, resolved.Server)
	if err != nil {
		return fmt.Errorf("disk grown; the filesystem grows at the next boot (couldn't connect now: %w)", err)
	}
	sp = startSpin(fmt.Sprintf("Growing %s's filesystem…", ref.Name))
	out, err := resizeRemoteFn(ctx, target2, growFSCommand, 2*time.Minute)
	sp.Stop()
	if err != nil {
		return fmt.Errorf("disk grown, but growing the filesystem failed (it grows at the next boot): %w: %s", err, lastLine(strings.TrimSpace(out)))
	}
	df, _ := resizeRemoteFn(ctx, target2, "df -h / | tail -1", 30*time.Second)
	fmt.Fprintf(cmd.OutOrStdout(), "%sfilesystem grown: %s\n", doneMark(true), strings.Join(strings.Fields(df), " "))
	return nil
}

// resizeRemoteFn runs a command on the VM. A seam for tests.
var resizeRemoteFn = func(ctx context.Context, target *sshTarget, command string, timeout time.Duration) (string, error) {
	return yardoRemoteFn(ctx, target, command, timeout)
}


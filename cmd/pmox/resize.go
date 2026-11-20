package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
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
	f := &resizeFlags{}
	cmd := &cobra.Command{
		Use:   "resize [name|vmid]",
		Short: "Change a VM's CPU cores, memory or disk size",
		Long: `Change a VM's CPU cores (--cpu), memory (--mem) or disk size (--disk),
in any combination, with the same flag names 'pmox launch' takes.

Cores and memory take effect when the VM is stopped and started (a
reboot from inside isn't enough): on a running VM pmox asks to do that
now, --restart does it without asking, and --no-restart leaves it for
later.

A disk only grows (Proxmox can't shrink one): --disk +20 grows it by
20 GiB, --disk 60 grows it to 60 GiB. On a running VM pmox grows the
partition and filesystem too, with no restart; on a stopped one they
grow at its next boot.

With no VM named, pmox picks the only pmox VM or shows the picker. The
default VM is not used.`,
		Example: `  pmox resize web1 --mem 8
  pmox resize web1 --cpu 4 --mem 16 --restart
  pmox resize web1 --disk +20`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResize(cmd, args, f)
		},
	}
	cmd.Flags().StringVar(&f.cpu, "cpu", "", "number of vCPU cores")
	cmd.Flags().StringVar(&f.mem, "mem", "", "memory in GiB, e.g. 8 or 1.5")
	cmd.Flags().StringVar(&f.disk, "disk", "", "grow the disk: +N by N GiB, N to N GiB")
	addRestartFlags(cmd, f)
	addSSHFlags(cmd, &f.ssh)
	// The old 'resize cpu|ram|disk <vm> <value>' forms.
	cmd.AddCommand(
		deprecated(newResizeRAMCmd(), "pmox resize <vm> --mem <GiB>"),
		deprecated(newResizeCPUCmd(), "pmox resize <vm> --cpu <cores>"),
		deprecated(newResizeDiskCmd(), "pmox resize <vm> --disk <+GiB|GiB>"),
	)
	return cmd
}

// resizeSelectFn asks which setting to change. A seam for tests.
var resizeSelectFn = tui.Select

// runResize is 'pmox resize [vm] --cpu/--mem/--disk': every value is
// checked before anything changes, the disk too (a refused shrink leaves
// cores and memory alone), and a running VM is restarted at most once.
func runResize(cmd *cobra.Command, args []string, f *resizeFlags) error {
	ctx := cmd.Context()
	if f.cpu == "" && f.mem == "" && f.disk == "" {
		if !tui.Interactive() {
			return fmt.Errorf("%w: nothing to change — pass --cpu <cores>, --mem <GiB> and/or --disk <+GiB|GiB> (example: pmox resize web1 --mem 8)", exitcode.ErrUserInput)
		}
		// From the command palette: ask what to change, then the value.
		which, err := resizeSelectFn("What to change", []huh.Option[string]{
			huh.NewOption("Memory", "mem"), huh.NewOption("CPU cores", "cpu"), huh.NewOption("Disk size", "disk"),
		})
		if err != nil {
			return err
		}
		ask := map[string]string{"mem": "Memory in GiB (e.g. 8)", "cpu": "CPU cores", "disk": "Disk: +GiB to grow by, or GiB to grow to"}[which]
		v, err := askTextFn(ask)
		if err != nil {
			return err
		}
		v = strings.TrimSpace(v)
		switch which {
		case "mem":
			f.mem = v
		case "cpu":
			f.cpu = v
		default:
			f.disk = v
		}
	}
	var cores, memMiB int
	var err error
	if f.cpu != "" {
		if cores, err = parseCores(f.cpu); err != nil {
			return err
		}
	}
	if f.mem != "" {
		if memMiB, err = parseGiB(f.mem); err != nil {
			return err
		}
	}
	if f.disk != "" && diskSizeRe.FindStringSubmatch(strings.TrimSpace(f.disk)) == nil {
		return fmt.Errorf("%w: disk size %q: want +N (grow by N GiB) or N (grow to N GiB)", exitcode.ErrUserInput, f.disk)
	}

	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	vmArg, err := resolveTargetArg(ctx, client, args, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	return executeResize(ctx, cmd, client, resolved, vmArg, cores, memMiB, f.disk, f)
}

// executeResize applies the requested changes (0 / "" = leave alone).
func executeResize(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, resolved *server.Resolved, vmArg string, cores, memMiB int, disk string, f *resizeFlags) error {
	if disk != "" {
		if err := checkDiskGrow(ctx, client, vmArg, disk, f); err != nil {
			return err
		}
	}
	var pending []string // settings changed on a running VM
	var ref *vm.Ref
	for _, c := range []struct {
		key  string
		want int
	}{{"cores", cores}, {"memory", memMiB}} {
		if c.want == 0 {
			continue
		}
		r, needsRestart, err := applyResizeSetting(ctx, cmd, client, vmArg, c.key, c.want, f)
		if err != nil {
			return err
		}
		ref = r
		if needsRestart {
			pending = append(pending, c.key)
		}
	}
	if disk != "" {
		if err := executeResizeDisk(ctx, cmd, client, resolved, vmArg, disk, f); err != nil {
			return err
		}
	}
	if len(pending) == 0 {
		return nil
	}
	return finishResizeRestart(ctx, cmd, client, ref, strings.Join(pending, " and "), f)
}

// checkDiskGrow refuses, before anything is changed, a disk size below
// the current one.
func checkDiskGrow(ctx context.Context, client *pveclient.Client, vmArg, value string, f *resizeFlags) error {
	m := diskSizeRe.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil || m[1] == "+" {
		return nil // executeResizeDisk reports a malformed size
	}
	n, _ := strconv.Atoi(m[2])
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
	if cur, known := diskGiB(cfg[resizeDisk]); known && float64(n) < cur {
		return fmt.Errorf("%w: %s's disk is %s GiB; Proxmox can't shrink a disk (grow it, or make a new VM)", exitcode.ErrUserInput, ref.Name, strconv.FormatFloat(cur, 'f', -1, 64))
	}
	return nil
}

// parseCores reads a core count.
func parseCores(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 512 {
		return 0, fmt.Errorf("%w: cores %q: want a whole number, 1 or more", exitcode.ErrUserInput, s)
	}
	return n, nil
}

type resizeFlags struct {
	ssh       sshFlags
	restart   bool
	noRestart bool
	// The flag form's values, as typed ("" = not given).
	cpu, mem, disk string
}

// resizeArgs splits [vm] [value]: the VM is optional (the only pmox VM,
// or a picker), and on a terminal a missing value is asked for (the
// command palette runs the verb with no arguments).
func resizeArgs(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, args []string, ask string) (vmArg, value string, err error) {
	var vmArgs []string
	switch len(args) {
	case 1:
		value = args[0]
	case 2:
		vmArgs, value = args[:1], args[1]
	}
	if value == "" && !tui.Interactive() {
		return "", "", fmt.Errorf("%w: usage: %s", exitcode.ErrUserInput, cmd.UseLine())
	}
	if vmArg, err = resolveTargetArg(ctx, client, vmArgs, cmd.ErrOrStderr()); err != nil {
		return "", "", err
	}
	if value == "" {
		if value, err = askTextFn(fmt.Sprintf("%s for %s", ask, vmArg)); err != nil {
			return "", "", err
		}
	}
	return vmArg, strings.TrimSpace(value), nil
}

// askTextFn asks for one line of text on the terminal. A seam for tests.
var askTextFn = func(title string) (string, error) {
	var v string
	err := huh.NewInput().Title(title).Value(&v).
		Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("required")
			}
			return nil
		}).WithTheme(tui.Theme()).Run()
	return v, err
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
		Args: cobra.MaximumNArgs(2),
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
		Args:    cobra.MaximumNArgs(2),
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
var resizeConfirmFn = func(q string) (bool, error) { return tui.ConfirmLine(q, false) }

func runResizeSetting(cmd *cobra.Command, args []string, f *resizeFlags, key string) error {
	ctx := cmd.Context()
	client, _, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	ask := map[string]string{"memory": "Memory in GiB (e.g. 8)", "cores": "CPU cores"}[key]
	vmArg, value, err := resizeArgs(ctx, cmd, client, args, ask)
	if err != nil {
		return err
	}
	var want int
	if key == "memory" {
		if want, err = parseGiB(value); err != nil {
			return err
		}
	} else if want, err = parseCores(value); err != nil {
		return err
	}
	return executeResizeSetting(ctx, cmd, client, vmArg, key, want, f)
}

func executeResizeSetting(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, vmArg, key string, want int, f *resizeFlags) error {
	ref, needsRestart, err := applyResizeSetting(ctx, cmd, client, vmArg, key, want, f)
	if err != nil || !needsRestart {
		return err
	}
	return finishResizeRestart(ctx, cmd, client, ref, key, f)
}

// applyResizeSetting sets cores or memory. needsRestart reports that the
// value changed on a running VM, where it applies only after a stop and
// start.
func applyResizeSetting(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, vmArg, key string, want int, f *resizeFlags) (ref *vm.Ref, needsRestart bool, err error) {
	ref, err = vm.Resolve(ctx, client, vmArg)
	if err != nil {
		return nil, false, err
	}
	if err := ref.RequirePMOXTag("resize", f.ssh.force); err != nil {
		return nil, false, err
	}
	cfg, err := client.GetConfig(ctx, ref.Node, ref.VMID)
	if err != nil {
		return nil, false, fmt.Errorf("read vm %d config: %w", ref.VMID, err)
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
		return ref, false, nil
	}
	kv := map[string]string{key: strconv.Itoa(want)}
	if key == "memory" {
		// A balloon above the new size would be refused; follow it.
		if b, err := strconv.Atoi(cfg["balloon"]); err == nil && b > want {
			kv["balloon"] = strconv.Itoa(want)
		}
	}
	if err := client.SetConfig(ctx, ref.Node, ref.VMID, kv); err != nil {
		return nil, false, fmt.Errorf("set %s on vm %d: %w", what, ref.VMID, err)
	}
	fmt.Fprintf(out, "%s%s: %s %s → %s\n", doneMark(true), ref.Name, what, show(cur), show(want))

	st, err := client.GetStatus(ctx, ref.Node, ref.VMID)
	if err != nil || !st.IsRunning() {
		return ref, false, nil // a stopped VM gets it at its next start
	}
	return ref, true, nil
}

// finishResizeRestart stops and starts a running VM so that what (e.g.
// "cores and memory") applies: asked once on a terminal, at once with
// --restart, left for later with --no-restart or without a terminal.
func finishResizeRestart(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, ref *vm.Ref, what string, f *resizeFlags) error {
	restart := f.restart
	if !f.restart && !f.noRestart && tui.Interactive() {
		var err error
		if restart, err = resizeConfirmFn(fmt.Sprintf("%s is running: stop and start it now so the new %s applies?", ref.Name, what)); err != nil {
			return err
		}
	}
	if !restart {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s applies when %s is stopped and started: pmox restart %s\n", what, ref.Name, ref.Name)
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
		Args: cobra.MaximumNArgs(2),
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
	vmArg, value, err := resizeArgs(ctx, cmd, client, args, "Disk: +GiB to grow by, or GiB to grow to")
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

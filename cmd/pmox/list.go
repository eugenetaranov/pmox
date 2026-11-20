package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/vm"
	"github.com/eugenetaranov/pmox/internal/vmwait"
)

const (
	listIPConcurrency  = 8
	listWatchInterval  = 5 * time.Second
	listWatchMinPeriod = time.Second
)

type listFlags struct {
	all      bool
	wide     bool
	watch    bool
	interval time.Duration
	// context is the name the host line opens with.
	context string
}

// listTerminal reports whether w is a terminal and how wide; a var so
// tests can render the terminal view into a buffer.
var listTerminal = func(w io.Writer) (bool, int) {
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return false, 0
	}
	width, _, _ := term.GetSize(int(f.Fd()))
	return true, width
}

func newListCmd() *cobra.Command {
	f := &listFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List VMs",
		Long: `List the cluster's pmox VMs (the ones tagged 'pmox'), running ones
first, with each one's state, CPU and memory use, disk size, uptime and
IP; --all lists every VM. A stopped VM shows what it is allocated. A
running VM's IP comes from its guest agent and is blank while the agent
isn't up. A VM on an overlay network shows both addresses, LAN first:
'192.168.0.171 / 100.104.54.92 ts', the overlay one marked ts
(Tailscale) or nb (NetBird). The default VM, if one is set, is marked
with *.

On a terminal the table sits under a line per node (uptime, CPU, memory,
largest storage pool) and above a count of what is listed; a terminal
too narrow for the table loses DISK, UP, MEM, then CPU. Piped output is
the table alone, with every column. --wide adds VMID and NODE (NODE
shows by itself on a multi-node cluster).

--watch redraws the view every --interval until q or Ctrl-C.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListCmd(cmd, f)
		},
	}
	cmd.Flags().BoolVar(&f.all, "all", false, "show every VM on the cluster, not just pmox-tagged ones")
	cmd.Flags().BoolVar(&f.wide, "wide", false, "also show the VMID and NODE columns")
	cmd.Flags().BoolVarP(&f.watch, "watch", "w", false, "keep the view open and refresh it until q or Ctrl-C")
	cmd.Flags().DurationVar(&f.interval, "interval", listWatchInterval, "time between --watch refreshes (at least 1s)")
	return cmd
}

func runListCmd(cmd *cobra.Command, f *listFlags) error {
	if err := checkListWatch(cmd, f); err != nil {
		return err
	}
	ctx := cmd.Context()
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	if f.context = contextLabel(resolved); f.context == "" {
		f.context = resolved.URL
	}
	if f.watch {
		return runListWatch(ctx, cmd, client, f)
	}
	return executeList(ctx, cmd, client, f)
}

// checkListWatch rejects --watch where it can't draw and --interval
// where it means nothing.
func checkListWatch(cmd *cobra.Command, f *listFlags) error {
	if !f.watch {
		if cmd.Flags().Changed("interval") {
			return fmt.Errorf("%w: --interval only applies with --watch", exitcode.ErrUserInput)
		}
		return nil
	}
	if outputMode == "json" {
		return fmt.Errorf("%w: --watch draws a live table and can't be combined with -o json", exitcode.ErrUserInput)
	}
	if isTerm, _ := listTerminal(cmd.OutOrStdout()); !isTerm {
		return fmt.Errorf("%w: --watch needs a terminal; drop it to print the table once", exitcode.ErrUserInput)
	}
	if f.interval < listWatchMinPeriod {
		return fmt.Errorf("%w: --interval must be at least %s", exitcode.ErrUserInput, listWatchMinPeriod)
	}
	return nil
}

// listSnapshot is one reading of the cluster for the list view.
type listSnapshot struct {
	rows []vm.Row
	// nodes is nil when not asked for or when reading them failed.
	nodes []hostNode
	// hidden counts the VMs left out for not being pmox-managed.
	hidden    int
	multiNode bool
}

// listIP is a VM's address as last read, kept across --watch refreshes.
type listIP struct {
	status string
	row    vm.Row
}

// loadList reads the VMs (and, with withNodes, the nodes for the host
// line). cache, when non-nil, supplies the address of a VM whose state
// hasn't changed since it was read, and is updated.
func loadList(ctx context.Context, client *pveclient.Client, f *listFlags, withNodes bool, cache map[int]listIP) (*listSnapshot, error) {
	snap := &listSnapshot{}
	var nodesDone chan struct{}
	if withNodes {
		nodesDone = make(chan struct{})
		go func() {
			defer close(nodesDone)
			snap.nodes, _ = collectNodes(ctx, client)
		}()
	}
	resources, err := client.QEMUResources(ctx)
	if err != nil {
		if nodesDone != nil {
			<-nodesDone
		}
		return nil, fmt.Errorf("list cluster resources: %w", err)
	}
	nodeNames := map[string]bool{}
	snap.rows = make([]vm.Row, 0, len(resources))
	for _, r := range resources {
		nodeNames[r.Node] = true
		if !f.all && !vm.HasPMOXTag(r.Tags) {
			snap.hidden++
			continue
		}
		row := vm.Row{
			Name:        r.Name,
			VMID:        r.VMID,
			Node:        r.Node,
			Status:      r.Status,
			Default:     sessionDefault.name != "" && r.Name == sessionDefault.name && vm.HasPMOXTag(r.Tags),
			CPUs:        r.MaxCPU,
			CPUUsed:     r.CPU,
			MemBytes:    r.Mem,
			MaxMemBytes: r.MaxMem,
			DiskBytes:   r.MaxDisk,
			UptimeSec:   r.Uptime,
		}
		if c, ok := cache[r.VMID]; ok && c.status == r.Status {
			row.IP, row.Overlay = c.row.IP, c.row.Overlay
			row.LANIP, row.OverlayIP, row.OverlayNet = c.row.LANIP, c.row.OverlayIP, c.row.OverlayNet
		}
		snap.rows = append(snap.rows, row)
	}
	snap.multiNode = len(nodeNames) > 1
	fetchIPs(ctx, client, snap.rows)
	if cache != nil {
		clear(cache)
		for _, r := range snap.rows {
			if r.IP != "" {
				cache[r.VMID] = listIP{r.Status, r}
			}
		}
	}
	vm.SortRows(snap.rows)
	if nodesDone != nil {
		<-nodesDone
	}
	return snap, nil
}

func executeList(ctx context.Context, cmd *cobra.Command, client *pveclient.Client, f *listFlags) error {
	out := cmd.OutOrStdout()
	isTerm, width := listTerminal(out)
	json := outputMode == "json"
	sp := startSpin("Loading VMs…")
	defer sp.Stop()
	snap, err := loadList(ctx, client, f, isTerm && !json, nil)
	if err != nil {
		return err
	}
	sp.Stop()

	if json {
		return printJSON(out, snap.rows)
	}
	renderList(out, snap, f, isTerm, width)
	return nil
}

// renderList writes the list view: on a terminal the host line, the
// table and the count line; otherwise the table alone.
func renderList(w io.Writer, snap *listSnapshot, f *listFlags, isTerm bool, width int) {
	opts := vm.TableOpts{Terminal: isTerm, Color: isTerm && tui.ColorEnabled(), Width: width, Wide: f.wide, ShowNode: snap.multiNode}
	if !isTerm {
		vm.RenderTable(w, snap.rows, opts)
		return
	}
	if host := hostLines(f.context, snap.nodes); host != "" {
		fmt.Fprintln(w, host)
		fmt.Fprintln(w)
	}
	vm.RenderTable(w, snap.rows, opts)
	fmt.Fprintln(w)
	fmt.Fprintln(w, countLine(snap, f))
}

// hostLines is one line per node: uptime, CPU, memory and its largest
// storage pool for an online node, the status alone otherwise. The first
// opens with the context's name. Empty when there are no nodes to show.
func hostLines(context string, nodes []hostNode) string {
	var lines []string
	for _, n := range nodes {
		parts := []string{n.Name + " " + n.Status}
		if n.Status == "online" {
			parts = []string{
				fmt.Sprintf("%s up %s", n.Name, uptimeStr(n.UptimeSec)),
				fmt.Sprintf("CPU %.0f%% of %d", n.CPUUsed*100, n.Cores),
				fmt.Sprintf("mem %s/%s GiB", gibStr(n.MemBytes), gibStr(n.MaxMemBytes)),
			}
			var largest *hostStorage
			for i := range n.Storage {
				if largest == nil || n.Storage[i].SizeBytes > largest.SizeBytes {
					largest = &n.Storage[i]
				}
			}
			if largest != nil {
				parts = append(parts, fmt.Sprintf("%s %.0f GiB free", largest.Name, float64(largest.FreeBytes)/(1<<30)))
			}
		}
		lead := ""
		switch {
		case context == "":
		case len(lines) == 0:
			lead = context + " · "
		default:
			lead = strings.Repeat(" ", len(context)+3)
		}
		lines = append(lines, lead+strings.Join(parts, " · "))
	}
	return strings.Join(lines, "\n")
}

// countLine sums up the listed VMs by state and, when some are hidden
// for not being pmox-managed, points at --all.
func countLine(snap *listSnapshot, f *listFlags) string {
	var running, stopped, other int
	for _, r := range snap.rows {
		switch r.Status {
		case "running":
			running++
		case "stopped":
			stopped++
		default:
			other++
		}
	}
	line := fmt.Sprintf("%d running, %d stopped", running, stopped)
	if other > 0 {
		line += fmt.Sprintf(", %d other", other)
	}
	switch {
	case f.all || snap.hidden == 0:
	case snap.hidden == 1:
		line += " · 1 more VM not managed by pmox (--all)"
	default:
		line += fmt.Sprintf(" · %d more VMs not managed by pmox (--all)", snap.hidden)
	}
	return line
}

// fetchIPs asks the guest agent of every running row that has no IP yet
// for its address.
func fetchIPs(ctx context.Context, client *pveclient.Client, rows []vm.Row) {
	sem := make(chan struct{}, listIPConcurrency)
	var wg sync.WaitGroup
	for i := range rows {
		if pveclient.VMState(rows[i].Status) != pveclient.StateRunning || rows[i].IP != "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			ifaces, err := client.AgentNetwork(ctx, rows[i].Node, rows[i].VMID)
			if err != nil {
				return
			}
			rows[i].IP = vmwait.PickGuestIPv4(ifaces, client.PreferOverlay)
			rows[i].Overlay = vmwait.OverlayKind(ifaces, rows[i].IP)
			// Both addresses, whichever one pmox connects to.
			if lan := vmwait.PickGuestIPv4(ifaces, false); vmwait.OverlayKind(ifaces, lan) == "" {
				rows[i].LANIP = lan
			}
			if ov := vmwait.PickGuestIPv4(ifaces, true); vmwait.OverlayKind(ifaces, ov) != "" {
				rows[i].OverlayIP, rows[i].OverlayNet = ov, vmwait.OverlayKind(ifaces, ov)
			}
		}(i)
	}
	wg.Wait()
}

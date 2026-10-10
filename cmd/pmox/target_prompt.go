package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/tui/target"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// The interactive <vm>:<path> and local path fields
// (openspec/specs/remote-target-input) as the commands use them.

// targetRunFn runs the fields. A seam so tests never start a program.
var targetRunFn = func(ctx context.Context, fields ...target.Field) error {
	return target.Run(ctx, target.Options{}, fields...)
}

// targetVMsFn loads the pmox VMs the target field offers. A seam for tests.
var targetVMsFn = targetVMs

// targetVMs lists the pmox VMs with their IPs, like 'pmox list'.
func targetVMs(ctx context.Context, client *pveclient.Client) ([]target.VM, error) {
	sp := startSpin("Loading VMs…")
	defer sp.Stop()
	resources, err := client.ClusterResources(ctx, "vm")
	if err != nil {
		return nil, fmt.Errorf("list cluster resources: %w", err)
	}
	var rows []vm.Row
	for _, r := range resources {
		if r.IsTemplate() || !vm.HasPMOXTag(r.Tags) {
			continue
		}
		rows = append(rows, vm.Row{Name: r.Name, VMID: r.VMID, Node: r.Node, Status: r.Status})
	}
	if len(rows) == 0 {
		return nil, vm.ErrNoPMOXVMs
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	fetchIPs(ctx, client, rows)
	out := make([]target.VM, len(rows))
	for i, r := range rows {
		out[i] = target.VM{Name: r.Name, VMID: r.VMID, Node: r.Node, Status: r.Status, IP: r.IP}
	}
	return out, nil
}

// targetLister completes remote paths with one SSH 'ls' per directory,
// as the login user the command would connect as.
func targetLister(f *sshFlags, serverURL string, srv *config.Server) target.Lister {
	return func(ctx context.Context, v target.VM, dir string) ([]string, error) {
		if v.IP == "" {
			return nil, errors.New("the VM has no IP yet")
		}
		user, key, _, err := resolveGuestIdentity(serverURL, v.VMID, f.user, f.identity, srv)
		if err != nil {
			return nil, err
		}
		t := &sshTarget{IP: v.IP, User: user, Key: key, VMID: v.VMID, Name: v.Name}
		code, out, err := remoteSSHFn(ctx, t, target.ListTimeout, "ls -1pA -- "+remoteShellPath(dir))
		if err != nil {
			return nil, err
		}
		if code != 0 {
			msg := strings.TrimSpace(out)
			if i := strings.IndexByte(msg, '\n'); i >= 0 {
				msg = msg[:i]
			}
			if msg == "" {
				msg = fmt.Sprintf("ls exited %d", code)
			}
			return nil, errors.New(msg)
		}
		var entries []string
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimRight(l, "\r"); l != "" {
				entries = append(entries, l)
			}
		}
		return entries, nil
	}
}

// mountDefaultPath is the remote path suggested for mounting local:
// /mnt/<base name of the absolute local path>.
func mountDefaultPath(local string) string {
	if local == "" {
		local = "."
	}
	if local == "~" || strings.HasPrefix(local, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			local = filepath.Join(home, strings.TrimPrefix(local, "~"))
		}
	}
	abs, err := filepath.Abs(local)
	if err != nil {
		return ""
	}
	base := filepath.Base(abs)
	if base == "/" || base == "." {
		return ""
	}
	return "/mnt/" + base
}

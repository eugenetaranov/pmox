// Package router manages pmox's overlay router: one unprivileged LXC
// container on the node's bridge that runs Tailscale in userspace mode and
// advertises the node's own address as a /32, so the PVE API and node SSH
// are reachable away from home. The container is managed with `pct exec`
// over node SSH (it runs no sshd) and found by its tag; pmox keeps no
// record of it.
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// Tag marks the router container. It never also carries the pmox tag, so
// pmox's VM commands and cleanup leave it alone.
const Tag = "pmox-router"

// API is the part of the PVE client the router uses.
type API interface {
	ClusterResources(ctx context.Context, typeFilter string) ([]pveclient.Resource, error)
	NextID(ctx context.Context) (int, error)
	WaitTask(ctx context.Context, node, upid string, timeout time.Duration) error
	ListStorage(ctx context.Context, node string) ([]pveclient.Storage, error)
	ListStorageContent(ctx context.Context, node, storage, contentFilter string) ([]pveclient.StorageContent, error)
	ListBridges(ctx context.Context, node string) ([]pveclient.Bridge, error)
	AplInfo(ctx context.Context, node string) ([]pveclient.Appliance, error)
	DownloadAppliance(ctx context.Context, node, storage, template string) (string, error)
	CreateLXC(ctx context.Context, node string, vmid int, kv map[string]string) (string, error)
	LXCStart(ctx context.Context, node string, vmid int) (string, error)
	LXCStop(ctx context.Context, node string, vmid int) (string, error)
	DeleteLXC(ctx context.Context, node string, vmid int) (string, error)
	LXCStatus(ctx context.Context, node string, vmid int) (*pveclient.VMStatus, error)
	LXCConfig(ctx context.Context, node string, vmid int) (map[string]string, error)
	SetLXCConfig(ctx context.Context, node string, vmid int, form url.Values) error
	LXCInterfaces(ctx context.Context, node string, vmid int) ([]pveclient.LXCIface, error)
	ClusterFirewallEnabled(ctx context.Context) (bool, error)
	SetLXCFirewallOptions(ctx context.Context, node string, vmid int, form url.Values) error
	AddLXCFirewallRule(ctx context.Context, node string, vmid int, form url.Values) error
	LXCFirewallOptions(ctx context.Context, node string, vmid int) (map[string]string, error)
}

// Runner runs a shell command on the node as root (node SSH), with
// stdin and streamed output; a non-zero exit is an error.
type Runner func(ctx context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) error

// ErrNotFound means the cluster has no router.
var ErrNotFound = errors.New("no pmox router on this cluster (create one with: pmox router create)")

// Find returns the router container, or ErrNotFound. More than one
// container with the tag is an error naming them.
func Find(ctx context.Context, api API) (*pveclient.Resource, error) {
	all, err := api.ClusterResources(ctx, "vm")
	if err != nil {
		return nil, fmt.Errorf("list cluster resources: %w", err)
	}
	var found []pveclient.Resource
	for _, r := range all {
		if r.Type == "lxc" && r.HasTag(Tag) {
			found = append(found, r)
		}
	}
	switch len(found) {
	case 0:
		return nil, ErrNotFound
	case 1:
		return &found[0], nil
	}
	ids := make([]string, len(found))
	for i, r := range found {
		ids[i] = strconv.Itoa(r.VMID)
	}
	return nil, fmt.Errorf("%d containers are tagged %s (%s); remove the tag from all but one", len(found), Tag, strings.Join(ids, ", "))
}

// Hostname is the router's device name on the tailnet.
func Hostname(node string) string { return "pmox-router-" + node }

// PickTemplate chooses the router's OS template from a node's appliance
// index: the newest Ubuntu LTS standard template (ubuntu-YY.04-standard,
// YY even), else the newest Debian standard one. ok is false when the
// index has neither.
func PickTemplate(apps []pveclient.Appliance) (app pveclient.Appliance, ok bool) {
	var ubuntu, debian []pveclient.Appliance
	for _, a := range apps {
		pkg := a.Package
		if pkg == "" {
			pkg, _, _ = strings.Cut(a.Template, "_")
		}
		switch {
		case isUbuntuLTS(pkg):
			ubuntu = append(ubuntu, a)
		case strings.HasPrefix(pkg, "debian-") && strings.HasSuffix(pkg, "-standard"):
			debian = append(debian, a)
		}
	}
	for _, set := range [][]pveclient.Appliance{ubuntu, debian} {
		if len(set) > 0 {
			sort.SliceStable(set, func(i, j int) bool { return versionLess(set[i].Template, set[j].Template) })
			return set[len(set)-1], true
		}
	}
	return pveclient.Appliance{}, false
}

// isUbuntuLTS matches "ubuntu-YY.04-standard" with an even YY.
func isUbuntuLTS(pkg string) bool {
	v, ok := strings.CutPrefix(pkg, "ubuntu-")
	if !ok {
		return false
	}
	v, ok = strings.CutSuffix(v, ".04-standard")
	if !ok {
		return false
	}
	yy, err := strconv.Atoi(v)
	return err == nil && yy%2 == 0
}

// versionLess orders template file names by their numeric parts, so
// 26.04-1 sorts after 24.04-2 and 13.1-2 after 13.1-1.
func versionLess(a, b string) bool {
	na, nb := numbers(a), numbers(b)
	for i := 0; i < len(na) && i < len(nb); i++ {
		if na[i] != nb[i] {
			return na[i] < nb[i]
		}
	}
	return len(na) < len(nb)
}

func numbers(s string) []int {
	var out []int
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		n, _ := strconv.Atoi(f)
		out = append(out, n)
	}
	return out
}

// pctExec is the node command running script in the container, through
// sh with no terminal.
func pctExec(vmid int, script string) string {
	return fmt.Sprintf("pct exec %d -- /bin/sh -c %s", vmid, shQuote(script))
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/accessreg"
	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/guestkeys"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// fakeGuests is an in-memory guest filesystem per VMID, standing in for
// the guest agent. errs injects a failure for a VMID.
type fakeGuests struct {
	mu     sync.Mutex
	files  map[int]map[string]string
	errs   map[int]error
	writes map[int]int
}

func newFakeGuests(vmids ...int) *fakeGuests {
	g := &fakeGuests{files: map[int]map[string]string{}, errs: map[int]error{}, writes: map[int]int{}}
	for _, id := range vmids {
		g.files[id] = map[string]string{
			"/etc/passwd":                       "ubuntu:x:1000:1000::/home/ubuntu:/bin/bash\n",
			"/home/ubuntu/.ssh/authorized_keys": "ssh-ed25519 AAAAlaunch alice@ws1\n",
		}
	}
	return g
}

func (g *fakeGuests) AgentFileRead(_ context.Context, _ string, vmid int, p string) ([]byte, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.errs[vmid]; err != nil {
		return nil, false, err
	}
	c, ok := g.files[vmid][p]
	if !ok {
		return nil, false, errors.New("No such file or directory")
	}
	return []byte(c), false, nil
}

func (g *fakeGuests) AgentFileWrite(_ context.Context, _ string, vmid int, p string, content []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.writes[vmid]++
	g.files[vmid][p] = string(content)
	return nil
}

func (g *fakeGuests) managed(vmid int) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return keyNames(guestkeys.ManagedKeys([]byte(g.files[vmid]["/home/ubuntu/.ssh/authorized_keys"])))
}

// setupAccessEnv extends setupRegistryEnv with a stubbed cluster: web1
// (101, running), db1 (102, running), old (103, stopped), plus an
// untagged VM and a template.
func setupAccessEnv(t *testing.T) (*memRegistry, *fakeGuests) {
	t.Helper()
	reg := setupRegistryEnv(t, testKeyA)
	guests := newFakeGuests(101, 102, 103)
	vms := []pveclient.Resource{
		{VMID: 101, Name: "web1", Node: "p0", Status: "running", Tags: "pmox"},
		{VMID: 102, Name: "db1", Node: "p0", Status: "running", Tags: "pmox"},
		{VMID: 103, Name: "old", Node: "p0", Status: "stopped", Tags: "pmox"},
		{VMID: 200, Name: "truenas", Node: "p0", Status: "running"},
		{VMID: 9000, Name: "tmpl", Node: "p0", Status: "stopped", Tags: "pmox", Template: 1},
	}
	origConnect, origAgent, origVMs := accessConnectFn, guestAgentFn, clusterVMsFn
	accessConnectFn = func(ctx context.Context, _ *cobra.Command) (*config.Config, *server.Resolved, *pveclient.Client, error) {
		cfg, err := config.Load()
		if err != nil {
			return nil, nil, nil, err
		}
		r, err := server.Resolve(ctx, server.Options{Cfg: cfg})
		return cfg, r, nil, err
	}
	guestAgentFn = func(*pveclient.Client) guestkeys.Agent { return guests }
	clusterVMsFn = func(context.Context, *pveclient.Client) ([]pveclient.Resource, error) { return vms, nil }
	t.Cleanup(func() { accessConnectFn, guestAgentFn, clusterVMsFn = origConnect, origAgent, origVMs })
	return reg, guests
}

func publishAs(ctx context.Context, t *testing.T, reg *memRegistry, name, key string) {
	t.Helper()
	k, err := accessreg.NewPublishedKey(name, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := accessreg.PublishKey(ctx, reg, k); err != nil {
		t.Fatal(err)
	}
}

func TestAccessGrantAndRevoke(t *testing.T) {
	reg, guests := setupAccessEnv(t)
	publishAs(t.Context(), t, reg, "bob", testKeyA)

	out, err := runCmd(t, "access", "grant", "web1", "102", "--to", "bob")
	if err != nil {
		t.Fatalf("grant: %v\n%s", err, out)
	}
	for _, id := range []int{101, 102} {
		if got := guests.managed(id); len(got) != 1 || got[0] != "bob" {
			t.Errorf("vm %d managed keys = %v", id, got)
		}
	}
	if !strings.HasPrefix(guests.files[101]["/home/ubuntu/.ssh/authorized_keys"], "ssh-ed25519 AAAAlaunch alice@ws1\n") {
		t.Error("launch key not preserved")
	}
	if !strings.Contains(out, "✓ web1 (101): keys: bob") {
		t.Errorf("output:\n%s", out)
	}
	acc, _ := accessreg.ReadAccess(context.Background(), reg)
	if !acc.Allowed("bob", 101) || !acc.Allowed("bob", 102) || acc.Allowed("bob", 103) {
		t.Errorf("registry = %+v", acc.People["bob"])
	}

	// Granting again changes nothing on the guest.
	out, _ = runCmd(t, "access", "grant", "web1", "--to", "bob")
	if guests.writes[101] != 1 || !strings.Contains(out, "= web1") {
		t.Errorf("regrant rewrote the guest (writes=%d):\n%s", guests.writes[101], out)
	}

	if out, err := runCmd(t, "access", "revoke", "web1", "--to", "bob"); err != nil {
		t.Fatalf("revoke: %v\n%s", err, out)
	}
	if got := guests.managed(101); len(got) != 0 {
		t.Errorf("after revoke, web1 still has %v", got)
	}
	if got := guests.managed(102); len(got) != 1 {
		t.Errorf("revoking web1 touched db1: %v", got)
	}
}

func TestAccessGrantAllVMsAndPending(t *testing.T) {
	reg, guests := setupAccessEnv(t)
	publishAs(t.Context(), t, reg, "carol", testKeyB)
	out, err := runCmd(t, "access", "grant", "--all-vms", "--to", "carol")
	if err != nil {
		t.Fatalf("grant --all-vms: %v\n%s", err, out)
	}
	if !strings.Contains(out, "… old (103): stopped") {
		t.Errorf("stopped VM should be pending:\n%s", out)
	}
	if strings.Contains(out, "truenas") || strings.Contains(out, "tmpl") {
		t.Errorf("untagged VM or template touched:\n%s", out)
	}
	if got := guests.managed(101); len(got) != 1 || got[0] != "carol" {
		t.Errorf("web1 = %v", got)
	}
	acc, _ := accessreg.ReadAccess(context.Background(), reg)
	if !acc.Allowed("carol", 4242) {
		t.Error("all-VMs grant should cover future VMs")
	}
}

func TestAccessGrantRequiresPublishedKey(t *testing.T) {
	setupAccessEnv(t)
	_, err := runCmd(t, "access", "grant", "web1", "--to", "dave")
	if err == nil || !strings.Contains(err.Error(), "pmox key publish") {
		t.Fatalf("err = %v, want a publish hint", err)
	}
}

func TestAccessRefusesUntaggedVMWithoutForce(t *testing.T) {
	reg, _ := setupAccessEnv(t)
	publishAs(t.Context(), t, reg, "bob", testKeyA)
	if _, err := runCmd(t, "access", "grant", "truenas", "--to", "bob"); err == nil || !strings.Contains(err.Error(), "not tagged") {
		t.Fatalf("err = %v, want the pmox-tag refusal", err)
	}
}

func TestAccessSyncReportsFailuresAndForbidden(t *testing.T) {
	reg, guests := setupAccessEnv(t)
	publishAs(t.Context(), t, reg, "bob", testKeyA)
	if _, err := runCmd(t, "access", "grant", "--all-vms", "--to", "bob"); err != nil {
		t.Fatal(err)
	}
	guests.errs[102] = fmt.Errorf("%w: 403", pveclient.ErrForbidden)
	guests.errs[101] = fmt.Errorf("%w: 500", pveclient.ErrAgentNotRunning)
	out, err := runCmd(t, "access", "sync")
	if !errors.Is(err, errAccessSync) {
		t.Fatalf("err = %v, want errAccessSync\n%s", err, out)
	}
	if !strings.Contains(out, "VM.GuestAgent.FileWrite") || !strings.Contains(out, "guest agent not responding") {
		t.Errorf("output:\n%s", out)
	}
}

func TestAccessListShowsDrift(t *testing.T) {
	reg, guests := setupAccessEnv(t)
	publishAs(t.Context(), t, reg, "bob", testKeyA)
	publishAs(t.Context(), t, reg, "carol", testKeyB)
	if _, err := runCmd(t, "access", "grant", "web1", "--to", "bob"); err != nil {
		t.Fatal(err)
	}
	// Someone grants carol on db1 in the registry only (no sync yet).
	_, _ = accessreg.UpdateAccess(context.Background(), reg, func(a *accessreg.Access) error { a.GrantVMs("carol", 102); return nil })
	out, err := runCmd(t, "access", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bob", "carol", "web1", "in sync", "OUT OF SYNC", "pmox access sync db1"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
	_ = guests
}

func withNodeSSH(t *testing.T) {
	t.Helper()
	cfg, _ := config.Load()
	cfg.Servers[formURL].NodeSSH = &config.NodeSSH{User: "root", Auth: config.AuthPassword}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if err := credstore.SetNodeSSHPassword(formURL, "pw"); err != nil {
		t.Fatal(err)
	}
}

func TestApplySharedAccessAfterLaunch(t *testing.T) {
	ctx := context.Background()
	t.Run("all-VM grantee gets the new VM", func(t *testing.T) {
		reg, guests := setupAccessEnv(t)
		withNodeSSH(t)
		publishAs(ctx, t, reg, "carol", testKeyB)
		_, _ = accessreg.UpdateAccess(ctx, reg, func(a *accessreg.Access) error { a.GrantAll("carol"); return nil })
		var stderr strings.Builder
		applySharedAccess(ctx, &stderr, nil, formURL, 101, "p0", "web1")
		if got := guests.managed(101); len(got) != 1 || got[0] != "carol" {
			t.Errorf("managed = %v; stderr=%q", got, stderr.String())
		}
		if !strings.Contains(stderr.String(), "shared access: keys: carol") {
			t.Errorf("stderr = %q", stderr.String())
		}
	})
	t.Run("failure only warns", func(t *testing.T) {
		reg, guests := setupAccessEnv(t)
		withNodeSSH(t)
		publishAs(ctx, t, reg, "carol", testKeyB)
		_, _ = accessreg.UpdateAccess(ctx, reg, func(a *accessreg.Access) error { a.GrantAll("carol"); return nil })
		guests.errs[101] = fmt.Errorf("%w: 500", pveclient.ErrAgentNotRunning)
		var stderr strings.Builder
		applySharedAccess(ctx, &stderr, nil, formURL, 101, "p0", "web1")
		if !strings.Contains(stderr.String(), "warning: could not apply shared access to web1") || !strings.Contains(stderr.String(), "pmox access sync web1") {
			t.Errorf("stderr = %q", stderr.String())
		}
	})
	t.Run("empty registry is silent", func(t *testing.T) {
		reg, _ := setupAccessEnv(t)
		withNodeSSH(t)
		var stderr strings.Builder
		applySharedAccess(ctx, &stderr, nil, formURL, 101, "p0", "web1")
		if stderr.Len() != 0 || reg.opens != 1 {
			t.Errorf("stderr = %q opens=%d", stderr.String(), reg.opens)
		}
	})
	t.Run("no node SSH is silent and never dials", func(t *testing.T) {
		reg, _ := setupAccessEnv(t)
		var stderr strings.Builder
		applySharedAccess(ctx, &stderr, nil, formURL, 101, "p0", "web1")
		if stderr.Len() != 0 || reg.opens != 0 {
			t.Errorf("stderr = %q opens=%d", stderr.String(), reg.opens)
		}
	})
}

func TestCleanupAccessRegistryItems(t *testing.T) {
	ctx := context.Background()
	reg, _ := setupAccessEnv(t)
	withNodeSSH(t)
	publishAs(ctx, t, reg, "bob", testKeyA)
	publishAs(ctx, t, reg, "carol", testKeyB)
	_, _ = accessreg.UpdateAccess(ctx, reg, func(a *accessreg.Access) error { a.GrantVMs("bob", 101, 999); return nil })

	cfg, _ := config.Load()
	items := accessRegistryItems(ctx, cfg, formURL, "pve", map[int]bool{101: true})
	var grants, keys []cleanupItem
	for _, it := range items {
		switch it.Category {
		case "access-grant":
			grants = append(grants, it)
		case "access-key":
			keys = append(keys, it)
		}
	}
	if len(grants) != 1 || !strings.Contains(grants[0].Detail, "bob's grant for vmid 999") {
		t.Fatalf("access-grant items = %+v", grants)
	}
	if len(keys) != 1 || !strings.Contains(keys[0].Detail, "carol") {
		t.Fatalf("access-key items = %+v (bob has a live grant and must not be listed)", keys)
	}
	for _, it := range items {
		if err := it.apply(); err != nil {
			t.Fatal(err)
		}
	}
	acc, _ := accessreg.ReadAccess(ctx, reg)
	if acc.Allowed("bob", 999) || !acc.Allowed("bob", 101) {
		t.Errorf("after cleanup bob = %+v", acc.People["bob"])
	}
	if _, err := accessreg.GetKey(ctx, reg, "carol"); !errors.Is(err, accessreg.ErrNotPublished) {
		t.Errorf("carol's key should be unpublished: %v", err)
	}
}

func TestKeyListShowsEveryone(t *testing.T) {
	ctx := context.Background()
	reg, _ := setupAccessEnv(t)
	publishAs(ctx, t, reg, "bob", testKeyA)
	publishAs(ctx, t, reg, "carol", testKeyB)
	_, _ = accessreg.UpdateAccess(ctx, reg, func(a *accessreg.Access) error { a.GrantAll("carol"); a.GrantVMs("bob", 101); return nil })
	out, err := runCmd(t, "key", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bob", "carol", "SHA256:", "all pmox VMs", "vmid 101"} {
		if !strings.Contains(out, want) {
			t.Errorf("key list missing %q:\n%s", want, out)
		}
	}
}

func TestAccessShowAndListAlias(t *testing.T) {
	setupAccessEnv(t)
	for _, verb := range []string{"show", "list"} {
		if out, err := runCmd(t, "access", verb); err != nil || !strings.Contains(out, "People:") {
			t.Errorf("access %s: %v\n%s", verb, err, out)
		}
	}
}

func TestAccessGrantPromptsWhenBare(t *testing.T) {
	ctx := context.Background()
	reg, guests := setupAccessEnv(t)
	publishAs(ctx, t, reg, "bob", testKeyA)
	origP, origV := pickPersonFn, pickVMsFn
	t.Cleanup(func() { pickPersonFn, pickVMsFn = origP, origV })
	forceInteractive(t)
	pickPersonFn = func(string, []huh.Option[string]) (string, error) { return "bob", nil }
	pickVMsFn = func(string, []huh.Option[string]) ([]string, error) { return []string{"101"}, nil }

	if out, err := runCmd(t, "access", "grant"); err != nil {
		t.Fatalf("bare grant on a terminal: %v\n%s", err, out)
	}
	if got := guests.managed(101); len(got) != 1 || got[0] != "bob" {
		t.Errorf("web1 managed = %v", got)
	}
	// Revoke offers only what bob can reach.
	var offered []string
	pickVMsFn = func(_ string, opts []huh.Option[string]) ([]string, error) {
		for _, o := range opts {
			offered = append(offered, o.Value)
		}
		return []string{"101"}, nil
	}
	if _, err := runCmd(t, "access", "revoke"); err != nil {
		t.Fatal(err)
	}
	if len(offered) != 1 || offered[0] != "101" {
		t.Errorf("revoke offered %v, want only bob's VM", offered)
	}
	if got := guests.managed(101); len(got) != 0 {
		t.Errorf("after revoke web1 = %v", got)
	}
}

// forceInteractive makes tui.Interactive report a terminal for the test.
func forceInteractive(t *testing.T) {
	t.Helper()
	in, errT := tui.StdinIsTerminal, tui.StderrIsTerminal
	tui.StdinIsTerminal = func() bool { return true }
	tui.StderrIsTerminal = func() bool { return true }
	t.Cleanup(func() { tui.StdinIsTerminal, tui.StderrIsTerminal = in, errT })
}

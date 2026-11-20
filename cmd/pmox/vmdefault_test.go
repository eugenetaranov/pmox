package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvetest"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// withDefaultVM sets the session's default VM for one test, records
// what gets saved, and stubs the picker (it must not run unless the
// test says so).
func withDefaultVM(t *testing.T, name string) (saved *[]string, picked *int) {
	t.Helper()
	origSave, origPick := saveDefaultVMFn, vmPickFn
	saved, picked = &[]string{}, new(int)
	sessionDefault.url, sessionDefault.name, vm.DefaultName, pickFlag = formURL, name, name, false
	saveDefaultVMFn = func(_, n string) error { *saved = append(*saved, n); return nil }
	vmPickFn = func(context.Context, *pveclient.Client) (*vm.Ref, error) {
		*picked++
		return &vm.Ref{VMID: 105, Name: "db1"}, nil
	}
	t.Cleanup(func() {
		saveDefaultVMFn, vmPickFn = origSave, origPick
		sessionDefault.url, sessionDefault.name, vm.DefaultName, pickFlag = "", "", "", false
	})
	return saved, picked
}

func TestPickTargetVM(t *testing.T) {
	f := pvetest.New(t)
	f.Handle("GET", "/cluster/resources", pvetest.JSON(threeVMsListFixture))
	ctx := context.Background()

	t.Run("the default is used without a picker", func(t *testing.T) {
		saved, picked := withDefaultVM(t, "web1")
		var errb bytes.Buffer
		ref, err := pickTargetVM(ctx, f.Client(), &errb)
		if err != nil || ref.VMID != 104 {
			t.Fatalf("ref = %+v, err = %v, want web1 (104)", ref, err)
		}
		if *picked != 0 || len(*saved) != 0 {
			t.Errorf("picker ran %d times, saved %v", *picked, *saved)
		}
		if !strings.Contains(errb.String(), "→ web1 (default VM)") {
			t.Errorf("stderr = %q, want it to name the chosen VM", errb.String())
		}
	})

	t.Run("an explicit argument wins", func(t *testing.T) {
		_, picked := withDefaultVM(t, "web1")
		var errb bytes.Buffer
		arg, err := resolveDefaultTargetArg(ctx, f.Client(), []string{"db1"}, &errb)
		if err != nil || arg != "db1" || *picked != 0 || errb.Len() != 0 {
			t.Errorf("arg = %q, err = %v, picked = %d, stderr = %q", arg, err, *picked, errb.String())
		}
	})

	t.Run("--pick ignores the default", func(t *testing.T) {
		saved, picked := withDefaultVM(t, "web1")
		pickFlag = true
		arg, err := resolveDefaultTargetArg(ctx, f.Client(), nil, &bytes.Buffer{})
		if err != nil || arg != "105" || *picked != 1 {
			t.Errorf("arg = %q, err = %v, picked = %d", arg, err, *picked)
		}
		if len(*saved) != 0 || sessionDefault.name != "web1" {
			t.Errorf("--pick must leave the default alone: saved %v, default %q", *saved, sessionDefault.name)
		}
	})

	t.Run("a default that is gone is reported and cleared", func(t *testing.T) {
		saved, picked := withDefaultVM(t, "gone")
		var errb bytes.Buffer
		ref, err := pickTargetVM(ctx, f.Client(), &errb)
		if err != nil || ref.Name != "db1" || *picked != 1 {
			t.Fatalf("ref = %+v, err = %v, picked = %d", ref, err, *picked)
		}
		if len(*saved) != 1 || (*saved)[0] != "" || sessionDefault.name != "" {
			t.Errorf("saved = %v, default = %q, want it cleared", *saved, sessionDefault.name)
		}
		if !strings.Contains(errb.String(), `default VM "gone" no longer exists`) {
			t.Errorf("stderr = %q", errb.String())
		}
	})

	t.Run("a default without the pmox tag counts as gone", func(t *testing.T) {
		saved, _ := withDefaultVM(t, "legacy")
		if _, err := pickTargetVM(ctx, f.Client(), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if len(*saved) != 1 {
			t.Errorf("saved = %v, want the default cleared", *saved)
		}
	})

	t.Run("an unreachable cluster clears nothing", func(t *testing.T) {
		down := pvetest.New(t)
		down.Handle("GET", "/cluster/resources", func(w http.ResponseWriter, _ *http.Request, _ string) {
			http.Error(w, "boom", http.StatusInternalServerError)
		})
		saved, picked := withDefaultVM(t, "web1")
		_, err := pickTargetVM(ctx, down.Client(), &bytes.Buffer{})
		if err == nil || errors.Is(err, pveclient.ErrNotFound) {
			t.Fatalf("err = %v, want the API error", err)
		}
		if len(*saved) != 0 || *picked != 0 || sessionDefault.name != "web1" {
			t.Errorf("saved %v, picked %d, default %q", *saved, *picked, sessionDefault.name)
		}
	})

	t.Run("no default: the usual rules", func(t *testing.T) {
		_, picked := withDefaultVM(t, "")
		var errb bytes.Buffer
		if _, err := pickTargetVM(ctx, f.Client(), &errb); err != nil || *picked != 1 || errb.Len() != 0 {
			t.Errorf("err = %v, picked = %d, stderr = %q", err, *picked, errb.String())
		}
	})
}

// Exactly the everyday commands take --pick; the ones that change or
// remove a VM must never grow it (and so can never use the default).
func TestPickFlagIsOnlyOnCommandsThatUseTheDefault(t *testing.T) {
	want := map[string]bool{
		"shell": true, "exec": true, "vm info": true, "start": true, "apply": true,
		"vm ssh-config": true, "yardo push": true, "yardo gitkey": true,
		"cp": true, "sync": true, "mount": true, "mount create": true,
		"vm shell": true, "vm exec": true, "vm start": true, "vm apply": true, "vm cp": true, "vm sync": true,
		"ssh-config": true, // the deprecated top-level form of vm ssh-config
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		path := strings.TrimPrefix(c.CommandPath(), "pmox ")
		if has := c.Flags().Lookup("pick") != nil; has != want[path] {
			t.Errorf("%q: --pick present = %v, want %v", path, has, want[path])
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	for _, c := range rootCmd.Commands() {
		walk(c)
	}
	for _, never := range []string{"stop", "delete", "vm resize", "vm clone", "access grant", "info"} {
		if findPath(t, never).Flags().Lookup("pick") != nil {
			t.Errorf("%q must not take --pick", never)
		}
	}
}

func TestVMUse(t *testing.T) {
	isolate(t)
	cfg := &config.Config{Servers: map[string]*config.Server{formURL: {TokenID: "root@pam!pmox", Name: "lab"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	reload := func() string {
		c, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		return c.Servers[formURL].DefaultVM
	}

	if err := config.SetDefaultVM(formURL, "web1"); err != nil || reload() != "web1" {
		t.Fatalf("set: err %v, default %q", err, reload())
	}
	if c, _ := config.Load(); c.Servers[formURL].TokenID != "root@pam!pmox" || c.Servers[formURL].Name != "lab" {
		t.Error("setting the default must leave the rest of the server entry alone")
	}
	if err := config.SetDefaultVM(formURL, ""); err != nil || reload() != "" {
		t.Fatalf("clear: err %v, default %q", err, reload())
	}
	if err := config.SetDefaultVM(formURL, ""); err != nil {
		t.Errorf("clearing with none set should succeed: %v", err)
	}
	if err := config.SetDefaultVM("https://other:8006/api2/json", "x"); err == nil {
		t.Error("an unconfigured server should be an error")
	}

	c := findPath(t, "vm use")
	if c.Flags().Lookup("clear") == nil || !strings.Contains(c.Short, "default VM") {
		t.Errorf("vm use: flags or summary wrong: %q", c.Short)
	}
	if got, _, _ := rootCmd.Find([]string{"use"}); got != rootCmd {
		t.Error("'pmox use' must not exist as a root shortcut")
	}
}

func TestDeleteClearsTheDefault(t *testing.T) {
	saved, _ := withDefaultVM(t, "web1")
	cmd, _, errb := newTestInfoCmd()
	forgetDefaultVM(cmd, &vm.Ref{VMID: 105, Name: "db1"})
	if len(*saved) != 0 {
		t.Fatalf("deleting another VM cleared the default: %v", *saved)
	}
	forgetDefaultVM(cmd, &vm.Ref{VMID: 104, Name: "web1"})
	if len(*saved) != 1 || (*saved)[0] != "" || !strings.Contains(errb.String(), "was the default VM") {
		t.Errorf("saved = %v, stderr = %q", *saved, errb.String())
	}
}

func TestListMarksTheDefault(t *testing.T) {
	var buf bytes.Buffer
	rows := []vm.Row{
		{Name: "web1", VMID: 104, Node: "pve1", Status: "running", IP: "10.0.0.1", Default: true},
		{Name: "db1", VMID: 105, Node: "pve1", Status: "stopped"},
	}
	vm.RenderTable(&buf, rows)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if !strings.HasPrefix(lines[0], "  NAME") || !strings.HasPrefix(lines[1], "* web1") || !strings.HasPrefix(lines[2], "  db1") {
		t.Errorf("table:\n%s", buf.String())
	}
	buf.Reset()
	rows[0].Default = false
	vm.RenderTable(&buf, rows)
	if !strings.HasPrefix(buf.String(), "NAME") {
		t.Errorf("no default: the table should have no marker column:\n%s", buf.String())
	}
}

func TestBareColonTargetUsesTheDefault(t *testing.T) {
	f := pvetest.New(t)
	f.Handle("GET", "/cluster/resources", pvetest.JSON(threeVMsListFixture))
	_, picked := withDefaultVM(t, "web1")
	cmd, _, _ := newTestInfoCmd()

	ref, err := ensureVMRef(cmd.Context(), cmd, f.Client(), "")
	if err != nil || ref != "104" || *picked != 0 {
		t.Errorf("cp ':' → %q, err %v, picked %d; want the default (104)", ref, err, *picked)
	}
	name, path, err := mountResolveDestFn(cmd.Context(), f.Client(), cmd.ErrOrStderr(), "/opt/app")
	if err != nil || name != "web1" || path != "/opt/app" {
		t.Errorf("mount bare path → %q:%q, err %v", name, path, err)
	}
	if got := targetDefaultVM(); got != "web1" {
		t.Errorf("targetDefaultVM = %q", got)
	}
	pickFlag = true
	if got := targetDefaultVM(); got != "" {
		t.Errorf("targetDefaultVM with --pick = %q", got)
	}
}

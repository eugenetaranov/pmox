package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/mount"
	"github.com/eugenetaranov/pmox/internal/pvetest"
)

const completionFixture = `{"data":[
  {"vmid":104,"name":"web1","node":"pve1","status":"running","tags":"pmox"},
  {"vmid":106,"name":"web2","node":"pve1","status":"running","tags":"pmox"},
  {"vmid":105,"name":"db1","node":"pve1","status":"stopped","tags":"pmox"},
  {"vmid":200,"name":"legacy","node":"pve2","status":"running","tags":""},
  {"vmid":9000,"name":"ubuntu-2404-pmox-9000","node":"pve1","status":"stopped","template":1}
]}`

// completionFake points completion at a fake cluster with an empty cache.
func completionFake(t *testing.T) *pvetest.Server {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f := pvetest.New(t)
	f.Handle("GET", "/cluster/resources", pvetest.JSON(completionFixture))
	orig := completionConnectFn
	completionConnectFn = func(context.Context) *completionSession {
		return &completionSession{client: f.Client(), url: formURL, srv: &config.Server{Node: "pve1"}}
	}
	t.Cleanup(func() { completionConnectFn = orig })
	return f
}

// complete runs path's argument completion.
func complete(t *testing.T, path string, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	t.Helper()
	c := findPath(t, path)
	if c.ValidArgsFunction == nil {
		t.Fatalf("%q has no argument completion", path)
	}
	c.SetContext(context.Background())
	got, dir := c.ValidArgsFunction(c, args, toComplete)
	names := make([]string, len(got))
	for i, g := range got {
		names[i], _, _ = strings.Cut(g, "\t")
	}
	return names, dir
}

func TestCompleteVMNames(t *testing.T) {
	completionFake(t)
	cases := []struct {
		path string
		args []string
		typ  string
		want string
	}{
		{"shell", nil, "", "web1 web2 db1"},
		{"vm shell", nil, "", "web1 web2 db1"},
		{"shell", []string{"web1"}, "", ""},
		{"stop", []string{"web1"}, "", "web2"}, // running only, minus the one typed
		{"start", nil, "", "db1"},              // stopped only
		{"delete", []string{"db1"}, "", "web1 web2"},
		{"restart", nil, "", "web1 web2 db1"},
		{"vm use", nil, "", "web1 web2 db1"},
		{"shell", nil, "10", "104 106 105"}, // digits: VMIDs
		{"vm resize cpu", nil, "", "web1 web2 db1"},
		{"access grant", []string{"web1"}, "", "web2 db1"},
	}
	for _, tc := range cases {
		got, dir := complete(t, tc.path, tc.args, tc.typ)
		if strings.Join(got, " ") != tc.want {
			t.Errorf("%s %v %q → %v, want %q", tc.path, tc.args, tc.typ, got, tc.want)
		}
		if dir&cobra.ShellCompDirectiveNoFileComp == 0 {
			t.Errorf("%s: VM arguments shouldn't fall back to file names", tc.path)
		}
	}
	// exec: the VM first, then the command (the shell's own completion).
	if _, dir := complete(t, "exec", []string{"web1"}, ""); dir != cobra.ShellCompDirectiveDefault {
		t.Errorf("exec after the VM: directive %v, want default", dir)
	}
}

func TestCompleteVMTargets(t *testing.T) {
	completionFake(t)
	got, dir := complete(t, "cp", []string{"./app.tar"}, "w")
	if strings.Join(got, " ") != "web1: web2: db1:" || dir&cobra.ShellCompDirectiveNoSpace == 0 {
		t.Errorf("cp target → %v (directive %v), want names with ':' and no space", got, dir)
	}
	if got, _ := complete(t, "cp", []string{"./a"}, "web1:/t"); len(got) != 0 {
		t.Errorf("a remote path isn't completed: %v", got)
	}
	// mount: the first argument is a local directory.
	if got, dir := complete(t, "mount create", nil, ""); len(got) != 0 || dir != cobra.ShellCompDirectiveFilterDirs {
		t.Errorf("mount's first argument → %v (directive %v), want directories", got, dir)
	}
	if got, _ := complete(t, "mount create", []string{"./src"}, ""); strings.Join(got, " ") != "web1: web2: db1:" {
		t.Errorf("mount target → %v", got)
	}

	dir2, err := mount.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mount.Save(dir2, mount.Record{VMName: "web1", LocalPath: "/src/app", RemotePath: "/opt/app", PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"umount", "mount delete", "mount restart"} {
		if got, _ := complete(t, path, nil, ""); strings.Join(got, " ") != "web1:/opt/app" {
			t.Errorf("%s → %v, want the running mount", path, got)
		}
	}
}

func TestCompleteContextsTemplatesAndPlacement(t *testing.T) {
	isolate(t)
	f := completionFake(t)
	f.HandleExact("GET", "/nodes", pvetest.JSON(`{"data":[{"node":"pve1","status":"online"},{"node":"pve2","status":"online"}]}`))
	f.Handle("GET", "/nodes/pve1/storage", pvetest.JSON(`{"data":[{"storage":"local-lvm","type":"lvmthin"},{"storage":"local","type":"dir"}]}`))
	f.Handle("GET", "/nodes/pve1/network", pvetest.JSON(`{"data":[{"iface":"vmbr0","type":"bridge"}]}`))
	cfg := &config.Config{Servers: map[string]*config.Server{
		formURL:                           {TokenID: "x", Name: "lab"},
		"https://prod.lan:8006/api2/json": {TokenID: "x", Name: "prod"},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	if got, _ := complete(t, "context use", nil, ""); strings.Join(got, " ") != "prod lab" {
		t.Errorf("context use → %v", got)
	}
	if got, _ := complete(t, "context rename", []string{"lab"}, ""); len(got) != 0 {
		t.Errorf("context rename's new name isn't completed: %v", got)
	}

	launch := findPath(t, "launch")
	launch.SetContext(context.Background())
	flag := func(name string) string {
		fn, ok := launch.GetFlagCompletionFunc(name)
		if !ok {
			t.Fatalf("launch --%s has no completion", name)
		}
		got, _ := fn(launch, nil, "")
		for i, g := range got {
			got[i], _, _ = strings.Cut(g, "\t")
		}
		return strings.Join(got, " ")
	}
	if got := flag("template"); got != "ubuntu-2404-pmox-9000" {
		t.Errorf("--template → %q", got)
	}
	if got := flag("node"); got != "pve1 pve2" {
		t.Errorf("--node → %q", got)
	}
	if got := flag("storage"); got != "local local-lvm" {
		t.Errorf("--storage → %q", got)
	}
	if got := flag("bridge"); got != "vmbr0" {
		t.Errorf("--bridge → %q", got)
	}
	if fn, ok := rootCmd.GetFlagCompletionFunc("context"); !ok {
		t.Error("--context has no completion")
	} else if got, _ := fn(rootCmd, nil, ""); len(got) != 2 {
		t.Errorf("--context → %v", got)
	}
}

func TestCompletionIsCachedAndInvalidated(t *testing.T) {
	f := completionFake(t)
	for i := 0; i < 3; i++ {
		if got, _ := complete(t, "shell", nil, ""); len(got) != 3 {
			t.Fatalf("completion %d → %v", i, got)
		}
	}
	if n := f.Count("GET", "/cluster/resources"); n != 1 {
		t.Errorf("3 completions made %d API calls, want 1 (cached)", n)
	}
	invalidateCompletionCache()
	complete(t, "shell", nil, "")
	if n := f.Count("GET", "/cluster/resources"); n != 2 {
		t.Errorf("after invalidation: %d API calls, want 2", n)
	}
	for _, name := range []string{"launch", "delete", "stop", "start", "restart", "clone"} {
		found := false
		for _, m := range completionMutators {
			found = found || m == name
		}
		if !found {
			t.Errorf("%s should invalidate the completion cache", name)
		}
	}
}

func TestCompletionIsSilentWhenTheClusterIsNot(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	down := pvetest.New(t)
	down.Handle("GET", "/cluster/resources", func(w http.ResponseWriter, _ *http.Request, _ string) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	orig := completionConnectFn
	t.Cleanup(func() { completionConnectFn = orig })

	completionConnectFn = func(context.Context) *completionSession {
		return &completionSession{client: down.Client(), url: formURL}
	}
	if got, dir := complete(t, "shell", nil, ""); len(got) != 0 || dir&cobra.ShellCompDirectiveError != 0 {
		t.Errorf("API error → %v (directive %v), want nothing and no error", got, dir)
	}

	// Not configured: nothing resolves, nothing is offered.
	completionConnectFn = func(context.Context) *completionSession { return nil }
	if got, _ := complete(t, "shell", nil, ""); len(got) != 0 {
		t.Errorf("unconfigured → %v", got)
	}

	// The real connect, with no config at all, returns nil quietly.
	isolate(t)
	if s := completionConnect(context.Background()); s != nil {
		t.Errorf("completionConnect without config = %+v, want nil", s)
	}
}

// Every command that takes a VM completes it; deprecated forms complete
// nothing.
func TestEveryVMCommandCompletes(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if strings.Contains(c.Use, "name|vmid") && !c.Hidden && c.ValidArgsFunction == nil {
			t.Errorf("%q takes a VM but has no completion", c.CommandPath())
		}
		if c.Hidden && c.ValidArgsFunction != nil {
			t.Errorf("deprecated %q should not complete", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)

	// The generated script never offers hidden commands.
	stdout, _, err := execRoot(t, "__complete", "create-")
	if err != nil || strings.Contains(stdout, "create-template") {
		t.Errorf("__complete create- → %q (err %v), want no deprecated command", stdout, err)
	}
}

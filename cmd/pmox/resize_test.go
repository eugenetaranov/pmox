package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvetest"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/vm"
)

func newResizeFake(t *testing.T, status, cfg string) *pvetest.Server {
	t.Helper()
	f := pvetest.New(t)
	f.Handle("GET", "/cluster/resources", pvetest.JSON(strings.Replace(oneVMResources, `"running"`, `"`+status+`"`, 1)))
	f.Handle("GET", "/config", pvetest.JSON(`{"data":`+cfg+`}`))
	f.Handle("POST", "/config", pvetest.JSON(`{"data":null}`))
	f.Handle("PUT", "/resize", pvetest.JSON(`{"data":null}`))
	f.Handle("GET", "/status/current", pvetest.JSON(`{"data":{"status":"`+status+`"}}`))
	return f
}

func TestParseGiB(t *testing.T) {
	for in, want := range map[string]int{"8": 8192, "1.5": 1536, "4G": 4096, "2GB": 2048} {
		if got, err := parseGiB(in); err != nil || got != want {
			t.Errorf("parseGiB(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"0", "0.25", "-1", "lots", ""} {
		if _, err := parseGiB(in); err == nil {
			t.Errorf("parseGiB(%q): want an error", in)
		}
	}
}

func TestDiskGiB(t *testing.T) {
	for spec, want := range map[string]float64{"local-lvm:vm-104-disk-0,size=32G": 32, "local:104/x.qcow2,iothread=1,size=1T": 1024, "x,size=512M": 0.5} {
		if got, ok := diskGiB(spec); !ok || got != want {
			t.Errorf("diskGiB(%q) = %v, %v; want %v", spec, got, ok, want)
		}
	}
}

func TestResizeRAMStoppedVM(t *testing.T) {
	f := newResizeFake(t, "stopped", `{"memory":"4096","cores":"2","balloon":"8192"}`)
	cmd, out, _ := newTestInfoCmd()
	if err := executeResizeSetting(cmd.Context(), cmd, f.Client(), "web1", "memory", 6144, &resizeFlags{}); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(f.Bodies("POST", "/config"), "&")
	if !strings.Contains(body, "memory=6144") || !strings.Contains(body, "balloon=6144") {
		t.Errorf("config body = %q", body)
	}
	if !strings.Contains(out.String(), "web1: memory 4 GiB → 6 GiB") {
		t.Errorf("out = %q", out.String())
	}
}

func TestResizeCPURunningVM(t *testing.T) {
	restarted := 0
	orig := resizeRestartFn
	resizeRestartFn = func(context.Context, *cobra.Command, *pveclient.Client, string) error { restarted++; return nil }
	t.Cleanup(func() { resizeRestartFn = orig })

	f := newResizeFake(t, "running", `{"memory":"4096","cores":"2"}`)
	cmd, out, errb := newTestInfoCmd()
	// Not on a terminal and no flag: left for later, with how.
	if err := executeResizeSetting(cmd.Context(), cmd, f.Client(), "web1", "cores", 4, &resizeFlags{}); err != nil {
		t.Fatal(err)
	}
	if restarted != 0 || !strings.Contains(errb.String(), "pmox restart web1") || !strings.Contains(out.String(), "cores 2 → 4") {
		t.Errorf("restarted %d; out %q; err %q", restarted, out.String(), errb.String())
	}
	if err := executeResizeSetting(cmd.Context(), cmd, f.Client(), "web1", "cores", 4, &resizeFlags{restart: true}); err != nil {
		t.Fatal(err)
	}
	if restarted != 1 {
		t.Errorf("--restart: restarted %d", restarted)
	}
	// The same value again changes nothing.
	f2 := newResizeFake(t, "running", `{"memory":"4096","cores":"4"}`)
	out.Reset()
	if err := executeResizeSetting(cmd.Context(), cmd, f2.Client(), "web1", "cores", 4, &resizeFlags{restart: true}); err != nil {
		t.Fatal(err)
	}
	if f2.Count("POST", "/config") != 0 || !strings.Contains(out.String(), "already has cores 4") {
		t.Errorf("same value: posts %d, out %q", f2.Count("POST", "/config"), out.String())
	}
}

func TestResizeDisk(t *testing.T) {
	res := &server.Resolved{URL: "https://pve:8006/api2/json", Server: &config.Server{}}
	f := newResizeFake(t, "stopped", `{"scsi0":"local-lvm:vm-104-disk-0,size=32G"}`)
	cmd, out, errb := newTestInfoCmd()
	if err := executeResizeDisk(cmd.Context(), cmd, f.Client(), res, "web1", "+20", &resizeFlags{}); err != nil {
		t.Fatal(err)
	}
	if body := strings.Join(f.Bodies("PUT", "/resize"), "&"); !strings.Contains(body, "disk=scsi0") || !strings.Contains(body, "size=%2B20G") {
		t.Errorf("resize body = %q", body)
	}
	if !strings.Contains(out.String(), "disk 32 → 52 GiB") || !strings.Contains(errb.String(), "grows when web1 next boots") {
		t.Errorf("out %q err %q", out.String(), errb.String())
	}
	// Absolute size: smaller is refused, equal is a no-op.
	if err := executeResizeDisk(cmd.Context(), cmd, f.Client(), res, "web1", "20", &resizeFlags{}); err == nil || !strings.Contains(err.Error(), "can't shrink") {
		t.Errorf("shrink: err = %v", err)
	}
	n := f.Count("PUT", "/resize")
	if err := executeResizeDisk(cmd.Context(), cmd, f.Client(), res, "web1", "32", &resizeFlags{}); err != nil || f.Count("PUT", "/resize") != n {
		t.Errorf("same size: %v, resizes %d", err, f.Count("PUT", "/resize"))
	}
	if err := executeResizeDisk(cmd.Context(), cmd, f.Client(), res, "web1", "+abc", &resizeFlags{}); err == nil {
		t.Error("want an error for +abc")
	}
}

func TestResizeTree(t *testing.T) {
	// The flag form, under vm and as a root shortcut.
	for _, path := range []string{"vm resize", "resize"} {
		c := findPath(t, path)
		for _, flag := range []string{"cpu", "mem", "disk", "restart", "no-restart", "force"} {
			if c.Flags().Lookup(flag) == nil {
				t.Errorf("%s lacks --%s", path, flag)
			}
		}
		if c.Flags().Lookup("pick") != nil {
			t.Errorf("%s must not use the default VM", path)
		}
	}
	// The old subcommands still resolve, hidden, pointing at the flag form.
	for path, want := range map[string]string{"vm resize ram": "--mem", "vm resize mem": "--mem", "vm resize cpu": "--cpu", "vm resize disk": "--disk"} {
		c, _, err := rootCmd.Find(strings.Fields(path))
		if err != nil || !c.Hidden || !strings.Contains(c.Annotations[deprecatedAnnotation], want) {
			t.Errorf("%s: hidden %v, replacement %q (%v)", path, c.Hidden, c.Annotations[deprecatedAnnotation], err)
		}
	}
}

// 'pmox resize web1 --cpu 4 --mem 8 --disk +10': every value applies and
// a running VM restarts once.
func TestResizeFlagForm(t *testing.T) {
	restarted := 0
	orig := resizeRestartFn
	resizeRestartFn = func(context.Context, *cobra.Command, *pveclient.Client, string) error { restarted++; return nil }
	t.Cleanup(func() { resizeRestartFn = orig })
	res := &server.Resolved{URL: "https://pve:8006/api2/json", Server: &config.Server{}}
	const cfg = `{"memory":"4096","cores":"2","scsi0":"local-lvm:vm-104-disk-0,size=32G"}`

	t.Run("cores and memory, one restart", func(t *testing.T) {
		restarted = 0
		f := newResizeFake(t, "running", cfg)
		cmd, out, _ := newTestInfoCmd()
		if err := executeResize(cmd.Context(), cmd, f.Client(), res, "web1", 4, 8192, "", &resizeFlags{restart: true}); err != nil {
			t.Fatal(err)
		}
		body := strings.Join(f.Bodies("POST", "/config"), "&")
		if !strings.Contains(body, "cores=4") || !strings.Contains(body, "memory=8192") {
			t.Errorf("config bodies = %q", body)
		}
		if restarted != 1 || !strings.Contains(out.String(), "cores 2 → 4") || !strings.Contains(out.String(), "memory 4 GiB → 8 GiB") {
			t.Errorf("restarted %d, out %q", restarted, out.String())
		}
	})

	t.Run("without a terminal or flag the change is left pending", func(t *testing.T) {
		restarted = 0
		f := newResizeFake(t, "running", cfg)
		cmd, _, errb := newTestInfoCmd()
		if err := executeResize(cmd.Context(), cmd, f.Client(), res, "web1", 4, 8192, "", &resizeFlags{}); err != nil {
			t.Fatal(err)
		}
		if restarted != 0 || strings.Count(errb.String(), "note:") != 1 || !strings.Contains(errb.String(), "cores and memory applies") {
			t.Errorf("restarted %d, stderr %q; want one pending note for both", restarted, errb.String())
		}
	})

	t.Run("a refused shrink changes nothing", func(t *testing.T) {
		f := newResizeFake(t, "stopped", cfg)
		cmd, _, _ := newTestInfoCmd()
		err := executeResize(cmd.Context(), cmd, f.Client(), res, "web1", 0, 8192, "20", &resizeFlags{})
		if err == nil || !strings.Contains(err.Error(), "can't shrink") {
			t.Fatalf("err = %v", err)
		}
		if f.Count("POST", "/config") != 0 || f.Count("PUT", "/resize") != 0 {
			t.Errorf("posts %d, resizes %d; want neither", f.Count("POST", "/config"), f.Count("PUT", "/resize"))
		}
	})

	t.Run("disk and cores on a stopped VM", func(t *testing.T) {
		restarted = 0
		f := newResizeFake(t, "stopped", cfg)
		cmd, out, _ := newTestInfoCmd()
		if err := executeResize(cmd.Context(), cmd, f.Client(), res, "web1", 4, 0, "+10", &resizeFlags{restart: true}); err != nil {
			t.Fatal(err)
		}
		if f.Count("PUT", "/resize") != 1 || restarted != 0 || !strings.Contains(out.String(), "disk 32 → 42 GiB") {
			t.Errorf("resizes %d, restarted %d, out %q", f.Count("PUT", "/resize"), restarted, out.String())
		}
	})

	t.Run("an unchanged value is a no-op", func(t *testing.T) {
		f := newResizeFake(t, "running", cfg)
		cmd, out, _ := newTestInfoCmd()
		if err := executeResize(cmd.Context(), cmd, f.Client(), res, "web1", 2, 0, "", &resizeFlags{restart: true}); err != nil {
			t.Fatal(err)
		}
		if f.Count("POST", "/config") != 0 || !strings.Contains(out.String(), "already has cores 2") {
			t.Errorf("posts %d, out %q", f.Count("POST", "/config"), out.String())
		}
	})
}

func TestResizeNeedsAFlag(t *testing.T) {
	_, _, err := execRoot(t, "resize", "web1", "--no-input")
	if exitcode.From(err) != exitcode.ExitUserError || !strings.Contains(err.Error(), "--cpu") {
		t.Errorf("err = %v (exit %d), want a user error naming the flags", err, exitcode.From(err))
	}
	for _, bad := range [][]string{{"--mem", "lots"}, {"--cpu", "0"}, {"--disk", "+abc"}} {
		args := append([]string{"resize", "web1", "--no-input"}, bad...)
		if _, _, err := execRoot(t, args...); exitcode.From(err) != exitcode.ExitUserError {
			t.Errorf("%v: err = %v, want a user error before any API call", bad, err)
		}
	}
}

func TestLaunchMemAcceptsFractions(t *testing.T) {
	for in, want := range map[float64]int{0: 2048, 4: 4096, 1.5: 1536, 0.5: 512} {
		if got, err := memMiB(in); err != nil || got != want {
			t.Errorf("memMiB(%v) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []float64{-1, 0.1} {
		if _, err := memMiB(bad); !errors.Is(err, exitcode.ErrUserInput) {
			t.Errorf("memMiB(%v): err = %v, want a user-input error", bad, err)
		}
	}
	c := newLaunchCmd()
	if err := c.Flags().Parse([]string{"--mem", "lots"}); err == nil {
		t.Error("--mem lots should not parse")
	}
	if err := c.Flags().Parse([]string{"--mem", "1.5"}); err != nil {
		t.Errorf("--mem 1.5: %v", err)
	}
}

// From the command palette a verb runs with no arguments: on a terminal
// the VM is picked and the value asked for; without one it's an error
// showing the usage.
func TestResizeArgsAsk(t *testing.T) {
	f := newResizeFake(t, "stopped", `{"memory":"4096"}`)
	cmd := newResizeRAMCmd()
	cmd.SetContext(context.Background())

	if _, _, err := resizeArgs(cmd.Context(), cmd, f.Client(), nil, "Memory"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("no terminal: err = %v", err)
	}

	in, errT := tui.StdinIsTerminal, tui.StderrIsTerminal
	tui.StdinIsTerminal = func() bool { return true }
	tui.StderrIsTerminal = func() bool { return true }
	t.Cleanup(func() { tui.StdinIsTerminal, tui.StderrIsTerminal = in, errT })
	oldPick := vmPickFn
	vmPickFn = func(context.Context, *pveclient.Client) (*vm.Ref, error) {
		return &vm.Ref{VMID: 104, Name: "web1"}, nil
	}
	t.Cleanup(func() { vmPickFn = oldPick })
	var asked string
	oldAsk := askTextFn
	askTextFn = func(title string) (string, error) { asked = title; return " 8 ", nil }
	t.Cleanup(func() { askTextFn = oldAsk })

	vmArg, value, err := resizeArgs(cmd.Context(), cmd, f.Client(), nil, "Memory in GiB")
	if err != nil || vmArg != "104" || value != "8" || asked != "Memory in GiB for 104" {
		t.Errorf("got %q %q %v (asked %q)", vmArg, value, err, asked)
	}
	// A value given: no question.
	asked = ""
	if _, value, _ := resizeArgs(cmd.Context(), cmd, f.Client(), []string{"web1", "16"}, "Memory"); value != "16" || asked != "" {
		t.Errorf("given: %q, asked %q", value, asked)
	}
}

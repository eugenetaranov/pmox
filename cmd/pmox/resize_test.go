package main

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvetest"
	"github.com/eugenetaranov/pmox/internal/server"
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
	if restarted != 0 || !strings.Contains(errb.String(), "pmox stop web1 && pmox start web1") || !strings.Contains(out.String(), "cores 2 → 4") {
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
	for path, want := range map[string]string{"vm resize ram": "ram", "vm resize mem": "ram", "vm resize cpu": "cpu", "vm resize disk": "disk"} {
		c, _, err := rootCmd.Find(strings.Fields(path))
		if err != nil || c.Name() != want {
			t.Errorf("%s: %v (%v)", path, c.Name(), err)
		}
	}
	// Only under vm: the root stays uncluttered.
	if c, _, _ := rootCmd.Find([]string{"resize"}); c != nil && c.Name() == "resize" {
		t.Error("resize is registered at the root")
	}
}

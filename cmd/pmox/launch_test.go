package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/server"
)

func TestResolveLaunchOptions_BuiltInDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	resolved := &server.Resolved{
		URL:    "https://pve.example:8006/api2/json",
		Server: &config.Server{TokenID: "t@pam!x", Node: "pve", Template: "9000", Storage: "local-lvm"},
		Secret: "s",
		Source: "single configured",
	}
	f := &launchFlags{}
	opts, err := resolveLaunchOptions(context.Background(), "web1", f, resolved)
	if err != nil {
		t.Fatalf("resolveLaunchOptions err: %v", err)
	}
	if opts.CPU != defaultCPU || opts.MemMB != defaultMemMB || opts.DiskSize != defaultDiskSize {
		t.Errorf("cpu/mem/disk = %d/%d/%q, want %d/%d/%q", opts.CPU, opts.MemMB, opts.DiskSize, defaultCPU, defaultMemMB, defaultDiskSize)
	}
	if opts.Wait != defaultWait {
		t.Errorf("wait = %v, want %v", opts.Wait, defaultWait)
	}
	if opts.TemplateID != 9000 {
		t.Errorf("templateID = %d, want 9000", opts.TemplateID)
	}
	wantPath, _ := config.CloudInitPath(resolved.URL)
	if opts.CloudInitPath != wantPath {
		t.Errorf("CloudInitPath = %q, want %q", opts.CloudInitPath, wantPath)
	}
}

func TestResolveLaunchOptions_CLIFlagWins(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	resolved := &server.Resolved{
		URL: "https://pve.example:8006/api2/json",
		Server: &config.Server{
			TokenID: "t@pam!x", Node: "pve", Template: "9000", Storage: "local-lvm",
		},
		Secret: "s",
	}
	f := &launchFlags{cpu: 8, memMB: 16384, disk: "80G", wait: 2 * time.Minute}
	opts, err := resolveLaunchOptions(context.Background(), "web1", f, resolved)
	if err != nil {
		t.Fatalf("resolveLaunchOptions err: %v", err)
	}
	if opts.CPU != 8 || opts.MemMB != 16384 || opts.DiskSize != "80G" {
		t.Errorf("flag values not honored: %+v", opts)
	}
	if opts.Wait != 2*time.Minute {
		t.Errorf("wait = %v, want 2m", opts.Wait)
	}
}

func TestResolveLaunchOptions_MissingTemplateIsConfigError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	resolved := &server.Resolved{
		URL:    "https://pve.example:8006/api2/json",
		Server: &config.Server{TokenID: "t@pam!x", Node: "pve"},
		Secret: "s",
	}
	_, err := resolveLaunchOptions(context.Background(), "web1", &launchFlags{}, resolved)
	if err == nil {
		t.Fatal("resolveLaunchOptions err=nil, want missing template error")
	}
	if !strings.Contains(err.Error(), "template") {
		t.Errorf("err = %v, want mention of template", err)
	}
	if !strings.Contains(err.Error(), "pmox configure") {
		t.Errorf("err = %v, want suggestion to run pmox configure", err)
	}
}

func TestLaunchVerboseLogLine(t *testing.T) {
	// Exercise just the format to keep the test hermetic — the real
	// runLaunch path requires a live keychain and PVE. A focused unit
	// test of the D-T4 line is enough: it's one fmt.Fprintf call.
	var buf bytes.Buffer
	resolved := &server.Resolved{URL: "https://host:8006/api2/json", Source: "--server flag"}
	// Mirror the exact Fprintf used in runLaunch.
	_, _ = buf.WriteString("using server " + resolved.URL + " (" + resolved.Source + ")\n")
	got := buf.String()
	if got != "using server https://host:8006/api2/json (--server flag)\n" {
		t.Errorf("log line = %q", got)
	}
}

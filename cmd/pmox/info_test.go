package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvetest"
	"github.com/eugenetaranov/pmox/internal/vm"
)

const oneVMResources = `{"data":[
  {"vmid":104,"name":"web1","node":"pve1","status":"running","tags":"pmox"}
]}`

const web1StatusRunning = `{"data":{"status":"running","vmid":104,"name":"web1","uptime":120,"cpus":2}}`

const web1Config = `{"data":{"cores":2,"memory":2048,"net0":"virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0","scsi0":"local-lvm:vm-104-disk-0,size=20G"}}`

const web1AgentNet = `{"data":{"result":[{"name":"eth0","hardware-address":"aa:bb:cc:dd:ee:ff","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"192.168.1.43"}]}]}}`

func newInfoFake(t *testing.T) *pvetest.Server {
	t.Helper()
	s := pvetest.New(t)
	s.Handle("GET", "/cluster/resources", pvetest.JSON(oneVMResources))
	s.Handle("GET", "/status/current", pvetest.JSON(web1StatusRunning))
	s.Handle("GET", "/qemu/104/config", pvetest.JSON(web1Config))
	s.Handle("GET", "/agent/network-get-interfaces", pvetest.JSON(web1AgentNet))
	return s
}

func newTestInfoCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "info"}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetContext(context.Background())
	return cmd, &out, &errb
}

func TestInfo_TextMode(t *testing.T) {
	f := newInfoFake(t)
	outputMode = "text"
	cmd, out, _ := newTestInfoCmd()
	if err := executeInfo(cmd.Context(), cmd, f.Client(), "web1"); err != nil {
		t.Fatalf("executeInfo: %v", err)
	}
	body := out.String()
	for _, want := range []string{
		"Name:     web1",
		"VMID:     104",
		"Node:     pve1",
		"Status:   running",
		"CPU:      2 cores",
		"Memory:   2048 MB",
		"20G",
		"local-lvm",
		"192.168.1.43",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q: %s", want, body)
		}
	}
}

// Task 3.3: zero-arg info auto-selects the only pmox VM via the
// picker seam.
func TestInfo_ZeroArgs_OneVMAutoSelect(t *testing.T) {
	f := newInfoFake(t)
	outputMode = "text"

	orig := vmPickFn
	vmPickFn = func(context.Context, *pveclient.Client, io.Writer) (*vm.Ref, error) {
		return &vm.Ref{VMID: 104}, nil
	}
	t.Cleanup(func() { vmPickFn = orig })

	cmd, out, _ := newTestInfoCmd()
	arg, err := resolveTargetArg(cmd.Context(), f.Client(), nil, io.Discard)
	if err != nil {
		t.Fatalf("resolveTargetArg: %v", err)
	}
	if err := executeInfo(cmd.Context(), cmd, f.Client(), arg); err != nil {
		t.Fatalf("executeInfo: %v", err)
	}
	if !strings.Contains(out.String(), "Name:     web1") {
		t.Errorf("stdout missing web1 header: %q", out.String())
	}
}

func TestInfo_JSONMode(t *testing.T) {
	f := newInfoFake(t)
	outputMode = "json"
	t.Cleanup(func() { outputMode = "text" })
	cmd, out, _ := newTestInfoCmd()
	if err := executeInfo(cmd.Context(), cmd, f.Client(), "104"); err != nil {
		t.Fatalf("executeInfo: %v", err)
	}
	var info vm.Info
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json decode: %v — body=%q", err, out.String())
	}
	if info.Name != "web1" || info.VMID != 104 || info.CPU != 2 || info.MemMB != 2048 {
		t.Errorf("info = %+v", info)
	}
	if info.DiskSize != "20G" || info.DiskStorage != "local-lvm" {
		t.Errorf("disk = %q on %q", info.DiskSize, info.DiskStorage)
	}
	if len(info.Interfaces) != 1 || info.Interfaces[0].Name != "eth0" {
		t.Errorf("interfaces = %+v", info.Interfaces)
	}
}

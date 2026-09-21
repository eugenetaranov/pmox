package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/pvetest"
)

func sshConfigServer(t *testing.T, status string) *pvetest.Server {
	s := pvetest.New(t)
	s.Handle("GET", "/cluster/resources", pvetest.JSON(`{"data":[{"vmid":100,"name":"web1","node":"pve1","status":"`+status+`","tags":"pmox"}]}`))
	s.Handle("GET", "/status/current", pvetest.JSON(`{"data":{"status":"`+status+`","vmid":100,"name":"web1"}}`))
	s.Handle("GET", "/agent/network-get-interfaces", pvetest.JSON(`{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"10.0.0.5"}]}]}}`))
	return s
}

func TestSSHConfig_ResolveRunningVM(t *testing.T) {
	s := sshConfigServer(t, "running")
	info, err := resolveSSHConnInfo(context.Background(), s.Client(), "web1", &sshFlags{}, "pmox", "")
	if err != nil {
		t.Fatalf("resolveSSHConnInfo: %v", err)
	}
	if info.Hostname != "10.0.0.5" {
		t.Errorf("hostname = %q, want 10.0.0.5", info.Hostname)
	}
	if info.User != "pmox" {
		t.Errorf("user = %q, want pmox", info.User)
	}
	if !strings.HasPrefix(info.Command, "ssh ") || !strings.Contains(info.Command, "pmox@10.0.0.5") {
		t.Errorf("command = %q, want an ssh command to pmox@10.0.0.5", info.Command)
	}
}

func TestSSHConfig_UserOverride(t *testing.T) {
	s := sshConfigServer(t, "running")
	info, err := resolveSSHConnInfo(context.Background(), s.Client(), "web1", &sshFlags{user: "ubuntu"}, "pmox", "")
	if err != nil {
		t.Fatalf("resolveSSHConnInfo: %v", err)
	}
	if info.User != "ubuntu" {
		t.Errorf("--user override ignored: got %q", info.User)
	}
	if !strings.Contains(info.Command, "ubuntu@10.0.0.5") {
		t.Errorf("command = %q, want ubuntu@10.0.0.5", info.Command)
	}
}

func TestSSHConfig_StoppedVMErrors(t *testing.T) {
	s := sshConfigServer(t, "stopped")
	_, err := resolveSSHConnInfo(context.Background(), s.Client(), "web1", &sshFlags{}, "pmox", "")
	if err == nil {
		t.Fatal("expected an error for a stopped VM")
	}
	if !strings.Contains(err.Error(), "not running") || !strings.Contains(err.Error(), "pmox start") {
		t.Errorf("error should say it's not running and how to start it, got: %v", err)
	}
}

func TestSSHConfig_UntaggedRefusedWithoutForce(t *testing.T) {
	s := pvetest.New(t)
	s.Handle("GET", "/cluster/resources", pvetest.JSON(`{"data":[{"vmid":200,"name":"legacy","node":"pve1","status":"running","tags":""}]}`))
	_, err := resolveSSHConnInfo(context.Background(), s.Client(), "legacy", &sshFlags{}, "pmox", "")
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("untagged VM should be refused with a --force hint, got: %v", err)
	}
}

func TestRenderSSHConfigBlock(t *testing.T) {
	// Deterministic host-key options via --ssh-insecure.
	sshInsecure = true
	sshInsecureWarned = true
	defer func() { sshInsecure = false }()

	info := &sshConnInfo{Name: "web1", Hostname: "10.0.0.5", User: "pmox", IdentityFile: "/home/u/.ssh/id"}
	var buf bytes.Buffer
	renderSSHConfigBlock(&buf, info)
	out := buf.String()
	for _, want := range []string{"Host web1", "HostName 10.0.0.5", "User pmox", "IdentityFile /home/u/.ssh/id", "IdentitiesOnly yes", "StrictHostKeyChecking no"} {
		if !strings.Contains(out, want) {
			t.Errorf("config block missing %q:\n%s", want, out)
		}
	}
}

func TestSSHCommandLine_QuotesSpaces(t *testing.T) {
	sshInsecure = true
	sshInsecureWarned = true
	defer func() { sshInsecure = false }()

	cmd := sshCommandLine(&sshTarget{IP: "10.0.0.5", User: "pmox", Key: "/home/First Last/.ssh/id"})
	if !strings.Contains(cmd, "-i \"/home/First Last/.ssh/id\"") {
		t.Errorf("key path with a space should be quoted: %s", cmd)
	}
}

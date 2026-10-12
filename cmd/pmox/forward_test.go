package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
)

func TestExpandForward(t *testing.T) {
	cases := map[string]string{
		"3000":                   "3000:localhost:3000",
		"8000:127.0.0.1:8000":    "8000:127.0.0.1:8000",
		"localhost:5432:db:5432": "localhost:5432:db:5432",
		"":                       "",
	}
	for in, want := range cases {
		if got := expandForward(in); got != want {
			t.Errorf("expandForward(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestForwardingArgs(t *testing.T) {
	got := forwardingArgs(true, []string{"3000", "8000:127.0.0.1:8000"})
	want := []string{"-A", "-L", "3000:localhost:3000", "-L", "8000:127.0.0.1:8000"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("forwardingArgs = %v, want %v", got, want)
	}
	if got := forwardingArgs(false, nil); len(got) != 0 {
		t.Errorf("no forwarding should add nothing, got %v", got)
	}
}

// forwardCmd parses argv with the forwarding flags registered.
func forwardCmd(t *testing.T, ports bool, argv ...string) (*cobra.Command, *sshFlags) {
	t.Helper()
	f := &sshFlags{}
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	addForwardFlags(cmd, f, ports)
	if err := cmd.ParseFlags(argv); err != nil {
		t.Fatalf("parse %v: %v", argv, err)
	}
	return cmd, f
}

func TestWantForwardAgent(t *testing.T) {
	on := &config.Server{ForwardAgent: true}
	off := &config.Server{}

	cmd, f := forwardCmd(t, true, "-A")
	if !wantForwardAgent(cmd, f, off) {
		t.Error("-A should turn forwarding on")
	}
	cmd, f = forwardCmd(t, false)
	if !wantForwardAgent(cmd, f, on) {
		t.Error("forward_agent: true should be the default")
	}
	cmd, f = forwardCmd(t, false, "--forward-agent=false")
	if wantForwardAgent(cmd, f, on) {
		t.Error("--forward-agent=false should override the config")
	}
	cmd, f = forwardCmd(t, false)
	if wantForwardAgent(cmd, f, off) || wantForwardAgent(cmd, f, nil) {
		t.Error("forwarding should be off by default")
	}
}

func TestForwardFlagsRegistered(t *testing.T) {
	for _, c := range []*cobra.Command{newShellCmd(), newExecCmd(), newSSHConfigCmd()} {
		if c.Flags().ShorthandLookup("A") == nil {
			t.Errorf("%s: missing -A", c.Name())
		}
	}
	if newShellCmd().Flags().ShorthandLookup("L") == nil {
		t.Error("shell: missing -L")
	}
	if newExecCmd().Flags().Lookup("forward") != nil {
		t.Error("exec should not take -L")
	}
	_, f := forwardCmd(t, true, "-L", "3000", "-L", "8000:127.0.0.1:8000")
	opts := forwardingArgs(false, f.forwards)
	args := buildSSHArgs("/usr/bin/ssh", &sshTarget{IP: "10.0.0.1", User: "pmox"}, append(testInsecureHostKeyOpts, opts...), nil)
	got := strings.Join(args, " ")
	if !strings.Contains(got, "-L 3000:localhost:3000 -L 8000:127.0.0.1:8000 pmox@10.0.0.1") {
		t.Errorf("forwards should come before user@ip: %s", got)
	}
}

func TestRenderSSHConfigBlock_ForwardAgent(t *testing.T) {
	sshInsecure = true
	sshInsecureWarned = true
	defer func() { sshInsecure = false }()

	var buf bytes.Buffer
	renderSSHConfigBlock(&buf, &sshConnInfo{Name: "web1", Hostname: "10.0.0.5", User: "pmox", ForwardAgent: true})
	if !strings.Contains(buf.String(), "  ForwardAgent yes\n") {
		t.Errorf("block missing ForwardAgent:\n%s", buf.String())
	}
	buf.Reset()
	renderSSHConfigBlock(&buf, &sshConnInfo{Name: "web1", Hostname: "10.0.0.5", User: "pmox"})
	if strings.Contains(buf.String(), "ForwardAgent") {
		t.Errorf("ForwardAgent written without being asked for:\n%s", buf.String())
	}
	if cmd := sshCommandLine(&sshTarget{IP: "10.0.0.5", User: "pmox"}, "-A"); !strings.Contains(cmd, " -A pmox@10.0.0.5") {
		t.Errorf("command = %q, want -A before the destination", cmd)
	}
}

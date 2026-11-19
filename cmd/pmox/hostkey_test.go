package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/vm"
)

const (
	oldHostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOldOldOldOldOldOldOldOldOldOldOldOldOldOldOld"
	newHostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINewNewNewNewNewNewNewNewNewNewNewNewNewNewNew"
)

type fakeAgentFiles map[string]string

func (f fakeAgentFiles) AgentFileRead(_ context.Context, _ string, _ int, path string) ([]byte, bool, error) {
	if c, ok := f[path]; ok {
		return []byte(c), false, nil
	}
	return nil, false, errors.New("no such file")
}

func guestKnownHosts(t *testing.T, content string) string {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := guestKnownHostsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(strings.TrimSuffix(path, "/known_hosts_guests"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRefreshGuestHostKeyRepinsStaleKey(t *testing.T) {
	path := guestKnownHosts(t, "10.0.0.5 "+oldHostKey+"\n10.0.0.9 "+oldHostKey+"\n")
	agent := fakeAgentFiles{"/etc/ssh/ssh_host_ed25519_key.pub": newHostKey + " root@alice\n"}
	var stderr bytes.Buffer
	refreshGuestHostKey(context.Background(), &stderr, agent, &vm.Ref{Name: "alice"}, "10.0.0.5")

	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "10.0.0.5 "+newHostKey) || strings.Contains(got, "10.0.0.5 "+oldHostKey) {
		t.Errorf("10.0.0.5 not re-pinned:\n%s", got)
	}
	if !strings.Contains(got, "10.0.0.9 "+oldHostKey) {
		t.Errorf("other hosts must be kept:\n%s", got)
	}
	if _, err := os.Stat(path + ".old"); !os.IsNotExist(err) {
		t.Errorf("ssh-keygen's .old backup left behind")
	}
	if !strings.Contains(stderr.String(), "alice has a new SSH host key") {
		t.Errorf("missing note, got %q", stderr.String())
	}
}

func TestRefreshGuestHostKeyLeavesFileAlone(t *testing.T) {
	for name, tc := range map[string]struct {
		known string
		agent fakeAgentFiles
	}{
		"current key":     {"10.0.0.5 " + newHostKey + "\n", fakeAgentFiles{"/etc/ssh/ssh_host_ed25519_key.pub": newHostKey}},
		"no pin":          {"10.0.0.9 " + oldHostKey + "\n", fakeAgentFiles{"/etc/ssh/ssh_host_ed25519_key.pub": newHostKey}},
		"agent can't say": {"10.0.0.5 " + oldHostKey + "\n", fakeAgentFiles{}},
	} {
		t.Run(name, func(t *testing.T) {
			path := guestKnownHosts(t, tc.known)
			var stderr bytes.Buffer
			refreshGuestHostKey(context.Background(), &stderr, tc.agent, &vm.Ref{Name: "alice"}, "10.0.0.5")
			if data, _ := os.ReadFile(path); string(data) != tc.known || stderr.Len() != 0 {
				t.Errorf("file or output changed:\n%s\n%s", data, stderr.String())
			}
		})
	}
}

func TestExplainSSHFailureHostKeyChanged(t *testing.T) {
	orig := sshProbeFn
	sshProbeFn = func(context.Context, *sshTarget) sshFailCause { return failHostKeyChanged }
	t.Cleanup(func() { sshProbeFn = orig })

	target := &sshTarget{IP: "10.0.0.5", User: "ubuntu", Name: "alice"}
	got := explainSSHFailure(context.Background(), target, formURL, exitErr(255), true)
	var he *hostKeyChangedError
	if !errors.As(got, &he) {
		t.Fatalf("want hostKeyChangedError, got %v", got)
	}
	for _, want := range []string{"alice (10.0.0.5) answered with a different SSH host key", "ssh-keygen -R 10.0.0.5 -f "} {
		if !strings.Contains(he.msg, want) {
			t.Errorf("message missing %q:\n%s", want, he.msg)
		}
	}
}

package hook

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeTempScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPostCreateHook_EnvVars(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script hook test not supported on windows")
	}
	script := writeTempScript(t, `echo "$PMOX_IP $PMOX_VMID $PMOX_NAME $PMOX_USER $PMOX_NODE"`)
	h := &PostCreateHook{Path: script}
	var stdout, stderr bytes.Buffer
	err := h.Run(context.Background(), Env{
		IP:   "192.168.1.10",
		VMID: 104,
		Name: "web1",
		User: "ubuntu",
		Node: "pve",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run err: %v (stderr=%q)", err, stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	want := "192.168.1.10 104 web1 ubuntu pve"
	if got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestPostCreateHook_NonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script hook test not supported on windows")
	}
	script := writeTempScript(t, "exit 1")
	h := &PostCreateHook{Path: script}
	err := h.Run(context.Background(), Env{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run err=nil, want ExitError")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("err = %T (%v), want *exec.ExitError", err, err)
	}
}

func TestPostCreateHook_ContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script hook test not supported on windows")
	}
	script := writeTempScript(t, "sleep 30")
	h := &PostCreateHook{Path: script}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := h.Run(ctx, Env{}, &bytes.Buffer{}, &bytes.Buffer{})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Run took %v, want <1s", elapsed)
	}
	if err == nil {
		t.Fatal("Run err=nil, want ctx cancel error")
	}
}

func TestTackHook_LookPathFail(t *testing.T) {
	t.Setenv("PATH", "")
	h := &TackHook{ConfigPath: "./tack.yaml"}
	err := h.Run(context.Background(), Env{IP: "1.2.3.4", User: "ubuntu"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run err=nil, want not-found error")
	}
	if !strings.Contains(err.Error(), "tack binary not found") {
		t.Errorf("err = %v, want 'tack binary not found'", err)
	}
}

func TestTackHook_ArgvShape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub not supported on windows")
	}
	pathDir := t.TempDir()
	argLogPath := filepath.Join(t.TempDir(), "argv")
	stub := `#!/bin/sh
printf '%s\n' "$@" > ` + argLogPath + `
`
	stubPath := filepath.Join(pathDir, "tack")
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)

	h := &TackHook{ConfigPath: "./tack.yaml"}
	err := h.Run(context.Background(), Env{IP: "192.168.1.10", User: "ubuntu"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	data, err := os.ReadFile(argLogPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"apply", "--host", "192.168.1.10", "--user", "ubuntu", "./tack.yaml"}
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAnsibleHook_LookPathFail(t *testing.T) {
	t.Setenv("PATH", "")
	h := &AnsibleHook{PlaybookPath: "./play.yaml"}
	err := h.Run(context.Background(), Env{IP: "1.2.3.4", User: "ubuntu"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run err=nil, want not-found error")
	}
	if !strings.Contains(err.Error(), "ansible-playbook binary not found") {
		t.Errorf("err = %v, want 'ansible-playbook binary not found'", err)
	}
}

func TestAnsibleHook_ArgvShape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub not supported on windows")
	}
	pathDir := t.TempDir()
	argLogPath := filepath.Join(t.TempDir(), "argv")
	stub := `#!/bin/sh
printf '%s\n' "$@" > ` + argLogPath + `
`
	stubPath := filepath.Join(pathDir, "ansible-playbook")
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)

	h := &AnsibleHook{PlaybookPath: "./play.yaml"}
	env := Env{
		IP:     "192.168.1.10",
		User:   "ubuntu",
		Name:   "web1",
		VMID:   104,
		SSHKey: "/tmp/id_ed25519",
	}
	err := h.Run(context.Background(), env, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	data, err := os.ReadFile(argLogPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")

	wantSubstrings := []string{"-i", "192.168.1.10,", "-u", "ubuntu", "--private-key", "/tmp/id_ed25519", "-e", "pmox_vmid=104", "-e", "pmox_name=web1"}
	for _, w := range wantSubstrings {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("argv missing %q; argv=%v", w, got)
		}
	}
	if got[len(got)-1] != "./play.yaml" {
		t.Errorf("last argv = %q, want ./play.yaml", got[len(got)-1])
	}
}

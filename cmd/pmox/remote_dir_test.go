package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
)

func TestRemoteShellPath(t *testing.T) {
	for in, want := range map[string]string{
		"/mnt/src":     `'/mnt/src'`,
		"~/project/x":  `"$HOME"/'project/x'`,
		"project/pmox": `"$HOME"/'project/pmox'`,
		"":             `"$HOME"`,
		"/it's":        `'/it'\''s'`,
	} {
		if got := remoteShellPath(in); got != want {
			t.Errorf("remoteShellPath(%q) = %s, want %s", in, got, want)
		}
	}
	if got := displayRemotePath("project/pmox"); got != "~/project/pmox" {
		t.Errorf("display relative = %q", got)
	}
}

func TestDirToCheck(t *testing.T) {
	cases := []struct {
		dest     string
		wholeDir bool
		want     string
	}{
		{"/mnt/src", true, "/mnt/src"}, // mount / sync of a directory
		{"~/project/pmox", true, "~/project/pmox"},
		{"/opt/new/", false, "/opt/new"},        // trailing slash: the dir itself
		{"/opt/new/app.tar", false, "/opt/new"}, // a file: its parent
		{"/etc/app", false, "/etc"},             // cp -r src /etc/app keeps scp's meaning
		{"file.txt", false, ""},                 // home: nothing to check
		{"~", true, ""},
	}
	for _, c := range cases {
		if got := dirToCheck(c.dest, c.wholeDir, remoteParent); got != c.want {
			t.Errorf("dirToCheck(%q, %v) = %q, want %q", c.dest, c.wholeDir, got, c.want)
		}
	}
}

// stubSSH records scripts and answers test -d with exists.
func stubRemoteSSH(t *testing.T, exists bool, createCode int) *[]string {
	t.Helper()
	var scripts []string
	orig := remoteSSHFn
	remoteSSHFn = func(_ context.Context, _ *sshTarget, _ time.Duration, script string) (int, string, error) {
		scripts = append(scripts, script)
		if strings.HasPrefix(script, "test -d") {
			if exists {
				return 0, "", nil
			}
			return 1, "", nil
		}
		if createCode != 0 {
			return createCode, "sudo: a password is required", nil
		}
		return 0, "", nil
	}
	t.Cleanup(func() { remoteSSHFn = orig })
	return &scripts
}

func TestEnsureRemoteDirPresent(t *testing.T) {
	scripts := stubRemoteSSH(t, true, 0)
	cmd, _, _ := newTestInfoCmd()
	if err := ensureRemoteDir(context.Background(), cmd, &sshTarget{Name: "web1"}, "/mnt/src", false); err != nil {
		t.Fatal(err)
	}
	if len(*scripts) != 1 {
		t.Errorf("scripts = %v, want only the check", *scripts)
	}
}

func TestEnsureRemoteDirNonInteractiveNeedsMkdir(t *testing.T) {
	stubRemoteSSH(t, false, 0)
	tui.SetNoInput(true)
	t.Cleanup(func() { tui.SetNoInput(false) })
	cmd, _, _ := newTestInfoCmd()
	err := ensureRemoteDir(context.Background(), cmd, &sshTarget{Name: "web1"}, "project/pmox", false)
	if !errors.Is(err, exitcode.ErrUserInput) || !strings.Contains(err.Error(), "~/project/pmox on web1") || !strings.Contains(err.Error(), "--mkdir") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureRemoteDirMkdirCreatesWithSudoFallback(t *testing.T) {
	scripts := stubRemoteSSH(t, false, 0)
	cmd, _, errb := newTestInfoCmd()
	if err := ensureRemoteDir(context.Background(), cmd, &sshTarget{Name: "web1"}, "/mnt/src", true); err != nil {
		t.Fatal(err)
	}
	create := (*scripts)[1]
	if !strings.Contains(create, "mkdir -p -- '/mnt/src'") || !strings.Contains(create, `sudo -n install -d -o "$(id -un)"`) {
		t.Errorf("create script = %s", create)
	}
	if !strings.Contains(errb.String(), "created /mnt/src on web1") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestEnsureRemoteDirPromptDeclined(t *testing.T) {
	scripts := stubRemoteSSH(t, false, 0)
	forceInteractive(t)
	orig := confirmMkdirFn
	var asked string
	confirmMkdirFn = func(title string, defYes bool) (bool, error) {
		asked = title
		if !defYes {
			t.Error("the create prompt should default to Yes")
		}
		return false, nil
	}
	t.Cleanup(func() { confirmMkdirFn = orig })
	cmd, _, _ := newTestInfoCmd()
	err := ensureRemoteDir(context.Background(), cmd, &sshTarget{Name: "web1"}, "~/project/pmox", false)
	if !errors.Is(err, tui.ErrAborted) || asked != "Create ~/project/pmox on web1?" || len(*scripts) != 1 {
		t.Fatalf("err=%v asked=%q scripts=%v", err, asked, *scripts)
	}
}

func TestEnsureRemoteDirCreateFails(t *testing.T) {
	stubRemoteSSH(t, false, 1)
	cmd, _, _ := newTestInfoCmd()
	err := ensureRemoteDir(context.Background(), cmd, &sshTarget{Name: "web1"}, "/mnt/src", true)
	if err == nil || !strings.Contains(err.Error(), "could not create /mnt/src on web1") || !strings.Contains(err.Error(), "~/src") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureLocalDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	tui.SetNoInput(true)
	t.Cleanup(func() { tui.SetNoInput(false) })
	cmd, _, _ := newTestInfoCmd()
	if err := ensureLocalDir(cmd, dir, false); !errors.Is(err, exitcode.ErrUserInput) {
		t.Fatalf("without --mkdir: %v", err)
	}
	if err := ensureLocalDir(cmd, dir, true); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("not created: %v", err)
	}
}

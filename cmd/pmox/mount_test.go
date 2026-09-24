package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/mount"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// spawnDetachedSleep starts a `sleep` reparented to init (via a
// short-lived sh) so it is NOT a child of the test process — matching
// how a real mount daemon is detached, so mount.Alive/Stop behave as in
// production. Returns the sleep's PID.
func spawnDetachedSleep(t *testing.T) int {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("sh not on PATH: %v", err)
	}
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep not on PATH: %v", err)
	}
	// Symlink sleep under a pmox-named path so its command line satisfies
	// mount.LooksReused's pmox-daemon check (a bare "sleep" would be
	// treated as a recycled PID and skipped).
	link := filepath.Join(t.TempDir(), "pmox-mount-daemon")
	if err := os.Symlink(sleepBin, link); err != nil {
		t.Fatalf("symlink sleep: %v", err)
	}
	out, err := exec.Command(sh, "-c", link+" 30 >/dev/null 2>&1 & echo $!").Output()
	if err != nil {
		t.Fatalf("spawn detached sleep: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		t.Fatalf("bad pid: %q", out)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Signal(syscall.SIGKILL)
		}
	})
	for i := 0; i < 50 && !mount.Alive(pid); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	return pid
}

// The P1 regression: `pmox umount web1:/opt/a` must stop only that mount,
// not every mount for web1. The registry records both endpoints, so
// umountByRemote can target one exactly.
func TestUmountByRemote_TargetsOnlyMatchingMount(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := testMountStateDir(t)
	require.NoError(t, os.MkdirAll(dir, 0o700))

	pidA := spawnDetachedSleep(t)
	pidB := spawnDetachedSleep(t)
	_, err := mount.Save(dir, mount.Record{VMName: "web1", LocalPath: "/src/a", RemotePath: "/opt/a", PID: pidA})
	require.NoError(t, err)
	_, err = mount.Save(dir, mount.Record{VMName: "web1", LocalPath: "/src/b", RemotePath: "/opt/b", PID: pidB})
	require.NoError(t, err)

	require.NoError(t, umountByRemote(newTestUmountCmd(), "web1", "/opt/a"))

	assert.False(t, mount.Alive(pidA), "targeted mount should be stopped")
	assert.True(t, mount.Alive(pidB), "other mount for the same VM must be left running")

	recs, _ := mount.ForVM(dir, "web1")
	require.Len(t, recs, 1, "only the targeted record should be removed")
	assert.Equal(t, "/opt/b", recs[0].RemotePath)
}

func TestBuildMountRsyncArgs(t *testing.T) {
	target := &sshTarget{IP: "10.0.0.1", User: "pmox", Key: "/home/user/.ssh/id"}

	tests := []struct {
		name         string
		localPath    string
		remotePath   string
		noGitignore  bool
		noDelete     bool
		excludes     []string
		extra        []string
		wantContains []string
		wantAbsent   []string
		wantSuffix   []string
	}{
		{
			name:       "defaults",
			localPath:  "./src",
			remotePath: "/opt/app",
			excludes:   defaultMountExcludes,
			wantContains: []string{
				"-az", "--partial", "--delete",
				"--filter=:- .gitignore",
				"--exclude=.git",
				"--exclude=node_modules",
				"--exclude=__pycache__",
			},
			wantSuffix: []string{"./src/", "pmox@10.0.0.1:/opt/app"},
		},
		{
			name:        "no-gitignore",
			localPath:   "./src",
			remotePath:  "/opt/app",
			noGitignore: true,
			excludes:    defaultMountExcludes,
			wantContains: []string{
				"--delete",
			},
			wantAbsent: []string{
				"--filter",
			},
		},
		{
			name:       "no-delete",
			localPath:  "./src",
			remotePath: "/opt/app",
			noDelete:   true,
			excludes:   defaultMountExcludes,
			wantContains: []string{
				"--filter=:- .gitignore",
			},
			wantAbsent: []string{
				"--delete",
			},
		},
		{
			name:       "custom excludes replace defaults",
			localPath:  "./src",
			remotePath: "/opt/app",
			excludes:   []string{".git", "*.log"},
			wantContains: []string{
				"--exclude=.git",
				"--exclude=*.log",
			},
			wantAbsent: []string{
				"--exclude=node_modules",
				"--exclude=.venv",
			},
		},
		{
			name:       "extra args via --",
			localPath:  "./src",
			remotePath: "/opt/app",
			excludes:   defaultMountExcludes,
			extra:      []string{"--bwlimit=1000"},
			wantContains: []string{
				"--bwlimit=1000",
			},
		},
		{
			name:       "trailing slash added to local path",
			localPath:  "./src",
			remotePath: "/opt/app/",
			excludes:   []string{".git"},
			wantSuffix: []string{"./src/", "pmox@10.0.0.1:/opt/app/"},
		},
		{
			name:       "already has trailing slash",
			localPath:  "./src/",
			remotePath: "/opt/app",
			excludes:   []string{".git"},
			wantSuffix: []string{"./src/", "pmox@10.0.0.1:/opt/app"},
		},
		{
			name:        "no-gitignore and no-delete combined",
			localPath:   "./src",
			remotePath:  "/opt/app",
			noGitignore: true,
			noDelete:    true,
			excludes:    []string{".git"},
			wantContains: []string{
				"-az", "--partial", "--exclude=.git",
			},
			wantAbsent: []string{
				"--delete", "--filter",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildMountRsyncArgs("/usr/bin/rsync", target, tt.localPath, tt.remotePath,
				tt.noGitignore, tt.noDelete, tt.excludes, testInsecureHostKeyOpts, tt.extra)
			joined := strings.Join(args, " ")

			assert.Equal(t, "/usr/bin/rsync", args[0])

			for _, s := range tt.wantContains {
				assert.Contains(t, joined, s, "should contain %q", s)
			}
			for _, s := range tt.wantAbsent {
				assert.NotContains(t, joined, s, "should not contain %q", s)
			}
			if len(tt.wantSuffix) > 0 {
				got := args[len(args)-len(tt.wantSuffix):]
				assert.Equal(t, tt.wantSuffix, got)
			}
		})
	}
}

func TestBuildMountRsyncArgs_SSHOptions(t *testing.T) {
	target := &sshTarget{IP: "10.0.0.5", User: "ubuntu", Key: "/tmp/key"}
	args := buildMountRsyncArgs("/usr/bin/rsync", target, "./src", "/opt/app",
		false, false, []string{".git"}, testInsecureHostKeyOpts, nil)

	assert.Equal(t, "-e", args[1])
	assert.Contains(t, args[2], "ssh")
	assert.Contains(t, args[2], "-i /tmp/key")
	assert.Contains(t, args[2], "StrictHostKeyChecking=no")
}

func TestResolveExcludes(t *testing.T) {
	t.Run("flag excludes take precedence", func(t *testing.T) {
		got := resolveExcludes([]string{".git", "*.log"})
		assert.Equal(t, []string{".git", "*.log"}, got)
	})

	t.Run("falls back to defaults when no flags and no config", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		got := resolveExcludes(nil)
		assert.Equal(t, defaultMountExcludes, got)
	})

	t.Run("config excludes replace defaults", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdg)
		require.NoError(t, os.MkdirAll(filepath.Join(xdg, "pmox"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(xdg, "pmox", "config.yaml"),
			[]byte("mount_excludes:\n  - .git\n  - vendor/\n"), 0o600))

		got := resolveExcludes(nil)
		assert.Equal(t, []string{".git", "vendor/"}, got)
	})

	t.Run("config load error falls back to defaults", func(t *testing.T) {
		old := mountConfigLoadFn
		mountConfigLoadFn = func() (*config.Config, error) { return nil, errors.New("boom") }
		defer func() { mountConfigLoadFn = old }()

		got := resolveExcludes(nil)
		assert.Equal(t, defaultMountExcludes, got)
	})
}

// testMountStateDir resolves (and creates nothing under) the mount state
// dir for the test's XDG_STATE_HOME.
func testMountStateDir(t *testing.T) string {
	t.Helper()
	dir, err := mount.StateDir()
	require.NoError(t, err)
	return dir
}

func TestMountChildArgs(t *testing.T) {
	const url = "https://pve.example:8006/api2/json"

	t.Run("forwards resolved server and omits unset user", func(t *testing.T) {
		f := &mountFlags{debounce: 300 * time.Millisecond}
		args := mountChildArgs("/bin/pmox", f, url, "./src", "web1", "/opt/app", false, nil)
		assert.Equal(t, []string{"/bin/pmox", "mount", "--foreground", "--server", url, "./src", "web1:/opt/app"}, args)
		assert.NotContains(t, args, "--user")
	})

	t.Run("forwards explicit user including pmox", func(t *testing.T) {
		for _, u := range []string{"pmox", "ubuntu"} {
			f := &mountFlags{sshFlags: sshFlags{user: u}, debounce: 300 * time.Millisecond}
			args := mountChildArgs("/bin/pmox", f, url, "./src", "web1", "/opt/app", false, nil)
			assert.Contains(t, strings.Join(args, " "), "--user "+u)
		}
	})

	t.Run("global flags precede the rsync pass-through args", func(t *testing.T) {
		f := &mountFlags{
			sshFlags:    sshFlags{identity: "/k", force: true},
			debounce:    time.Second,
			noGitignore: true,
			noDelete:    true,
			excludes:    []string{"*.log"},
		}
		args := mountChildArgs("/bin/pmox", f, url, "./src", "web1", "/opt/app", true, []string{"--bwlimit=1000"})
		dash := -1
		for i, a := range args {
			if a == "--" {
				dash = i
			}
		}
		require.NotEqual(t, -1, dash)
		assert.Equal(t, []string{"--bwlimit=1000"}, args[dash+1:])
		head := strings.Join(args[:dash], " ")
		for _, want := range []string{"--server " + url, "--identity /k", "--force", "--ssh-insecure", "--no-gitignore", "--no-delete", "--debounce 1s", "--exclude=*.log", "./src web1:/opt/app"} {
			assert.Contains(t, head, want)
		}
	})
}

func TestRunMountDaemon_SpawnsWithResolvedServer(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const url = "https://pve.example:8006/api2/json"

	var gotArgs []string
	var gotRec mount.Record
	orig := mountStartDaemonFn
	mountStartDaemonFn = func(stateDir, exe string, args []string, rec mount.Record) (mount.Record, error) {
		gotArgs, gotRec = args, rec
		rec.PID = 4242
		return rec, nil
	}
	t.Cleanup(func() { mountStartDaemonFn = orig })

	cmd := newTestUmountCmd()
	var errbuf bytes.Buffer
	cmd.SetErr(&errbuf)
	f := &mountFlags{debounce: 300 * time.Millisecond}
	require.NoError(t, runMountDaemon(cmd, "./src", "web1", "/opt/app", url, f))

	assert.Contains(t, strings.Join(gotArgs, " "), "--server "+url)
	assert.NotContains(t, gotArgs, "--user")
	assert.Equal(t, mount.LogPath(testMountStateDir(t), "web1", "./src", "/opt/app"), gotRec.LogPath)
	assert.Contains(t, errbuf.String(), "mount started in background (pid 4242)")
}

func TestRunMountDaemon_RefusesLiveDuplicate(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := testMountStateDir(t)
	pid := spawnDetachedSleep(t)
	_, err := mount.Save(dir, mount.Record{VMName: "web1", LocalPath: "./src", RemotePath: "/opt/app", PID: pid})
	require.NoError(t, err)

	orig := mountStartDaemonFn
	mountStartDaemonFn = func(string, string, []string, mount.Record) (mount.Record, error) {
		t.Fatal("must not spawn a duplicate daemon")
		return mount.Record{}, nil
	}
	t.Cleanup(func() { mountStartDaemonFn = orig })

	err = runMountDaemon(newTestUmountCmd(), "./src", "web1", "/opt/app", "", &mountFlags{debounce: 300 * time.Millisecond})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mount already active")
}

func TestMountArgValidation(t *testing.T) {
	tests := []struct {
		name     string
		arg      string
		wantRef  string
		wantPath string
		wantOk   bool
	}{
		{"valid remote", "web1:/opt/app", "web1", "/opt/app", true},
		{"vmid remote", "100:/tmp/", "100", "/tmp/", true},
		{"local path", "./src", "", "", false},
		{"absolute local", "/home/user/src", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, path, isRemote := parseRemoteArg(tt.arg)
			assert.Equal(t, tt.wantRef, ref)
			assert.Equal(t, tt.wantPath, path)
			assert.Equal(t, tt.wantOk, isRemote)
		})
	}
}

func TestMountSourceValidation(t *testing.T) {
	t.Run("nonexistent source", func(t *testing.T) {
		cmd := newMountCmd()
		cmd.SetArgs([]string{"/nonexistent/path/that/does/not/exist", "web1:/opt/app"})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("source is a file", func(t *testing.T) {
		tmp := t.TempDir()
		f := filepath.Join(tmp, "testfile")
		require.NoError(t, os.WriteFile(f, []byte("test"), 0o644))

		cmd := newMountCmd()
		cmd.SetArgs([]string{f, "web1:/opt/app"})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a directory")
	})
}

func TestDebounceDefault(t *testing.T) {
	cmd := newMountCmd()
	d, err := cmd.Flags().GetDuration("debounce")
	require.NoError(t, err)
	assert.Equal(t, 300*time.Millisecond, d)
}

func TestDefaultMountExcludes(t *testing.T) {
	expected := []string{
		".git", ".venv", ".terraform", ".terraform.*",
		"node_modules", "__pycache__", ".DS_Store",
		"*.swp", "*.swo", "*~",
	}
	assert.Equal(t, expected, defaultMountExcludes)
}

// --- Optional-target picker coverage ---

func newTestUmountCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "umount"}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())
	return cmd
}

// Task 3.1: `runMount` with a bare remote path routes through the
// picker helper and forwards the raw arg as the remote path. The
// returned ref must be the picked VM's canonical *name* so log
// lines, PID files, and daemon child args match the identifier an
// explicit `<name>:<path>` invocation would use.
func TestMountResolveDest_BareArgInvokesPicker(t *testing.T) {
	called := false
	orig := vmPickFn
	vmPickFn = func(context.Context, *pveclient.Client) (*vm.Ref, error) {
		called = true
		return &vm.Ref{VMID: 104, Name: "web1"}, nil
	}
	t.Cleanup(func() { vmPickFn = orig })

	ref, remotePath, err := mountResolveDestFn(context.Background(), nil, io.Discard, "/opt/app")
	require.NoError(t, err)
	assert.True(t, called, "picker must run for bare remote path")
	assert.Equal(t, "web1", ref, "picker must return VM name, not vmid")
	assert.Equal(t, "/opt/app", remotePath)
}

// Task 3.2: `runMount` with an explicit <name>:<path> must not consult
// the picker at all.
func TestMountResolveDest_ExplicitArgBypassesPicker(t *testing.T) {
	orig := vmPickFn
	vmPickFn = func(context.Context, *pveclient.Client) (*vm.Ref, error) {
		t.Fatalf("picker must not run for explicit <name>:<path>")
		return nil, nil
	}
	t.Cleanup(func() { vmPickFn = orig })

	ref, remotePath, err := mountResolveDestFn(context.Background(), nil, io.Discard, "web1:/opt/app")
	require.NoError(t, err)
	assert.Equal(t, "web1", ref)
	assert.Equal(t, "/opt/app", remotePath)
}

// Task 3.3: zero-arg `pmox umount` runs the picker, then delegates to
// the umountAll code path for the resolved VM. When nothing matches,
// it reports friendly info to stderr and exits 0 (not an error).
func TestRunUmount_ZeroArgs_PickerThenUmountAll(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	called := false
	orig := umountResolveVMFn
	umountResolveVMFn = func(*cobra.Command) (string, error) {
		called = true
		return "web1", nil
	}
	t.Cleanup(func() { umountResolveVMFn = orig })

	require.NoError(t, os.MkdirAll(testMountStateDir(t), 0o700))

	cmd := newTestUmountCmd()
	var errbuf bytes.Buffer
	cmd.SetErr(&errbuf)

	err := runUmount(cmd, nil, false)
	require.NoError(t, err, "zero-arg umount with no active mounts is not an error")
	assert.True(t, called, "picker must run for zero-arg umount")
	assert.Contains(t, errbuf.String(), "No active mounts for web1")
}

// Task 3.4: explicit `pmox umount web1:/opt/app` still routes to
// umountByRemote — recognizable by its "no mount found for <vm>:<path>"
// error message — and must not consult the picker.
func TestRunUmount_ExplicitRemote_RoutesToUmountByRemote(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	orig := umountResolveVMFn
	umountResolveVMFn = func(*cobra.Command) (string, error) {
		t.Fatalf("picker must not run when an explicit arg is supplied")
		return "", nil
	}
	t.Cleanup(func() { umountResolveVMFn = orig })

	require.NoError(t, os.MkdirAll(testMountStateDir(t), 0o700))

	err := runUmount(newTestUmountCmd(), []string{"web1:/opt/app"}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no mount found for web1:/opt/app")
}

// Task 3.5: `pmox umount --all web1` still routes to umountAll.
func TestRunUmount_AllFlag_RoutesToUmountAll(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	orig := umountResolveVMFn
	umountResolveVMFn = func(*cobra.Command) (string, error) {
		t.Fatalf("picker must not run when --all is used with an explicit VM")
		return "", nil
	}
	t.Cleanup(func() { umountResolveVMFn = orig })

	require.NoError(t, os.MkdirAll(testMountStateDir(t), 0o700))

	err := runUmount(newTestUmountCmd(), []string{"web1"}, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no mounts found for web1")
}

// Task 3.6: help-text includes the new bare-form examples for both
// mount and umount.
func TestMountUmountHelpIncludesBareExamples(t *testing.T) {
	mountHelp := newMountCmd().Long
	assert.Contains(t, mountHelp, "pmox mount ./src /opt/app", "mount --help must show bare-path example")

	umountHelp := newUmountCmd().Long
	assert.Contains(t, umountHelp, "pmox umount\n", "umount --help must show zero-arg example")
}

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// Remote paths (openspec/specs/remote-target-input): "/x" is absolute;
// "~/x" and anything else are relative to the login user's home.

// shQuote single-quotes s for a POSIX shell.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// remoteShellPath is p as a shell word on the VM, with the home
// directory resolved by the remote shell.
func remoteShellPath(p string) string {
	switch {
	case p == "" || p == "~" || p == ".":
		return `"$HOME"`
	case strings.HasPrefix(p, "/"):
		return shQuote(p)
	case strings.HasPrefix(p, "~/"):
		return `"$HOME"/` + shQuote(p[2:])
	default:
		return `"$HOME"/` + shQuote(p)
	}
}

// displayRemotePath shows p the way the user should read it: relative
// paths get their implied "~/".
func displayRemotePath(p string) string {
	switch {
	case p == "" || p == "~" || p == ".":
		return "~"
	case strings.HasPrefix(p, "/"), strings.HasPrefix(p, "~/"):
		return p
	default:
		return "~/" + p
	}
}

// dirToCheck is the directory a transfer to dest needs: dest itself when
// it ends in "/" or when wholeDir (mount/sync of a directory), else its
// parent. "" means nothing to check (home, root, current directory).
func dirToCheck(dest string, wholeDir bool, join func(string) string) string {
	d := dest
	if !strings.HasSuffix(dest, "/") && !wholeDir {
		d = join(dest)
	}
	d = strings.TrimRight(d, "/")
	switch d {
	case "", ".", "~":
		return ""
	}
	return d
}

func remoteParent(p string) string { return path.Dir(p) }
func localParent(p string) string  { return filepath.Dir(p) }

// confirmMkdirFn asks before creating a missing directory. A seam for tests.
var confirmMkdirFn = tui.Confirm

// remoteSSHFn runs a shell command on the target and returns its exit
// code and combined output. A seam for tests.
var remoteSSHFn = func(ctx context.Context, target *sshTarget, timeout time.Duration, script string) (int, string, error) {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return -1, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append([]string{"-o", "BatchMode=yes", "-o", fmt.Sprintf("ConnectTimeout=%d", int(timeout.Seconds()+0.5))}, guestHostKeyOpts()...)
	if target.Key != "" {
		args = append(args, "-i", target.Key)
	}
	args = append(args, fmt.Sprintf("%s@%s", target.User, target.IP), script)
	out, err := exec.CommandContext(ctx, sshPath, args...).CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out), nil
	}
	if err != nil {
		return -1, string(out), err
	}
	return 0, string(out), nil
}

// askToCreate decides whether a missing directory may be created: yes
// with --mkdir, asked on a terminal, an error otherwise.
func askToCreate(what string, mkdir bool) error {
	if mkdir {
		return nil
	}
	if !tui.Interactive() {
		return fmt.Errorf("%w: %s doesn't exist — create it, or pass --mkdir", exitcode.ErrUserInput, what)
	}
	ok, err := confirmMkdirFn(fmt.Sprintf("Create %s?", what), true)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %s not created", tui.ErrAborted, what)
	}
	return nil
}

// ensureRemoteDir makes sure dir exists on the target, confirming
// before creating it (sudo when its parent isn't writable).
func ensureRemoteDir(ctx context.Context, cmd *cobra.Command, target *sshTarget, dir string, mkdir bool) error {
	if dir == "" || dir == "/" {
		return nil
	}
	shown := displayRemotePath(dir)
	vmName := target.Name
	if vmName == "" {
		vmName = target.IP
	}
	sp := startSpin(fmt.Sprintf("Checking %s on %s…", shown, vmName))
	code, _, err := remoteSSHFn(ctx, target, 15*time.Second, "test -d "+remoteShellPath(dir))
	sp.Stop()
	if err != nil || code != 1 {
		// Present (0), or ssh itself failed (255): the transfer reports
		// connection problems with its own guidance.
		return nil
	}
	if err := askToCreate(fmt.Sprintf("%s on %s", shown, vmName), mkdir); err != nil {
		return err
	}
	q := remoteShellPath(dir)
	script := fmt.Sprintf(`mkdir -p -- %s 2>/dev/null || sudo -n install -d -o "$(id -un)" -g "$(id -gn)" -- %s`, q, q)
	sp = startSpin(fmt.Sprintf("Creating %s on %s…", shown, vmName))
	code, out, err := remoteSSHFn(ctx, target, 30*time.Second, script)
	sp.Stop()
	if err == nil && code == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "✓ created %s on %s\n", shown, vmName)
		return nil
	}
	detail := strings.TrimSpace(out)
	if err != nil {
		detail = err.Error()
	}
	return fmt.Errorf("could not create %s on %s (%s) — pick a path you can write to, such as ~/%s",
		shown, vmName, detail, path.Base(strings.TrimRight(dir, "/")))
}

// ensureLocalDir makes sure the local directory dir exists, confirming
// before creating it.
func ensureLocalDir(cmd *cobra.Command, dir string, mkdir bool) error {
	if dir == "" {
		return nil
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
	}
	if st, err := os.Stat(dir); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("%w: %s exists and is not a directory", exitcode.ErrUserInput, dir)
		}
		return nil
	}
	if err := askToCreate(dir, mkdir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("could not create %s: %w", dir, err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "✓ created %s\n", dir)
	return nil
}

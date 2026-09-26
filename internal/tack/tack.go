// Package tack builds and runs invocations of the tack CLI
// (github.com/tackhq/tack) against a pmox-managed VM. pmox creates VMs;
// tack configures them. All SSH parameters are passed via tack's
// connection flags — no inventory file is synthesized.
package tack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/eugenetaranov/pmox/internal/pvessh"
)

// ErrNotInstalled is returned when the tack binary is not on PATH.
var ErrNotInstalled = errors.New("tack binary not found on PATH; install tack from https://github.com/tackhq/tack")

// Options describes a single `tack run` invocation against one host.
type Options struct {
	Playbook    string   // path to the playbook (required)
	User        string   // SSH user
	IP          string   // SSH host/IP (required)
	Port        int      // SSH port; 0 = tack default (22)
	KeyPath     string   // SSH private key path; empty = tack's default resolution
	Insecure    bool     // skip host-key verification (tack --ssh-insecure)
	Check       bool     // plan only (tack --check)
	AutoApprove bool     // skip tack's interactive approval (tack --auto-approve)
	Tags        []string // --tags
	SkipTags    []string // --skip-tags
	OutputJSON  bool     // --output json
}

// Available reports whether the tack binary can be found on PATH.
func Available() error {
	if _, err := exec.LookPath("tack"); err != nil {
		return ErrNotInstalled
	}
	return nil
}

// Args builds the argument vector for tack (excluding the leading "tack"
// program name), so it can be unit-tested independently of execution.
func Args(o Options) []string {
	args := []string{"run", o.Playbook}

	conn := "ssh://"
	if o.User != "" {
		conn += o.User + "@"
	}
	conn += o.IP
	if o.Port != 0 {
		conn += ":" + strconv.Itoa(o.Port)
	}
	args = append(args, "-c", conn)

	if o.KeyPath != "" {
		args = append(args, "--ssh-key", o.KeyPath)
	}
	if o.Insecure {
		args = append(args, "--ssh-insecure")
	}
	if o.Check {
		args = append(args, "--check")
	}
	if o.AutoApprove {
		args = append(args, "--auto-approve")
	}
	if len(o.Tags) > 0 {
		args = append(args, "--tags", strings.Join(o.Tags, ","))
	}
	if len(o.SkipTags) > 0 {
		args = append(args, "--skip-tags", strings.Join(o.SkipTags, ","))
	}
	if o.OutputJSON {
		args = append(args, "--output", "json")
	}
	return args
}

// waitDelay caps how long Wait blocks after tack exits (or ctx is
// cancelled) while a descendant still holds stdout/stderr open, so a
// backgrounded child can't hang pmox.
const waitDelay = 500 * time.Millisecond

// Command validates o and returns a ready-to-run `tack run` command with
// the process environment inherited and WaitDelay set. Callers wire
// stdio. It returns ErrNotInstalled if tack is absent.
func Command(ctx context.Context, o Options) (*exec.Cmd, error) {
	if err := Available(); err != nil {
		return nil, err
	}
	if o.Playbook == "" {
		return nil, fmt.Errorf("tack: no playbook specified")
	}
	if o.IP == "" {
		return nil, fmt.Errorf("tack: no target host specified")
	}
	cmd := exec.CommandContext(ctx, "tack", Args(o)...)
	// Every pmox-managed VM has passwordless sudo (the cloud-init
	// template sets "sudo: ALL=(ALL) NOPASSWD:ALL" for its user) and
	// key-based SSH (pmox always supplies KeyPath), so tack should
	// never need to prompt for either password — and several callers
	// (the --tack launch/clone hook, --output json) don't even wire a
	// terminal for tack to prompt on, so an unexpected prompt just hangs
	// forever. Set via env, not a flag: the right shape for anything
	// with a value too, so a real secret never lands in argv (visible
	// to any local user via ps) — these two happen to be plain
	// booleans, but the convention should hold regardless.
	cmd.Env = append(os.Environ(), "TACK_SUDO_NO_PROMPT=1", "TACK_SSH_NO_PROMPT=1")
	cmd.WaitDelay = waitDelay
	return cmd, nil
}

// Run executes `tack run` with the given options, wiring stdio through so
// tack's own plan/apply confirmation is visible to (and driven by) the
// user. It returns ErrNotInstalled if tack is absent.
func Run(ctx context.Context, o Options, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd, err := Command(ctx, o)
	if err != nil {
		return err
	}
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// pinHostKeyTimeout bounds PinHostKey's own dial+handshake, independent
// of ctx's deadline (callers — apply, the --tack hook — pass a
// long-lived, deadline-less command context). Without this, an
// unreachable VM would hang the pre-flight probe indefinitely before
// tack itself ever gets a chance to fail with its own, more specific
// connection error.
const pinHostKeyTimeout = 10 * time.Second

// PinHostKey pins ip's SSH host key into ~/.ssh/known_hosts if it isn't
// already there, with no prompt (TOFU) — a no-op once the host is
// known. tack's own SSH client always reads that fixed file and has no
// equivalent of ssh's -o UserKnownHostsFile, so this is the only way to
// give a VM's first tack connection the same trust-on-first-connect
// treatment every other pmox SSH command already applies via its own,
// separately-managed known_hosts. insecure skips pinning entirely —
// tack's own --ssh-insecure (wired from the same flag) already disables
// its host-key verification, so pinning would be pointless. Callers
// should treat a non-nil error as best-effort: fall through to tack's
// own connection attempt rather than failing outright.
func PinHostKey(ctx context.Context, ip string, insecure bool) (pinned bool, err error) {
	if insecure {
		return false, nil
	}
	path, err := pvessh.DefaultKnownHostsPath()
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, pinHostKeyTimeout)
	defer cancel()
	return pvessh.EnsureHostKeyKnown(ctx, ip, path)
}

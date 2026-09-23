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

// Run executes `tack run` with the given options, wiring stdio through so
// tack's own plan/apply confirmation is visible to (and driven by) the
// user. It returns ErrNotInstalled if tack is absent.
func Run(ctx context.Context, o Options, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := Available(); err != nil {
		return err
	}
	if o.Playbook == "" {
		return fmt.Errorf("tack: no playbook specified")
	}
	if o.IP == "" {
		return fmt.Errorf("tack: no target host specified")
	}
	cmd := exec.CommandContext(ctx, "tack", Args(o)...)
	cmd.Env = os.Environ()
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

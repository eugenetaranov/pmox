// Package hook implements the post-SSH-ready hook phase of pmox launch
// and clone. A Hook runs after wait-SSH succeeds and hands off to an
// external tool (a user script, tack, ansible) to do post-boot work.
package hook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// waitDelay caps how long Wait blocks after the process exits (or ctx
// cancel fires) when a descendant still holds stdout/stderr open.
// Without it, a hook that backgrounds a child keeps pmox hanging.
const waitDelay = 500 * time.Millisecond

// Env is the set of values a hook receives about the just-launched VM.
// It is locked by spec and exposed to shell hooks as PMOX_* env vars.
type Env struct {
	IP     string
	Name   string
	User   string
	Node   string
	VMID   int
	SSHKey string
}

// Hook is the interface the launch state machine calls after wait-SSH.
// Implementations must be safe to call at most once per launch.
type Hook interface {
	Name() string
	Run(ctx context.Context, env Env, stdout, stderr io.Writer) error
}

// setenv returns os.Environ() augmented with the PMOX_* variables the
// post-create script contract guarantees.
func setenv(env Env) []string {
	out := os.Environ()
	out = append(out,
		"PMOX_IP="+env.IP,
		"PMOX_VMID="+strconv.Itoa(env.VMID),
		"PMOX_NAME="+env.Name,
		"PMOX_USER="+env.User,
		"PMOX_NODE="+env.Node,
	)
	return out
}

// PostCreateHook invokes a user-supplied executable directly (no shell
// wrapper) with PMOX_* env vars. The script's own shebang handles
// interpretation; pmox does not quote, split, or pipe anything.
type PostCreateHook struct {
	Path string
}

func (h *PostCreateHook) Name() string { return "post-create" }

func (h *PostCreateHook) Run(ctx context.Context, env Env, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, h.Path)
	cmd.Env = setenv(env)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = waitDelay
	return cmd.Run()
}

// TackHook runs `tack apply --host <ip> --user <user> <config>`.
type TackHook struct {
	ConfigPath string
}

func (h *TackHook) Name() string { return "tack" }

func (h *TackHook) Run(ctx context.Context, env Env, stdout, stderr io.Writer) error {
	if _, err := exec.LookPath("tack"); err != nil {
		return errors.New("tack binary not found on PATH. install tack from https://github.com/tackhq/tack or pass --post-create instead")
	}
	cmd := exec.CommandContext(ctx, "tack", "apply",
		"--host", env.IP,
		"--user", env.User,
		h.ConfigPath,
	)
	cmd.Env = os.Environ()
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = waitDelay
	return cmd.Run()
}

// AnsibleHook runs `ansible-playbook` against the new VM using an
// inline one-host inventory.
type AnsibleHook struct {
	PlaybookPath string
}

func (h *AnsibleHook) Name() string { return "ansible" }

func (h *AnsibleHook) Run(ctx context.Context, env Env, stdout, stderr io.Writer) error {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		return errors.New("ansible-playbook binary not found on PATH. install ansible or pass --post-create instead")
	}
	// The trailing comma after the IP is required: `-i host,` is
	// Ansible's inline-inventory syntax for a single host. Without
	// the comma, Ansible treats the value as an inventory file path.
	cmd := exec.CommandContext(ctx, "ansible-playbook",
		"-i", env.IP+",",
		"-u", env.User,
		"--private-key", env.SSHKey,
		"-e", fmt.Sprintf("pmox_vmid=%d", env.VMID),
		"-e", "pmox_name="+env.Name,
		h.PlaybookPath,
	)
	cmd.Env = os.Environ()
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = waitDelay
	return cmd.Run()
}

package steps

import (
	"bytes"
	"context"
	"os"
	"os/exec"
)

// OSExec runs commands on this machine. AsUser commands run through
// runuser as User, from their home, with mise's shims first on PATH.
type OSExec struct{ User string }

// UserShell is how AsUser commands start: cd home, mise shims on PATH,
// then the command and its arguments as given.
const UserShell = `cd && PATH="$HOME/.local/share/mise/shims:$PATH" && exec "$@"`

// Run runs c and returns its combined output.
func (x OSExec) Run(ctx context.Context, c Cmd) ([]byte, error) {
	name, args := c.Name, c.Args
	if c.AsUser {
		name, args = "runuser", append([]string{"-u", x.User, "--", "sh", "-c", UserShell, "_", c.Name}, c.Args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), c.Env...)
	if c.Stdin != nil {
		cmd.Stdin = bytes.NewReader(c.Stdin)
	}
	return cmd.CombinedOutput()
}

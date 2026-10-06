package vmwait

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// nologinPath is created by systemd early in boot and removed by
// systemd-user-sessions once logins are allowed. While it exists, sshd
// answers (so WaitForSSH succeeds) but pam_nologin rejects every
// non-root login: "System is booting up. Unprivileged users are not
// permitted to log in yet."
const nologinPath = "/run/nologin"

// FileReader is the guest-agent file read WaitForBoot needs
// (pveclient.Client).
type FileReader interface {
	AgentFileRead(ctx context.Context, node string, vmid int, path string) ([]byte, bool, error)
}

// WaitForBoot waits until the guest accepts logins, i.e. /run/nologin is
// gone, checking through the guest agent (no SSH login needed). It is
// best-effort: when the agent can't answer file reads (missing
// privilege, agent not running, non-systemd guest) it returns
// (false, nil) at once and callers rely on the SSH probe alone. waited
// reports whether the guest was still booting at the first check. On
// timeout it returns an error wrapping context.DeadlineExceeded.
func WaitForBoot(ctx context.Context, r FileReader, node string, vmid int, timeout time.Duration, opts ...Option) (waited bool, err error) {
	cfg := newConfig(opts)
	deadline := time.Now().Add(timeout)
	for first := true; ; first = false {
		_, _, err := r.AgentFileRead(ctx, node, vmid, nologinPath)
		switch {
		case err == nil:
			// Still booting.
			if first {
				waited = true
			}
		case strings.Contains(strings.ToLower(err.Error()), "no such file"):
			return waited, nil
		default:
			return false, nil // can't tell; don't block
		}
		if time.Now().After(deadline) {
			return waited, fmt.Errorf("wait for boot to finish: %w", context.DeadlineExceeded)
		}
		select {
		case <-ctx.Done():
			return waited, ctx.Err()
		case <-time.After(cfg.pollInterval):
		}
	}
}

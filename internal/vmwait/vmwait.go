// Package vmwait waits for a freshly started VM to become usable: the
// qemu-guest-agent reporting an IPv4 (WaitForIP) and sshd answering a
// handshake (WaitForSSH). It also owns the IP-picking heuristic
// (PickIPv4) shared by launch, list, ssh, ssh-config and cleanup.
package vmwait

import "time"

// DefaultPollInterval is the guest-agent poll period used by WaitForIP.
// WaitForSSH derives its backoff from the same value: it starts at half
// the interval and doubles up to five times the interval.
const DefaultPollInterval = 1 * time.Second

// Option tunes WaitForIP / WaitForSSH. Production callers pass none.
type Option func(*config)

type config struct {
	pollInterval time.Duration
	sshPort      string
}

// WithPollInterval overrides DefaultPollInterval. Tests use it to keep
// the wait loops fast.
func WithPollInterval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.pollInterval = d
		}
	}
}

// WithSSHPort overrides the port WaitForSSH dials (default "22"). Tests
// use it to target a fake sshd on an ephemeral port.
func WithSSHPort(port string) Option {
	return func(c *config) {
		if port != "" {
			c.sshPort = port
		}
	}
}

func newConfig(opts []Option) config {
	c := config{pollInterval: DefaultPollInterval, sshPort: "22"}
	for _, o := range opts {
		o(&c)
	}
	return c
}

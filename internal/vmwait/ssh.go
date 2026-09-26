package vmwait

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// WaitForSSH dials `<ip>:22` with exponential backoff and runs an SSH
// handshake. It returns nil as soon as sshd answers the banner — even
// if authentication would fail — because "banner exchange completed"
// is what we actually care about for reachability.
//
// TCP-only reachability is not enough: sshd binds the port before it
// finishes generating host keys, so a bare TCP dial can succeed against
// a server that will immediately slam the connection shut. Running the
// handshake proves sshd is actually ready to serve.
//
// Each attempt's dial and handshake are bounded by the overall timeout
// and by ctx. On timeout the error wraps pveclient.ErrTimeout and the
// last dial/handshake failure.
func WaitForSSH(ctx context.Context, ip string, timeout time.Duration, opts ...Option) error {
	cfg := newConfig(opts)
	deadline := time.Now().Add(timeout)
	backoff := cfg.pollInterval / 2
	maxBackoff := 5 * cfg.pollInterval
	addr := net.JoinHostPort(ip, cfg.sshPort)
	var lastErr error
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("wait for ssh on %s: %w", ip, ctx.Err())
		}
		if time.Now().After(deadline) {
			if lastErr == nil {
				return fmt.Errorf("wait for ssh on %s: %w", ip, pveclient.ErrTimeout)
			}
			return fmt.Errorf("wait for ssh on %s: %w: %w", ip, pveclient.ErrTimeout, lastErr)
		}

		if err := probeSSH(ctx, addr, deadline); err != nil {
			lastErr = err
		} else {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for ssh on %s: %w", ip, ctx.Err())
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// probeSSH makes one dial+handshake attempt against addr. It returns
// nil when sshd answered the banner. The dial is capped at 2s and the
// handshake at 5s, both clipped to deadline and cancelled with ctx.
func probeSSH(ctx context.Context, addr string, deadline time.Time) error {
	dialTimeout := min(2*time.Second, time.Until(deadline))
	if dialTimeout <= 0 {
		return context.DeadlineExceeded
	}
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	hsDeadline := time.Now().Add(5 * time.Second)
	if deadline.Before(hsDeadline) {
		hsDeadline = deadline
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	hsErr := sshHandshake(conn, hsDeadline)
	if err := ctx.Err(); err != nil {
		return err
	}
	if hsErr == nil || handshakeMeansReady(hsErr) {
		return nil
	}
	return hsErr
}

// sshHandshake runs the client side of the SSH handshake over an
// already-established TCP connection, with an I/O deadline. The
// connection is always closed before the function returns.
func sshHandshake(conn net.Conn, deadline time.Time) error {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(deadline)
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, conn.RemoteAddr().String(), &ssh.ClientConfig{
		User:            "pmox",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return err
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		for ch := range chans {
			_ = ch.Reject(ssh.Prohibited, "pmox probe")
		}
	}()
	_ = sshConn.Close()
	return nil
}

// handshakeMeansReady reports whether an ssh.NewClientConn error
// actually indicates that sshd answered the banner. Auth-style errors
// mean the server negotiated protocol version and key exchange with
// us — it's ready. Transport-level errors (EOF, reset) mean sshd is
// still warming up, so the caller should retry — and so does a
// server-initiated disconnect (e.g. PAM's pam_nologin during boot),
// which looks superficially like an auth-style error but means the
// opposite: sshd refused the connection outright.
func handshakeMeansReady(err error) bool {
	if err == nil {
		return true
	}
	// x/crypto/ssh wraps transport errors as "ssh: handshake failed:
	// %w", so the "ssh:" prefix check below would misread an I/O
	// timeout (host accepts TCP but never sends a banner) or our own
	// ctx-triggered close as ready. Rule those out first.
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrDeadlineExceeded) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "connection reset") {
		return false
	}
	// A server-initiated disconnect (x/crypto/ssh formats this as
	// "ssh: disconnect, reason N: ...") means sshd actively refused the
	// connection during our auth-less probe — most concretely PAM's
	// pam_nologin ("System is booting up. Unprivileged users are not
	// permitted to log in yet.") sent as the disconnect message before
	// the "none" method even gets a normal auth-failure response. That
	// is the opposite of ready, and must be checked before the generic
	// "ssh:" prefix match below, which would otherwise treat it the
	// same as a real auth failure and declare the guest ready to log
	// into while it's still finishing boot.
	if strings.HasPrefix(msg, "ssh: disconnect,") {
		return false
	}
	// Any other error surfaced with the "ssh:" prefix (including
	// "unable to authenticate" / "no supported methods") means the
	// client successfully parsed the server's banner.
	if strings.Contains(msg, "ssh:") ||
		strings.Contains(msg, "unable to authenticate") ||
		strings.Contains(msg, "no supported methods") {
		return true
	}
	return false
}

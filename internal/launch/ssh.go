package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
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
func WaitForSSH(ctx context.Context, ip string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	backoff := 500 * time.Millisecond
	addr := net.JoinHostPort(ip, "22")
	var lastErr error
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("wait for ssh on %s: %w", ip, ctx.Err())
		}
		if time.Now().After(deadline) {
			if lastErr == nil {
				lastErr = context.DeadlineExceeded
			}
			return fmt.Errorf("wait for ssh on %s: %w", ip, lastErr)
		}

		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			lastErr = err
		} else {
			hsErr := sshHandshake(conn)
			if hsErr == nil || handshakeMeansReady(hsErr) {
				return nil
			}
			lastErr = hsErr
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for ssh on %s: %w", ip, ctx.Err())
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 5*time.Second {
			backoff = 5 * time.Second
		}
	}
}

// sshHandshake runs the client side of the SSH handshake over an
// already-established TCP connection. The connection is always closed
// before the function returns.
func sshHandshake(conn net.Conn) error {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, conn.RemoteAddr().String(), &ssh.ClientConfig{
		User:            "pmox",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
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
// still warming up, so the caller should retry.
func handshakeMeansReady(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "connection reset") {
		return false
	}
	// Any error surfaced with the "ssh:" prefix (including
	// "unable to authenticate" / "no supported methods") means the
	// client successfully parsed the server's banner.
	if strings.Contains(msg, "ssh:") ||
		strings.Contains(msg, "unable to authenticate") ||
		strings.Contains(msg, "no supported methods") {
		return true
	}
	return false
}

package pvessh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// ExitError is a command that ran on the node and exited non-zero.
type ExitError struct {
	Command string
	Code    int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%q exited with status %d", e.Command, e.Code)
}

// Run runs command on the node in a new session, without a terminal:
// stdin (may be nil) is copied to it, and its output streams to stdout
// and stderr (nil discards) as it arrives. A non-zero exit returns an
// *ExitError. Cancelling ctx closes the session.
func (c *Client) Run(ctx context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.ssh == nil {
		return errors.New("pvessh: not connected")
	}
	sess, err := c.ssh.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session on %s: %w", c.host, err)
	}
	defer func() { _ = sess.Close() }()
	sess.Stdin = stdin
	sess.Stdout, sess.Stderr = orDiscard(stdout), orDiscard(stderr)
	if stdout != nil && stdout == stderr {
		// The two streams are copied concurrently; one writer (e.g. a
		// bytes.Buffer, whose ReadFrom isn't safe for concurrent use)
		// would lose output. Serialize them.
		w := &lockedWriter{w: stdout}
		sess.Stdout, sess.Stderr = w, w
	}

	done := make(chan error, 1)
	go func() { done <- sess.Run(command) }()
	select {
	case <-ctx.Done():
		_ = sess.Close()
		return ctx.Err()
	case err := <-done:
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			return &ExitError{Command: command, Code: exit.ExitStatus()}
		}
		if err != nil {
			return fmt.Errorf("run on %s: %w", c.host, err)
		}
		return nil
	}
}

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// lockedWriter serializes writes from the stdout and stderr copiers. It
// has only Write, so io.Copy never uses the target's ReadFrom.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

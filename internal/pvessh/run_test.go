package pvessh

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func dialRunServer(t *testing.T) *Client {
	t.Helper()
	srv := newTestServer(t)
	srv.start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, Config{Host: srv.addr, User: "root", Password: srv.validPassword, KnownHosts: srv.writeKnownHosts(t)})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestRun_StdinReachesCommand(t *testing.T) {
	c := dialRunServer(t)
	var out bytes.Buffer
	if err := c.Run(context.Background(), "cat", strings.NewReader("tskey-abc"), &out, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.String() != "tskey-abc" {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestRun_ExitCode(t *testing.T) {
	c := dialRunServer(t)
	var errOut bytes.Buffer
	err := c.Run(context.Background(), "exit 3", nil, nil, &errOut)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 3 {
		t.Fatalf("err = %v, want ExitError code 3", err)
	}
	if errOut.String() != "bye\n" {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestRun_Cancel(t *testing.T) {
	c := dialRunServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.Run(ctx, "sleep", nil, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("Run took %s after cancel", time.Since(start))
	}
}

// One writer for both streams must get both: the copiers run
// concurrently, and a shared bytes.Buffer used to lose output.
func TestRun_SameWriterForBothStreams(t *testing.T) {
	c := dialRunServer(t)
	for i := 0; i < 20; i++ {
		var out bytes.Buffer
		if err := c.Run(context.Background(), "both", nil, &out, &out); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !strings.Contains(out.String(), "to-stdout") || !strings.Contains(out.String(), "to-stderr") {
			t.Fatalf("run %d: output = %q, want both streams", i, out.String())
		}
	}
}

package pvessh

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// silentListener accepts TCP connections and never writes a byte, like
// a host whose sshd is wedged or a port-forward to nowhere. Accepted
// conns are held open until the test ends.
func silentListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var conns []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return ln.Addr().String()
}

func TestDial_SilentServerHonorsContextDeadline(t *testing.T) {
	addr := silentListener(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Dial(ctx, Config{Host: addr, User: "root", Password: "hunter2-unused", Insecure: true})
	if err == nil {
		t.Fatal("Dial against silent server returned nil error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "timeout") {
		t.Errorf("err = %v, want deadline/timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Dial took %v; handshake did not honor the ctx deadline", elapsed)
	}
}

func TestDial_SilentServerHonorsCancel(t *testing.T) {
	addr := silentListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	start := time.Now()
	_, err := Dial(ctx, Config{Host: addr, User: "root", Password: "hunter2-unused", Insecure: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Dial took %v; handshake ignored ctx cancellation", elapsed)
	}
}

func TestClientHandshake_ConfigTimeoutWithoutCtxDeadline(t *testing.T) {
	addr := silentListener(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	start := time.Now()
	_, _, _, err = clientHandshake(context.Background(), conn, addr, &ssh.ClientConfig{
		User:            "root",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         200 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("handshake against silent server returned nil error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("handshake took %v; ClientConfig.Timeout not applied", elapsed)
	}
}

func TestPromptAndPinHostKey_SilentServerHonorsCancel(t *testing.T) {
	addr := silentListener(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	kh := filepath.Join(t.TempDir(), "known_hosts")
	err := PromptAndPinHostKey(ctx, addr, &strings.Builder{}, strings.NewReader("yes\n"), kh)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

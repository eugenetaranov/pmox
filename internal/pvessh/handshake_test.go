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

// TestEnsureHostKeyKnown covers the TOFU (no-prompt) pinning path used
// for callers — currently just tack — that read a fixed known_hosts
// file with no way to point at pmox's own: pin once on an unknown host,
// then no-op (no second dial's worth of side effects to check for, but
// pinned must report false) once it's known.
func TestEnsureHostKeyKnown(t *testing.T) {
	srv := newTestServer(t)
	srv.start(t)
	kh := filepath.Join(t.TempDir(), "known_hosts")

	pinned, err := EnsureHostKeyKnown(context.Background(), srv.addr, kh)
	if err != nil {
		t.Fatalf("EnsureHostKeyKnown (first call): %v", err)
	}
	if !pinned {
		t.Error("pinned = false on first call, want true (host was unknown)")
	}
	has, err := KnownHostsHas(kh, srv.addr)
	if err != nil || !has {
		t.Errorf("KnownHostsHas after pinning: has=%v err=%v, want true/nil", has, err)
	}

	pinned, err = EnsureHostKeyKnown(context.Background(), srv.addr, kh)
	if err != nil {
		t.Fatalf("EnsureHostKeyKnown (second call): %v", err)
	}
	if pinned {
		t.Error("pinned = true on second call, want false (host already known)")
	}
}

func TestEnsureHostKeyKnown_SilentServerHonorsCancel(t *testing.T) {
	addr := silentListener(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	kh := filepath.Join(t.TempDir(), "known_hosts")
	if _, err := EnsureHostKeyKnown(ctx, addr, kh); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestDefaultKnownHostsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := DefaultKnownHostsPath()
	if err != nil {
		t.Fatalf("DefaultKnownHostsPath: %v", err)
	}
	if want := filepath.Join(home, ".ssh", "known_hosts"); got != want {
		t.Errorf("DefaultKnownHostsPath() = %q, want %q", got, want)
	}
}

// TestFetchHostKeyThenAppend covers the split the interactive init
// wizard uses: fetch (no trust), show the fingerprint, then pin.
func TestFetchHostKeyThenAppend(t *testing.T) {
	srv := newTestServer(t)
	srv.start(t)
	kh := filepath.Join(t.TempDir(), "known_hosts")

	k, err := FetchHostKey(context.Background(), srv.addr)
	if err != nil {
		t.Fatalf("FetchHostKey: %v", err)
	}
	if !strings.HasPrefix(k.Fingerprint, "SHA256:") || k.Type == "" {
		t.Errorf("got type=%q fingerprint=%q, want a type and SHA256 fingerprint", k.Type, k.Fingerprint)
	}
	if has, _ := KnownHostsHas(kh, srv.addr); has {
		t.Fatal("FetchHostKey must not pin anything")
	}
	if err := AppendKnownHost(kh, k); err != nil {
		t.Fatalf("AppendKnownHost: %v", err)
	}
	if has, err := KnownHostsHas(kh, srv.addr); err != nil || !has {
		t.Errorf("KnownHostsHas after AppendKnownHost: has=%v err=%v, want true/nil", has, err)
	}
}

func TestAppendKnownHostRejectsUnfetchedKey(t *testing.T) {
	kh := filepath.Join(t.TempDir(), "known_hosts")
	if err := AppendKnownHost(kh, HostKey{Host: "h:22"}); err == nil {
		t.Fatal("AppendKnownHost with a zero HostKey returned nil error")
	}
}

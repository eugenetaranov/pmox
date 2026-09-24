package vmwait

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// startFakeSSHServer accepts one connection on 127.0.0.1, runs the
// server half of an SSH handshake with a throwaway ed25519 host key,
// then closes the connection. Returns the listener address.
func startFakeSSHServer(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("sign host key: %v", err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
		if err != nil {
			_ = conn.Close()
			return
		}
		go ssh.DiscardRequests(reqs)
		go func() {
			for ch := range chans {
				_ = ch.Reject(ssh.Prohibited, "test")
			}
		}()
		_ = sconn.Close()
	}()
	return ln.Addr().String()
}

func TestWaitForSSH_Timeout(t *testing.T) {
	// Use TEST-NET-1 (RFC 5737) so dial never connects — ECONNREFUSED
	// to localhost could accidentally hit a real sshd on CI runners.
	start := time.Now()
	err := WaitForSSH(context.Background(), "192.0.2.1", 800*time.Millisecond, WithPollInterval(50*time.Millisecond))
	if err == nil {
		t.Fatal("WaitForSSH err=nil, want timeout error")
	}
	if !strings.Contains(err.Error(), "wait for ssh on 192.0.2.1") {
		t.Errorf("err = %v, want wrapped with wait for ssh on 192.0.2.1", err)
	}
	if !errors.Is(err, pveclient.ErrTimeout) {
		t.Errorf("err = %v, want wrapped pveclient.ErrTimeout", err)
	}
	// The per-attempt 2s dial timeout must be clipped to the budget.
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Errorf("WaitForSSH took %v, want ~800ms budget honored", elapsed)
	}
}

// sshd answering the banner counts as ready even though the probe's
// auth would fail.
func TestWaitForSSH_HandshakeSuccess(t *testing.T) {
	addr := startFakeSSHServer(t)
	host, port, _ := net.SplitHostPort(addr)
	err := WaitForSSH(context.Background(), host, 5*time.Second, WithSSHPort(port), WithPollInterval(10*time.Millisecond))
	if err != nil {
		t.Fatalf("WaitForSSH err = %v, want nil against fake sshd", err)
	}
}

// A host that accepts TCP but never sends an SSH banner must not hang
// WaitForSSH past ctx cancellation.
func TestWaitForSSH_SilentServerHonorsCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var held []net.Conn
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-accepted
		for _, c := range held {
			_ = c.Close()
		}
	})
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	// A handshake I/O timeout is not "ready", even though x/crypto/ssh
	// reports it with an "ssh:" prefix.
	err = WaitForSSH(context.Background(), host, 300*time.Millisecond, WithSSHPort(port), WithPollInterval(10*time.Millisecond))
	if !errors.Is(err, pveclient.ErrTimeout) {
		t.Fatalf("err = %v, want pveclient.ErrTimeout against silent server", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	start := time.Now()
	err = WaitForSSH(ctx, host, 30*time.Second, WithSSHPort(port))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("WaitForSSH took %v after cancel; handshake ignored ctx", elapsed)
	}
}

// TestWaitForSSH_TCPOnlyNotEnough: a plain TCP listener that accepts
// and immediately closes is *not* ready by our definition — sshd
// hasn't answered the banner. sshHandshake returns io.EOF which
// handshakeMeansReady treats as "retry". Exercise the two helpers
// directly and assert the retry classification.
func TestWaitForSSH_TCPOnlyNotEnough(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	hsErr := sshHandshake(conn, time.Now().Add(5*time.Second))
	if hsErr == nil {
		t.Fatal("sshHandshake err=nil against TCP-only listener, want transport error")
	}
	if handshakeMeansReady(hsErr) {
		t.Errorf("handshakeMeansReady(%v) = true, want false for io.EOF / transport close", hsErr)
	}
}

package launch

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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

func TestWaitForSSH_HandshakeSuccess(t *testing.T) {
	addr := startFakeSSHServer(t)
	// WaitForSSH hardcodes :22, so exercise the two helpers it uses
	// directly against the fake server's random port. This covers the
	// same "banner exchange = ready" behavior WaitForSSH relies on.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("handshake never succeeded against %s", addr)
		}
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		hsErr := sshHandshake(conn)
		if hsErr == nil || handshakeMeansReady(hsErr) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWaitForSSH_Timeout(t *testing.T) {
	// Use TEST-NET-1 (RFC 5737) so dial never connects — ECONNREFUSED
	// to localhost could accidentally hit a real sshd on CI runners.
	err := WaitForSSH(context.Background(), "192.0.2.1", 800*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForSSH err=nil, want timeout error")
	}
	if !strings.Contains(err.Error(), "wait for ssh on 192.0.2.1") {
		t.Errorf("err = %v, want wrapped with wait for ssh on 192.0.2.1", err)
	}
}

// TestWaitForSSH_TCPOnlyNotEnough: a plain TCP listener that accepts
// and immediately closes is *not* ready by our definition — sshd
// hasn't answered the banner. sshHandshake returns io.EOF which
// handshakeMeansReady treats as "retry", so a 1s timeout should
// trip. We can't actually use WaitForSSH (hardcoded :22), so we
// exercise the two helpers directly and assert the retry logic.
func TestWaitForSSH_TCPOnlyNotEnough(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
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
	hsErr := sshHandshake(conn)
	if hsErr == nil {
		t.Fatal("sshHandshake err=nil against TCP-only listener, want transport error")
	}
	if handshakeMeansReady(hsErr) {
		t.Errorf("handshakeMeansReady(%v) = true, want false for io.EOF / transport close", hsErr)
	}
}

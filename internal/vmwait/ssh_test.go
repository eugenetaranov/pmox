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

// TestHandshakeMeansReady_ServerDisconnectDuringBoot guards a real
// false-positive: sshd sends a raw SSH_MSG_DISCONNECT (x/crypto/ssh
// formats this as "ssh: disconnect, reason N: ...") instead of a normal
// USERAUTH_FAILURE when PAM's pam_nologin rejects the very first
// ("none") auth probe during boot — the client-observed symptom is the
// "System is booting up..." banner followed by "Connection closed".
// Before this fix, the generic "ssh:"-prefix match below classified
// this the same as a real auth failure (server negotiated fine, just
// rejected our credentials) and declared the guest ready — so
// 'pmox launch' printed success and handed off to a VM that then
// refused 'pmox shell' moments later with the exact same nologin
// message. A disconnect must be classified as "retry", not "ready".
func TestHandshakeMeansReady_ServerDisconnectDuringBoot(t *testing.T) {
	nologin := errors.New(`ssh: disconnect, reason 11: System is booting up. Unprivileged users are not permitted to log in yet. Please come back later. For technical details, see pam_nologin(8).`)
	if handshakeMeansReady(nologin) {
		t.Errorf("handshakeMeansReady(%v) = true, want false — a server disconnect is not readiness", nologin)
	}

	// A genuine auth-style rejection (the server engaged normally and
	// just doesn't accept our probe's lack of credentials) must still
	// count as ready — this is the behavior TestWaitForSSH_HandshakeSuccess
	// and the doc comment describe; confirm the fix didn't overcorrect.
	authFailure := errors.New("ssh: unable to authenticate, attempted methods [none], no supported methods remain")
	if !handshakeMeansReady(authFailure) {
		t.Errorf("handshakeMeansReady(%v) = false, want true for a normal auth-failure response", authFailure)
	}
}

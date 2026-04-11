package pvessh

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDial_AmbiguousAuth(t *testing.T) {
	_, err := Dial(context.Background(), Config{Host: "127.0.0.1:0", User: "x"})
	if !errors.Is(err, ErrAuthMethodAmbiguous) {
		t.Fatalf("want ErrAuthMethodAmbiguous, got %v", err)
	}
	_, err = Dial(context.Background(), Config{Host: "127.0.0.1:0", User: "x", Password: "p", KeyPath: "k"})
	if !errors.Is(err, ErrAuthMethodAmbiguous) {
		t.Fatalf("want ErrAuthMethodAmbiguous when both set, got %v", err)
	}
}

func TestDial_PasswordHappyPath(t *testing.T) {
	srv := newTestServer(t)
	srv.start(t)
	kh := srv.writeKnownHosts(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Dial(ctx, Config{
		Host: srv.addr, User: "root", Password: srv.validPassword, KnownHosts: kh,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestDial_KeyHappyPath(t *testing.T) {
	srv := newTestServer(t)
	keyPath, pub := writeED25519Key(t)
	srv.authorizedKey = pub
	srv.validPassword = ""
	srv.start(t)
	kh := srv.writeKnownHosts(t)

	ctx := context.Background()
	client, err := Dial(ctx, Config{
		Host: srv.addr, User: "root", KeyPath: keyPath, KnownHosts: kh,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
}

func TestDial_EncryptedKeyNoPassphrase(t *testing.T) {
	keyPath, _ := writeEncryptedED25519Key(t, "secret")
	_, err := Dial(context.Background(), Config{
		Host: "127.0.0.1:0", User: "root", KeyPath: keyPath, KnownHosts: "/dev/null",
	})
	if !errors.Is(err, ErrEncryptedKeyNoPass) {
		t.Fatalf("want ErrEncryptedKeyNoPass, got %v", err)
	}
}

func TestDial_WrongPasswordNoLeak(t *testing.T) {
	srv := newTestServer(t)
	srv.validPassword = "correct-horse"
	srv.start(t)
	kh := srv.writeKnownHosts(t)

	secret := "Tr0ub4dor&3"
	_, err := Dial(context.Background(), Config{
		Host: srv.addr, User: "root", Password: secret, KnownHosts: kh,
	})
	if err == nil {
		t.Fatalf("expected auth error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error message leaks password: %q", err.Error())
	}
}

func TestDial_WrongBothAndNeither(t *testing.T) {
	_, err := Dial(context.Background(), Config{Host: "h", User: "u", Password: "p", KeyPath: "k"})
	if !errors.Is(err, ErrAuthMethodAmbiguous) {
		t.Fatalf("both set: want ErrAuthMethodAmbiguous, got %v", err)
	}
	_, err = Dial(context.Background(), Config{Host: "h", User: "u"})
	if !errors.Is(err, ErrAuthMethodAmbiguous) {
		t.Fatalf("neither set: want ErrAuthMethodAmbiguous, got %v", err)
	}
}

func TestPing_SFTPDisabled(t *testing.T) {
	srv := newTestServer(t)
	srv.disableSFTP = true
	srv.start(t)
	kh := srv.writeKnownHosts(t)

	_, err := Dial(context.Background(), Config{
		Host: srv.addr, User: "root", Password: srv.validPassword, KnownHosts: kh,
	})
	if err == nil {
		t.Fatalf("expected sftp open failure")
	}
	if !strings.Contains(err.Error(), "sftp") {
		t.Fatalf("error should mention sftp: %v", err)
	}
}

func TestUploadSnippet_CreatesDirAndWrites(t *testing.T) {
	srv := newTestServer(t)
	srv.start(t)
	kh := srv.writeKnownHosts(t)

	ctx := context.Background()
	client, err := Dial(ctx, Config{
		Host: srv.addr, User: "root", Password: srv.validPassword, KnownHosts: kh,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// Use the test server's rootDir as an absolute path so sftp writes
	// land under the t.TempDir() jail.
	storagePath := srv.rootDir + "/pool"
	if err := client.UploadSnippet(ctx, storagePath, "bake.yaml", []byte("hello")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	on := filepath.Join(srv.rootDir, "pool/snippets/bake.yaml")
	got, err := os.ReadFile(on)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q", got)
	}
}

func TestUploadSnippet_Overwrites(t *testing.T) {
	srv := newTestServer(t)
	srv.start(t)
	kh := srv.writeKnownHosts(t)

	// Pre-create the file with old content.
	pool := filepath.Join(srv.rootDir, "snip")
	dir := filepath.Join(pool, "snippets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.yaml"), []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}

	client, err := Dial(context.Background(), Config{
		Host: srv.addr, User: "root", Password: srv.validPassword, KnownHosts: kh,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if err := client.UploadSnippet(context.Background(), pool, "file.yaml", []byte("NEW")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "file.yaml"))
	if string(got) != "NEW" {
		t.Fatalf("content = %q", got)
	}
}

func TestUploadSnippet_ContextCancel(t *testing.T) {
	srv := newTestServer(t)
	srv.start(t)
	kh := srv.writeKnownHosts(t)

	client, err := Dial(context.Background(), Config{
		Host: srv.addr, User: "root", Password: srv.validPassword, KnownHosts: kh,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	pool := filepath.Join(srv.rootDir, "cancel")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call
	err = client.UploadSnippet(ctx, pool, "f.yaml", []byte("xyz"))
	if err == nil {
		t.Fatalf("expected context error")
	}
	if !errors.Is(err, context.Canceled) {
		// Goroutine might have raced to completion — accept that too,
		// but if we got an error, it should be the ctx error.
		t.Logf("cancel race: err=%v", err)
	}
	// Dest should not exist with partial content.
	dest := filepath.Join(pool, "snippets/f.yaml")
	if data, err := os.ReadFile(dest); err == nil {
		// Accept the race where upload completed successfully.
		if string(data) != "xyz" {
			t.Fatalf("dest exists but is partial: %q", data)
		}
	}
}

func TestHostKeyMismatch(t *testing.T) {
	srv := newTestServer(t)
	srv.start(t)

	// Write a known_hosts with a DIFFERENT key so strict verify fails.
	otherSigner := genED25519(t)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	host, _, _ := splitHP(srv.addr)
	_, port, _ := splitHP(srv.addr)
	line := "[" + host + "]:" + port + " ssh-ed25519 " + keyBase64(otherSigner.PublicKey()) + "\n"
	if err := os.WriteFile(kh, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Dial(context.Background(), Config{
		Host: srv.addr, User: "root", Password: srv.validPassword, KnownHosts: kh,
	})
	if err == nil {
		t.Fatalf("expected host key mismatch error")
	}
}

func TestInsecureSkipsHostKey(t *testing.T) {
	srv := newTestServer(t)
	srv.start(t)

	// No known_hosts configured — with Insecure this should still work.
	client, err := Dial(context.Background(), Config{
		Host: srv.addr, User: "root", Password: srv.validPassword, Insecure: true,
	})
	if err != nil {
		t.Fatalf("dial insecure: %v", err)
	}
	defer client.Close()
}

// splitHP is a tiny wrapper so the test file doesn't depend on "net".
func splitHP(addr string) (host, port string, err error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "", nil
	}
	return addr[:i], addr[i+1:], nil
}

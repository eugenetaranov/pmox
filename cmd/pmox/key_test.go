package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/eugenetaranov/pmox/internal/accessreg"
	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/server"
)

// memRegistry is an in-memory accessreg.FS shared by key/access tests.
type memRegistry struct {
	mu    sync.Mutex
	files map[string][]byte
	opens int
}

func (m *memRegistry) ReadFile(_ context.Context, p string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte{}, d...), nil
}

func (m *memRegistry) WriteFile(_ context.Context, p string, d []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[p] = append([]byte{}, d...)
	return nil
}

func (m *memRegistry) Remove(_ context.Context, p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[p]; !ok {
		return os.ErrNotExist
	}
	delete(m.files, p)
	return nil
}

func (m *memRegistry) ReadDir(_ context.Context, dir string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	for p := range m.files {
		if rest, ok := strings.CutPrefix(p, dir+"/"); ok && !strings.Contains(rest, "/") {
			names = append(names, rest)
		}
	}
	if len(names) == 0 {
		return nil, os.ErrNotExist
	}
	sort.Strings(names)
	return names, nil
}

const (
	testKeyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB9jnDa6rNsCPaUhjTew6VOVxCJdYS0HL4tE7O9I8+Y7 bob@mbp"
	testKeyB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOA7Sj0n2Y2ZZbDy7Bq7fV1a8pSfNVo2qGx1t1c3cZ6Y other@mbp"
)

// setupRegistryEnv configures one server (formURL) whose ssh_pubkey is
// key, stubs the registry with an in-memory one and the local username
// with "bob".
func setupRegistryEnv(t *testing.T, key string) *memRegistry {
	t.Helper()
	isolate(t)
	keyPath := writePubKey(t, key+"\n")
	cfg := &config.Config{Servers: map[string]*config.Server{
		formURL: {TokenID: "root@pam!pmox", Node: "p0", User: "ubuntu", SSHPubkey: keyPath},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if err := credstore.Set(formURL, "sek"); err != nil {
		t.Fatal(err)
	}
	reg := &memRegistry{files: map[string][]byte{}}
	origOpen, origUser := openRegistryFn, localUsername
	openRegistryFn = func(context.Context, *server.Resolved) (accessreg.FS, func(), error) {
		reg.opens++
		return reg, func() {}, nil
	}
	localUsername = func() (string, error) { return "bob", nil }
	t.Cleanup(func() { openRegistryFn, localUsername = origOpen, origUser })
	return reg
}

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newKeyCmd()
	if args[0] == "access" {
		cmd = newAccessCmd()
	}
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args[1:])
	err := cmd.ExecuteContext(context.Background())
	return out.String() + errOut.String(), err
}

func TestKeyPublishDefaultsToLocalUsername(t *testing.T) {
	reg := setupRegistryEnv(t, testKeyA)
	out, err := runCmd(t, "key", "publish")
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	k, err := accessreg.GetKey(context.Background(), reg, "bob")
	if err != nil || !strings.HasSuffix(k.Line, " pmox-access:bob") || k.TokenUser != "root@pam!pmox" {
		t.Fatalf("published key = %+v, %v", k, err)
	}
	if !strings.Contains(out, "✓ published bob") || !strings.Contains(out, "pmox access grant <vm> --to bob") {
		t.Errorf("output:\n%s", out)
	}
	// Publishing the same key again is fine (refreshes metadata).
	if _, err := runCmd(t, "key", "publish"); err != nil {
		t.Fatalf("republish same key: %v", err)
	}
}

func TestKeyPublishDifferentKeyNeedsReplace(t *testing.T) {
	reg := setupRegistryEnv(t, testKeyA)
	other, _ := accessreg.NewPublishedKey("bob", testKeyB)
	other.Host = "other-ws"
	if err := accessreg.PublishKey(context.Background(), reg, other); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "key", "publish")
	if !errors.Is(err, exitcode.ErrUserInput) && !strings.Contains(out, "--replace") {
		t.Fatalf("want a --replace hint without a terminal, got err=%v\n%s", err, out)
	}
	if k, _ := accessreg.GetKey(context.Background(), reg, "bob"); k.Fingerprint != other.Fingerprint {
		t.Fatal("existing key was replaced without --replace")
	}
	if _, err := runCmd(t, "key", "publish", "--replace"); err != nil {
		t.Fatalf("--replace: %v", err)
	}
	mine, _ := accessreg.NewPublishedKey("bob", testKeyA)
	if k, _ := accessreg.GetKey(context.Background(), reg, "bob"); k.Fingerprint != mine.Fingerprint {
		t.Error("--replace did not replace the key")
	}
}

func TestKeyPublishRejectsBadName(t *testing.T) {
	setupRegistryEnv(t, testKeyA)
	if _, err := runCmd(t, "key", "publish", "--name", "../x"); !errors.Is(err, exitcode.ErrUserInput) {
		t.Errorf("err = %v, want a user-input error", err)
	}
}

func TestKeyUnpublishAndShow(t *testing.T) {
	reg := setupRegistryEnv(t, testKeyA)
	if _, err := runCmd(t, "key", "publish"); err != nil {
		t.Fatal(err)
	}
	out, _ := runCmd(t, "key", "show")
	if !strings.Contains(out, "status: published") {
		t.Errorf("show after publish:\n%s", out)
	}
	if _, err := runCmd(t, "key", "unpublish"); err != nil {
		t.Fatal(err)
	}
	if _, err := accessreg.GetKey(context.Background(), reg, "bob"); !errors.Is(err, accessreg.ErrNotPublished) {
		t.Errorf("still published: %v", err)
	}
	out, _ = runCmd(t, "key", "show")
	if !strings.Contains(out, "not published") {
		t.Errorf("show after unpublish:\n%s", out)
	}
	// Unpublishing again: the goal already holds, so it's success.
	out, err := runCmd(t, "key", "unpublish")
	if err != nil || !strings.Contains(out, "✓ nothing is published as bob") {
		t.Errorf("second unpublish: err=%v\n%s", err, out)
	}
}

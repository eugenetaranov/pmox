package pvessh

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFileOpsRoundTrip(t *testing.T) {
	srv := newTestServer(t)
	srv.start(t)
	kh := srv.writeKnownHosts(t)
	ctx := context.Background()
	c, err := Dial(ctx, Config{Host: srv.addr, User: "root", Password: srv.validPassword, KnownHosts: kh})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	dir := srv.rootDir + "/etc/pve/pmox/keys"
	p := dir + "/bob.pub"

	if _, err := c.ReadFile(ctx, p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadFile missing: err = %v, want ErrNotExist", err)
	}
	if _, err := c.ReadDir(ctx, dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadDir missing: err = %v, want ErrNotExist", err)
	}
	for _, content := range []string{"first", "second"} { // create, then replace
		if err := c.WriteFile(ctx, p, []byte(content)); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		got, err := c.ReadFile(ctx, p)
		if err != nil || string(got) != content {
			t.Fatalf("ReadFile = %q, %v; want %q", got, err, content)
		}
	}
	if err := c.WriteFile(ctx, dir+"/alice.pub", []byte("a")); err != nil {
		t.Fatal(err)
	}
	names, err := c.ReadDir(ctx, dir)
	if err != nil || !slices.Equal(names, []string{"alice.pub", "bob.pub"}) {
		t.Fatalf("ReadDir = %v, %v (temp files must not linger)", names, err)
	}
	if err := c.Remove(ctx, p); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(srv.rootDir, "etc/pve/pmox/keys/bob.pub")); !os.IsNotExist(err) {
		t.Errorf("file still on disk after Remove: %v", err)
	}
	if err := c.Remove(ctx, p); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Remove missing: err = %v, want ErrNotExist", err)
	}
}

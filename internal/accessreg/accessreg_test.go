package accessreg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// memFS is an in-memory FS. onRead, when set, runs after each read of
// access.yaml (to simulate a concurrent writer).
type memFS struct {
	files  map[string][]byte
	onRead func(n int)
	reads  int
}

func newMemFS() *memFS { return &memFS{files: map[string][]byte{}} }

func (m *memFS) ReadFile(_ context.Context, p string) ([]byte, error) {
	d, ok := m.files[p]
	if p == AccessFile {
		m.reads++
		defer func() {
			if m.onRead != nil {
				m.onRead(m.reads)
			}
		}()
	}
	if !ok {
		return nil, fmt.Errorf("read %s: %w", p, os.ErrNotExist)
	}
	return append([]byte{}, d...), nil
}

func (m *memFS) WriteFile(_ context.Context, p string, d []byte) error {
	m.files[p] = append([]byte{}, d...)
	return nil
}

func (m *memFS) Remove(_ context.Context, p string) error {
	if _, ok := m.files[p]; !ok {
		return os.ErrNotExist
	}
	delete(m.files, p)
	return nil
}

func (m *memFS) ReadDir(_ context.Context, dir string) ([]string, error) {
	var names []string
	for p := range m.files {
		if strings.HasPrefix(p, dir+"/") && !strings.Contains(p[len(dir)+1:], "/") {
			names = append(names, p[len(dir)+1:])
		}
	}
	if len(names) == 0 {
		return nil, os.ErrNotExist
	}
	sort.Strings(names)
	return names, nil
}

const bobKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB9jnDa6rNsCPaUhjTew6VOVxCJdYS0HL4tE7O9I8+Y7 bob@mbp"

func TestPublishGetListUnpublish(t *testing.T) {
	ctx := context.Background()
	fs := newMemFS()
	k, err := NewPublishedKey("bob", bobKey)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(k.Line, " pmox-access:bob") || !strings.HasPrefix(k.Fingerprint, "SHA256:") {
		t.Fatalf("normalized key = %+v", k)
	}
	k.Host, k.UID, k.TokenUser, k.PublishedAt = "mbp", "502", "root@pam!pmox", time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	if err := PublishKey(ctx, fs, k); err != nil {
		t.Fatal(err)
	}
	got, err := GetKey(ctx, fs, "bob")
	if err != nil || got.Line != k.Line || got.Host != "mbp" || got.UID != "502" || !got.PublishedAt.Equal(k.PublishedAt) {
		t.Fatalf("GetKey = %+v, %v", got, err)
	}
	fs.files[KeysDir+"/broken.pub"] = []byte("# nothing\n")
	keys, errs, err := ListKeys(ctx, fs)
	if err != nil || len(keys) != 1 || keys[0].Name != "bob" || len(errs) != 1 {
		t.Fatalf("ListKeys = %v %v %v", keys, errs, err)
	}
	if err := UnpublishKey(ctx, fs, "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetKey(ctx, fs, "bob"); !errors.Is(err, ErrNotPublished) {
		t.Errorf("after unpublish: %v", err)
	}
	if err := UnpublishKey(ctx, fs, "bob"); !errors.Is(err, ErrNotPublished) {
		t.Errorf("unpublish twice: %v", err)
	}
	if _, err := GetKey(ctx, fs, "../etc/passwd"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("path traversal name accepted: %v", err)
	}
}

func TestUpdateAccessRoundTripAndKeysFor(t *testing.T) {
	ctx := context.Background()
	fs := newMemFS()
	_, err := UpdateAccess(ctx, fs, func(a *Access) error {
		a.GrantVMs("bob", 102, 101, 101)
		a.GrantAll("carol")
		a.GrantVMs("dave", 101)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := ReadAccess(ctx, fs)
	if err != nil || !slices.Equal(a.People["bob"].VMs, []int{101, 102}) || !a.People["carol"].AllVMs {
		t.Fatalf("ReadAccess = %+v %v\n%s", a, err, fs.files[AccessFile])
	}
	bob, _ := NewPublishedKey("bob", bobKey)
	carol, _ := NewPublishedKey("carol", strings.Replace(bobKey, "bob@mbp", "c", 1))
	lines, missing := a.KeysFor(101, []PublishedKey{carol, bob})
	if !slices.Equal(lines, []string{bob.Line, carol.Line}) || !slices.Equal(missing, []string{"dave"}) {
		t.Errorf("KeysFor(101) = %v missing %v", lines, missing)
	}
	if lines, _ := a.KeysFor(999, []PublishedKey{bob, carol}); !slices.Equal(lines, []string{carol.Line}) {
		t.Errorf("KeysFor(999) = %v, want only the all-VMs grantee", lines)
	}

	_, _ = UpdateAccess(ctx, fs, func(a *Access) error {
		a.RevokeVMs("bob", 101, 102)
		return nil
	})
	a, _ = ReadAccess(ctx, fs)
	if _, ok := a.People["bob"]; ok {
		t.Errorf("empty grant should be dropped: %+v", a.People["bob"])
	}
}

func TestUpdateAccessConcurrentChange(t *testing.T) {
	ctx := context.Background()
	t.Run("retried once and merged", func(t *testing.T) {
		fs := newMemFS()
		fs.onRead = func(n int) {
			if n == 1 { // someone else writes between our read and our check
				fs.files[AccessFile] = []byte("version: 1\npeople:\n  carol:\n    all_vms: true\n")
			}
		}
		if _, err := UpdateAccess(ctx, fs, func(a *Access) error { a.GrantVMs("bob", 101); return nil }); err != nil {
			t.Fatal(err)
		}
		a, _ := ReadAccess(ctx, fs)
		if !a.Allowed("bob", 101) || !a.Allowed("carol", 5) {
			t.Errorf("concurrent change lost: %s", fs.files[AccessFile])
		}
	})
	t.Run("keeps changing → conflict", func(t *testing.T) {
		fs := newMemFS()
		fs.onRead = func(n int) {
			if n%2 == 1 {
				fs.files[AccessFile] = []byte(fmt.Sprintf("version: 1\npeople:\n  x%d:\n    all_vms: true\n", n))
			}
		}
		if _, err := UpdateAccess(ctx, fs, func(a *Access) error { return nil }); !errors.Is(err, ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})
}

func TestParseAccessRejectsUnknownVersion(t *testing.T) {
	fs := newMemFS()
	fs.files[AccessFile] = []byte("version: 2\n")
	if _, err := ReadAccess(context.Background(), fs); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("err = %v", err)
	}
}

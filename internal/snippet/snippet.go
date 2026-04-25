// Package snippet orchestrates the upload, validation, and cleanup
// of cloud-init snippet files stored on a Proxmox host.
package snippet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// MaxBytes is the maximum size of a cloud-init snippet file accepted
// by pmox. PVE enforces a 64 KiB limit on snippet contents.
const MaxBytes = 64 * 1024

// ValidateContent checks that the given cloud-init file contents are
// non-empty, within the 64 KiB limit, and valid UTF-8.
func ValidateContent(content []byte) error {
	if len(content) == 0 {
		return errors.New("cloud-init file is empty")
	}
	if len(content) > MaxBytes {
		return fmt.Errorf("cloud-init file is %d bytes; max 64 KiB", len(content))
	}
	if !utf8.Valid(content) {
		return errors.New("cloud-init file is not valid UTF-8")
	}
	return nil
}

// HasSSHKeys reports whether the content contains an
// `ssh_authorized_keys:` substring. This is a coarse text check that
// catches the common shape (top-level or nested under users).
func HasSSHKeys(content []byte) bool {
	return bytes.Contains(content, []byte("ssh_authorized_keys:"))
}

// Filename returns the pmox-owned snippet filename for the given VMID.
// This convention is frozen: both the uploader and the delete hook
// rely on it.
func Filename(vmid int) string {
	return fmt.Sprintf("pmox-%d-user-data.yaml", vmid)
}

// ValidateStorage verifies that the given storage has `snippets` in
// its content types. The error message is the exact template from
// design D3 and is actionable: it names the storage, lists its
// current content types, and points at both fix paths.
func ValidateStorage(ctx context.Context, client *pveclient.Client, node, storage string) error {
	storages, err := client.ListStorage(ctx, node)
	if err != nil {
		return fmt.Errorf("list storage on %s: %w", node, err)
	}
	for _, s := range storages {
		if s.Storage != storage {
			continue
		}
		for _, c := range strings.Split(s.Content, ",") {
			if strings.TrimSpace(c) == "snippets" {
				return nil
			}
		}
		return fmt.Errorf(`storage %q does not have 'snippets' in its content types

  current content: %s
  expected to include: snippets

fix options:
  1. edit /etc/pve/storage.cfg on the PVE host and add snippets
     to the content= line for this storage
  2. re-run with --storage <other-storage> pointing to a storage
     that supports snippets (see: pmox configure --list-storage)

see https://pve.proxmox.com/wiki/Storage for content-type details`, storage, s.Content)
	}
	return fmt.Errorf("storage %q not found on node %s", storage, node)
}

// Cleanup removes the snippet referenced by a VM's cicustom config
// value. An already-missing file (ErrNotFound) is swallowed as
// success; other errors are returned verbatim for the caller to
// surface as a warning.
func Cleanup(ctx context.Context, client *pveclient.Client, node, cicustomValue string) error {
	storage, filename, err := ParseCicustom(cicustomValue)
	if err != nil {
		return err
	}
	if err := client.DeleteSnippet(ctx, node, storage, filename); err != nil {
		if errors.Is(err, pveclient.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// ParseCicustom extracts the storage and filename from a `cicustom`
// config value of the form
// `user=<storage>:snippets/<filename>[,meta=...][,network=...]`.
func ParseCicustom(value string) (storage, filename string, err error) {
	var userPart string
	for _, seg := range strings.Split(value, ",") {
		seg = strings.TrimSpace(seg)
		if strings.HasPrefix(seg, "user=") {
			userPart = strings.TrimPrefix(seg, "user=")
			break
		}
	}
	if userPart == "" {
		return "", "", fmt.Errorf("cicustom value %q has no user= segment", value)
	}
	colon := strings.IndexByte(userPart, ':')
	if colon < 0 {
		return "", "", fmt.Errorf("cicustom user= segment %q is missing ':'", userPart)
	}
	storage = userPart[:colon]
	rest := userPart[colon+1:]
	const prefix = "snippets/"
	if !strings.HasPrefix(rest, prefix) {
		return "", "", fmt.Errorf("cicustom user= segment %q is missing 'snippets/' prefix", userPart)
	}
	filename = strings.TrimPrefix(rest, prefix)
	if filename == "" {
		return "", "", fmt.Errorf("cicustom user= segment %q has empty filename", userPart)
	}
	return storage, filename, nil
}

package pvessh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"

	"github.com/pkg/sftp"
)

// Generic file operations on the node, used for pmox's cluster-side
// state (e.g. the /etc/pve/pmox access registry). Like UploadSnippet,
// SFTP calls ignore ctx; on cancellation the Client is closed to unblock
// them and ctx.Err() is returned (the Client is unusable afterwards).

// run executes fn, aborting the session if ctx is cancelled first.
func (c *Client) run(ctx context.Context, fn func() error) error {
	if c.sftp == nil {
		return errors.New("pvessh: client has no sftp session")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case <-ctx.Done():
		_ = c.Close()
		<-done
		return ctx.Err()
	case err := <-done:
		return err
	}
}

// ReadFile returns the contents of p. A missing file yields an error
// wrapping os.ErrNotExist.
func (c *Client) ReadFile(ctx context.Context, p string) ([]byte, error) {
	var data []byte
	err := c.run(ctx, func() error {
		f, err := c.sftp.Open(p)
		if err != nil {
			return mapNotExist(err)
		}
		defer func() { _ = f.Close() }()
		data, err = io.ReadAll(f)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	return data, nil
}

// WriteFile atomically replaces p with data: it creates p's directory if
// needed, writes a dot-temp next to it, then renames it over p. It never
// chmods (Proxmox's /etc/pve filesystem manages permissions itself).
func (c *Client) WriteFile(ctx context.Context, p string, data []byte) error {
	dir := path.Dir(p)
	tmp := path.Join(dir, "."+path.Base(p)+".tmp")
	err := c.run(ctx, func() error {
		if err := c.sftp.MkdirAll(dir); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		f, err := c.sftp.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			return fmt.Errorf("open temp %s: %w", tmp, err)
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			return fmt.Errorf("write temp %s: %w", tmp, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close temp %s: %w", tmp, err)
		}
		if err := c.sftp.PosixRename(tmp, p); err != nil {
			if _, statErr := c.sftp.Stat(p); statErr == nil {
				_ = c.sftp.Remove(p)
			}
			if err2 := c.sftp.Rename(tmp, p); err2 != nil {
				return fmt.Errorf("rename %s -> %s: %w", tmp, p, err2)
			}
		}
		return nil
	})
	if err != nil {
		if c.sftp != nil {
			_ = c.sftp.Remove(tmp)
		}
		return fmt.Errorf("write %s: %w", p, err)
	}
	return nil
}

// Remove deletes p. A missing file yields an error wrapping
// os.ErrNotExist.
func (c *Client) Remove(ctx context.Context, p string) error {
	err := c.run(ctx, func() error { return mapNotExist(c.sftp.Remove(p)) })
	if err != nil {
		return fmt.Errorf("remove %s: %w", p, err)
	}
	return nil
}

// ReadDir returns the sorted names of the regular files in dir. A
// missing directory yields an error wrapping os.ErrNotExist.
func (c *Client) ReadDir(ctx context.Context, dir string) ([]string, error) {
	var names []string
	err := c.run(ctx, func() error {
		infos, err := c.sftp.ReadDir(dir)
		if err != nil {
			return mapNotExist(err)
		}
		for _, fi := range infos {
			if fi.Mode().IsRegular() {
				names = append(names, fi.Name())
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	sort.Strings(names)
	return names, nil
}

// mapNotExist normalizes SFTP's "no such file" status to os.ErrNotExist.
func mapNotExist(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return err
	}
	var se *sftp.StatusError
	if errors.As(err, &se) && se.FxCode() == sftp.ErrSSHFxNoSuchFile {
		return fmt.Errorf("%w: %w", os.ErrNotExist, err)
	}
	return err
}

// RemoveDir deletes the empty directory p.
func (c *Client) RemoveDir(ctx context.Context, p string) error {
	err := c.run(ctx, func() error { return mapNotExist(c.sftp.RemoveDirectory(p)) })
	if err != nil {
		return fmt.Errorf("remove dir %s: %w", p, err)
	}
	return nil
}

package answers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// openDir opens acct.Home/dirs... without following a symlink below the
// home, creating missing directories (0700, the user's) when create.
func openDir(acct Account, dirs []string, create, chown bool) (int, error) {
	fd, err := unix.Open(acct.Home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, &fs.PathError{Op: "open", Path: acct.Home, Err: err}
	}
	for _, d := range dirs {
		flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		nfd, err := unix.Openat(fd, d, flags, 0)
		if errors.Is(err, unix.ENOENT) && create {
			if err = unix.Mkdirat(fd, d, 0o700); err == nil || errors.Is(err, unix.EEXIST) {
				nfd, err = unix.Openat(fd, d, flags, 0)
				if err == nil && chown {
					err = unix.Fchown(nfd, acct.UID, acct.GID)
				}
			}
		}
		_ = unix.Close(fd)
		if err != nil {
			return -1, noFollowErr(d, err)
		}
		fd = nfd
	}
	return fd, nil
}

func noFollowErr(name string, err error) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return fs.ErrNotExist
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR), errors.Is(err, unix.EMLINK):
		return fmt.Errorf("%s is a symlink or not a directory; devbox-setup won't follow it", name)
	}
	return fmt.Errorf("%s: %w", name, err)
}

// readNoFollow reads acct.Home/path..., refusing symlinks, anything but a
// regular file, a file owned by someone other than the user or root, and
// files over maxUserFile.
func readNoFollow(acct Account, path []string) ([]byte, error) {
	dfd, err := openDir(acct, path[:len(path)-1], false, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(dfd) }()
	name := path[len(path)-1]
	// O_NONBLOCK: a FIFO planted there can't hang the run.
	fd, err := unix.Openat(dfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EMLINK) {
			return nil, errors.New("is a symlink; devbox-setup won't follow it")
		}
		return nil, noFollowErr(name, err)
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, err
	}
	switch {
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return nil, errors.New("not a regular file")
	case int(st.Uid) != acct.UID && st.Uid != 0:
		return nil, fmt.Errorf("owned by uid %d, not the user", st.Uid)
	case st.Size > maxUserFile:
		return nil, fmt.Errorf("larger than %d bytes", maxUserFile)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxUserFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxUserFile {
		return nil, fmt.Errorf("larger than %d bytes", maxUserFile)
	}
	return b, nil
}

// writeNoFollow replaces acct.Home/path... (0600, the user's when chown)
// through a new temp file, without following symlinks.
func writeNoFollow(acct Account, path []string, b []byte, chown bool) error {
	dfd, err := openDir(acct, path[:len(path)-1], true, chown)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(dfd) }()
	name := path[len(path)-1]
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := "." + name + "." + hex.EncodeToString(rnd[:])
	fd, err := unix.Openat(dfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return noFollowErr(tmp, err)
	}
	f := os.NewFile(uintptr(fd), tmp)
	_, err = f.Write(b)
	if err == nil {
		err = f.Chmod(0o600)
	}
	if err == nil && chown {
		err = unix.Fchown(fd, acct.UID, acct.GID)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = unix.Renameat(dfd, tmp, dfd, name)
	}
	if err != nil {
		_ = unix.Unlinkat(dfd, tmp, 0)
	}
	return err
}

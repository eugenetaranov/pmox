package pvessh

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// UploadSnippet writes content to <storagePath>/snippets/<filename>
// atomically: MkdirAll, write to a dot-temp in the same directory, then
// rename over the destination. On context cancellation, the temp file is
// removed and the destination is untouched.
func (c *Client) UploadSnippet(ctx context.Context, storagePath, filename string, content []byte) error {
	if c.sftp == nil {
		return errors.New("pvessh: client has no sftp session")
	}
	if storagePath == "" {
		return errors.New("pvessh: storagePath is empty")
	}
	if filename == "" {
		return errors.New("pvessh: filename is empty")
	}
	if strings.ContainsAny(filename, "/\\") {
		return fmt.Errorf("pvessh: filename must be a basename, got %q", filename)
	}

	destDir := path.Join(storagePath, "snippets")
	dest := path.Join(destDir, filename)
	tmp := path.Join(destDir, "."+filename+".tmp")

	if err := c.sftp.MkdirAll(destDir); err != nil {
		return fmt.Errorf("sftp mkdir %s: %w", destDir, err)
	}

	done := make(chan error, 1)
	go func() {
		done <- c.writeAndRename(tmp, dest, content)
	}()

	select {
	case <-ctx.Done():
		// Best-effort cleanup — the goroutine may or may not be past
		// the write. Remove the temp file either way. The destination
		// is only touched by Rename after the write fully succeeds, so
		// if we cancel before the goroutine reaches Rename, the dest
		// is left untouched.
		_ = c.sftp.Remove(tmp)
		// Wait for the goroutine to finish so we don't leak it.
		<-done
		_ = c.sftp.Remove(tmp)
		return ctx.Err()
	case err := <-done:
		if err != nil {
			_ = c.sftp.Remove(tmp)
			return fmt.Errorf("upload %s: %w", dest, err)
		}
	}
	return nil
}

func (c *Client) writeAndRename(tmp, dest string, content []byte) error {
	// O_TRUNC so a stale temp from a prior crashed upload is overwritten.
	f, err := c.sftp.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("open temp %s: %w", tmp, err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp %s: %w", tmp, err)
	}
	// PosixRename replaces the destination atomically. Fall back to
	// Remove+Rename for servers without the posix-rename extension.
	if err := c.sftp.PosixRename(tmp, dest); err != nil {
		if _, statErr := c.sftp.Stat(dest); statErr == nil {
			_ = c.sftp.Remove(dest)
		}
		if err2 := c.sftp.Rename(tmp, dest); err2 != nil {
			return fmt.Errorf("rename %s -> %s: %w", tmp, dest, err2)
		}
	}
	return nil
}

// PromptAndPinHostKey dials the host once with an accept-first host-key
// callback, prints the fingerprint to w, and reads y/n from r. On "yes"
// it appends the key to knownHostsPath (creating the file with mode
// 0600 under a parent dir with mode 0700). It NEVER touches
// ~/.ssh/known_hosts.
func PromptAndPinHostKey(ctx context.Context, host string, w io.Writer, r io.Reader, knownHostsPath string) error {
	if knownHostsPath == "" {
		return errors.New("pvessh: knownHostsPath is empty")
	}
	if !strings.Contains(host, ":") {
		host = host + ":22"
	}

	var capturedKey ssh.PublicKey
	var capturedAddr string
	callback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		capturedKey = key
		capturedAddr = remote.String()
		return nil
	}

	cfg := &ssh.ClientConfig{
		User:            "pmox-hostkey-probe",
		Auth:            []ssh.AuthMethod{ssh.Password("pmox-hostkey-probe")},
		HostKeyCallback: callback,
		Timeout:         10 * time.Second,
	}

	dialer := net.Dialer{}
	if dl, ok := ctx.Deadline(); ok {
		dialer.Deadline = dl
	}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return fmt.Errorf("dial %s: %w", host, err)
	}
	// NewClientConn will fail auth, but the host-key callback fires
	// during the handshake before auth, so we get the key either way.
	sshConn, _, _, _ := ssh.NewClientConn(conn, host, cfg)
	if sshConn != nil {
		_ = sshConn.Close()
	}
	_ = conn.Close()

	if capturedKey == nil {
		return fmt.Errorf("host-key probe of %s did not capture a key", host)
	}

	fp := ssh.FingerprintSHA256(capturedKey)
	fmt.Fprintf(w, "The authenticity of host '%s (%s)' can't be established.\n", host, capturedAddr)
	fmt.Fprintf(w, "%s key fingerprint is %s\n", capturedKey.Type(), fp)
	fmt.Fprintf(w, "Are you sure you want to continue connecting (yes/no)? ")

	br := bufio.NewReader(r)
	ans, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("read host-key answer: %w", err)
	}
	ans = strings.TrimSpace(strings.ToLower(ans))
	if ans != "yes" && ans != "y" {
		return errors.New("host-key pin declined by user")
	}

	return appendKnownHost(knownHostsPath, host, capturedKey)
}

func appendKnownHost(knownHostsPath, host string, key ssh.PublicKey) error {
	dir := filepath.Dir(knownHostsPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create known_hosts dir %s: %w", dir, err)
	}
	_ = os.Chmod(dir, 0o700)

	// knownhosts.Normalize keeps the host:port form; strip default :22
	// so entries match both "host" and "host:22" lookups.
	h := strings.TrimSuffix(host, ":22")

	line := fmt.Sprintf("%s %s %s\n", h, key.Type(), keyBase64(key))
	f, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open known_hosts %s: %w", knownHostsPath, err)
	}
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("append known_hosts %s: %w", knownHostsPath, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	_ = os.Chmod(knownHostsPath, 0o600)
	return nil
}

func keyBase64(key ssh.PublicKey) string {
	// ssh.MarshalAuthorizedKey returns "type base64-key comment\n".
	// We only need the base64 middle segment.
	raw := ssh.MarshalAuthorizedKey(key)
	raw = []byte(strings.TrimRight(string(raw), "\n"))
	parts := strings.SplitN(string(raw), " ", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return string(raw)
}

// KnownHostsPath returns the pmox-managed known_hosts path,
// respecting $XDG_CONFIG_HOME and falling back to ~/.config/pmox/known_hosts.
func KnownHostsPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "pmox", "known_hosts"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "pmox", "known_hosts"), nil
}

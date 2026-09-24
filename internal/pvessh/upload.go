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

	"github.com/eugenetaranov/pmox/internal/paths"
)

// UploadSnippet writes content to <storagePath>/snippets/<filename>
// atomically: MkdirAll, write to a dot-temp in the same directory, then
// rename over the destination.
//
// SFTP calls are not context-aware, so on context cancellation
// UploadSnippet closes the Client to unblock the in-flight transfer and
// returns ctx.Err(); the Client is unusable afterwards. The destination
// is either untouched or fully written (the rename is atomic), but the
// dot-temp may be left behind on the node — the next upload of the same
// filename truncates and reuses it.
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

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.sftp.MkdirAll(destDir); err != nil {
		return fmt.Errorf("sftp mkdir %s: %w", destDir, err)
	}

	done := make(chan error, 1)
	go func() {
		done <- c.writeAndRename(tmp, dest, content)
	}()

	select {
	case <-ctx.Done():
		// The in-flight SFTP calls ignore ctx; closing the session
		// makes them fail fast so a hung node can't block us forever.
		// Wait for the goroutine so it doesn't leak.
		_ = c.Close()
		<-done
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
	// Restrict to owner-only before the rename so the published snippet is
	// never briefly world-readable. The snippet embeds the default user's
	// SSH public key and its sudo policy; on a shared PVE host the node's
	// default umask would otherwise leave it 0644.
	if err := c.sftp.Chmod(tmp, 0o600); err != nil {
		return fmt.Errorf("chmod temp %s: %w", tmp, err)
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
	sshConn, _, _, hsErr := clientHandshake(ctx, conn, host, cfg)
	if sshConn != nil {
		_ = sshConn.Close()
	}
	_ = conn.Close()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("host-key probe of %s: %w", host, err)
	}

	if capturedKey == nil {
		if hsErr != nil {
			return fmt.Errorf("host-key probe of %s did not capture a key: %w", host, hsErr)
		}
		return fmt.Errorf("host-key probe of %s did not capture a key", host)
	}

	fp := ssh.FingerprintSHA256(capturedKey)
	fmt.Fprintf(w, "The authenticity of host '%s (%s)' can't be established.\n", host, capturedAddr)
	fmt.Fprintf(w, "%s key fingerprint is %s\n", capturedKey.Type(), fp)
	fmt.Fprintf(w, "Are you sure you want to continue connecting (yes/no)? ")

	br := bufio.NewReader(r)
	ans, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
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
	dir, err := paths.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "known_hosts"), nil
}

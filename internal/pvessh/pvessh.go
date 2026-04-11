package pvessh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Config describes the target PVE node and the credentials to use.
// Exactly one of Password / KeyPath must be set.
type Config struct {
	Host       string // "pve.example.com:22"
	User       string
	Password   string
	KeyPath    string
	KeyPass    string
	Insecure   bool
	KnownHosts string // path to pmox-managed known_hosts file
}

// Client is a live SSH+SFTP session opened by Dial.
type Client struct {
	ssh  *ssh.Client
	sftp *sftp.Client
	host string // copied from Config.Host for error messages (no credentials)
}

// ErrEncryptedKeyNoPass is returned when KeyPath points at an encrypted
// key but KeyPass is empty.
var ErrEncryptedKeyNoPass = errors.New("ssh key is passphrase-protected but no passphrase was supplied")

// ErrAuthMethodAmbiguous is returned when Config has both Password and
// KeyPath set, or neither.
var ErrAuthMethodAmbiguous = errors.New("pvessh.Config: exactly one of Password or KeyPath must be set")

// Dial opens an SSH connection and the SFTP subsystem.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if err := validateAuth(cfg); err != nil {
		return nil, err
	}

	auth, err := buildAuth(cfg)
	if err != nil {
		return nil, err
	}

	hkCB, err := hostKeyCallback(cfg)
	if err != nil {
		return nil, err
	}

	clientCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: hkCB,
		Timeout:         15 * time.Second,
	}

	deadline, ok := ctx.Deadline()
	dialer := net.Dialer{}
	if ok {
		dialer.Deadline = deadline
	}
	conn, err := dialer.DialContext(ctx, "tcp", cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", cfg.Host, scrub(err, cfg))
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, cfg.Host, clientCfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh handshake %s: %w", cfg.Host, scrub(err, cfg))
	}
	sshClient := ssh.NewClient(sshConn, chans, reqs)

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return nil, fmt.Errorf("open sftp subsystem on %s: %w", cfg.Host, err)
	}

	return &Client{ssh: sshClient, sftp: sftpClient, host: cfg.Host}, nil
}

// Close tears down the SFTP session and then the underlying SSH client.
func (c *Client) Close() error {
	var first error
	if c.sftp != nil {
		if err := c.sftp.Close(); err != nil {
			first = err
		}
	}
	if c.ssh != nil {
		if err := c.ssh.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Ping stat()s "/" over SFTP to confirm the subsystem is alive. It never
// creates, modifies, or deletes anything on the remote host.
func (c *Client) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.sftp == nil {
		return errors.New("pvessh: sftp subsystem not available")
	}
	if _, err := c.sftp.Stat("/"); err != nil {
		return fmt.Errorf("sftp stat / on %s: %w", c.host, err)
	}
	return nil
}

func validateAuth(cfg Config) error {
	havePass := cfg.Password != ""
	haveKey := cfg.KeyPath != ""
	if havePass == haveKey {
		return ErrAuthMethodAmbiguous
	}
	return nil
}

func buildAuth(cfg Config) (ssh.AuthMethod, error) {
	if cfg.Password != "" {
		return ssh.Password(cfg.Password), nil
	}
	raw, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("read ssh key %s: %w", cfg.KeyPath, err)
	}
	var signer ssh.Signer
	if cfg.KeyPass != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(raw, []byte(cfg.KeyPass))
	} else {
		signer, err = ssh.ParsePrivateKey(raw)
		if err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) {
				return nil, fmt.Errorf("%w: %s", ErrEncryptedKeyNoPass, cfg.KeyPath)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("parse ssh key %s: %w", cfg.KeyPath, err)
	}
	return ssh.PublicKeys(signer), nil
}

func hostKeyCallback(cfg Config) (ssh.HostKeyCallback, error) {
	if cfg.Insecure {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	if cfg.KnownHosts == "" {
		return nil, errors.New("pvessh: KnownHosts path is empty and Insecure is false")
	}
	// If the file doesn't exist yet, knownhosts.New fails; callers must
	// pin via PromptAndPinHostKey before calling Dial in strict mode.
	cb, err := knownhosts.New(cfg.KnownHosts)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts %s: %w", cfg.KnownHosts, err)
	}
	return cb, nil
}

// scrub returns err unchanged unless it literally contains the password
// value, in which case it replaces the password with a redaction marker.
// Defensive — x/crypto/ssh does not leak passwords today, but makes the
// promise in the spec explicit and survives future changes.
func scrub(err error, cfg Config) error {
	if err == nil || cfg.Password == "" {
		return err
	}
	msg := err.Error()
	scrubbed := strings.ReplaceAll(msg, cfg.Password, "[REDACTED]")
	if scrubbed == msg {
		return err
	}
	return errors.New(scrubbed)
}

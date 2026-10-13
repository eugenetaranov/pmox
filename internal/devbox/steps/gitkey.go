package steps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

func init() { RegisterGo("gitkey", func(catalog.Step) Step { return gitKeyStep{} }) }

// gitKeyStep sets up the SSH key git uses: ~/.ssh/devbox_git_key (sent by
// pmox devbox gitkey, kept, or generated), and a block in ~/.ssh/config
// that makes every connection to the git hosts use it, so it works for
// any repo it is allowed on (one repo for a deploy key, all of them for
// an account key). GitHub's host keys go in known_hosts.
type gitKeyStep struct{}

const (
	// GitKeyPath is where the key goes.
	GitKeyPath = "~/.ssh/devbox_git_key"
	sshConfig  = "~/.ssh/config"
	knownHosts = "~/.ssh/known_hosts"
	gitBegin   = "# >>> devbox-setup gitkey (pmox devbox gitkey; managed)"
	gitEnd     = "# <<< devbox-setup gitkey"
)

func gitHosts(e *Env) []string {
	if e.Answers != nil && len(e.Answers.GitKeyHosts) > 0 {
		return e.Answers.GitKeyHosts
	}
	return []string{"github.com"}
}

func sentKey(e *Env) []byte {
	if e.Secrets == nil {
		return nil
	}
	return e.Secrets.GitKey
}

func (gitKeyStep) Plan(e *Env) []Action {
	key := "keep " + GitKeyPath + " (or generate one)"
	if sentKey(e) != nil {
		key = "write the key sent by pmox to " + GitKeyPath
	}
	return []Action{{"file", key}, {"file", sshConfig + ": use it for " + strings.Join(gitHosts(e), ", ")}}
}

// configBlock is the ~/.ssh/config block for the hosts.
func configBlock(hosts []string) string {
	var b strings.Builder
	b.WriteString(gitBegin + "\n")
	for _, h := range hosts {
		host, port, _ := strings.Cut(h, ":")
		fmt.Fprintf(&b, "Host %s\n  User git\n  IdentityFile %s\n  IdentitiesOnly yes\n", host, GitKeyPath)
		if port != "" {
			fmt.Fprintf(&b, "  Port %s\n", port)
		}
		if host != "github.com" {
			b.WriteString("  StrictHostKeyChecking accept-new\n")
		}
	}
	b.WriteString(gitEnd + "\n")
	return b.String()
}

// withConfigBlock puts the block first in ~/.ssh/config (ssh takes the
// first value it finds), replacing an earlier one.
func withConfigBlock(cur []byte, block string) []byte {
	s := string(cur)
	if i, j := strings.Index(s, gitBegin), strings.Index(s, gitEnd); i >= 0 && j > i {
		s = s[:i] + strings.TrimPrefix(s[j+len(gitEnd):], "\n")
	}
	s = strings.TrimLeft(s, "\n")
	if s != "" {
		s = "\n" + s
	}
	return []byte(block + s)
}

// sshRead and sshWrite reach the user's ~/.ssh as the user when running
// as root (a symlink there leads nowhere the user couldn't write), and
// directly otherwise (tests).
func sshRead(ctx context.Context, e *Env, p string) ([]byte, error) {
	if !e.Chown {
		b, err := os.ReadFile(e.Path(p))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return b, err
	}
	out, err := e.Exec.Run(ctx, Cmd{Name: "sh", Args: []string{"-c", `[ -e "$1" ] || exit 0; printf x; cat -- "$1"`, "_", e.Path(p)}, AsUser: true})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out[1:], nil
}

func sshWrite(ctx context.Context, e *Env, p string, b []byte) error {
	if !e.Chown {
		if err := os.MkdirAll(filepath.Dir(e.Path(p)), 0o700); err != nil {
			return err
		}
		return writeFile(e.Path(p), b, 0o600)
	}
	_, err := e.run(ctx, Cmd{Name: "sh", Args: []string{"-c",
		`umask 077 && mkdir -p -- "$(dirname -- "$1")" && chmod 700 -- "$(dirname -- "$1")" && cat > "$1.tmp" && chmod 600 "$1.tmp" && mv -f -- "$1.tmp" "$1"`, "_", e.Path(p)},
		Stdin: b, AsUser: true})
	return err
}

func (s gitKeyStep) Check(ctx context.Context, e *Env) (bool, error) {
	key, err := sshRead(ctx, e, GitKeyPath)
	if err != nil || key == nil {
		return false, err
	}
	if sent := sentKey(e); sent != nil && !bytes.Equal(key, sent) {
		return false, nil
	}
	cfg, err := sshRead(ctx, e, sshConfig)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(withConfigBlock(cfg, configBlock(gitHosts(e))), cfg) {
		return false, nil
	}
	return s.knownDone(ctx, e)
}

// knownDone reports whether GitHub's host keys are in known_hosts (when
// github.com is one of the hosts).
func (gitKeyStep) knownDone(ctx context.Context, e *Env) (bool, error) {
	if !hasHost(gitHosts(e), "github.com") {
		return true, nil
	}
	kh, err := sshRead(ctx, e, knownHosts)
	return err == nil && bytes.Contains(kh, []byte("github.com ssh-ed25519 ")), err
}

func hasHost(hosts []string, h string) bool {
	for _, x := range hosts {
		if x == h {
			return true
		}
	}
	return false
}

func (s gitKeyStep) Apply(ctx context.Context, e *Env) error {
	key, err := sshRead(ctx, e, GitKeyPath)
	if err != nil {
		return err
	}
	switch sent := sentKey(e); {
	case sent != nil:
		if !bytes.Contains(sent, []byte("PRIVATE KEY-----")) {
			return errors.New("what pmox sent isn't an SSH private key")
		}
		if err := sshWrite(ctx, e, GitKeyPath, sent); err != nil {
			return err
		}
		if err := s.publicKey(ctx, e); err != nil {
			e.note("git SSH key: " + err.Error())
		}
	case key == nil:
		if _, err := e.run(ctx, Cmd{Name: "sh", Args: []string{"-c",
			`umask 077 && mkdir -p ~/.ssh && ssh-keygen -q -t ed25519 -N "" -C "$1" -f "$2"`, "_", e.User + "@" + hostname() + " (git)", e.Path(GitKeyPath)}, AsUser: true}); err != nil {
			return err
		}
		pub, _ := sshRead(ctx, e, GitKeyPath+".pub")
		e.note(fmt.Sprintf("A new git SSH key: add it on GitHub as a deploy key (repo → Settings → Deploy keys; one repo) or to your account (github.com/settings/keys; all your repos): %s", strings.TrimSpace(string(pub))))
	}
	cfg, err := sshRead(ctx, e, sshConfig)
	if err != nil {
		return err
	}
	if want := withConfigBlock(cfg, configBlock(gitHosts(e))); !bytes.Equal(want, cfg) {
		if err := sshWrite(ctx, e, sshConfig, want); err != nil {
			return err
		}
	}
	if done, _ := s.knownDone(ctx, e); !done {
		gh, err := fs.ReadFile(e.Files, "github_known_hosts")
		if err != nil {
			return err
		}
		kh, err := sshRead(ctx, e, knownHosts)
		if err != nil {
			return err
		}
		if len(kh) > 0 && !bytes.HasSuffix(kh, []byte("\n")) {
			kh = append(kh, '\n')
		}
		if err := sshWrite(ctx, e, knownHosts, append(kh, gh...)); err != nil {
			return err
		}
	}
	return nil
}

// publicKey writes the sent key's .pub next to it (ssh-keygen -y); a key
// with a passphrase can't be read unattended, which is reported.
func (gitKeyStep) publicKey(ctx context.Context, e *Env) error {
	if !e.Chown {
		return nil
	}
	out, err := e.Exec.Run(ctx, Cmd{Name: "ssh-keygen", Args: []string{"-y", "-P", "", "-f", e.Path(GitKeyPath)}, AsUser: true})
	if err != nil {
		return errors.New("it has a passphrase, so git asks for it on each use; a key without one (or a forwarded agent) works unattended")
	}
	return sshWrite(ctx, e, GitKeyPath+".pub", out)
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

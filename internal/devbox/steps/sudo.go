package steps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

func init() { RegisterGo("sudopw", func(catalog.Step) Step { return sudoPWStep{} }) }

// sudoersDir holds the drop-in rules (cloud-init's 90-cloud-init-users,
// devbox-setup's 90-devbox-<user>).
const sudoersDir = "/etc/sudoers.d"

// sudoPWStep makes the user's sudo ask for their password: NOPASSWD is
// removed from their rules in /etc/sudoers.d. It refuses while the user
// has no password (sudo would then lock them out), unless one was typed
// this run.
type sudoPWStep struct{}

func (sudoPWStep) Plan(e *Env) []Action {
	return []Action{{"run", "remove NOPASSWD from " + e.User + "'s rules in " + sudoersDir}}
}

// nopasswd returns the sudoers.d files with a NOPASSWD rule for the user,
// and each one's content without NOPASSWD.
func (sudoPWStep) nopasswd(e *Env) (map[string][]byte, error) {
	out := map[string][]byte{}
	files, err := os.ReadDir(e.Path(sudoersDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	for _, f := range files {
		// sudo skips names with a '.' or ending in '~'.
		if f.IsDir() || strings.Contains(f.Name(), ".") || strings.HasSuffix(f.Name(), "~") {
			continue
		}
		p := filepath.Join(sudoersDir, f.Name())
		b, err := os.ReadFile(e.Path(p))
		if err != nil {
			return nil, err
		}
		lines := strings.Split(string(b), "\n")
		hit := false
		for i, l := range lines {
			if w := strings.Fields(l); len(w) > 1 && w[0] == e.User && strings.Contains(l, "NOPASSWD:") {
				lines[i], hit = strings.ReplaceAll(l, "NOPASSWD:", ""), true
			}
		}
		if hit {
			out[p] = []byte(strings.Join(lines, "\n"))
		}
	}
	return out, nil
}

func (s sudoPWStep) Check(_ context.Context, e *Env) (bool, error) {
	files, err := s.nopasswd(e)
	return err == nil && len(files) == 0, err
}

func (s sudoPWStep) Apply(ctx context.Context, e *Env) error {
	st, _ := e.Exec.Run(ctx, Cmd{Name: "passwd", Args: []string{"-S", e.User}})
	if f := strings.Fields(string(st)); len(f) < 2 || f[1] != "P" {
		pw := ""
		if e.Secrets != nil {
			pw = e.Secrets.SudoPW
		}
		if pw == "" {
			return fmt.Errorf("%s has no password, so sudo would lock them out: set one with 'sudo passwd %s', then re-run devbox-setup", e.User, e.User)
		}
		if _, err := e.Exec.Run(ctx, Cmd{Name: "chpasswd", Stdin: []byte(e.User + ":" + pw + "\n")}); err != nil {
			return fmt.Errorf("chpasswd: %w", err)
		}
		if e.Log != nil {
			fmt.Fprintf(e.Log, "$ chpasswd (%s; the password isn't logged)\n", e.User)
		}
	}
	files, err := s.nopasswd(e)
	if err != nil {
		return err
	}
	for p, b := range files {
		// A temp name with a '.', which sudo skips, checked before it's moved in.
		tmp := p + ".devbox-new"
		if err := writeFile(e.Path(tmp), b, 0o440); err != nil {
			return err
		}
		if _, err := e.run(ctx, Cmd{Name: "visudo", Args: []string{"-cqf", tmp}}); err != nil {
			_ = os.Remove(e.Path(tmp))
			return fmt.Errorf("%s without NOPASSWD doesn't check: %w", p, err)
		}
		if err := os.Rename(e.Path(tmp), e.Path(p)); err != nil {
			return err
		}
	}
	return nil
}

package steps

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

// The zsh themes: oh-my-zsh's default, and Powerlevel10k once it's cloned.
const (
	DefaultZshTheme = "robbyrussell"
	P10kTheme       = "powerlevel10k/powerlevel10k"
	p10kDir         = "~/.oh-my-zsh/custom/themes/powerlevel10k"
)

// Drop-ins: a line in .zshrc and .bashrc for each file devbox-setup keeps
// shell settings in.
var Dropins = []struct{ File, Line string }{
	{"~/.config/devbox/shell.sh", "[ -r ~/.config/devbox/shell.sh ] && . ~/.config/devbox/shell.sh"},
	{"~/.config/devbox/agent-env.sh", "[ -r ~/.config/devbox/agent-env.sh ] && . ~/.config/devbox/agent-env.sh"},
}

func init() { RegisterGo("zshrc", func(catalog.Step) Step { return zshrcStep{} }) }

// zshrcStep writes ~/.zshrc from zshrc.tmpl: the theme follows the p10k
// clone, so re-rendering keeps the prompt, and the drop-ins of the files
// that exist are added back (the template replaced them).
type zshrcStep struct{}

const zshrc = "~/.zshrc"

func (zshrcStep) Plan(*Env) []Action { return []Action{{"file", zshrc + " (from zshrc.tmpl)"}} }

func (zshrcStep) want(e *Env) ([]byte, error) {
	theme := DefaultZshTheme
	if _, err := os.Stat(e.Path(p10kDir)); err == nil {
		theme = P10kTheme
	}
	b, err := fileStep{s: catalog.Step{Src: "zshrc.tmpl", Vars: map[string]string{"devbox_zsh_theme": theme}}, template: true}.content(e)
	if err != nil {
		return nil, err
	}
	for _, d := range Dropins {
		if _, err := os.Stat(e.Path(d.File)); err == nil {
			b = WithLine(b, d.Line, "")
		}
	}
	return b, nil
}

func (z zshrcStep) Check(_ context.Context, e *Env) (bool, error) {
	want, err := z.want(e)
	if err != nil {
		return false, err
	}
	cur, err := os.ReadFile(e.Path(zshrc))
	return err == nil && bytes.Equal(cur, want), nil
}

func (z zshrcStep) Apply(_ context.Context, e *Env) error {
	want, err := z.want(e)
	if err != nil {
		return err
	}
	p := e.Path(zshrc)
	// The user's own .zshrc is kept once, as .zshrc.orig.
	if cur, err := os.ReadFile(p); err == nil {
		if _, err := os.Stat(p + ".orig"); errors.Is(err, fs.ErrNotExist) {
			if err := writeFile(p+".orig", cur, 0o644); err != nil {
				return err
			}
			if err := e.chown(p+".orig", e.UID, e.GID); err != nil {
				return err
			}
		}
	}
	if err := writeFile(p, want, 0o644); err != nil {
		return err
	}
	return e.chown(p, e.UID, e.GID)
}

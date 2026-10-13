package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

func init() { RegisterGo("dotfiles", func(catalog.Step) Step { return dotfilesStep{} }) }

// dotfilesStep brings the user's dotfiles repo up to date in ~/.dotfiles
// and applies it, as the user, on every run: the first of its install
// scripts, or else stow for each top-level directory. Its item comes last,
// so what it writes wins.
type dotfilesStep struct{}

// DotfilesScripts are the install scripts looked for, in order.
var DotfilesScripts = []string{"install.sh", "bootstrap.sh", "setup.sh"}

func (dotfilesStep) Always() bool { return true }

func dotfilesRepo(e *Env) string {
	if e.Answers == nil {
		return ""
	}
	return e.Answers.Dotfiles
}

func (dotfilesStep) Plan(e *Env) []Action {
	if dotfilesRepo(e) == "" {
		return []Action{{"note", "no dotfiles repo set"}}
	}
	return []Action{{"run", "git clone " + dotfilesRepo(e) + " ~/.dotfiles (or pull), then " + strings.Join(DotfilesScripts, ", ") + " or stow"}}
}

func (dotfilesStep) Check(context.Context, *Env) (bool, error) { return true, nil }

func (dotfilesStep) Apply(ctx context.Context, e *Env) error {
	repo := dotfilesRepo(e)
	if repo == "" {
		e.note("dotfiles: no repo set; run sudo devbox-setup and give one")
		return nil
	}
	dir := filepath.Join(e.Home, ".dotfiles")
	switch _, err := os.Stat(e.Path(dir)); {
	case err != nil:
		if _, err := e.run(ctx, Cmd{Name: "git", Args: []string{"clone", "--", repo, dir}, AsUser: true}); err != nil {
			return fmt.Errorf("dotfiles: clone %s: %w", repo, err)
		}
	default:
		if _, err := os.Stat(e.Path(filepath.Join(dir, ".git"))); err != nil {
			return fmt.Errorf("dotfiles: %s is there and isn't a git clone; move it away", dir)
		}
		if _, err := e.run(ctx, Cmd{Name: "git", Args: []string{"-C", dir, "pull", "--ff-only"}, AsUser: true}); err != nil {
			return fmt.Errorf("dotfiles: pull: %w", err)
		}
	}
	for _, s := range DotfilesScripts {
		if _, err := os.Stat(e.Path(filepath.Join(dir, s))); err == nil {
			_, err := e.run(ctx, Cmd{Name: "sh", Args: []string{"-c", `cd "$1" && if [ -x "./$2" ]; then "./$2"; else sh "./$2"; fi`, "_", dir, s}, AsUser: true})
			if err != nil {
				return fmt.Errorf("dotfiles: %s: %w", s, err)
			}
			return nil
		}
	}
	entries, err := os.ReadDir(e.Path(dir))
	if err != nil {
		return err
	}
	var pkgs []string
	for _, d := range entries {
		if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			pkgs = append(pkgs, d.Name())
		}
	}
	if len(pkgs) == 0 {
		e.note("dotfiles: " + dir + " has no install script and no directories to stow")
		return nil
	}
	if _, err := e.run(ctx, Cmd{Name: "stow", Args: append([]string{"-d", dir, "-t", e.Home, "--restow"}, pkgs...), AsUser: true}); err != nil {
		return fmt.Errorf("dotfiles: stow (a file in the way? stow won't replace it): %w", err)
	}
	return nil
}

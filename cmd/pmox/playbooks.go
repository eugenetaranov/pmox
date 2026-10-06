package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// Playbooks for 'pmox apply' live under ~/.config/pmox/tack — either a
// scaffolded starter or a git repo cloned with 'pmox apply --init <url>'.
// A repo may hold several playbooks; one is the default (remembered in
// pmox's config as tack_default_playbook), run by a bare 'pmox apply
// <vm>' and by 'launch --tack'. Other playbooks are run by name
// ('pmox apply <vm> <name>').

// preferredPlaybooks are picked as the default, in order, when nobody
// chooses (non-interactive) — common entry-point names.
var preferredPlaybooks = []string{"site", "playbook", "main"}

// playbookName is a playbook's name: its path relative to the tack dir
// without the .yml/.yaml extension (e.g. "site", "playbooks/db").
func playbookName(rel string) string {
	return strings.TrimSuffix(strings.TrimSuffix(filepath.ToSlash(rel), ".yaml"), ".yml")
}

// discoverPlaybooks lists the tack playbooks under dir as relative
// paths, sorted: YAML files whose top level is a play (a mapping with
// "hosts") or a list of plays. roles/, hidden dirs and anything deeper
// than three levels are skipped.
func discoverPlaybooks(dir string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "roles" || strings.Count(rel, string(filepath.Separator)) >= 3) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".yml" && ext != ".yaml" {
			return nil
		}
		if isPlaybook(path) {
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(found)
	return found, err
}

// isPlaybook reports whether the YAML file at path looks like a tack
// playbook: a play mapping with "hosts", or a list whose first item is.
func isPlaybook(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 1<<20 {
		return false
	}
	var doc any
	if yaml.Unmarshal(data, &doc) != nil {
		return false
	}
	hasHosts := func(v any) bool {
		m, ok := v.(map[string]any)
		_, has := m["hosts"]
		return ok && has
	}
	if list, ok := doc.([]any); ok && len(list) > 0 {
		return hasHosts(list[0])
	}
	return hasHosts(doc)
}

// findPlaybook resolves a playbook name ("web", "playbooks/db") or file
// name ("web.yml") under dir.
func findPlaybook(dir, name string) (string, bool) {
	for _, cand := range []string{name, name + ".yaml", name + ".yml"} {
		p := filepath.Join(dir, filepath.FromSlash(cand))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
	}
	return "", false
}

// defaultPlaybook returns the playbook a bare 'pmox apply' runs: the
// configured tack_default_playbook when it still exists, else
// playbook.yaml. The second value names where it came from.
func defaultPlaybook() (path, source string, err error) {
	dir, err := tackDir()
	if err != nil {
		return "", "", err
	}
	if cfg, cerr := config.Load(); cerr == nil && cfg.TackDefaultPlaybook != "" {
		if p, ok := findPlaybook(dir, cfg.TackDefaultPlaybook); ok {
			return p, fmt.Sprintf("default playbook %q", playbookName(cfg.TackDefaultPlaybook)), nil
		}
	}
	return filepath.Join(dir, "playbook.yaml"), "default playbook", nil
}

func saveDefaultPlaybook(rel string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.TackDefaultPlaybook = rel
	return cfg.Save()
}

// pickPlaybookFn is a seam over the default-playbook picker.
var pickPlaybookFn = tui.SelectOne

// chooseDefaultPlaybook picks the default among found: the only one;
// on a terminal, the user's choice (starting on current or a preferred
// name); otherwise current, a preferred name, or the first.
func chooseDefaultPlaybook(found []string, current string, interactive bool) (string, error) {
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	}
	initial := preferredOf(found, current)
	if !interactive {
		return initial, nil
	}
	opts := make([]huh.Option[string], 0, len(found))
	for _, f := range found {
		opts = append(opts, huh.NewOption(fmt.Sprintf("%-24s %s", playbookName(f), f), f))
	}
	return pickPlaybookFn("Default playbook for 'pmox apply'", opts, initial)
}

// preferredOf picks the playbook to start from: current when present,
// else the first preferred entry-point name, else the first found.
func preferredOf(found []string, current string) string {
	for _, want := range append([]string{playbookName(current)}, preferredPlaybooks...) {
		for _, f := range found {
			if want != "" && playbookName(f) == want {
				return f
			}
		}
	}
	return found[0]
}

// gitURLRe matches scp-style git remotes (git@host:path).
var gitURLRe = regexp.MustCompile(`^[\w.-]+@[\w.-]+:\S+$`)

// isGitURL reports whether s is a git remote pmox can clone.
func isGitURL(s string) bool {
	for _, p := range []string{"https://", "http://", "ssh://", "git://", "file://"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return gitURLRe.MatchString(s)
}

// gitFn runs git with args (stdout/stderr to w). A seam for tests.
var gitFn = func(w io.Writer, args ...string) error {
	c := exec.Command("git", args...)
	c.Stdout, c.Stderr = w, w
	return c.Run()
}

// gitOrigin returns dir's origin URL ("" when dir isn't a git clone).
func gitOrigin(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return ""
	}
	var out bytes.Buffer
	if gitFn(&out, "-C", dir, "remote", "get-url", "origin") != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// confirmBackupFn is a seam over the backup confirmation.
var confirmBackupFn = tui.Confirm

// runApplyInitFromURL clones url into the tack dir — moving any existing
// dir aside first, after asking (or with -y) — then sets the default
// playbook.
func runApplyInitFromURL(cmd *cobra.Command, url string, yes bool) error {
	out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("%w: git is required to clone %s — install git", exitcode.ErrNotFound, url)
	}
	dir, err := tackDir()
	if err != nil {
		return err
	}
	if origin := gitOrigin(dir); origin == url {
		fmt.Fprintf(out, "%s is already cloned from %s — run 'pmox apply --update' to pull changes\n", dir, url)
		return nil
	}

	backup := ""
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		backup = fmt.Sprintf("%s.bak-%s", dir, time.Now().Format("20060102-150405"))
		if !yes {
			if !tui.Interactive() {
				return fmt.Errorf("%w: %s already has playbooks; pass -y to move it to %s and clone %s", exitcode.ErrUserInput, dir, filepath.Base(backup), url)
			}
			ok, err := confirmBackupFn(fmt.Sprintf("%s already has playbooks. Move it to %s and clone %s?", displayHome(dir), filepath.Base(backup), url), false)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: nothing changed", tui.ErrAborted)
			}
		}
		if err := os.Rename(dir, backup); err != nil {
			return fmt.Errorf("move %s aside: %w", dir, err)
		}
	} else if err == nil {
		_ = os.Remove(dir) // empty dir: let git create it
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}

	fmt.Fprintf(errw, "cloning %s …\n", url)
	if err := gitFn(errw, "clone", url, dir); err != nil {
		_ = os.RemoveAll(dir)
		if backup != "" {
			_ = os.Rename(backup, dir) // put the old playbooks back
		}
		return fmt.Errorf("git clone %s: %w", url, err)
	}
	fmt.Fprintf(out, "cloned %s into %s\n", url, displayHome(dir))
	if backup != "" {
		fmt.Fprintf(out, "previous playbooks kept in %s\n", displayHome(backup))
	}
	return settleDefaultPlaybook(cmd, dir, "")
}

// settleDefaultPlaybook discovers dir's playbooks and records the
// default (asking when there are several and a terminal).
func settleDefaultPlaybook(cmd *cobra.Command, dir, current string) error {
	out := cmd.OutOrStdout()
	found, err := discoverPlaybooks(dir)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), tui.Warnf("warning: no playbooks found (YAML files with a 'hosts:' play); add one, then set it with 'pmox apply --default <name>'"))
		return nil
	}
	interactive := tui.Interactive() && outputMode != "json"
	chosen, err := chooseDefaultPlaybook(found, current, interactive)
	if err != nil {
		return err
	}
	if err := saveDefaultPlaybook(chosen); err != nil {
		return err
	}
	fmt.Fprintf(out, "found %d playbook(s); default: %s\n", len(found), playbookName(chosen))
	if len(found) > 1 {
		fmt.Fprintf(out, "change it with 'pmox apply --default <name>'; run another with 'pmox apply <vm> <name>'\n")
	}
	return nil
}

// runApplySetDefault sets the default playbook by name, or with a
// picker on a terminal when no name is given.
func runApplySetDefault(cmd *cobra.Command, name string) error {
	dir, err := tackDir()
	if err != nil {
		return err
	}
	found, err := discoverPlaybooks(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(found) == 0 {
		return fmt.Errorf("%w: no playbooks in %s — run 'pmox apply --init [url]' first", exitcode.ErrNotFound, displayHome(dir))
	}
	names := make([]string, len(found))
	for i, f := range found {
		names[i] = playbookName(f)
	}
	var chosen string
	switch {
	case name != "":
		for _, f := range found {
			if playbookName(f) == playbookName(name) {
				chosen = f
			}
		}
		if chosen == "" {
			return fmt.Errorf("%w: no playbook named %q; available: %s", exitcode.ErrNotFound, name, strings.Join(names, ", "))
		}
	case tui.Interactive() && outputMode != "json":
		cfg, _ := config.Load()
		current := ""
		if cfg != nil {
			current = cfg.TackDefaultPlaybook
		}
		if chosen, err = chooseDefaultPlaybook(found, current, true); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: name the default playbook: pmox apply --default <name> (available: %s)", exitcode.ErrUserInput, strings.Join(names, ", "))
	}
	if err := saveDefaultPlaybook(chosen); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "default playbook: %s\n", playbookName(chosen))
	return nil
}

// runApplyUpdate pulls the cloned playbook repo and re-checks the
// default.
func runApplyUpdate(cmd *cobra.Command) error {
	dir, err := tackDir()
	if err != nil {
		return err
	}
	origin := gitOrigin(dir)
	if origin == "" {
		return fmt.Errorf("%w: %s is not a git clone — 'pmox apply --init <url>' clones a playbook repo", exitcode.ErrUserInput, displayHome(dir))
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "pulling %s …\n", origin)
	if err := gitFn(cmd.ErrOrStderr(), "-C", dir, "pull", "--ff-only"); err != nil {
		return fmt.Errorf("git pull: %w", err)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.TackDefaultPlaybook != "" {
		if _, ok := findPlaybook(dir, cfg.TackDefaultPlaybook); ok {
			fmt.Fprintf(cmd.OutOrStdout(), "updated; default playbook: %s\n", playbookName(cfg.TackDefaultPlaybook))
			return nil
		}
		fmt.Fprintln(cmd.ErrOrStderr(), tui.Warnf(fmt.Sprintf("the default playbook %q is gone after the update", cfg.TackDefaultPlaybook)))
	}
	return settleDefaultPlaybook(cmd, dir, cfg.TackDefaultPlaybook)
}

// displayHome shortens a path under $HOME to ~/….
func displayHome(p string) string {
	home, _ := os.UserHomeDir()
	return displayPath(p, home)
}

// knownPlaybookNames lists the names available for messages.
func knownPlaybookNames() []string {
	dir, err := tackDir()
	if err != nil {
		return nil
	}
	found, _ := discoverPlaybooks(dir)
	names := make([]string, 0, len(found))
	for _, f := range found {
		names = append(names, playbookName(f))
	}
	return slices.Compact(names)
}

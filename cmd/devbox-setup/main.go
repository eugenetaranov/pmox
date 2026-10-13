// Command devbox-setup picks what to install on this machine, then
// installs it. It is the Go version of internal/bootstrap/devbox-setup,
// built for linux and copied to VMs by pmox.
//
//	sudo devbox-setup             choose tools, languages, AI and settings
//	sudo devbox-setup --yes       re-apply everything chosen so far, no questions
//	devbox-setup --dry-run        ask, then show what would happen
//	sudo devbox-setup --profile team.toml --profile me.toml [--yes]
//	                              apply profiles (review first without --yes)
//	sudo devbox-setup export [--lock] [file]
//	                              write the saved choices as a profile
//	                              (--lock: with the installed versions)
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/creds"
	"github.com/eugenetaranov/pmox/internal/devbox/plan"
	"github.com/eugenetaranov/pmox/internal/devbox/profile"
	"github.com/eugenetaranov/pmox/internal/devbox/run"
	"github.com/eugenetaranov/pmox/internal/devbox/steps"
	"github.com/eugenetaranov/pmox/internal/devbox/ui"
	"golang.org/x/term"
)

// version is set by the build (-X main.version=...).
var version = "dev"

const (
	defaultState = "/var/lib/devbox-setup"
	defaultLog   = "/var/log/devbox-setup.log"
	// defaultLegacy is the bash installer the legacy steps run (until
	// every item is ported).
	defaultLegacy = "/usr/local/share/devbox-setup/devbox-setup.sh"
)

func main() {
	if bashHandles(os.Args[1:]) {
		script := envOr("DEVBOX_SETUP_LEGACY", defaultLegacy)
		err := handoff(script, os.Args[1:])
		fmt.Fprintf(os.Stderr, "devbox-setup: %v\n", err)
		os.Exit(1)
	}
	if os.Geteuid() != 0 && needsRoot(os.Args[1:]) {
		err := asRoot(os.Args[1:])
		fmt.Fprintf(os.Stderr, "devbox-setup: %v\n", err)
		os.Exit(1)
	}
	if err := cli(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "devbox-setup: %v\n", err)
		os.Exit(1)
	}
}

type options struct {
	yes, dryRun, verify, showVersion, list bool
	state, log, legacy, user               string
	profiles                               list
	// lock is the profiles' [lock] tables, merged.
	lock *answers.Lock
}

// list is a repeatable string flag.
type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func cli(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("devbox-setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.BoolVar(&o.yes, "yes", false, "re-apply everything chosen so far, no questions")
	fs.BoolVar(&o.yes, "y", false, "short for --yes")
	fs.BoolVar(&o.dryRun, "dry-run", false, "show what would happen; change nothing")
	fs.BoolVar(&o.dryRun, "n", false, "short for --dry-run")
	fs.BoolVar(&o.verify, "verify", false, "check that the saved items are installed; change nothing")
	fs.BoolVar(&o.showVersion, "version", false, "print the version")
	fs.BoolVar(&o.list, "list", false, "list the catalog's items (id, tab, label)")
	fs.StringVar(&o.state, "state", envOr("DEVBOX_SETUP_STATE", defaultState), "state directory")
	fs.StringVar(&o.log, "log", envOr("DEVBOX_SETUP_LOG", defaultLog), "log file (every command and its output)")
	fs.Var(&o.profiles, "profile", "apply a profile (file or https URL; repeatable, later ones win)")
	fs.StringVar(&o.user, "user", "", "the account to set up, created if missing (wins over profiles)")
	o.legacy = envOr("DEVBOX_SETUP_LEGACY", defaultLegacy)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if o.showVersion {
		fmt.Fprintln(stdout, version)
		return nil
	}
	cat, err := catalog.Load(localCatalog(), confFS(o.share()))
	if err != nil {
		return err
	}
	if o.list {
		for _, it := range cat.Items {
			if !it.Hidden() {
				fmt.Fprintf(stdout, "%s\t%s\t%s\n", it.ID, it.Tab, it.Label)
			}
		}
		return nil
	}
	a, notes, err := answers.Read(o.state)
	if err != nil {
		return err
	}
	for _, n := range notes {
		fmt.Fprintln(stderr, "devbox-setup: "+n)
	}
	if fs.NArg() > 0 {
		if fs.Arg(0) == "export" {
			return o.export(stdout, stderr, cat, a, fs.Args()[1:])
		}
		return fmt.Errorf("unknown argument %q", fs.Arg(0))
	}
	interactive := !o.yes && !o.verify
	if o.user != "" && !profile.ValidUser(o.user) {
		return fmt.Errorf("--user %q is not a valid user name", o.user)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var dropped []string
	for _, src := range o.profiles {
		p, b, err := loadProfile(ctx, src)
		if err != nil {
			return err
		}
		if interactive && strings.HasPrefix(src, "https://") {
			fmt.Fprintf(stdout, "Profile %s:\n%s\n", src, b)
		}
		if p.Lock != nil {
			if o.lock == nil {
				o.lock = &answers.Lock{}
			}
			o.lock.Add(p.Lock)
		}
		d, warn := p.Apply(a, cat)
		for _, w := range warn {
			fmt.Fprintln(stderr, "devbox-setup: "+w)
		}
		dropped = answers.Merge(dropped, d)
	}
	// A later profile (or the same one, unnamed) may have kept it.
	dropped = slices.DeleteFunc(dropped, func(id string) bool { return slices.Contains(a.Picks, id) })
	changed := len(o.profiles) > 0 || (o.user != "" && o.user != a.User)
	if o.user != "" {
		a.User = o.user
	}
	if a.User == "" && len(o.profiles) > 0 {
		a.User = os.Getenv("SUDO_USER")
		if a.User == "" && !interactive {
			return errors.New("no user to set up: give --user, or run devbox-setup with sudo from that user's login")
		}
	}
	var items []*catalog.Item
	extra := a.MiseExtra
	sec := creds.New()
	if interactive {
		if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
			return errors.New("needs a terminal (or --yes to re-apply saved choices)")
		}
		k := &asker{cat: cat, a: a, share: o.share(), out: stdout, installed: func(t string) bool { return userHas(a.User, t) },
			profile: len(o.profiles) > 0, fixedUser: o.user != "", dropped: dropped}
		c, err := k.ask(ctx)
		if errors.Is(err, ui.ErrQuit) {
			fmt.Fprintln(stdout, "Cancelled.")
			return nil
		}
		if err != nil || c == nil {
			return err
		}
		a.Pick(c.picks)
		a.MiseExtra = answers.Merge(a.MiseExtra, c.miseExtra)
		items, extra, sec = c.items, c.miseExtra, c.secrets
		changed = true
	} else {
		if len(a.Picks) == 0 {
			return errors.New("no saved choices yet; run devbox-setup without --yes first")
		}
		if items, err = plan.Resolve(cat, a.Picks, len(a.MiseExtra) > 0); err != nil {
			return err
		}
		listDropped(stdout, cat, dropped)
	}
	created, err := o.ensureUser(ctx, a.User, stdout)
	if err != nil {
		return err
	}
	if changed && !o.dryRun && !o.verify {
		if err := answers.Write(o.state, a); err != nil {
			return err
		}
	}
	if o.dryRun || o.verify {
		env, err := o.env(a, sec, nil)
		if err != nil {
			return err
		}
		if o.dryRun {
			return run.DryRun(stdout, env, items, extra)
		}
		return verify(ctx, stdout, env, items)
	}
	logf, err := os.OpenFile(o.log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	fmt.Fprintf(logf, "=== devbox-setup %s, %s\n", version, time.Now().Format(time.RFC3339))
	env, err := o.env(a, sec, logf)
	if err != nil {
		return err
	}
	if created {
		env.Note(fmt.Sprintf("Created user %s (passwordless sudo, no password or SSH key): add a key to %s/.ssh/authorized_keys, or use 'sudo -iu %s'.", a.User, env.Home, a.User))
	}
	bash := legacyRunner(env, o.legacy, o.state)
	if bash != nil {
		stdin := sec.Encode()
		env.Legacy = func(ctx context.Context, id string) error {
			// The GitHub steps may open the browser login: in an
			// interactive run they get the terminal.
			if interactive && (id == "github" || id == "gitsign") {
				c := exec.CommandContext(ctx, "bash", o.legacy, "--only", id, "--secrets")
				c.Env = append(os.Environ(), legacyCmd(o.legacy, o.state).Env...)
				c.Stdin, c.Stdout, c.Stderr = bytes.NewReader(stdin), os.Stdout, os.Stderr
				return c.Run()
			}
			cmd := legacyCmd(o.legacy, o.state, "--only", id, "--secrets")
			cmd.Stdin = stdin
			_, err := env.RunLogged(ctx, cmd)
			return err
		}
		if err := bash(ctx, "--begin"); err != nil {
			return err
		}
	}
	notesFile := filepath.Join(o.state, ".notes")
	if bash == nil {
		_ = os.Remove(notesFile) // bash's --begin does this when it runs
	}
	r := &run.Runner{Env: env, Progress: run.Plain{W: stdout}, TailLines: 25}
	res := r.Run(ctx, items, extra)
	if err := primeZshCompletions(ctx, env, r.Progress); err != nil {
		res.Failed = append(res.Failed, "zshcomp")
		res.Errors["zshcomp"] = err.Error()
	}
	// While bash runs steps, its notes (with "Secrets to fill in") are the
	// fuller ~/devbox-setup.txt.
	if bash != nil {
		if out, err := bashOut(ctx, env, o.legacy, o.state, "--finish"); err != nil {
			fmt.Fprintf(stderr, "devbox-setup: notes: %v\n", err)
		} else {
			_, _ = stdout.Write(out)
		}
	} else {
		if b, err := os.ReadFile(notesFile); err == nil && len(b) > 0 {
			for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				fmt.Fprintf(stdout, "! %s\n", l)
			}
		}
		if err := writeSummary(env, items, extra); err != nil {
			fmt.Fprintf(stderr, "devbox-setup: %s/devbox-setup.txt: %v\n", env.Home, err)
		}
	}
	fmt.Fprintf(stdout, "How each tool was installed and how to update it: %s/devbox-setup.txt\nLog: %s\n", env.Home, o.log)
	if !res.OK() {
		for _, id := range res.Failed {
			fmt.Fprintf(stderr, "✗ %s: %s\n", id, res.Errors[id])
		}
		return fmt.Errorf("failed: %s — fix and re-run devbox-setup", strings.Join(res.Failed, " "))
	}
	return nil
}

// primeZshCompletions fills the zsh completion cache once every tool of
// the run is installed, so the first login doesn't pay for it.
func primeZshCompletions(ctx context.Context, env *steps.Env, p run.Progress) error {
	if _, err := os.Stat(env.Path("~/.config/devbox/completions.zsh")); err != nil {
		return nil
	}
	if _, err := exec.LookPath("zsh"); err != nil {
		return nil
	}
	const title = "zsh completions for installed tools"
	p.Start(title)
	_, err := env.RunLogged(ctx, steps.Cmd{Name: "zsh", Args: []string{"-c", "source ~/.config/devbox/completions.zsh && devbox_comp_refresh"}, AsUser: true})
	if err != nil {
		p.Done("fail", title)
		return err
	}
	p.Done("ok", title)
	return nil
}

func verify(ctx context.Context, w io.Writer, env *steps.Env, items []*catalog.Item) error {
	missing := 0
	for _, s := range run.Verify(ctx, env, items) {
		if s.Item.Hidden() {
			continue
		}
		switch s.State {
		case "ok":
			fmt.Fprintf(w, "ok         %s\n", s.Item.Label)
		case "unchecked":
			fmt.Fprintf(w, "unchecked  %s (not ported yet)\n", s.Item.Label)
		default:
			missing++
			fmt.Fprintf(w, "missing    %s: %s\n", s.Item.Label, s.Detail)
		}
	}
	if missing > 0 {
		return fmt.Errorf("%d item(s) missing", missing)
	}
	return nil
}

// env is the steps' Env for the run: the user's, with the saved answers
// and this run's secrets. Notes go to the state's .notes (shown at the
// end of the run) and the log.
func (o *options) env(a *answers.Answers, sec *creds.Secrets, log io.Writer) (*steps.Env, error) {
	env, err := newEnv(a.User, o.share(), log, o.dryRun)
	if err != nil {
		return nil, err
	}
	env.Share, env.State, env.Answers, env.Secrets, env.Lock = o.share(), o.state, a, sec, o.lock
	env.Note = func(text string) {
		if log != nil {
			fmt.Fprintf(log, "NOTE: %s\n", text)
		}
		f, err := os.OpenFile(filepath.Join(o.state, ".notes"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintln(f, text)
	}
	return env, nil
}

// share is the directory devbox-setup's files are installed in (next to
// the bash script).
func (o *options) share() string { return filepath.Dir(o.legacy) }

// confFS is share's conf directory, which file steps read, when it's
// there (it isn't on a developer's machine).
func confFS(share string) fs.FS {
	dir := filepath.Join(share, "conf")
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return os.DirFS(dir)
	}
	return nil
}

// localCatalog is /etc/devbox-setup/catalog.d, if there is one.
func localCatalog() fs.FS {
	if fi, err := os.Stat(catalog.LocalDir); err == nil && fi.IsDir() {
		return os.DirFS(catalog.LocalDir)
	}
	return nil
}

// loadProfile reads a profile from a file or an https URL; b is its text.
func loadProfile(ctx context.Context, src string) (p *profile.Profile, b []byte, err error) {
	switch {
	case strings.HasPrefix(src, "https://"):
		b, err = download(ctx, src)
	case strings.Contains(src, "://"):
		return nil, nil, fmt.Errorf("%s: profiles load from a file or an https URL", src)
	default:
		b, err = os.ReadFile(src)
	}
	if err != nil {
		return nil, nil, err
	}
	p, err = profile.Parse(src, b)
	return p, b, err
}

// listDropped prints the picks named profiles dropped.
func listDropped(w io.Writer, cat *catalog.Catalog, ids []string) {
	if len(ids) == 0 {
		return
	}
	fmt.Fprintln(w, "Dropped by the profile (still installed, no longer re-applied):")
	for _, id := range ids {
		label := id
		if it := cat.Item(id); it != nil {
			label = fmt.Sprintf("%s (%s)", it.Label, id)
		}
		fmt.Fprintf(w, "  - %s\n", label)
	}
}

// ensureUser creates the target user when it doesn't exist (a dry run
// says it would; verify fails).
func (o *options) ensureUser(ctx context.Context, name string, w io.Writer) (created bool, err error) {
	if _, err := user.Lookup(name); err == nil {
		return false, nil
	}
	switch {
	case o.verify:
		return false, fmt.Errorf("user %q does not exist", name)
	case o.dryRun:
		fmt.Fprintf(w, "Would create user %s (passwordless sudo, the groups of the user running sudo)\n", name)
		return false, nil
	}
	if err := createUser(ctx, steps.OSExec{}, name, os.Getenv("SUDO_USER")); err != nil {
		return false, err
	}
	fmt.Fprintf(w, "Created user %s.\n", name)
	return true, nil
}

// export writes the saved answers as a profile, to file or stdout. With
// --lock it adds the installed versions of the plan's mise tools and apt
// packages.
func (o *options) export(stdout, stderr io.Writer, cat *catalog.Catalog, a *answers.Answers, args []string) error {
	fs := flag.NewFlagSet("devbox-setup export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	lock := fs.Bool("lock", false, "add a [lock] table with the installed mise and apt versions")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("export: unexpected argument %q", fs.Arg(1))
	}
	file := fs.Arg(0)
	if len(a.Picks) == 0 {
		return errors.New("no saved choices to export yet")
	}
	var l *answers.Lock
	if *lock {
		var err error
		if l, err = o.versions(context.Background(), stderr, cat, a); err != nil {
			return err
		}
	}
	b := profile.Export(a, l)
	if file == "" {
		_, err := stdout.Write(b)
		return err
	}
	return os.WriteFile(file, b, 0o644)
}

// versions reads the installed versions of the saved plan's mise tools
// and apt packages.
func (o *options) versions(ctx context.Context, stderr io.Writer, cat *catalog.Catalog, a *answers.Answers) (*answers.Lock, error) {
	items, err := plan.Resolve(cat, a.Picks, len(a.MiseExtra) > 0)
	if err != nil {
		return nil, err
	}
	env, err := o.env(a, creds.New(), nil)
	if err != nil {
		return nil, err
	}
	var tools, pkgs []string
	for _, it := range items {
		for _, s := range it.Steps {
			switch s.Type {
			case "mise":
				tools = append(tools, s.Tools...)
			case "apt":
				for _, p := range s.Packages {
					if !strings.Contains(p, "{{") { // per-kernel names don't carry over
						pkgs = append(pkgs, p)
					}
				}
			}
		}
	}
	l, missing := steps.Versions(ctx, env, answers.Merge(tools, a.MiseExtra), answers.Merge(nil, pkgs))
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "devbox-setup: not installed, so not locked: %s\n", strings.Join(missing, " "))
	}
	return l, nil
}

// legacyRunner runs the bash installer with args (logged), or is nil when
// it isn't installed.
func legacyRunner(env *steps.Env, script, state string) func(ctx context.Context, args ...string) error {
	if _, err := os.Stat(script); err != nil {
		return nil
	}
	return func(ctx context.Context, args ...string) error {
		_, err := env.RunLogged(ctx, legacyCmd(script, state, args...))
		return err
	}
}

func bashOut(ctx context.Context, env *steps.Env, script, state string, args ...string) ([]byte, error) {
	return env.Exec.Run(ctx, legacyCmd(script, state, args...))
}

func legacyCmd(script, state string, args ...string) steps.Cmd {
	return steps.Cmd{Name: "bash", Args: append([]string{script}, args...),
		Env: []string{"DEVBOX_SETUP_STATE=" + state, "DEVBOX_SETUP_SHARE=" + filepath.Dir(script)}}
}

func writeSummary(env *steps.Env, items []*catalog.Item, extra []string) error {
	p := filepath.Join(env.Home, "devbox-setup.txt")
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	run.Summary(f, items, extra, time.Now())
	fmt.Fprintln(f, "Run 'sudo devbox-setup' again to add more.")
	if err := f.Close(); err != nil {
		return err
	}
	if env.Chown {
		return os.Chown(p, env.UID, env.GID)
	}
	return nil
}

// bashHandles says whether the bash devbox-setup runs this command line:
// forget isn't ported yet, and its help describes both.
func bashHandles(args []string) bool {
	for _, a := range args {
		switch a {
		case "forget", "-h", "--help", "-help":
			return true
		}
	}
	return false
}

// userHas reports whether tool is on the user's PATH (mise shims included).
func userHas(user, tool string) bool {
	return exec.Command("runuser", "-u", user, "--", "sh", "-c", steps.UserShell, "_", "sh", "-c", "command -v "+tool).Run() == nil
}

// needsRoot: everything but --version and --list reads the root-only state.
func needsRoot(args []string) bool {
	for _, a := range args {
		switch a {
		case "--version", "-version", "--list", "-list":
			return false
		}
	}
	return true
}

// asRoot re-runs this command through sudo, keeping a forwarded SSH agent
// (as the bash devbox-setup does).
func asRoot(args []string) error {
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return errors.New("needs root: run it with sudo")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	argv := []string{"sudo"}
	if os.Getenv("SSH_AUTH_SOCK") != "" {
		argv = append(argv, "--preserve-env=SSH_AUTH_SOCK")
	}
	return syscall.Exec(sudo, append(append(argv, self), args...), os.Environ())
}

// handoff replaces this process with the bash devbox-setup.
func handoff(script string, args []string) error {
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("the picker isn't in this build and the bash devbox-setup (%s) is missing; use --yes", script)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return err
	}
	return syscall.Exec(bash, append([]string{"bash", script}, args...), os.Environ())
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

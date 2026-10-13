// Package steps runs catalog steps: each one can say what it would do
// (Plan), whether it's already done (Check), and do it (Apply). Commands go
// through an Execer and files through Env.Root, so tests run against a
// temp directory and a fake executor.
package steps

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/creds"
)

// Cmd is one command to run.
type Cmd struct {
	Name  string
	Args  []string
	Env   []string // extra KEY=value
	Stdin []byte
	// AsUser runs it as the target user, from their home, with mise's
	// shims on PATH.
	AsUser bool
}

func (c Cmd) String() string {
	s := c.Name
	for _, a := range c.Args {
		if a == "" || strings.ContainsAny(a, " \t'\"$\\") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		s += " " + a
	}
	if c.AsUser {
		s = "(as user) " + s
	}
	return s
}

// Execer runs commands; it returns combined output.
type Execer interface {
	Run(ctx context.Context, c Cmd) ([]byte, error)
}

// Env is what steps run against.
type Env struct {
	User, Group string
	UID, GID    int
	Home        string // the user's home, without Root
	Arch        string // dpkg architecture (amd64, arm64)
	Codename    string // Ubuntu/Debian codename
	Kernel      string // the running kernel's release (uname -r)
	Exec        Execer
	Files       fs.FS // the catalog's files (file/template src)
	// Root prefixes every absolute path (tests); "" in production.
	Root string
	// Log gets each command and its output.
	Log io.Writer
	// Chown is false in tests run as non-root.
	Chown bool
	// Legacy runs the bash installer for one item id (see LegacyStep).
	Legacy func(ctx context.Context, id string) error
	// Download fetches a URL (apt keys). Tests replace it.
	Download func(ctx context.Context, url string) ([]byte, error)

	// Share is the directory devbox-setup's files are installed in;
	// State holds its answers and created.json.
	Share, State string
	// Answers are the run's saved choices; Secrets what was typed this run
	// (never saved).
	Answers *answers.Answers
	Secrets *creds.Secrets
	// Note shows text after the run (a step that has to leave something
	// for the user to do).
	Note func(text string)
}

func (e *Env) note(text string) {
	if e.Note != nil {
		e.Note(text)
	}
}

// record notes something devbox-setup created, for `devbox-setup forget`.
func (e *Env) record(kind, value string) error {
	if e.State == "" {
		return nil
	}
	return answers.AddCreated(e.State, answers.Created{Kind: kind, Value: value})
}

// Path resolves p: "~/x" is under the user's home; everything under Root.
func (e *Env) Path(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(e.Home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Join(e.Root, p)
}

// RunLogged runs c with the command and its output in the log.
func (e *Env) RunLogged(ctx context.Context, c Cmd) ([]byte, error) { return e.run(ctx, c) }

func (e *Env) run(ctx context.Context, c Cmd) ([]byte, error) {
	if e.Log != nil {
		fmt.Fprintf(e.Log, "$ %s\n", c)
	}
	out, err := e.Exec.Run(ctx, c)
	if e.Log != nil && len(out) > 0 {
		_, _ = e.Log.Write(out)
		if out[len(out)-1] != '\n' {
			fmt.Fprintln(e.Log)
		}
	}
	if err != nil {
		return out, fmt.Errorf("%s: %w", c.Name, err)
	}
	return out, nil
}

// owner says who owns path to: the user for ~/ paths unless the step says
// root, else root.
func (e *Env) owner(s catalog.Step) (uid, gid int, user bool) {
	user = s.Owner == "user" || (s.Owner == "" && strings.HasPrefix(s.To, "~"))
	if user {
		return e.UID, e.GID, true
	}
	return 0, 0, false
}

func (e *Env) chown(p string, uid, gid int) error {
	if !e.Chown {
		return nil
	}
	return os.Lchown(p, uid, gid)
}

// Action is one thing a step would do, for --dry-run.
type Action struct {
	Kind   string // apt, mise, repo, file, line, link, clone, unit, sysctl, shell, run, legacy
	Target string
}

func (a Action) String() string { return a.Kind + " " + a.Target }

// Step is a runnable catalog step.
type Step interface {
	Plan(e *Env) []Action
	// Check reports whether the step has nothing to do.
	Check(ctx context.Context, e *Env) (bool, error)
	Apply(ctx context.Context, e *Env) error
}

// Follower is a step that only runs when an earlier step of the same item
// changed something (a restart, a daemon-reload).
type Follower interface{ Follows() bool }

// BeforeApt is a step the runner applies with the apt repositories, before
// the batch installs packages (a config a package reads when it first
// starts).
type BeforeApt interface{ BeforeApt() bool }

// Always is a step that runs on every run, after its item's other steps,
// and is idempotent itself (registering MCP servers): it doesn't count as
// a change, and a failure is a note, not a failed item.
type Always interface{ Always() bool }

// GoStep makes a registered Go step for an item.
type GoStep func(s catalog.Step) Step

var goSteps = map[string]GoStep{}

// RegisterGo registers a `go:<name>` step.
func RegisterGo(name string, f GoStep) {
	goSteps[name] = f
	catalog.GoSteps[name] = true
}

// New makes the runnable step for s, for item id.
func New(id string, s catalog.Step) (Step, error) {
	switch s.Type {
	case "apt":
		return aptStep{s}, nil
	case "mise":
		return miseStep{s}, nil
	case "apt_repo":
		return aptRepoStep{s}, nil
	case "file":
		return fileStep{s, false}, nil
	case "template":
		return fileStep{s, true}, nil
	case "link":
		return linkStep{s}, nil
	case "line":
		return lineStep{s}, nil
	case "git_clone":
		return gitCloneStep{s}, nil
	case "systemctl":
		return systemctlStep{s}, nil
	case "sysctl":
		return sysctlStep{s}, nil
	case "git_config":
		return gitConfigStep{s}, nil
	case "user_shell":
		return userShellStep{s}, nil
	case "run":
		return runStep{s}, nil
	case "note":
		return noteStep{s}, nil
	case "legacy":
		return LegacyStep{ID: id}, nil
	}
	if name, ok := strings.CutPrefix(s.Type, "go:"); ok {
		if f := goSteps[name]; f != nil {
			return f(s), nil
		}
		return nil, fmt.Errorf("no Go step %q", name)
	}
	return nil, fmt.Errorf("unknown step type %q", s.Type)
}

// LegacyStep runs the bundled bash installer for one item, until the item
// is ported to Go steps.
type LegacyStep struct{ ID string }

func (l LegacyStep) Plan(*Env) []Action {
	return []Action{{"legacy", "devbox-setup.sh --only " + l.ID}}
}

// Check is false: the bash functions are idempotent and cheap to re-run.
func (LegacyStep) Check(context.Context, *Env) (bool, error) { return false, nil }

func (l LegacyStep) Apply(ctx context.Context, e *Env) error {
	if e.Legacy == nil {
		return fmt.Errorf("%s: the bash installer isn't available", l.ID)
	}
	return e.Legacy(ctx, l.ID)
}

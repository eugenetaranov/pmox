package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

// Credentials: devbox-setup never stores them. A cred step writes the
// tool's own file (0600) with what was typed this run; a skipped value
// stays empty, and ~/devbox-setup.txt lists it under "Secrets to fill in".

func init() {
	RegisterGo("registries", func(catalog.Step) Step { return registriesStep{} })
	RegisterGo("agekey", func(catalog.Step) Step { return ageKeyStep{} })
}

// The ~/.npmrc block devbox-setup owns.
const (
	NpmrcBegin = "# >>> devbox-setup npmrc"
	NpmrcEnd   = "# <<< devbox-setup npmrc"
)

type credStep struct {
	id string
	s  catalog.Step
}

func (c credStep) Plan(*Env) []Action { return []Action{{"cred", c.s.To + " (0600)"}} }

// typedCred is what was typed for the step's item this run.
func (c credStep) typed(e *Env, key string) string {
	if e.Secrets == nil {
		return ""
	}
	return e.Secrets.Cred[c.id+" "+key]
}

// want is the file's content: nil when there's nothing to write (npmrc
// without a scope).
func (c credStep) want(e *Env, cur []byte, exists bool) []byte {
	switch c.s.Format {
	case "npmrc":
		return c.npmrc(e, cur)
	default:
		b := cur
		if !exists {
			b = c.skeleton()
		}
		for _, k := range c.s.Keys {
			if v := c.typed(e, k.Name); v != "" {
				b = EnvSet(b, k.Name, v)
			}
		}
		return b
	}
}

// skeleton is a new env file: the header, then each key, empty, under its
// prompt and where to get it.
func (c credStep) skeleton() []byte {
	var b strings.Builder
	b.WriteString(strings.TrimRight(c.s.Header, "\n") + "\n")
	for _, k := range c.s.Keys {
		fmt.Fprintf(&b, "\n# %s: %s\n%s=\n", k.Prompt, k.How, k.Name)
	}
	return []byte(b.String())
}

func (c credStep) key(name string) catalog.CredKey {
	for _, k := range c.s.Keys {
		if k.Name == name {
			return k
		}
	}
	return catalog.CredKey{}
}

func npmScope(e *Env) string {
	if e.Answers == nil {
		return ""
	}
	return e.Answers.NPM.Scope
}

// npmrc is cur with devbox-setup's block (scope, registry, token) at the
// end: the token typed this run, else the one already there.
func (c credStep) npmrc(e *Env, cur []byte) []byte {
	scope := npmScope(e)
	if scope == "" {
		return nil
	}
	reg := c.key("NPM_REGISTRY").Default
	if e.Answers.NPM.Registry != "" {
		reg = e.Answers.NPM.Registry
	}
	reg = strings.TrimRight(reg, "/")
	tok := c.typed(e, "NPM_TOKEN")
	var keep []string
	in := false
	for _, l := range strings.Split(strings.TrimSuffix(string(cur), "\n"), "\n") {
		switch {
		case l == NpmrcBegin:
			in = true
		case l == NpmrcEnd:
			in = false
		case in:
			if i := strings.Index(l, ":_authToken="); i >= 0 && tok == "" {
				tok = l[i+len(":_authToken="):]
			}
		case len(cur) > 0:
			keep = append(keep, l)
		}
	}
	host := reg
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	block := []string{NpmrcBegin, scope + ":registry=" + reg + "/", "//" + host + "/:_authToken=" + tok, NpmrcEnd}
	return []byte(strings.Join(append(keep, block...), "\n") + "\n")
}

func (c credStep) Check(_ context.Context, e *Env) (bool, error) {
	p := e.Path(c.s.To)
	cur, err := os.ReadFile(p)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	want := c.want(e, cur, exists)
	if want == nil {
		return true, nil
	}
	if !exists || !bytes.Equal(cur, want) {
		return false, nil
	}
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().Perm() == 0o600, nil
}

func (c credStep) Apply(_ context.Context, e *Env) error {
	p := e.Path(c.s.To)
	cur, err := os.ReadFile(p)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	want := c.want(e, cur, exists)
	if want == nil {
		return nil
	}
	// A file devbox-setup created is its own (forget removes it); in the
	// user's own ~/.npmrc it owns only its block.
	home := filepath.Join(e.Home, strings.TrimPrefix(c.s.To, "~/"))
	switch {
	case !exists:
		err = e.record("file", home)
	case c.s.Format == "npmrc" && !slices.Contains(answers.ReadCreated(e.State), answers.Created{Kind: "file", Value: home}):
		err = e.record("block", home)
	}
	if err != nil {
		return err
	}
	if err := mkdirFor(e, p, c.s); err != nil {
		return err
	}
	if err := writeFile(p, want, 0o600); err != nil {
		return err
	}
	return e.chown(p, e.UID, e.GID)
}

// --- go:registries -----------------------------------------------------------

// registriesStep logs Docker into each chosen registry with the username
// and token typed this run; ghcr.io falls back to gh's token when it has
// read:packages. One that is logged in already is left alone.
type registriesStep struct{}

func (registriesStep) registries(e *Env) []string {
	if e.Answers == nil {
		return nil
	}
	return e.Answers.Registries
}

func (r registriesStep) Plan(e *Env) []Action {
	var out []Action
	for _, reg := range r.registries(e) {
		out = append(out, Action{"run", "docker login " + reg + " (the token typed, if any)"})
	}
	return out
}

// loggedIn reports whether ~/.docker/config.json has a login for reg.
func loggedIn(e *Env, reg string) bool {
	b, err := os.ReadFile(e.Path("~/.docker/config.json"))
	if err != nil {
		return false
	}
	var c struct {
		Auths       map[string]json.RawMessage `json:"auths"`
		CredHelpers map[string]json.RawMessage `json:"credHelpers"`
	}
	if json.Unmarshal(b, &c) != nil {
		return false
	}
	_, a := c.Auths[reg]
	_, h := c.CredHelpers[reg]
	return a || h
}

// login is the username and password for reg, or "" when there are none.
func (registriesStep) login(ctx context.Context, e *Env, reg string) (user, pass string) {
	if e.Secrets != nil {
		user, pass = e.Secrets.RegUser[reg], e.Secrets.RegPass[reg]
	}
	if pass == "" && reg == "ghcr.io" {
		if out, err := e.Exec.Run(ctx, Cmd{Name: "gh", Args: []string{"auth", "status", "-h", "github.com"}, AsUser: true}); err == nil &&
			strings.Contains(string(out), "'read:packages'") {
			tok, _ := e.Exec.Run(ctx, Cmd{Name: "gh", Args: []string{"auth", "token"}, AsUser: true})
			pass = strings.TrimSpace(string(tok))
			if user == "" {
				login, _ := e.Exec.Run(ctx, Cmd{Name: "gh", Args: []string{"api", "user", "-q", ".login"}, AsUser: true})
				user = strings.TrimSpace(string(login))
			}
		}
	}
	if user == "" || pass == "" {
		return "", ""
	}
	return user, pass
}

func (r registriesStep) Check(ctx context.Context, e *Env) (bool, error) {
	for _, reg := range r.registries(e) {
		if loggedIn(e, reg) {
			continue
		}
		if u, _ := r.login(ctx, e, reg); u != "" {
			return false, nil
		}
	}
	return true, nil
}

func (r registriesStep) Apply(ctx context.Context, e *Env) error {
	var errs []error
	for _, reg := range r.registries(e) {
		if loggedIn(e, reg) {
			continue
		}
		user, pass := r.login(ctx, e, reg)
		if user == "" {
			continue
		}
		if _, err := e.run(ctx, Cmd{Name: "docker", Args: []string{"login", reg, "-u", user, "--password-stdin"}, Stdin: []byte(pass), AsUser: true}); err != nil {
			errs = append(errs, fmt.Errorf("docker login %s: %w", reg, err))
			continue
		}
		if err := e.record("registry", reg); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// --- go:agekey ---------------------------------------------------------------

// ageKeyStep sets up ~/.config/sops/age/keys.txt (0600) as chosen this run:
// generate a key, paste one, or leave a commented file to fill in.
type ageKeyStep struct{}

const ageKeys = "~/.config/sops/age/keys.txt"

func (ageKeyStep) choice(e *Env) (string, string) {
	if e.Secrets == nil {
		return "", ""
	}
	return e.Secrets.AgeChoice, e.Secrets.AgeKey
}

func (ageKeyStep) Plan(*Env) []Action { return []Action{{"cred", ageKeys + " (0600)"}} }

// HasAgeKey reports whether an age keys file has a secret key line.
func HasAgeKey(b []byte) bool {
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "AGE-SECRET-KEY-") {
			return true
		}
	}
	return false
}

func (a ageKeyStep) Check(_ context.Context, e *Env) (bool, error) {
	p := e.Path(ageKeys)
	b, err := os.ReadFile(p)
	if err != nil {
		return false, nil
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		return false, nil
	}
	choice, _ := a.choice(e)
	return (choice != "generate" && choice != "paste") || HasAgeKey(b), nil
}

func (a ageKeyStep) Apply(ctx context.Context, e *Env) error {
	p := e.Path(ageKeys)
	home := filepath.Join(e.Home, strings.TrimPrefix(ageKeys, "~/"))
	cur, err := os.ReadFile(p)
	exists := err == nil
	if err := mkdirFor(e, p, catalog.Step{To: ageKeys}); err != nil {
		return err
	}
	choice, key := a.choice(e)
	if !HasAgeKey(cur) && (choice == "generate" || choice == "paste") {
		if !exists {
			if err := e.record("file", home); err != nil {
				return err
			}
		}
		switch choice {
		case "generate":
			_ = os.Remove(p)
			if _, err := e.run(ctx, Cmd{Name: "sh", Args: []string{"-c", `umask 077; age-keygen -o "$1"`, "_", p}, AsUser: true}); err != nil {
				return err
			}
			if pub, err := e.Exec.Run(ctx, Cmd{Name: "age-keygen", Args: []string{"-y", p}, AsUser: true}); err == nil {
				e.note("age: new key; its public key is " + strings.TrimSpace(string(pub)))
			}
		case "paste":
			if err := writeFile(p, append(cur, strings.TrimSpace(key)+"\n"...), 0o600); err != nil {
				return err
			}
		}
		exists = true
	}
	if !exists {
		if err := e.record("file", home); err != nil {
			return err
		}
		if err := writeFile(p, []byte("# age key for sops: paste a line AGE-SECRET-KEY-1… here, or run: age-keygen -o ~/.config/sops/age/keys.txt\n"), 0o600); err != nil {
			return err
		}
	}
	if err := os.Chmod(p, 0o600); err != nil {
		return err
	}
	return e.chown(p, e.UID, e.GID)
}

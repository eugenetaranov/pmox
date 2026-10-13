package steps

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/creds"
)

func credEnv(t *testing.T, x *fakeExec) *Env {
	t.Helper()
	e := testEnv(t, x)
	e.State = t.TempDir()
	e.Answers = &answers.Answers{}
	e.Secrets = creds.New()
	return e
}

func catalogStep(t *testing.T, id string, typ string) (string, catalog.Step) {
	t.Helper()
	c, err := catalog.Load(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range c.Item(id).Steps {
		if s.Type == typ {
			return id, s
		}
	}
	t.Fatalf("%s has no %s step", id, typ)
	return "", catalog.Step{}
}

func credOf(t *testing.T, id string) Step {
	t.Helper()
	id, s := catalogStep(t, id, "cred")
	st, err := New(id, s)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// agent.env: the same skeleton as the bash installer wrote, the typed key
// in it, 0600, recorded as created.
func TestCredEnv(t *testing.T) {
	e := credEnv(t, &fakeExec{})
	st := credOf(t, "agentkeys")
	e.Secrets.Cred["agentkeys OPENAI_API_KEY"] = "sk-1"
	apply(t, e, st)
	p := e.Path("~/.config/devbox/agent.env")
	want := `# Agent API keys, written by devbox-setup and read by interactive shells.
# Fill in a key, then open a new shell. Not shared with services or cron.

# Anthropic API key (empty: log in with 'claude' instead): https://console.anthropic.com/settings/keys
ANTHROPIC_API_KEY=

# OpenAI API key (Codex): https://platform.openai.com/api-keys
OPENAI_API_KEY=sk-1

# Gemini API key: https://aistudio.google.com/apikey
GEMINI_API_KEY=
`
	if got := read(t, p); got != want {
		t.Errorf("agent.env:\n%s\nwant:\n%s", got, want)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	if !check(t, e, st) {
		t.Error("Check after Apply = false")
	}
	// A later run without typing keeps what the user filled in.
	put(t, p, strings.Replace(read(t, p), "GEMINI_API_KEY=", "GEMINI_API_KEY=mine", 1))
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	e.Secrets = creds.New()
	if !check(t, e, st) {
		t.Error("nothing typed: Check = false")
	}
	if got := answers.ReadCreated(e.State); len(got) != 1 || got[0].Value != "/home/ada/.config/devbox/agent.env" {
		t.Errorf("created = %+v", got)
	}
}

func TestCredNpmrc(t *testing.T) {
	e := credEnv(t, &fakeExec{})
	st := credOf(t, "npmrc")
	if !check(t, e, st) {
		t.Error("no scope: Check = false (the note says what to do)")
	}
	apply(t, e, st)
	if _, err := os.Stat(e.Path("~/.npmrc")); err == nil {
		t.Error(".npmrc written without a scope")
	}
	// The user's own .npmrc: devbox-setup adds (and owns) only its block.
	p := e.Path("~/.npmrc")
	put(t, p, "fund=false\n")
	e.Answers.NPM.Scope = "@acme"
	e.Secrets.Cred["npmrc NPM_TOKEN"] = "ghp_1"
	apply(t, e, st)
	want := "fund=false\n" + NpmrcBegin + "\n@acme:registry=https://npm.pkg.github.com/\n//npm.pkg.github.com/:_authToken=ghp_1\n" + NpmrcEnd + "\n"
	if got := read(t, p); got != want {
		t.Errorf(".npmrc = %q\nwant    %q", got, want)
	}
	if got := answers.ReadCreated(e.State); len(got) != 1 || got[0].Kind != "block" {
		t.Errorf("created = %+v", got)
	}
	// A new registry, no token typed: the block is replaced, the token kept.
	e.Secrets = creds.New()
	e.Answers.NPM.Registry = "https://npm.example.com/"
	if check(t, e, st) {
		t.Error("Check = true with the registry changed")
	}
	apply(t, e, st)
	want = "fund=false\n" + NpmrcBegin + "\n@acme:registry=https://npm.example.com/\n//npm.example.com/:_authToken=ghp_1\n" + NpmrcEnd + "\n"
	if got := read(t, p); got != want {
		t.Errorf(".npmrc = %q\nwant    %q", got, want)
	}
	if !check(t, e, st) {
		t.Error("Check after Apply = false")
	}
}

func TestRegistries(t *testing.T) {
	scopes := ""
	var e *Env
	auths := []string{`"quay.io": {}`}
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		switch {
		case c.Name == "docker":
			// docker login writes the user's config.
			auths = append(auths, `"`+c.Args[1]+`": {}`)
			put(t, e.Path("~/.docker/config.json"), `{"auths": {`+strings.Join(auths, ", ")+`}}`)
		case c.Name == "gh" && c.Args[0] == "auth" && c.Args[1] == "status":
			return []byte("Token scopes: 'gist', " + scopes), nil
		case c.Name == "gh" && c.Args[0] == "auth":
			return []byte("gho_tok\n"), nil
		case c.Name == "gh":
			return []byte("ada-gh\n"), nil
		}
		return nil, nil
	}}
	e = credEnv(t, x)
	e.Answers.Registries = []string{"ghcr.io", "registry.example.com", "quay.io"}
	e.Secrets.RegUser["registry.example.com"] = "ada"
	e.Secrets.RegPass["registry.example.com"] = "pw"
	put(t, e.Path("~/.docker/config.json"), `{"auths": {"quay.io": {}}}`)
	st := step(t, catalog.Step{Type: "go:registries"})
	apply(t, e, st)
	var logins []string
	for _, c := range x.ran {
		if c.Name == "docker" {
			logins = append(logins, c.String()+" <"+string(c.Stdin)+">")
		}
	}
	// ghcr.io: gh has no read:packages, so no login (it's left to fill in).
	if strings.Join(logins, "; ") != "(as user) docker login registry.example.com -u ada --password-stdin <pw>" {
		t.Errorf("logins: %q", logins)
	}
	scopes = "'read:packages'"
	x.ran = nil
	apply(t, e, st)
	if got := x.ran[len(x.ran)-1]; got.String() != "(as user) docker login ghcr.io -u ada-gh --password-stdin" || string(got.Stdin) != "gho_tok" {
		t.Errorf("last ran %s <%s>", got, got.Stdin)
	}
	if got := answers.ReadCreated(e.State); len(got) != 2 {
		t.Errorf("created = %+v, want the two logins devbox-setup made", got)
	}
	if !check(t, e, st) {
		t.Error("Check with every registry logged in = false")
	}
}

func TestAgeKey(t *testing.T) {
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if c.Name == "sh" && strings.Contains(c.Args[1], "age-keygen -o") {
			return nil, os.WriteFile(c.Args[3], []byte("# created\nAGE-SECRET-KEY-1GEN\n"), 0o600)
		}
		if c.Name == "age-keygen" {
			return []byte("age1pub\n"), nil
		}
		return nil, errors.New("unexpected")
	}}
	e := credEnv(t, x)
	var notes []string
	e.Note = func(s string) { notes = append(notes, s) }
	st := step(t, catalog.Step{Type: "go:agekey"})
	p := e.Path(ageKeys)
	// Later: a commented file to fill in.
	e.Secrets.AgeChoice = "later"
	apply(t, e, st)
	if got := read(t, p); !strings.HasPrefix(got, "# age key for sops") {
		t.Errorf("keys.txt = %q", got)
	}
	if !check(t, e, st) {
		t.Error("later: Check after Apply = false")
	}
	// Paste: the key goes in; the comment stays.
	e.Secrets.AgeChoice, e.Secrets.AgeKey = "paste", " AGE-SECRET-KEY-1PASTED \n"
	if check(t, e, st) {
		t.Error("paste: Check = true without a key")
	}
	apply(t, e, st)
	if got := read(t, p); !strings.HasSuffix(got, "\nAGE-SECRET-KEY-1PASTED\n") {
		t.Errorf("keys.txt = %q", got)
	}
	// Generate with a key there: nothing to do.
	e.Secrets.AgeChoice = "generate"
	if !check(t, e, st) {
		t.Error("generate with a key: Check = false")
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	// Generate on a fresh home.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	apply(t, e, st)
	if !strings.Contains(read(t, p), "AGE-SECRET-KEY-1GEN") || len(notes) != 1 || !strings.Contains(notes[0], "age1pub") {
		t.Errorf("keys.txt = %q, notes %q", read(t, p), notes)
	}
}

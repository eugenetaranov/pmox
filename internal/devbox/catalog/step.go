package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// Step is one install action of an item. Type picks the kind; each kind
// uses its own fields (see validate). Paths in To may start with "~/" (the
// target user's home).
type Step struct {
	Type string `toml:"type"`

	// apt; "{{kernel}}" in a name is the running kernel's release
	Packages []string `toml:"packages"`
	// mise (`mise use -g`, as the user)
	Tools []string `toml:"tools"`

	// apt_repo: a signing key and a sources entry. Name names both files;
	// Source is the deb822 body, with {{arch}}, {{codename}} and
	// {{keyring}} filled in.
	Name           string `toml:"name"`
	KeyURL         string `toml:"key_url"`
	KeyFingerprint string `toml:"key_fingerprint"`
	Source         string `toml:"source"`

	// file, template (Src in the catalog's files), link (Src is a path)
	Src      string            `toml:"src"`
	To       string            `toml:"to"`
	Mode     string            `toml:"mode"`
	Owner    string            `toml:"owner"` // "user" or "root" (default: user under ~/, else root)
	KeepOrig bool              `toml:"keep_orig"`
	IfAbsent bool              `toml:"if_absent"`
	Vars     map[string]string `toml:"vars"`

	// line: Line in the file To, once (appended after a blank line), or
	// in place of the lines matching the regexp Match. IfExists leaves a
	// missing file alone instead of creating it.
	Line     string `toml:"line"`
	Match    string `toml:"match"`
	IfExists bool   `toml:"if_exists"`
	// Top puts the line first instead (in .bashrc: before Ubuntu's
	// "not interactive: return" guard).
	Top bool `toml:"top"`

	// git_clone
	Repo string `toml:"repo"`
	Ref  string `toml:"ref"`

	// systemctl
	Unit   string `toml:"unit"`
	Action string `toml:"action"` // enable-now (default), restart, daemon-reload

	// user_shell
	Shell string `toml:"shell"`

	// git_config: the user's global git settings; with IfTool, only when
	// that command is installed. "{{git_name}}" and "{{git_email}}" are the
	// answers' identity; a setting whose value comes out empty is skipped.
	Values map[string]string `toml:"values"`
	IfTool string            `toml:"if_tool"`

	// run: a shell one-liner; Creates or Check makes a re-run a no-op.
	// {{user}}, {{group}}, {{home}} and {{arch}} in Cmd and Check are
	// filled in.
	Cmd     string `toml:"cmd"`
	AsUser  bool   `toml:"as_user"`
	Creates string `toml:"creates"`
	Check   string `toml:"check"`
	// OnChange runs it only after an earlier step of the item changed
	// something (sysctl --system after a new sysctl file), instead.
	OnChange bool `toml:"on_change"`

	// note: Text is shown after the run when the shell condition When
	// holds (always, without one). On a run step, Text is shown after the
	// run when the step ran.
	Text string `toml:"text"`
	When string `toml:"when"`

	// cred: a credential file the user fills in (To, mode 0600). Format
	// is env (KEY=value lines; a new file is Header plus a commented line
	// per key) or npmrc (a devbox-setup block in ~/.npmrc). Keys are asked
	// for, or skipped and left empty.
	Format string    `toml:"format"`
	Header string    `toml:"header"`
	Keys   []CredKey `toml:"key"`

	// Downloads: a run step that fetches with curl or wget pins what it
	// fetches (SHA256, used as {{sha256}} in Cmd, or per-architecture
	// pins checked in Cmd with sha256sum -c), or says why it can't.
	SHA256     string `toml:"sha256"`
	Unverified string `toml:"unverified"`
}

// CredKey is one value a cred step asks for.
type CredKey struct {
	Name   string `toml:"name"`
	Prompt string `toml:"prompt"`
	How    string `toml:"how"` // where to get it
	// When: asked only if this item is picked or this tool installed.
	When string `toml:"when"`
	// Answer is the saved answer a plain setting goes in (npm.scope,
	// npm.registry); none: a secret, read without echo and never saved.
	Answer  string `toml:"answer"`
	Default string `toml:"default"`
	// LoggedIn is a file whose presence means the tool has its own login,
	// so an empty key isn't missing.
	LoggedIn string `toml:"logged_in"`
}

// Secret reports whether the key is a secret.
func (k CredKey) Secret() bool { return k.Answer == "" }

// CredAnswers are the saved answers a cred key may set.
var CredAnswers = map[string]bool{"npm.scope": true, "npm.registry": true}

var keyNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

var (
	modeRe = regexp.MustCompile(`^0[0-7]{3}$`)
	hexRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	fprRe  = regexp.MustCompile(`^[0-9A-F]{40}$`)
	// GoSteps are the registered `go:<name>` step names; the steps package
	// fills it in (catalog doesn't import it).
	GoSteps = map[string]bool{}
)

func (s *Step) validate(files fs.FS) error {
	need := func(field, v string) error {
		if v == "" {
			return fmt.Errorf("needs %s", field)
		}
		return nil
	}
	src := func() error {
		if s.Src == "" {
			return errors.New("needs src")
		}
		if files == nil {
			return nil
		}
		if _, err := fs.Stat(files, s.Src); err != nil {
			return fmt.Errorf("src %q is not in devbox-setup's files", s.Src)
		}
		return nil
	}
	if s.Mode != "" && !modeRe.MatchString(s.Mode) {
		return fmt.Errorf("mode %q: want an octal like 0644", s.Mode)
	}
	if s.Owner != "" && s.Owner != "user" && s.Owner != "root" {
		return fmt.Errorf("owner %q: want user or root", s.Owner)
	}
	if s.SHA256 != "" && !hexRe.MatchString(s.SHA256) {
		return errors.New("sha256: want 64 lowercase hex digits")
	}
	switch {
	case s.Type == "apt":
		if len(s.Packages) == 0 {
			return errors.New("needs packages")
		}
	case s.Type == "mise":
		if len(s.Tools) == 0 {
			return errors.New("needs tools")
		}
	case s.Type == "apt_repo":
		for f, v := range map[string]string{"name": s.Name, "key_url": s.KeyURL, "source": s.Source} {
			if err := need(f, v); err != nil {
				return err
			}
		}
		if !fprRe.MatchString(s.KeyFingerprint) {
			return errors.New("key_fingerprint: want the signing key's 40 uppercase hex digits")
		}
	case s.Type == "file", s.Type == "template":
		if err := src(); err != nil {
			return err
		}
		return need("to", s.To)
	case s.Type == "link":
		if err := need("src", s.Src); err != nil {
			return err
		}
		return need("to", s.To)
	case s.Type == "line":
		if err := need("line", s.Line); err != nil {
			return err
		}
		if strings.Contains(s.Line, "\n") {
			return errors.New("line: one line only")
		}
		if _, err := regexp.Compile(s.Match); err != nil {
			return fmt.Errorf("match: %w", err)
		}
		return need("to", s.To)
	case s.Type == "git_clone":
		if err := need("repo", s.Repo); err != nil {
			return err
		}
		return need("to", s.To)
	case s.Type == "systemctl":
		switch s.Action {
		case "", "enable-now", "restart":
			return need("unit", s.Unit)
		case "daemon-reload":
		default:
			return fmt.Errorf("action %q: want enable-now, restart or daemon-reload", s.Action)
		}
	case s.Type == "sysctl":
		if err := src(); err != nil {
			return err
		}
		return need("to", s.To)
	case s.Type == "git_config":
		if len(s.Values) == 0 {
			return errors.New("needs values")
		}
	case s.Type == "user_shell":
		return need("shell", s.Shell)
	case s.Type == "run":
		if err := need("cmd", s.Cmd); err != nil {
			return err
		}
		if s.Creates == "" && s.Check == "" && !s.OnChange {
			return errors.New("needs creates, check or on_change, so a re-run is a no-op")
		}
		if err := s.pinned(); err != nil {
			return err
		}
	case s.Type == "note":
		return need("text", s.Text)
	case s.Type == "cred":
		if s.Format != "env" && s.Format != "npmrc" {
			return fmt.Errorf("format %q: want env or npmrc", s.Format)
		}
		if len(s.Keys) == 0 {
			return errors.New("needs keys")
		}
		for _, k := range s.Keys {
			if !keyNameRe.MatchString(k.Name) || k.Prompt == "" {
				return fmt.Errorf("key %q: needs an UPPER_CASE name and a prompt", k.Name)
			}
			if k.Answer != "" && !CredAnswers[k.Answer] {
				return fmt.Errorf("key %s: answer %q is not a saved answer", k.Name, k.Answer)
			}
		}
		return need("to", s.To)
	case s.Type == "legacy":
		// Runs the bundled bash installer for this item (until it's ported).
	case strings.HasPrefix(s.Type, "go:"):
		if !GoSteps[strings.TrimPrefix(s.Type, "go:")] && len(GoSteps) > 0 {
			return fmt.Errorf("no Go step %q", strings.TrimPrefix(s.Type, "go:"))
		}
	default:
		return fmt.Errorf("unknown step type %q", s.Type)
	}
	return nil
}

var fetches = regexp.MustCompile(`\b(curl|wget)\b`)

// Downloads reports whether a run step fetches something.
func (s Step) Downloads() bool { return s.Type == "run" && fetches.MatchString(s.Cmd) }

// pinned checks that a download is pinned or says why it isn't.
func (s Step) pinned() error {
	switch {
	case s.SHA256 != "" && !strings.Contains(s.Cmd, "{{sha256}}"):
		return errors.New("sha256 is set but the cmd doesn't check {{sha256}}")
	case !s.Downloads() || s.Unverified != "" || s.SHA256 != "" || strings.Contains(s.Cmd, "sha256sum -c"):
		return nil
	}
	return errors.New("downloads with curl or wget: pin it (sha256, or sha256sum -c in the cmd) or say why it can't be (unverified = \"…\")")
}

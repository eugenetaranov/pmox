package bootstrap

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// shellFns returns the named functions from the devbox-setup script.
func shellFns(t *testing.T, names ...string) string {
	t.Helper()
	b, err := os.ReadFile("devbox-setup")
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	var out strings.Builder
	vars := regexp.MustCompile(`(?m)^ANSWER_VARS="[^"]*"$`).FindString(script)
	if vars == "" {
		t.Fatal("no ANSWER_VARS in devbox-setup")
	}
	out.WriteString(vars + "\n")
	for _, n := range names {
		i := strings.Index(script, "\n"+n+"() {")
		if i < 0 {
			t.Fatalf("no %s() in devbox-setup", n)
		}
		fn := script[i+1:]
		if n == "merge" {
			fn = fn[:strings.Index(fn, "\n")+1]
		} else {
			fn = fn[:strings.Index(fn, "\n}\n")+3]
		}
		out.WriteString(fn)
	}
	return out.String()
}

func bashPath(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	return bash
}

// runShell runs body after the answers functions, with SHARE at this
// directory (picker.py) and STATE at state.
func runShell(t *testing.T, state, body string, env ...string) (string, string) {
	t.Helper()
	cmd := exec.Command(bashPath(t), "-c", "set -euo pipefail\n"+shellFns(t, "load_answers", "save_answers", "merge")+body)
	cmd.Env = append(append(os.Environ(), "SHARE=.", "STATE="+state), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bash: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

const dumpVars = `for v in $ANSWER_VARS; do printf '%s=%s\0' "$v" "${!v-}"; done`

func parseDump(out string) map[string]string {
	vals := map[string]string{}
	for _, kv := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vals[k] = v
		}
	}
	return vals
}

func writeLegacy(t *testing.T, content string) string {
	t.Helper()
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "answers"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return state
}

// loadAnswers runs load_answers against state and returns the A_* values.
func loadAnswers(t *testing.T, state string) map[string]string {
	t.Helper()
	out, _ := runShell(t, state, "for v in $ANSWER_VARS; do printf -v \"$v\" ''; done\nload_answers\n"+dumpVars)
	return parseDump(out)
}

func TestAnswersLegacyConverted(t *testing.T) {
	state := writeLegacy(t, "# devbox-setup choices. No secrets here.\nA_PICKS=\"editors\"\nA_WIRE_CLAUDE=yes\n")
	vals := loadAnswers(t, state)
	if vals["A_PICKS"] != "editors neovim" || vals["A_WIRE_AGENTS"] != "yes" {
		t.Errorf("A_PICKS=%q A_WIRE_AGENTS=%q, want \"editors neovim\" and yes", vals["A_PICKS"], vals["A_WIRE_AGENTS"])
	}
	if _, err := os.Stat(filepath.Join(state, "answers")); !os.IsNotExist(err) {
		t.Errorf("legacy answers not renamed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state, "answers.old")); err != nil {
		t.Errorf("answers.old: %v", err)
	}
	fi, err := os.Stat(filepath.Join(state, "answers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("answers.json mode %o, want 600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(filepath.Join(state, "answers.json"))
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil || doc["version"] != 1.0 || doc["wire_agents"] != true {
		t.Errorf("answers.json = %s (%v)", b, err)
	}
}

// TestAnswersTamperedRunsNothing: the legacy file used to be sourced as
// root; now no line of it may run.
func TestAnswersTamperedRunsNothing(t *testing.T) {
	dir := t.TempDir()
	pwned := filepath.Join(dir, "pwned")
	state := writeLegacy(t, strings.Join([]string{
		"A_PICKS=cli; touch " + pwned,
		"touch " + pwned,
		"A_TZ=$(touch " + pwned + ")",
		"A_GIT_NAME=\"$(touch " + pwned + ")\"",
		"A_GIT_EMAIL=`touch " + pwned + "`",
		"A_USER=ubuntu",
		"",
	}, "\n"))
	_, stderr := runShell(t, state, "load_answers\n"+dumpVars)
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("a command from the answers file ran")
	}
	vals := loadAnswers(t, state)
	if vals["A_USER"] != "ubuntu" || vals["A_PICKS"] != "" || vals["A_TZ"] != "" {
		t.Errorf("vals = %v, want only A_USER kept", vals)
	}
	for _, w := range []string{"ignored A_PICKS", "not a devbox-setup answer", "ignored A_TZ", "ignored A_GIT_NAME", "ignored A_GIT_EMAIL"} {
		if !strings.Contains(stderr, w) {
			t.Errorf("stderr lacks %q:\n%s", w, stderr)
		}
	}
}

// TestAnswersLegacyRoundTrip: a file the previous save_answers wrote
// (printf %q) decodes to the values sourcing it gave, in both a UTF-8 and
// the C locale (where %q escapes non-ASCII bytes).
func TestAnswersLegacyRoundTrip(t *testing.T) {
	set := `A_USER=ubuntu A_PICKS='cli zsh docker' A_MISE_EXTRA='starship terraform@1.9' A_MCP_LISTEN=0.0.0.0 A_MCP_SERVERS='github jira' A_WIRE_AGENTS=no A_GIT_NAME="Zoë O'Brien \"zo\" \$HOME" A_GIT_EMAIL='zo+dev@example.com' A_TZ=$'Europe/Istanbul'` + "\n"
	oldSave := `for v in $ANSWER_VARS; do printf '%s=%q\n' "$v" "${!v}"; done`
	for _, locale := range []string{"C", "en_US.UTF-8"} {
		bash := bashPath(t)
		vars := "ANSWER_VARS=\"A_USER A_PICKS A_MISE_EXTRA A_MCP_LISTEN A_MCP_SERVERS A_WIRE_AGENTS A_GIT_NAME A_GIT_EMAIL A_TZ\"\n"
		cmd := exec.Command(bash, "-c", vars+set+oldSave)
		cmd.Env = append(os.Environ(), "LC_ALL="+locale)
		legacy, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command(bash, "-c", vars+set+dumpVars)
		want, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		got := loadAnswers(t, writeLegacy(t, string(legacy)))
		for k, v := range parseDump(string(want)) {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q (file: %s)", locale, k, got[k], v, legacy)
			}
		}
	}
}

func TestAnswersSaveLoad(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	runShell(t, state, `A_USER=ubuntu A_PICKS='cli' A_MISE_EXTRA= A_MCP_LISTEN=127.0.0.1 A_MCP_SERVERS=github A_WIRE_AGENTS=yes A_GIT_NAME="Ada L" A_GIT_EMAIL=ada@example.com A_TZ=UTC
A_GH_MODE=agent A_NPM_SCOPE=@acme A_NPM_REGISTRY=https://npm.pkg.github.com A_REGISTRIES='ghcr.io quay.io'
RUN_PICKS='zsh cli' RUN_MISE_EXTRA=starship
save_answers`)
	vals := loadAnswers(t, state)
	want := map[string]string{"A_USER": "ubuntu", "A_PICKS": "cli zsh", "A_MISE_EXTRA": "starship", "A_MCP_LISTEN": "127.0.0.1", "A_MCP_SERVERS": "github", "A_WIRE_AGENTS": "yes", "A_GIT_NAME": "Ada L", "A_GIT_EMAIL": "ada@example.com", "A_TZ": "UTC",
		"A_GH_MODE": "agent", "A_NPM_SCOPE": "@acme", "A_NPM_REGISTRY": "https://npm.pkg.github.com", "A_REGISTRIES": "ghcr.io quay.io"}
	for k, v := range want {
		if vals[k] != v {
			t.Errorf("%s = %q, want %q", k, vals[k], v)
		}
	}
	if fi, err := os.Stat(state); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("state dir: %v %v", fi, err)
	}
}

func TestAnswersChronyDropped(t *testing.T) {
	state := writeLegacy(t, "A_PICKS='cli chrony'\n")
	_, stderr := runShell(t, state, "load_answers\necho \"$A_PICKS\"")
	if !strings.Contains(stderr, "pmox at launch") {
		t.Errorf("no note about chrony moving to pmox:\n%s", stderr)
	}
	if vals := loadAnswers(t, state); vals["A_PICKS"] != "cli" {
		t.Errorf("A_PICKS = %q, want cli", vals["A_PICKS"])
	}
}

package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// scriptDecls returns the script's top-level assignments of the named
// variables (including arrays), so tests use the real values.
func scriptDecls(t *testing.T, names ...string) string {
	t.Helper()
	b, err := os.ReadFile("devbox-setup")
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	var out strings.Builder
	for _, n := range names {
		if i := strings.Index(script, "\n"+n+"=(\n"); i >= 0 {
			body := script[i+1:]
			out.WriteString(body[:strings.Index(body, "\n)\n")+3])
			continue
		}
		line := regexp.MustCompile(`(?m)^` + n + `=.*$`).FindString(script)
		if line == "" {
			t.Fatalf("no %s= in devbox-setup", n)
		}
		out.WriteString(line + "\n")
	}
	return out.String()
}

// credShell runs body with the named script functions and test stubs: the
// target user is the current user with home $H, as_user runs directly,
// record (deduplicated, like created.json) and note append to $H/record
// and $H/notes.
func credShell(t *testing.T, home, body string, fns ...string) string {
	t.Helper()
	stubs := `
user_home() { echo "$H"; }
as_user() { "$@"; }
as_user_sh() { sh -c "$1"; }
record() { grep -qxF "$*" "$H/record" 2>/dev/null || echo "$*" >> "$H/record"; }
recorded() { grep -qxF "$1 $2" "$H/record" 2>/dev/null; }
# GNU install's -D/-d/-m (BSD install differs).
install() {
  local mode= dir=0 a=()
  while [ $# -gt 0 ]; do
    case "$1" in -D) ;; -d) dir=1 ;; -m) mode=$2; shift ;; *) a+=("$1") ;; esac
    shift
  done
  if [ "$dir" = 1 ]; then mkdir -p "${a[@]}"; else mkdir -p "$(dirname "${a[1]}")"; cp "${a[0]}" "${a[1]}"; fi
  [ -z "$mode" ] || chmod "$mode" "${a[@]: -1}"
}
note() { echo "$*" >> "$H/notes"; }
picked() { case " $PLAN " in *" $1 "*) return 0 ;; esac; return 1; }
user_has() { case " $HAVE " in *" $1 "*) return 0 ;; esac; return 1; }
have() { case " $HAVE " in *" $1 "*) return 0 ;; esac; return 1; }
gh_logged_in() { [ -n "${GH_IN:-}" ]; }
declare -A CRED=() REG_USER=() REG_PASS=()
`
	src := "set -euo pipefail\n" +
		scriptDecls(t, "NPMRC_BEGIN", "AGENTENV_LINE", "CRED_ROWS") +
		shellFns(t, append([]string{"envget", "envset"}, fns...)...) + stubs + body
	cmd := exec.Command(bashPath(t), "-c", src)
	cmd.Env = append(os.Environ(), "H="+home, "SHARE=.", "STATE="+filepath.Join(home, "state"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	return string(out)
}

func mkdirAll(t *testing.T, p string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(p, mode); err != nil {
		t.Fatal(err)
	}
}

func putFile(t *testing.T, p string, b []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(p, b, mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAgentEnvExportsOnlyFilledKeys(t *testing.T) {
	home := t.TempDir()
	mkdirAll(t, filepath.Join(home, ".config/devbox"), 0o755)
	putFile(t, filepath.Join(home, ".config/devbox/agent.env"),
		[]byte("# keys\nOPENAI_API_KEY=sk-1=2\nGEMINI_API_KEY=\n# ANTHROPIC_API_KEY=x\nbad-key=1\n"), 0o600)
	cmd := exec.Command("sh", "-c", `. ./conf/agent-env.sh; echo "o=${OPENAI_API_KEY-unset} g=${GEMINI_API_KEY-unset} a=${ANTHROPIC_API_KEY-unset}"; env | grep -c _devbox_ || true`)
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := string(out); got != "o=sk-1=2 g=unset a=unset\n0\n" {
		t.Errorf("got %q", got)
	}
}

func TestAgentKeysFile(t *testing.T) {
	home := t.TempDir()
	putFile(t, filepath.Join(home, ".zshrc"), []byte("# mine\n"), 0o644)
	body := `CONF=./conf
CRED["agentkeys OPENAI_API_KEY"]=sk-test
install_agentkeys
CRED=()
install_agentkeys`
	credShell(t, home, body, "new_user_file", "add_dropin", "install_agentkeys")
	f := filepath.Join(home, ".config/devbox/agent.env")
	got := readFile(t, f)
	for _, want := range []string{"OPENAI_API_KEY=sk-test\n", "ANTHROPIC_API_KEY=\n", "GEMINI_API_KEY=\n", "https://platform.openai.com/api-keys"} {
		if !strings.Contains(got, want) {
			t.Errorf("agent.env missing %q:\n%s", want, got)
		}
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o600 {
		t.Errorf("agent.env mode %v, want 0600", fi.Mode().Perm())
	}
	for _, rc := range []string{".bashrc", ".zshrc"} {
		if n := strings.Count(readFile(t, filepath.Join(home, rc)), ". ~/.config/devbox/agent-env.sh"); n != 1 {
			t.Errorf("%s sources agent-env.sh %d times, want 1", rc, n)
		}
	}
	if rec := readFile(t, filepath.Join(home, "record")); rec != "file "+f+"\n" {
		t.Errorf("record = %q, want the file once", rec)
	}
}

func TestNpmrcBlock(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, ".npmrc")
	putFile(t, f, []byte("save-exact=true\n"), 0o644)
	body := `A_NPM_SCOPE=@acme A_NPM_REGISTRY=https://npm.pkg.github.com/
CRED["npmrc NPM_TOKEN"]=ghp_one
install_npmrc
CRED=()
install_npmrc
npmrc_token`
	out := credShell(t, home, body, "new_user_file", "npmrc_token", "install_npmrc")
	if out != "ghp_one\n" {
		t.Errorf("token after a re-run with no answer = %q, want it kept", out)
	}
	want := "save-exact=true\n# >>> devbox-setup npmrc\n@acme:registry=https://npm.pkg.github.com/\n//npm.pkg.github.com/:_authToken=ghp_one\n# <<< devbox-setup npmrc\n"
	if got := readFile(t, f); got != want {
		t.Errorf(".npmrc =\n%s\nwant\n%s", got, want)
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o600 {
		t.Errorf(".npmrc mode %v, want 0600", fi.Mode().Perm())
	}
	if rec := readFile(t, filepath.Join(home, "record")); rec != "block "+f+"\n" {
		t.Errorf("an existing .npmrc should be recorded as a block, got %q", rec)
	}
}

// A .npmrc devbox-setup created stays recorded as its file only, also
// after a re-run (no extra "block" entry).
func TestNpmrcCreatedRecordedOnce(t *testing.T) {
	home := t.TempDir()
	body := `A_NPM_SCOPE=@acme
install_npmrc
install_npmrc`
	credShell(t, home, body, "new_user_file", "npmrc_token", "install_npmrc")
	f := filepath.Join(home, ".npmrc")
	if rec := readFile(t, filepath.Join(home, "record")); rec != "file "+f+"\n" {
		t.Errorf("record = %q, want only the file", rec)
	}
}

func TestMissingSecrets(t *testing.T) {
	home := t.TempDir()
	cat := t.TempDir()
	mkdirAll(t, filepath.Join(home, ".config/devbox"), 0o755)
	putFile(t, filepath.Join(home, ".config/devbox/agent.env"), []byte("OPENAI_API_KEY=\nANTHROPIC_API_KEY=\nGEMINI_API_KEY=set\n"), 0o600)
	// Claude Code has its own login: its empty key isn't missing.
	mkdirAll(t, filepath.Join(home, ".claude"), 0o700)
	putFile(t, filepath.Join(home, ".claude/.credentials.json"), []byte("{}"), 0o600)
	mkdirAll(t, filepath.Join(home, ".mcp/linear"), 0o700)
	putFile(t, filepath.Join(home, ".mcp/linear/env"), []byte("LINEAR_API_KEY=\n"), 0o600)
	putFile(t, filepath.Join(cat, "linear.spec"), []byte("REQUIRED=\"LINEAR_API_KEY\"\nskeleton() { :; }\n"), 0o644)
	body := `spec_file() { [ -f "` + cat + `/$1.spec" ] && echo "` + cat + `/$1.spec"; }
A_MCP_SERVERS=linear A_NPM_SCOPE=
HAVE="claude codex gemini"
missing_secrets "agentkeys npmrc mcpjungle github"`
	out := credShell(t, home, body, "spec_field", "cred_logged_in", "npmrc_token", "missing_secrets")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	want := []string{
		home + "/.config/devbox/agent.env  OPENAI_API_KEY — https://platform.openai.com/api-keys",
		home + "/.mcp/linear/env  LINEAR_API_KEY — see the comments in the file; then run: mcp-sync",
		"gh  GitHub login — run: gh auth login",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("missing_secrets =\n%s\nwant\n%s", out, strings.Join(want, "\n"))
	}
}

func TestYesNeverStartsGitHubLogin(t *testing.T) {
	home := t.TempDir()
	body := `as_user() { echo "ran: $*" >> "$H/ran"; }
install_sshkey() { :; }
header() { :; }; faint() { :; }; ok() { :; }; warn() { :; }
CONF=./conf INTERACTIVE=0 A_USER=ada PLAN=github
for A_GH_MODE in key agent token; do github_login; done`
	credShell(t, home, body, "github_login")
	if ran := readFile(t, filepath.Join(home, "ran")); strings.Contains(ran, "auth login") || strings.Contains(ran, "refresh") {
		t.Errorf("--yes started an interactive gh step:\n%s", ran)
	}
	notes := readFile(t, filepath.Join(home, "notes"))
	for _, want := range []string{"run 'gh auth login'", "gh auth login --with-token"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes missing %q:\n%s", want, notes)
		}
	}
}

func TestGitHubKnownHostsPinned(t *testing.T) {
	b, _ := os.ReadFile("devbox-setup")
	if strings.Contains(string(b), "ssh-keyscan") {
		t.Error("devbox-setup still uses ssh-keyscan; use conf/github_known_hosts")
	}
	hosts := readFile(t, "conf/github_known_hosts")
	if !strings.Contains(hosts, "github.com ssh-ed25519 ") {
		t.Errorf("conf/github_known_hosts has no ed25519 key:\n%s", hosts)
	}
	if os.Getenv("PMOX_ONLINE_TESTS") == "" {
		t.Log("set PMOX_ONLINE_TESTS=1 to compare with api.github.com/meta")
		return
	}
	out, err := exec.Command("sh", "-c", `curl -fsS --max-time 10 https://api.github.com/meta | python3 -c 'import json,sys; print("\n".join("github.com "+k for k in json.load(sys.stdin)["ssh_keys"]))'`).Output()
	if err != nil {
		t.Skipf("api.github.com/meta: %v", err)
	}
	if string(out) != hosts {
		t.Errorf("conf/github_known_hosts differs from api.github.com/meta:\n%s", out)
	}
}

func TestCreatedRecord(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	state := filepath.Join(t.TempDir(), "state")
	run := func(args ...string) string {
		out, err := exec.Command("python3", append([]string{"picker.py", "created"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("created %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("add", state, "gh_key", "123\tbox (ada)\tauth")
	run("add", state, "gh_login")
	run("add", state, "gh_login")
	run("add", state, "file", "/home/ada/.npmrc")
	run("remove", state, "file", "/home/ada/.npmrc")
	if got := run("list", state); got != "gh_key\t123\tbox (ada)\tauth\ngh_login\t\n" {
		t.Errorf("list = %q", got)
	}
	if fi, _ := os.Stat(filepath.Join(state, "created.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("created.json mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestForget(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	agent := filepath.Join(home, ".config/devbox/agent.env")
	npmrc := filepath.Join(home, ".npmrc")
	mkdirAll(t, filepath.Dir(agent), 0o755)
	putFile(t, agent, []byte("# Anthropic\nANTHROPIC_API_KEY=sk-ant-1\nOPENAI_API_KEY=\n"), 0o600)
	putFile(t, npmrc, []byte("save-exact=true\n# >>> devbox-setup npmrc\n@acme:registry=https://npm.pkg.github.com/\n//npm.pkg.github.com/:_authToken=ghp_x\n# <<< devbox-setup npmrc\n"), 0o600)
	add := func(args ...string) {
		if out, err := exec.Command("python3", append([]string{"picker.py", "created", "add", state}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	add("gh_key", "42\tbox (ada)\tauth")
	add("gh_login")
	add("registry", "ghcr.io")
	add("file", agent)
	add("block", npmrc)
	body := `as_user() { echo "$*" >> "$H/ran"; }
header() { :; }; ok() { echo "$*"; }; warn() { echo "$*"; }; say() { echo "$*"; }
is_secret() { case "$1" in *TOKEN*|*SECRET*|*PASSWORD*|*_KEY|*_KEY_ID|*APIKEY*) return 0 ;; esac; return 1; }
YES=1 A_USER=ada STATE="$H/state"
forget`
	out := credShell(t, home, body, "forget", "unrecord", "revoke_hints")
	ran := readFile(t, filepath.Join(home, "ran"))
	for _, want := range []string{"gh api -X DELETE user/keys/42", "docker logout ghcr.io", "gh auth logout -h github.com"} {
		if !strings.Contains(ran, want) {
			t.Errorf("forget did not run %q; ran:\n%s", want, ran)
		}
	}
	if strings.Index(ran, "DELETE") > strings.Index(ran, "auth logout") {
		t.Error("the GitHub key must be deleted before gh is logged out")
	}
	if got := readFile(t, agent); got != "# Anthropic\nANTHROPIC_API_KEY=\nOPENAI_API_KEY=\n" {
		t.Errorf("agent.env after forget = %q, want the keys kept and emptied", got)
	}
	if got := readFile(t, npmrc); got != "save-exact=true\n" {
		t.Errorf(".npmrc after forget = %q, want only the user's own line", got)
	}
	for _, want := range []string{"https://console.anthropic.com/settings/keys", "npm token: https://github.com/settings/tokens"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing revoke hint %q:\n%s", want, out)
		}
	}
	left, _ := exec.Command("python3", "picker.py", "created", "list", state).Output()
	if string(left) != "file\t"+agent+"\n" {
		t.Errorf("record after forget = %q, want only the credential file (it stays devbox-setup's)", left)
	}
}

func TestForgetNothingRecorded(t *testing.T) {
	home := t.TempDir()
	body := `as_user() { echo "$*" >> "$H/ran"; }
header() { :; }; say() { echo "$*"; }
YES=1 A_USER=ada STATE="$H/state"
forget`
	out := credShell(t, home, body, "forget")
	if !strings.Contains(out, "https://github.com/settings/keys") {
		t.Errorf("want the settings link when nothing is recorded:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, "ran")); err == nil {
		t.Error("forget with no record ran commands")
	}
}

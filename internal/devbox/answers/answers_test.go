package answers

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadMissingIsEmpty(t *testing.T) {
	a, notes, err := Read(t.TempDir())
	if err != nil || len(notes) != 0 || len(a.Picks) != 0 || a.Version != Version {
		t.Fatalf("a=%+v notes=%v err=%v", a, notes, err)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	yes := true
	in := &Answers{User: "ada", Picks: []string{"cli", "zsh"}, MiseExtra: []string{"starship"}, MCPListen: "127.0.0.1",
		MCPServers: []string{"github"}, WireAgents: &yes, Git: Git{"Ada L", "ada@example.com"}, TZ: "UTC",
		GitHub: GitHub{"agent"}, NPM: NPM{"@acme", "https://npm.pkg.github.com"}, Registries: []string{"ghcr.io"}}
	if err := Write(state, in); err != nil {
		t.Fatal(err)
	}
	got, _, err := Read(state)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, in)
	}
	for p, want := range map[string]os.FileMode{state: 0o700, filepath.Join(state, File): 0o600} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v (%v)", p, fi.Mode().Perm(), want, err)
		}
	}
}

func TestMigratePicks(t *testing.T) {
	got, notes := MigratePicks([]string{"cli", "shell", "kubectl", "helm", "chrony", "zsh"})
	if want := []string{"cli", "zsh", "zshcfg", "p10k", "k8s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("picks = %v, want %v", got, want)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "clock sync") {
		t.Errorf("notes = %v", notes)
	}
}

func TestBadVersion(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, File), []byte(`{"version": 7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read(state); err == nil || !strings.Contains(err.Error(), "version 7") {
		t.Errorf("err = %v", err)
	}
}

func TestLegacyConverted(t *testing.T) {
	state := t.TempDir()
	legacy := "A_USER=ubuntu\nA_PICKS=cli\\ shell\nA_GIT_NAME=$'Ada \\'L\\''\nA_WIRE_CLAUDE=yes\nA_TZ='Europe/Berlin'\nNOT_OURS=1\n"
	if err := os.WriteFile(filepath.Join(state, Legacy), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	a, notes, err := Read(state)
	if err != nil {
		t.Fatal(err)
	}
	if a.User != "ubuntu" || a.Git.Name != "Ada 'L'" || a.TZ != "Europe/Berlin" || a.WireAgents == nil || !*a.WireAgents {
		t.Errorf("a = %+v", a)
	}
	if want := []string{"cli", "zsh", "zshcfg", "p10k"}; !reflect.DeepEqual(a.Picks, want) {
		t.Errorf("picks = %v, want %v", a.Picks, want)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "NOT_OURS") {
		t.Errorf("notes = %v", notes)
	}
	if _, err := os.Stat(filepath.Join(state, Legacy+".old")); err != nil {
		t.Error("legacy file not kept as answers.old")
	}
	if _, err := os.Stat(filepath.Join(state, File)); err != nil {
		t.Error("answers.json not written")
	}
}

func TestUnquoteRejectsCode(t *testing.T) {
	for _, v := range []string{"$(touch /tmp/x)", "`id`", "a;b", `"$HOME"`, "a b", "'open", "x|y"} {
		if _, err := Unquote(v); !errors.Is(err, ErrUnsafe) {
			t.Errorf("Unquote(%q) err = %v, want ErrUnsafe", v, err)
		}
	}
}

// Values bash's printf %q wrote decode to the original, in the C and UTF-8
// locales (which quote non-ASCII differently).
func TestUnquoteBashRoundTrip(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	values := []string{"plain", "with space", "it's", `back\slash`, "tab\there", "new\nline", "Zoë Çelik — ✓", "$(id)", "`x`", "a;b|c&d", ""}
	for _, loc := range []string{"C", "en_US.UTF-8"} {
		for _, v := range values {
			cmd := exec.Command(bash, "-c", `printf '%q' "$V"`)
			cmd.Env = append(os.Environ(), "V="+v, "LC_ALL="+loc)
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			got, err := Unquote(string(out))
			if err != nil || got != v {
				t.Errorf("%s: %q → %q → %q (%v)", loc, v, out, got, err)
			}
		}
	}
}

// The bash installer (picker.py) reads what Go writes, and the other way
// round, while both exist.
func TestSharedWithPickerPy(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	picker := "../../bootstrap/picker.py"
	if _, err := os.Stat(picker); err != nil {
		t.Skip("picker.py gone")
	}
	state := t.TempDir()
	if err := Write(state, &Answers{User: "ada", Picks: []string{"cli"}, GitHub: GitHub{"token"}, NPM: NPM{Scope: "@acme"}}); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("python3", picker, "answers", "read", state).Output()
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{}
	parts := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		vals[parts[i]] = parts[i+1]
	}
	if vals["A_USER"] != "ada" || vals["A_PICKS"] != "cli" || vals["A_GH_MODE"] != "token" || vals["A_NPM_SCOPE"] != "@acme" {
		t.Errorf("picker.py read %v", vals)
	}

	cmd := exec.Command("python3", picker, "answers", "write", state)
	cmd.Stdin = strings.NewReader("A_USER\x00bob\x00A_PICKS\x00zsh go\x00A_REGISTRIES\x00ghcr.io\x00")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	a, _, err := Read(state)
	if err != nil {
		t.Fatal(err)
	}
	if a.User != "bob" || !reflect.DeepEqual(a.Picks, []string{"zsh", "go"}) || !reflect.DeepEqual(a.Registries, []string{"ghcr.io"}) {
		t.Errorf("Go read %+v", a)
	}
	var raw map[string]any
	b, _ := os.ReadFile(filepath.Join(state, File))
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "user", "picks", "github", "npm", "registries"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("picker.py's file has no %q", k)
		}
	}
}

func TestMerge(t *testing.T) {
	if got := Merge([]string{"a", "b"}, []string{"b", "c", ""}); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("Merge = %v", got)
	}
}

func TestDevPorts(t *testing.T) {
	for p, rule := range map[string]string{"3000": "allow 3000/tcp", "8000-8010": "allow 8000:8010/tcp", "5353/udp": "allow 5353/udp", "60000-61000/udp": "allow 60000:61000/udp"} {
		if !ValidPort(p) || UFWRule(p) != rule {
			t.Errorf("%s: valid %v, rule %q, want %q", p, ValidPort(p), UFWRule(p), rule)
		}
	}
	for _, p := range []string{"", "0", "70000", "9-3", "22/sctp", "3000;rm", "a"} {
		if ValidPort(p) {
			t.Errorf("%q: valid", p)
		}
	}
}

func TestProxyAndCAChecks(t *testing.T) {
	for u, ok := range map[string]bool{"http://proxy:3128": true, "https://p.acme": true, "http://user:pw@proxy:3128": false, "socks5://p:1080": false, "proxy:3128": false, "http://p:1;rm": false, "http://p:1/$(x)": false} {
		if err := CheckProxy(u); (err == nil) != ok {
			t.Errorf("%s: err = %v", u, err)
		}
	}
	if CheckNoProxy("localhost,127.0.0.1,.acme,10.0.0.0/8") != nil || CheckNoProxy("a b") == nil {
		t.Error("no_proxy checks")
	}
	for c, ok := range map[CA]bool{{}: true, {Source: "/etc/acme.pem"}: true, {Source: "acme.pem"}: false, {Source: "https://x/r.pem"}: false, {Source: "https://x/r.pem", SHA256: strings.Repeat("a", 64)}: true} {
		if err := CheckCA(c); (err == nil) != ok {
			t.Errorf("%+v: err = %v", c, err)
		}
	}
}

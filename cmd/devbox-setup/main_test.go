package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/steps"
)

func TestDryRunPlan(t *testing.T) {
	state := t.TempDir()
	me, _ := user.Current()
	if err := answers.Write(state, &answers.Answers{User: me.Username, Picks: []string{"p10k", "terraform"}, MiseExtra: []string{"starship"}}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := cli([]string{"--yes", "--dry-run", "--state", state}, &out, &errb); err != nil {
		t.Fatalf("run: %v\n%s", err, errb.String())
	}
	got := out.String()
	for _, want := range []string{"For user: " + me.Username, "zsh (login shell)\n  apt    zsh git\n  shell  ", "  clone  https://github.com/romkatv/powerlevel10k.git", "mise   terraform@latest", "Extra mise tools\n  mise   starship"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestYesWithoutAnswers(t *testing.T) {
	err := cli([]string{"--yes", "--dry-run", "--state", t.TempDir()}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no saved choices yet") {
		t.Errorf("err = %v", err)
	}
}

// devbox-setup must stay movable to its own repo: nothing under
// internal/devbox or cmd/devbox-setup may import pmox's PVE, config or
// launch code.
func TestImportBoundary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}",
		"github.com/eugenetaranov/pmox/cmd/devbox-setup", "github.com/eugenetaranov/pmox/internal/devbox/...").Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	const pmox = "github.com/eugenetaranov/pmox/"
	// Not internal/bootstrap: it embeds the Go builds, so each build of
	// devbox-setup would carry the previous ones.
	allowed := []string{pmox + "internal/devbox", pmox + "cmd/devbox-setup", pmox + "internal/tui"}
	for _, p := range strings.Fields(string(out)) {
		if !strings.HasPrefix(p, pmox) {
			continue
		}
		ok := false
		for _, a := range allowed {
			ok = ok || p == a || strings.HasPrefix(p, a+"/")
		}
		if !ok {
			t.Errorf("devbox-setup depends on %s", p)
		}
	}
}

func TestLegacyCmd(t *testing.T) {
	c := legacyCmd("/usr/local/share/devbox-setup/devbox-setup.sh", "/var/lib/devbox-setup", "--only", "docker")
	if c.String() != "bash /usr/local/share/devbox-setup/devbox-setup.sh --only docker" {
		t.Errorf("cmd = %s", c)
	}
	env := strings.Join(c.Env, " ")
	if !strings.Contains(env, "DEVBOX_SETUP_STATE=/var/lib/devbox-setup") || !strings.Contains(env, "DEVBOX_SETUP_SHARE=/usr/local/share/devbox-setup") {
		t.Errorf("env = %v", c.Env)
	}
	if legacyRunner(nil, "/nonexistent/devbox-setup.sh", "/tmp") != nil {
		t.Error("a missing bash installer should give no legacy runner")
	}
}

func TestBashHandles(t *testing.T) {
	for args, want := range map[string]bool{
		"": false, "--dry-run": false, "forget": true, "forget --yes": true, "--help": true,
		"--yes": false, "-y --dry-run": false, "--verify": false, "--version": false,
	} {
		if got := bashHandles(strings.Fields(args)); got != want {
			t.Errorf("bashHandles(%q) = %v, want %v", args, got, want)
		}
	}
}

func TestExportAndProfile(t *testing.T) {
	me, _ := user.Current()
	src := t.TempDir()
	if err := answers.Write(src, &answers.Answers{User: me.Username, Picks: []string{"cli", "terraform"}, TZ: "UTC"}); err != nil {
		t.Fatal(err)
	}
	f := t.TempDir() + "/team.toml"
	if err := cli([]string{"--state", src, "export", f}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	me2 := t.TempDir() + "/me.toml"
	if err := os.WriteFile(me2, []byte("[system]\ntz = \"Europe/Berlin\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	t.Setenv("SUDO_USER", me.Username)
	var out bytes.Buffer
	if err := cli([]string{"--state", dst, "--profile", f, "--profile", me2, "--yes", "--dry-run"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Terraform") || !strings.Contains(out.String(), "CLI upgrades") {
		t.Errorf("dry run of the profile:\n%s", out.String())
	}
	if _, err := os.Stat(dst + "/answers.json"); err == nil {
		t.Error("--dry-run saved the profile's answers")
	}
	if err := cli([]string{"--state", dst, "--profile", "http://example.com/p.toml", "--yes", "--dry-run"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("plain http profile: err = %v", err)
	}
}

// A named profile applied again lists what it dropped; --user names a
// user to create, which a dry run only reports.
func TestProfileLayersAndUser(t *testing.T) {
	me, _ := user.Current()
	state := t.TempDir()
	a := &answers.Answers{User: me.Username, Picks: []string{"lazygit", "cli", "terraform"}, Layers: map[string][]string{"team": {"cli", "terraform"}}}
	if err := answers.Write(state, a); err != nil {
		t.Fatal(err)
	}
	f := t.TempDir() + "/team.toml"
	if err := os.WriteFile(f, []byte("name = \"team\"\npicks = [\"cli\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cli([]string{"--state", state, "--profile", f, "--user", "devboxnewuser", "--yes", "--dry-run"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Dropped by the profile (still installed, no longer re-applied):\n  - Terraform (terraform)\n", "Would create user devboxnewuser", "For user: devboxnewuser\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Terraform\n") {
		t.Errorf("dropped item still planned:\n%s", got)
	}
	if b, _, _ := answers.Read(state); b.User != me.Username {
		t.Error("--dry-run saved the answers")
	}
	if err := cli([]string{"--state", state, "--user", "Bad User", "--yes", "--dry-run"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Error("want an error for --user 'Bad User'")
	}
}

type fakeExec struct{ cmds []string }

func (f *fakeExec) Run(_ context.Context, c steps.Cmd) ([]byte, error) {
	f.cmds = append(f.cmds, c.String())
	if c.Name == "id" {
		return []byte("ubuntu adm sudo docker\n"), nil
	}
	return nil, nil
}

func TestCreateUser(t *testing.T) {
	sudoersDir = t.TempDir()
	x := &fakeExec{}
	if err := createUser(context.Background(), x, "ada", "ubuntu"); err != nil {
		t.Fatal(err)
	}
	if want := "useradd -m -U -s /bin/bash -G adm,sudo,docker ada"; len(x.cmds) != 2 || x.cmds[1] != want {
		t.Errorf("commands = %q, want %q last", x.cmds, want)
	}
	b, err := os.ReadFile(sudoersDir + "/90-devbox-ada")
	if err != nil || string(b) != "ada ALL=(ALL) NOPASSWD:ALL\n" {
		t.Errorf("sudo rule = %q, %v", b, err)
	}
}

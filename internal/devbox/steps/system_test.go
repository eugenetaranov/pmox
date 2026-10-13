package steps

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/creds"
)

func TestDockerDaemonFreshEngineOnly(t *testing.T) {
	haveDockerd := false
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if c.Name == "sh" && strings.HasSuffix(c.Args[1], " dockerd") && !haveDockerd {
			return nil, errors.New("not found")
		}
		return nil, nil
	}}
	e := shellEnv(t)
	e.Exec = x
	st := step(t, catalog.Step{Type: "go:docker_daemon"})
	if _, ok := st.(BeforeApt); !ok {
		t.Fatal("docker_daemon must run before the apt batch")
	}
	apply(t, e, st)
	if !strings.Contains(read(t, e.Path(dockerDaemon)), "log-opts") {
		t.Error("daemon.json not written for a fresh engine")
	}
	// An engine that is already there keeps its config.
	if err := os.Remove(e.Path(dockerDaemon)); err != nil {
		t.Fatal(err)
	}
	haveDockerd = true
	if !check(t, e, st) {
		t.Error("Check = false with dockerd installed")
	}
	apply(t, e, st)
	if _, err := os.Stat(e.Path(dockerDaemon)); err == nil {
		t.Error("daemon.json written for an existing engine")
	}
}

func TestSSHHardenRefusesWithoutKey(t *testing.T) {
	x := &fakeExec{}
	e := shellEnv(t)
	e.Exec = x
	st := step(t, catalog.Step{Type: "go:sshharden"})
	if err := st.Apply(t.Context(), e); err == nil || !strings.Contains(err.Error(), "lock ada out") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(e.Path(sshdDropin)); err == nil {
		t.Error("config written with no SSH key")
	}
	put(t, e.Path("~/.ssh/authorized_keys"), "ssh-ed25519 AAAA ada@laptop\n")
	apply(t, e, st)
	if got := strings.Join(x.commands(), "; "); got != "sshd -t; systemctl try-reload-or-restart ssh" {
		t.Errorf("ran %s", got)
	}
	if !check(t, e, st) {
		t.Error("Check after Apply = false")
	}
}

func TestSSHHardenRollsBack(t *testing.T) {
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if c.Name == "sshd" {
			return []byte("bad option"), errors.New("exit 255")
		}
		return nil, nil
	}}
	e := shellEnv(t)
	e.Exec = x
	put(t, e.Path("~/.ssh/authorized_keys"), "ssh-ed25519 AAAA\n")
	err := step(t, catalog.Step{Type: "go:sshharden"}).Apply(t.Context(), e)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(e.Path(sshdDropin)); err == nil {
		t.Error("a rejected config was left in place")
	}
	for _, c := range x.commands() {
		if strings.HasPrefix(c, "systemctl") {
			t.Errorf("ssh reloaded after a rejected config: %s", c)
		}
	}
}

func TestUFWRules(t *testing.T) {
	status, added := "Status: inactive\n", "(None)\n"
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		switch {
		case c.Name == "sh":
			return nil, errors.New("not installed")
		case c.Name == "ufw" && c.Args[0] == "status":
			return []byte(status), nil
		case c.Name == "ufw" && c.Args[0] == "show":
			return []byte(added), nil
		}
		return nil, nil
	}}
	e := testEnv(t, x)
	e.Answers = &answers.Answers{Picks: []string{"mosh", "mcpjungle"}, MCPListen: "0.0.0.0"}
	st := step(t, catalog.Step{Type: "go:ufw"})
	if check(t, e, st) {
		t.Fatal("Check = true with ufw inactive")
	}
	apply(t, e, st)
	want := "ufw default deny incoming; ufw default allow outgoing; ufw limit OpenSSH; ufw allow 60000:61000/udp; ufw allow 8080/tcp; ufw --force enable"
	if got := strings.Join(x.commands()[len(x.commands())-6:], "; "); got != want {
		t.Errorf("ran %s\nwant %s", got, want)
	}
	status = "Status: active\nLogging: on (low)\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"
	added = "Added user rules (see 'ufw status' for running firewall):\nufw limit OpenSSH\nufw allow 60000:61000/udp\nufw allow 8080/tcp\n"
	if !check(t, e, st) {
		t.Error("Check = false with every rule added")
	}
	e.Answers.MCPListen = "127.0.0.1"
	added = "ufw limit OpenSSH\n"
	if check(t, e, st) {
		t.Error("Check = true without the mosh rule")
	}
	// Another user's gateway on every interface is opened too.
	put(t, e.Path("/etc/mcpjungle/bob.env"), "MCP_HOST=0.0.0.0\nMCP_PORT=8081\nMCP_DIR=/home/bob/.mcp\n")
	put(t, e.Path("/etc/mcpjungle/cy.env"), "MCP_HOST=127.0.0.1\nMCP_PORT=8082\n")
	if r := strings.Join(ufwStep{}.rules(context.Background(), e), "; "); r != "limit OpenSSH; allow 60000:61000/udp; allow 8081/tcp" {
		t.Errorf("rules = %s", r)
	}
}

func TestTimezone(t *testing.T) {
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) { return []byte("Etc/UTC\n"), nil }}
	e := testEnv(t, x)
	st := step(t, catalog.Step{Type: "go:timezone"})
	if !check(t, e, st) {
		t.Error("no timezone chosen: Check = false")
	}
	e.Answers = &answers.Answers{TZ: "Europe/Berlin"}
	if check(t, e, st) {
		t.Error("Check = true with UTC set and Berlin chosen")
	}
	apply(t, e, st)
	if got := x.commands(); got[len(got)-1] != "timedatectl set-timezone Europe/Berlin" {
		t.Errorf("ran %q", got)
	}
}

func TestTailscaleUpKeyFromFile(t *testing.T) {
	var keyFile string
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if c.Name == "tailscale" {
			keyFile = strings.TrimPrefix(c.Args[1], "--auth-key=file:")
			b, _ := os.ReadFile(keyFile)
			if string(b) != "tskey-auth-1" {
				t.Errorf("key file holds %q", b)
			}
		}
		return nil, nil
	}}
	e := testEnv(t, x)
	e.State = t.TempDir()
	st := step(t, catalog.Step{Type: "go:tailscale_up"})
	if !check(t, e, st) {
		t.Error("no key typed: Check = false")
	}
	e.Secrets = creds.New()
	e.Secrets.TSAuthKey = "tskey-auth-1"
	apply(t, e, st)
	if strings.Contains(strings.Join(x.commands(), " "), "tskey") {
		t.Error("the key is on the command line")
	}
	if _, err := os.Stat(keyFile); err == nil {
		t.Error("key file left behind")
	}
	if got := answers.ReadCreated(e.State); len(got) != 1 || got[0].Kind != "tailscale" {
		t.Errorf("created = %+v", got)
	}
}

func TestNoteWhen(t *testing.T) {
	cond := false
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if cond {
			return nil, nil
		}
		return nil, errors.New("false")
	}}
	e := testEnv(t, x)
	var notes []string
	e.Note = func(s string) { notes = append(notes, s) }
	st := step(t, catalog.Step{Type: "note", Text: "see {{home}}", When: "test -e {{home}}/x"})
	apply(t, e, st)
	cond = true
	apply(t, e, st)
	if len(notes) != 1 || notes[0] != "see /home/ada" {
		t.Errorf("notes = %q", notes)
	}
	if x.ran[0].Args[1] != "test -e /home/ada/x" {
		t.Errorf("when ran as %q", x.ran[0].Args[1])
	}
}

func TestGitConfig(t *testing.T) {
	cfg := map[string]string{}
	haveDelta := false
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		switch {
		case c.Name == "sh":
			if haveDelta {
				return nil, nil
			}
			return nil, errors.New("no delta")
		case c.Name == "git" && c.Args[2] == "--get":
			if v, ok := cfg[c.Args[3]]; ok {
				return []byte(v + "\n"), nil
			}
			return nil, errors.New("unset")
		case c.Name == "git":
			cfg[c.Args[2]] = c.Args[3]
		}
		return nil, nil
	}}
	e := testEnv(t, x)
	e.Answers = &answers.Answers{Git: answers.Git{Email: "ada@example.com"}}
	id := step(t, catalog.Step{Type: "git_config", Values: map[string]string{"user.name": "{{git_name}}", "user.email": "{{git_email}}"}})
	apply(t, e, id)
	// No name in the answers: the user's own (or GitHub's) stays.
	if _, ok := cfg["user.name"]; ok || cfg["user.email"] != "ada@example.com" {
		t.Errorf("git config = %v", cfg)
	}
	if !check(t, e, id) {
		t.Error("Check after Apply = false")
	}
	delta := step(t, catalog.Step{Type: "git_config", IfTool: "delta", Values: map[string]string{"core.pager": "delta"}})
	if !check(t, e, delta) {
		t.Error("without delta: Check = false")
	}
	haveDelta = true
	if check(t, e, delta) {
		t.Error("with delta, unset: Check = true")
	}
	apply(t, e, delta)
	if cfg["core.pager"] != "delta" {
		t.Errorf("git config = %v", cfg)
	}
}

func TestSudoPW(t *testing.T) {
	hasPW := false
	x := &fakeExec{reply: func(c Cmd) ([]byte, error) {
		if c.Name == "passwd" {
			if hasPW {
				return []byte("ada P 2026-10-13 0 99999 7 -1\n"), nil
			}
			return []byte("ada L 2026-10-13 0 99999 7 -1\n"), nil
		}
		return nil, nil
	}}
	e := testEnv(t, x)
	e.Secrets = creds.New()
	put(t, e.Path("/etc/sudoers.d/90-cloud-init-users"), "# Created by cloud-init\n\n# User rules for ada\nada ALL=(ALL) NOPASSWD:ALL\n")
	put(t, e.Path("/etc/sudoers.d/README"), "bob ALL=(ALL) NOPASSWD:ALL\n")
	st := step(t, catalog.Step{Type: "go:sudopw"})
	if check(t, e, st) {
		t.Fatal("Check = true with NOPASSWD")
	}
	if err := st.Apply(context.Background(), e); err == nil || !strings.Contains(err.Error(), "sudo passwd ada") {
		t.Fatalf("no password: err = %v", err)
	}
	if got := read(t, e.Path("/etc/sudoers.d/90-cloud-init-users")); !strings.Contains(got, "NOPASSWD") {
		t.Error("NOPASSWD removed while ada has no password")
	}
	e.Secrets.SudoPW = "s3cret"
	apply(t, e, st)
	if got := x.commands(); !strings.Contains(strings.Join(got, "\n"), "chpasswd") || !strings.HasSuffix(got[len(got)-1], "visudo -cqf /etc/sudoers.d/90-cloud-init-users.devbox-new") {
		t.Errorf("ran %q", got)
	}
	if got := read(t, e.Path("/etc/sudoers.d/90-cloud-init-users")); got != "# Created by cloud-init\n\n# User rules for ada\nada ALL=(ALL) ALL\n" {
		t.Errorf("rules = %q", got)
	}
	if got := read(t, e.Path("/etc/sudoers.d/README")); !strings.Contains(got, "bob ALL=(ALL) NOPASSWD:ALL") {
		t.Errorf("bob's rule changed: %q", got)
	}
	if !check(t, e, st) {
		t.Error("Check = false after Apply")
	}
	hasPW, e.Secrets.SudoPW = true, ""
	put(t, e.Path("/etc/sudoers.d/90-devbox-ada"), "ada ALL=(ALL) NOPASSWD:ALL\n")
	apply(t, e, st)
	if got := read(t, e.Path("/etc/sudoers.d/90-devbox-ada")); got != "ada ALL=(ALL) ALL\n" {
		t.Errorf("with a password already: %q", got)
	}
}

package steps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
)

func init() {
	RegisterGo("docker_daemon", func(catalog.Step) Step { return dockerDaemonStep{} })
	RegisterGo("timezone", func(catalog.Step) Step { return timezoneStep{} })
	RegisterGo("tailscale_up", func(catalog.Step) Step { return tailscaleUpStep{} })
	RegisterGo("sshharden", func(catalog.Step) Step { return sshHardenStep{} })
	RegisterGo("ufw", func(catalog.Step) Step { return ufwStep{} })
}

// root reports whether a command is on root's PATH.
func root(ctx context.Context, e *Env, tool string) bool {
	_, err := e.Exec.Run(ctx, Cmd{Name: "sh", Args: []string{"-c", "command -v " + tool}})
	return err == nil
}

// --- go:docker_daemon --------------------------------------------------------

// dockerDaemonStep writes /etc/docker/daemon.json (log rotation, address
// pools) before the engine is installed, for a fresh engine only: a later
// change means a restart that stops running containers.
type dockerDaemonStep struct{}

const dockerDaemon = "/etc/docker/daemon.json"

func (dockerDaemonStep) BeforeApt() bool { return true }

func (dockerDaemonStep) Plan(*Env) []Action {
	return []Action{{"file", dockerDaemon + " (a fresh engine only)"}}
}

func (dockerDaemonStep) Check(ctx context.Context, e *Env) (bool, error) {
	if _, err := os.Stat(e.Path(dockerDaemon)); err == nil {
		return true, nil
	}
	return root(ctx, e, "dockerd"), nil
}

func (d dockerDaemonStep) Apply(ctx context.Context, e *Env) error {
	if done, _ := d.Check(ctx, e); done {
		return nil
	}
	b, err := fs.ReadFile(e.Files, "daemon.json")
	if err != nil {
		return err
	}
	return writeFile(e.Path(dockerDaemon), b, 0o644)
}

// --- go:timezone -------------------------------------------------------------

type timezoneStep struct{}

func tz(e *Env) string {
	if e.Answers == nil {
		return ""
	}
	return e.Answers.TZ
}

func (timezoneStep) Plan(e *Env) []Action {
	if tz(e) == "" {
		return nil
	}
	return []Action{{"run", "timedatectl set-timezone " + tz(e)}}
}

func (timezoneStep) Check(ctx context.Context, e *Env) (bool, error) {
	if tz(e) == "" {
		return true, nil
	}
	out, err := e.Exec.Run(ctx, Cmd{Name: "timedatectl", Args: []string{"show", "-p", "Timezone", "--value"}})
	return err == nil && strings.TrimSpace(string(out)) == tz(e), nil
}

func (timezoneStep) Apply(ctx context.Context, e *Env) error {
	if tz(e) == "" {
		return nil
	}
	_, err := e.run(ctx, Cmd{Name: "timedatectl", Args: []string{"set-timezone", tz(e)}})
	return err
}

// --- go:tailscale_up ---------------------------------------------------------

// tailscaleUpStep joins the tailnet with the auth key typed this run (none:
// the user runs `sudo tailscale up` later).
type tailscaleUpStep struct{}

func authKey(e *Env) string {
	if e.Secrets == nil {
		return ""
	}
	return e.Secrets.TSAuthKey
}

func (tailscaleUpStep) Plan(e *Env) []Action {
	if authKey(e) == "" {
		return nil
	}
	return []Action{{"run", "tailscale up --auth-key=file:… (the key typed)"}}
}

func (tailscaleUpStep) Check(_ context.Context, e *Env) (bool, error) { return authKey(e) == "", nil }

func (tailscaleUpStep) Apply(ctx context.Context, e *Env) error {
	if authKey(e) == "" {
		return nil
	}
	// From a file: on the command line, ps would show the key.
	f, err := os.CreateTemp("", "tailscale-key.")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, werr := f.WriteString(authKey(e))
	if err := errors.Join(werr, f.Close()); err != nil {
		return err
	}
	if _, err := e.run(ctx, Cmd{Name: "tailscale", Args: []string{"up", "--auth-key=file:" + f.Name()}}); err != nil {
		return err
	}
	return e.record("tailscale", "")
}

// --- go:sshharden ------------------------------------------------------------

// sshHardenStep turns off password and root login, refusing when the user
// has no SSH key (they'd be locked out) and rolling back a config sshd
// rejects.
type sshHardenStep struct{}

const sshdDropin = "/etc/ssh/sshd_config.d/10-devbox.conf"

var keyLine = regexp.MustCompile(`(?m)^(ssh-|ecdsa-|sk-)`)

func (sshHardenStep) Plan(*Env) []Action {
	return []Action{{"file", sshdDropin}, {"unit", "try-reload-or-restart ssh"}}
}

func (sshHardenStep) want(e *Env) ([]byte, error) { return fs.ReadFile(e.Files, "sshd-devbox.conf") }

func (s sshHardenStep) Check(_ context.Context, e *Env) (bool, error) {
	want, err := s.want(e)
	if err != nil {
		return false, err
	}
	return same(e.Path(sshdDropin), wantFile{b: want, mode: 0o644}), nil
}

func (s sshHardenStep) Apply(ctx context.Context, e *Env) error {
	keys := e.Path("~/.ssh/authorized_keys")
	b, _ := os.ReadFile(keys)
	if !keyLine.Match(b) {
		return fmt.Errorf("%s has no SSH key: turning off password login would lock %s out", keys, e.User)
	}
	want, err := s.want(e)
	if err != nil {
		return err
	}
	p := e.Path(sshdDropin)
	if err := writeFile(p, want, 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(e.Path("/run/sshd"), 0o755); err != nil {
		return err
	}
	if _, err := e.run(ctx, Cmd{Name: "sshd", Args: []string{"-t"}}); err != nil {
		_ = os.Remove(p)
		return fmt.Errorf("sshd rejected the config; nothing changed: %w", err)
	}
	_, err = e.run(ctx, Cmd{Name: "systemctl", Args: []string{"try-reload-or-restart", "ssh"}})
	return err
}

// --- go:ufw ------------------------------------------------------------------

// ufwStep: deny incoming but SSH (rate-limited), and the ports of what is
// installed: Tailscale's interface, mosh, MCPJungle when it listens on
// every interface.
type ufwStep struct{}

func (ufwStep) picked(e *Env, id string) bool {
	return e.Answers != nil && slices.Contains(e.Answers.Picks, id)
}

func (u ufwStep) rules(ctx context.Context, e *Env) []string {
	rules := []string{"limit OpenSSH"}
	if u.picked(e, "tailscale") || root(ctx, e, "tailscale") {
		rules = append(rules, "allow in on tailscale0")
	}
	if u.picked(e, "mosh") || root(ctx, e, "mosh-server") {
		rules = append(rules, "allow 60000:61000/udp")
	}
	if (u.picked(e, "mcpjungle") || root(ctx, e, "mcpjungle")) && listen(e) == "0.0.0.0" {
		rules = append(rules, fmt.Sprintf("allow %d/tcp", MCPJunglePort))
	}
	return rules
}

func (ufwStep) Plan(*Env) []Action {
	return []Action{{"run", "ufw: deny incoming, limit OpenSSH, allow what is installed; enable"}}
}

func (u ufwStep) Check(ctx context.Context, e *Env) (bool, error) {
	status, err := e.Exec.Run(ctx, Cmd{Name: "ufw", Args: []string{"status", "verbose"}})
	if err != nil || !strings.Contains(string(status), "Status: active") ||
		!strings.Contains(string(status), "deny (incoming), allow (outgoing)") {
		return false, nil
	}
	added, err := e.Exec.Run(ctx, Cmd{Name: "ufw", Args: []string{"show", "added"}})
	if err != nil {
		return false, nil
	}
	for _, r := range u.rules(ctx, e) {
		if !hasLine(added, "ufw "+r) {
			return false, nil
		}
	}
	return true, nil
}

func (u ufwStep) Apply(ctx context.Context, e *Env) error {
	cmds := [][]string{{"default", "deny", "incoming"}, {"default", "allow", "outgoing"}}
	for _, r := range u.rules(ctx, e) {
		cmds = append(cmds, strings.Fields(r))
	}
	// Last, once SSH is allowed.
	cmds = append(cmds, []string{"--force", "enable"})
	for _, args := range cmds {
		if _, err := e.run(ctx, Cmd{Name: "ufw", Args: args}); err != nil {
			return err
		}
	}
	return nil
}

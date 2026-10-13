package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/eugenetaranov/pmox/internal/devbox/answers"
	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/creds"
	"github.com/eugenetaranov/pmox/internal/devbox/mcp"
	"github.com/eugenetaranov/pmox/internal/devbox/plan"
	"github.com/eugenetaranov/pmox/internal/devbox/profile"
	"github.com/eugenetaranov/pmox/internal/devbox/steps"
	"github.com/eugenetaranov/pmox/internal/devbox/ui"
)

// choices is what one interactive run picked and typed.
type choices struct {
	picks, miseExtra, mcpServers []string
	items                        []*catalog.Item
	secrets                      *creds.Secrets
}

// asker runs the prompts. Its hooks are seams for tests.
type asker struct {
	cat   *catalog.Catalog
	a     *answers.Answers
	share string
	out   io.Writer
	// installed reports whether a tool is on the user's PATH.
	installed func(tool string) bool
	// profile: the answers already hold the profiles' picks; review them
	// instead of opening the picker. dropped are the picks they dropped.
	profile bool
	dropped []string
	// port is the MCPJungle port the user would get.
	port func() int
}

// ask runs every prompt, then the review; it loops on "Start over".
func (k *asker) ask(ctx context.Context) (*choices, error) {
	if k.profile {
		c, err := k.fromProfile(ctx)
		if err != nil || c == nil {
			return c, err
		}
		if k.review(c, "Install", "Quit") != "Install" {
			return nil, ui.ErrQuit
		}
		return c, nil
	}
	for {
		c, err := k.once(ctx)
		if err != nil || c == nil {
			return c, err
		}
		switch k.review(c, "Install", "Start over", "Quit") {
		case "Install":
			return c, nil
		case "Start over":
			continue
		default:
			return nil, ui.ErrQuit
		}
	}
}

func (k *asker) once(ctx context.Context) (*choices, error) {
	fmt.Fprintln(k.out, "devbox-setup — pick what to install: nothing is preselected. Run it again any time to add more.")

	// 1. Pick: one tabbed checklist over every category, plus mise's registry.
	var tabs []ui.Tab
	for _, t := range k.cat.Tabs {
		tabs = append(tabs, ui.Tab{ID: t.ID, Title: t.Title})
	}
	entries := ui.CatalogEntries(k.cat)
	entries = ui.Dedupe(append(entries, ui.LoadMise(miseRegistry(ctx, k.share))...))
	picked, ok, err := ui.NewPicker("devbox-setup — pick what to install", tabs, entries, true).Run()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ui.ErrQuit
	}
	c := &choices{secrets: creds.New()}
	var fromSearch []string
	for _, id := range picked {
		if t, ok := strings.CutPrefix(id, "mise:"); ok {
			fromSearch = append(fromSearch, t)
		} else {
			c.picks = append(c.picks, id)
		}
	}

	// 2. Type: every text prompt in one place.
	if c.miseExtra, err = k.askMiseExtra(ctx, fromSearch); err != nil {
		return nil, err
	}
	if c.items, err = plan.Resolve(k.cat, c.picks, len(c.miseExtra) > 0); err != nil {
		return nil, err
	}
	if len(c.items) == 0 {
		fmt.Fprintln(k.out, "Nothing picked.")
		return nil, nil
	}
	has := planHas(c.items)
	if has("gitid") {
		if err := k.askGit(); err != nil {
			return nil, err
		}
	}
	if has("tzlocale") {
		if err := k.askTZ(); err != nil {
			return nil, err
		}
	}
	if has("mcpjungle") {
		if err := k.askMCP(ctx, c, has); err != nil {
			return nil, err
		}
	}
	return c, k.askSecrets(c, has)
}

// fromProfile plans the profiles' picks (already in the answers) and asks
// only what a profile can't hold: secrets, and settings it left unset.
func (k *asker) fromProfile(ctx context.Context) (*choices, error) {
	a := k.a
	c := &choices{secrets: creds.New(), miseExtra: a.MiseExtra, mcpServers: a.MCPServers}
	var err error
	if c.items, err = plan.Resolve(k.cat, a.Picks, len(a.MiseExtra) > 0); err != nil {
		return nil, err
	}
	if len(c.items) == 0 {
		fmt.Fprintln(k.out, "The profiles pick nothing.")
		return nil, nil
	}
	has := planHas(c.items)
	if has("gitid") && a.Git.Name+a.Git.Email == "" {
		if err := k.askGit(); err != nil {
			return nil, err
		}
	}
	if has("tzlocale") && a.TZ == "" {
		if err := k.askTZ(); err != nil {
			return nil, err
		}
	}
	return c, k.askSecrets(c, has)
}

// planHas reports whether an id is in the plan.
func planHas(items []*catalog.Item) func(string) bool {
	picks := " " + strings.Join(plan.IDs(items), " ") + " "
	return func(id string) bool { return strings.Contains(picks, " "+id+" ") }
}

// askUser asks which user to set up, offering to create a missing one.
func askUser(def string) (string, error) {
	u, err := ui.Input("Set up for which user?", "", def)
	if err != nil {
		return "", err
	}
	if _, err := user.Lookup(u); err != nil {
		if !profile.ValidUser(u) {
			return "", fmt.Errorf("user %q does not exist", u)
		}
		yes, err := ui.Confirm("User "+u+" does not exist. Create it?", "Like the VM's first user: passwordless sudo and the same groups.", false)
		if err != nil {
			return "", err
		}
		if !yes {
			return "", fmt.Errorf("user %q does not exist", u)
		}
	}
	return u, nil
}

// askSecrets asks for the credentials and secrets of the plan's items.
func (k *asker) askSecrets(c *choices, has func(string) bool) error {
	var err error
	if has("github") {
		if err := k.askGitHub(c); err != nil {
			return err
		}
	}
	if err := k.askCreds(c, has); err != nil {
		return err
	}
	if has("registries") {
		if err := k.askRegistries(c); err != nil {
			return err
		}
	}
	if has("agekey") {
		if err := k.askAgeKey(c); err != nil {
			return err
		}
	}
	if has("sudopw") && !hasPassword(k.a.User) {
		fmt.Fprintf(k.out, "\n%s has no password; sudo will ask for one, so set it now.\n", k.a.User)
		for {
			pw, err := ui.Password("New password for "+k.a.User+" (empty: set it later with 'sudo passwd "+k.a.User+"')", "")
			if err != nil || pw == "" {
				return err
			}
			again, err := ui.Password("The same password again", "")
			if err != nil {
				return err
			}
			if pw == again {
				c.secrets.SudoPW = pw
				break
			}
			fmt.Fprintln(k.out, "They don't match.")
		}
	}
	if has("tailscale") && exec.Command("tailscale", "status").Run() != nil {
		if c.secrets.TSAuthKey, err = ui.Password("Tailscale auth key (empty: run 'sudo tailscale up' later)", ""); err != nil {
			return err
		}
	}
	return nil
}

// miseRegistry is the live registry when mise is installed, else pmox's
// snapshot in the share directory.
func miseRegistry(ctx context.Context, share string) string {
	if out, err := exec.CommandContext(ctx, "mise", "registry", "-J", "--hide-aliased").Output(); err == nil {
		if tsv := ui.RegistryTSV(out); tsv != "" {
			return tsv
		}
	}
	b, _ := os.ReadFile(filepath.Join(share, "mise-registry.tsv"))
	return string(b)
}

func (k *asker) askMiseExtra(ctx context.Context, prefill []string) ([]string, error) {
	val := strings.Join(prefill, " ")
	for {
		v, err := ui.Input("Extra mise tools, space-separated (e.g. just sops@3), blank to skip", "", val)
		if err != nil {
			return nil, err
		}
		tools := strings.Fields(v)
		if len(tools) == 0 || !k.installed("mise") {
			return tools, nil
		}
		var bad []string
		for _, t := range tools {
			name := strings.SplitN(t, "@", 2)[0]
			if strings.Contains(name, ":") {
				continue // backend:tool, can't check offline
			}
			if exec.CommandContext(ctx, "mise", "registry", name).Run() != nil {
				bad = append(bad, name)
			}
		}
		if len(bad) == 0 {
			return tools, nil
		}
		fmt.Fprintf(k.out, "Not in mise's registry: %s — fix or clear them.\n", strings.Join(bad, " "))
		val = v
	}
}

func (k *asker) gitConfig(key string) string {
	out, _ := exec.Command("runuser", "-u", k.a.User, "--", "git", "config", "--global", key).Output()
	return strings.TrimSpace(string(out))
}

func (k *asker) askGit() error {
	name, email := k.gitConfig("user.name"), k.gitConfig("user.email")
	if name == "" {
		name = k.a.Git.Name
	}
	if email == "" {
		email = k.a.Git.Email
	}
	var err error
	desc := "Leave empty to take it from your GitHub account after the gh login."
	if k.a.Git.Name, err = ui.Input("Git user.name", desc, name); err != nil {
		return err
	}
	k.a.Git.Email, err = ui.Input("Git user.email", desc, email)
	return err
}

func (k *asker) askTZ() error {
	cur := k.a.TZ
	if cur == "" {
		out, _ := exec.Command("timedatectl", "show", "-p", "Timezone", "--value").Output()
		cur = strings.TrimSpace(string(out))
	}
	if cur == "" {
		cur = "Etc/UTC"
	}
	zones, _ := exec.Command("timedatectl", "list-timezones").Output()
	for {
		tz, err := ui.Input("Timezone (e.g. Europe/Berlin, America/New_York)", "", cur)
		if err != nil {
			return err
		}
		if len(zones) == 0 || strings.Contains("\n"+string(zones), "\n"+tz+"\n") {
			k.a.TZ = tz
			return nil
		}
		fmt.Fprintf(k.out, "Unknown timezone %q.\n", tz)
		cur = tz
	}
}

func (k *asker) askMCP(ctx context.Context, c *choices, has func(string) bool) error {
	one, all := "this machine only", "all interfaces (no auth — trusted networks only)"
	sel := one
	if k.a.MCPListen == "0.0.0.0" {
		sel = all
	}
	l, err := ui.Choose("MCPJungle gateway listens on", []string{one, all}, sel)
	if err != nil {
		return err
	}
	k.a.MCPListen = "127.0.0.1"
	if l == all {
		k.a.MCPListen = "0.0.0.0"
	}

	dirs := mcp.Dirs(k.share)
	var entries []*ui.Entry
	specs := map[string]*mcp.Spec{}
	for _, n := range mcp.Names(dirs) {
		s, err := mcp.Load(ctx, dirs, n)
		if err != nil {
			continue
		}
		specs[n] = s
		entries = append(entries, &ui.Entry{Tab: "mcp", ID: n, Label: n, Source: s.Description + " · " + s.Image + s.URL})
	}
	servers, ok, err := ui.NewPicker("MCPJungle — which MCP servers?", []ui.Tab{{ID: "mcp", Title: "MCP servers"}}, entries, false).Run()
	if err != nil {
		return err
	}
	if !ok {
		return ui.ErrQuit
	}
	c.mcpServers = servers
	// Servers set up before stay enabled (drop one by editing ~/.mcp/servers).
	k.a.MCPServers = answers.Merge(k.a.MCPServers, servers)

	if has("claude") || has("codex") || has("gemini") || k.installed("claude") || k.installed("codex") || k.installed("gemini") {
		def := k.a.WireAgents == nil || *k.a.WireAgents
		yes, err := ui.Confirm("Add the MCPJungle gateway to "+k.a.User+"'s coding agents?",
			"Claude Code, Codex and Gemini CLI (those installed) can then use every MCP server registered in MCPJungle.", def)
		if err != nil {
			return err
		}
		k.a.WireAgents = &yes
	}

	home := homeOf(k.a.User)
	for _, n := range servers {
		s := specs[n]
		if s == nil || len(s.Keys)+len(s.Optional) == 0 {
			continue
		}
		envf := filepath.Join(home, ".mcp", n, "env")
		envb := readAsOwner(k.a.User, envf)
		keys := append([]string{}, s.Keys...)
		if len(s.Optional) > 0 {
			var opt []*ui.Entry
			for _, o := range s.Optional {
				opt = append(opt, &ui.Entry{Tab: "opt", ID: o, Label: o, Source: "optional · leave unticked to skip"})
			}
			chosen, ok, err := ui.NewPicker(n+" — optional settings (enter to skip)", []ui.Tab{{ID: "opt", Title: "Optional settings"}}, opt, false).Run()
			if err != nil {
				return err
			}
			if ok {
				keys = append(keys, chosen...)
			}
		}
		fmt.Fprintf(k.out, "\n%s — %s\nLeave a value empty to fill it in later: mcp-sync edit %s\n", n, s.Description, n)
		for _, key := range keys {
			cur := steps.EnvGet(envb, key)
			if cur == "" {
				cur = s.Defaults[key]
			}
			var v string
			if mcp.IsSecret(key) {
				ph := ""
				if cur != "" {
					ph = "(unchanged)"
				}
				if v, err = ui.Password(n+": "+key, ph); err != nil {
					return err
				}
				if v == "" {
					v = cur
				}
			} else if v, err = ui.Input(n+": "+key, "", cur); err != nil {
				return err
			}
			c.secrets.MCP[n+" "+key] = v
		}
	}
	return nil
}

func (k *asker) askGitHub(c *choices) error {
	agent := "Forwarded SSH agent (no key on this VM; connect with pmox shell -A)"
	key := "A key on this VM (~/.ssh/id_ed25519, uploaded to GitHub)"
	token := "A fine-grained token (git over HTTPS; no key)"
	labels := map[string]string{"agent": agent, "key": key, "token": token}
	def := k.a.GitHub.Mode
	if def == "" {
		def = "key"
		if os.Getenv("SSH_AUTH_SOCK") != "" && exec.Command("ssh-add", "-l").Run() == nil {
			def = "agent"
		}
	}
	ch, err := ui.Choose("How should this VM reach GitHub?", []string{agent, key, token}, labels[def])
	if err != nil {
		return err
	}
	for m, l := range labels {
		if l == ch {
			k.a.GitHub.Mode = m
		}
	}
	home := homeOf(k.a.User)
	if k.a.GitHub.Mode == "key" {
		if _, err := os.Stat(filepath.Join(home, ".ssh", "id_ed25519")); err != nil {
			yes, err := ui.Confirm("Protect the new key with a passphrase?", "ssh-keygen asks for it after the installs.", false)
			if err != nil {
				return err
			}
			if yes {
				c.secrets.GHPassphrase = "yes"
			}
		}
	}
	if k.a.GitHub.Mode == "token" && exec.Command("runuser", "-u", k.a.User, "--", "gh", "auth", "status", "-h", "github.com").Run() != nil {
		fmt.Fprintln(k.out, "Create one at https://github.com/settings/personal-access-tokens (Contents: read/write on your repos).")
		if c.secrets.GHToken, err = ui.Password("GitHub fine-grained token (empty: later)", ""); err != nil {
			return err
		}
	}
	return nil
}

func (k *asker) askCreds(c *choices, has func(string) bool) error {
	for _, it := range k.cat.Items {
		if !has(it.ID) {
			continue
		}
		shown := false
		for _, s := range it.Steps {
			if s.Type != "cred" {
				continue
			}
			for _, key := range s.Keys {
				if key.When != "" && !has(key.When) && !k.installed(key.When) {
					continue
				}
				if !shown {
					fmt.Fprintf(k.out, "\n%s\nLeave a value empty to fill it in later: ~/devbox-setup.txt lists what is missing.\n", it.Label)
					shown = true
				}
				if key.Secret() {
					v, err := ui.Password(key.Prompt, "")
					if err != nil {
						return err
					}
					c.secrets.Cred[it.ID+" "+key.Name] = v
					continue
				}
				p := answerField(k.a, key.Answer)
				cur := *p
				if cur == "" {
					cur = key.Default
				}
				v, err := ui.Input(key.Prompt, "", cur)
				if err != nil {
					return err
				}
				*p = v
			}
		}
	}
	if s := k.a.NPM.Scope; s != "" && !strings.HasPrefix(s, "@") {
		k.a.NPM.Scope = "@" + s
	}
	return nil
}

// answerField is the saved answer a cred key's setting goes in
// (catalog.CredAnswers).
func answerField(a *answers.Answers, name string) *string {
	switch name {
	case "npm.scope":
		return &a.NPM.Scope
	case "npm.registry":
		return &a.NPM.Registry
	}
	var discard string
	return &discard
}

func (k *asker) askRegistries(c *choices) error {
	def := strings.Join(k.a.Registries, " ")
	if def == "" {
		def = "ghcr.io"
	}
	v, err := ui.Input("Registries, space-separated (e.g. ghcr.io registry.gitlab.com)",
		"Each is logged in with 'docker login'. Empty password: later (ghcr.io uses the gh token when it has read:packages).", def)
	if err != nil {
		return err
	}
	k.a.Registries = strings.Fields(v)
	for _, r := range k.a.Registries {
		if c.secrets.RegUser[r], err = ui.Input(r+": username", "", ""); err != nil {
			return err
		}
		if c.secrets.RegPass[r], err = ui.Password(r+": password or token", ""); err != nil {
			return err
		}
	}
	return nil
}

func (k *asker) askAgeKey(c *choices) error {
	f := filepath.Join(homeOf(k.a.User), ".config", "sops", "age", "keys.txt")
	// A key line, not the skeleton's comment that mentions one.
	if b, err := os.ReadFile(f); err == nil && steps.HasAgeKey(b) {
		c.secrets.AgeChoice = "keep"
		return nil
	}
	ch, err := ui.Choose("age key for "+k.a.User, []string{"Generate a new key", "Paste an existing key", "Later"}, "Generate a new key")
	if err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(ch, "Gen"):
		c.secrets.AgeChoice = "generate"
	case strings.HasPrefix(ch, "Paste"):
		c.secrets.AgeChoice = "paste"
		if c.secrets.AgeKey, err = ui.Password("age secret key (AGE-SECRET-KEY-1…)", ""); err != nil {
			return err
		}
		if c.secrets.AgeKey == "" {
			c.secrets.AgeChoice = "later"
		}
	default:
		c.secrets.AgeChoice = "later"
	}
	return nil
}

// review shows the plan and what the profiles dropped, and asks which of
// options (Install first).
func (k *asker) review(c *choices, options ...string) string {
	if _, err := user.Lookup(k.a.User); err != nil {
		fmt.Fprintf(k.out, "\nFor user: %s (new account)\n", k.a.User)
	} else {
		fmt.Fprintf(k.out, "\nFor user: %s\n", k.a.User)
	}
	for _, it := range c.items {
		if it.Hidden() {
			continue
		}
		fmt.Fprintf(k.out, "  ✓ %-40s %s\n", it.Label, it.Describe())
	}
	if len(c.miseExtra) > 0 {
		fmt.Fprintf(k.out, "  ✓ %-40s mise use -g %s\n", "Extra mise tools", strings.Join(c.miseExtra, " "))
	}
	if len(c.mcpServers) > 0 {
		port := k.a.MCPPort
		if port == 0 && k.port != nil {
			port = k.port()
		}
		fmt.Fprintf(k.out, "  MCPJungle: http://%s:%d/mcp · servers: %s\n", k.a.MCPListen, port, strings.Join(k.a.MCPServers, " "))
	}
	if k.a.Git.Name+k.a.Git.Email != "" {
		fmt.Fprintf(k.out, "  Git: %s <%s>\n", k.a.Git.Name, k.a.Git.Email)
	}
	listDropped(k.out, k.cat, k.dropped)
	ch, err := ui.Choose("Ready?", options, options[0])
	if err != nil {
		return "Quit"
	}
	return ch
}

func homeOf(name string) string {
	if u, err := user.Lookup(name); err == nil {
		return u.HomeDir
	}
	return "/home/" + name
}

// readAsOwner reads a file under login's ~/.mcp as its owner: the
// gateway's service user, or login before there is one. A symlink planted
// there then can't show root's files.
func readAsOwner(login, path string) []byte {
	owner := "mcp-" + login
	if _, err := user.Lookup(owner); err != nil {
		owner = login
	}
	out, err := exec.Command("runuser", "-u", owner, "--", "cat", "--", path).Output()
	if err != nil {
		return nil
	}
	return out
}

// hasPassword reports whether the user has a usable password (passwd -S).
func hasPassword(name string) bool {
	out, err := exec.Command("passwd", "-S", name).Output()
	f := strings.Fields(string(out))
	return err == nil && len(f) > 1 && f[1] == "P"
}

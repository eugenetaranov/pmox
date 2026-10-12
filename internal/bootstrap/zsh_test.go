package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readEmbedded(t *testing.T, p string) string {
	t.Helper()
	b, err := files.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The template sources the completion drop-in before oh-my-zsh (which
// runs compinit), builds plugins from installed tools, and keeps
// zsh-syntax-highlighting last.
func TestZshrcTemplateOrder(t *testing.T) {
	tmpl := readEmbedded(t, "conf/zshrc.tmpl")
	comp := strings.Index(tmpl, "source ~/.config/devbox/completions.zsh")
	omz := strings.Index(tmpl, `source "$ZSH/oh-my-zsh.sh"`)
	if comp < 0 || omz < 0 || comp > omz {
		t.Fatalf("completions.zsh must be sourced before oh-my-zsh (comp=%d omz=%d)", comp, omz)
	}
	if strings.Contains(tmpl, "devbox_zsh_plugins") {
		t.Error("the plugin list must be built at shell start, not substituted")
	}
	last := strings.LastIndex(tmpl, "plugins+=(")
	if last < 0 || !strings.Contains(tmpl[last:strings.Index(tmpl[last:], "\n")+last], "zsh-syntax-highlighting)") {
		t.Error("zsh-syntax-highlighting must be the last plugin added")
	}
	if strings.Index(tmpl, "devbox_bash_completers") < omz {
		t.Error("bash-style completers must be registered after oh-my-zsh's compinit")
	}
	if strings.Contains(readEmbedded(t, "devbox-setup"), "ZSH_PLUGINS") {
		t.Error("devbox-setup still carries a fixed plugin list")
	}
}

func needZsh(t *testing.T) string {
	t.Helper()
	z, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	return z
}

func TestZshFilesParse(t *testing.T) {
	z := needZsh(t)
	for _, p := range []string{"conf/completions.zsh", "conf/zshrc.tmpl", "conf/shell.sh", "conf/p10k-devbox.zsh"} {
		f := filepath.Join(t.TempDir(), filepath.Base(p))
		if err := os.WriteFile(f, []byte(readEmbedded(t, p)), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(z, "-n", f).CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", p, err, out)
		}
	}
}

// completions.zsh caches a tool's #compdef script once, skips output that
// isn't one, does nothing on a warm start, and refreshes after an upgrade.
func TestCompletionCache(t *testing.T) {
	z := needZsh(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	// "just" prints a real completion; "yq" prints junk.
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho "+name+" >> "+calls+"\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("just", "printf '#compdef just\\n_just() { :; }\\n'\n")
	write("yq", "echo 'unknown command'\n")
	dropin := filepath.Join(dir, "completions.zsh")
	if err := os.WriteFile(dropin, []byte(readEmbedded(t, "conf/completions.zsh")), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		c := exec.Command(z, "-f", "-c", "source "+dropin+"; print -r -- $fpath[1]")
		c.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + dir, "XDG_CACHE_HOME=" + cache}
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("zsh: %v\n%s", err, out)
		}
		if got := strings.TrimSpace(string(out)); got != filepath.Join(cache, "devbox", "zsh-completions") {
			t.Fatalf("fpath[1] = %q", got)
		}
	}
	countCalls := func(name string) int {
		b, _ := os.ReadFile(calls)
		return strings.Count(string(b), name+"\n")
	}

	run()
	compDir := filepath.Join(cache, "devbox", "zsh-completions")
	if b, err := os.ReadFile(filepath.Join(compDir, "_just")); err != nil || !strings.HasPrefix(string(b), "#compdef just") {
		t.Fatalf("_just not cached: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(compDir, "_yq")); err == nil {
		t.Error("junk output must not be cached")
	}

	run() // warm: just is current, so it isn't run again
	if n := countCalls("just"); n != 1 {
		t.Errorf("just ran %d times, want 1 (warm start must do no work)", n)
	}

	// Upgrade: the binary becomes newer than its cache file.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(bin, "just"), future, future); err != nil {
		t.Fatal(err)
	}
	run()
	if n := countCalls("just"); n != 2 {
		t.Errorf("just ran %d times after an upgrade, want 2", n)
	}
}

// The p10k item's config is the lean preset from the clone plus our
// overrides; `p10k configure` must still write ~/.p10k.zsh, not the preset.
func TestP10kConfig(t *testing.T) {
	c := readEmbedded(t, "conf/p10k-devbox.zsh")
	src := strings.Index(c, "source ~/.oh-my-zsh/custom/themes/powerlevel10k/config/p10k-lean.zsh")
	if src < 0 {
		t.Fatal("must source p10k's lean preset from the clone")
	}
	for _, want := range []string{
		"POWERLEVEL9K_LEFT_PROMPT_ELEMENTS=(os_icon dir vcs prompt_char)",
		"(${POWERLEVEL9K_RIGHT_PROMPT_ELEMENTS:#newline} time)",
		"POWERLEVEL9K_MODE=nerdfont-v3",
		"POWERLEVEL9K_PROMPT_ADD_NEWLINE=false",
		"POWERLEVEL9K_TRANSIENT_PROMPT=always",
		"POWERLEVEL9K_INSTANT_PROMPT=quiet",
	} {
		if i := strings.Index(c, want); i < src {
			t.Errorf("missing (after the preset): %s", want)
		}
	}
	if i := strings.Index(c, "POWERLEVEL9K_CONFIG_FILE=${${(%):-%x}:a}"); i < src {
		t.Error("POWERLEVEL9K_CONFIG_FILE must be reset after the preset sets it to itself")
	}
	if !strings.HasSuffix(strings.TrimSpace(c), "p10k reload") {
		t.Error("must end with p10k reload so the overrides take effect")
	}
	script := readEmbedded(t, "devbox-setup")
	if !strings.Contains(script, `[ -e "$home/.p10k.zsh" ] || `) {
		t.Error("install_p10k must keep an existing ~/.p10k.zsh")
	}
}

// The instant prompt runs first, and only for the p10k theme.
func TestZshrcInstantPrompt(t *testing.T) {
	tmpl := readEmbedded(t, "conf/zshrc.tmpl")
	ip := strings.Index(tmpl, "p10k-instant-prompt-")
	comp := strings.Index(tmpl, "source ~/.config/devbox/completions.zsh")
	if ip < 0 || ip > comp {
		t.Fatalf("instant prompt must come before everything else (ip=%d comp=%d)", ip, comp)
	}
	if !strings.Contains(tmpl, "$ZSH_THEME == powerlevel10k/*") {
		t.Error("instant prompt must be guarded on the p10k theme")
	}
}

// Sourced after a preset that defines BATTERY_STAGES as an array (as
// p10k-lean.zsh does), the overrides must not stop with a type error.
func TestP10kConfigSources(t *testing.T) {
	z := needZsh(t)
	home := t.TempDir()
	cfg := filepath.Join(home, ".oh-my-zsh/custom/themes/powerlevel10k/config")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	preset := "typeset -g POWERLEVEL9K_RIGHT_PROMPT_ELEMENTS=(status newline)\n" +
		"typeset -g POWERLEVEL9K_BATTERY_STAGES=('a' 'b')\n" +
		"typeset -g POWERLEVEL9K_CONFIG_FILE=${${(%):-%x}:a}\n"
	if err := os.WriteFile(filepath.Join(cfg, "p10k-lean.zsh"), []byte(preset), 0o644); err != nil {
		t.Fatal(err)
	}
	p10k := filepath.Join(home, ".p10k.zsh")
	if err := os.WriteFile(p10k, []byte(readEmbedded(t, "conf/p10k-devbox.zsh")), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(z, "-f", "-c", `source ~/.p10k.zsh && print -r -- "$POWERLEVEL9K_CONFIG_FILE|${POWERLEVEL9K_RIGHT_PROMPT_ELEMENTS[*]}"`)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sourcing failed: %v\n%s", err, out)
	}
	want := p10k + "|status time"
	if got := strings.TrimSpace(string(out)); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

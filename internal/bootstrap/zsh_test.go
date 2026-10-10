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
	tmpl := readEmbedded(t, "assets/zshrc.tmpl")
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
	for _, p := range []string{"conf/completions.zsh", "assets/zshrc.tmpl", "conf/shell.sh"} {
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

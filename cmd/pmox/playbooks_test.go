package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const play = "name: x\nhosts: all\ntasks: []\n"

func TestDiscoverPlaybooks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "site.yml"), play)
	writeFile(t, filepath.Join(dir, "web.yaml"), "- name: w\n  hosts: all\n")
	writeFile(t, filepath.Join(dir, "vars.yml"), "foo: bar\n")
	writeFile(t, filepath.Join(dir, "playbooks", "db.yml"), play)
	writeFile(t, filepath.Join(dir, "roles", "docker", "tasks", "main.yml"), play)
	writeFile(t, filepath.Join(dir, ".github", "workflows", "ci.yml"), play)
	writeFile(t, filepath.Join(dir, "README.md"), "hosts: all\n")
	got, err := discoverPlaybooks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"playbooks/db.yml", "site.yml", "web.yaml"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if p, ok := findPlaybook(dir, "playbooks/db"); !ok || !strings.HasSuffix(p, "db.yml") {
		t.Errorf("findPlaybook(playbooks/db) = %q %v", p, ok)
	}
}

func TestIsGitURL(t *testing.T) {
	for url, want := range map[string]bool{
		"https://github.com/me/infra.git": true,
		"git@github.com:me/infra.git":     true,
		"ssh://git@host/me/infra":         true,
		"file:///tmp/repo":                true,
		"web":                             false,
		"./local/dir":                     false,
	} {
		if got := isGitURL(url); got != want {
			t.Errorf("isGitURL(%q) = %v", url, got)
		}
	}
}

func TestChooseDefaultPlaybookNonInteractive(t *testing.T) {
	found := []string{"a.yml", "playbooks/db.yml", "site.yml"}
	if got, _ := chooseDefaultPlaybook(found, "", false); got != "site.yml" {
		t.Errorf("preferred name: got %q", got)
	}
	if got, _ := chooseDefaultPlaybook(found, "playbooks/db.yml", false); got != "playbooks/db.yml" {
		t.Errorf("current wins: got %q", got)
	}
	if got, _ := chooseDefaultPlaybook([]string{"b.yml", "c.yml"}, "", false); got != "b.yml" {
		t.Errorf("first: got %q", got)
	}
}

// gitRepo creates a local git repo with files and returns its file:// URL.
func gitRepo(t *testing.T, files map[string]string) (dir, url string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	dir = t.TempDir()
	for name, content := range files {
		writeFile(t, filepath.Join(dir, name), content)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir, "file://" + dir
}

func runApplyCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newApplyCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestApplyInitFromURL(t *testing.T) {
	isolate(t)
	if err := (&config.Config{Servers: map[string]*config.Server{}}).Save(); err != nil {
		t.Fatal(err)
	}
	src, url := gitRepo(t, map[string]string{"site.yml": play, "web.yml": play, "roles/r/tasks/main.yml": play})
	dir, _ := tackDir()
	writeFile(t, filepath.Join(dir, "playbook.yaml"), play) // a scaffolded starter exists

	// Non-interactively an existing dir needs -y.
	if out, err := runApplyCmd(t, "--init", url); err == nil || !strings.Contains(err.Error(), "pass -y") {
		t.Fatalf("want a -y refusal, got %v\n%s", err, out)
	}
	out, err := runApplyCmd(t, "--init", url, "-y")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if !strings.Contains(out, "previous playbooks kept in") || !strings.Contains(out, "default: site") {
		t.Errorf("output:\n%s", out)
	}
	if matches, _ := filepath.Glob(dir + ".bak-*"); len(matches) != 1 {
		t.Errorf("backup not created: %v", matches)
	}
	if cfg, _ := config.Load(); cfg.TackDefaultPlaybook != "site.yml" {
		t.Errorf("default = %q", cfg.TackDefaultPlaybook)
	}
	if p, _, _ := defaultPlaybook(); !strings.HasSuffix(p, "site.yml") {
		t.Errorf("defaultPlaybook = %q", p)
	}

	// Same URL again: nothing to do.
	if out, _ := runApplyCmd(t, "--init", url); !strings.Contains(out, "already cloned") {
		t.Errorf("re-init:\n%s", out)
	}

	// Upstream adds a playbook and drops the default; --update pulls and
	// re-settles the default.
	writeFile(t, filepath.Join(src, "main.yml"), play)
	for _, args := range [][]string{{"rm", "-q", "site.yml"}, {"add", "."}, {"commit", "-q", "-m", "next"}} {
		if o, err := exec.Command("git", append([]string{"-C", src}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, o)
		}
	}
	out, err = runApplyCmd(t, "--update")
	if err != nil || !strings.Contains(out, "is gone after the update") {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if cfg, _ := config.Load(); cfg.TackDefaultPlaybook != "main.yml" {
		t.Errorf("default after update = %q, want main.yml", cfg.TackDefaultPlaybook)
	}

	// --default switches by name.
	if out, err := runApplyCmd(t, "--default", "web"); err != nil || !strings.Contains(out, "default playbook: web") {
		t.Fatalf("--default web: %v\n%s", err, out)
	}
	if out, err := runApplyCmd(t, "--default", "nope"); err == nil || !strings.Contains(err.Error(), "available: main, web") {
		t.Errorf("unknown name: %v\n%s", err, out)
	}
}

func TestApplyInitRejectsNonURL(t *testing.T) {
	isolate(t)
	if _, err := runApplyCmd(t, "--init", "web1"); err == nil || !strings.Contains(err.Error(), "not a git URL") {
		t.Errorf("err = %v", err)
	}
}

func TestApplyInitFromURLAsksWhichDefault(t *testing.T) {
	isolate(t)
	forceInteractive(t)
	if err := (&config.Config{Servers: map[string]*config.Server{}}).Save(); err != nil {
		t.Fatal(err)
	}
	_, url := gitRepo(t, map[string]string{"site.yml": play, "web.yml": play})
	var offered []string
	var start string
	orig := pickPlaybookFn
	pickPlaybookFn = func(_ string, opts []huh.Option[string], fallback string) (string, error) {
		for _, o := range opts {
			offered = append(offered, o.Value)
		}
		start = fallback
		return "web.yml", nil
	}
	t.Cleanup(func() { pickPlaybookFn = orig })

	if out, err := runApplyCmd(t, "--init", url); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if !slices.Equal(offered, []string{"site.yml", "web.yml"}) || start != "site.yml" {
		t.Errorf("offered %v starting at %q", offered, start)
	}
	if cfg, _ := config.Load(); cfg.TackDefaultPlaybook != "web.yml" {
		t.Errorf("default = %q, want the picked web.yml", cfg.TackDefaultPlaybook)
	}
}

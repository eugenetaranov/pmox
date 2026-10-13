package bootstrap

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/eugenetaranov/pmox/internal/config"
)

type writeFile struct {
	Path        string `yaml:"path"`
	Permissions string `yaml:"permissions"`
	Encoding    string `yaml:"encoding"`
	Content     string `yaml:"content"`
}

func decode(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parse(t *testing.T, b []byte) (map[string]any, []writeFile) {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("output is not YAML: %v\n%s", err, b)
	}
	var wf struct {
		WriteFiles []writeFile `yaml:"write_files"`
	}
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatal(err)
	}
	return doc, wf.WriteFiles
}

func TestInjectStarterTemplate(t *testing.T) {
	in, err := config.RenderTemplate("ubuntu", "ssh-ed25519 AAAA test")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Inject(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("#cloud-config\n")) {
		t.Errorf("missing #cloud-config header:\n%.80s", out)
	}
	doc, wfs := parse(t, out)
	if doc["package_update"] != true || doc["runcmd"] == nil || doc["users"] == nil {
		t.Errorf("original keys lost: %v", doc)
	}
	if boot, _ := doc["bootcmd"].([]any); len(boot) != 1 || boot[0] != stopAutoUpgrades {
		t.Errorf("bootcmd = %v", doc["bootcmd"])
	}
	// yardo itself is pushed over SSH, never put in user-data.
	if len(wfs) != 1 || wfs[0].Path != noAutoUpgrades.Path || wfs[0].Permissions != "0644" || wfs[0].Encoding != "gz+b64" {
		t.Fatalf("write_files = %+v, want only %s", wfs, noAutoUpgrades.Path)
	}
	if !bytes.Equal(decode(t, wfs[0].Content), noAutoUpgrades.Content) {
		t.Errorf("decoded content differs")
	}
}

func TestInjectKeepsExistingWriteFiles(t *testing.T) {
	in := []byte("#cloud-config\n# my comment\nwrite_files:\n  - path: /etc/motd\n    content: hi\n")
	out, err := Inject(in)
	if err != nil {
		t.Fatal(err)
	}
	_, wfs := parse(t, out)
	if len(wfs) != 2 || wfs[0].Path != "/etc/motd" {
		t.Errorf("existing entry not kept first: %+v", wfs)
	}
	if !strings.Contains(string(out), "# my comment") {
		t.Errorf("comment lost:\n%s", out)
	}
}

func TestInjectEmptyWriteFiles(t *testing.T) {
	out, err := Inject([]byte("#cloud-config\nwrite_files:\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, wfs := parse(t, out); len(wfs) != 1 {
		t.Errorf("got %d entries", len(wfs))
	}
}

func TestInjectKeepsExistingBootcmd(t *testing.T) {
	out, err := Inject([]byte("#cloud-config\nbootcmd:\n  - echo hi\n"))
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := parse(t, out)
	if boot, _ := doc["bootcmd"].([]any); len(boot) != 2 || boot[0] != "echo hi" {
		t.Errorf("bootcmd = %v", doc["bootcmd"])
	}
}

func TestInjectGuardsBootcmd(t *testing.T) {
	if !strings.HasPrefix(stopAutoUpgrades, "[ -e "+AutoUpgradesMarker+" ] ||") {
		t.Errorf("bootcmd does not yield to the opt-in marker: %s", stopAutoUpgrades)
	}
	// A cloud-init file with an older form (no guard, or the marker's old
	// path) gets the current command in its place, not both.
	for _, prev := range oldStopAutoUpgrades {
		out, err := Inject([]byte("#cloud-config\nbootcmd:\n  - \"" + prev + "\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		doc, _ := parse(t, out)
		if boot, _ := doc["bootcmd"].([]any); len(boot) != 1 || boot[0] != stopAutoUpgrades {
			t.Errorf("from %q: bootcmd = %v", prev, doc["bootcmd"])
		}
	}
}

func TestStarterTemplateBootcmdMatches(t *testing.T) {
	in, err := config.RenderTemplate("ubuntu", "ssh-ed25519 AAAA test")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(in, &doc); err != nil {
		t.Fatal(err)
	}
	if boot, _ := doc["bootcmd"].([]any); len(boot) != 1 || boot[0] != stopAutoUpgrades {
		t.Errorf("starter template bootcmd = %v, want %q", doc["bootcmd"], stopAutoUpgrades)
	}
}

func TestInjectRejectsNonMapping(t *testing.T) {
	for _, in := range []string{"#cloud-config\n- a\n- b\n", "key: [unclosed\n", "write_files: nope\n"} {
		if _, err := Inject([]byte(in)); err == nil {
			t.Errorf("Inject(%q) = nil error", in)
		}
	}
}

func TestYardoInstallCommand(t *testing.T) {
	got := YardoInstallCommand("v1.2.3")
	for _, want := range []string{"sudo -n sh -c '", yardoRepo + "/releases/download/v1.2.3/install.sh", "| sh -s -- --version v1.2.3'"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s\nlacks %q", got, want)
		}
	}
	if got := YardoInstallCommand("latest"); !strings.Contains(got, "/releases/latest/download/install.sh") || strings.Contains(got, "--version") {
		t.Errorf("latest: %s", got)
	}
	if Yardo() != "latest" {
		t.Errorf("default Yardo() = %s, want latest", Yardo())
	}
	t.Setenv("PMOX_YARDO_VERSION", "v9.9.9")
	if Yardo() != "v9.9.9" {
		t.Errorf("Yardo() = %s", Yardo())
	}
}

// TeamSyncCommand, run against temp directories: the team's files land,
// the ones it shipped before and no longer ships go, others stay.
func TestTeamSync(t *testing.T) {
	d := t.TempDir()
	script := strings.TrimSuffix(strings.TrimPrefix(TeamSyncCommand, "sudo -n sh -c '"), "'")
	script = strings.NewReplacer("/etc/yardo/catalog.d", d+"/catalog", "/usr/local/share/mcp-catalog", d+"/mcp").Replace(script)
	if strings.Contains(script, "'") {
		t.Fatal("the sync script can't hold a single quote: it is sent inside one")
	}
	run := func(files []File) {
		t.Helper()
		tgz, err := TeamTarball(files)
		if err != nil {
			t.Fatal(err)
		}
		c := exec.Command("sh", "-c", script)
		c.Stdin = bytes.NewReader(tgz)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("sync: %v: %s", err, out)
		}
	}
	if err := os.MkdirAll(d+"/catalog", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d+"/catalog/mine.toml", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ls := func(dir, glob string) string {
		m, _ := filepath.Glob(filepath.Join(d, dir, glob))
		for i := range m {
			m[i] = filepath.Base(m[i])
		}
		return strings.Join(m, " ")
	}
	run([]File{{Path: "catalog.d/a.toml", Content: []byte("a")}, {Path: "catalog.d/b.toml"}, {Path: "mcp-catalog/acme.spec"}})
	if got := ls("catalog", "*.toml"); got != "a.toml b.toml mine.toml" {
		t.Errorf("first push: %s", got)
	}
	if got := ls("mcp", "*.spec"); got != "acme.spec" {
		t.Errorf("specs: %s", got)
	}
	run([]File{{Path: "catalog.d/b.toml"}})
	if got := ls("catalog", "*.toml") + " | " + ls("mcp", "*.spec"); got != "b.toml mine.toml | " {
		t.Errorf("after dropping a.toml and acme.spec: %s", got)
	}
	run(nil)
	if got := ls("catalog", "*.toml"); got != "mine.toml" {
		t.Errorf("no team files: %s", got)
	}
}

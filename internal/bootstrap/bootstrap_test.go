package bootstrap

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/snippet"
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
	if len(out) > snippet.MaxBytes {
		t.Errorf("user-data is %d bytes, over the %d snippet limit", len(out), snippet.MaxBytes)
	}
	doc, wfs := parse(t, out)
	if doc["package_update"] != true || doc["runcmd"] == nil || doc["users"] == nil {
		t.Errorf("original keys lost: %v", doc)
	}
	want := map[string]File{noAutoUpgrades.Path: noAutoUpgrades}
	for _, f := range Files() {
		want[f.Path] = f
	}
	if boot, _ := doc["bootcmd"].([]any); len(boot) != 1 || boot[0] != stopAutoUpgrades {
		t.Errorf("bootcmd = %v", doc["bootcmd"])
	}
	if len(wfs) != len(want) {
		t.Fatalf("got %d write_files, want %d", len(wfs), len(want))
	}
	for _, w := range wfs {
		f, ok := want[w.Path]
		if !ok {
			t.Errorf("unexpected path %s", w.Path)
			continue
		}
		if w.Encoding != "gz+b64" || w.Permissions != f.Permissions {
			t.Errorf("%s: encoding %q permissions %q", w.Path, w.Encoding, w.Permissions)
		}
		if !bytes.Equal(decode(t, w.Content), f.Content) {
			t.Errorf("%s: decoded content differs", w.Path)
		}
	}
	for _, p := range []string{ScriptPath, ShareDir + "/picker.py", ShareDir + "/mcp-sync", ShareDir + "/mcp-catalog/jira.spec", ShareDir + "/zshrc.tmpl", ShareDir + "/conf/shell.sh", ShareDir + "/conf/daemon.json"} {
		if _, ok := want[p]; !ok {
			t.Errorf("missing %s", p)
		}
	}
}

func TestInjectKeepsExistingWriteFiles(t *testing.T) {
	in := []byte("#cloud-config\n# my comment\nwrite_files:\n  - path: /etc/motd\n    content: hi\n")
	out, err := Inject(in)
	if err != nil {
		t.Fatal(err)
	}
	_, wfs := parse(t, out)
	if wfs[0].Path != "/etc/motd" || len(wfs) != len(Files())+2 {
		t.Errorf("existing entry not kept first: %+v", wfs[0])
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
	if _, wfs := parse(t, out); len(wfs) != len(Files())+1 {
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
	// A cloud-init file written before the guard gets the guarded command
	// in place of the old one, not both.
	out, err := Inject([]byte("#cloud-config\nbootcmd:\n  - \"" + legacyStopAutoUpgrades + "\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := parse(t, out)
	if boot, _ := doc["bootcmd"].([]any); len(boot) != 1 || boot[0] != stopAutoUpgrades {
		t.Errorf("bootcmd = %v", doc["bootcmd"])
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

func TestScriptUsesMarker(t *testing.T) {
	script, _ := files.ReadFile("devbox-setup")
	if !strings.Contains(string(script), "AUTO_UPGRADES_MARKER="+AutoUpgradesMarker+"\n") {
		t.Errorf("script's AUTO_UPGRADES_MARKER does not match %s", AutoUpgradesMarker)
	}
}

func TestScriptPointsAtShareDir(t *testing.T) {
	script, _ := files.ReadFile("devbox-setup")
	if !strings.Contains(string(script), "SHARE=${DEVBOX_SETUP_SHARE:-"+ShareDir+"}") {
		t.Errorf("script's SHARE default does not match ShareDir %s", ShareDir)
	}
}

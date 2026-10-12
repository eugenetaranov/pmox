package bootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
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
	// devbox-setup itself is pushed over SSH, never put in user-data.
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

// TestPickerPy runs picker.py's own tests (search, dedupe, registry
// parsing) against the real catalog and the registry snapshot.
func TestPickerPy(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	cmd := exec.Command(py, "-m", "unittest", "picker_test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("picker_test: %v\n%s", err, out)
	}
}

func TestTarball(t *testing.T) {
	tgz, err := Tarball()
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]File{}
	for _, f := range Files() {
		want[strings.TrimPrefix(f.Path, "/")] = f
	}
	tr := tar.NewReader(zr)
	seen := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		f, ok := want[h.Name]
		if !ok || h.Typeflag != tar.TypeReg {
			t.Errorf("unexpected entry %s (type %c); only files, so / and /usr keep their modes", h.Name, h.Typeflag)
			continue
		}
		seen++
		if got := fmt.Sprintf("%04o", h.Mode); got != f.Permissions || h.Uid != 0 || h.Gid != 0 {
			t.Errorf("%s: mode %s uid %d gid %d, want %s root", h.Name, got, h.Uid, h.Gid, f.Permissions)
		}
		b, _ := io.ReadAll(tr)
		if !bytes.Equal(b, f.Content) {
			t.Errorf("%s: content differs", h.Name)
		}
	}
	if seen != len(want) {
		t.Errorf("tarball has %d of %d files", seen, len(want))
	}
	for _, p := range []string{ScriptPath, ShareDir + "/picker.py", ShareDir + "/mise-registry.tsv", ShareDir + "/mcp-sync", ShareDir + "/mcp-catalog/jira.spec", ShareDir + "/conf/zshrc.tmpl", ShareDir + "/conf/vimrc", ShareDir + "/conf/shell.sh", ShareDir + "/conf/completions.zsh", ShareDir + "/conf/daemon.json"} {
		if _, ok := want[strings.TrimPrefix(p, "/")]; !ok {
			t.Errorf("missing %s", p)
		}
	}
	if want[ScriptPath[1:]].Permissions != "0755" || want[strings.TrimPrefix(ShareDir, "/")+"/mcp-sync"].Permissions != "0755" {
		t.Errorf("script or mcp-sync not executable")
	}
}

// TestFullRegistryShipped: with no user-data limit, the VM gets the
// snapshot with descriptions, so search shows them offline.
func TestFullRegistryShipped(t *testing.T) {
	src, _ := files.ReadFile("mise-registry.tsv")
	for _, f := range Files() {
		if strings.HasSuffix(f.Path, "/mise-registry.tsv") && !bytes.Equal(f.Content, src) {
			t.Errorf("%s differs from the snapshot", f.Path)
		}
	}
}

func TestAssetsOnlySynced(t *testing.T) {
	_ = fs.WalkDir(files, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if p != "assets/mcp-sync" && p != "assets/SOURCE" && !strings.HasPrefix(p, "assets/mcp-catalog/") {
			t.Errorf("%s is in assets/, which bootstrap:sync owns; devbox-setup's own files go in conf/", p)
		}
		return nil
	})
}

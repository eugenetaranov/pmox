package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestGumPinsMatchRelease: the pinned SHA-256s are the ones gum published
// for GUM_VERSION (testdata/gum-<version>-checksums.txt, recorded when the
// version was bumped).
func TestGumPinsMatchRelease(t *testing.T) {
	script, _ := files.ReadFile("devbox-setup")
	ver := regexp.MustCompile(`(?m)^GUM_VERSION=(\S+)$`).FindSubmatch(script)
	if ver == nil {
		t.Fatal("no GUM_VERSION")
	}
	sums, err := os.ReadFile(filepath.Join("testdata", "gum-"+string(ver[1])+"-checksums.txt"))
	if err != nil {
		t.Fatalf("record gum's checksums.txt for v%s: %v", ver[1], err)
	}
	for _, arch := range []string{"x86_64", "arm64"} {
		pin := regexp.MustCompile(`(?m)^GUM_SHA256_` + arch + `=([0-9a-f]{64})$`).FindSubmatch(script)
		if pin == nil {
			t.Errorf("no GUM_SHA256_%s pin", arch)
			continue
		}
		line := string(pin[1]) + "  gum_" + string(ver[1]) + "_Linux_" + arch + ".tar.gz"
		if !strings.Contains(string(sums), line) {
			t.Errorf("GUM_SHA256_%s does not match the release's checksums.txt", arch)
		}
	}
}

// TestGumMismatchInstallsNothing runs ensure_gum with a curl that
// downloads garbage: it must fail before extracting or installing.
func TestGumMismatchInstallsNothing(t *testing.T) {
	bash := bashPath(t)
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not installed")
	}
	b, _ := os.ReadFile("devbox-setup")
	script := string(b)
	fn := func(name string) string {
		i := strings.Index(script, "\n"+name+"() {")
		if i < 0 {
			t.Fatalf("no %s()", name)
		}
		s := script[i+1:]
		return s[:strings.Index(s, "\n}\n")+3]
	}
	pins := regexp.MustCompile(`(?m)^(GUM_VERSION|GUM_SHA256_\w+)=.*$`).FindAllString(script, -1)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	fake := filepath.Join(dir, "fake")
	for _, d := range []string{bin, fake} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// curl -fsSL -o FILE URL writes junk; id -u says root.
	for name, body := range map[string]string{
		"curl": "#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = -o ] && { echo junk > \"$2\"; shift; }; shift; done\n",
		"id":   "#!/bin/sh\necho 0\n",
	} {
		if err := os.WriteFile(filepath.Join(fake, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	body := strings.Join(pins, "\n") + "\nBIN=" + bin + "\n" +
		"say() { :; }\ndie() { echo \"die: $*\" >&2; exit 1; }\nrel_arch() { echo x86_64; }\n" +
		fn("ensure_gum") + "ensure_gum\n"
	cmd := exec.Command(bash, "-c", body)
	cmd.Env = append(os.Environ(), "PATH="+fake+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("ensure_gum succeeded with a bad download:\n%s", out)
	}
	if !strings.Contains(string(out), "does not match its pinned SHA-256") {
		t.Errorf("output = %s", out)
	}
	if _, err := os.Stat(filepath.Join(bin, "gum")); err == nil {
		t.Error("gum installed despite the mismatch")
	}
}

package bootstrap

import (
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func withBins(t *testing.T, fsys fstest.MapFS) {
	t.Helper()
	old := Bins
	Bins = fsys
	t.Cleanup(func() { Bins = old })
}

func paths() map[string]File {
	out := map[string]File{}
	for _, f := range Files() {
		out[f.Path] = f
	}
	return out
}

func TestFilesWithoutGoBuilds(t *testing.T) {
	withBins(t, fstest.MapFS{"README.md": {Data: []byte("x")}})
	p := paths()
	if _, ok := p[ScriptPath]; !ok {
		t.Error("without Go builds the bash script goes to " + ScriptPath)
	}
	if _, ok := p[LegacyScriptPath]; ok {
		t.Error("no legacy copy without Go builds")
	}
}

func TestFilesWithGoBuilds(t *testing.T) {
	withBins(t, fstest.MapFS{
		"devbox-setup-linux-amd64.gz": {Data: gz(t, "amd64 build")},
		"devbox-setup-linux-arm64.gz": {Data: gz(t, "arm64 build")},
	})
	p := paths()
	if _, ok := p[ScriptPath]; ok {
		t.Error("with Go builds, install.sh writes " + ScriptPath + ", not the tarball")
	}
	for _, want := range []string{LegacyScriptPath, ShareDir + "/install.sh", ShareDir + "/bin/devbox-setup-linux-amd64.gz", ShareDir + "/bin/devbox-setup-linux-arm64.gz"} {
		if _, ok := p[want]; !ok {
			t.Errorf("tarball missing %s", want)
		}
	}
	if !strings.Contains(InstallCommand, "sh "+ShareDir+"/install.sh") {
		t.Errorf("InstallCommand doesn't run install.sh: %s", InstallCommand)
	}
}

// install.sh picks the build for uname -m, falls back to the bash script,
// and removes bin/.
func TestInstallScript(t *testing.T) {
	if _, err := exec.LookPath("gunzip"); err != nil {
		t.Skip("no gunzip")
	}
	src, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ uname, want string }{
		{"x86_64", "amd64 build"}, {"aarch64", "arm64 build"}, {"riscv64", "#!/bin/bash legacy"},
	} {
		root := t.TempDir()
		share := filepath.Join(root, "share")
		must := func(err error) {
			if err != nil {
				t.Fatal(err)
			}
		}
		must(os.MkdirAll(filepath.Join(share, "bin"), 0o755))
		must(os.MkdirAll(filepath.Join(root, "bin"), 0o755))
		must(os.WriteFile(filepath.Join(share, "bin", "devbox-setup-linux-amd64.gz"), gz(t, "amd64 build"), 0o644))
		must(os.WriteFile(filepath.Join(share, "bin", "devbox-setup-linux-arm64.gz"), gz(t, "arm64 build"), 0o644))
		must(os.WriteFile(filepath.Join(share, "devbox-setup.sh"), []byte("#!/bin/bash legacy"), 0o755))
		fake := filepath.Join(root, "fakebin")
		must(os.MkdirAll(fake, 0o755))
		must(os.WriteFile(filepath.Join(fake, "uname"), []byte("#!/bin/sh\necho "+tc.uname+"\n"), 0o755))
		script := strings.NewReplacer("/usr/local/share/devbox-setup", share, "/usr/local/bin/devbox-setup", filepath.Join(root, "bin", "devbox-setup")).Replace(string(src))
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = append(os.Environ(), "PATH="+fake+":"+os.Getenv("PATH"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", tc.uname, err, out)
		}
		got, err := os.ReadFile(filepath.Join(root, "bin", "devbox-setup"))
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: installed %q (%v), want %q", tc.uname, got, err, tc.want)
		}
		if fi, _ := os.Stat(filepath.Join(root, "bin", "devbox-setup")); fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: mode %v", tc.uname, fi.Mode().Perm())
		}
		if _, err := os.Stat(filepath.Join(share, "bin")); err == nil {
			t.Errorf("%s: bin/ not removed", tc.uname)
		}
	}
}

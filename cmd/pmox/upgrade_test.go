package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
)

func TestParseAndCompareVersion(t *testing.T) {
	for _, s := range []string{"0.35.0", "v0.35.0", " 1.2.3 "} {
		if _, ok := parseVersion(s); !ok {
			t.Errorf("parseVersion(%q) failed", s)
		}
	}
	for _, s := range []string{"dev", "v0.35.0-3-gabc123", "0.35.0-dirty", "1.2", "x.y.z", ""} {
		if _, ok := parseVersion(s); ok {
			t.Errorf("parseVersion(%q) should not be a release", s)
		}
	}
	a, _ := parseVersion("0.34.1")
	b, _ := parseVersion("0.35.0")
	c, _ := parseVersion("0.9.10")
	d, _ := parseVersion("0.10.0")
	if !a.less(b) || b.less(a) || !c.less(d) || a.less(a) {
		t.Error("version ordering is wrong")
	}
}

func withExecutable(t *testing.T, path string) {
	t.Helper()
	orig := executablePathFn
	executablePathFn = func() (string, error) { return path, nil }
	t.Cleanup(func() { executablePathFn = orig })
}

func TestDetectInstall(t *testing.T) {
	cases := map[string]installMethod{
		"/opt/homebrew/Cellar/pmox/0.34.1/bin/pmox":              installBrew,
		"/home/linuxbrew/.linuxbrew/Cellar/pmox/0.34.1/bin/pmox": installBrew,
		"/usr/local/bin/pmox":                                    installBinary,
		"/home/u/go/bin/pmox":                                    installBinary,
	}
	for p, want := range cases {
		withExecutable(t, p)
		info, err := detectInstall()
		if err != nil || info.method != want {
			t.Errorf("%s: method %v err %v, want %v", p, info.method, err, want)
		}
		if want == installBrew && info.brew == "" {
			t.Errorf("%s: no brew path", p)
		}
	}

	// A symlink into the Cellar counts as Homebrew.
	dir := t.TempDir()
	cellar := filepath.Join(dir, "Cellar", "pmox", "0.34.1", "bin")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(cellar, "pmox")
	if err := os.WriteFile(real, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "bin-pmox")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	withExecutable(t, link)
	if info, _ := detectInstall(); info.method != installBrew {
		t.Errorf("symlink into the Cellar: method %v", info.method)
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("archive")
	sum := sha256.Sum256(data)
	sums := []byte(hex.EncodeToString(sum[:]) + "  pmox_1.0.0_linux_amd64.tar.gz\nffff  other.tar.gz\n")
	if err := verifyChecksum(sums, "pmox_1.0.0_linux_amd64.tar.gz", data); err != nil {
		t.Errorf("match: %v", err)
	}
	if err := verifyChecksum(sums, "pmox_1.0.0_linux_amd64.tar.gz", []byte("tampered")); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("mismatch: %v", err)
	}
	if err := verifyChecksum(sums, "missing.tar.gz", data); err == nil {
		t.Error("an unlisted file must fail")
	}
}

func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, b := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	a := tarGz(t, map[string][]byte{"README.md": []byte("r"), "pmox": []byte("BIN")})
	b, err := extractBinary(a)
	if err != nil || string(b) != "BIN" {
		t.Fatalf("got %q, %v", b, err)
	}
	if _, err := extractBinary(tarGz(t, map[string][]byte{"README.md": []byte("r")})); err == nil {
		t.Error("an archive without pmox must fail")
	}
}

// fakeRelease serves a GitHub release whose pmox prints newVersion.
type fakeRelease struct {
	srv       *httptest.Server
	downloads atomic.Int32
}

func newFakeRelease(t *testing.T, newVersion string, tamper bool) *fakeRelease {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script binary")
	}
	fr := &fakeRelease{}
	name := fmt.Sprintf("pmox_%s_%s_%s.tar.gz", newVersion, runtime.GOOS, runtime.GOARCH)
	bin := []byte("#!/bin/sh\necho \"pmox version " + newVersion + " (commit: x, built: y)\"\n")
	archive := tarGz(t, map[string][]byte{"pmox": bin, "LICENSE": []byte("l")})
	sum := sha256.Sum256(archive)
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	if tamper {
		archive = append(archive, 0)
	}
	mux := http.NewServeMux()
	fr.srv = httptest.NewServer(mux)
	t.Cleanup(fr.srv.Close)
	mux.HandleFunc("/repos/"+upgradeRepo+"/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(release{Tag: "v" + newVersion, Assets: []releaseAsset{
			{Name: name, URL: fr.srv.URL + "/dl/" + name},
			{Name: "checksums.txt", URL: fr.srv.URL + "/dl/checksums.txt"},
		}})
	})
	mux.HandleFunc("/dl/"+name, func(w http.ResponseWriter, _ *http.Request) {
		fr.downloads.Add(1)
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(sums)) })
	orig := githubAPIBase
	githubAPIBase = fr.srv.URL
	t.Cleanup(func() { githubAPIBase = orig })
	return fr
}

func withVersion(t *testing.T, v string) {
	t.Helper()
	orig := version
	version = v
	t.Cleanup(func() { version = orig })
}

// recordAttached records brew/sudo calls; sudo install is performed so
// the replaced binary can be checked.
func recordAttached(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	orig := runAttachedFn
	runAttachedFn = func(name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "sudo" {
			a := args
			if a[0] == "-n" {
				a = a[1:]
			}
			b, err := os.ReadFile(a[3])
			if err != nil {
				return err
			}
			return os.WriteFile(a[4], b, 0o755)
		}
		return nil
	}
	t.Cleanup(func() { runAttachedFn = orig })
	return &calls
}

func runUpgrade(t *testing.T, f *upgradeFlags) (string, error) {
	t.Helper()
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())
	err := runVersionUpgrade(cmd, f)
	return out.String(), err
}

func installedTarget(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "pmox")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	withExecutable(t, target)
	if r, err := filepath.EvalSymlinks(target); err == nil {
		target = r // macOS: /var → /private/var, as detectInstall resolves it
	}
	return target
}

func TestUpgradeReplacesWritableBinary(t *testing.T) {
	newFakeRelease(t, "9.9.9", false)
	withVersion(t, "0.34.1")
	target := installedTarget(t)
	out, err := runUpgrade(t, &upgradeFlags{yes: true})
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(target)
	st, _ := os.Stat(target)
	if !strings.Contains(string(b), "pmox version 9.9.9") || st.Mode().Perm() != 0o755 {
		t.Errorf("target not replaced (mode %v): %q", st.Mode(), b)
	}
	if !strings.Contains(out, "✓ pmox upgraded 0.34.1 → 9.9.9") {
		t.Errorf("output: %q", out)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".pmox-upgrade-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestUpgradeChecksumMismatchKeepsBinary(t *testing.T) {
	newFakeRelease(t, "9.9.9", true)
	withVersion(t, "0.34.1")
	target := installedTarget(t)
	_, err := runUpgrade(t, &upgradeFlags{yes: true})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), "echo old") {
		t.Error("binary changed despite a checksum mismatch")
	}
}

func TestUpgradeUpToDateAndCheckDownloadNothing(t *testing.T) {
	fr := newFakeRelease(t, "9.9.9", false)
	installedTarget(t)

	withVersion(t, "9.9.9")
	out, err := runUpgrade(t, &upgradeFlags{})
	if err != nil || !strings.Contains(out, "✓ pmox 9.9.9 is up to date") {
		t.Errorf("up to date: %v %q", err, out)
	}

	withVersion(t, "0.34.1")
	out, err = runUpgrade(t, &upgradeFlags{check: true})
	if err != nil || !strings.Contains(out, "0.34.1 → 9.9.9 available") {
		t.Errorf("--check: %v %q", err, out)
	}
	if n := fr.downloads.Load(); n != 0 {
		t.Errorf("downloaded %d times, want 0", n)
	}
}

func TestUpgradeNeedsYesWithoutTerminal(t *testing.T) {
	newFakeRelease(t, "9.9.9", false)
	withVersion(t, "0.34.1")
	installedTarget(t)
	tui.SetNoInput(true)
	t.Cleanup(func() { tui.SetNoInput(false) })
	_, err := runUpgrade(t, &upgradeFlags{})
	if !errors.Is(err, exitcode.ErrUserInput) || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpgradeDeclined(t *testing.T) {
	newFakeRelease(t, "9.9.9", false)
	withVersion(t, "0.34.1")
	target := installedTarget(t)
	forceInteractive(t)
	orig := confirmUpgradeFn
	var asked string
	confirmUpgradeFn = func(q string, defYes bool) (bool, error) {
		asked = q
		if !defYes {
			t.Error("the upgrade prompt should default to Yes")
		}
		return false, nil
	}
	t.Cleanup(func() { confirmUpgradeFn = orig })
	_, err := runUpgrade(t, &upgradeFlags{})
	if !errors.Is(err, tui.ErrAborted) || !strings.Contains(asked, "0.34.1 → 9.9.9") {
		t.Fatalf("err=%v asked=%q", err, asked)
	}
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), "echo old") {
		t.Error("binary changed after declining")
	}
}

func TestUpgradeHomebrewRunsBrew(t *testing.T) {
	fr := newFakeRelease(t, "9.9.9", false)
	withVersion(t, "0.34.1")
	withExecutable(t, "/opt/homebrew/Cellar/pmox/0.34.1/bin/pmox")
	calls := recordAttached(t)
	if _, err := runUpgrade(t, &upgradeFlags{yes: true}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0], "brew upgrade "+upgradeFormula) {
		t.Errorf("calls = %v", *calls)
	}
	if fr.downloads.Load() != 0 {
		t.Error("a Homebrew install must not download the archive itself")
	}
	if _, err := runUpgrade(t, &upgradeFlags{yes: true, version: "9.9.9"}); !errors.Is(err, exitcode.ErrUserInput) {
		t.Errorf("--version with Homebrew: %v", err)
	}
}

func TestUpgradeUnwritableDirUsesSudo(t *testing.T) {
	newFakeRelease(t, "9.9.9", false)
	withVersion(t, "0.34.1")
	target := installedTarget(t)
	orig := dirWritableFn
	dirWritableFn = func(string) bool { return false }
	t.Cleanup(func() { dirWritableFn = orig })
	calls := recordAttached(t)
	tui.SetNoInput(true)
	t.Cleanup(func() { tui.SetNoInput(false) })
	if _, err := runUpgrade(t, &upgradeFlags{yes: true}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !strings.HasPrefix((*calls)[0], "sudo -n install -m 0755 ") || !strings.HasSuffix((*calls)[0], " "+target) {
		t.Errorf("calls = %v", *calls)
	}
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), "9.9.9") {
		t.Error("target not replaced through sudo")
	}
}

func TestVersionCommandOutputUnchanged(t *testing.T) {
	withVersion(t, "1.2.3")
	cmd := newVersionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "pmox version 1.2.3 (commit: ") {
		t.Errorf("version output = %q", out.String())
	}
}

package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// pmox version upgrade (openspec/specs/self-upgrade): find the latest
// release, work out how pmox was installed, and upgrade the same way.

const (
	upgradeRepo    = "eugenetaranov/pmox"
	upgradeFormula = "eugenetaranov/tap/pmox"
	maxBinarySize  = 200 << 20
)

// Seams for tests.
var (
	githubAPIBase    = "https://api.github.com"
	executablePathFn = os.Executable
	// runAttachedFn runs a command with the terminal attached (brew, sudo).
	runAttachedFn = func(name string, args ...string) error {
		c := exec.Command(name, args...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}
	confirmUpgradeFn = tui.Confirm
	// dirWritableFn reports whether pmox can create files in dir.
	dirWritableFn = dirWritable
)

type upgradeFlags struct {
	check   bool
	yes     bool
	version string
}

func newVersionUpgradeCmd() *cobra.Command {
	f := &upgradeFlags{}
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade pmox to the latest release",
		Long: `Upgrade pmox to the latest GitHub release, the same way it was
installed:

  Homebrew      runs 'brew upgrade ` + upgradeFormula + `'
  plain binary  downloads the release archive, verifies its sha256,
                and replaces the pmox binary (with sudo when its
                directory isn't writable; sudo asks for your password)

pmox asks before upgrading; --yes skips the question, and --check only
reports whether a newer release exists.`,
		Example: `  pmox version upgrade
  pmox version upgrade --check
  pmox version upgrade --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runVersionUpgrade(cmd, f) },
	}
	cmd.Flags().BoolVar(&f.check, "check", false, "only report whether a newer release exists")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "upgrade without asking")
	cmd.Flags().StringVar(&f.version, "version", "", "install this release (vX.Y.Z) instead of the latest; plain binary only")
	return cmd
}

// --- versions ----------------------------------------------------------------

type semver [3]int

// parseVersion reads "1.2.3" or "v1.2.3". ok is false for anything else
// (dev builds, git describe strings).
func parseVersion(s string) (v semver, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func (a semver) less(b semver) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func (a semver) String() string { return fmt.Sprintf("%d.%d.%d", a[0], a[1], a[2]) }

// --- install method -----------------------------------------------------------

type installMethod int

const (
	installBinary installMethod = iota
	installBrew
)

type installInfo struct {
	method installMethod
	path   string // the resolved executable
	brew   string // brew binary (installBrew)
}

func (i installInfo) label() string {
	if i.method == installBrew {
		return "Homebrew"
	}
	return "download to " + i.path
}

// detectInstall works out how this pmox was installed from its resolved
// path: inside a Homebrew Cellar, or a plain binary anywhere else.
func detectInstall() (installInfo, error) {
	exe, err := executablePathFn()
	if err != nil {
		return installInfo{}, fmt.Errorf("find the pmox executable: %w", err)
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	info := installInfo{method: installBinary, path: exe}
	slashed := filepath.ToSlash(exe)
	if i := strings.Index(slashed, "/Cellar/pmox/"); i >= 0 {
		info.method = installBrew
		prefix := filepath.FromSlash(slashed[:i])
		info.brew = filepath.Join(prefix, "bin", "brew")
		if _, err := os.Stat(info.brew); err != nil {
			if p, err := exec.LookPath("brew"); err == nil {
				info.brew = p
			} else {
				info.brew = "brew"
			}
		}
	}
	return info, nil
}

// --- GitHub release -------------------------------------------------------------

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type release struct {
	Tag    string         `json:"tag_name"`
	Assets []releaseAsset `json:"assets"`
}

func (r *release) asset(name string) (releaseAsset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return releaseAsset{}, false
}

// networkError carries exit code 5 for failed GitHub requests.
type networkError struct{ err error }

func (e *networkError) Error() string { return e.err.Error() }
func (e *networkError) Unwrap() error { return e.err }
func (e *networkError) ExitCode() int { return exitcode.ExitNetworkError }

var upgradeHTTP = &http.Client{Timeout: 15 * time.Second}

func githubGet(ctx context.Context, url string, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" && strings.HasPrefix(url, githubAPIBase) {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := upgradeHTTP.Do(req)
	if err != nil {
		return nil, &networkError{err: err}
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		msg := fmt.Sprintf("GET %s: %s", url, resp.Status)
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			msg += " (GitHub rate limit? set GITHUB_TOKEN)"
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s", exitcode.ErrNotFound, msg)
		}
		return nil, &networkError{err: errors.New(msg)}
	}
	return resp, nil
}

// fetchRelease returns the latest release, or the one tagged tag.
func fetchRelease(ctx context.Context, tag string) (*release, error) {
	url := githubAPIBase + "/repos/" + upgradeRepo + "/releases/latest"
	if tag != "" {
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		url = githubAPIBase + "/repos/" + upgradeRepo + "/releases/tags/" + tag
	}
	resp, err := githubGet(ctx, url, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("read release: %w", err)
	}
	return &r, nil
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := githubGet(ctx, url, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, &networkError{err: fmt.Errorf("download %s: %w", url, err)}
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("download %s: larger than %d bytes", url, limit)
	}
	return b, nil
}

// verifyChecksum checks data's sha256 against name's line in a
// goreleaser checksums.txt ("<hex>  <name>").
func verifyChecksum(checksums []byte, name string, data []byte) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	sc := bufio.NewScanner(bytes.NewReader(checksums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			if !strings.EqualFold(fields[0], got) {
				return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, fields[0], got)
			}
			return nil
		}
	}
	return fmt.Errorf("%s is not listed in checksums.txt", name)
}

// extractBinary returns the "pmox" entry of a .tar.gz archive.
func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("the archive has no pmox binary")
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "pmox" {
			b, err := io.ReadAll(io.LimitReader(tr, maxBinarySize+1))
			if err != nil {
				return nil, fmt.Errorf("read archive: %w", err)
			}
			if len(b) > maxBinarySize {
				return nil, errors.New("the pmox binary in the archive is too large")
			}
			return b, nil
		}
	}
}

// --- replacing the binary -------------------------------------------------------

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".pmox-write-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// checkNewBinary runs path --version and requires it to report want.
func checkNewBinary(path string, want semver) error {
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the downloaded pmox doesn't run: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), " "+want.String()) {
		return fmt.Errorf("the downloaded pmox reports %q, want %s", strings.TrimSpace(string(out)), want)
	}
	return nil
}

// replaceBinary puts bin at target: an atomic rename when target's
// directory is writable, else sudo install (sudo asks for the password
// on the terminal; -n without one).
func replaceBinary(cmd *cobra.Command, target string, bin []byte, want semver) error {
	dir := filepath.Dir(target)
	if dirWritableFn(dir) {
		tmp := filepath.Join(dir, fmt.Sprintf(".pmox-upgrade-%d", os.Getpid()))
		if err := os.WriteFile(tmp, bin, 0o755); err != nil {
			return fmt.Errorf("write %s: %w", tmp, err)
		}
		defer func() { _ = os.Remove(tmp) }()
		if err := os.Chmod(tmp, 0o755); err != nil {
			return err
		}
		if err := checkNewBinary(tmp, want); err != nil {
			return err
		}
		if err := os.Rename(tmp, target); err != nil {
			return fmt.Errorf("replace %s: %w", target, err)
		}
		return nil
	}

	f, err := os.CreateTemp("", "pmox-upgrade-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(bin); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if err := checkNewBinary(tmp, want); err != nil {
		return err
	}
	args := []string{"install", "-m", "0755", tmp, target}
	if !tui.Interactive() {
		args = append([]string{"-n"}, args...)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "Installing to %s needs sudo.\n", target)
	}
	if err := runAttachedFn("sudo", args...); err != nil {
		return fmt.Errorf("sudo install to %s failed: %w — run 'pmox version upgrade' on a terminal so sudo can ask for your password, or install the release by hand", target, err)
	}
	return nil
}

// --- command --------------------------------------------------------------------

func runVersionUpgrade(cmd *cobra.Command, f *upgradeFlags) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	info, err := detectInstall()
	if err != nil {
		return err
	}
	if info.method == installBrew && f.version != "" {
		return fmt.Errorf("%w: --version doesn't apply to a Homebrew install; brew installs the formula's version", exitcode.ErrUserInput)
	}

	sp := startSpin("Checking for a newer pmox…")
	rel, err := fetchRelease(ctx, f.version)
	sp.Stop()
	if err != nil {
		return err
	}
	target, ok := parseVersion(rel.Tag)
	if !ok {
		return fmt.Errorf("the release tag %q isn't a version", rel.Tag)
	}
	current, isRelease := parseVersion(version)

	switch {
	case isRelease && f.version == "" && !current.less(target):
		fmt.Fprintf(out, "✓ pmox %s is up to date\n", current)
		return nil
	case isRelease && f.version != "" && current == target:
		fmt.Fprintf(out, "✓ pmox %s is already installed\n", current)
		return nil
	}

	from := version
	if isRelease {
		from = current.String()
	}
	how := info.label()
	if info.method == installBrew {
		how = "Homebrew: brew upgrade " + upgradeFormula
	}
	if f.check {
		if !isRelease {
			fmt.Fprintf(out, "pmox %s is a development build; the latest release is %s (%s)\n", version, target, how)
			return nil
		}
		fmt.Fprintf(out, "pmox %s → %s available (%s)\n", from, target, how)
		return nil
	}

	if !f.yes {
		if !tui.Interactive() {
			return fmt.Errorf("%w: pmox %s → %s is available; pass --yes to upgrade without a terminal", exitcode.ErrUserInput, from, target)
		}
		q := fmt.Sprintf("Upgrade pmox %s → %s via %s?", from, target, info.label())
		if !isRelease {
			q = fmt.Sprintf("pmox %s is a development build. Install release %s via %s?", version, target, info.label())
		}
		yes, err := confirmUpgradeFn(q, true)
		if err != nil {
			return err
		}
		if !yes {
			return fmt.Errorf("%w: upgrade declined", tui.ErrAborted)
		}
	}

	if info.method == installBrew {
		if err := runAttachedFn(info.brew, "upgrade", upgradeFormula); err != nil {
			return fmt.Errorf("brew upgrade %s: %w", upgradeFormula, err)
		}
		return nil
	}

	archiveName := fmt.Sprintf("pmox_%s_%s_%s.tar.gz", target, runtime.GOOS, runtime.GOARCH)
	archiveAsset, ok := rel.asset(archiveName)
	if !ok {
		return fmt.Errorf("release %s has no %s", rel.Tag, archiveName)
	}
	sumsAsset, ok := rel.asset("checksums.txt")
	if !ok {
		return fmt.Errorf("release %s has no checksums.txt", rel.Tag)
	}
	sp = startSpin(fmt.Sprintf("Downloading pmox %s…", target))
	archive, err := download(ctx, archiveAsset.URL, maxBinarySize)
	var sums []byte
	if err == nil {
		sums, err = download(ctx, sumsAsset.URL, 1<<20)
	}
	sp.Stop()
	if err != nil {
		return err
	}
	if err := verifyChecksum(sums, archiveName, archive); err != nil {
		return err
	}
	bin, err := extractBinary(archive)
	if err != nil {
		return err
	}
	if err := replaceBinary(cmd, info.path, bin, target); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ pmox upgraded %s → %s (%s)\n", from, target, info.path)
	return nil
}

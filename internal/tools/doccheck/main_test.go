package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheck_ValidAndBrokenLinks(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "README.md"), `# Title

See [the setup doc](./docs/pve-setup.md) and [examples](./examples/README.md)
and also [broken](./docs/missing.md).
External: [upstream](https://example.com) is skipped.
`)
	write(t, filepath.Join(root, "docs", "pve-setup.md"), "stub\n")
	write(t, filepath.Join(root, "examples", "README.md"), "stub\n")

	files, err := gather(root)
	if err != nil {
		t.Fatal(err)
	}
	misses := check(root, files)
	if len(misses) != 1 {
		t.Fatalf("want 1 miss, got %d: %v", len(misses), misses)
	}
	if !strings.Contains(misses[0], "docs/missing.md") {
		t.Fatalf("miss did not name broken target: %q", misses[0])
	}
	if !strings.Contains(misses[0], "README.md:4") {
		t.Fatalf("miss should include source line number: %q", misses[0])
	}
}

func TestCheck_AllClean(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "README.md"),
		"See [examples](./examples/README.md) and [setup](./docs/pve-setup.md).\n")
	write(t, filepath.Join(root, "docs", "pve-setup.md"), "stub\n")
	write(t, filepath.Join(root, "examples", "README.md"),
		"One: [cloud-init](./cloud-init.yaml)\n")
	write(t, filepath.Join(root, "examples", "cloud-init.yaml"), "data\n")

	files, err := gather(root)
	if err != nil {
		t.Fatal(err)
	}
	misses := check(root, files)
	if len(misses) != 0 {
		t.Fatalf("want 0 misses, got %v", misses)
	}
}

func TestCheck_FragmentAndAnchor(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "README.md"),
		"[setup](./docs/pve-setup.md#token) and [anchor only](#commands)\n")
	write(t, filepath.Join(root, "docs", "pve-setup.md"), "stub\n")

	misses := check(root, mustGather(t, root))
	if len(misses) != 0 {
		t.Fatalf("fragments should resolve to file; got %v", misses)
	}
}

func TestCheck_SkipsHttpAndMailto(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "README.md"),
		"[a](https://example.com) [b](http://x.y) [c](mailto:x@y)\n")
	misses := check(root, mustGather(t, root))
	if len(misses) != 0 {
		t.Fatalf("external links must be skipped: %v", misses)
	}
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustGather(t *testing.T, root string) []string {
	t.Helper()
	files, err := gather(root)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

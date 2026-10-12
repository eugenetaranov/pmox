package bootstrap

import (
	"io/fs"
	"os/exec"
	"path"
	"strings"
	"testing"
)

func specNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".spec") {
			names[strings.TrimSuffix(e.Name(), ".spec")] = true
		}
	}
	return names
}

// pmox's own MCP specs sit next to the synced tack-roles ones; a name in
// both would make one silently replace the other on the VM.
func TestMCPSpecNamesDistinct(t *testing.T) {
	tack, own := specNames(t, "assets/mcp-catalog"), specNames(t, "conf/mcp-catalog")
	for n := range own {
		if tack[n] {
			t.Errorf("MCP spec %q is in both assets/mcp-catalog (tack-roles) and conf/mcp-catalog (pmox)", n)
		}
	}
	for _, want := range []string{"github", "postgres", "playwright", "linear"} {
		if !own[want] {
			t.Errorf("conf/mcp-catalog has no %s.spec", want)
		}
	}
}

// Each pmox spec parses, pins its image, and its skeleton has every
// required key with a comment saying where to get it.
func TestMCPSpecsValid(t *testing.T) {
	bash := bashPath(t)
	for n := range specNames(t, "conf/mcp-catalog") {
		p := path.Join("conf/mcp-catalog", n+".spec")
		out, err := exec.Command(bash, "-c", `set -eu; . "$1"; printf '%s\n%s\n%s\n%s\n---\n' "$KIND" "${IMAGE:-}" "${URL:-}" "$REQUIRED"; skeleton`, "_", p).CombinedOutput()
		if err != nil {
			t.Errorf("%s: %v\n%s", n, err, out)
			continue
		}
		head, skel, _ := strings.Cut(string(out), "---\n")
		f := strings.Split(head, "\n")
		kind, image, url, required := f[0], f[1], f[2], strings.Fields(f[3])
		switch kind {
		case "docker":
			if i := strings.LastIndex(image, ":"); i < 0 || image[i+1:] == "latest" {
				t.Errorf("%s: image %q is not pinned to a tag", n, image)
			}
		case "http":
			if !strings.HasPrefix(url, "https://") {
				t.Errorf("%s: URL %q is not https", n, url)
			}
		default:
			t.Errorf("%s: KIND %q", n, kind)
		}
		for _, k := range required {
			if !strings.Contains(skel, "\n"+k+"=\n") && !strings.HasPrefix(skel, k+"=\n") {
				t.Errorf("%s: skeleton has no empty %s= line", n, k)
			}
		}
		if len(required) > 0 && !strings.Contains(skel, "https://") {
			t.Errorf("%s: skeleton doesn't say where to get the credential", n)
		}
	}
}

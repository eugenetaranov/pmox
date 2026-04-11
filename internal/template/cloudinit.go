package template

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed snippet.yaml
var bakeSnippet []byte

// bakeSnippetFilename is the fixed filename used for the uploaded
// snippet on every build. Using a stable name means a second build
// overwrites the first, so drift across invocations is impossible.
const bakeSnippetFilename = "pmox-qga-bake.yaml"

// templateName builds a deterministic template name from the release
// codename and allocated vmid, e.g. "ubuntu-2404-pmox-9000".
func templateName(img ImageEntry, vmid int) string {
	// Release is expected to be the version string like "24.04"; fall
	// back to codename if that field is empty.
	release := strings.ReplaceAll(img.Release, ".", "")
	if release == "" {
		release = img.Codename
	}
	release = strings.ToLower(release)
	return fmt.Sprintf("ubuntu-%s-pmox-%d", release, vmid)
}

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
// codename and allocated vmid, e.g. "ubuntu-2404-pmox-9000". PVE
// requires `name` to look like a valid DNS name, so the release token
// is run through dnsSlug — Ubuntu's simplestreams metadata isn't
// always the clean "24.04" this assumes: an LTS release_title can read
// "24.04 LTS", and a not-yet-numbered devel release's title can be a
// multi-word name like "Resolute Raccoon". Either would otherwise
// produce a name PVE's create-VM call rejects with "does not look like
// a valid DNS name".
func templateName(img ImageEntry, vmid int) string {
	// Release is expected to be the version string like "24.04"; fall
	// back to codename if that field is empty.
	release := strings.ReplaceAll(img.Release, ".", "")
	if release == "" {
		release = img.Codename
	}
	release = dnsSlug(release)
	if release == "" {
		release = "ubuntu"
	}
	return fmt.Sprintf("ubuntu-%s-pmox-%d", release, vmid)
}

// dnsSlug lowercases s and collapses every run of characters outside
// [a-z0-9] into a single hyphen, trimming a leading or trailing one —
// turning arbitrary text (spaces, parentheses, punctuation) into a
// single DNS-label-safe token.
func dnsSlug(s string) string {
	var b strings.Builder
	prevDash := true // suppress a leading hyphen
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

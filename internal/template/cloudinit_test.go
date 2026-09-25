package template

import (
	"strings"
	"testing"
)

func TestBakeSnippetContent(t *testing.T) {
	body := string(bakeSnippet)
	for _, want := range []string{
		"qemu-guest-agent",
		"cloud-init clean",
		"truncate -s 0 /etc/machine-id",
		"rm -f /etc/netplan/50-cloud-init.yaml",
		"poweroff",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("snippet missing %q", want)
		}
	}
}

func TestTemplateName(t *testing.T) {
	cases := []struct {
		name string
		img  ImageEntry
		vmid int
		want string
	}{
		{"clean release", ImageEntry{Release: "24.04", Codename: "noble"}, 9000, "ubuntu-2404-pmox-9000"},
		{"clean release", ImageEntry{Release: "22.04", Codename: "jammy"}, 9050, "ubuntu-2204-pmox-9050"},
		{"clean release", ImageEntry{Release: "20.04", Codename: "focal"}, 9099, "ubuntu-2004-pmox-9099"},
		// Regression: `pmox create-template` failed with PVE's "name:
		// invalid format - value does not look like a valid DNS name"
		// whenever the release string wasn't a clean "24.04". An LTS
		// release_title with a suffix, e.g. "24.04 LTS", used to leave
		// a literal space in the assembled name.
		{"LTS suffix has a space", ImageEntry{Release: "24.04 LTS", Codename: "noble"}, 9000, "ubuntu-2404-lts-pmox-9000"},
		// A not-yet-numbered devel release can carry a multi-word title
		// (e.g. "Resolute Raccoon") instead of a version number, with
		// no dots for the old code to strip.
		{"devel title, no version yet", ImageEntry{Release: "Resolute Raccoon", Codename: "resolute"}, 9000, "ubuntu-resolute-raccoon-pmox-9000"},
		{"empty release falls back to codename", ImageEntry{Release: "", Codename: "resolute"}, 9000, "ubuntu-resolute-pmox-9000"},
		{"codename needs sanitizing too", ImageEntry{Release: "", Codename: "Resolute Raccoon"}, 9000, "ubuntu-resolute-raccoon-pmox-9000"},
		{"punctuation is dropped, not just spaces", ImageEntry{Release: "26.04 (LTS)", Codename: "noble"}, 9000, "ubuntu-2604-lts-pmox-9000"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.img.Codename, func(t *testing.T) {
			if got := templateName(tc.img, tc.vmid); got != tc.want {
				t.Errorf("templateName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDNSSlug(t *testing.T) {
	cases := map[string]string{
		"2404":                "2404",
		"24.04 LTS":           "24-04-lts", // dots aren't pre-stripped here; templateName does that itself
		"Resolute Raccoon":    "resolute-raccoon",
		"26.04 (LTS)":         "26-04-lts",
		"  leading/trailing ": "leading-trailing",
		"":                    "",
		"---":                 "",
	}
	for in, want := range cases {
		if got := dnsSlug(in); got != want {
			t.Errorf("dnsSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

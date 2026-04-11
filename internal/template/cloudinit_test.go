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
		"poweroff",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("snippet missing %q", want)
		}
	}
}

func TestTemplateName(t *testing.T) {
	cases := []struct {
		img  ImageEntry
		vmid int
		want string
	}{
		{ImageEntry{Release: "24.04", Codename: "noble"}, 9000, "ubuntu-2404-pmox-9000"},
		{ImageEntry{Release: "22.04", Codename: "jammy"}, 9050, "ubuntu-2204-pmox-9050"},
		{ImageEntry{Release: "20.04", Codename: "focal"}, 9099, "ubuntu-2004-pmox-9099"},
	}
	for _, tc := range cases {
		t.Run(tc.img.Codename, func(t *testing.T) {
			if got := templateName(tc.img, tc.vmid); got != tc.want {
				t.Errorf("templateName = %q, want %q", got, tc.want)
			}
		})
	}
}

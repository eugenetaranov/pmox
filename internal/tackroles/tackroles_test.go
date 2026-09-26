package tackroles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// realReadmeExcerpt is a trimmed copy of tackhq/tack-roles' actual
// README.md (fetched 2026-09-26) — enough surrounding context (the
// intro paragraph before the table, and the "## Using a role" section
// after it) to prove parseTable finds exactly the table and stops at
// its natural boundaries either side.
const realReadmeExcerpt = `# tack-roles

A community collection of reusable [Tack](https://github.com/tackhq/tack) roles.

Tack is a single-binary, dependency-free configuration management and
system-bootstrapping tool inspired by Ansible.

## Available roles

| Role | Description |
| --- | --- |
| [` + "`ai-tools`" + `](roles/ai-tools) | Installs Claude Code + omp coding agents and an MCPJungle MCP gateway with servers. |
| [` + "`devbox`" + `](roles/devbox) | Sets up a developer environment: CLI tools, editors, multiplexers, and opt-in zsh/vim config. |
| [` + "`docker`" + `](roles/docker) | Installs Docker Engine on Ubuntu from Docker's official apt repository. |
| [` + "`tailscale`" + `](roles/tailscale) | Installs Tailscale from its official apt repo; optionally joins a tailnet. |
| [` + "`terraform`" + `](roles/terraform) | Installs Terraform via tfenv for easy version switching. |
| [` + "`wireguard`" + `](roles/wireguard) | Installs WireGuard and its tooling. |
| [` + "`openvpn-client`" + `](roles/openvpn-client) | Installs the OpenVPN client package. |

## Using a role

Tack resolves a plain role name against the ` + "`roles/`" + ` directory next to your
playbook, but it can also fetch a role straight from a git or HTTPS URL.
`

func TestParseTable_RealReadmeShape(t *testing.T) {
	got := parseTable(realReadmeExcerpt)
	want := []Role{
		{Name: "ai-tools", Description: "Installs Claude Code + omp coding agents and an MCPJungle MCP gateway with servers."},
		{Name: "devbox", Description: "Sets up a developer environment: CLI tools, editors, multiplexers, and opt-in zsh/vim config."},
		{Name: "docker", Description: "Installs Docker Engine on Ubuntu from Docker's official apt repository."},
		{Name: "tailscale", Description: "Installs Tailscale from its official apt repo; optionally joins a tailnet."},
		{Name: "terraform", Description: "Installs Terraform via tfenv for easy version switching."},
		{Name: "wireguard", Description: "Installs WireGuard and its tooling."},
		{Name: "openvpn-client", Description: "Installs the OpenVPN client package."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTable = %+v,\nwant %+v", got, want)
	}
}

func TestParseTable_NoSection(t *testing.T) {
	if got := parseTable("# tack-roles\n\nNo such section here.\n"); got != nil {
		t.Errorf("parseTable = %+v, want nil", got)
	}
}

func TestParseTable_EmptyTable(t *testing.T) {
	md := "## Available roles\n\n| Role | Description |\n| --- | --- |\n\n## Next section\n"
	if got := parseTable(md); got != nil {
		t.Errorf("parseTable = %+v, want nil for a table with no data rows", got)
	}
}

func TestParseTable_MalformedRowSkipped(t *testing.T) {
	md := "## Available roles\n\n| Role | Description |\n| --- | --- |\n| no backticks here | some description |\n| [`docker`](roles/docker) | Installs Docker. |\n"
	got := parseTable(md)
	want := []Role{{Name: "docker", Description: "Installs Docker."}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseTable = %+v, want %+v (malformed row skipped)", got, want)
	}
}

func TestFetch_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(realReadmeExcerpt))
	}))
	defer srv.Close()

	roles, err := Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(roles) != 7 {
		t.Fatalf("got %d roles, want 7: %+v", len(roles), roles)
	}
	if roles[2].Name != "docker" {
		t.Errorf("roles[2].Name = %q, want docker", roles[2].Name)
	}
}

func TestFetch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want it to mention the 404", err)
	}
}

func TestFetch_NoRolesFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# tack-roles\n\nno table here\n"))
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "no roles found") {
		t.Fatalf("err = %v, want a no-roles-found error", err)
	}
}

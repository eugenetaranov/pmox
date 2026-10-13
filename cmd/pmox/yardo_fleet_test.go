package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
)

var fleetVMs = []pveclient.Resource{
	{VMID: 101, Name: "web1", Node: "pve", Status: "running", Tags: "pmox;team"},
	{VMID: 102, Name: "web2", Node: "pve", Status: "running", Tags: "pmox;team"},
	{VMID: 103, Name: "web3", Node: "pve", Status: "stopped", Tags: "pmox;team"},
	{VMID: 104, Name: "db", Node: "pve", Status: "running", Tags: "pmox"},
	{VMID: 105, Name: "other", Node: "pve", Status: "running", Tags: "team"},
	{VMID: 900, Name: "tmpl", Node: "pve", Status: "stopped", Tags: "pmox;team", Template: 1},
}

func names(rs []pveclient.Resource) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return strings.Join(out, " ")
}

func TestSelectFleet(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		all       bool
		tag, want string
		err       string
	}{
		{tag: "team", want: "web1 web2"},
		{all: true, want: "web1 web2 db"},
		{args: []string{"db", "103"}, want: "db web3"},
		{args: []string{"other"}, err: `no pmox VM "other"`},
		{tag: "nope", err: `no running pmox VM has the tag "nope"`},
		{err: "name the VMs"},
		{args: []string{"db"}, all: true, err: "not both"},
	} {
		got, err := selectFleet(fleetVMs, tc.args, tc.all, tc.tag)
		switch {
		case tc.err != "":
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%+v: err = %v, want %q", tc, err, tc.err)
			}
		case err != nil || names(got) != tc.want:
			t.Errorf("%+v: %s, %v; want %s", tc, names(got), err, tc.want)
		}
	}
}

func TestFleetCommand(t *testing.T) {
	cmd, stdin, err := fleetCommand(nil, false)
	if err != nil || cmd != "sudo -n yardo --yes" || stdin != nil {
		t.Errorf("no profiles: %q %v", cmd, err)
	}
	cmd, stdin, err = fleetCommand([][]byte{[]byte("picks = [\"cli\"]\n"), []byte("user = \"ada\"\n")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "yardo --profile /var/lib/yardo/pmox-profiles/1.toml --profile /var/lib/yardo/pmox-profiles/2.toml --verify'") {
		t.Errorf("cmd = %s", cmd)
	}
	tr := tar.NewReader(bytes.NewReader(stdin))
	var got []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		got = append(got, h.Name+"="+string(b))
	}
	if strings.Join(got, "|") != "1.toml=picks = [\"cli\"]\n|2.toml=user = \"ada\"\n" {
		t.Errorf("tar = %q", got)
	}
}

func TestFleetOne(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	key := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldIP, oldPush, oldRun := fleetIPFn, yardoPushFn, yardoFleetRunFn
	t.Cleanup(func() { fleetIPFn, yardoPushFn, yardoFleetRunFn = oldIP, oldPush, oldRun })
	fleetIPFn = func(_ context.Context, _ *pveclient.Client, r pveclient.Resource) (string, error) {
		if r.Name == "web3" {
			return "", errors.New("not running")
		}
		return "10.0.0.1", nil
	}
	yardoPushFn = func(_ context.Context, target *sshTarget) error {
		if target.Name == "web2" {
			return errors.New("ssh: connect to host 10.0.0.1 port 22: Connection refused")
		}
		return nil
	}
	yardoFleetRunFn = func(context.Context, *sshTarget, string, []byte) (string, error) {
		return "ok         zsh\nmissing    starship: not in mise ls\nmissing    Docker: docker not found\n", errors.New("exit status 1")
	}
	f := &fleetFlags{}
	f.ssh.user, f.ssh.identity = "ubuntu", key
	res := &server.Resolved{URL: "https://pve:8006/api2/json", Server: &config.Server{}}
	for name, want := range map[string]string{"web1": "2 missing: starship, Docker", "web2": "push: ssh: connect", "web3": "not running"} {
		line, ok := fleetOne(context.Background(), nil, pveclient.Resource{Name: name, VMID: 1}, f, res, "cmd", nil, true)
		if ok || !strings.HasPrefix(line, want) {
			t.Errorf("%s: %q (ok %v), want %q", name, line, ok, want)
		}
	}
	yardoFleetRunFn = func(context.Context, *sshTarget, string, []byte) (string, error) { return "ok         zsh\n", nil }
	if line, ok := fleetOne(context.Background(), nil, pveclient.Resource{Name: "web1"}, f, res, "cmd", nil, false); !ok || line != "applied" {
		t.Errorf("apply: %q %v", line, ok)
	}
}

func TestYardoTeamFiles(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if files, err := yardoTeamFiles(); err != nil || len(files) != 0 {
		t.Fatalf("no team dir: %v %v", files, err)
	}
	dir := filepath.Join(cfg, "pmox", "yardo")
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("catalog.d/acme.toml", "[[item]]\nid = \"acmecli\"\ntab = \"dev\"\nlabel = \"Acme CLI\"\n[[item.step]]\ntype = \"apt\"\npackages = [\"jq\"]\n")
	write("mcp-catalog/acme-docs.spec", "NAME=acme-docs\nKIND=http\nURL=https://docs.acme/mcp\n")
	write("mcp-catalog/notes.txt", "ignored")
	files, err := yardoTeamFiles()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Path)
	}
	if want := "catalog.d/acme.toml mcp-catalog/acme-docs.spec"; strings.Join(got, " ") != want {
		t.Errorf("files = %v", got)
	}

	write("catalog.d/broken.toml", "[[item]\nid = \"x\"\n")
	if _, err := yardoTeamFiles(); err == nil || !strings.Contains(err.Error(), "catalog.d") {
		t.Errorf("broken item: err = %v", err)
	}
}

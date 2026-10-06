package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/template"
)

func launchTemplateEnv(t *testing.T, resources string) (*cobra.Command, *pveclient.Client, *server.Resolved, *bytes.Buffer) {
	t.Helper()
	isolate(t)
	cfg := &config.Config{Servers: map[string]*config.Server{formURL: {TokenID: "root@pam!pmox", Node: "p0"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(resources)) }))
	t.Cleanup(srv.Close)
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	return cmd, pveclient.New(srv.URL, "t@pam!x", "s", false), &server.Resolved{URL: formURL, Server: cfg.Servers[formURL]}, &stderr
}

func TestEnsureLaunchTemplatePicksExistingAndSavesDefault(t *testing.T) {
	cmd, client, resolved, stderr := launchTemplateEnv(t, `{"data":[
		{"vmid":9000,"name":"ubuntu-2604-lts-pmox-9000","node":"p0","template":1},
		{"vmid":101,"name":"web1","node":"p0","template":0}]}`)
	var offered []string
	orig := pickLaunchTemplateFn
	pickLaunchTemplateFn = func(_ string, opts []huh.Option[string]) (string, error) {
		for _, o := range opts {
			offered = append(offered, o.Key)
		}
		return "9000", nil
	}
	t.Cleanup(func() { pickLaunchTemplateFn = orig })

	if err := ensureLaunchTemplate(cmd, client, resolved); err != nil {
		t.Fatal(err)
	}
	if len(offered) != 2 || !strings.Contains(offered[0], "9000") || !strings.Contains(offered[1], "Build a new") {
		t.Errorf("offered %v", offered)
	}
	if resolved.Server.Template != "9000" || mustLoad(t).Servers[formURL].Template != "9000" {
		t.Errorf("template not set/saved: %q", resolved.Server.Template)
	}
	if !strings.Contains(stderr.String(), "saved as the default") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestEnsureLaunchTemplateBuildsWhenNoneExist(t *testing.T) {
	cmd, client, resolved, stderr := launchTemplateEnv(t, `{"data":[{"vmid":101,"name":"web1","node":"p0","template":0}]}`)
	defer stubTemplateRunFn(t, &template.Result{VMID: 9001, Name: "ubuntu-2604-lts-pmox-9001"}, nil)()
	if err := ensureLaunchTemplate(cmd, client, resolved); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "building one now") {
		t.Errorf("should announce the build: %q", stderr.String())
	}
	if mustLoad(t).Servers[formURL].Template != "9001" {
		t.Errorf("built template not saved as default")
	}
}

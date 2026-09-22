package main

import (
	"context"
	"os"
	"testing"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/doctor"
	"github.com/eugenetaranov/pmox/internal/pvetest"
	"github.com/eugenetaranov/pmox/internal/server"
)

const allPrivsJSON = `{"data":{"/":{
	"Sys.Audit":1,"VM.Audit":1,"VM.Allocate":1,"VM.Clone":1,
	"VM.Config.Disk":1,"VM.Config.CPU":1,"VM.Config.Memory":1,
	"VM.Config.Network":1,"VM.Config.Cloudinit":1,"VM.PowerMgmt":1,
	"Datastore.Audit":1,"Datastore.AllocateSpace":1,"SDN.Use":1
}}}`

// healthyDoctorServer registers responders for a fully-ready cluster.
// Individual tests re-register a route to inject a specific failure
// (last matching handler does NOT win — first does — so tests that need
// to override must build their own server).
func healthyDoctorServer(t *testing.T) *pvetest.Server {
	s := pvetest.New(t)
	s.Handle("GET", "/version", pvetest.JSON(`{"data":{"version":"8.2.4"}}`))
	s.Handle("GET", "/access/permissions", pvetest.JSON(allPrivsJSON))
	s.Handle("GET", "/cluster/resources", pvetest.JSON(`{"data":[{"node":"pve1","status":"online"}]}`))
	s.Handle("GET", "/network", pvetest.JSON(`{"data":[{"iface":"vmbr0","type":"bridge"}]}`))
	s.Handle("GET", "/storage", pvetest.JSON(`{"data":[{"storage":"local","type":"dir","content":"images,snippets","active":1,"enabled":1}]}`))
	s.Handle("GET", "/qemu/9000/config", pvetest.JSON(`{"data":{"template":"1","agent":"1"}}`))
	return s
}

func doctorResolved(url string) *server.Resolved {
	return &server.Resolved{
		URL:            url,
		Server:         &config.Server{TokenID: "t@pam!x", Node: "pve1", Template: "9000", Storage: "local", SnippetStorage: "local", Bridge: "vmbr0"},
		Secret:         "secret",
		Source:         "test",
		NodeSSHUser:    "root",
		NodeSSHAuth:    "key",
		NodeSSHKeyPath: "/k",
	}
}

func healthyDeps() doctorDeps {
	return doctorDeps{
		lookPath:          func(string) (string, error) { return "/usr/bin/x", nil },
		knownHostHasEntry: func(string) (bool, error) { return true, nil },
		sshDial:           func(context.Context) error { return nil },
	}
}

func runChecks(t *testing.T, s *pvetest.Server, resolved *server.Resolved, deps doctorDeps, strict bool) doctor.Report {
	t.Helper()
	cl := &doctor.Checklist{}
	executeDoctor(context.Background(), cl, s.Client(), resolved, deps, strict)
	return cl.Finalize(resolved.URL, resolved.Source, strict)
}

func findCheck(r doctor.Report, id string) (doctor.Check, bool) {
	for _, c := range r.Checks {
		if c.ID == id {
			return c, true
		}
	}
	return doctor.Check{}, false
}

func TestDoctor_HealthyClusterIsReady(t *testing.T) {
	s := healthyDoctorServer(t)
	r := runChecks(t, s, doctorResolved(s.URL()), healthyDeps(), false)
	if !r.Ready {
		t.Fatalf("expected ready; fails=%d, checks=%+v", r.Summary.Fail, r.Checks)
	}
	if r.Summary.Fail != 0 {
		t.Errorf("unexpected failures: %d", r.Summary.Fail)
	}
	for _, id := range []string{"api.reachable", "api.auth", "api.privileges", "api.node", "storage.disk", "storage.snippets", "template.resolves", "template.agent", "ssh.dial"} {
		if c, ok := findCheck(r, id); !ok || c.Status == doctor.Fail {
			t.Errorf("check %s missing or failed: %+v", id, c)
		}
	}
}

func TestDoctor_MissingPrivilegeFails(t *testing.T) {
	s := pvetest.New(t)
	s.Handle("GET", "/version", pvetest.JSON(`{"data":{"version":"8.2.4"}}`))
	// Grant everything except VM.Clone.
	s.Handle("GET", "/access/permissions", pvetest.JSON(`{"data":{"/":{
		"Sys.Audit":1,"VM.Audit":1,"VM.Allocate":1,
		"VM.Config.Disk":1,"VM.Config.CPU":1,"VM.Config.Memory":1,
		"VM.Config.Network":1,"VM.Config.Cloudinit":1,"VM.PowerMgmt":1,
		"Datastore.Audit":1,"Datastore.AllocateSpace":1,"SDN.Use":1}}}`))
	s.Handle("GET", "/cluster/resources", pvetest.JSON(`{"data":[{"node":"pve1","status":"online"}]}`))
	s.Handle("GET", "/network", pvetest.JSON(`{"data":[{"iface":"vmbr0","type":"bridge"}]}`))
	s.Handle("GET", "/storage", pvetest.JSON(`{"data":[{"storage":"local","content":"images,snippets","enabled":1}]}`))
	s.Handle("GET", "/qemu/9000/config", pvetest.JSON(`{"data":{"template":"1","agent":"1"}}`))

	r := runChecks(t, s, doctorResolved(s.URL()), healthyDeps(), false)
	c, ok := findCheck(r, "api.privileges")
	if !ok || c.Status != doctor.Fail {
		t.Fatalf("api.privileges should fail; got %+v", c)
	}
	if r.Ready {
		t.Error("cluster with a missing privilege must not be ready")
	}
}

func TestDoctor_OfflineNodeFails(t *testing.T) {
	s := pvetest.New(t)
	s.Handle("GET", "/version", pvetest.JSON(`{"data":{"version":"8.2.4"}}`))
	s.Handle("GET", "/access/permissions", pvetest.JSON(allPrivsJSON))
	s.Handle("GET", "/cluster/resources", pvetest.JSON(`{"data":[{"node":"pve1","status":"offline"}]}`))

	r := runChecks(t, s, doctorResolved(s.URL()), healthyDeps(), false)
	c, ok := findCheck(r, "api.node")
	if !ok || c.Status != doctor.Fail {
		t.Fatalf("api.node should fail for an offline node; got %+v", c)
	}
	// Storage/template checks depend on a healthy node — they must be skipped.
	if _, ok := findCheck(r, "storage.disk"); ok {
		t.Error("storage checks should be skipped when the node is offline")
	}
}

func TestDoctor_TemplateNotAtemplateWarns(t *testing.T) {
	// A fresh server whose vmid 9000 is a plain VM, not a template.
	s2 := pvetest.New(t)
	s2.Handle("GET", "/version", pvetest.JSON(`{"data":{"version":"8.2.4"}}`))
	s2.Handle("GET", "/access/permissions", pvetest.JSON(allPrivsJSON))
	s2.Handle("GET", "/cluster/resources", pvetest.JSON(`{"data":[{"node":"pve1","status":"online"}]}`))
	s2.Handle("GET", "/network", pvetest.JSON(`{"data":[{"iface":"vmbr0","type":"bridge"}]}`))
	s2.Handle("GET", "/storage", pvetest.JSON(`{"data":[{"storage":"local","content":"images,snippets","enabled":1}]}`))
	s2.Handle("GET", "/qemu/9000/config", pvetest.JSON(`{"data":{"template":"0","agent":"1"}}`))

	r := runChecks(t, s2, doctorResolved(s2.URL()), healthyDeps(), false)
	c, ok := findCheck(r, "template.resolves")
	if !ok || c.Status != doctor.Warn {
		t.Fatalf("template.resolves should warn when vmid isn't a template; got %+v", c)
	}
	if !r.Ready {
		t.Error("a warning alone should not block readiness")
	}
}

func TestDoctor_UnpinnedHostKeyFails(t *testing.T) {
	s := healthyDoctorServer(t)
	deps := healthyDeps()
	deps.knownHostHasEntry = func(string) (bool, error) { return false, nil }
	dialCalled := false
	deps.sshDial = func(context.Context) error { dialCalled = true; return nil }

	r := runChecks(t, s, doctorResolved(s.URL()), deps, false)
	c, ok := findCheck(r, "ssh.known_host")
	if !ok || c.Status != doctor.Fail {
		t.Fatalf("ssh.known_host should fail when unpinned; got %+v", c)
	}
	if dialCalled {
		t.Error("doctor must not dial (which could prompt) before the host key is pinned")
	}
}

func TestDoctor_MissingToolWarnsButStaysReady(t *testing.T) {
	s := healthyDoctorServer(t)
	deps := healthyDeps()
	deps.lookPath = func(bin string) (string, error) {
		if bin == "rsync" {
			return "", context.DeadlineExceeded // any error = not found
		}
		return "/usr/bin/" + bin, nil
	}
	r := runChecks(t, s, doctorResolved(s.URL()), deps, false)
	c, ok := findCheck(r, "tooling.rsync")
	if !ok || c.Status != doctor.Warn {
		t.Fatalf("tooling.rsync should warn when missing; got %+v", c)
	}
	if !r.Ready {
		t.Error("a missing optional tool must not block launch readiness")
	}
}

func TestDoctorCloudInitKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	url := "https://pve.lan:8006/api2/json"
	ciPath, err := config.CloudInitPath(url)
	if err != nil {
		t.Fatal(err)
	}
	// Cloud-init authorizes keyA.
	keyA := "ssh-ed25519 AAAAC3keyAbodyaaa me@host"
	if err := config.WriteCloudInit(ciPath, "ubuntu", keyA); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pubMatch := dir + "/match.pub"
	pubDiff := dir + "/diff.pub"
	if err := os.WriteFile(pubMatch, []byte(keyA+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pubDiff, []byte("ssh-rsa AAAAB3differentbody other@host\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("match passes", func(t *testing.T) {
		cl := &doctor.Checklist{}
		doctorCloudInitKey(cl, url, pubMatch)
		if cl.StatusOf("config.cloud_init_key") != doctor.Pass {
			t.Errorf("want pass, got %q", cl.StatusOf("config.cloud_init_key"))
		}
	})
	t.Run("mismatch warns", func(t *testing.T) {
		cl := &doctor.Checklist{}
		doctorCloudInitKey(cl, url, pubDiff)
		if cl.StatusOf("config.cloud_init_key") != doctor.Warn {
			t.Errorf("want warn, got %q", cl.StatusOf("config.cloud_init_key"))
		}
	})
}

func TestDoctorSecretBackend(t *testing.T) {
	t.Run("file mode warns (plaintext on disk)", func(t *testing.T) {
		t.Setenv("PMOX_SECRET_STORE", "file")
		cl := &doctor.Checklist{}
		doctorSecretBackend(cl)
		if cl.StatusOf("config.secret_store") != doctor.Warn {
			t.Errorf("want warn for file backend, got %q", cl.StatusOf("config.secret_store"))
		}
	})
	t.Run("keychain mode passes", func(t *testing.T) {
		t.Setenv("PMOX_SECRET_STORE", "keychain")
		cl := &doctor.Checklist{}
		doctorSecretBackend(cl)
		if cl.StatusOf("config.secret_store") != doctor.Pass {
			t.Errorf("want pass for keychain backend, got %q", cl.StatusOf("config.secret_store"))
		}
	})
}

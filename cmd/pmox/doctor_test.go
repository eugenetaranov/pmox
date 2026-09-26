package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/doctor"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pvetest"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/template"
	"github.com/eugenetaranov/pmox/internal/tui"
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

// newTestDoctorCmd returns a cobra.Command plumbed with buffer
// stdout/stderr, for the fixer functions that print to them.
func newTestDoctorCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "doctor"}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetContext(context.Background())
	return cmd, &out, &errb
}

// testConfigFor wraps resolved.Server in a *config.Config under
// resolved.URL — the same pointer, so a fixer's cfg.Save() (via
// srv.Template = ...) is observable by re-reading resolved.Server, and
// cfg.Save() itself succeeds against a sandboxed XDG_CONFIG_HOME.
func testConfigFor(t *testing.T, resolved *server.Resolved) *config.Config {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return &config.Config{Servers: map[string]*config.Server{resolved.URL: resolved.Server}}
}

func runChecks(t *testing.T, s *pvetest.Server, resolved *server.Resolved, deps doctorDeps, strict bool) doctor.Report {
	t.Helper()
	cl := &doctor.Checklist{}
	cmd, _, _ := newTestDoctorCmd()
	executeDoctor(context.Background(), cl, s.Client(), resolved, deps, strict, cmd, testConfigFor(t, resolved))
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

func TestDoctorTack(t *testing.T) {
	t.Run("tack absent warns (optional)", func(t *testing.T) {
		deps := healthyDeps()
		deps.lookPath = func(bin string) (string, error) {
			if bin == "tack" {
				return "", context.DeadlineExceeded
			}
			return "/usr/bin/" + bin, nil
		}
		cl := &doctor.Checklist{}
		doctorTack(cl, deps)
		if cl.StatusOf("tooling.tack") != doctor.Warn {
			t.Errorf("want warn when tack absent, got %q", cl.StatusOf("tooling.tack"))
		}
	})
	t.Run("tack present + playbook passes", func(t *testing.T) {
		cfg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", cfg)
		dir := filepath.Join(cfg, "pmox", "tack")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "playbook.yaml"), []byte("name: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		deps := healthyDeps() // lookPath returns success for everything
		cl := &doctor.Checklist{}
		doctorTack(cl, deps)
		if cl.StatusOf("tooling.tack") != doctor.Pass {
			t.Errorf("want pass when tack + playbook present, got %q", cl.StatusOf("tooling.tack"))
		}
	})
}

func TestDoctorTLSPin_ComparesNormalizedPins(t *testing.T) {
	withStubbedFingerprint(t, "aabbcc", nil)
	resolved := doctorResolved("https://pve.lan:8006/api2/json")
	resolved.Server.Insecure = true
	resolved.Server.TLSPinSHA256 = "sha256:AA:BB:CC"
	cl := &doctor.Checklist{}
	doctorTLSMode(context.Background(), cl, resolved, false)
	if got := cl.StatusOf("config.tls_pin"); got != doctor.Pass {
		t.Errorf("tls_pin = %q, want pass for an equivalent pin spelling", got)
	}

	withStubbedFingerprint(t, "ddeeff", nil)
	cl = &doctor.Checklist{}
	doctorTLSMode(context.Background(), cl, resolved, false)
	if got := cl.StatusOf("config.tls_pin"); got != doctor.Fail {
		t.Errorf("tls_pin = %q, want fail for a different cert", got)
	}
}

func TestDoctorNodeSSH_UnusableBlockFails(t *testing.T) {
	resolved := doctorResolved("https://pve.lan:8006/api2/json")
	resolved.NodeSSHErr = errors.New(`unknown node_ssh.auth "kerberos"`)
	cl := &doctor.Checklist{}
	doctorNodeSSH(context.Background(), cl, resolved, healthyDeps())
	if got := cl.StatusOf("ssh.configured"); got != doctor.Fail {
		t.Errorf("ssh.configured = %q, want fail", got)
	}
}

// --- doctor --fix: template checks attach a fix, and running it works ---

// hitCount counts requests matching method and a path substring.
func hitCount(hits []pvetest.Hit, method, pathContains string) int {
	n := 0
	for _, h := range hits {
		if h.Method == method && strings.Contains(h.Path, pathContains) {
			n++
		}
	}
	return n
}

// runTemplateCheck runs doctorTemplate directly (not the whole
// executeDoctor pass) against a fresh server/config/cmd triple, so
// these tests can inspect and run the attached fix without depending on
// the other, unrelated checks.
func runTemplateCheck(t *testing.T, s *pvetest.Server, resolved *server.Resolved) (doctor.Report, *cobra.Command, *config.Config) {
	t.Helper()
	cl := &doctor.Checklist{}
	cmd, _, _ := newTestDoctorCmd()
	cfg := testConfigFor(t, resolved)
	doctorTemplate(context.Background(), cmd, cl, cfg, s.Client(), resolved)
	return cl.Finalize(resolved.URL, resolved.Source, false), cmd, cfg
}

// TestDoctorTemplate_ConfigReadErrorOffersRebuildFix reproduces the bug
// reported live: GetConfig on a stale template vmid returns a 500 ("...
// does not exist"), which used to warn with NO remediation at all. It
// must now suggest rebuilding, attach a fix, and running that fix must
// rebuild the template and set it as the new default.
func TestDoctorTemplate_ConfigReadErrorOffersRebuildFix(t *testing.T) {
	s := pvetest.New(t)
	s.Handle("GET", "/qemu/9000/config", func(w http.ResponseWriter, _ *http.Request, _ string) {
		http.Error(w, `{"data":null,"message":"Configuration file 'nodes/p0/qemu-server/9000.conf' does not exist"}`, http.StatusInternalServerError)
	})

	resolved := doctorResolved(s.URL())
	report, cmd, cfg := runTemplateCheck(t, s, resolved)

	c, ok := findCheck(report, "template.resolves")
	if !ok || c.Status != doctor.Warn {
		t.Fatalf("template.resolves = %+v, want a warn", c)
	}
	if c.Remediation == "" {
		t.Error("remediation must not be empty (used to be, for this exact error)")
	}
	if !c.Fixable() {
		t.Fatal("expected a fix to be attached")
	}

	restore := stubTemplateRunFn(t, &template.Result{VMID: 9001, Name: "ubuntu-2404-pmox-9001"}, nil)
	defer restore()

	if err := c.RunFix(context.Background()); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	if resolved.Server.Template != "9001" {
		t.Errorf("Server.Template = %q, want 9001", resolved.Server.Template)
	}
	if cfg.Servers[resolved.URL].Template != "9001" {
		t.Errorf("cfg not updated: %q", cfg.Servers[resolved.URL].Template)
	}
	if !strings.Contains(cmd.OutOrStdout().(*bytes.Buffer).String(), "9001") {
		t.Error("expected confirmation output naming the new vmid")
	}
}

// TestDoctorTemplate_NotFoundByNameAlsoOffersRebuildFix covers the
// sibling Fail branch (a named template that ListTemplates can't find)
// — it must attach the same rebuild fix as the config-read-error case.
func TestDoctorTemplate_NotFoundByNameAlsoOffersRebuildFix(t *testing.T) {
	s := pvetest.New(t)
	s.Handle("GET", "/qemu", pvetest.JSON(`{"data":[]}`))

	resolved := doctorResolved(s.URL())
	resolved.Server.Template = "web-base" // a name, not a numeric vmid
	report, _, _ := runTemplateCheck(t, s, resolved)

	c, ok := findCheck(report, "template.resolves")
	if !ok || c.Status != doctor.Fail || !c.Fixable() {
		t.Fatalf("template.resolves = %+v, want a fixable fail", c)
	}
}

// TestDoctorTemplate_NotTemplateFixConverts checks the "exists but isn't
// a template" warn's fix actually issues the qm-template conversion.
func TestDoctorTemplate_NotTemplateFixConverts(t *testing.T) {
	s := pvetest.New(t)
	s.Handle("GET", "/qemu/9000/config", pvetest.JSON(`{"data":{"template":"0","agent":"1"}}`))
	s.Handle("POST", "/qemu/9000/template", pvetest.JSON(`{"data":null}`))

	resolved := doctorResolved(s.URL())
	report, _, _ := runTemplateCheck(t, s, resolved)

	c, ok := findCheck(report, "template.resolves")
	if !ok || c.Status != doctor.Warn || !c.Fixable() {
		t.Fatalf("template.resolves = %+v, want a fixable warn", c)
	}
	if err := c.RunFix(context.Background()); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	if n := hitCount(s.Hits(), "POST", "/qemu/9000/template"); n != 1 {
		t.Errorf("qm-template POST hits = %d, want 1", n)
	}
}

// TestDoctorTemplate_NoAgentFixSetsAgent checks the missing-agent warn's
// fix actually sets agent: 1 via SetConfig.
func TestDoctorTemplate_NoAgentFixSetsAgent(t *testing.T) {
	s := pvetest.New(t)
	s.Handle("GET", "/qemu/9000/config", pvetest.JSON(`{"data":{"template":"1","agent":"0"}}`))
	s.Handle("POST", "/qemu/9000/config", pvetest.JSON(`{"data":null}`))

	resolved := doctorResolved(s.URL())
	report, _, _ := runTemplateCheck(t, s, resolved)

	c, ok := findCheck(report, "template.agent")
	if !ok || c.Status != doctor.Warn || !c.Fixable() {
		t.Fatalf("template.agent = %+v, want a fixable warn", c)
	}
	if err := c.RunFix(context.Background()); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	hits := s.Hits()
	if n := hitCount(hits, "POST", "/qemu/9000/config"); n != 1 {
		t.Errorf("config POST hits = %d, want 1", n)
	}
	for _, h := range hits {
		if h.Method == "POST" && strings.Contains(h.Path, "/qemu/9000/config") && !strings.Contains(h.Body, "agent=1") {
			t.Errorf("POST body = %q, want it to set agent=1", h.Body)
		}
	}
}

// --- doctor --fix: orchestration (offerDoctorFixes, doctorFixConfirmer) ---

// fakeDoctorConfirmer is a scripted tui.Confirmer for these tests.
type fakeDoctorConfirmer struct {
	result bool
	err    error
	calls  int
	prompt string
}

func (f *fakeDoctorConfirmer) Confirm(_ context.Context, prompt string) (bool, error) {
	f.calls++
	f.prompt = prompt
	return f.result, f.err
}

func reportWithOneFix(t *testing.T, ran *bool, fixErr error) doctor.Report {
	t.Helper()
	cl := &doctor.Checklist{}
	cl.Warn("x.y", "x", "broken", "fix it")
	cl.WithFix(doctor.Fix{Prompt: "fix x.y now?", Run: func(context.Context) error {
		if ran != nil {
			*ran = true
		}
		return fixErr
	}})
	return cl.Finalize("s", "src", false)
}

func TestOfferDoctorFixes_NoFixableChecksPrintsNote(t *testing.T) {
	cl := &doctor.Checklist{}
	cl.Pass("a", "config", "ok")
	report := cl.Finalize("s", "src", false)

	cmd, _, errb := newTestDoctorCmd()
	fc := &fakeDoctorConfirmer{}
	if err := offerDoctorFixes(context.Background(), cmd, report, fc); err != nil {
		t.Fatalf("offerDoctorFixes: %v", err)
	}
	if fc.calls != 0 {
		t.Error("confirmer should not be asked when nothing is fixable")
	}
	if !strings.Contains(errb.String(), "no fixable issues") {
		t.Errorf("stderr = %q, want a note about nothing to fix", errb.String())
	}
}

func TestOfferDoctorFixes_DeclinedSkipsWithoutError(t *testing.T) {
	var ran bool
	report := reportWithOneFix(t, &ran, nil)
	cmd, _, errb := newTestDoctorCmd()
	fc := &fakeDoctorConfirmer{result: false}
	if err := offerDoctorFixes(context.Background(), cmd, report, fc); err != nil {
		t.Fatalf("offerDoctorFixes: %v", err)
	}
	if ran {
		t.Error("fix must not run when declined")
	}
	if !strings.Contains(errb.String(), "skipped: x.y") {
		t.Errorf("stderr = %q, want it to note the skip", errb.String())
	}
}

func TestOfferDoctorFixes_ApprovedRunsFix(t *testing.T) {
	var ran bool
	report := reportWithOneFix(t, &ran, nil)
	cmd, _, errb := newTestDoctorCmd()
	fc := &fakeDoctorConfirmer{result: true}
	if err := offerDoctorFixes(context.Background(), cmd, report, fc); err != nil {
		t.Fatalf("offerDoctorFixes: %v", err)
	}
	if !ran {
		t.Error("fix should have run when approved")
	}
	if fc.prompt != "fix x.y now? [y/N]: " {
		t.Errorf("prompt = %q", fc.prompt)
	}
	if !strings.Contains(errb.String(), "fixed: x.y") {
		t.Errorf("stderr = %q, want it to confirm the fix", errb.String())
	}
}

func TestOfferDoctorFixes_FixErrorIsReportedNotFatal(t *testing.T) {
	report := reportWithOneFix(t, nil, errors.New("boom"))
	cmd, _, errb := newTestDoctorCmd()
	fc := &fakeDoctorConfirmer{result: true}
	if err := offerDoctorFixes(context.Background(), cmd, report, fc); err != nil {
		t.Fatalf("offerDoctorFixes should not fail the whole pass: %v", err)
	}
	if !strings.Contains(errb.String(), "fix failed (x.y): boom") {
		t.Errorf("stderr = %q, want the fix error reported", errb.String())
	}
}

func TestDoctorFixConfirmer(t *testing.T) {
	cmd, _, _ := newTestDoctorCmd()

	t.Run("yes always approves, no TTY needed", func(t *testing.T) {
		origTTY := tui.StdinIsTerminal
		tui.StdinIsTerminal = func() bool { return false }
		defer func() { tui.StdinIsTerminal = origTTY }()

		c, err := doctorFixConfirmer(cmd, true)
		if err != nil {
			t.Fatalf("doctorFixConfirmer: %v", err)
		}
		ok, err := c.Confirm(context.Background(), "x")
		if err != nil || !ok {
			t.Errorf("Confirm = %v, %v; want true, nil", ok, err)
		}
	})
	t.Run("refuses without -y when not a TTY", func(t *testing.T) {
		origTTY := tui.StdinIsTerminal
		tui.StdinIsTerminal = func() bool { return false }
		defer func() { tui.StdinIsTerminal = origTTY }()

		_, err := doctorFixConfirmer(cmd, false)
		if !errors.Is(err, exitcode.ErrUserInput) {
			t.Errorf("err = %v, want ErrUserInput", err)
		}
	})
}

// TestRunDoctor_FixWithNothingFixableNeedsNoTTYOrYes guards a bug found
// while manually smoke-testing this feature: --fix used to demand -y
// (or a TTY) unconditionally, even when the report had nothing
// fixable at all (e.g. no server configured yet — a config.server
// failure, which has no attached fix). --fix must only require a way
// to confirm when there is actually something to confirm.
func TestRunDoctor_FixWithNothingFixableNeedsNoTTYOrYes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no config at all
	cmd, _, errb := newTestDoctorCmd()
	err := runDoctor(cmd, &doctorFlags{fix: true, timeout: 5 * time.Second})
	// Still reports doctor's own real failure (no server configured) —
	// --fix must not mask or replace that with a confirmer error.
	if got := exitcode.From(err); got != exitcode.ExitUserError {
		t.Fatalf("exitcode.From(err) = %d, want ExitUserError (the underlying no-server failure), got err=%v", got, err)
	}
	if !strings.Contains(errb.String(), "no fixable issues") {
		t.Errorf("stderr = %q, want a note that nothing was fixable", errb.String())
	}
}

func TestRunDoctor_FixRejectsJSONOutput(t *testing.T) {
	origMode := outputMode
	outputMode = "json"
	defer func() { outputMode = origMode }()

	cmd, _, _ := newTestDoctorCmd()
	err := runDoctor(cmd, &doctorFlags{fix: true, timeout: 5 * time.Second})
	if !errors.Is(err, exitcode.ErrUserInput) {
		t.Errorf("err = %v, want ErrUserInput", err)
	}
}

// stubTemplateRunFn replaces templateRunFn for the duration of the
// test (restore it via the returned func), so fixRebuildTemplate can be
// exercised without real interactive image/storage pickers.
func stubTemplateRunFn(t *testing.T, result *template.Result, err error) func() {
	t.Helper()
	orig := templateRunFn
	templateRunFn = func(context.Context, template.Options) (*template.Result, error) {
		return result, err
	}
	return func() { templateRunFn = orig }
}

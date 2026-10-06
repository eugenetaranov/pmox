package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvessh"
	"github.com/eugenetaranov/pmox/internal/setup"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/tui/wizard"
)

const formURL = "https://pve.home.lan:8006/api2/json"

// stubOps is a scripted wizardOps. Persist and CloudInit use the real
// cores (writing under the test's XDG_CONFIG_HOME) so tests can assert
// on what actually lands on disk.
type stubOps struct {
	mu sync.Mutex

	reach      []setup.Reach // consumed in order; the last one repeats
	probeHang  bool          // Probe blocks until its context is cancelled
	pinChange  *pinChange
	createErr  error
	verifyErr  []error // consumed in order
	nodes      []pveclient.Node
	nodesErr   error
	resources  map[string]nodeResources
	enableErr  error
	hostKnown  bool
	sshErr     []error // consumed in order
	persistErr error

	calls []string
}

func (o *stubOps) record(c string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, c)
}

func (o *stubOps) count(prefix string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, c := range o.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func pop[T any](list *[]T, zero T) T {
	if len(*list) == 0 {
		return zero
	}
	v := (*list)[0]
	if len(*list) > 1 {
		*list = (*list)[1:]
	}
	return v
}

func (o *stubOps) Probe(ctx context.Context, c string) setup.Reach {
	o.record("probe " + c)
	if o.probeHang {
		<-ctx.Done()
		return setup.Reach{Status: pveclient.ReachUnreachable, Err: ctx.Err()}
	}
	return pop(&o.reach, setup.Reach{Status: pveclient.Reachable})
}

func (o *stubOps) CheckPin(_ context.Context, _ *config.Config, c string, _ bool, accepted string) (string, *pinChange) {
	o.record("pin " + c)
	if o.pinChange != nil && accepted == "" {
		return "", o.pinChange
	}
	return accepted, nil
}

func (o *stubOps) CreateToken(_ context.Context, _ string, _ bool, _, user, _, name string, replace bool) (string, string, error) {
	if replace {
		o.record("replace " + name)
		return user + "!" + name, "replaced-secret", nil
	}
	o.record("create " + name)
	if o.createErr != nil {
		return "", "", o.createErr
	}
	return user + "!" + name, "generated-secret", nil
}

func (o *stubOps) VerifyToken(_ context.Context, _, id, _ string, known bool, _ string) (bool, error) {
	o.record("verify " + id)
	return known, pop(&o.verifyErr, nil)
}

func (o *stubOps) ListNodes(context.Context, *pveclient.Client) ([]pveclient.Node, error) {
	o.record("nodes")
	return o.nodes, o.nodesErr
}

func (o *stubOps) NodeResources(_ context.Context, _ *pveclient.Client, node string) nodeResources {
	o.record("resources " + node)
	return o.resources[node]
}

func (o *stubOps) EnableSnippets(_ context.Context, _ *pveclient.Client, storage string, _ []string) error {
	o.record("enable " + storage)
	return o.enableErr
}

func (o *stubOps) HostKnown(string) (bool, string, error) {
	o.record("hostknown")
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hostKnown, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "pmox", "known_hosts"), nil
}

func (o *stubOps) FetchHostKey(_ context.Context, host string) (pvessh.HostKey, error) {
	o.record("fetchkey")
	return pvessh.HostKey{Host: host, Addr: "10.0.0.5:22", Type: "ssh-ed25519", Fingerprint: "SHA256:stubbed"}, nil
}

func (o *stubOps) PinHostKey(string, pvessh.HostKey) error {
	o.record("pinkey")
	o.mu.Lock()
	o.hostKnown = true
	o.mu.Unlock()
	return nil
}

func (o *stubOps) ValidateSSH(_ context.Context, cfg pvessh.Config) error {
	o.record("ssh " + cfg.User)
	return pop(&o.sshErr, nil)
}

func (o *stubOps) Persist(cfg *config.Config, in persistInput) (*config.Server, []notice, error) {
	o.record("persist")
	if o.persistErr != nil {
		return nil, nil, o.persistErr
	}
	return persistCore(cfg, in)
}

func (o *stubOps) CloudInit(canonical, user, key string) cloudInitResult {
	o.record("cloudinit")
	return ensureCloudInit(canonical, user, key)
}

// oneNode is a cluster where every Defaults field has exactly one
// option, so the page needs no picking.
func oneNode() *stubOps {
	return &stubOps{
		nodes: []pveclient.Node{{Node: "pve", Status: "online"}},
		resources: map[string]nodeResources{"pve": {
			templates: []pveclient.Template{{VMID: 9000, Name: "ubuntu-2404-pmox-9000"}}, templateTotal: 1,
			storage: []pveclient.Storage{
				{Storage: "local", Type: "dir", Content: "iso,snippets"},
				{Storage: "local-lvm", Type: "lvmthin", Content: "images,rootdir"},
			},
			bridges: []pveclient.Bridge{{Iface: "vmbr0"}},
		}},
		hostKnown: true,
	}
}

// harness drives the real wizard model with real stages and stub ops,
// without a terminal: forms are "submitted" by setting the stage's
// bound values and calling its submit method, exactly what huh does on
// Enter.
type harness struct {
	t    *testing.T
	ops  *stubOps
	st   *wizState
	m    *wizard.Model
	conn *connectionStage
	defs *defaultsStage
	acc  *accessStage
	rev  *reviewStage
	save *saveStage
}

func newHarness(t *testing.T, ops *stubOps, st *wizState, start string) *harness {
	t.Helper()
	if st == nil {
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		st = newWizState(cfg)
	}
	st.ops = ops
	h := &harness{t: t, ops: ops, st: st,
		conn: &connectionStage{st: st}, defs: &defaultsStage{st: st}, acc: &accessStage{st: st},
		rev: &reviewStage{st: st}, save: &saveStage{st: st}}
	h.m = wizard.New(context.Background(), []wizard.Stage{h.conn, h.defs, h.acc, h.rev, h.save},
		wizard.Options{Start: start, Hub: "review"})
	h.run(h.m.Init())
	h.run(func() tea.Msg { return tea.WindowSizeMsg{Width: 160, Height: 50} })
	return h
}

var cmdType = reflect.TypeOf(tea.Cmd(nil))

// run executes cmd and feeds every resulting message back into the
// model until nothing is left, honoring tea.Batch / tea.Sequence (both
// are slices of commands). Commands that don't return promptly — cursor
// blinks, spinner ticks — are dropped.
func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(30 * time.Millisecond):
		// Timers (cursor blink, spinner tick) never return promptly and
		// are dropped — but a wizard operation still running is waited
		// for, however slow the machine.
		if opsInFlight.Load() == 0 {
			return
		}
		select {
		case msg = <-ch:
		case <-time.After(5 * time.Second):
			return // not ours after all (e.g. a timer); drop it
		}
	}
	if msg == nil {
		return
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == cmdType {
		for i := 0; i < v.Len(); i++ {
			h.run(v.Index(i).Interface().(tea.Cmd))
		}
		return
	}
	if _, ok := msg.(tea.QuitMsg); ok {
		return
	}
	// Timer messages (cursor blink, spinner tick) re-arm themselves; never
	// feed them back, or the harness would loop on them.
	if name := reflect.TypeOf(msg).String(); strings.Contains(strings.ToLower(name), "blink") || strings.Contains(name, "spinner.TickMsg") {
		return
	}
	_, next := h.m.Update(msg)
	h.run(next)
}

func (h *harness) active() string { return h.m.Active().ID() }

func (h *harness) expectStage(id string) {
	h.t.Helper()
	if h.active() != id {
		h.t.Fatalf("active stage = %s, want %s\n%s", h.active(), id, h.m.View())
	}
}

func (h *harness) key(k tea.KeyMsg) { h.run(func() tea.Msg { return k }) }
func (h *harness) answer(yes bool) {
	r := 'n'
	if yes {
		r = 'y'
	}
	h.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

func (h *harness) submitConnection(url string) {
	h.conn.in.urlRaw = url
	h.conn.in.tokenSource = "paste"
	h.conn.in.tokenID = "root@pam!pmox"
	h.conn.in.tokenSecret = "sek"
	h.run(h.conn.submit())
}

func (h *harness) submitAccess() {
	h.acc.keyAction = "existing"
	h.acc.keyPath = writePubKey(h.t, "ssh-ed25519 AAAA wizard@test\n")
	h.acc.auth, h.acc.password = "password", "ssh-pass"
	h.run(h.acc.submit())
}

func (h *harness) review(action string) {
	h.expectStage("review")
	h.rev.action = action
	h.run(h.rev.submit())
}

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
}

func TestWizardHappyPathWritesConfigAndSummary(t *testing.T) {
	isolate(t)
	h := newHarness(t, oneNode(), nil, "")
	h.expectStage("connection")

	h.submitConnection("pve.home.lan")
	h.expectStage("defaults")
	if v := h.m.View(); !strings.Contains(v, "9000  ubuntu-2404-pmox-9000") || !strings.Contains(v, "vmbr0") {
		t.Fatalf("defaults page missing discovered values:\n%s", v)
	}
	h.run(h.defs.submit())
	h.expectStage("access")
	h.submitAccess()
	h.review("confirm")

	if !h.m.Done() || h.m.Err() != nil {
		t.Fatalf("done=%v err=%v", h.m.Done(), h.m.Err())
	}
	cfg, _ := config.Load()
	srv := cfg.Servers[formURL]
	if srv == nil || srv.Node != "pve" || srv.Template != "9000" || srv.Storage != "local-lvm" ||
		srv.SnippetStorage != "local" || srv.Bridge != "vmbr0" || srv.User != "ubuntu" ||
		srv.NodeSSH == nil || srv.NodeSSH.Auth != config.AuthPassword {
		t.Fatalf("saved server wrong: %+v", srv)
	}
	if sec, err := credstore.Get(formURL); err != nil || sec != "sek" {
		t.Errorf("token secret = %q, %v", sec, err)
	}
	if pw, err := credstore.GetNodeSSHPassword(formURL); err != nil || pw != "ssh-pass" {
		t.Errorf("node ssh password = %q, %v", pw, err)
	}
	final := h.m.View()
	for _, want := range []string{"configured server " + formURL, "config saved to", "wrote cloud-init template"} {
		if !strings.Contains(final, want) {
			t.Errorf("summary missing %q:\n%s", want, final)
		}
	}
}

func TestWizardUnreachableStaysOnConnectionWithValues(t *testing.T) {
	isolate(t)
	ops := oneNode()
	ops.reach = []setup.Reach{{Status: pveclient.ReachUnreachable, Err: errors.New("connection refused")}, {Status: pveclient.Reachable}}
	h := newHarness(t, ops, nil, "")

	h.submitConnection("10.0.0.9")
	h.expectStage("connection")
	v := h.m.View()
	if !strings.Contains(v, "nothing responding at 10.0.0.9:8006") || !strings.Contains(v, "connection refused") {
		t.Errorf("want the reachability error with its cause:\n%s", v)
	}
	if h.conn.in.urlRaw != "10.0.0.9" || h.conn.in.tokenID != "root@pam!pmox" {
		t.Errorf("answers not preserved: %+v", h.conn.in)
	}
	h.run(h.conn.submit())
	h.expectStage("defaults")
}

func TestWizardRejectedTokenStaysOnConnection(t *testing.T) {
	isolate(t)
	ops := oneNode()
	ops.verifyErr = []error{pveclient.ErrUnauthorized}
	h := newHarness(t, ops, nil, "")
	h.submitConnection("pve.home.lan")
	h.expectStage("connection")
	if !strings.Contains(h.m.View(), "credential check failed") {
		t.Errorf("missing credential error:\n%s", h.m.View())
	}
}

func TestWizardTokenNameCollision(t *testing.T) {
	collide := func(t *testing.T) (*harness, *stubOps) {
		isolate(t)
		ops := oneNode()
		ops.createErr = pveclient.ErrTokenExists
		h := newHarness(t, ops, nil, "")
		h.conn.in.urlRaw, h.conn.in.tokenSource, h.conn.in.password = "pve.home.lan", "generate", "pw"
		h.conn.in.tokenName = "pmox-personal"
		h.run(h.conn.submit())
		if !strings.Contains(h.m.View(), "root@pam!pmox-personal already exists. Replace it?") {
			t.Fatalf("no replace dialog:\n%s", h.m.View())
		}
		return h, ops
	}
	t.Run("default keeps the token and asks for another name", func(t *testing.T) {
		h, ops := collide(t)
		h.key(tea.KeyMsg{Type: tea.KeyEsc})
		h.expectStage("connection")
		if !strings.Contains(h.m.View(), `a token named "pmox-personal" already exists`) || ops.count("replace") != 0 {
			t.Errorf("calls=%v view:\n%s", ops.calls, h.m.View())
		}
	})
	t.Run("replace deletes and recreates", func(t *testing.T) {
		h, ops := collide(t)
		h.answer(true)
		h.expectStage("defaults")
		if ops.count("replace pmox-personal") != 1 || h.st.conn.secret != "replaced-secret" {
			t.Errorf("calls=%v conn=%+v", ops.calls, h.st.conn)
		}
	})
}

func TestWizardGeneratedTokenIsUsedAndReported(t *testing.T) {
	isolate(t)
	h := newHarness(t, oneNode(), nil, "")
	h.conn.in.urlRaw, h.conn.in.tokenSource, h.conn.in.password = "pve.home.lan", "generate", "pw"
	h.run(h.conn.submit())
	h.expectStage("defaults")
	if h.st.conn.tokenID != "root@pam!pmox" || h.st.conn.secret != "generated-secret" {
		t.Errorf("conn = %+v", h.st.conn)
	}
	if !strings.Contains(renderSummary(h.st.summary), "created API token root@pam!pmox") {
		t.Errorf("summary missing token creation: %v", h.st.summary)
	}
}

func renderSummary(lines []wizard.Line) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

func TestWizardOverwriteDeclined(t *testing.T) {
	isolate(t)
	cfg := &config.Config{Servers: map[string]*config.Server{formURL: {TokenID: "orig@pve!x"}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	ops := oneNode()
	h := newHarness(t, ops, nil, "")
	h.submitConnection("pve.home.lan")
	if !strings.Contains(h.m.View(), "already configured. Overwrite?") {
		t.Fatalf("no overwrite dialog:\n%s", h.m.View())
	}
	h.answer(false)
	if !errors.Is(h.m.Err(), errOverwriteDeclined) {
		t.Fatalf("err = %v, want errOverwriteDeclined", h.m.Err())
	}
	if ops.count("probe") != 0 {
		t.Error("probed after the overwrite was declined")
	}
}

func TestWizardChangedCertificate(t *testing.T) {
	change := &pinChange{fp: strings.Repeat("bb", 32), oldFP: strings.Repeat("aa", 32), newFP: strings.Repeat("bb", 32)}
	t.Run("declined aborts before any credential", func(t *testing.T) {
		isolate(t)
		ops := oneNode()
		ops.pinChange = change
		h := newHarness(t, ops, nil, "")
		h.submitConnection("pve.home.lan")
		v := h.m.View()
		if !strings.Contains(v, "re-pin") || !strings.Contains(v, "pinned: sha256:"+change.oldFP) {
			t.Fatalf("no re-pin dialog with fingerprints:\n%s", v)
		}
		h.key(tea.KeyMsg{Type: tea.KeyEsc}) // Esc = safe answer (No)
		if !errors.Is(h.m.Err(), tui.ErrAborted) {
			t.Fatalf("err = %v, want ErrAborted", h.m.Err())
		}
		if ops.count("verify") != 0 {
			t.Error("token verified despite declining the certificate")
		}
	})
	t.Run("accepted re-pins and continues", func(t *testing.T) {
		isolate(t)
		ops := oneNode()
		ops.pinChange = change
		h := newHarness(t, ops, nil, "")
		h.submitConnection("pve.home.lan")
		h.answer(true)
		h.expectStage("defaults")
		if h.st.conn.pin != change.fp {
			t.Errorf("pin = %q, want the new fingerprint", h.st.conn.pin)
		}
	})
}

func TestWizardStaleResultIsIgnored(t *testing.T) {
	isolate(t)
	h := newHarness(t, oneNode(), nil, "")
	old := h.conn.seq
	h.conn.seq++ // the user went back / retried since that probe started
	h.run(func() tea.Msg { return connProbeMsg{seq: old, r: reachResult{ok: true}} })
	h.expectStage("connection")
	if h.st.conn.canonical != "" {
		t.Errorf("stale probe result advanced the wizard: %+v", h.st.conn)
	}
}

func TestWizardDefaultsNodeChangeReloads(t *testing.T) {
	isolate(t)
	ops := oneNode()
	ops.nodes = []pveclient.Node{{Node: "pve"}, {Node: "pve2"}}
	ops.resources["pve2"] = nodeResources{
		templates: []pveclient.Template{{VMID: 9100, Name: "other"}}, templateTotal: 1,
		storage: []pveclient.Storage{{Storage: "nfs", Type: "nfs", Content: "images,snippets"}},
		bridges: []pveclient.Bridge{{Iface: "vmbr9"}},
	}
	h := newHarness(t, ops, nil, "")
	h.submitConnection("pve.home.lan")
	h.expectStage("defaults")

	h.defs.node = "pve2"
	h.run(h.defs.submit())
	h.expectStage("defaults") // reloaded, not advanced
	if ops.count("resources pve2") != 1 {
		t.Fatalf("calls = %v, want resources reloaded for pve2", ops.calls)
	}
	if v := h.m.View(); !strings.Contains(v, "9100  other") || !strings.Contains(v, "vmbr9") {
		t.Errorf("page not showing pve2's resources:\n%s", v)
	}
	h.run(h.defs.submit())
	h.expectStage("access")
	if h.st.defs.node != "pve2" || h.st.defs.template != "9100" || h.st.defs.bridge != "vmbr9" {
		t.Errorf("defs = %+v", h.st.defs)
	}
}

func TestWizardDefaultsStorageErrorFallsBackToManual(t *testing.T) {
	isolate(t)
	ops := oneNode()
	r := ops.resources["pve"]
	r.storage, r.storageErr = nil, errors.New("403 forbidden")
	ops.resources["pve"] = r
	h := newHarness(t, ops, nil, "")
	h.submitConnection("pve.home.lan")
	v := h.m.View()
	if !strings.Contains(v, "Datastore.Audit") || !strings.Contains(v, "Default storage") {
		t.Fatalf("want the permission notice and a manual storage field:\n%s", v)
	}
	if !h.defs.sc.manual {
		t.Error("storage field should be manual entry")
	}
	h.defs.storage = " typed-pool "
	h.run(h.defs.submit())
	if h.st.defs.storage != "typed-pool" {
		t.Errorf("storage = %q", h.st.defs.storage)
	}
}

func TestWizardDefaultsEnableSnippetsDialog(t *testing.T) {
	isolate(t)
	ops := oneNode()
	r := ops.resources["pve"]
	r.storage = []pveclient.Storage{{Storage: "local", Type: "dir", Content: "iso"}, {Storage: "local-lvm", Type: "lvmthin", Content: "images"}}
	ops.resources["pve"] = r
	h := newHarness(t, ops, nil, "")
	h.submitConnection("pve.home.lan")
	if !strings.Contains(h.m.View(), `Enable snippets on "local"?`) {
		t.Fatalf("no enable-snippets dialog:\n%s", h.m.View())
	}
	h.key(tea.KeyMsg{Type: tea.KeyEnter}) // default Yes
	if ops.count("enable local") != 1 || h.defs.snippet != "local" {
		t.Fatalf("calls=%v snippet=%q", ops.calls, h.defs.snippet)
	}
}

func TestWizardAccessHostKeyDialogThenSSHFailureStays(t *testing.T) {
	isolate(t)
	ops := oneNode()
	ops.hostKnown = false
	ops.sshErr = []error{errors.New("ssh: unable to authenticate"), nil}
	h := newHarness(t, ops, nil, "")
	h.submitConnection("pve.home.lan")
	h.run(h.defs.submit())
	h.submitAccess()

	v := h.m.View()
	if !strings.Contains(v, "SHA256:stubbed") || !strings.Contains(v, "ssh-ed25519") {
		t.Fatalf("no host-key dialog:\n%s", v)
	}
	h.answer(true)
	if ops.count("pinkey") != 1 {
		t.Fatalf("host key not pinned: %v", ops.calls)
	}
	h.expectStage("access")
	if !strings.Contains(h.m.View(), "unable to authenticate") {
		t.Errorf("ssh error not shown:\n%s", h.m.View())
	}
	if h.acc.password != "ssh-pass" || h.acc.user != "ubuntu" {
		t.Errorf("answers not preserved: user=%q password=%q", h.acc.user, h.acc.password)
	}
	h.run(h.acc.submit())
	h.expectStage("review")
}

func TestWizardHostKeyDeclinedStaysOnAccess(t *testing.T) {
	isolate(t)
	ops := oneNode()
	ops.hostKnown = false
	h := newHarness(t, ops, nil, "")
	h.submitConnection("pve.home.lan")
	h.run(h.defs.submit())
	h.submitAccess()
	h.answer(false)
	h.expectStage("access")
	if ops.count("pinkey") != 0 || ops.count("ssh") != 0 {
		t.Errorf("pinned or dialed after declining: %v", ops.calls)
	}
}

func TestWizardEditFromReviewReturnsToReview(t *testing.T) {
	isolate(t)
	ops := oneNode()
	h := newHarness(t, ops, nil, "")
	h.submitConnection("pve.home.lan")
	h.run(h.defs.submit())
	h.submitAccess()
	h.review("defaults")
	h.expectStage("defaults")
	h.run(h.defs.submit())
	h.expectStage("review") // not access
	if ops.count("ssh") != 1 {
		t.Errorf("access re-ran: %v", ops.calls)
	}
}

func TestWizardEscBackFromAccessKeepsDefaults(t *testing.T) {
	isolate(t)
	h := newHarness(t, oneNode(), nil, "")
	h.submitConnection("pve.home.lan")
	h.run(h.defs.submit())
	h.expectStage("access")
	h.key(tea.KeyMsg{Type: tea.KeyEsc})
	h.expectStage("defaults")
	if h.defs.template != "9000" || h.defs.bridge != "vmbr0" {
		t.Errorf("previous answers not reselected: template=%q bridge=%q", h.defs.template, h.defs.bridge)
	}
}

func TestWizardCancelWritesNothing(t *testing.T) {
	isolate(t)
	h := newHarness(t, oneNode(), nil, "")
	h.submitConnection("pve.home.lan")
	h.run(h.defs.submit())
	h.submitAccess()
	h.review("cancel")
	if h.m.Err() == nil {
		t.Fatal("want a cancellation error")
	}
	cfg, _ := config.Load()
	if len(cfg.Servers) != 0 {
		t.Errorf("cancel wrote %+v", cfg.Servers)
	}
}

func TestWizardCtrlCMidProbeWritesNothing(t *testing.T) {
	isolate(t)
	h := newHarness(t, oneNode(), nil, "")
	h.conn.in.urlRaw, h.conn.in.tokenSource, h.conn.in.tokenID, h.conn.in.tokenSecret = "pve.home.lan", "paste", "root@pam!pmox", "sek"
	h.conn.canonical = formURL
	h.run(wizard.Busy("Checking pve.home.lan:8006 …")) // probe in progress, result never delivered
	h.key(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !errors.Is(h.m.Err(), tui.ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted", h.m.Err())
	}
	cfg, _ := config.Load()
	if len(cfg.Servers) != 0 {
		t.Errorf("ctrl+c wrote %+v", cfg.Servers)
	}
}

func TestWizardCloudInitDriftDialog(t *testing.T) {
	isolate(t)
	// A cloud-init file that authorizes a different key already exists.
	other := writePubKey(t, "ssh-ed25519 BBBB other@host\n")
	writeInitialCloudInit(&fakePrompter{}, formURL, "ubuntu", other)

	h := newHarness(t, oneNode(), nil, "")
	h.submitConnection("pve.home.lan")
	h.run(h.defs.submit())
	h.submitAccess()
	h.review("confirm")
	if !strings.Contains(h.m.View(), "Regenerate it now") {
		t.Fatalf("no drift dialog:\n%s", h.m.View())
	}
	h.answer(true)
	if !h.m.Done() || !strings.Contains(h.m.View(), "regenerated cloud-init") {
		t.Errorf("done=%v view:\n%s", h.m.Done(), h.m.View())
	}
}

func TestWizardReviewShowsBuildChoiceReadably(t *testing.T) {
	isolate(t)
	st := newWizState(&config.Config{Servers: map[string]*config.Server{}})
	st.defs.template = createTemplateSentinel
	for _, r := range wizardReviewRows(st) {
		if strings.Contains(r, "\x00") {
			t.Fatalf("sentinel leaked into review row %q", r)
		}
	}
}

// --- runWizard / runEditForm wiring ---

func stubRunWizard(t *testing.T, fn func(stages []wizard.Stage, opts wizard.Options) (wizard.Result, error)) {
	t.Helper()
	orig := runWizardFn
	runWizardFn = func(_ context.Context, stages []wizard.Stage, opts wizard.Options) (wizard.Result, error) {
		return fn(stages, opts)
	}
	t.Cleanup(func() { runWizardFn = orig })
}

func TestRunWizardOverwriteDeclinedPrintsAbort(t *testing.T) {
	isolate(t)
	stubRunWizard(t, func([]wizard.Stage, wizard.Options) (wizard.Result, error) {
		return wizard.Result{}, errOverwriteDeclined
	})
	p := &fakePrompter{}
	if err := runInteractiveForm(context.Background(), p); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !strings.Contains(p.out.String(), "aborted; no changes") {
		t.Errorf("stdout = %q", p.out.String())
	}
}

func TestRunWizardBuildsTemplateAfterExit(t *testing.T) {
	isolate(t)
	defer stubTemplateRunFn(t, nil, errors.New("boom"))()
	stubRunWizard(t, func([]wizard.Stage, wizard.Options) (wizard.Result, error) {
		return wizard.Result{Value: wizardOutcome{
			in:  persistInput{canonical: formURL, template: createTemplateSentinel},
			srv: &config.Server{},
		}, Summary: []wizard.Line{{Text: "configured server " + formURL}, {Warn: true, Text: "WARNING: tls"}}}, nil
	})
	p := &fakePrompter{}
	if err := runInteractiveForm(context.Background(), p); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(p.err.String(), "building the template failed") {
		t.Errorf("template build was not attempted after the wizard: stderr=%q", p.err.String())
	}
	// Full-screen mode restores the terminal on exit, so the summary is
	// printed afterwards: info to stdout, warnings to stderr.
	if !strings.Contains(p.out.String(), "configured server "+formURL) || !strings.Contains(p.err.String(), "WARNING: tls") {
		t.Errorf("summary not printed after exit: stdout=%q stderr=%q", p.out.String(), p.err.String())
	}
}

func TestRunEditFormStartsOnReviewSeeded(t *testing.T) {
	isolate(t)
	cfg := &config.Config{Servers: map[string]*config.Server{
		formURL: {TokenID: "root@pam!pmox", Node: "pve", Template: "9000", Storage: "local-lvm",
			SnippetStorage: "local", Bridge: "vmbr0", User: "deploy",
			NodeSSH: &config.NodeSSH{User: "root", Auth: config.AuthPassword}},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if err := credstore.SetNodeSSHPassword(formURL, "stored-pw"); err != nil {
		t.Fatal(err)
	}
	orig := establishEditConnectionFn
	establishEditConnectionFn = func(_ context.Context, _ prompter, _ *config.Config, c string) (resolvedConn, error) {
		return resolvedConn{canonical: c, tokenID: "root@pam!pmox", secret: "sek"}, nil
	}
	t.Cleanup(func() { establishEditConnectionFn = orig })

	var gotStart string
	var st *wizState
	stubRunWizard(t, func(stages []wizard.Stage, opts wizard.Options) (wizard.Result, error) {
		gotStart = opts.Start
		st = stages[0].(*connectionStage).st
		return wizard.Result{}, nil
	})
	loaded, _ := config.Load()
	if err := runEditForm(context.Background(), &fakePrompter{}, loaded, formURL); err != nil {
		t.Fatal(err)
	}
	if gotStart != "review" {
		t.Errorf("start = %q, want review", gotStart)
	}
	if st.defs.template != "9000" || st.acc.user != "deploy" || st.acc.sshPassword != "stored-pw" || !st.confirmed[formURL] {
		t.Errorf("state not seeded from the stored config: defs=%+v acc=%+v", st.defs, st.acc)
	}

	// And the real model opens on Review listing those values.
	h := newHarness(t, oneNode(), st, "review")
	h.expectStage("review")
	if v := h.m.View(); !strings.Contains(v, "Template:  9000") || !strings.Contains(v, "User:      deploy") {
		t.Errorf("review not showing current values:\n%s", v)
	}
}

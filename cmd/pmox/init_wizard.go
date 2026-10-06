package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvessh"
	"github.com/eugenetaranov/pmox/internal/sshkey"
	"github.com/eugenetaranov/pmox/internal/tui"
	"github.com/eugenetaranov/pmox/internal/tui/wizard"
)

// The interactive 'pmox init' / 'pmox config edit' experience: one
// persistent bubbletea program (internal/tui/wizard) with Connection,
// Defaults, Access and Review stages plus a hidden save step. Every
// network/SSH/disk side effect goes through wizardOps inside a tea.Cmd;
// each stage tags its in-flight operations with a sequence number and
// drops results from attempts the user has since abandoned.

// wizState is what the stages share: the answers collected so far and
// the lines to print in the final summary.
type wizState struct {
	ops        wizardOps
	cfg        *config.Config
	conn       resolvedConn
	defs       defaultsAnswers
	acc        accessAnswers
	prevConn   connInputs
	haveDefs   bool
	haveAccess bool
	confirmed  map[string]bool   // canonical URLs whose overwrite was OK'd
	repinned   map[string]string // canonical URL -> re-pin accepted this run
	summary    []wizard.Line
}

func newWizState(cfg *config.Config) *wizState {
	return &wizState{ops: newWizardOps(), cfg: cfg, confirmed: map[string]bool{}, repinned: map[string]string{}}
}

func (st *wizState) addSummary(ns ...notice) { st.summary = append(st.summary, toLines(ns)...) }

func toLines(ns []notice) []wizard.Line {
	lines := make([]wizard.Line, 0, len(ns))
	for _, n := range ns {
		lines = append(lines, wizard.Line{Warn: n.warn, Text: n.text})
	}
	return lines
}

func noticeText(ns []notice) string {
	texts := make([]string, 0, len(ns))
	for _, n := range ns {
		texts = append(texts, n.text)
	}
	return strings.Join(texts, "\n")
}

// wizardOutcome is the save step's result: what was persisted, so the
// caller can run the template build after the program exits.
type wizardOutcome struct {
	in  persistInput
	srv *config.Server
}

// runWizardFn is a seam so tests can drive runWizard without a terminal.
var runWizardFn = wizard.Run

// runWizard runs the stages full-screen starting at start, then — after
// the terminal is restored — prints the result summary and, if one was
// requested, builds a template with normal streaming output.
func runWizard(ctx context.Context, p prompter, st *wizState, title, start string) error {
	// SSHInsecure prints a one-shot warning to stderr on first use; get
	// it out before the program owns the terminal.
	SSHInsecure()

	stages := []wizard.Stage{
		&connectionStage{st: st},
		&defaultsStage{st: st},
		&accessStage{st: st},
		&reviewStage{st: st},
		&saveStage{st: st},
	}
	res, err := runWizardFn(ctx, stages, wizard.Options{Title: title, AltScreen: true, Start: start, Hub: "review"})
	if errors.Is(err, errOverwriteDeclined) {
		p.Printf("aborted; no changes\n")
		return nil
	}
	if err != nil {
		return err
	}
	for _, l := range res.Summary {
		if l.Warn {
			p.Errf("%s\n", l.Text)
		} else {
			p.Printf("%s\n", l.Text)
		}
	}
	out, ok := res.Value.(wizardOutcome)
	if !ok {
		return nil
	}
	if out.in.template == createTemplateSentinel {
		if err := offerBuiltTemplate(ctx, p, out.in, out.srv); err != nil {
			p.Errf("warning: building the template failed: %v\n", err)
			p.Errf("no default template is set — run 'pmox template create' when ready, then set one with a fresh 'pmox init' (or edit the config file's 'template:' field).\n")
		}
	}
	return nil
}

// opsInFlight counts wizard operations created but not yet finished. The
// test harness waits for these (and only these) instead of guessing with
// a timeout, which flaked on slow CI runners.
var opsInFlight atomic.Int32

// busyThen shows label with a spinner, then runs op. Sequenced (not
// batched) so the spinner can never start after op's result arrived.
func busyThen(label string, op func() tea.Msg) tea.Cmd {
	opsInFlight.Add(1)
	return tea.Sequence(wizard.Busy(label), func() tea.Msg {
		defer opsInFlight.Add(-1)
		return op()
	})
}

// ---------------------------------------------------------------- Connection

type (
	connProbeMsg struct {
		seq int
		r   reachResult
	}
	connPinMsg struct {
		seq    int
		pin    string
		change *pinChange
	}
	connTokenMsg struct {
		seq        int
		id, secret string
		err        error
	}
	connVerifyMsg struct {
		seq      int
		insecure bool
		err      error
	}
)

type connectionStage struct {
	st   *wizState
	ctx  context.Context
	seq  int
	in   connInputs
	form *wizard.Form

	canonical       string
	insecure        bool
	pin             string
	tokenID, secret string
	notices         []wizard.Line
}

func (s *connectionStage) ID() string       { return "connection" }
func (s *connectionStage) Title() string    { return "Connection" }
func (s *connectionStage) Subtitle() string { return stageSubtitles["Connection"] }
func (s *connectionStage) View() string     { return s.form.View() }

func (s *connectionStage) Enter(ctx context.Context) tea.Cmd {
	s.ctx = ctx
	s.seq++
	s.in = withConnDefaults(s.st.prevConn)
	s.notices = nil
	return s.newForm()
}

func (s *connectionStage) newForm() tea.Cmd {
	s.form = wizard.NewForm(connectionForm(&s.in))
	return s.form.Init()
}

// retry abandons the current attempt and reopens the page with every
// answer preserved and text as the error.
func (s *connectionStage) retry(text string) tea.Cmd {
	s.seq++
	return tea.Batch(wizard.Fail(text), s.newForm())
}

func (s *connectionStage) Update(msg tea.Msg) (wizard.Stage, tea.Cmd) {
	switch msg := msg.(type) {
	case connProbeMsg:
		if msg.seq == s.seq {
			return s, s.onProbe(msg.r)
		}
		return s, nil
	case connPinMsg:
		if msg.seq == s.seq {
			return s, s.onPin(msg.pin, msg.change)
		}
		return s, nil
	case connTokenMsg:
		if msg.seq == s.seq {
			return s, s.onToken(msg)
		}
		return s, nil
	case connVerifyMsg:
		if msg.seq == s.seq {
			return s, s.onVerify(msg.insecure, msg.err)
		}
		return s, nil
	}
	cmd, submitted := s.form.Update(msg)
	if submitted {
		return s, tea.Batch(cmd, s.submit())
	}
	return s, cmd
}

func (s *connectionStage) submit() tea.Cmd {
	canonical, upgraded, err := config.CanonicalizeURLVerbose(s.in.urlRaw)
	if err != nil {
		return s.retry(err.Error())
	}
	s.canonical = canonical
	s.notices = nil
	if upgraded {
		s.notices = append(s.notices, wizard.Line{Warn: true, Text: "note: using https (upgraded from http): " + canonical})
	}
	cmds := []tea.Cmd{wizard.Notice(s.notices)}
	if _, exists := s.st.cfg.Servers[canonical]; exists && !s.st.confirmed[canonical] {
		seq := s.seq
		return tea.Batch(append(cmds, wizard.Ask(&wizard.Dialog{
			Title: fmt.Sprintf("Server %s is already configured. Overwrite?", canonical),
			OnResult: func(yes bool) tea.Cmd {
				if seq != s.seq {
					return nil
				}
				if !yes {
					return wizard.Abort(errOverwriteDeclined)
				}
				s.st.confirmed[canonical] = true
				return s.probe()
			},
		}))...)
	}
	return tea.Batch(append(cmds, s.probe())...)
}

// reachResult is a classified reachability probe (see classifyProbe).
type reachResult struct {
	insecure, ok bool
	notices      []notice
}

func (s *connectionStage) probe() tea.Cmd {
	seq, ctx, c, ops := s.seq, s.ctx, s.canonical, s.st.ops
	return busyThen("Checking "+hostPort(c)+" …", func() tea.Msg {
		insecure, ok, notices := classifyProbe(ops.Probe(ctx, c), c)
		return connProbeMsg{seq: seq, r: reachResult{insecure: insecure, ok: ok, notices: notices}}
	})
}

func (s *connectionStage) onProbe(r reachResult) tea.Cmd {
	if !r.ok {
		return s.retry(noticeText(r.notices))
	}
	s.insecure = r.insecure
	s.notices = append(s.notices, toLines(r.notices)...)
	if r.insecure {
		s.st.addSummary(r.notices...)
	}
	seq, ctx, c, ops, cfg, insecure, accepted := s.seq, s.ctx, s.canonical, s.st.ops, s.st.cfg, s.insecure, s.st.repinned[s.canonical]
	return tea.Batch(wizard.Notice(s.notices), busyThen("Checking the TLS certificate …", func() tea.Msg {
		pin, change := ops.CheckPin(ctx, cfg, c, insecure, accepted)
		return connPinMsg{seq: seq, pin: pin, change: change}
	}))
}

func (s *connectionStage) onPin(pin string, change *pinChange) tea.Cmd {
	c := s.canonical
	if change != nil {
		seq := s.seq
		return wizard.Ask(&wizard.Dialog{
			Title: "Trust the new certificate and re-pin it?",
			Body:  tui.Warnf(change.notice(c).text),
			OnResult: func(yes bool) tea.Cmd {
				if seq != s.seq {
					return nil
				}
				if !yes {
					return wizard.Abort(tui.ErrAborted)
				}
				s.st.repinned[c] = change.fp
				s.pin = change.fp
				s.st.addSummary(infoNotice(fmt.Sprintf("re-pinning TLS certificate for %s (sha256:%s)", c, change.newFP)))
				return s.authenticate()
			},
		})
	}
	s.pin = pin
	if pin != storedPinFor(s.st.cfg, c) {
		s.st.repinned[c] = pin
	}
	return s.authenticate()
}

func (s *connectionStage) authenticate() tea.Cmd { return s.createToken(false) }

// createToken verifies a pasted token, or logs in and creates one —
// replacing an existing token of the same name when replace is set.
func (s *connectionStage) createToken(replace bool) tea.Cmd {
	if s.in.tokenSource == "paste" {
		s.tokenID, s.secret = strings.TrimSpace(s.in.tokenID), s.in.tokenSecret
		return s.verify()
	}
	seq, ctx, c, ops, insecure, pin := s.seq, s.ctx, s.canonical, s.st.ops, s.insecure, s.pin
	user, password, name := strings.TrimSpace(s.in.loginUser), s.in.password, strings.TrimSpace(s.in.tokenName)
	verb := "creating"
	if replace {
		verb = "replacing"
	}
	return busyThen(fmt.Sprintf("Logging in as %s and %s API token %q …", user, verb, name), func() tea.Msg {
		id, secret, err := ops.CreateToken(ctx, c, insecure, pin, user, password, name, replace)
		return connTokenMsg{seq: seq, id: id, secret: secret, err: err}
	})
}

func (s *connectionStage) onToken(msg connTokenMsg) tea.Cmd {
	if msg.err != nil {
		if errors.Is(msg.err, pveclient.ErrTokenExists) {
			name, user := strings.TrimSpace(s.in.tokenName), strings.TrimSpace(s.in.loginUser)
			seq := s.seq
			return tea.Batch(wizard.Idle(), wizard.Ask(&wizard.Dialog{
				Title:       fmt.Sprintf("Token %s!%s already exists. Replace it?", user, name),
				Body:        "Replacing deletes the existing token and creates a new one with the same name.\nAnything still using the old token (another machine, scripts) stops working.",
				Affirmative: "Replace",
				Negative:    "Choose another name",
				OnResult: func(yes bool) tea.Cmd {
					if seq != s.seq {
						return nil
					}
					if !yes {
						return s.retry(fmt.Sprintf("a token named %q already exists; choose another name", name))
					}
					return s.createToken(true)
				},
			}))
		}
		return s.retry(fmt.Sprintf("login/token creation failed: %v", msg.err))
	}
	s.tokenID, s.secret = msg.id, msg.secret
	s.st.addSummary(infoNotice(fmt.Sprintf("created API token %s (privilege separation off)", msg.id)))
	return s.verify()
}

func (s *connectionStage) verify() tea.Cmd {
	seq, ctx, c, ops, id, secret, insecure, pin := s.seq, s.ctx, s.canonical, s.st.ops, s.tokenID, s.secret, s.insecure, s.pin
	return busyThen("Verifying the API token …", func() tea.Msg {
		final, err := ops.VerifyToken(ctx, c, id, secret, insecure, pin)
		return connVerifyMsg{seq: seq, insecure: final, err: err}
	})
}

func (s *connectionStage) onVerify(insecure bool, err error) tea.Cmd {
	if err != nil {
		return s.retry(fmt.Sprintf("credential check failed: %v", err))
	}
	if insecure && !s.insecure {
		s.st.addSummary(tlsFallbackNotices(s.canonical)...)
	}
	prev := s.st.conn.canonical
	s.st.conn = resolvedConn{
		canonical: s.canonical, tokenID: s.tokenID, secret: s.secret, insecure: insecure, pin: s.pin,
		client: newInitClient(s.canonical, s.tokenID, s.secret, insecure, s.pin),
	}
	s.st.prevConn = s.in
	// A different server invalidates every discovered default: always
	// re-discover rather than returning straight to Review.
	if s.st.haveDefs && prev != s.canonical {
		s.st.haveDefs = false
		return wizard.GoTo("defaults")
	}
	return wizard.Next()
}

// ---------------------------------------------------------------- Defaults

type (
	defNodesMsg struct {
		seq   int
		nodes []pveclient.Node
		err   error
	}
	defResMsg struct {
		seq  int
		node string
		res  nodeResources
	}
	defEnableMsg struct {
		seq     int
		storage string
		err     error
	}
)

type defaultsStage struct {
	st  *wizState
	ctx context.Context
	seq int

	nodes      fieldChoice
	node       string // bound to the node field
	loadedNode string
	tc, sc, bc fieldChoice
	snipOpts   []huh.Option[string]

	template, storage, snippet, bridge string // bound
	notices                            []wizard.Line
	form                               *wizard.Form
}

func (s *defaultsStage) ID() string       { return "defaults" }
func (s *defaultsStage) Title() string    { return "Defaults" }
func (s *defaultsStage) Subtitle() string { return stageSubtitles["Defaults"] }

func (s *defaultsStage) View() string {
	if s.form == nil {
		return ""
	}
	return s.form.View()
}

func (s *defaultsStage) Enter(ctx context.Context) tea.Cmd {
	s.ctx = ctx
	s.seq++
	s.form, s.notices = nil, nil
	cur := s.st.defs
	s.node, s.template, s.storage, s.snippet, s.bridge = cur.node, cur.template, cur.storage, cur.snippetStorage, cur.bridge
	seq, ops, client := s.seq, s.st.ops, s.st.conn.client
	return busyThen("Listing nodes …", func() tea.Msg {
		nodes, err := ops.ListNodes(ctx, client)
		return defNodesMsg{seq: seq, nodes: nodes, err: err}
	})
}

func (s *defaultsStage) Update(msg tea.Msg) (wizard.Stage, tea.Cmd) {
	switch msg := msg.(type) {
	case defNodesMsg:
		if msg.seq == s.seq {
			return s, s.onNodes(msg.nodes, msg.err)
		}
		return s, nil
	case defResMsg:
		if msg.seq == s.seq && msg.node == s.node {
			return s, s.onResources(msg.node, msg.res)
		}
		return s, nil
	case defEnableMsg:
		if msg.seq == s.seq {
			return s, s.onEnabled(msg.storage, msg.err)
		}
		return s, nil
	}
	if s.form == nil {
		return s, nil
	}
	cmd, submitted := s.form.Update(msg)
	if submitted {
		return s, tea.Batch(cmd, s.submit())
	}
	return s, cmd
}

func (s *defaultsStage) onNodes(nodes []pveclient.Node, err error) tea.Cmd {
	s.nodes = nodeChoice(nodes, err, s.node)
	s.notices = toLines(s.nodes.notices)
	if !s.nodes.manual {
		s.node = s.nodes.initial
	}
	if strings.TrimSpace(s.node) == "" {
		// Nothing to load resources for yet: ask for the node alone.
		s.form = wizard.NewForm(huh.NewForm(huh.NewGroup(
			huh.NewInput().Title(s.nodes.manualLabel).Value(&s.node).Validate(validateNonEmpty),
		)))
		return tea.Batch(wizard.Idle(), wizard.Notice(s.notices), s.form.Init())
	}
	return s.load()
}

func (s *defaultsStage) load() tea.Cmd {
	s.node = strings.TrimSpace(s.node)
	seq, ctx, ops, client, node := s.seq, s.ctx, s.st.ops, s.st.conn.client, s.node
	return busyThen(fmt.Sprintf("Loading templates, storage and bridges on %s …", node), func() tea.Msg {
		return defResMsg{seq: seq, node: node, res: ops.NodeResources(ctx, client, node)}
	})
}

func (s *defaultsStage) onResources(node string, r nodeResources) tea.Cmd {
	s.loadedNode = node
	s.tc = templateChoice(r.templates, r.templateTotal, r.templateErr, node, s.template, true)
	s.sc = storageChoice(r.storage, r.storageErr, node, s.storage)
	s.bc = bridgeChoice(r.bridges, r.bridgeErr, node, s.bridge)
	s.notices = toLines(s.nodes.notices)
	for _, c := range []fieldChoice{s.tc, s.sc, s.bc} {
		s.notices = append(s.notices, toLines(c.notices)...)
	}

	// Snippet storage mirrors pickSnippetStorage: one capable storage is
	// fixed, several are a picker, none offers to enable snippets.
	s.snipOpts = nil
	if r.storageErr != nil {
		s.snippet = "" // the storage notice already explains why
		return s.showForm()
	}
	matches := pveclient.FilterStorage(r.storage, pveclient.Storage.SupportsSnippets)
	if len(matches) > 0 {
		for _, m := range matches {
			s.snipOpts = append(s.snipOpts, huh.NewOption(storageLabel(m), m.Storage))
		}
		s.snippet = optionOr(s.snipOpts, s.snippet, matches[0].Storage)
		return s.showForm()
	}
	target, ok := snippetEnableTarget(r.storage)
	if !ok {
		s.snippet = ""
		s.notices = append(s.notices, toLines(snippetRemediationNotices())...)
		return s.showForm()
	}
	seq, ctx, ops, client := s.seq, s.ctx, s.st.ops, s.st.conn.client
	return tea.Batch(wizard.Idle(), wizard.Ask(&wizard.Dialog{
		Title:   fmt.Sprintf("No storage supports snippets. Enable snippets on %q?", target.Storage),
		Body:    "pmox uploads cloud-init snippets to this storage when launching VMs.",
		Default: true,
		OnResult: func(yes bool) tea.Cmd {
			if seq != s.seq {
				return nil
			}
			if !yes {
				s.snippet = ""
				s.notices = append(s.notices, toLines(snippetRemediationNotices())...)
				return s.showForm()
			}
			content := snippetsEnabledContent(target)
			return busyThen(fmt.Sprintf("Enabling snippets on %s …", target.Storage), func() tea.Msg {
				return defEnableMsg{seq: seq, storage: target.Storage, err: ops.EnableSnippets(ctx, client, target.Storage, content)}
			})
		},
	}))
}

func (s *defaultsStage) onEnabled(storage string, err error) tea.Cmd {
	if err != nil {
		s.snippet = ""
		s.notices = append(s.notices, wizard.Line{Warn: true, Text: fmt.Sprintf("could not enable snippets on %q: %v", storage, err)})
		s.notices = append(s.notices, toLines(snippetRemediationNotices())...)
		return s.showForm()
	}
	s.snippet = storage
	s.snipOpts = []huh.Option[string]{huh.NewOption(storage, storage)}
	s.st.addSummary(infoNotice("enabled snippets on " + storage))
	return s.showForm()
}

// optionOr returns want when it is one of opts' values, else fallback.
func optionOr(opts []huh.Option[string], want, fallback string) string {
	for _, o := range opts {
		if o.Value == want && want != "" {
			return want
		}
	}
	return fallback
}

// choiceField renders a fieldChoice as a form field bound to v: free
// text when manual, a fixed note for a single option, else a select.
func choiceField(c fieldChoice, v *string) huh.Field {
	switch {
	case c.manual:
		return huh.NewInput().Title(c.manualLabel).Value(v)
	case len(c.opts) == 1:
		*v = c.opts[0].Value
		return huh.NewNote().Title(c.title).Description(c.opts[0].Key)
	default:
		*v = optionOr(c.opts, *v, c.initial)
		return huh.NewSelect[string]().Title(c.title).Options(c.opts...).Value(v)
	}
}

func (s *defaultsStage) showForm() tea.Cmd {
	fields := []huh.Field{s.nodeField()}
	fields = append(fields,
		choiceField(s.tc, &s.template),
		choiceField(s.sc, &s.storage),
		s.snippetField(),
		choiceField(s.bc, &s.bridge),
	)
	s.form = wizard.NewForm(huh.NewForm(huh.NewGroup(fields...)))
	return tea.Batch(wizard.Idle(), wizard.Notice(s.notices), s.form.Init())
}

func (s *defaultsStage) nodeField() huh.Field {
	switch {
	case s.nodes.manual:
		return huh.NewInput().Title("Default node").
			Description("changing it reloads templates, storage and bridges").
			Value(&s.node).Validate(validateNonEmpty)
	case len(s.nodes.opts) > 1:
		return huh.NewSelect[string]().Title("Default node").
			Description("changing it reloads templates, storage and bridges").
			Options(s.nodes.opts...).Value(&s.node)
	default:
		return huh.NewNote().Title("Default node").Description(s.nodes.opts[0].Key)
	}
}

func (s *defaultsStage) snippetField() huh.Field {
	switch len(s.snipOpts) {
	case 0:
		return huh.NewNote().Title("Snippet storage").Description("(none)")
	case 1:
		return huh.NewNote().Title("Snippet storage").Description(s.snipOpts[0].Key)
	default:
		return huh.NewSelect[string]().Title("Snippet storage").Options(s.snipOpts...).Value(&s.snippet)
	}
}

func (s *defaultsStage) submit() tea.Cmd {
	if strings.TrimSpace(s.node) != s.loadedNode {
		// A different node: its templates/storage/bridges differ, so
		// reload and show the page again instead of saving stale picks.
		s.template, s.storage, s.snippet, s.bridge = "", "", "", ""
		return s.load()
	}
	s.st.defs = defaultsAnswers{
		node:           s.loadedNode,
		template:       strings.TrimSpace(s.template),
		storage:        strings.TrimSpace(s.storage),
		snippetStorage: s.snippet,
		bridge:         strings.TrimSpace(s.bridge),
	}
	s.st.haveDefs = true
	return wizard.Next()
}

// ---------------------------------------------------------------- Access

type (
	accessHostMsg struct {
		seq        int
		known      bool
		knownHosts string
		key        pvessh.HostKey
		err        error
	}
	accessValidateMsg struct {
		seq int
		err error
	}
)

type accessStage struct {
	st  *wizState
	ctx context.Context
	seq int

	home, sshDir string
	pubKeys      []string
	host         string

	// bound form values
	keyAction    string // "generate" | "existing"
	keyPath      string
	user         string
	sshUser      string
	auth         string // "password" | "key"
	password     string
	keyFile      string
	keyProtected bool
	keyPass      string

	resolvedKey string
	form        *wizard.Form
}

func (s *accessStage) ID() string       { return "access" }
func (s *accessStage) Title() string    { return "Access" }
func (s *accessStage) Subtitle() string { return stageSubtitles["Access"] }

func (s *accessStage) View() string {
	if s.form == nil {
		return ""
	}
	return s.form.View()
}

func (s *accessStage) Enter(ctx context.Context) tea.Cmd {
	s.ctx = ctx
	s.seq++
	host, err := pvessh.HostFromURL(s.st.conn.canonical)
	if err != nil {
		return wizard.Abort(err)
	}
	s.host = host
	s.home, _ = os.UserHomeDir()
	s.sshDir = filepath.Join(s.home, ".ssh")
	s.pubKeys = sshkey.FindPubKeys(s.sshDir)

	prev := s.st.acc
	suggest := sshkey.DefaultSuggestion(prev.sshKey, s.sshDir)
	s.keyAction, s.keyPath = "generate", suggest
	if suggest != "" {
		s.keyAction = "existing"
	}
	s.user = prev.user
	if !s.st.haveAccess && s.user == "" {
		s.user = configuredUser(s.st.cfg, s.st.conn.canonical)
	}
	if s.user == "" {
		s.user = "ubuntu"
	}
	s.sshUser, s.auth = "root", "password"
	s.password, s.keyFile, s.keyPass, s.keyProtected = prev.sshPassword, "", prev.sshKeyPass, prev.sshKeyPass != ""
	if ns := prev.nodeSSH; ns != nil {
		if ns.User != "" {
			s.sshUser = ns.User
		}
		if ns.Auth == config.AuthKey {
			s.auth, s.keyFile = "key", ns.KeyPath
		}
	}
	return s.newForm()
}

func (s *accessStage) newForm() tea.Cmd {
	var keyField huh.Field
	if len(s.pubKeys) > 0 {
		opts := make([]huh.Option[string], 0, len(s.pubKeys))
		for _, k := range s.pubKeys {
			label := k
			if rel, err := filepath.Rel(s.sshDir, k); err == nil {
				label = rel
			}
			opts = append(opts, huh.NewOption(label, k))
		}
		s.keyPath = optionOr(opts, s.keyPath, s.pubKeys[0])
		keyField = huh.NewSelect[string]().Title("Default SSH public key").
			Description("shift+tab to go back and generate one instead").
			Options(opts...).Value(&s.keyPath)
	} else {
		keyField = huh.NewInput().Title("Default SSH public key path").Value(&s.keyPath).
			Validate(func(p string) error { return s.checkKey(p) })
	}

	s.form = wizard.NewForm(huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("SSH key for VM bootstrap").
				Options(
					huh.NewOption("Generate a new dedicated key", "generate"),
					huh.NewOption("Use an existing key", "existing"),
				).Value(&s.keyAction),
		),
		huh.NewGroup(keyField).WithHideFunc(func() bool { return s.keyAction != "existing" }),
		huh.NewGroup(
			huh.NewInput().Title("Default user").
				Description("created by cloud-init on every VM; also the default SSH login").
				Value(&s.user).Validate(validateNonEmpty),
			huh.NewInput().Title("Proxmox node SSH username").Value(&s.sshUser).Validate(validateNonEmpty),
			huh.NewSelect[string]().Title("Authenticate with").
				Options(huh.NewOption("Password", "password"), huh.NewOption("SSH key file", "key")).
				Value(&s.auth),
		),
		huh.NewGroup(
			huh.NewInput().Title("Password").EchoMode(huh.EchoModePassword).Value(&s.password).Validate(validateNonEmpty),
		).WithHideFunc(func() bool { return s.auth != "password" }),
		huh.NewGroup(
			huh.NewInput().Title("Path to SSH private key").Value(&s.keyFile).Validate(validateNonEmpty),
			huh.NewConfirm().Title("Key is passphrase-protected?").Value(&s.keyProtected),
		).WithHideFunc(func() bool { return s.auth != "key" }),
		huh.NewGroup(
			huh.NewInput().Title("Key passphrase").EchoMode(huh.EchoModePassword).Value(&s.keyPass),
		).WithHideFunc(func() bool { return s.auth != "key" || !s.keyProtected }),
	))
	return s.form.Init()
}

// checkKey reports whether p (with ~ expanded) is a readable file.
func (s *accessStage) checkKey(p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return fmt.Errorf("ssh key path is required")
	}
	expanded := p
	if strings.HasPrefix(expanded, "~/") {
		expanded = filepath.Join(s.home, expanded[2:])
	}
	if _, err := os.ReadFile(expanded); err != nil {
		return fmt.Errorf("cannot read %s: %w", p, err)
	}
	return nil
}

func (s *accessStage) retry(text string) tea.Cmd {
	s.seq++
	return tea.Batch(wizard.Fail(text), s.newForm())
}

func (s *accessStage) Update(msg tea.Msg) (wizard.Stage, tea.Cmd) {
	switch msg := msg.(type) {
	case accessHostMsg:
		if msg.seq == s.seq {
			return s, s.onHost(msg)
		}
		return s, nil
	case accessValidateMsg:
		if msg.seq == s.seq {
			return s, s.onValidated(msg.err)
		}
		return s, nil
	}
	if s.form == nil {
		return s, nil
	}
	cmd, submitted := s.form.Update(msg)
	if submitted {
		return s, tea.Batch(cmd, s.submit())
	}
	return s, cmd
}

func (s *accessStage) submit() tea.Cmd {
	if s.keyAction == "generate" {
		pub, reused, err := sshkey.EnsureBootstrap(s.sshDir, sshkey.DefaultComment())
		switch {
		case errors.Is(err, sshkey.ErrPubKeyMissing):
			priv := filepath.Join(s.sshDir, sshkey.BootstrapKeyName)
			return s.retry(fmt.Sprintf("%s exists but %s is missing; remove it or pick another key",
				displayPath(priv, s.home), displayPath(priv+".pub", s.home)))
		case err != nil:
			return s.retry(err.Error())
		case reused:
			s.st.addSummary(infoNotice("reusing existing pmox key: " + displayPath(pub, s.home)))
		default:
			s.st.addSummary(infoNotice("generated new SSH key: " + displayPath(pub, s.home)))
		}
		s.resolvedKey = pub
		// Show the generated key as the existing choice on a revisit.
		s.pubKeys = sshkey.FindPubKeys(s.sshDir)
		s.keyPath = pub
	} else {
		if err := s.checkKey(s.keyPath); err != nil {
			return s.retry(err.Error())
		}
		s.resolvedKey = strings.TrimSpace(s.keyPath)
	}

	if SSHInsecure() {
		return s.validate()
	}
	seq, ctx, ops, host := s.seq, s.ctx, s.st.ops, s.host
	return busyThen(fmt.Sprintf("Checking the SSH host key of %s …", host), func() tea.Msg {
		known, kh, err := ops.HostKnown(host)
		if err != nil || known {
			return accessHostMsg{seq: seq, known: known, knownHosts: kh, err: err}
		}
		k, err := ops.FetchHostKey(ctx, host)
		return accessHostMsg{seq: seq, knownHosts: kh, key: k, err: err}
	})
}

func (s *accessStage) onHost(msg accessHostMsg) tea.Cmd {
	if msg.err != nil {
		return s.retry(fmt.Sprintf("pin host key for %s: %v", s.host, msg.err))
	}
	if msg.known {
		return s.validate()
	}
	seq, k := s.seq, msg.key
	return tea.Batch(wizard.Idle(), wizard.Ask(&wizard.Dialog{
		Title: "Trust this host key and continue connecting?",
		Body: fmt.Sprintf("The authenticity of host '%s (%s)' can't be established.\n%s key fingerprint is %s",
			k.Host, k.Addr, k.Type, k.Fingerprint),
		OnResult: func(yes bool) tea.Cmd {
			if seq != s.seq {
				return nil
			}
			if !yes {
				return s.retry("host key not trusted — pmox needs SSH access to the node to upload cloud-init snippets")
			}
			if err := s.st.ops.PinHostKey(msg.knownHosts, k); err != nil {
				return s.retry(fmt.Sprintf("pin host key for %s: %v", s.host, err))
			}
			return s.validate()
		},
	}))
}

func (s *accessStage) sshConfig() (pvessh.Config, error) {
	cfg := pvessh.Config{Host: s.host, User: strings.TrimSpace(s.sshUser), Insecure: SSHInsecure()}
	if !cfg.Insecure {
		kh, err := sshKnownHostsPathFn()
		if err != nil {
			return cfg, err
		}
		cfg.KnownHosts = kh
	}
	if s.auth == "key" {
		cfg.KeyPath = strings.TrimSpace(sshkey.ExpandHome(s.keyFile))
		if s.keyProtected {
			cfg.KeyPass = s.keyPass
		}
	} else {
		cfg.Password = s.password
	}
	return cfg, nil
}

func (s *accessStage) validate() tea.Cmd {
	cfg, err := s.sshConfig()
	if err != nil {
		return s.retry(err.Error())
	}
	seq, ctx, ops := s.seq, s.ctx, s.st.ops
	return busyThen(fmt.Sprintf("Verifying SSH connectivity to %s …", s.host), func() tea.Msg {
		return accessValidateMsg{seq: seq, err: ops.ValidateSSH(ctx, cfg)}
	})
}

func (s *accessStage) onValidated(err error) tea.Cmd {
	if err != nil {
		return s.retry(err.Error())
	}
	cfg, _ := s.sshConfig()
	ns := &config.NodeSSH{User: cfg.User}
	acc := accessAnswers{sshKey: s.resolvedKey, user: strings.TrimSpace(s.user), nodeSSH: ns}
	if s.auth == "key" {
		ns.Auth, ns.KeyPath = config.AuthKey, cfg.KeyPath
		acc.sshKeyPass = cfg.KeyPass
	} else {
		ns.Auth = config.AuthPassword
		acc.sshPassword = s.password
	}
	s.st.acc, s.st.haveAccess = acc, true
	return wizard.Next()
}

// ---------------------------------------------------------------- Review

type reviewStage struct {
	st     *wizState
	action string
	form   *wizard.Form
}

func (s *reviewStage) ID() string       { return "review" }
func (s *reviewStage) Title() string    { return "Review" }
func (s *reviewStage) Subtitle() string { return stageSubtitles["Review"] }

func (s *reviewStage) Enter(context.Context) tea.Cmd {
	s.action = "confirm"
	s.form = wizard.NewForm(huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Apply this configuration?").Options(
			huh.NewOption("Confirm — write configuration", "confirm"),
			huh.NewOption("Edit connection (URL / token)", "connection"),
			huh.NewOption("Edit defaults (node / template / storage / bridge)", "defaults"),
			huh.NewOption("Edit access (SSH key / user / node SSH)", "access"),
			huh.NewOption("Cancel — quit without saving", "cancel"),
		).Value(&s.action),
	)))
	return s.form.Init()
}

func (s *reviewStage) View() string {
	var b strings.Builder
	for _, r := range wizardReviewRows(s.st) {
		b.WriteString("  " + r + "\n")
	}
	b.WriteString("\n")
	b.WriteString(s.form.View())
	return b.String()
}

// wizardReviewRows is reviewRows with the build-template choice spelled
// out instead of its internal sentinel value.
func wizardReviewRows(st *wizState) []string {
	defs := st.defs
	if defs.template == createTemplateSentinel {
		defs.template = "(build a new Ubuntu template after saving)"
	}
	rows := reviewRows(st.conn, defs, st.acc)
	if ns := st.acc.nodeSSH; ns != nil {
		rows = append(rows, fmt.Sprintf("Node SSH:  %s (%s)", ns.User, ns.Auth))
	}
	return rows
}

func (s *reviewStage) Update(msg tea.Msg) (wizard.Stage, tea.Cmd) {
	cmd, submitted := s.form.Update(msg)
	if !submitted {
		return s, cmd
	}
	return s, tea.Batch(cmd, s.submit())
}

func (s *reviewStage) submit() tea.Cmd {
	switch s.action {
	case "confirm":
		return wizard.GoTo("save")
	case "connection", "defaults", "access":
		return wizard.GoTo(s.action)
	default:
		return wizard.Abort(fmt.Errorf("%w: cancelled", exitcode.ErrUserInput))
	}
}

// ---------------------------------------------------------------- Save

type saveMsg struct {
	seq     int
	srv     *config.Server
	notices []notice
	ci      cloudInitResult
	err     error
}

// saveStage writes the configuration. It is a hidden step of the Review
// tab and can't be left with Esc while writing.
type saveStage struct {
	st      *wizState
	seq     int
	in      persistInput
	working bool
}

func (s *saveStage) ID() string       { return "save" }
func (s *saveStage) Title() string    { return "Review" }
func (s *saveStage) Subtitle() string { return stageSubtitles["Review"] }
func (s *saveStage) Hidden() bool     { return true }
func (s *saveStage) Pinned() bool     { return s.working }
func (s *saveStage) View() string     { return "" }

func (s *saveStage) Enter(context.Context) tea.Cmd {
	s.seq++
	s.working = true
	st := s.st
	s.in = persistInput{
		canonical: st.conn.canonical, tokenID: st.conn.tokenID, secret: st.conn.secret, insecure: st.conn.insecure,
		pin: st.conn.pin, node: st.defs.node, template: st.defs.template, storage: st.defs.storage,
		snippetStorage: st.defs.snippetStorage, bridge: st.defs.bridge,
		sshKey: st.acc.sshKey, user: st.acc.user, nodeSSH: st.acc.nodeSSH,
		sshPassword: st.acc.sshPassword, sshKeyPass: st.acc.sshKeyPass,
	}
	seq, ops, cfg, in := s.seq, st.ops, st.cfg, s.in
	return busyThen("Saving configuration …", func() tea.Msg {
		srv, notices, err := ops.Persist(cfg, in)
		if err != nil {
			return saveMsg{seq: seq, err: err}
		}
		return saveMsg{seq: seq, srv: srv, notices: notices, ci: ops.CloudInit(in.canonical, in.user, in.sshKey)}
	})
}

func (s *saveStage) Update(msg tea.Msg) (wizard.Stage, tea.Cmd) {
	m, ok := msg.(saveMsg)
	if !ok || m.seq != s.seq {
		return s, nil
	}
	s.working = false
	if m.err != nil {
		return s, wizard.Abort(m.err)
	}
	lines := append(append([]wizard.Line{}, s.st.summary...), toLines(m.notices)...)
	lines = append(lines, toLines(m.ci.notices)...)
	finish := func(extra []notice) tea.Cmd {
		return wizard.Finish(wizard.Result{
			Value:   wizardOutcome{in: s.in, srv: m.srv},
			Summary: append(lines, toLines(extra)...),
		})
	}
	if !m.ci.drift {
		return s, finish(nil)
	}
	return s, tea.Batch(wizard.Idle(), wizard.Ask(&wizard.Dialog{
		Title: "Regenerate it now with the selected user + key? (existing edits will be lost)",
		Body:  noticeText(m.ci.notices),
		OnResult: func(yes bool) tea.Cmd {
			return finish(m.ci.resolveDrift(yes))
		},
	}))
}

// contextName is canonical's context name, for the edit wizard's title.
func contextName(cfg *config.Config, canonical string) string {
	for _, c := range cfg.Contexts() {
		if c.URL == canonical {
			return c.Name
		}
	}
	return canonical
}

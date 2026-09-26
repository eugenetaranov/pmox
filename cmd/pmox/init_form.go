package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/setup"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// connInputs holds the raw Connection-page answers before probe/auth.
type connInputs struct {
	urlRaw      string
	tokenSource string // "generate" | "paste"
	loginUser   string
	password    string
	tokenName   string
	tokenID     string
	tokenSecret string
}

// resolvedConn is a connection that has been probed and authenticated.
type resolvedConn struct {
	canonical string
	tokenID   string
	secret    string
	insecure  bool
	pin       string // stored TLS pin enforced on this connection ("" = none)
	client    *pveclient.Client
}

type defaultsAnswers struct {
	node           string
	template       string
	storage        string
	snippetStorage string
	bridge         string
}

type accessAnswers struct {
	sshKey      string
	user        string
	nodeSSH     *config.NodeSSH
	sshPassword string
	sshKeyPass  string
}

// Seams so the orchestration can be tested without a TTY.
var (
	establishConnectionFn     = establishConnection
	establishEditConnectionFn = establishEditConnection
	collectDefaultsFn         = collectDefaults
	collectAccessFn           = collectAccess
	reviewFn                  = runReviewForm
)

// formState is the mutable state threaded through the connection →
// defaults → access → review stage loop (runFormLoop) — shared by a
// fresh 'pmox init' run (runInteractiveForm) and 'pmox config edit's
// pre-seeded one (runEditForm).
type formState struct {
	conn       resolvedConn
	defs       defaultsAnswers
	acc        accessAnswers
	prevConn   connInputs
	haveDefs   bool
	haveAccess bool
	confirmed  map[string]bool // canonical URLs whose overwrite was OK'd

	// Once the wizard has reached Review once, editing a single stage from
	// there returns straight back to Review instead of cascading forward
	// through the remaining stages (which would force re-entering data the
	// user already provided).
	reachedReview bool
}

// runInteractiveForm is the TTY init experience: phased form pages with a
// final review that can jump back and edit before anything is written.
func runInteractiveForm(ctx context.Context, p prompter) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return runFormLoop(ctx, p, cfg, "connection", &formState{confirmed: map[string]bool{}})
}

// runEditForm reopens the wizard for an already-configured context,
// landing straight on Review instead of the Connection stage —
// 'pmox config edit's whole point is never re-typing a URL/token that
// already works. establishEditConnectionFn re-verifies the stored
// credentials (reachability + a live token check) without asking for
// anything; defaults/access are pre-seeded from the server's current
// config (and, for node SSH, the keychain) so Review shows exactly
// what's configured today, and jumping back to Defaults/Access starts
// every field from its current value rather than a blank
// re-discovery.
func runEditForm(ctx context.Context, p prompter, cfg *config.Config, canonical string) error {
	srv, ok := cfg.Servers[canonical]
	if !ok {
		return fmt.Errorf("%w: no context named %q (see 'pmox config get-contexts')", exitcode.ErrNotFound, canonical)
	}
	conn, err := establishEditConnectionFn(ctx, p, cfg, canonical)
	if err != nil {
		return err
	}

	defs := defaultsAnswers{
		node: srv.Node, template: srv.Template, storage: srv.Storage,
		snippetStorage: srv.SnippetStorage, bridge: srv.Bridge,
	}
	acc := accessAnswers{sshKey: srv.SSHPubkey, user: srv.User, nodeSSH: srv.NodeSSH}
	if srv.NodeSSH != nil {
		switch srv.NodeSSH.Auth {
		case config.AuthPassword:
			if pw, perr := credstore.GetNodeSSHPassword(canonical); perr == nil {
				acc.sshPassword = pw
			}
		case config.AuthKey:
			if pp, perr := credstore.GetNodeSSHKeyPassphrase(canonical); perr == nil {
				acc.sshKeyPass = pp
			}
		}
	}

	return runFormLoop(ctx, p, cfg, "review", &formState{
		conn: conn, defs: defs, haveDefs: true, acc: acc, haveAccess: true,
		reachedReview: true, confirmed: map[string]bool{canonical: true},
	})
}

// runFormLoop drives the shared connection → defaults → access → review
// stage machine from whichever stage the caller starts it at, mutating
// st in place.
func runFormLoop(ctx context.Context, p prompter, cfg *config.Config, stage string, st *formState) error {
	for {
		switch stage {
		case "connection":
			printTabs(p, "Connection")
			rc, in, cerr := establishConnectionFn(ctx, p, cfg, st.prevConn, st.confirmed)
			if cerr != nil {
				if errors.Is(cerr, errOverwriteDeclined) {
					p.Printf("aborted; no changes\n")
					return nil
				}
				return cerr
			}
			st.prevConn, st.conn = in, rc
			if st.reachedReview {
				stage = "review"
			} else {
				stage = "defaults"
			}
		case "defaults":
			printTabs(p, "Defaults")
			d, derr := collectDefaultsFn(ctx, p, st.conn, st.defs, st.haveDefs)
			if derr != nil {
				return derr
			}
			st.defs, st.haveDefs = d, true
			if st.reachedReview {
				stage = "review"
			} else {
				stage = "access"
			}
		case "access":
			printTabs(p, "Access")
			a, aerr := collectAccessFn(ctx, p, cfg, st.conn.canonical, st.acc, st.haveAccess)
			if aerr != nil {
				return aerr
			}
			st.acc, st.haveAccess = a, true
			stage = "review"
		case "review":
			st.reachedReview = true
			printTabs(p, "Review")
			action, rerr := reviewFn(p, reviewRows(st.conn, st.defs, st.acc))
			if rerr != nil {
				return rerr
			}
			switch action {
			case "confirm":
				return persistServer(ctx, p, cfg, persistInput{
					canonical: st.conn.canonical, tokenID: st.conn.tokenID, secret: st.conn.secret, insecure: st.conn.insecure,
					pin: st.conn.pin, node: st.defs.node, template: st.defs.template, storage: st.defs.storage,
					snippetStorage: st.defs.snippetStorage, bridge: st.defs.bridge,
					sshKey: st.acc.sshKey, user: st.acc.user, nodeSSH: st.acc.nodeSSH,
					sshPassword: st.acc.sshPassword, sshKeyPass: st.acc.sshKeyPass,
				})
			case "connection", "defaults", "access":
				stage = action
			default:
				return fmt.Errorf("%w: cancelled", exitcode.ErrUserInput)
			}
		}
	}
}

// errOverwriteDeclined signals that the user chose not to overwrite an
// already-configured server, before anything (including a server-side API
// token) was created for it.
var errOverwriteDeclined = errors.New("overwrite declined")

// establishEditConnection re-verifies an already-configured context's
// stored credentials — reachability, then a live token check — without
// asking for anything. This is 'pmox config edit's answer to "always
// re-probe + re-auth, but never re-ask for credentials that already
// work": any failure names the specific problem and points at
// 'pmox init' to fix it, since repairing a broken connection isn't
// what edit mode is for.
func establishEditConnection(ctx context.Context, p prompter, cfg *config.Config, canonical string) (resolvedConn, error) {
	srv, ok := cfg.Servers[canonical]
	if !ok {
		return resolvedConn{}, fmt.Errorf("%w: no context named %q (see 'pmox config get-contexts')", exitcode.ErrNotFound, canonical)
	}
	secret, err := credstore.Get(canonical)
	if err != nil {
		return resolvedConn{}, fmt.Errorf("load stored token for %s: %w — run 'pmox init' to reconnect", canonical, err)
	}

	insecure, ok := probeURL(ctx, p, canonical)
	if !ok {
		return resolvedConn{}, fmt.Errorf("%w: %s is not reachable — run 'pmox init' to fix the connection", exitcode.ErrUserInput, canonical)
	}
	pin, err := resolveInitPin(ctx, p, cfg, canonical, insecure, "")
	if err != nil {
		return resolvedConn{}, err
	}
	insecure, err = validateCredentials(ctx, p, canonical, srv.TokenID, secret, insecure, pin)
	if err != nil {
		return resolvedConn{}, fmt.Errorf("stored token for %s no longer works: %w — run 'pmox init' to reconnect", canonical, err)
	}
	return resolvedConn{
		canonical: canonical, tokenID: srv.TokenID, secret: secret, insecure: insecure, pin: pin,
		client: newInitClient(canonical, srv.TokenID, secret, insecure, pin),
	}, nil
}

// establishConnection runs the Connection form, confirms overwrite of an
// already-configured server, then probes + authenticates — looping back to
// the form (values preserved) on any failure. The overwrite check happens
// before any server-side mutation (token creation), so declining never
// leaves an orphaned token on the Proxmox host.
func establishConnection(ctx context.Context, p prompter, cfg *config.Config, prev connInputs, confirmed map[string]bool) (resolvedConn, connInputs, error) {
	repinned := map[string]string{} // canonical URL -> re-pin accepted this run
	for {
		in, err := runConnectionForm(p, prev)
		if err != nil {
			return resolvedConn{}, in, err
		}
		prev = in

		canonical, _, cerr := config.CanonicalizeURLVerbose(in.urlRaw)
		if cerr != nil {
			p.Errf("%v\n", cerr)
			continue
		}

		if _, exists := cfg.Servers[canonical]; exists && !confirmed[canonical] {
			overwrite, aerr := tui.Confirm(fmt.Sprintf("Server %s is already configured. Overwrite?", canonical), false)
			if aerr != nil {
				return resolvedConn{}, in, aerr
			}
			if !overwrite {
				return resolvedConn{}, in, errOverwriteDeclined
			}
			confirmed[canonical] = true
		}

		insecure, ok := probeURL(ctx, p, canonical)
		if !ok {
			continue // probeURL already reported why
		}
		// Re-configuring a pinned server: authenticate every credentialed
		// connection against the stored pin (empty on a first-ever
		// connect), or a changed certificate the user explicitly re-pinned.
		pin, perr := resolveInitPin(ctx, p, cfg, canonical, insecure, repinned[canonical])
		if perr != nil {
			return resolvedConn{}, in, perr
		}
		if pin != storedPinFor(cfg, canonical) {
			repinned[canonical] = pin
		}

		var tokenID, secret string
		if in.tokenSource == "paste" {
			tokenID, secret = strings.TrimSpace(in.tokenID), in.tokenSecret
		} else {
			tokenID, secret, err = generateTokenFromInputs(ctx, p, canonical, insecure, pin, in)
			if err != nil {
				if errors.Is(err, pveclient.ErrTokenExists) {
					p.Errf("a token named %q already exists; choose another name\n", in.tokenName)
				} else {
					p.Errf("login/token creation failed: %v\n", err)
				}
				continue
			}
		}

		insecure, err = validateCredentials(ctx, p, canonical, tokenID, secret, insecure, pin)
		if err != nil {
			p.Errf("credential check failed: %v\n", err)
			continue
		}
		return resolvedConn{
			canonical: canonical, tokenID: tokenID, secret: secret, insecure: insecure, pin: pin,
			client: newInitClient(canonical, tokenID, secret, insecure, pin),
		}, in, nil
	}
}

func generateTokenFromInputs(ctx context.Context, p prompter, baseURL string, insecure bool, pin string, in connInputs) (string, string, error) {
	issuer, err := setup.Login(ctx, baseURL, insecure, pin, in.loginUser, in.password)
	if err != nil {
		return "", "", err
	}
	full, secret, err := issuer.Create(ctx, in.tokenName)
	if err != nil {
		return "", "", err
	}
	p.Printf("created API token %s (privilege separation off)\n", full)
	return full, secret, nil
}

// runConnectionForm renders the Connection page: URL + token source, with
// conditional login/paste field groups.
func runConnectionForm(p prompter, prev connInputs) (connInputs, error) {
	in := prev
	if in.tokenSource == "" {
		in.tokenSource = "generate"
	}
	if in.loginUser == "" {
		in.loginUser = "root@pam"
	}
	if in.tokenName == "" {
		in.tokenName = "pmox"
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Proxmox API URL").
				Description("IP, hostname, host:port, or a full URL").
				Value(&in.urlRaw).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("URL is required")
					}
					_, _, err := config.CanonicalizeURLVerbose(s)
					return err
				}),
			huh.NewSelect[string]().Title("API token").
				Options(
					huh.NewOption("Generate a new token (log in)", "generate"),
					huh.NewOption("Paste an existing token", "paste"),
				).Value(&in.tokenSource),
		),
		huh.NewGroup(
			huh.NewInput().Title("PVE login (user@realm)").Value(&in.loginUser).Validate(validateLoginUser),
			huh.NewInput().Title("Password").EchoMode(huh.EchoModePassword).Value(&in.password).Validate(validateNonEmpty),
			huh.NewInput().Title("New API token name").Value(&in.tokenName).Validate(validateTokenName),
		).WithHideFunc(func() bool { return in.tokenSource != "generate" }),
		huh.NewGroup(
			huh.NewInput().Title("API token ID (user@realm!name)").Value(&in.tokenID).Validate(validateTokenID),
			huh.NewInput().Title("API token secret").EchoMode(huh.EchoModePassword).Value(&in.tokenSecret).Validate(validateNonEmpty),
		).WithHideFunc(func() bool { return in.tokenSource != "paste" }),
	)
	if err := runForm(form); err != nil {
		return in, err
	}
	return in, nil
}

// collectDefaults discovers and selects node/template/storage/snippet/bridge
// (reusing the auto-selecting pickers). prev seeds each picker with the
// answer from a previous pass through this stage — earlier in this
// same wizard run, or (for 'pmox config edit') the value already
// configured on disk — so revisiting a choice starts from what's
// already set rather than a blank re-discovery.
func collectDefaults(ctx context.Context, p prompter, conn resolvedConn, prev defaultsAnswers, _ bool) (defaultsAnswers, error) {
	return discoverDefaults(ctx, p, conn.client, prev)
}

// collectAccess gathers the SSH key, default user, and node SSH creds.
// The default-user prompt prefers, in order: the answer from an earlier
// pass through this stage this session (haveAccess), the user already
// configured for this server on disk (reconfiguring), then "ubuntu".
func collectAccess(ctx context.Context, p prompter, cfg *config.Config, canonical string, prev accessAnswers, haveAccess bool) (accessAnswers, error) {
	sshKey, err := promptSSHKey(p, prev.sshKey)
	if err != nil {
		return accessAnswers{}, err
	}
	prevUser := prev.user
	if !haveAccess && prevUser == "" {
		prevUser = configuredUser(cfg, canonical)
	}
	user, err := promptDefaultUser(p, prevUser)
	if err != nil {
		return accessAnswers{}, err
	}
	nodeSSH, pw, kp, err := promptNodeSSH(ctx, p, canonical)
	if err != nil {
		return accessAnswers{}, err
	}
	return accessAnswers{sshKey: sshKey, user: user, nodeSSH: nodeSSH, sshPassword: pw, sshKeyPass: kp}, nil
}

// reviewRows renders the collected values (secrets masked) for the review
// screen.
func reviewRows(conn resolvedConn, defs defaultsAnswers, acc accessAnswers) []string {
	tls := "verified"
	if conn.insecure {
		tls = "insecure (cert not verified)"
	}
	return []string{
		"Server:    " + conn.canonical,
		"Token:     " + conn.tokenID,
		"Secret:    " + mask(conn.secret),
		"TLS:       " + tls,
		"Node:      " + defs.node,
		"Template:  " + defs.template,
		"Storage:   " + defs.storage,
		"Snippets:  " + defs.snippetStorage,
		"Bridge:    " + defs.bridge,
		"SSH key:   " + acc.sshKey,
		"User:      " + acc.user,
	}
}

func mask(s string) string {
	if s == "" {
		return "(none)"
	}
	return "••••••••"
}

// runReviewForm prints the summary and offers Confirm / edit a page.
func runReviewForm(p prompter, rows []string) (string, error) {
	p.Printf("\nReview configuration:\n")
	for _, r := range rows {
		p.Printf("  %s\n", r)
	}
	return tui.Select("Apply this configuration?", []huh.Option[string]{
		huh.NewOption("Confirm — write configuration", "confirm"),
		huh.NewOption("Edit connection (URL / token)", "connection"),
		huh.NewOption("Edit defaults (node / template / storage / bridge)", "defaults"),
		huh.NewOption("Edit access (SSH key / user / node SSH)", "access"),
		huh.NewOption("Cancel — quit without saving", "cancel"),
	})
}

// stageSubtitles describes what each wizard phase configures, printed under
// the tab bar so a bare phase name (e.g. "Defaults") isn't the only cue.
var stageSubtitles = map[string]string{
	"Connection": "Server URL and API credentials",
	"Defaults":   "Node · template · storage · snippets · bridge",
	"Access":     "SSH key, default user, node SSH login",
	"Review":     "Confirm and write configuration",
}

// printTabs renders the wizard phase tabs, plus a one-line subtitle for the
// active phase, above the current page.
func printTabs(p prompter, active string) {
	p.Printf("\n%s\n", tui.Steps(active, "Connection", "Defaults", "Access", "Review"))
	if sub, ok := stageSubtitles[active]; ok {
		p.Printf("%s\n", tui.Subtitle(sub))
	}
}

// runForm runs a huh form (themed), mapping a user abort to tui.ErrAborted.
func runForm(f *huh.Form) error {
	return tui.AbortErr(f.WithTheme(tui.Theme()).Run())
}

func validateNonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("value is required")
	}
	return nil
}

func validateLoginUser(s string) error {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "@") || strings.Contains(s, "!") {
		return fmt.Errorf("must be user@realm (e.g. root@pam)")
	}
	return nil
}

func validateTokenName(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("token name is required")
	}
	if strings.ContainsAny(s, " \t@!") {
		return fmt.Errorf("token name may not contain spaces, '@', or '!'")
	}
	return nil
}

func validateTokenID(s string) error {
	if !tokenIDRegex.MatchString(strings.TrimSpace(s)) {
		return fmt.Errorf("must be user@realm!tokenname")
	}
	return nil
}

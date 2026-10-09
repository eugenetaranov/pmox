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
	// generateKey defers creating the bootstrap key at sshKey until the
	// configuration is saved, so an abandoned wizard leaves no files.
	generateKey bool
}

// establishEditConnectionFn is a seam so 'config edit' tests needn't dial.
var establishEditConnectionFn = establishEditConnection

// runInteractiveForm is the TTY init experience: one persistent
// terminal app (see init_wizard.go) with a final review that can jump
// back and edit before anything is written.
func runInteractiveForm(ctx context.Context, p prompter) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return runWizard(ctx, p, newWizState(cfg), "pmox init", "connection")
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
		return fmt.Errorf("%w: no context named %q (see 'pmox context list')", exitcode.ErrNotFound, canonical)
	}
	conn, err := establishEditConnectionFn(ctx, p, cfg, canonical)
	if err != nil {
		return err
	}

	st := newWizState(cfg)
	st.conn = conn
	st.defs = defaultsAnswers{
		node: srv.Node, template: srv.Template, storage: srv.Storage,
		snippetStorage: srv.SnippetStorage, bridge: srv.Bridge,
	}
	st.acc = accessAnswers{sshKey: srv.SSHPubkey, user: srv.User, nodeSSH: srv.NodeSSH}
	if srv.NodeSSH != nil {
		switch srv.NodeSSH.Auth {
		case config.AuthPassword:
			if pw, perr := credstore.GetNodeSSHPassword(canonical); perr == nil {
				st.acc.sshPassword = pw
			}
		case config.AuthKey:
			if pp, perr := credstore.GetNodeSSHKeyPassphrase(canonical); perr == nil {
				st.acc.sshKeyPass = pp
			}
		}
	}
	st.haveDefs, st.haveAccess = true, true
	st.prevConn = connInputs{urlRaw: canonical, tokenSource: "paste", tokenID: conn.tokenID, tokenSecret: conn.secret}
	st.confirmed[canonical] = true
	return runWizard(ctx, p, st, "pmox config edit · "+contextName(cfg, canonical), "review")
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
		return resolvedConn{}, fmt.Errorf("%w: no context named %q (see 'pmox context list')", exitcode.ErrNotFound, canonical)
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

// withConnDefaults fills the Connection page's suggested defaults into
// any answer not given yet.
func withConnDefaults(in connInputs) connInputs {
	if in.tokenSource == "" {
		in.tokenSource = "generate"
	}
	if in.loginUser == "" {
		in.loginUser = "root@pam"
	}
	if in.tokenName == "" {
		in.tokenName = "pmox"
	}
	return in
}

// connectionForm builds the Connection page bound to in.
func connectionForm(in *connInputs) *huh.Form {
	return huh.NewForm(
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

// stageSubtitles describes what each wizard phase configures, printed under
// the tab bar so a bare phase name (e.g. "Defaults") isn't the only cue.
var stageSubtitles = map[string]string{
	"Connection": "Server URL and API credentials",
	"Defaults":   "Node · template · storage · snippets · bridge",
	"Access":     "SSH key, default user, node SSH login",
	"Review":     "Confirm and write configuration",
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

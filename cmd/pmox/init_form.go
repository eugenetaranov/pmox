package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
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
	establishConnectionFn = establishConnection
	collectDefaultsFn     = collectDefaults
	collectAccessFn       = collectAccess
	reviewFn              = runReviewForm
)

// runInteractiveForm is the TTY init experience: phased form pages with a
// final review that can jump back and edit before anything is written.
func runInteractiveForm(ctx context.Context, p prompter) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	var (
		conn       resolvedConn
		defs       defaultsAnswers
		acc        accessAnswers
		prevConn   connInputs
		haveDefs   bool
		haveAccess bool
		confirmed  = map[string]bool{} // canonical URLs whose overwrite was OK'd
	)

	stage := "connection"
	for {
		switch stage {
		case "connection":
			rc, in, cerr := establishConnectionFn(ctx, p, cfg, prevConn)
			if cerr != nil {
				return cerr
			}
			prevConn, conn = in, rc
			if _, exists := cfg.Servers[conn.canonical]; exists && !confirmed[conn.canonical] {
				ans, aerr := p.Prompt(fmt.Sprintf("Server %s is already configured. Overwrite? [y/N]: ", conn.canonical))
				if aerr != nil {
					return aerr
				}
				if strings.ToLower(strings.TrimSpace(ans)) != "y" {
					p.Printf("aborted; no changes\n")
					return nil
				}
				confirmed[conn.canonical] = true
			}
			stage = "defaults"
		case "defaults":
			d, derr := collectDefaultsFn(ctx, p, conn, defs, haveDefs)
			if derr != nil {
				return derr
			}
			defs, haveDefs = d, true
			stage = "access"
		case "access":
			a, aerr := collectAccessFn(ctx, p, conn.canonical, acc, haveAccess)
			if aerr != nil {
				return aerr
			}
			acc, haveAccess = a, true
			stage = "review"
		case "review":
			action, rerr := reviewFn(p, reviewRows(conn, defs, acc))
			if rerr != nil {
				return rerr
			}
			switch action {
			case "confirm":
				return persistServer(p, cfg, persistInput{
					canonical: conn.canonical, tokenID: conn.tokenID, secret: conn.secret, insecure: conn.insecure,
					node: defs.node, template: defs.template, storage: defs.storage,
					snippetStorage: defs.snippetStorage, bridge: defs.bridge,
					sshKey: acc.sshKey, user: acc.user, nodeSSH: acc.nodeSSH,
					sshPassword: acc.sshPassword, sshKeyPass: acc.sshKeyPass,
				})
			case "connection", "defaults", "access":
				stage = action
			default:
				return fmt.Errorf("%w: cancelled", exitcode.ErrUserInput)
			}
		}
	}
}

// establishConnection runs the Connection form, then probes + authenticates,
// looping back to the form (values preserved) on any failure.
func establishConnection(ctx context.Context, p prompter, _ *config.Config, prev connInputs) (resolvedConn, connInputs, error) {
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
		insecure, ok := probeURL(ctx, p, canonical)
		if !ok {
			continue // probeURL already reported why
		}

		var tokenID, secret string
		if in.tokenSource == "paste" {
			tokenID, secret = strings.TrimSpace(in.tokenID), in.tokenSecret
		} else {
			tokenID, secret, err = generateTokenFromInputs(ctx, p, canonical, insecure, in)
			if err != nil {
				if errors.Is(err, pveclient.ErrTokenExists) {
					p.Errf("a token named %q already exists; choose another name\n", in.tokenName)
				} else {
					p.Errf("login/token creation failed: %v\n", err)
				}
				continue
			}
		}

		insecure, err = validateCredentials(ctx, p, canonical, tokenID, secret, insecure)
		if err != nil {
			p.Errf("credential check failed: %v\n", err)
			continue
		}
		return resolvedConn{
			canonical: canonical, tokenID: tokenID, secret: secret, insecure: insecure,
			client: pveclient.New(canonical, tokenID, secret, insecure),
		}, in, nil
	}
}

func generateTokenFromInputs(ctx context.Context, p prompter, baseURL string, insecure bool, in connInputs) (string, string, error) {
	ticket, err := pveclient.Login(ctx, baseURL, insecure, in.loginUser, in.password)
	if err != nil {
		return "", "", err
	}
	full, secret, err := pveclient.CreateToken(ctx, baseURL, insecure, ticket, in.loginUser, in.tokenName)
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
// (reusing the auto-selecting pickers).
func collectDefaults(ctx context.Context, p prompter, conn resolvedConn, _ defaultsAnswers, _ bool) (defaultsAnswers, error) {
	node := pickNode(ctx, p, conn.client)
	if err := ctx.Err(); err != nil {
		return defaultsAnswers{}, fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
	}
	template := pickTemplate(ctx, p, conn.client, node)
	storage := pickStorage(ctx, p, conn.client, node)
	snippet := pickSnippetStorage(ctx, p, conn.client, node)
	bridge := pickBridge(ctx, p, conn.client, node)
	if err := ctx.Err(); err != nil {
		return defaultsAnswers{}, fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
	}
	return defaultsAnswers{node: node, template: template, storage: storage, snippetStorage: snippet, bridge: bridge}, nil
}

// collectAccess gathers the SSH key, default user, and node SSH creds.
func collectAccess(ctx context.Context, p prompter, canonical string, prev accessAnswers, _ bool) (accessAnswers, error) {
	sshKey, err := promptSSHKey(p, prev.sshKey)
	if err != nil {
		return accessAnswers{}, err
	}
	user, err := p.Prompt("Default user [ubuntu]: ")
	if err != nil {
		return accessAnswers{}, err
	}
	user = strings.TrimSpace(user)
	if user == "" {
		user = "ubuntu"
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
	})
}

// runForm runs a huh form, mapping a user abort to a clean SIGINT-based exit.
func runForm(f *huh.Form) error {
	if err := f.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
			return fmt.Errorf("%w: interrupted", exitcode.ErrUserInput)
		}
		return err
	}
	return nil
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

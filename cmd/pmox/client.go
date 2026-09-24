package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// session is what connect hands back: a PVE client whose API connection
// is TLS-pinned (for insecure servers), the resolved server record, and
// the loaded config it came from.
type session struct {
	Client   *pveclient.Client
	Resolved *server.Resolved
	Cfg      *config.Config
}

// connectOptions tunes connect for the rare command that deviates from
// the default behavior.
type connectOptions struct {
	// Stdin feeds the interactive server picker. nil never prompts.
	Stdin *os.File
}

// connect is the single factory every command uses to reach the PVE
// API. It loads config, resolves the target server (flag > env >
// single > picker), emits the --verbose server line, runs the TLS
// trust-on-first-use logic (see checkTLSPin), and returns a client
// whose every TLS handshake is checked against the pin — so the
// connection that carries the API token is the one that is
// authenticated, not a separate probe.
func connect(ctx context.Context, cmd *cobra.Command, opts connectOptions) (*session, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	resolved, err := server.Resolve(ctx, server.Options{
		Cfg:        cfg,
		Flag:       serverFlag,
		Context:    contextFlag,
		Env:        os.Getenv("PMOX_SERVER"),
		ContextEnv: os.Getenv("PMOX_CONTEXT"),
		Pick:       contextPicker(opts.Stdin),
	})
	if err != nil {
		return nil, err
	}
	if verbose {
		fmt.Fprintf(cmd.ErrOrStderr(), "using server %s (%s)\n", resolved.URL, resolved.Source)
	}
	pin, err := checkTLSPin(ctx, cmd.ErrOrStderr(), cfg, resolved.URL, resolved.Server, pinTOFU)
	if err != nil {
		return nil, err
	}
	return &session{Client: newAPIClient(resolved.URL, resolved.Server, resolved.Secret, pin), Resolved: resolved, Cfg: cfg}, nil
}

// buildClient is connect with the default options (interactive picker
// on stdin), returning just the client and resolved server — the shape
// most commands need.
func buildClient(ctx context.Context, cmd *cobra.Command) (*pveclient.Client, *server.Resolved, error) {
	s, err := connect(ctx, cmd, connectOptions{Stdin: os.Stdin})
	if err != nil {
		return nil, nil, err
	}
	return s.Client, s.Resolved, nil
}

// contextPicker returns the TUI-backed server.Options.Pick, or nil (no
// picker; the resolver reports the ambiguity instead) unless stdin and
// stderr are both terminals and input is not disabled.
func contextPicker(stdin *os.File) func(string, []server.Choice) (string, error) {
	if stdin == nil || !term.IsTerminal(int(stdin.Fd())) || !tui.StderrIsTerminal() || tui.NoInput() {
		return nil
	}
	return func(title string, choices []server.Choice) (string, error) {
		opts := make([]huh.Option[string], 0, len(choices))
		for _, c := range choices {
			opts = append(opts, huh.NewOption(c.Label, c.Value))
		}
		return tui.Select(title, opts)
	}
}

// newAPIClient builds the PVE client for srv. pin, when non-empty, is
// enforced on every TLS handshake of the API connection.
func newAPIClient(url string, srv *config.Server, secret, pin string) *pveclient.Client {
	return pveclient.NewWithOptions(url, srv.TokenID, secret, srv.Insecure, pveclient.Options{PinSHA256: pin})
}

// storedPin returns the TLS pin to enforce for srv without probing or
// saving anything: the configured pin for an insecure server, "" else.
func storedPin(srv *config.Server) string {
	if !srv.Insecure {
		return ""
	}
	return srv.TLSPinSHA256
}

// fetchCertFingerprint is overridable in tests.
var fetchCertFingerprint = pveclient.FetchCertFingerprint

// pinMode selects how checkTLSPin treats an insecure server.
type pinMode int

const (
	// pinTOFU pins and saves the certificate on first connect and owns
	// the insecure-TLS messaging. Used by every normal command.
	pinTOFU pinMode = iota
	// pinReadOnly never saves a pin and prints nothing; a stored pin is
	// still enforced. Used when scanning servers the user did not
	// explicitly target (cleanup).
	pinReadOnly
)

// checkTLSPin implements TLS trust-on-first-use for insecure servers and
// returns the fingerprint the API client must enforce ("" = none). In
// pinTOFU mode it owns all insecure-TLS messaging:
//
//   - First connect (no pin): warn once that the transport is unverified,
//     pin the leaf cert's SHA-256 in config, and note that pmox will warn
//     if it later changes.
//   - Later connect, cert matches the pin: SILENT. The transport is now
//     authenticated against the pinned certificate (TOFU, like SSH), so
//     there is nothing to warn about on every run. `-v` prints a note.
//   - Later connect, cert differs: hard error (possible MITM).
//
// It is a no-op for verified (secure) servers. A fingerprint-fetch error
// never blocks: the stored pin (if any) is still enforced by the client,
// and the real request surfaces genuine network problems itself.
func checkTLSPin(ctx context.Context, w io.Writer, cfg *config.Config, url string, srv *config.Server, mode pinMode) (string, error) {
	if !srv.Insecure {
		return "", nil
	}
	fp, err := fetchCertFingerprint(ctx, url)
	if err != nil {
		// Can't fetch the cert to pin/verify this run. With a stored pin
		// the client still enforces it; without one the transport is
		// unverified, so warn (once).
		if srv.TLSPinSHA256 == "" && mode == pinTOFU {
			warnInsecureTLS(w, url, true)
		}
		return srv.TLSPinSHA256, nil
	}
	switch {
	case srv.TLSPinSHA256 == "":
		// First insecure connect: genuinely unverified this time. Pin the
		// client to what we just saw either way, so the cert cannot be
		// swapped between the probe and the API call.
		if mode == pinReadOnly {
			return fp, nil
		}
		warnInsecureTLS(w, url, true)
		srv.TLSPinSHA256 = fp
		if saveErr := cfg.Save(); saveErr != nil {
			fmt.Fprintf(w, "WARNING: pinned the TLS certificate but could not save it to config: %v\n", saveErr)
			return fp, nil
		}
		fmt.Fprintf(w, "Pinned TLS certificate for %s (sha256:%s). pmox will warn if it changes.\n", url, fp)
		return fp, nil
	case pveclient.NormalizePin(srv.TLSPinSHA256) != pveclient.NormalizePin(fp):
		return "", fmt.Errorf("%w: TLS certificate for %s CHANGED — pinned sha256:%s, now sha256:%s. This may be a man-in-the-middle attack. If you deliberately replaced the certificate, clear tls_pin_sha256 for this server in the pmox config (or re-run 'pmox init')",
			pveclient.ErrTLSVerificationFailed, url, srv.TLSPinSHA256, fp)
	default:
		// Pin matches — authenticated against the pinned cert; stay quiet.
		if verbose && mode == pinTOFU {
			fmt.Fprintf(w, "TLS: certificate for %s matches the pinned fingerprint\n", url)
		}
		return srv.TLSPinSHA256, nil
	}
}

// insecureTLSWarned guards the one-shot insecure-TLS warning within a
// process. The warning fires only on a first (unpinned) insecure connect
// or when the cert can't be fetched to pin/verify — once a cert is pinned
// and matches, checkTLSPin stays silent, so this is not printed on every
// run.
var insecureTLSWarned bool

// warnInsecureTLS prints a stderr warning the first time a command in
// this process talks to a server whose certificate is not verified.
// Unlike the configure-time acceptance, this fires on every command so
// the unauthenticated-transport risk stays visible in logs.
func warnInsecureTLS(w io.Writer, serverURL string, insecure bool) {
	if !insecure || insecureTLSWarned {
		return
	}
	insecureTLSWarned = true
	fmt.Fprintf(w, "WARNING: TLS certificate verification is disabled for %s (configured insecure) — API traffic, including the API token, is not authenticated against a verified certificate.\n", serverURL)
}

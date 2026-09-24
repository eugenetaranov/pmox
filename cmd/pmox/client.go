package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
)

// buildClient is the single entry point every command uses to reach the
// PVE API. It loads config, resolves the target server (flag > env >
// single > picker), emits the --verbose server line, warns once when the
// resolved server has TLS verification disabled, and returns a ready
// client alongside the resolved record. Centralizing it keeps server
// resolution, the verbose log, and the insecure-TLS warning identical
// across launch/clone/delete/list/info/start/stop/shell/exec/cp/sync/mount.
func buildClient(ctx context.Context, cmd *cobra.Command) (*pveclient.Client, *server.Resolved, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	resolved, err := server.Resolve(ctx, server.Options{
		Cfg:        cfg,
		Flag:       serverFlag,
		Context:    contextFlag,
		Env:        os.Getenv("PMOX_SERVER"),
		ContextEnv: os.Getenv("PMOX_CONTEXT"),
		Stdin:      os.Stdin,
	})
	if err != nil {
		return nil, nil, err
	}
	if verbose {
		fmt.Fprintf(cmd.ErrOrStderr(), "using server %s (%s)\n", resolved.URL, resolved.Source)
	}
	srv := resolved.Server
	if err := checkTLSPin(ctx, cmd.ErrOrStderr(), cfg, resolved); err != nil {
		return nil, nil, err
	}
	return pveclient.New(resolved.URL, srv.TokenID, resolved.Secret, srv.Insecure), resolved, nil
}

// fetchCertFingerprint is overridable in tests.
var fetchCertFingerprint = pveclient.FetchCertFingerprint

// checkTLSPin implements TLS trust-on-first-use for insecure servers and
// owns all insecure-TLS messaging:
//
//   - First connect (no pin): warn once that the transport is unverified,
//     pin the leaf cert's SHA-256 in config, and note that pmox will warn
//     if it later changes.
//   - Later connect, cert matches the pin: SILENT. The transport is now
//     authenticated against the pinned certificate (TOFU, like SSH), so
//     there is nothing to warn about on every run. `-v` prints a note.
//   - Later connect, cert differs: hard error (possible MITM).
//
// It is a no-op for verified (secure) servers and never blocks on a
// fingerprint-fetch error — the subsequent API call surfaces genuine
// network problems itself.
func checkTLSPin(ctx context.Context, w io.Writer, cfg *config.Config, resolved *server.Resolved) error {
	srv := resolved.Server
	if !srv.Insecure {
		return nil
	}
	fp, err := fetchCertFingerprint(ctx, resolved.URL)
	if err != nil {
		// Can't fetch the cert to pin/verify this run — the transport is
		// unverified, so warn (once), then let the real request report any
		// network error.
		warnInsecureTLS(w, resolved.URL, true)
		return nil
	}
	switch {
	case srv.TLSPinSHA256 == "":
		// First insecure connect: genuinely unverified this time.
		warnInsecureTLS(w, resolved.URL, true)
		srv.TLSPinSHA256 = fp
		if saveErr := cfg.Save(); saveErr != nil {
			fmt.Fprintf(w, "WARNING: pinned the TLS certificate but could not save it to config: %v\n", saveErr)
			return nil
		}
		fmt.Fprintf(w, "Pinned TLS certificate for %s (sha256:%s). pmox will warn if it changes.\n", resolved.URL, fp)
		return nil
	case srv.TLSPinSHA256 != fp:
		return fmt.Errorf("%w: TLS certificate for %s CHANGED — pinned sha256:%s, now sha256:%s. This may be a man-in-the-middle attack. If you deliberately replaced the certificate, clear tls_pin_sha256 for this server in the pmox config (or re-run 'pmox init')",
			pveclient.ErrTLSVerificationFailed, resolved.URL, srv.TLSPinSHA256, fp)
	default:
		// Pin matches — authenticated against the pinned cert; stay quiet.
		if verbose {
			fmt.Fprintf(w, "TLS: certificate for %s matches the pinned fingerprint\n", resolved.URL)
		}
		return nil
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

// buildDeleteClient is a thin adapter kept for the many single-target
// commands (delete/list/info/start/stop) that only need the client.
func buildDeleteClient(ctx context.Context, cmd *cobra.Command) (*pveclient.Client, error) {
	client, _, err := buildClient(ctx, cmd)
	return client, err
}

// buildSSHClient is a thin adapter for the SSH-adjacent commands
// (shell/exec/cp/sync/mount) that also need the resolved server record
// for its User / SSHPubkey fields.
func buildSSHClient(ctx context.Context, cmd *cobra.Command) (*pveclient.Client, *config.Server, error) {
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return nil, nil, err
	}
	return client, resolved.Server, nil
}

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
		Cfg:    cfg,
		Flag:   serverFlag,
		Env:    os.Getenv("PMOX_SERVER"),
		Stdin:  os.Stdin,
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	})
	if err != nil {
		return nil, nil, err
	}
	if verbose {
		fmt.Fprintf(cmd.ErrOrStderr(), "using server %s (%s)\n", resolved.URL, resolved.Source)
	}
	srv := resolved.Server
	warnInsecureTLS(cmd.ErrOrStderr(), resolved.URL, srv.Insecure)
	if err := checkTLSPin(ctx, cmd.ErrOrStderr(), cfg, resolved); err != nil {
		return nil, nil, err
	}
	return pveclient.New(resolved.URL, srv.TokenID, resolved.Secret, srv.Insecure), resolved, nil
}

// fetchCertFingerprint is overridable in tests.
var fetchCertFingerprint = pveclient.FetchCertFingerprint

// checkTLSPin implements TLS trust-on-first-use for insecure servers:
// it pins the server's leaf-cert SHA-256 in config on first insecure
// connect, and on later connects fails if the presented cert no longer
// matches the pin (a possible MITM). It is a no-op for verified (secure)
// servers, and it never blocks on a fingerprint-fetch error — the
// subsequent API call will surface a genuine network problem itself.
func checkTLSPin(ctx context.Context, w io.Writer, cfg *config.Config, resolved *server.Resolved) error {
	srv := resolved.Server
	if !srv.Insecure {
		return nil
	}
	fp, err := fetchCertFingerprint(ctx, resolved.URL)
	if err != nil {
		return nil // don't block; the real request will report the network error
	}
	switch {
	case srv.TLSPinSHA256 == "":
		srv.TLSPinSHA256 = fp
		if saveErr := cfg.Save(); saveErr != nil {
			fmt.Fprintf(w, "WARNING: pinned the TLS certificate but could not save it to config: %v\n", saveErr)
			return nil
		}
		fmt.Fprintf(w, "Pinned TLS certificate for %s (sha256:%s). pmox will warn if it changes.\n", resolved.URL, fp)
		return nil
	case srv.TLSPinSHA256 != fp:
		return fmt.Errorf("%w: TLS certificate for %s CHANGED — pinned sha256:%s, now sha256:%s. This may be a man-in-the-middle attack. If you deliberately replaced the certificate, clear tls_pin_sha256 for this server in the pmox config (or re-run 'pmox configure')",
			pveclient.ErrTLSVerificationFailed, resolved.URL, srv.TLSPinSHA256, fp)
	default:
		return nil // pin matches
	}
}

// insecureTLSWarned guards the one-shot insecure-TLS warning for this
// process. Each command is its own process, so once-per-process means
// the operator sees the warning on every invocation that targets an
// insecure server — not just at `pmox configure` time.
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

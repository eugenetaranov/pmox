package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/doctor"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvessh"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/snippet"
	"github.com/eugenetaranov/pmox/internal/sshkey"
	"github.com/eugenetaranov/pmox/internal/tui"
)

type doctorFlags struct {
	strict  bool
	timeout time.Duration
	// yes applies any offered fix without asking (env: PMOX_ASSUME_YES).
	// Works without a terminal too — the one thing that still requires
	// a real TTY regardless of yes is a fix marked RequiresTTY (it
	// shells into its own picker flow).
	yes bool
	// noFix suppresses the fix-offer step entirely: report only, the
	// pre-v0.17 default behavior. The escape hatch for a human who
	// wants to look before deciding.
	noFix bool
	// fix is accepted for compatibility with v0.16 scripts
	// ('pmox doctor --fix -y') but is now a no-op — doctor offers a fix
	// whenever one exists, unconditionally. Passing it prints a
	// one-line deprecation note; it does not change behavior.
	fix bool
}

// doctorError carries the exit code of the worst failing check so
// exitcode.From returns it without sentinel matching.
type doctorError struct{ code int }

func (e *doctorError) Error() string { return "doctor found blocking issues" }
func (e *doctorError) ExitCode() int { return e.code }
func (e *doctorError) SelfReported() {}

func newDoctorCmd() *cobra.Command {
	f := &doctorFlags{}
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate configuration and Proxmox connectivity",
		Long: `Run checks that validate your pmox configuration, Proxmox API and
SSH connectivity, storage and template readiness, and local tooling,
then report whether the tool is ready to launch VMs.

When a check fails or warns and pmox knows how to repair it, doctor
offers to fix it right there — confirmed one at a time — then
automatically re-runs every check afterward so you see confirmation
the problem is actually gone, instead of being told to re-run
'pmox doctor' yourself. Pass -y (or PMOX_ASSUME_YES=1) to apply fixes
without asking; that works without a terminal too (e.g. in CI), except
for a fix that itself needs one (like rebuilding a template, which
reuses 'pmox create-template's own image/storage pickers) — that one
is only ever offered on a real terminal. Pass --no-fix to see only the
report. Without a terminal and without -y, nothing is ever offered or
changed — same as a plain report.

Aside from an explicitly accepted fix, doctor never changes anything
on its own: no prompts, no pinned host keys, no modified config. Each
check reports pass/warn/fail with a remediation hint. The process
exits non-zero if any check fails (or, with --strict, if any warning
is present), using the same exit-code taxonomy as other commands so CI
can gate on it. Use --output json for machine-readable output (never
offers a fix); --verbose to also list passing checks.

Examples:
  pmox doctor
  pmox doctor --strict
  pmox doctor -y
  pmox doctor --no-fix
  pmox doctor --output json | jq '.checks[] | select(.status=="fail")'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd, f)
		},
	}
	cmd.Flags().BoolVar(&f.strict, "strict", false, "treat warnings as failures (exit non-zero on any warning)")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 20*time.Second, "overall time budget for all checks")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "apply an offered fix without asking (env: PMOX_ASSUME_YES); works without a terminal")
	cmd.Flags().BoolVar(&f.noFix, "no-fix", false, "never offer to fix anything; report only")
	cmd.Flags().BoolVar(&f.fix, "fix", false, "deprecated: fixes are now offered by default; kept for compatibility")
	_ = cmd.Flags().MarkHidden("fix")
	return cmd
}

// doctorDeps are the non-PVE-client probes, injected so tests can drive
// executeDoctor deterministically.
type doctorDeps struct {
	lookPath          func(string) (string, error)
	knownHostHasEntry func(host string) (bool, error)
	sshDial           func(ctx context.Context) error
}

func runDoctor(cmd *cobra.Command, f *doctorFlags) error {
	f.yes = f.yes || envBool("PMOX_ASSUME_YES")
	if cmd.Flags().Changed("fix") {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: --fix is deprecated and has no effect — pmox doctor now offers fixes automatically (see --no-fix)")
	}

	// parent is unbounded except by the process's own signal handling
	// (Ctrl-C) — used for fix execution and for each fresh diagnostic
	// pass's own deadline below. It must never itself carry f.timeout:
	// a fix like rebuilding a template can legitimately take much
	// longer than the 20s default meant for a read-only check pass (the
	// bug this comment is here to prevent: reusing one fixed-deadline
	// ctx across the whole flow made the fix — and then the re-verify
	// pass after it — fail with "context deadline exceeded" no matter
	// how long the fix actually needed).
	parent := cmd.Context()
	ctx, cancel := context.WithTimeout(parent, f.timeout)
	defer cancel()

	cl := &doctor.Checklist{}

	// --- Config layer (no network) ---
	cfg, err := config.Load()
	if err != nil {
		cl.Fail("config.file", "config", "no usable pmox config found", "run 'pmox init' to create one", exitcode.ExitUserError)
		// Nothing resolved yet, so nothing could ever be fixable here —
		// rerun is nil, renderAndFinish skips straight to the verdict.
		return renderAndFinish(parent, cmd, f, cl.Finalize("", "", f.strict), nil)
	}
	cl.Pass("config.file", "config", "config loaded")
	if err := cfg.Validate(); err != nil {
		cl.Warn("config.valid", "config", "config has values pmox cannot act on: "+err.Error(), "run 'pmox init' to reconfigure the affected server")
	}

	resolved, err := server.Resolve(ctx, server.Options{
		Cfg:        cfg,
		Flag:       serverFlag,
		Context:    contextFlag,
		Env:        os.Getenv("PMOX_SERVER"),
		ContextEnv: os.Getenv("PMOX_CONTEXT"),
		Pick:       nil, // doctor never prompts for a context: no interactive picker
	})
	if err != nil {
		cl.Fail("config.server", "config", "no server resolved: "+err.Error(), "run 'pmox init', or pass --server / set PMOX_SERVER", exitcode.ExitUserError)
		return renderAndFinish(parent, cmd, f, cl.Finalize("", "", f.strict), nil)
	}
	cl.Pass("config.server", "config", "server resolves: "+resolved.URL)

	deps := doctorDeps{
		lookPath: exec.LookPath,
		knownHostHasEntry: func(host string) (bool, error) {
			return knownHostsHasEntry(host)
		},
		sshDial: func(ctx context.Context) error { return doctorSSHDial(ctx, resolved) },
	}

	// doctor never pins on its own (that's a mutation), but a pin that
	// is already stored is enforced on the API connection like every
	// other command.
	client := newAPIClient(resolved.URL, resolved.Server, resolved.Secret, storedPin(resolved.Server))
	rerun := func() doctor.Report {
		// A fresh deadline relative to now, not the original (long
		// since expired after a slow fix) one above.
		rctx, rcancel := context.WithTimeout(parent, f.timeout)
		defer rcancel()
		cl2 := &doctor.Checklist{}
		executeDoctor(rctx, cl2, client, resolved, deps, f.strict, cmd, cfg)
		return cl2.Finalize(resolved.URL, resolved.Source, f.strict)
	}
	executeDoctor(ctx, cl, client, resolved, deps, f.strict, cmd, cfg)
	return renderAndFinish(parent, cmd, f, cl.Finalize(resolved.URL, resolved.Source, f.strict), rerun)
}

// renderAndFinish renders report, then — unless suppressed, running as
// --output json, or nothing resolved far enough to retry (rerun == nil)
// — offers to fix anything it can. fixCtx (not bounded by --timeout,
// which is meant for a read-only check pass, not a fix that can
// legitimately run for minutes) is what a fix actually runs under; each
// call to rerun creates its own fresh, --timeout-bounded context for
// that diagnostic pass. When at least one fix actually runs, it re-runs
// the entire check pass via rerun and renders that report too, so "is
// it fixed?" is answered by doctor itself rather than by asking the
// user to run it again. The returned error carries the exit code of
// whichever report is final.
func renderAndFinish(fixCtx context.Context, cmd *cobra.Command, f *doctorFlags, report doctor.Report, rerun func() doctor.Report) error {
	if err := renderDoctorReport(cmd, report); err != nil {
		return err
	}

	if outputMode == "json" || f.noFix || rerun == nil || !reportHasFixes(report) {
		return doctorVerdict(report)
	}

	confirmer, cerr := doctorFixConfirmer(cmd, f.yes)
	if cerr != nil {
		// No terminal and no -y: still just a report. Offering by
		// default must never turn a plain, non-interactive
		// 'pmox doctor' into a new hard failure it didn't have before.
		fmt.Fprintf(cmd.ErrOrStderr(), "\n%d fixable issue(s) above — run on a terminal, or with -y, to fix them.\n", countFixable(report))
		return doctorVerdict(report)
	}

	ranAny, err := offerDoctorFixes(fixCtx, cmd, report, confirmer)
	if err != nil {
		return err
	}
	if !ranAny {
		return doctorVerdict(report)
	}

	fmt.Fprintln(cmd.ErrOrStderr(), "\nRe-running doctor to confirm...")
	report2 := rerun()
	if err := renderDoctorReport(cmd, report2); err != nil {
		return err
	}
	return doctorVerdict(report2)
}

// renderDoctorReport prints report in the current --output mode.
func renderDoctorReport(cmd *cobra.Command, report doctor.Report) error {
	if outputMode == "json" {
		return printJSON(cmd.OutOrStdout(), report)
	}
	color := !noColor && tui.StderrIsTerminal() && os.Getenv("NO_COLOR") == ""
	doctor.RenderText(cmd.OutOrStdout(), report, verbose, color)
	return nil
}

// doctorVerdict turns a finalized report into the command's return
// value: nil when ready, else an error carrying its exit code.
func doctorVerdict(report doctor.Report) error {
	if !report.Ready {
		return &doctorError{code: report.ExitCode}
	}
	return nil
}

// doctorFixConfirmer builds the confirmer a fix is asked through,
// mirroring pmox delete's own yes/TTY/refuse pattern: -y (or
// PMOX_ASSUME_YES) always approves, a TTY gets a real y/N prompt on
// stderr, and anything else is refused — a fix mutates the
// cluster/config, so it never runs non-interactively without an
// explicit opt-in.
func doctorFixConfirmer(cmd *cobra.Command, yes bool) (tui.Confirmer, error) {
	if yes {
		return tui.AlwaysConfirmer{}, nil
	}
	if tui.StdinIsTerminal() {
		return tui.NewTTYConfirmer(os.Stdin, cmd.ErrOrStderr()), nil
	}
	return nil, fmt.Errorf("%w: needs -y (or PMOX_ASSUME_YES=1) when stdin is not a TTY", exitcode.ErrUserInput)
}

// reportHasFixes reports whether any check in the report carries an
// attached doctor.Fix.
func reportHasFixes(report doctor.Report) bool {
	return countFixable(report) > 0
}

// countFixable counts checks in the report that carry an attached fix.
func countFixable(report doctor.Report) int {
	n := 0
	for _, c := range report.Checks {
		if c.Fixable() {
			n++
		}
	}
	return n
}

// offerDoctorFixes walks the report for checks that carry an attached
// doctor.Fix and asks confirmer before running each one — except a fix
// marked FixRequiresTTY, which is skipped without asking when stdin
// isn't a real terminal (confirmer may be an unconditional
// AlwaysConfirmer from -y, which such a fix must never trust alone). It
// returns whether at least one fix actually ran, so the caller knows a
// re-verify pass is worth it. A fix's own failure is reported and does
// not stop the rest of the pass.
func offerDoctorFixes(ctx context.Context, cmd *cobra.Command, report doctor.Report, confirmer tui.Confirmer) (ranAny bool, err error) {
	var fixable []doctor.Check
	for _, c := range report.Checks {
		if c.Fixable() {
			fixable = append(fixable, c)
		}
	}
	if len(fixable) == 0 {
		return false, nil
	}

	stderr := cmd.ErrOrStderr()
	fmt.Fprintln(stderr)
	for _, c := range fixable {
		if c.FixRequiresTTY() && !tui.StdinIsTerminal() {
			fmt.Fprintf(stderr, "skipped: %s (needs an interactive terminal)\n", c.ID)
			continue
		}
		ok, cerr := confirmer.Confirm(ctx, c.FixPrompt()+" [y/N]: ")
		if cerr != nil {
			return ranAny, fmt.Errorf("confirmation: %w", cerr)
		}
		if !ok {
			fmt.Fprintf(stderr, "skipped: %s\n", c.ID)
			continue
		}
		fmt.Fprintf(stderr, "--- running fix: %s ---\n", c.ID)
		if err := c.RunFix(ctx); err != nil {
			fmt.Fprintf(stderr, "fix failed (%s): %v\n", c.ID, err)
			continue
		}
		fmt.Fprintf(stderr, "fixed: %s\n", c.ID)
		ranAny = true
	}
	return ranAny, nil
}

// executeDoctor runs the network/SSH/storage/template/tooling checks
// against an already-resolved server. Extracted so tests can drive it
// with a fake PVE client and stubbed deps.
func executeDoctor(ctx context.Context, cl *doctor.Checklist, client *pveclient.Client, resolved *server.Resolved, deps doctorDeps, strict bool, cmd *cobra.Command, cfg *config.Config) {
	srv := resolved.Server

	// --- Config values that gate later checks ---
	if srv.TokenID == "" {
		cl.Fail("config.token_id", "config", "API token_id not set", "run 'pmox init'", exitcode.ExitUserError)
	} else {
		cl.Pass("config.token_id", "config", "token_id set: "+srv.TokenID)
	}
	if resolved.Secret == "" {
		cl.Fail("config.api_secret", "config", "API token secret missing from keychain", "run 'pmox init' to re-enter it", exitcode.ExitUserError)
	} else {
		cl.Pass("config.api_secret", "config", "API secret present in keychain")
	}
	doctorConfigDefaults(cl, srv)
	doctorCloudInit(cl, resolved.URL)
	doctorCloudInitKey(cl, resolved.URL, srv.SSHPubkey)
	doctorSecretBackend(cl)
	doctorTLSMode(ctx, cl, resolved, strict)

	// --- Local tooling (independent of network) ---
	doctorTooling(cl, deps)
	doctorTack(cl, deps)

	// --- API reachability + auth ---
	authOK := doctorAPIReach(ctx, cl, client)

	if authOK {
		doctorPrivileges(ctx, cl, client, srv)
		nodeOK := doctorNode(ctx, cl, client, srv.Node)
		if nodeOK {
			doctorBridge(ctx, cl, client, srv.Node, srv.Bridge)
			doctorStorage(ctx, cl, client, srv.Node, srv.Storage, srv.SnippetStorage)
			doctorTemplate(ctx, cmd, cl, cfg, client, resolved)
		}
	}

	// --- Node SSH (independent of API auth) ---
	doctorNodeSSH(ctx, cl, resolved, deps)
}

// doctorConfigDefaults checks the launch defaults. All three are hard
// requirements of resolveLaunchOptions/resolveVMSpec (empty node,
// template, or storage returns a wrapped ErrNotFound before any PVE
// call) — a bare 'pmox launch <name>' cannot succeed without them, so
// each is a Fail, not a Warn: "pmox is ready" must mean launch actually
// works, not "mostly configured."
func doctorConfigDefaults(cl *doctor.Checklist, srv *config.Server) {
	if srv.Node != "" {
		cl.Pass("config.default_node", "config", "default node: "+srv.Node)
	} else {
		cl.Fail("config.default_node", "config", "no default node configured", "run 'pmox init' (launch needs a node)", exitcode.ExitNotFound)
	}
	if srv.Template != "" {
		cl.Pass("config.default_template", "config", "default template: "+srv.Template)
	} else {
		cl.Fail("config.default_template", "config", "no default template configured", "run 'pmox create-template', then set it via 'pmox init'", exitcode.ExitNotFound)
	}
	if srv.Storage != "" {
		cl.Pass("config.default_storage", "config", "default storage: "+srv.Storage)
	} else {
		cl.Fail("config.default_storage", "config", "no default storage configured", "run 'pmox init' (launch needs a disk storage)", exitcode.ExitNotFound)
	}
}

// doctorCloudInit checks the per-server cloud-init file. resolveVMSpec
// resolves the identical config.CloudInitPath, and launch.Run's phase 0
// reads it before any PVE call, aborting launch on either error — so
// both branches block launch and are Fails.
func doctorCloudInit(cl *doctor.Checklist, serverURL string) {
	path, err := config.CloudInitPath(serverURL)
	if err != nil {
		cl.Fail("config.cloud_init", "config", "could not resolve cloud-init path: "+err.Error(), "run 'pmox init --regen-cloud-init'", exitcode.ExitUserError)
		return
	}
	if _, err := os.Stat(path); err != nil {
		cl.Fail("config.cloud_init", "config", "cloud-init file missing: "+path, "run 'pmox init --regen-cloud-init'", exitcode.ExitUserError)
		return
	}
	cl.Pass("config.cloud_init", "config", "cloud-init file present")
}

// doctorSecretBackend reports which secret store is active and warns when
// the plaintext file fallback is in use.
func doctorSecretBackend(cl *doctor.Checklist) {
	switch credstore.ActiveBackend() {
	case credstore.BackendKeychain:
		cl.Pass("config.secret_store", "config", "secrets stored in the OS keychain")
	default:
		cl.Warn("config.secret_store", "config",
			"OS keychain unavailable — secrets are in the plaintext file fallback (~/.config/pmox/secrets.yaml, 0600)",
			"install a keychain (gnome-keyring/KWallet) for encrypted-at-rest storage, or accept the file fallback for headless use")
	}
}

// doctorCloudInitKey warns when the configured ssh_pubkey isn't the key
// authorized by the per-server cloud-init file — the drift that surfaces
// only as a Permission denied (publickey) at connect time.
func doctorCloudInitKey(cl *doctor.Checklist, serverURL, sshPubkeyPath string) {
	if sshPubkeyPath == "" {
		return // no configured key to compare; config.default_* covers this
	}
	path, err := config.CloudInitPath(serverURL)
	if err != nil {
		return
	}
	pub, err := os.ReadFile(sshkey.ExpandHome(sshPubkeyPath))
	if err != nil {
		cl.Warn("config.cloud_init_key", "config", "cannot read ssh_pubkey "+sshPubkeyPath+": "+err.Error(), "fix 'ssh_pubkey' in config or re-run 'pmox init'")
		return
	}
	authorized, hasAny, err := config.CloudInitAuthorizesKey(path, string(pub))
	if err != nil || !hasAny {
		return // no cloud-init keys to compare against (covered by config.cloud_init)
	}
	if authorized {
		cl.Pass("config.cloud_init_key", "config", "cloud-init authorizes the configured ssh_pubkey")
		return
	}
	cl.Warn("config.cloud_init_key", "config",
		"cloud-init authorizes a different key than ssh_pubkey — new VMs won't accept your configured key",
		"run 'pmox init --regen-cloud-init' (then relaunch existing VMs), or point ssh_pubkey at the key the VMs already have")
}

func doctorTLSMode(ctx context.Context, cl *doctor.Checklist, resolved *server.Resolved, strict bool) {
	srv := resolved.Server
	if !srv.Insecure {
		cl.Pass("config.tls_mode", "config", "TLS certificate verification enabled")
		return
	}
	// Deliberately-configured insecure TLS is the norm for homelab
	// self-signed certs — it is informational, not a warning, unless the
	// operator asked for strict checking.
	if strict {
		cl.Warn("config.tls_mode", "config", "TLS certificate verification disabled (insecure: true)", "install a trusted cert on the PVE node and set insecure: false, or drop --strict")
	} else {
		cl.Info("config.tls_mode", "config", "TLS certificate verification disabled (insecure: true, expected for self-signed homelab certs)")
	}

	// Verify the pinned certificate, read-only. doctor never pins (that's
	// a mutation) — it only reports whether the presented cert still
	// matches what was pinned on first connect.
	if srv.TLSPinSHA256 == "" {
		cl.Info("config.tls_pin", "config", "no TLS certificate pinned yet (pmox pins it on the first insecure connect)")
		return
	}
	fp, err := fetchCertFingerprint(ctx, resolved.URL)
	if err != nil {
		cl.Info("config.tls_pin", "config", "could not fetch the certificate to compare against the pin: "+err.Error())
		return
	}
	if pveclient.NormalizePin(fp) == pveclient.NormalizePin(srv.TLSPinSHA256) {
		cl.Pass("config.tls_pin", "config", "TLS certificate matches the pinned fingerprint")
		return
	}
	cl.Fail("config.tls_pin", "config",
		"TLS certificate CHANGED from the pinned fingerprint (possible MITM)",
		"if you deliberately replaced the cert, re-run 'pmox init' interactively to review and re-pin it, or clear tls_pin_sha256 in config",
		exitcode.ExitNetworkError)
}

func doctorTooling(cl *doctor.Checklist, deps doctorDeps) {
	tools := []struct {
		bin, uses string
	}{
		{"ssh", "shell/exec/cp/sync/mount"},
		{"scp", "cp"},
		{"rsync", "sync/mount"},
	}
	for _, t := range tools {
		if _, err := deps.lookPath(t.bin); err != nil {
			cl.Warn("tooling."+t.bin, "tooling", t.bin+" not found on PATH", "install "+t.bin+" (needed by "+t.uses+")")
		} else {
			cl.Pass("tooling."+t.bin, "tooling", t.bin+" available")
		}
	}
}

// doctorTack reports whether tack (for 'pmox apply' and '--tack') is
// available and a default playbook is resolvable. tack is optional, so
// absence is a warning, not a failure.
func doctorTack(cl *doctor.Checklist, deps doctorDeps) {
	if _, err := deps.lookPath("tack"); err != nil {
		cl.Warn("tooling.tack", "tooling", "tack not found on PATH — 'pmox apply' and '--tack' are unavailable",
			"install tack from https://github.com/tackhq/tack (optional)")
		return
	}
	dir, err := tackDir()
	if err != nil {
		cl.Warn("tooling.tack", "tooling", "tack available but the tack config dir cannot be resolved: "+err.Error(),
			"set HOME or XDG_CONFIG_HOME")
		return
	}
	pb := filepath.Join(dir, "playbook.yaml")
	if _, err := os.Stat(pb); err != nil {
		cl.Warn("tooling.tack", "tooling", "tack available but no default playbook at "+pb,
			"run 'pmox apply --init' to scaffold one, or use a profile/--playbook")
		return
	}
	cl.Pass("tooling.tack", "tooling", "tack available; default playbook present")
}

// doctorAPIReach probes GET /version, splitting reachability from auth.
// Returns true when the API is reachable AND authenticated.
func doctorAPIReach(ctx context.Context, cl *doctor.Checklist, client *pveclient.Client) bool {
	version, err := client.GetVersion(ctx)
	switch {
	case err == nil:
		cl.Pass("api.reachable", "api", "reached API, PVE "+version)
		cl.Pass("api.auth", "api", "API token accepted")
		return true
	case errors.Is(err, pveclient.ErrUnauthorized):
		cl.Pass("api.reachable", "api", "reached API")
		cl.Fail("api.auth", "api", "API token rejected (401/403)", "check token_id and secret with 'pmox init', and the token's privileges", exitcode.ExitUnauthorized)
		return false
	case errors.Is(err, pveclient.ErrTLSVerificationFailed):
		cl.Fail("api.reachable", "api", "TLS verification failed: "+err.Error(), "install a trusted cert, or set insecure: true if this is a self-signed homelab cert", exitcode.ExitNetworkError)
		return false
	case errors.Is(err, pveclient.ErrNetwork), errors.Is(err, context.DeadlineExceeded):
		cl.Fail("api.reachable", "api", "cannot reach the API: "+err.Error(), "check the host is up and port 8006 is reachable (firewall?)", exitcode.From(err))
		return false
	default:
		cl.Fail("api.reachable", "api", "API error: "+err.Error(), "", exitcode.From(err))
		return false
	}
}

func doctorPrivileges(ctx context.Context, cl *doctor.Checklist, client *pveclient.Client, srv *config.Server) {
	perms, err := client.GetPermissions(ctx)
	if err != nil {
		cl.Warn("api.privileges", "api", "could not read token privileges: "+err.Error(), "grant Sys.Audit so doctor can introspect /access/permissions")
		return
	}
	required := doctor.RequiredPrivileges(srv.Storage, srv.SnippetStorage)
	missing := doctor.MissingPrivileges(perms, required)
	if len(missing) == 0 {
		cl.Pass("api.privileges", "api", "token has all required privileges")
		return
	}
	var names []string
	for _, m := range missing {
		names = append(names, m.Priv+" on "+m.Path)
	}
	cl.Fail("api.privileges", "api",
		"token missing "+fmt.Sprint(len(missing))+" privilege(s): "+strings.Join(names, ", "),
		"grant them, e.g. add to a role and: pveum acl modify / -token '"+srv.TokenID+"' -role <role>",
		exitcode.ExitUnauthorized)
}

func doctorNode(ctx context.Context, cl *doctor.Checklist, client *pveclient.Client, node string) bool {
	if node == "" {
		return false // already warned in config.default_node
	}
	resources, err := client.ClusterResources(ctx, "node")
	if err != nil {
		cl.Fail("api.node", "api", "could not list cluster nodes: "+err.Error(), "", exitcode.From(err))
		return false
	}
	for _, r := range resources {
		if r.Node == node {
			if r.Status == "online" {
				cl.Pass("api.node", "api", "node '"+node+"' is online")
				return true
			}
			cl.Fail("api.node", "api", "node '"+node+"' is "+r.Status+" (not online)", "start the node or point 'node' at an online one", exitcode.ExitGeneric)
			return false
		}
	}
	cl.Fail("api.node", "api", "configured node '"+node+"' not found in cluster", "fix the 'node' value in config (see 'pmox init')", exitcode.ExitNotFound)
	return false
}

func doctorBridge(ctx context.Context, cl *doctor.Checklist, client *pveclient.Client, node, bridge string) {
	if bridge == "" {
		return
	}
	bridges, err := client.ListBridges(ctx, node)
	if err != nil {
		cl.Warn("api.bridge", "api", "could not list bridges: "+err.Error(), "")
		return
	}
	for _, b := range bridges {
		if b.Iface == bridge {
			cl.Pass("api.bridge", "api", "bridge '"+bridge+"' exists on "+node)
			return
		}
	}
	cl.Warn("api.bridge", "api", "configured bridge '"+bridge+"' not found on node '"+node+"'", "create the bridge on the node, or fix 'bridge' in config")
}

func doctorStorage(ctx context.Context, cl *doctor.Checklist, client *pveclient.Client, node, diskStorage, snippetStorage string) {
	storages, err := client.ListStorage(ctx, node)
	if err != nil {
		cl.Fail("storage.disk", "storage", "could not list storage: "+err.Error(), "", exitcode.From(err))
		return
	}

	if diskStorage != "" {
		var found *pveclient.Storage
		for i := range storages {
			if storages[i].Storage == diskStorage {
				found = &storages[i]
				break
			}
		}
		switch {
		case found == nil:
			cl.Fail("storage.disk", "storage", "disk storage '"+diskStorage+"' not found on node '"+node+"'", "fix 'storage' in config (see 'pmox init')", exitcode.ExitNotFound)
		case !found.SupportsVMDisks():
			cl.Fail("storage.disk", "storage", "storage '"+diskStorage+"' has no 'images' content (can't hold VM disks)", "pick a storage with 'images' content, or add it: pvesm set "+diskStorage+" --content images,...", exitcode.ExitGeneric)
		default:
			cl.Pass("storage.disk", "storage", "disk storage '"+diskStorage+"' supports VM disks")
		}
	}

	if snippetStorage != "" {
		if err := snippet.ValidateStorage(ctx, client, node, snippetStorage); err != nil {
			hint := "pvesm set " + snippetStorage + " --content images,iso,vztmpl,rootdir,snippets"
			if alt := firstSnippetStorage(storages); alt != "" && alt != snippetStorage {
				hint += ", or use --snippet-storage " + alt + " (already snippet-capable)"
			}
			cl.Fail("storage.snippets", "storage", "snippet storage '"+snippetStorage+"' does not support 'snippets'", hint, exitcode.From(err))
		} else {
			cl.Pass("storage.snippets", "storage", "snippet storage '"+snippetStorage+"' supports 'snippets'")
		}
	}
}

func firstSnippetStorage(storages []pveclient.Storage) string {
	if m := pveclient.FilterStorage(storages, pveclient.Storage.SupportsSnippets); len(m) > 0 {
		return m[0].Storage
	}
	return ""
}

func doctorTemplate(ctx context.Context, cmd *cobra.Command, cl *doctor.Checklist, cfg *config.Config, client *pveclient.Client, resolved *server.Resolved) {
	template := resolved.Server.Template
	if template == "" {
		return // already warned in config.default_template
	}
	node := resolved.Server.Node

	// The configured template reference is unusable in all three
	// branches below (not found by name/id, or gone from the node) —
	// rebuilding a fresh one and setting it as the new default resolves
	// any of them the same way.
	rebuildPrompt := "Run 'pmox create-template' now to build a fresh template " +
		"(downloads an Ubuntu image and bakes a VM — can take several " +
		"minutes), and set it as the new default?"
	rebuildFix := doctor.Fix{
		Prompt: rebuildPrompt,
		Run:    func(ctx context.Context) error { return fixRebuildTemplate(ctx, cmd, cfg, resolved, client) },
		// Shells into 'pmox create-template's own image/storage
		// pickers, exactly like create-template itself requires a real
		// TTY for — must never run via -y alone non-interactively, or
		// it would hang or misbehave.
		RequiresTTY: true,
	}

	id, _, err := resolveTemplate(ctx, client, node, template)
	if err != nil {
		cl.Fail("template.resolves", "template", "template '"+template+"' not found on node '"+node+"'", "run 'pmox create-template', or fix 'template' in config", exitcode.From(err))
		cl.WithFix(rebuildFix)
		return
	}

	// resolveTemplate trusts a numeric id without checking it exists;
	// GetConfig confirms existence (404 if gone) and lets us verify the
	// template flag and guest-agent setting in one call.
	tcfg, err := client.GetConfig(ctx, node, id)
	if err != nil {
		if errors.Is(err, pveclient.ErrNotFound) {
			cl.Fail("template.resolves", "template", "template '"+template+"' (vmid "+fmt.Sprint(id)+") not found on node '"+node+"'", "run 'pmox create-template', or fix 'template' in config", exitcode.ExitNotFound)
			cl.WithFix(rebuildFix)
			return
		}
		cl.Fail("template.resolves", "template", "could not read template config: "+err.Error(), "run 'pmox create-template', or fix 'template' in config", exitcode.From(err))
		cl.WithFix(rebuildFix)
		return
	}
	if tcfg["template"] == "1" {
		cl.Pass("template.resolves", "template", "template resolves (vmid "+fmt.Sprint(id)+")")
	} else {
		cl.Warn("template.resolves", "template", "vmid "+fmt.Sprint(id)+" exists but is not marked as a template", "convert it (qm template "+fmt.Sprint(id)+") or point 'template' at a real template")
		cl.WithFix(doctor.Fix{
			Prompt: fmt.Sprintf("Convert vmid %d to a template now (qm template %d)?", id, id),
			Run:    func(ctx context.Context) error { return client.ConvertToTemplate(ctx, node, id) },
		})
	}

	if agentEnabled(tcfg["agent"]) {
		cl.Pass("template.agent", "template", "guest agent enabled on template (agent: 1)")
	} else {
		// Without agent: 1, PVE never creates the virtio-serial channel,
		// so the guest-agent IP query fails forever: launch blocks for
		// the full --wait budget (default 3m) and then fails, leaving
		// an orphaned running VM behind. Blocking, so Fail — matches
		// the exit code vmwait.WaitForIP's real timeout wraps.
		cl.Fail("template.agent", "template", "template has no 'agent: 1' — launch will time out waiting for an IP", "qm set "+fmt.Sprint(id)+" --agent 1, and ensure qemu-guest-agent is installed inside the image", exitcode.ExitTimeout)
		cl.WithFix(doctor.Fix{
			Prompt: fmt.Sprintf("Set agent: 1 on vmid %d now?", id),
			Run: func(ctx context.Context) error {
				return client.SetConfig(ctx, node, id, map[string]string{"agent": "1"})
			},
		})
	}
}

// fixRebuildTemplate runs the same interactive image/storage pickers as
// 'pmox create-template', reusing the client/node/bridge doctor already
// resolved, then records the freshly built template as the server's new
// default (config.Server.Template) and saves it — closing the loop so
// 'pmox doctor' and 'pmox launch' both work again without a manual edit.
func fixRebuildTemplate(ctx context.Context, cmd *cobra.Command, cfg *config.Config, resolved *server.Resolved, client *pveclient.Client) error {
	srv := resolved.Server
	if srv.Node == "" {
		return fmt.Errorf("no node configured; run 'pmox init'")
	}
	bridge := firstNonEmpty(srv.Bridge, "vmbr0")
	upload, closeUpload := newSnippetUploader(resolved)
	defer closeUpload()

	r, err := templateRunFn(ctx, buildTemplateOptions(cmd, client, srv.Node, bridge, 10*time.Minute, upload))
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "created template %s (vmid=%d)\n", r.Name, r.VMID)

	srv.Template = strconv.Itoa(r.VMID)
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("template created (vmid=%d) but could not save it as the default: %w", r.VMID, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "set vmid %d as the default template for %s\n", r.VMID, resolved.URL)
	return nil
}

// agentEnabled reports whether a PVE `agent` config value turns the
// guest agent on. The value is like "1" or "1,fstrim_cloned_disks=1".
func agentEnabled(v string) bool {
	for _, part := range strings.Split(v, ",") {
		if strings.TrimSpace(part) == "1" || strings.TrimSpace(part) == "enabled=1" {
			return true
		}
	}
	return false
}

func doctorNodeSSH(ctx context.Context, cl *doctor.Checklist, resolved *server.Resolved, deps doctorDeps) {
	if resolved.NodeSSHErr != nil {
		cl.Fail("ssh.configured", "ssh", "node SSH misconfigured: "+resolved.NodeSSHErr.Error(), "run 'pmox init' to reconfigure node SSH", exitcode.ExitUserError)
		return
	}
	if !resolved.HasNodeSSH() {
		// runLaunch calls resolved.RequireNodeSSH("launch") right after
		// buildClient and hard-fails when node SSH isn't configured —
		// this blocks a bare 'pmox launch' before any PVE call, so it's
		// a Fail with the same ErrUserInput RequireNodeSSH itself wraps.
		cl.Fail("ssh.configured", "ssh", "node SSH not configured", "run 'pmox init' to add it — launch/clone/create-template upload cloud-init over SSH (shell/exec/list/info/delete don't need it)", exitcode.ExitUserError)
		return
	}
	cl.Pass("ssh.configured", "ssh", "node SSH configured (user "+resolved.NodeSSHUser+", "+string(resolved.NodeSSHAuth)+" auth)")

	host, err := pvessh.HostFromURL(resolved.URL)
	if err != nil {
		// The identical call resolved.NodeSSHConfig makes at launch's
		// snippet-upload phase (dialPvessh) would fail the same way,
		// after the VM is already cloned/tagged/resized.
		cl.Fail("ssh.known_host", "ssh", "could not derive SSH host: "+err.Error(), "", exitcode.From(err))
		return
	}
	pinned, err := deps.knownHostHasEntry(host)
	if err != nil {
		// Same file the runtime hostKeyCallback reads at that same
		// launch phase — a read/parse error there fails identically.
		cl.Fail("ssh.known_host", "ssh", "could not read pmox known_hosts: "+err.Error(), "", exitcode.From(err))
		return
	}
	if !pinned {
		cl.Fail("ssh.known_host", "ssh", "no pinned host key for "+host, "doctor won't prompt — run 'pmox init' (or 'pmox create-template') once to pin the node host key", exitcode.ExitUserError)
		return
	}
	cl.Pass("ssh.known_host", "ssh", "node host key is pinned")

	if err := deps.sshDial(ctx); err != nil {
		cl.Fail("ssh.dial", "ssh", "SSH+SFTP to "+host+" failed: "+err.Error(), "check node SSH credentials and that sshd is reachable on the node", exitcode.From(err))
		return
	}
	cl.Pass("ssh.dial", "ssh", "SSH+SFTP dial ok")
}

// knownHostsHasEntry reports whether the pmox-managed known_hosts file
// has an entry whose host token matches host (with or without :22).
func knownHostsHasEntry(host string) (bool, error) {
	path, err := pvessh.KnownHostsPath()
	if err != nil {
		return false, err
	}
	return pvessh.KnownHostsHas(path, host)
}

// doctorSSHDial opens a strict (never-prompting) SSH+SFTP session to the
// node and closes it, returning any dial error. It uses the pmox-managed
// known_hosts and never falls back to insecure or interactive pinning.
func doctorSSHDial(ctx context.Context, resolved *server.Resolved) error {
	cfg, err := resolved.NodeSSHConfig(false)
	if err != nil {
		return err
	}
	c, err := pvessh.Dial(ctx, cfg)
	if err != nil {
		return err
	}
	return c.Close()
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	"github.com/eugenetaranov/pmox/internal/tui"
)

type doctorFlags struct {
	strict  bool
	timeout time.Duration
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
		Long: `Run read-only checks that validate your pmox configuration, Proxmox
API and SSH connectivity, storage and template readiness, and local
tooling, then report whether the tool is ready to launch VMs.

doctor never changes anything: it does not prompt, pin host keys, or
modify config. Each check reports pass/warn/fail with a remediation
hint. The process exits non-zero if any check fails (or, with --strict,
if any warning is present), using the same exit-code taxonomy as other
commands so CI can gate on it. Use --output json for machine-readable
output; --verbose to also list passing checks.

Examples:
  pmox doctor
  pmox doctor --strict
  pmox doctor --output json | jq '.checks[] | select(.status=="fail")'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd, f)
		},
	}
	cmd.Flags().BoolVar(&f.strict, "strict", false, "treat warnings as failures (exit non-zero on any warning)")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 20*time.Second, "overall time budget for all checks")
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
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, f.timeout)
	defer cancel()

	cl := &doctor.Checklist{}

	// --- Config layer (no network) ---
	cfg, err := config.Load()
	if err != nil {
		cl.Fail("config.file", "config", "no usable pmox config found", "run 'pmox init' to create one", exitcode.ExitUserError)
		return finishDoctor(cmd, f, cl, "", "")
	}
	cl.Pass("config.file", "config", "config loaded")

	resolved, err := server.Resolve(ctx, server.Options{
		Cfg:        cfg,
		Flag:       serverFlag,
		Context:    contextFlag,
		Env:        os.Getenv("PMOX_SERVER"),
		ContextEnv: os.Getenv("PMOX_CONTEXT"),
		Stdin:      os.Stdin,
	})
	if err != nil {
		cl.Fail("config.server", "config", "no server resolved: "+err.Error(), "run 'pmox init', or pass --server / set PMOX_SERVER", exitcode.ExitUserError)
		return finishDoctor(cmd, f, cl, "", "")
	}
	cl.Pass("config.server", "config", "server resolves: "+resolved.URL)

	deps := doctorDeps{
		lookPath: exec.LookPath,
		knownHostHasEntry: func(host string) (bool, error) {
			return knownHostsHasEntry(host)
		},
		sshDial: func(ctx context.Context) error { return doctorSSHDial(ctx, resolved) },
	}

	client := pveclient.New(resolved.URL, resolved.Server.TokenID, resolved.Secret, resolved.Server.Insecure)
	executeDoctor(ctx, cl, client, resolved, deps, f.strict)
	return finishDoctor(cmd, f, cl, resolved.URL, resolved.Source)
}

// finishDoctor computes the report, renders it, and returns an error
// carrying the right exit code when not ready.
func finishDoctor(cmd *cobra.Command, f *doctorFlags, cl *doctor.Checklist, serverURL, source string) error {
	report := cl.Finalize(serverURL, source, f.strict)

	if outputMode == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		color := !noColor && tui.StderrIsTerminal() && os.Getenv("NO_COLOR") == ""
		doctor.RenderText(cmd.OutOrStdout(), report, verbose, color)
	}

	if !report.Ready {
		return &doctorError{code: report.ExitCode}
	}
	return nil
}

// executeDoctor runs the network/SSH/storage/template/tooling checks
// against an already-resolved server. Extracted so tests can drive it
// with a fake PVE client and stubbed deps.
func executeDoctor(ctx context.Context, cl *doctor.Checklist, client *pveclient.Client, resolved *server.Resolved, deps doctorDeps, strict bool) {
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
			doctorTemplate(ctx, cl, client, srv.Node, srv.Template)
		}
	}

	// --- Node SSH (independent of API auth) ---
	doctorNodeSSH(ctx, cl, resolved, deps)
}

func doctorConfigDefaults(cl *doctor.Checklist, srv *config.Server) {
	if srv.Node != "" {
		cl.Pass("config.default_node", "config", "default node: "+srv.Node)
	} else {
		cl.Warn("config.default_node", "config", "no default node configured", "run 'pmox init' (launch needs a node)")
	}
	if srv.Template != "" {
		cl.Pass("config.default_template", "config", "default template: "+srv.Template)
	} else {
		cl.Warn("config.default_template", "config", "no default template configured", "run 'pmox create-template', then set it via 'pmox init'")
	}
	if srv.Storage != "" {
		cl.Pass("config.default_storage", "config", "default storage: "+srv.Storage)
	} else {
		cl.Warn("config.default_storage", "config", "no default storage configured", "run 'pmox init' (launch needs a disk storage)")
	}
}

func doctorCloudInit(cl *doctor.Checklist, serverURL string) {
	path, err := config.CloudInitPath(serverURL)
	if err != nil {
		cl.Warn("config.cloud_init", "config", "could not resolve cloud-init path: "+err.Error(), "")
		return
	}
	if _, err := os.Stat(path); err != nil {
		cl.Warn("config.cloud_init", "config", "cloud-init file missing: "+path, "run 'pmox init --regen-cloud-init'")
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
	pub, err := os.ReadFile(expandHome(sshPubkeyPath))
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
	if fp == srv.TLSPinSHA256 {
		cl.Pass("config.tls_pin", "config", "TLS certificate matches the pinned fingerprint")
		return
	}
	cl.Fail("config.tls_pin", "config",
		"TLS certificate CHANGED from the pinned fingerprint (possible MITM)",
		"if you deliberately replaced the cert, clear tls_pin_sha256 in config (or re-run 'pmox init')",
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
	pb := filepath.Join(tackDir(), "playbook.yaml")
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
		cl.Fail("api.reachable", "api", "cannot reach the API: "+err.Error(), "check the host is up and port 8006 is reachable (firewall?)", errExitCode(err))
		return false
	default:
		cl.Fail("api.reachable", "api", "API error: "+err.Error(), "", errExitCode(err))
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
		cl.Fail("api.node", "api", "could not list cluster nodes: "+err.Error(), "", errExitCode(err))
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
		cl.Fail("storage.disk", "storage", "could not list storage: "+err.Error(), "", errExitCode(err))
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
			cl.Fail("storage.snippets", "storage", "snippet storage '"+snippetStorage+"' does not support 'snippets'", hint, errExitCode(err))
		} else {
			cl.Pass("storage.snippets", "storage", "snippet storage '"+snippetStorage+"' supports 'snippets'")
		}
	}
}

func firstSnippetStorage(storages []pveclient.Storage) string {
	for _, s := range storages {
		for _, c := range strings.Split(s.Content, ",") {
			if strings.TrimSpace(c) == "snippets" {
				return s.Storage
			}
		}
	}
	return ""
}

func doctorTemplate(ctx context.Context, cl *doctor.Checklist, client *pveclient.Client, node, template string) {
	if template == "" {
		return // already warned in config.default_template
	}
	id, _, err := resolveTemplate(ctx, client, node, template)
	if err != nil {
		cl.Fail("template.resolves", "template", "template '"+template+"' not found on node '"+node+"'", "run 'pmox create-template', or fix 'template' in config", errExitCode(err))
		return
	}

	// resolveTemplate trusts a numeric id without checking it exists;
	// GetConfig confirms existence (404 if gone) and lets us verify the
	// template flag and guest-agent setting in one call.
	cfg, err := client.GetConfig(ctx, node, id)
	if err != nil {
		if errors.Is(err, pveclient.ErrNotFound) {
			cl.Fail("template.resolves", "template", "template '"+template+"' (vmid "+fmt.Sprint(id)+") not found on node '"+node+"'", "run 'pmox create-template', or fix 'template' in config", exitcode.ExitNotFound)
			return
		}
		cl.Warn("template.resolves", "template", "could not read template config: "+err.Error(), "")
		return
	}
	if cfg["template"] == "1" {
		cl.Pass("template.resolves", "template", "template resolves (vmid "+fmt.Sprint(id)+")")
	} else {
		cl.Warn("template.resolves", "template", "vmid "+fmt.Sprint(id)+" exists but is not marked as a template", "convert it (qm template "+fmt.Sprint(id)+") or point 'template' at a real template")
	}

	if agentEnabled(cfg["agent"]) {
		cl.Pass("template.agent", "template", "guest agent enabled on template (agent: 1)")
	} else {
		cl.Warn("template.agent", "template", "template has no 'agent: 1' — launch may never get an IP", "qm set "+fmt.Sprint(id)+" --agent 1, and ensure qemu-guest-agent is installed inside the image")
	}
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
	if !resolved.HasNodeSSH() {
		cl.Warn("ssh.configured", "ssh", "node SSH not configured", "run 'pmox init' to add it — launch/clone/create-template upload cloud-init over SSH (shell/exec/list/info/delete don't need it)")
		return
	}
	cl.Pass("ssh.configured", "ssh", "node SSH configured (user "+resolved.NodeSSHUser+", "+string(resolved.NodeSSHAuth)+" auth)")

	host, err := pvessh.HostFromURL(resolved.URL)
	if err != nil {
		cl.Warn("ssh.known_host", "ssh", "could not derive SSH host: "+err.Error(), "")
		return
	}
	pinned, err := deps.knownHostHasEntry(host)
	if err != nil {
		cl.Warn("ssh.known_host", "ssh", "could not read pmox known_hosts: "+err.Error(), "")
		return
	}
	if !pinned {
		cl.Fail("ssh.known_host", "ssh", "no pinned host key for "+host, "doctor won't prompt — run 'pmox init' (or 'pmox create-template') once to pin the node host key", exitcode.ExitUserError)
		return
	}
	cl.Pass("ssh.known_host", "ssh", "node host key is pinned")

	if err := deps.sshDial(ctx); err != nil {
		cl.Fail("ssh.dial", "ssh", "SSH+SFTP to "+host+" failed: "+err.Error(), "check node SSH credentials and that sshd is reachable on the node", errExitCode(err))
		return
	}
	cl.Pass("ssh.dial", "ssh", "SSH+SFTP dial ok")
}

// errExitCode maps a probe error to the closest exit-code category.
func errExitCode(err error) int {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return exitcode.ExitTimeout
	case errors.Is(err, pveclient.ErrUnauthorized):
		return exitcode.ExitUnauthorized
	case errors.Is(err, pveclient.ErrTLSVerificationFailed), errors.Is(err, pveclient.ErrNetwork):
		return exitcode.ExitNetworkError
	case errors.Is(err, pveclient.ErrTimeout):
		return exitcode.ExitTimeout
	case errors.Is(err, pveclient.ErrNotFound):
		return exitcode.ExitNotFound
	case errors.Is(err, pveclient.ErrAPIError):
		return exitcode.ExitAPIError
	default:
		return exitcode.ExitGeneric
	}
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

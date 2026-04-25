package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/muesli/cancelreader"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/credstore"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvessh"
	"github.com/eugenetaranov/pmox/internal/tui"
)

var (
	configureList         bool
	configureRemove       string
	configureRegenCloudCI bool
)

var configureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Configure a Proxmox VE server for pmox",
	Long: `Interactively configure credentials and defaults for a Proxmox VE server.

Walks through API URL, token, credential validation against /version, and
auto-discovery of node, template, storage, snippet storage, and bridge,
then collects SSH credentials for the PVE node (used by 'pmox create-template'
to upload cloud-init snippets via SFTP) and validates them with a live
handshake. Snippet storage is picked separately from VM disk storage —
if no storage on the cluster has the 'snippets' content type, configure
offers to enable it on an existing directory-backed storage.
Secrets are stored in the system keychain; everything else is written to
$XDG_CONFIG_HOME/pmox/config.yaml (or ~/.config/pmox/config.yaml).

Prompts:
  Proxmox API URL   base URL of the PVE host, e.g. https://192.168.0.185:8006
                    (the web UI URL with '#v1:...' also works — the path is
                    stripped automatically)
  API token ID      in the form 'user@realm!tokenname', e.g. 'root@pam!pmox'
                    or 'pmox@pve!mytoken'. Create one in the PVE web UI under
                    Datacenter → Permissions → API Tokens → Add.
  API token secret  the UUID shown once when the token is created.
  Node SSH user     Linux user pmox SSHs into on the PVE node (default: root).
  Node SSH auth     'p' for password or 'k' for a private key file.
  Node SSH secret   password or key passphrase, stored in the OS keyring.
                    First-time connections prompt to pin the host key into
                    ~/.config/pmox/known_hosts (bypass with --ssh-insecure).`,
	RunE: runConfigure,
}

func init() {
	configureCmd.Flags().BoolVar(&configureList, "list", false, "List configured server URLs")
	configureCmd.Flags().StringVar(&configureRemove, "remove", "", "Remove a configured server by URL")
	configureCmd.Flags().BoolVar(&configureRegenCloudCI, "regen-cloud-init", false, "Rewrite the per-server cloud-init template with stored user+pubkey")
	rootCmd.AddCommand(configureCmd)
}

var tokenIDRegex = regexp.MustCompile(`^[^@!]+@[^@!]+![^@!]+$`)

// prompter abstracts terminal I/O so tests can drive configure with a fake.
type prompter interface {
	Prompt(msg string) (string, error)
	PromptSecret(msg string) (string, error)
	Printf(format string, args ...interface{})
	Errf(format string, args ...interface{})
}

type stdPrompter struct {
	in     *bufio.Reader
	cancel cancelreader.CancelReader
	out    io.Writer
	err    io.Writer
}

func newStdPrompter(ctx context.Context) *stdPrompter {
	cr, err := cancelreader.NewReader(os.Stdin)
	if err != nil {
		// Fall back to uncancellable bufio on platforms cancelreader can't handle.
		return &stdPrompter{
			in:  bufio.NewReader(os.Stdin),
			out: os.Stdout,
			err: os.Stderr,
		}
	}
	p := &stdPrompter{
		in:     bufio.NewReader(cr),
		cancel: cr,
		out:    os.Stdout,
		err:    os.Stderr,
	}
	// Cancel the underlying read as soon as the context is done (e.g. on SIGINT).
	go func() {
		<-ctx.Done()
		cr.Cancel()
	}()
	return p
}

func (p *stdPrompter) Prompt(msg string) (string, error) {
	fmt.Fprint(p.out, msg)
	line, err := p.in.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err != nil {
		// cancelreader.ErrCanceled → user hit Ctrl+C; io.EOF with no data same.
		if errors.Is(err, cancelreader.ErrCanceled) || (errors.Is(err, io.EOF) && line == "") {
			return "", fmt.Errorf("%w: interrupted", exitcode.ErrUserInput)
		}
		if errors.Is(err, io.EOF) {
			return line, nil
		}
		return "", err
	}
	return line, nil
}

func (p *stdPrompter) PromptSecret(msg string) (string, error) {
	fmt.Fprint(p.out, msg)
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(p.out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (p *stdPrompter) Printf(format string, args ...interface{}) {
	fmt.Fprintf(p.out, format, args...)
}

func (p *stdPrompter) Errf(format string, args ...interface{}) {
	fmt.Fprintf(p.err, format, args...)
}

func runConfigure(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	nexcl := 0
	if configureList {
		nexcl++
	}
	if configureRemove != "" {
		nexcl++
	}
	if configureRegenCloudCI {
		nexcl++
	}
	if nexcl > 1 {
		return fmt.Errorf("--list, --remove, and --regen-cloud-init are mutually exclusive")
	}
	if configureList {
		return runList(newStdPrompter(ctx))
	}
	if configureRemove != "" {
		return runRemove(newStdPrompter(ctx), configureRemove)
	}
	if configureRegenCloudCI {
		return runRegenCloudInit(ctx, newStdPrompter(ctx))
	}
	return runInteractive(ctx, newStdPrompter(ctx))
}

func runList(p prompter) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	urls := cfg.ServerURLs()
	if len(urls) == 0 {
		p.Printf("no servers configured\n")
		return nil
	}
	for _, u := range urls {
		p.Printf("%s\n", u)
	}
	return nil
}

func runRemove(p prompter, rawURL string) error {
	canonical, err := config.CanonicalizeURL(rawURL)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.RemoveServer(canonical) {
		return fmt.Errorf("%w: server %s is not configured", credstore.ErrNotFound, canonical)
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	if err := credstore.Remove(canonical); err != nil && !errors.Is(err, credstore.ErrNotFound) {
		return err
	}
	if err := credstore.RemoveNodeSSHPassword(canonical); err != nil && !errors.Is(err, credstore.ErrNotFound) {
		return err
	}
	if err := credstore.RemoveNodeSSHKeyPassphrase(canonical); err != nil && !errors.Is(err, credstore.ErrNotFound) {
		return err
	}
	p.Printf("removed %s\n", canonical)
	return nil
}

func runInteractive(ctx context.Context, p prompter) error {
	// Step 1: URL
	canonical, err := promptCanonicalURL(p)
	if err != nil {
		return err
	}

	// Step 2: check overwrite
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if _, exists := cfg.Servers[canonical]; exists {
		ans, err := p.Prompt(fmt.Sprintf("Server %s is already configured. Overwrite? [y/N]: ", canonical))
		if err != nil {
			return err
		}
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			p.Printf("aborted; no changes\n")
			return nil
		}
	}

	// Step 3: token ID
	tokenID, err := promptTokenID(p)
	if err != nil {
		return err
	}

	// Step 4: token secret
	secret, err := promptSecret(p)
	if err != nil {
		return err
	}

	// Step 5: validate credentials (strict TLS, fall back to insecure)
	insecure, err := validateCredentials(ctx, p, canonical, tokenID, secret)
	if err != nil {
		return err
	}

	// Steps 7–10: auto-discovery pickers
	client := pveclient.New(canonical, tokenID, secret, insecure)
	node := pickNode(ctx, p, client)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
	}
	template := pickTemplate(ctx, p, client, node)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
	}
	storage := pickStorage(ctx, p, client, node)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
	}
	snippetStorage := pickSnippetStorage(ctx, p, client, node)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
	}
	bridge := pickBridge(ctx, p, client, node)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
	}

	// Step 11: SSH key
	sshKey, err := promptSSHKey(p)
	if err != nil {
		return err
	}

	// Step 12: default user
	user, err := p.Prompt("Default user [ubuntu]: ")
	if err != nil {
		return err
	}
	user = strings.TrimSpace(user)
	if user == "" {
		user = "ubuntu"
	}

	// Step 12.5: node SSH credentials for snippet upload.
	nodeSSH, sshPassword, sshKeyPass, err := promptNodeSSH(ctx, p, canonical)
	if err != nil {
		return err
	}

	// Step 13: save
	srv := &config.Server{
		TokenID:        tokenID,
		Node:           node,
		Template:       template,
		Storage:        storage,
		SnippetStorage: snippetStorage,
		Bridge:         bridge,
		SSHPubkey:      sshKey,
		User:           user,
		Insecure:       insecure,
		NodeSSH:        nodeSSH,
	}
	cfg.AddServer(canonical, srv)
	if err := cfg.Save(); err != nil {
		return err
	}
	if err := credstore.Set(canonical, secret); err != nil {
		// Best-effort revert: delete the server we just added and re-save.
		cfg.RemoveServer(canonical)
		_ = cfg.Save()
		return fmt.Errorf("save secret to keychain: %w", err)
	}
	if sshPassword != "" {
		if err := credstore.SetNodeSSHPassword(canonical, sshPassword); err != nil {
			return fmt.Errorf("save node ssh password to keychain: %w", err)
		}
	} else {
		_ = credstore.RemoveNodeSSHPassword(canonical)
	}
	if sshKeyPass != "" {
		if err := credstore.SetNodeSSHKeyPassphrase(canonical, sshKeyPass); err != nil {
			return fmt.Errorf("save node ssh key passphrase to keychain: %w", err)
		}
	} else {
		_ = credstore.RemoveNodeSSHKeyPassphrase(canonical)
	}

	// Step 14
	p.Printf("configured server %s\n", canonical)
	if path, perr := config.Path(); perr == nil {
		home, _ := os.UserHomeDir()
		p.Printf("config saved to %s\n", displayPath(path, home))
	}

	// Step 15: write the starter cloud-init template for this server.
	// Failures here are non-fatal — the credentials are already saved,
	// and the user can always rerun with --regen-cloud-init.
	writeInitialCloudInit(p, canonical, user, sshKey)
	return nil
}

// writeInitialCloudInit renders and writes the per-server cloud-init
// starter on first configure. It reads the SSH pubkey file named by
// sshKeyPath, calls WriteStarterCloudInit, and prints a human message
// for each outcome. Errors are warned to stderr but never returned.
func writeInitialCloudInit(p prompter, canonicalURL, user, sshKeyPath string) {
	path, err := config.CloudInitPath(canonicalURL)
	if err != nil {
		p.Errf("warning: could not resolve cloud-init path: %v\n", err)
		return
	}
	pubkeyContent, err := readSSHKey(sshKeyPath)
	if err != nil {
		p.Errf("warning: could not read ssh pubkey %s: %v\n", sshKeyPath, err)
		return
	}
	home, _ := os.UserHomeDir()
	switch err := config.WriteStarterCloudInit(path, user, pubkeyContent); {
	case err == nil:
		p.Printf("wrote cloud-init template to %s — edit it to customize packages, users, runcmd\n", displayPath(path, home))
	case errors.Is(err, config.ErrCloudInitExists):
		p.Printf("cloud-init template already exists at %s — not overwriting\n", displayPath(path, home))
	default:
		p.Errf("warning: could not write cloud-init template to %s: %v\n", path, err)
	}
}

// runRegenCloudInit rewrites the per-server cloud-init template from
// the stored user+pubkey. If more than one server is configured, it
// prompts the user to pick one. If the target file already exists, it
// prompts for overwrite confirmation before clobbering user edits.
func runRegenCloudInit(ctx context.Context, p prompter) error {
	_ = ctx
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	urls := cfg.ServerURLs()
	if len(urls) == 0 {
		return fmt.Errorf("no servers configured; run 'pmox configure' first")
	}

	var canonical string
	switch len(urls) {
	case 1:
		canonical = urls[0]
	default:
		opts := make([]huh.Option[string], 0, len(urls))
		for _, u := range urls {
			opts = append(opts, huh.NewOption(u, u))
		}
		canonical = tui.SelectOne("Select server", opts, urls[0])
		if canonical == "" {
			return fmt.Errorf("%w: no server selected", exitcode.ErrUserInput)
		}
	}

	srv := cfg.Servers[canonical]
	if srv == nil {
		return fmt.Errorf("server %s not found in config", canonical)
	}
	if srv.SSHPubkey == "" {
		return fmt.Errorf("server %s has no ssh_pubkey configured; run 'pmox configure' to set one", canonical)
	}
	user := srv.User
	if user == "" {
		user = "ubuntu"
	}
	pubkeyContent, err := readSSHKey(srv.SSHPubkey)
	if err != nil {
		return fmt.Errorf("read ssh pubkey %s: %w", srv.SSHPubkey, err)
	}

	path, err := config.CloudInitPath(canonical)
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(path); statErr == nil {
		ans, err := p.Prompt(fmt.Sprintf("cloud-init template %s already exists — overwrite? [y/N]: ", path))
		if err != nil {
			return err
		}
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			p.Printf("aborted; %s not modified\n", path)
			return nil
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, statErr)
	}

	if err := config.WriteCloudInit(path, user, pubkeyContent); err != nil {
		return fmt.Errorf("write cloud-init template: %w", err)
	}
	home, _ := os.UserHomeDir()
	p.Printf("wrote cloud-init template to %s\n", displayPath(path, home))
	return nil
}

func promptCanonicalURL(p prompter) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		raw, err := p.Prompt("Proxmox API URL: ")
		if err != nil {
			return "", err
		}
		c, err := config.CanonicalizeURL(raw)
		if err == nil {
			return c, nil
		}
		p.Errf("%v\n", err)
	}
	return "", fmt.Errorf("%w: too many invalid URL attempts", exitcode.ErrUserInput)
}

func promptTokenID(p prompter) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		s, err := p.Prompt("API token ID: ")
		if err != nil {
			return "", err
		}
		s = strings.TrimSpace(s)
		if tokenIDRegex.MatchString(s) {
			return s, nil
		}
		p.Errf("token ID must be in the form 'user@realm!tokenname' (got: '%s')\n", s)
	}
	return "", fmt.Errorf("%w: too many invalid token ID attempts", exitcode.ErrUserInput)
}

func promptSecret(p prompter) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		s, err := p.PromptSecret("API token secret: ")
		if err != nil {
			return "", err
		}
		if s != "" {
			return s, nil
		}
		p.Errf("token secret cannot be empty\n")
	}
	return "", fmt.Errorf("%w: too many empty secret attempts", exitcode.ErrUserInput)
}

// validateCredentials runs GetVersion with strict TLS, then falls back to
// insecure on TLS errors. Returns the final insecure flag used.
func validateCredentials(ctx context.Context, p prompter, baseURL, tokenID, secret string) (bool, error) {
	client := pveclient.New(baseURL, tokenID, secret, false)
	_, err := client.GetVersion(ctx)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, pveclient.ErrTLSVerificationFailed) {
		return false, err
	}
	// Retry insecure.
	client = pveclient.New(baseURL, tokenID, secret, true)
	if _, err2 := client.GetVersion(ctx); err2 != nil {
		return false, err2
	}
	p.Errf("WARNING: TLS verification failed for %s\n", baseURL)
	p.Errf("         falling back to insecure mode; the certificate will not be verified.\n")
	p.Errf("         to re-enable, set 'insecure: false' in ~/.config/pmox/config.yaml.\n")
	return true, nil
}

func discoveryCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 5*time.Second)
}

func pickNode(ctx context.Context, p prompter, client *pveclient.Client) string {
	if ctx.Err() != nil {
		return ""
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	nodes, err := client.ListNodes(dctx)
	if err != nil {
		p.Errf("could not list nodes: %v\n", err)
		ans, _ := p.Prompt("Default node: ")
		return strings.TrimSpace(ans)
	}
	if len(nodes) == 0 {
		ans, _ := p.Prompt("Default node: ")
		return strings.TrimSpace(ans)
	}
	opts := make([]huh.Option[string], 0, len(nodes))
	for _, n := range nodes {
		label := n.Node
		if n.Status != "" {
			label = fmt.Sprintf("%s (%s)", n.Node, n.Status)
		}
		opts = append(opts, huh.NewOption(label, n.Node))
	}
	return tui.SelectOne("Default node", opts, nodes[0].Node)
}

func pickTemplate(ctx context.Context, p prompter, client *pveclient.Client, node string) string {
	if ctx.Err() != nil {
		return ""
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	tmpls, total, err := client.ListTemplates(dctx, node)
	if err != nil {
		p.Errf("could not list templates on node %s: %v\n", node, err)
		ans, _ := p.Prompt("Default template (VMID): ")
		return strings.TrimSpace(ans)
	}
	if len(tmpls) == 0 {
		if total == 0 {
			p.Errf("no VMs visible on node %s — the API token cannot see any VMs.\n", node)
			p.Errf("  Fix: grant VM.Audit on /vms to the token's user, OR\n")
			p.Errf("       edit the token in Datacenter → Permissions → API Tokens\n")
			p.Errf("       and uncheck 'Privilege Separation' so it inherits the user's rights.\n")
			p.Errf("  See README.md → 'Required permissions' for the full list.\n")
		} else {
			p.Errf("node %s has %d VMs but none are marked as templates\n", node, total)
			p.Errf("  Fix: in the PVE web UI, right-click a VM → Convert to template.\n")
		}
		ans, _ := p.Prompt("Default template (VMID): ")
		return strings.TrimSpace(ans)
	}
	opts := make([]huh.Option[string], 0, len(tmpls))
	for _, t := range tmpls {
		label := fmt.Sprintf("%d  %s", t.VMID, t.Name)
		opts = append(opts, huh.NewOption(label, strconv.Itoa(t.VMID)))
	}
	return tui.SelectOne("Default template", opts, strconv.Itoa(tmpls[0].VMID))
}

func pickStorage(ctx context.Context, p prompter, client *pveclient.Client, node string) string {
	if ctx.Err() != nil {
		return ""
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	pools, err := client.ListStorage(dctx, node)
	if err != nil {
		p.Errf("could not list storage on node %s: %v\n", node, err)
		p.Errf("  (the API token likely needs Datastore.Audit on /storage)\n")
		ans, _ := p.Prompt("Default storage: ")
		return strings.TrimSpace(ans)
	}
	if len(pools) == 0 {
		p.Errf("no storage pools returned for node %s\n", node)
		ans, _ := p.Prompt("Default storage: ")
		return strings.TrimSpace(ans)
	}
	// Filter to storages that can actually hold VM disk images.
	usable := make([]pveclient.Storage, 0, len(pools))
	for _, s := range pools {
		if s.SupportsVMDisks() {
			usable = append(usable, s)
		}
	}
	if len(usable) == 0 {
		usable = pools
	}
	opts := make([]huh.Option[string], 0, len(usable))
	for _, s := range usable {
		label := fmt.Sprintf("%s (%s)", s.Storage, s.Type)
		opts = append(opts, huh.NewOption(label, s.Storage))
	}
	return tui.SelectOne("Default storage", opts, usable[0].Storage)
}

// snippetCapableTypes lists the PVE storage backends that can host the
// `snippets` content type. dir is the common case; nfs/cifs/cephfs are
// the other directory-shaped backends PVE accepts snippets on.
var snippetCapableTypes = map[string]bool{
	"dir": true, "nfs": true, "cifs": true, "cephfs": true,
}

// snippetStoragePicker abstracts the storage list and update calls
// pickSnippetStorage needs. Tests stub this with an in-memory fake to
// avoid spinning up an httptest server just for two endpoints.
type snippetStoragePicker interface {
	ListStorage(ctx context.Context, node string) ([]pveclient.Storage, error)
	UpdateStorageContent(ctx context.Context, storage string, content []string) error
}

// selectSnippetStorageFn is a test seam over tui.SelectOne so the
// multi-match branch can be driven without a real terminal.
var selectSnippetStorageFn = func(title string, opts []huh.Option[string], fallback string) string {
	return tui.SelectOne(title, opts, fallback)
}

func hasSnippets(s pveclient.Storage) bool {
	for _, c := range strings.Split(s.Content, ",") {
		if strings.TrimSpace(c) == "snippets" {
			return true
		}
	}
	return false
}

// pickSnippetStorage resolves the storage that pmox will use for
// cloud-init snippets. Decision tree: exactly one snippet-capable
// storage → silent; multiple → TUI picker; zero → offer to enable
// snippets on an existing dir-backed storage.
func pickSnippetStorage(ctx context.Context, p prompter, client snippetStoragePicker, node string) string {
	if ctx.Err() != nil {
		return ""
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	pools, err := client.ListStorage(dctx, node)
	if err != nil {
		p.Errf("could not list storage on node %s: %v\n", node, err)
		return ""
	}

	var matches []pveclient.Storage
	for _, s := range pools {
		if hasSnippets(s) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		p.Printf("Snippet storage: %s\n", matches[0].Storage)
		return matches[0].Storage
	case 0:
		return offerEnableSnippets(ctx, p, client, pools)
	}
	opts := make([]huh.Option[string], 0, len(matches))
	for _, s := range matches {
		label := fmt.Sprintf("%s (%s)", s.Storage, s.Type)
		opts = append(opts, huh.NewOption(label, s.Storage))
	}
	return selectSnippetStorageFn("Snippet storage", opts, matches[0].Storage)
}

// offerEnableSnippets is the zero-match branch of pickSnippetStorage.
// It looks for a dir-backed storage to enable `snippets` on, defaults
// to "local" when present, and on confirmation issues
// UpdateStorageContent. Decline or absence prints the manual remediation
// and returns "" so credentials still save.
func offerEnableSnippets(ctx context.Context, p prompter, client snippetStoragePicker, pools []pveclient.Storage) string {
	var capable []pveclient.Storage
	for _, s := range pools {
		if snippetCapableTypes[s.Type] {
			capable = append(capable, s)
		}
	}
	if len(capable) == 0 {
		printSnippetManualRemediation(p)
		return ""
	}
	target := capable[0]
	for _, s := range capable {
		if s.Storage == "local" {
			target = s
			break
		}
	}
	ans, err := p.Prompt(fmt.Sprintf("no storage supports snippets. enable snippets on %q? [Y/n]: ", target.Storage))
	if err != nil {
		return ""
	}
	ans = strings.ToLower(strings.TrimSpace(ans))
	if ans != "" && ans != "y" && ans != "yes" {
		printSnippetManualRemediation(p)
		return ""
	}

	newContent := splitContent(target.Content)
	if !containsString(newContent, "snippets") {
		newContent = append(newContent, "snippets")
	}
	if err := client.UpdateStorageContent(ctx, target.Storage, newContent); err != nil {
		p.Errf("could not enable snippets on %q: %v\n", target.Storage, err)
		printSnippetManualRemediation(p)
		return ""
	}
	p.Printf("enabled snippets on %s\n", target.Storage)
	return target.Storage
}

func splitContent(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func printSnippetManualRemediation(p prompter) {
	p.Errf("no snippet storage configured.\n")
	p.Errf("  Fix: edit /etc/pve/storage.cfg on the PVE host and add 'snippets' to\n")
	p.Errf("       the content= line of a directory-backed storage, then re-run\n")
	p.Errf("       'pmox configure' to record it.\n")
}

func pickBridge(ctx context.Context, p prompter, client *pveclient.Client, node string) string {
	if ctx.Err() != nil {
		return ""
	}
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	bridges, err := client.ListBridges(dctx, node)
	if err != nil {
		p.Errf("could not list bridges on node %s: %v\n", node, err)
		p.Errf("  (the API token likely needs SDN.Audit or Sys.Audit on /nodes/%s)\n", node)
		ans, _ := p.Prompt("Default bridge: ")
		return strings.TrimSpace(ans)
	}
	if len(bridges) == 0 {
		p.Errf("no bridges returned for node %s\n", node)
		ans, _ := p.Prompt("Default bridge: ")
		return strings.TrimSpace(ans)
	}
	opts := make([]huh.Option[string], 0, len(bridges))
	for _, b := range bridges {
		opts = append(opts, huh.NewOption(b.Iface, b.Iface))
	}
	return tui.SelectOne("Default bridge", opts, bridges[0].Iface)
}

func displayPath(path, home string) string {
	if home != "" && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

func findPubKeys(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".pub") {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// Test seams for SSH validation and host-key pinning. In production
// these delegate to pvessh; tests replace them with in-process stubs.
var (
	sshValidateFn = func(ctx context.Context, cfg pvessh.Config) error {
		c, err := pvessh.Dial(ctx, cfg)
		if err != nil {
			return err
		}
		defer func() { _ = c.Close() }()
		return c.Ping(ctx)
	}
	sshPinHostKeyFn = func(ctx context.Context, host, knownHosts string, w io.Writer, r io.Reader) error {
		return pvessh.PromptAndPinHostKey(ctx, host, w, r, knownHosts)
	}
	sshKnownHostsPathFn = pvessh.KnownHostsPath
)

// promptNodeSSH collects SSH user, auth mode and secret, pins the host
// key on first use (unless --ssh-insecure), then validates via dial+ping
// before returning. Returns (NodeSSH block for YAML, password secret,
// key passphrase secret).
func promptNodeSSH(ctx context.Context, p prompter, canonicalURL string) (*config.NodeSSH, string, string, error) {
	host, err := sshHostFromURL(canonicalURL)
	if err != nil {
		return nil, "", "", err
	}

	// Host-key pin (skipped under --ssh-insecure).
	if !SSHInsecure() {
		kh, err := sshKnownHostsPathFn()
		if err != nil {
			return nil, "", "", err
		}
		if need, err := needsHostKeyPin(kh, host); err != nil {
			return nil, "", "", err
		} else if need {
			if std, ok := p.(*stdPrompter); ok {
				if err := sshPinHostKeyFn(ctx, host, kh, std.out, std.in); err != nil {
					return nil, "", "", fmt.Errorf("pin host key for %s: %w", host, err)
				}
			} else {
				// Non-TTY prompter (tests): fall back to the seam with
				// a throwaway reader/writer. Real tests stub the seam.
				if err := sshPinHostKeyFn(ctx, host, kh, io.Discard, strings.NewReader("yes\n")); err != nil {
					return nil, "", "", fmt.Errorf("pin host key for %s: %w", host, err)
				}
			}
		}
	}

	for attempt := 0; attempt < 3; attempt++ {
		userAns, err := p.Prompt("Proxmox node SSH username [root]: ")
		if err != nil {
			return nil, "", "", err
		}
		userAns = strings.TrimSpace(userAns)
		if userAns == "" {
			userAns = "root"
		}

		authAns, err := p.Prompt("Authenticate with (p)assword or (k)ey file? [p]: ")
		if err != nil {
			return nil, "", "", err
		}
		authAns = strings.ToLower(strings.TrimSpace(authAns))
		if authAns == "" {
			authAns = "p"
		}

		cfg := pvessh.Config{
			Host:     host,
			User:     userAns,
			Insecure: SSHInsecure(),
		}
		if !cfg.Insecure {
			kh, kerr := sshKnownHostsPathFn()
			if kerr != nil {
				return nil, "", "", kerr
			}
			cfg.KnownHosts = kh
		}

		var (
			password string
			keyPath  string
			keyPass  string
		)
		switch authAns {
		case "p", "password":
			pw, err := p.PromptSecret("Password: ")
			if err != nil {
				return nil, "", "", err
			}
			if pw == "" {
				p.Errf("password cannot be empty\n")
				continue
			}
			password = pw
			cfg.Password = pw
		case "k", "key":
			kp, err := p.Prompt("Path to SSH private key: ")
			if err != nil {
				return nil, "", "", err
			}
			kp = strings.TrimSpace(expandHome(kp))
			if kp == "" {
				p.Errf("key path cannot be empty\n")
				continue
			}
			keyPath = kp
			cfg.KeyPath = kp

			yn, err := p.Prompt("Key is passphrase-protected? [y/N]: ")
			if err != nil {
				return nil, "", "", err
			}
			if strings.ToLower(strings.TrimSpace(yn)) == "y" {
				kpass, err := p.PromptSecret("Key passphrase: ")
				if err != nil {
					return nil, "", "", err
				}
				keyPass = kpass
				cfg.KeyPass = kpass
			}
		default:
			p.Errf("answer 'p' for password or 'k' for key file\n")
			continue
		}

		p.Printf("Verifying SSH connectivity to %s... ", host)
		if err := sshValidateFn(ctx, cfg); err != nil {
			p.Printf("failed\n")
			p.Errf("%v\n", err)
			continue
		}
		p.Printf("ok\n")

		ns := &config.NodeSSH{User: userAns}
		if password != "" {
			ns.Auth = "password"
		} else {
			ns.Auth = "key"
			ns.KeyPath = keyPath
		}
		return ns, password, keyPass, nil
	}
	return nil, "", "", fmt.Errorf("%w: too many failed SSH credential attempts", exitcode.ErrUserInput)
}

// sshHostFromURL extracts host:22 from a canonical PVE API URL.
func sshHostFromURL(canonicalURL string) (string, error) {
	u, err := url.Parse(canonicalURL)
	if err != nil {
		return "", fmt.Errorf("parse server url: %w", err)
	}
	h := u.Hostname()
	if h == "" {
		return "", fmt.Errorf("server url has no host: %s", canonicalURL)
	}
	return h + ":22", nil
}

// needsHostKeyPin reports whether the known_hosts file is missing an
// entry for host. A missing file counts as needing a pin.
func needsHostKeyPin(knownHostsPath, host string) (bool, error) {
	data, err := os.ReadFile(knownHostsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, fmt.Errorf("read %s: %w", knownHostsPath, err)
	}
	h := strings.TrimSuffix(host, ":22")
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// known_hosts lines start with "host[,host2] type base64".
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for _, n := range strings.Split(fields[0], ",") {
			if n == h || n == host {
				return false, nil
			}
		}
	}
	return true, nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func promptSSHKey(p prompter) (string, error) {
	home, _ := os.UserHomeDir()
	sshDir := filepath.Join(home, ".ssh")
	candidates := []string{
		filepath.Join(sshDir, "id_ed25519.pub"),
		filepath.Join(sshDir, "id_rsa.pub"),
	}
	var suggest string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			suggest = c
			break
		}
	}

	// Interactive select populated with .pub files found under ~/.ssh.
	pubKeys := findPubKeys(sshDir)
	if len(pubKeys) > 0 {
		fmt.Println()
		opts := make([]huh.Option[string], 0, len(pubKeys))
		for _, k := range pubKeys {
			label := k
			if rel, rerr := filepath.Rel(sshDir, k); rerr == nil {
				label = rel
			}
			opts = append(opts, huh.NewOption(label, k))
		}
		picked := suggest
		if picked == "" {
			picked = pubKeys[0]
		}
		err := huh.NewSelect[string]().
			Title("Default SSH public key").
			Options(opts...).
			Value(&picked).
			Filtering(false).
			Run()
		if err == nil && picked != "" {
			if _, rErr := os.ReadFile(picked); rErr == nil {
				p.Printf("Default SSH public key: %s\n", displayPath(picked, home))
				return picked, nil
			}
			p.Errf("cannot read %s\n", displayPath(picked, home))
		} else if errors.Is(err, huh.ErrUserAborted) {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
			return "", fmt.Errorf("%w: interrupted", exitcode.ErrUserInput)
		}
	}

	// Fallback: plain text prompt with ~ expansion and retries.
	for attempt := 0; attempt < 3; attempt++ {
		label := "Default SSH public key path"
		if suggest != "" {
			label = fmt.Sprintf("%s [%s]", label, suggest)
		}
		ans, err := p.Prompt(label + ": ")
		if err != nil {
			return "", err
		}
		ans = strings.TrimSpace(ans)
		if ans == "" {
			ans = suggest
		}
		if ans == "" {
			p.Errf("ssh key path is required\n")
			continue
		}
		expanded := ans
		if strings.HasPrefix(expanded, "~/") {
			expanded = filepath.Join(home, expanded[2:])
		}
		if _, err := os.ReadFile(expanded); err != nil {
			p.Errf("cannot read %s: %v\n", ans, err)
			continue
		}
		return ans, nil
	}
	return "", fmt.Errorf("%w: too many invalid ssh key attempts", exitcode.ErrUserInput)
}


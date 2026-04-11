package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/eugenetaranov/pmox/internal/tui"
)

var (
	configureList   bool
	configureRemove string
)

var configureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Configure a Proxmox VE server for pmox",
	Long: `Interactively configure credentials and defaults for a Proxmox VE server.

Walks through API URL, token, credential validation against /version, and
auto-discovery of node, template, storage, and bridge. The token secret is
stored in the system keychain; everything else is written to
$XDG_CONFIG_HOME/pmox/config.yaml (or ~/.config/pmox/config.yaml).

Prompts:
  Proxmox API URL   base URL of the PVE host, e.g. https://192.168.0.185:8006
                    (the web UI URL with '#v1:...' also works — the path is
                    stripped automatically)
  API token ID      in the form 'user@realm!tokenname', e.g. 'root@pam!pmox'
                    or 'pmox@pve!mytoken'. Create one in the PVE web UI under
                    Datacenter → Permissions → API Tokens → Add.
  API token secret  the UUID shown once when the token is created.`,
	RunE: runConfigure,
}

func init() {
	configureCmd.Flags().BoolVar(&configureList, "list", false, "List configured server URLs")
	configureCmd.Flags().StringVar(&configureRemove, "remove", "", "Remove a configured server by URL")
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
	if configureList && configureRemove != "" {
		return fmt.Errorf("--list and --remove are mutually exclusive")
	}
	if configureList {
		return runList(newStdPrompter(ctx))
	}
	if configureRemove != "" {
		return runRemove(newStdPrompter(ctx), configureRemove)
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

	// Step 13: save
	srv := &config.Server{
		TokenID:  tokenID,
		Node:     node,
		Template: template,
		Storage:  storage,
		Bridge:   bridge,
		SSHPubkey: sshKey,
		User:     user,
		Insecure: insecure,
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

	// Step 14
	p.Printf("configured server %s\n", canonical)
	if path, perr := config.Path(); perr == nil {
		home, _ := os.UserHomeDir()
		p.Printf("config saved to %s\n", displayPath(path, home))
	}
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


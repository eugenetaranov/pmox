package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
	"github.com/eugenetaranov/pmox/internal/vmwait"
)

// sshConnInfo is the resolved connection detail for a VM.
type sshConnInfo struct {
	Name         string            `json:"name"`
	Hostname     string            `json:"hostname"`
	User         string            `json:"user"`
	IdentityFile string            `json:"identity_file,omitempty"`
	ForwardAgent bool              `json:"forward_agent,omitempty"`
	Options      map[string]string `json:"options,omitempty"`
	Command      string            `json:"command"`
}

func newSSHConfigCmd() *cobra.Command {
	f := &sshFlags{}
	var asCommand bool
	cmd := &cobra.Command{
		Use:   "ssh-config [name|vmid]",
		Short: "Print SSH config for a VM",
		Long: `Print how to reach a VM over SSH: an OpenSSH config block, or with
--command the full ssh command. --output json prints the fields.

Append the block to ~/.ssh/config, use it in place with
'ssh -F <(pmox vm ssh-config web1) web1', or give it to VS Code
Remote-SSH, rsync or Ansible.

The VM must be running; ssh-config never starts it. The login user and
key are the ones 'pmox shell' uses; --user and --identity override them.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSSHConfig(cmd, args, f, asCommand)
		},
	}
	addSSHFlags(cmd, f)
	addForwardFlags(cmd, f, false)
	cmd.Flags().BoolVar(&asCommand, "command", false, "print a ready-to-run ssh command instead of a config block")
	addPickFlag(cmd)
	return cmd
}

func runSSHConfig(cmd *cobra.Command, args []string, f *sshFlags, asCommand bool) error {
	ctx := cmd.Context()
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	srv := resolved.Server
	arg, err := resolveDefaultTargetArg(ctx, client, args, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	info, err := resolveSSHConnInfo(ctx, client, arg, f, resolved.URL, srv, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	if wantForwardAgent(cmd, f, srv) {
		info.ForwardAgent = true
		info.Command = sshCommandLine(&sshTarget{IP: info.Hostname, User: info.User, Key: info.IdentityFile}, "-A")
	}

	out := cmd.OutOrStdout()
	switch {
	case outputMode == "json":
		return printJSON(out, info)
	case asCommand:
		fmt.Fprintln(out, info.Command)
	default:
		renderSSHConfigBlock(out, info)
	}
	return nil
}

// resolveSSHConnInfo resolves a VM's connection details WITHOUT starting
// it (unlike shell/exec). A stopped VM is an error, since there is no IP.
func resolveSSHConnInfo(ctx context.Context, client *pveclient.Client, arg string, f *sshFlags, serverURL string, srv *config.Server, stderr io.Writer) (*sshConnInfo, error) {
	sp := startSpin(fmt.Sprintf("Looking up %s…", arg))
	defer sp.Stop()
	ref, err := vm.Resolve(ctx, client, arg)
	if err != nil {
		return nil, err
	}
	if err := ref.RequirePMOXTag("report SSH details for", f.force); err != nil {
		return nil, err
	}

	status, err := client.GetStatus(ctx, ref.Node, ref.VMID)
	if err != nil {
		if errors.Is(err, pveclient.ErrNotFound) {
			return nil, fmt.Errorf("VM %q (vmid %d) not found", ref.Name, ref.VMID)
		}
		return nil, fmt.Errorf("get status for vm %d: %w", ref.VMID, err)
	}
	if !status.IsRunning() {
		return nil, fmt.Errorf("VM %q (vmid %d) is %s, not running — run 'pmox start %s' first", ref.Name, ref.VMID, status.Status, ref.Name)
	}

	ifaces, err := client.AgentNetwork(ctx, ref.Node, ref.VMID)
	sp.Stop() // before any note printed below
	if err != nil {
		return nil, fmt.Errorf("VM %q is running but the guest agent is not responding; is qemu-guest-agent installed?", ref.Name)
	}
	ip := vmwait.PickGuestIPv4(ifaces, client.PreferOverlay)
	if ip == "" {
		return nil, fmt.Errorf("VM %q is running but the guest agent reports no usable IPv4 address yet", ref.Name)
	}

	user, key, note, err := resolveGuestIdentity(serverURL, ref.VMID, f.user, f.identity, srv)
	if err != nil {
		return nil, err
	}
	if note != "" && stderr != nil {
		fmt.Fprintln(stderr, note)
	}

	info := &sshConnInfo{
		Name:         ref.Name,
		Hostname:     ip,
		User:         user,
		IdentityFile: key,
		Options:      hostKeyOptionMap(),
		Command:      sshCommandLine(&sshTarget{IP: ip, User: user, Key: key}),
	}
	return info, nil
}

// renderSSHConfigBlock writes an OpenSSH config stanza for the VM.
func renderSSHConfigBlock(w io.Writer, info *sshConnInfo) {
	fmt.Fprintf(w, "Host %s\n", info.Name)
	fmt.Fprintf(w, "  HostName %s\n", info.Hostname)
	fmt.Fprintf(w, "  User %s\n", info.User)
	if info.IdentityFile != "" {
		fmt.Fprintf(w, "  IdentityFile %s\n", sshConfValue(info.IdentityFile))
		fmt.Fprintln(w, "  IdentitiesOnly yes")
	}
	if info.ForwardAgent {
		fmt.Fprintln(w, "  ForwardAgent yes")
	}
	for _, line := range guestHostKeyConfigLines() {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

// sshCommandLine builds a ready-to-run ssh command string; extra options
// go before the destination.
func sshCommandLine(target *sshTarget, extra ...string) string {
	parts := []string{"ssh"}
	for _, o := range append(guestHostKeyOpts(), extra...) {
		parts = append(parts, shellQuote(o))
	}
	if target.Key != "" {
		parts = append(parts, "-i", shellQuote(target.Key))
	}
	parts = append(parts, target.User+"@"+target.IP)
	return strings.Join(parts, " ")
}

// guestHostKeyConfigLines converts the "-o Key=Value" options into
// ssh_config directive lines ("Key Value").
func guestHostKeyConfigLines() []string {
	opts := guestHostKeyOpts()
	var lines []string
	for i := 0; i+1 < len(opts); i += 2 {
		kv := opts[i+1]
		if j := strings.IndexByte(kv, '='); j > 0 {
			lines = append(lines, kv[:j]+" "+sshConfValue(kv[j+1:]))
		}
	}
	return lines
}

// hostKeyOptionMap returns the host-key options as a Key->Value map for
// JSON output.
func hostKeyOptionMap() map[string]string {
	opts := guestHostKeyOpts()
	m := map[string]string{}
	for i := 0; i+1 < len(opts); i += 2 {
		kv := opts[i+1]
		if j := strings.IndexByte(kv, '='); j > 0 {
			m[kv[:j]] = kv[j+1:]
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// shellQuote wraps s in double quotes when it contains whitespace so the
// printed command is copy-paste-safe.
func shellQuote(s string) string {
	if strings.ContainsAny(s, " \t") {
		return "\"" + s + "\""
	}
	return s
}

// sshConfValue quotes an ssh_config value when it contains whitespace.
func sshConfValue(s string) string {
	if strings.ContainsAny(s, " \t") {
		return "\"" + s + "\""
	}
	return s
}

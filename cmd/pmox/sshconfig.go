package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

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
	Options      map[string]string `json:"options,omitempty"`
	Command      string            `json:"command"`
}

func newSSHConfigCmd() *cobra.Command {
	f := &sshFlags{}
	var asCommand bool
	cmd := &cobra.Command{
		Use:   "ssh-config [name|vmid]",
		Short: "Print SSH connection details for a VM",
		Long: `Print how to connect to a pmox VM over SSH — an OpenSSH config
block by default, or the full ssh command with --command.

The config block can be appended to ~/.ssh/config, used directly with
'ssh -F <(pmox ssh-config web1) <name>', or consumed by VS Code
Remote-SSH, rsync, and Ansible. Use --output json for a structured form.

The VM must be running — ssh-config never starts it. The default login
user is the cloud-init user ("pmox") and the identity key is derived
from the configured SSH public key; override with --user / --identity.

Examples:
  pmox ssh-config web1
  pmox ssh-config web1 --command
  pmox ssh-config web1 --output json | jq -r .hostname`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSSHConfig(cmd, args, f, asCommand)
		},
	}
	addSSHFlags(cmd, f)
	cmd.Flags().BoolVar(&asCommand, "command", false, "print a ready-to-run ssh command instead of a config block")
	return cmd
}

func runSSHConfig(cmd *cobra.Command, args []string, f *sshFlags, asCommand bool) error {
	ctx := cmd.Context()
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return err
	}
	srv := resolved.Server
	arg, err := resolveTargetArg(ctx, client, args, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	info, err := resolveSSHConnInfo(ctx, client, arg, f, srv.User, srv.SSHPubkey)
	if err != nil {
		return err
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
func resolveSSHConnInfo(ctx context.Context, client *pveclient.Client, arg string, f *sshFlags, configUser, configPubkey string) (*sshConnInfo, error) {
	ref, err := vm.Resolve(ctx, client, arg)
	if err != nil {
		return nil, err
	}
	if !f.force && !vm.HasPMOXTag(ref.Tags) {
		return nil, fmt.Errorf("refusing to report SSH details for VM %q (vmid %d): not tagged \"pmox\" — pass --force to override", ref.Name, ref.VMID)
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
	if err != nil {
		return nil, fmt.Errorf("VM %q is running but the guest agent is not responding; is qemu-guest-agent installed?", ref.Name)
	}
	ip := vmwait.PickIPv4(ifaces)
	if ip == "" {
		return nil, fmt.Errorf("VM %q is running but the guest agent reports no usable IPv4 address yet", ref.Name)
	}

	key, err := resolveIdentityKey(f.identity, configPubkey)
	if err != nil {
		return nil, err
	}
	user := firstNonEmpty(f.user, configUser, defaultUser)

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
	for _, line := range guestHostKeyConfigLines() {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

// sshCommandLine builds a ready-to-run ssh command string.
func sshCommandLine(target *sshTarget) string {
	parts := []string{"ssh"}
	for _, o := range guestHostKeyOpts() {
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

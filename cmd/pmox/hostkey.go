package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// guestHostKeyFiles are the sshd host public keys read through the guest
// agent to confirm a guest's identity.
var guestHostKeyFiles = []string{
	"/etc/ssh/ssh_host_ed25519_key.pub",
	"/etc/ssh/ssh_host_ecdsa_key.pub",
	"/etc/ssh/ssh_host_rsa_key.pub",
}

// refreshGuestHostKeyFn is a seam: tests never touch a guest agent.
var refreshGuestHostKeyFn = func(ctx context.Context, stderr io.Writer, client *pveclient.Client, ref *vm.Ref, ip string) {
	refreshGuestHostKey(ctx, stderr, client, ref, ip)
}

// agentFileReader is the guest-agent call refreshGuestHostKey needs.
type agentFileReader interface {
	AgentFileRead(ctx context.Context, node string, vmid int, path string) ([]byte, bool, error)
}

// refreshGuestHostKey re-pins ip's host key when the pinned one is stale
// (the VM was rebuilt, or DHCP gave a deleted VM's address to this one).
// The new keys come from the guest agent over the authenticated PVE API,
// not from the network, so this doesn't weaken TOFU. Best-effort: with no
// pin, no agent, or no ssh-keygen it leaves the file alone and ssh decides.
func refreshGuestHostKey(ctx context.Context, stderr io.Writer, client agentFileReader, ref *vm.Ref, ip string) {
	if SSHInsecure() {
		return
	}
	path, err := guestKnownHostsPath()
	if err != nil {
		return
	}
	pinned := pinnedHostKeys(path, ip)
	if len(pinned) == 0 {
		return // never connected: accept-new pins it
	}
	var actual []string
	for _, f := range guestHostKeyFiles {
		data, _, err := client.AgentFileRead(ctx, ref.Node, ref.VMID, f)
		if err != nil {
			continue
		}
		if k := hostKeyField(string(data)); k != "" {
			actual = append(actual, k)
		}
	}
	if len(actual) == 0 {
		return // can't confirm: leave it to ssh (and explainSSHFailure)
	}
	for _, k := range actual {
		if pinned[k] {
			return // still current
		}
	}
	if err := forgetGuestHostKey(path, ip); err != nil {
		return
	}
	var b strings.Builder
	for _, k := range actual {
		fmt.Fprintf(&b, "%s %s\n", ip, k)
	}
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, werr := fh.WriteString(b.String())
	if cerr := fh.Close(); werr != nil || cerr != nil {
		return
	}
	fmt.Fprintf(stderr, "%s has a new SSH host key since you last connected (rebuilt, or its address was reused) — confirmed it with the guest agent and updated pmox's known hosts.\n", ref.Name)
}

// pinnedHostKeys returns the "type base64" keys pinned for host in the
// known_hosts file at path. ssh-keygen -F also finds hashed entries.
func pinnedHostKeys(path, host string) map[string]bool {
	out, err := exec.Command("ssh-keygen", "-F", host, "-f", path).Output()
	if err != nil {
		return nil // not found (exit 1), no file, or no ssh-keygen
	}
	keys := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if fields := strings.Fields(line); len(fields) >= 3 {
			keys[fields[1]+" "+fields[2]] = true
		}
	}
	return keys
}

// forgetGuestHostKey drops host's entries (hashed ones too) from the
// known_hosts file at path, without leaving ssh-keygen's .old backup.
func forgetGuestHostKey(path, host string) error {
	if err := exec.Command("ssh-keygen", "-R", host, "-f", path).Run(); err != nil {
		return err
	}
	_ = os.Remove(path + ".old")
	return nil
}

// hostKeyField returns the "type base64" part of a .pub file's content.
func hostKeyField(pub string) string {
	fields := strings.Fields(pub)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "ssh-") && !strings.HasPrefix(fields[0], "ecdsa-") {
		return ""
	}
	return fields[0] + " " + fields[1]
}

// hostKeyChangedError explains a host key mismatch pmox couldn't resolve
// itself, after ssh's own alarming banner.
type hostKeyChangedError struct{ msg string }

func (e *hostKeyChangedError) Error() string { return e.msg }

func hostKeyGuidance(target *sshTarget) string {
	name := target.Name
	if name == "" {
		name = target.IP
	}
	path, err := guestKnownHostsPath()
	if err != nil {
		path = "~/.config/pmox/known_hosts_guests"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s) answered with a different SSH host key than the one pmox saved.\n", name, target.IP)
	b.WriteString("  Usually that's harmless: the VM was rebuilt, or its address now belongs to another VM.\n")
	b.WriteString("  pmox couldn't confirm the new key through the guest agent, so it didn't trust it.\n")
	fmt.Fprintf(&b, "  If you expected this, forget the old key and reconnect:   ssh-keygen -R %s -f %s", target.IP, posixQuote(path))
	return b.String()
}

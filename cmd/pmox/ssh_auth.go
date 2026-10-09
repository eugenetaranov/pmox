package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/eugenetaranov/pmox/internal/accessreg"
	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/server"
)

// sshAuthError reports that a VM rejected the caller's SSH key, with the
// steps to get access. It carries its own exit code (ExitSSHAuth).
type sshAuthError struct{ msg string }

func (e *sshAuthError) Error() string { return e.msg }
func (e *sshAuthError) ExitCode() int { return exitcode.ExitSSHAuth }

// sshAuthRejectedFn reports whether the guest refuses target's key. It is
// a seam: tests never run ssh.
var sshAuthRejectedFn = sshAuthRejected

// sshAuthRejected runs one non-interactive, publickey-only ssh probe and
// looks for sshd's "Permission denied (publickey…)".
func sshAuthRejected(ctx context.Context, target *sshTarget) bool {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := append([]string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=8",
		"-o", "PreferredAuthentications=publickey", "-o", "IdentitiesOnly=yes"}, guestHostKeyOpts()...)
	if target.Key != "" {
		args = append(args, "-i", target.Key)
	}
	args = append(args, fmt.Sprintf("%s@%s", target.User, target.IP), "true")
	var stderr bytes.Buffer
	c := exec.CommandContext(ctx, sshPath, args...)
	c.Stderr = &stderr
	if c.Run() == nil {
		return false
	}
	return strings.Contains(stderr.String(), "Permission denied (publickey")
}

// explainSSHFailure turns a failed ssh/scp/rsync/tack run into guidance
// when the cause is the guest rejecting the caller's key; any other
// error is returned unchanged. With only255 (plain ssh), only ssh's own
// failure status triggers the check — any other status is the remote
// command's.
func explainSSHFailure(ctx context.Context, target *sshTarget, serverURL string, err error, only255 bool) error {
	if err == nil || target == nil {
		return err
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || (only255 && ee.ExitCode() != 255) {
		return err
	}
	if !sshAuthRejectedFn(ctx, target) {
		return err
	}
	return &sshAuthError{msg: authGuidance(ctx, target, serverURL)}
}

// regStatus is what the registry says about the caller and this VM.
type regStatus int

const (
	regUnknown regStatus = iota
	regNotPublished
	regDifferentKey
	regNotGranted
	regGranted
)

// registryStatusFn is a seam over the registry lookup.
var registryStatusFn = registryStatus

// registryStatus looks the caller up in the registry. A key published
// under any name with the caller's fingerprint counts as theirs (--name
// may differ from the local username); otherwise name is checked. It
// returns the name the registry knows the caller by.
func registryStatus(ctx context.Context, serverURL, name, fingerprint string, vmid int) (regStatus, string) {
	cfg, err := config.Load()
	if err != nil {
		return regUnknown, name
	}
	r, err := server.Resolve(ctx, server.Options{Cfg: cfg, Flag: serverURL})
	if err != nil || !r.HasNodeSSH() {
		return regUnknown, name
	}
	fs, closeFS, err := openRegistryFn(ctx, r)
	if err != nil {
		return regUnknown, name
	}
	defer closeFS()
	matched := false
	if fingerprint != "" {
		if keys, _, err := accessreg.ListKeys(ctx, fs); err == nil {
			for _, k := range keys {
				if k.Fingerprint == fingerprint {
					name, matched = k.Name, true
					break
				}
			}
		}
	}
	if !matched {
		k, err := accessreg.GetKey(ctx, fs, name)
		switch {
		case errors.Is(err, accessreg.ErrNotPublished):
			return regNotPublished, name
		case err != nil:
			return regUnknown, name
		case fingerprint != "" && k.Fingerprint != fingerprint:
			return regDifferentKey, name
		}
	}
	acc, err := accessreg.ReadAccess(ctx, fs)
	if err != nil {
		return regUnknown, name
	}
	if acc.Allowed(name, vmid) {
		return regGranted, name
	}
	return regNotGranted, name
}

// keyFingerprint returns the SHA256 fingerprint of the public half of
// privateKeyPath ("" when it can't be read).
func keyFingerprint(privateKeyPath string) string {
	data, err := os.ReadFile(privateKeyPath + ".pub")
	if err != nil {
		return ""
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(pub)
}

func authGuidance(ctx context.Context, target *sshTarget, serverURL string) string {
	vmName := target.Name
	if vmName == "" {
		vmName = target.IP
	}
	fp := keyFingerprint(target.Key)
	keyDesc := target.Key
	if fp != "" {
		keyDesc += " (" + fp + ")"
	}
	name, err := localUsername()
	if err != nil || accessreg.ValidName(name) != nil {
		name = "<your-name>"
	}

	status, name := registryStatusFn(ctx, serverURL, name, fp, target.VMID)
	var b strings.Builder
	fmt.Fprintf(&b, "%s doesn't accept your SSH key %s as %s.\n", vmName, keyDesc, target.User)
	grant := fmt.Sprintf("pmox access grant %s --to %s", vmName, name)
	switch status {
	case regNotGranted:
		fmt.Fprintf(&b, "  Your key is published as %q but not granted for %s.\n  Ask a cluster admin to run:   %s", name, vmName, grant)
	case regGranted:
		fmt.Fprintf(&b, "  You are granted access, but the VM hasn't been updated yet.\n  Run:   pmox access sync %s", vmName)
	case regDifferentKey:
		fmt.Fprintf(&b, "  A different key is published as %q. Publish this one:   pmox key publish --replace\n  then a cluster admin runs:   %s", name, grant)
	default:
		fmt.Fprintf(&b, "  Publish your key once:   pmox key publish\n  Then a cluster admin grants it:   %s", grant)
	}
	return b.String()
}

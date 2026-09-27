package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/paths"
	"github.com/eugenetaranov/pmox/internal/vmidentity"
)

// guestIdentityStateDir returns ~/.local/state/pmox/vmidentity (XDG-aware),
// mirroring tackStateDir (cmd/pmox/apply.go).
func guestIdentityStateDir() (string, error) {
	dir, err := paths.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "vmidentity"), nil
}

// recordVMIdentity persists, for vmid, the user + SSH public-key line
// ACTUALLY baked into the cloud-init file just uploaded as its cicustom
// snippet — read back from cloudInitPath, not from srv.User/srv.SSHPubkey,
// which can already have drifted from that file if it wasn't
// regenerated after a later config edit. Best-effort: recording never
// fails launch/clone — a failure here just means later SSH-based
// commands fall back to current config for this VM, exactly as they did
// before this feature existed.
func recordVMIdentity(stderr io.Writer, serverURL string, vmid int, cloudInitPath string) {
	if serverURL == "" {
		return
	}
	identity, err := config.CloudInitIdentityFromFile(cloudInitPath)
	if err != nil || identity.User == "" || len(identity.KeyLines) == 0 {
		if err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: could not record identity for vm %d: %v\n", vmid, err)
		}
		return
	}
	dir, err := guestIdentityStateDir()
	if err != nil {
		return
	}
	rec := vmidentity.Identity{User: identity.User, SSHPubkeyLine: identity.KeyLines[0]}
	if err := vmidentity.Set(dir, serverURL, vmid, rec); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "warning: could not record identity for vm %d: %v\n", vmid, err)
	}
}

// resolveGuestIdentity resolves the SSH user and private-key path pmox
// uses to connect to a specific VM, plus a one-line, non-blocking note
// (empty when there's nothing to say) describing any mismatch between
// what was recorded for that VM at launch time and current config.
//
// Precedence:
//
//	user: --user flag > vmidentity record for (serverURL,vmid) > srv.User > "pmox"
//	key:  --identity flag > derived from srv.SSHPubkey — EXACTLY as
//	      resolveIdentityKey already did. The per-VM record can only
//	      ever improve *user* resolution: pmox never records a private
//	      key *path*, only the public key body actually baked in (which
//	      may trace back to a machine pmox has no local trace of), so
//	      there is no alternate private-key file it could point
//	      --identity at automatically. A recorded key body that no
//	      longer matches the currently configured public key only
//	      surfaces via note, never via a different resolved keyPath.
func resolveGuestIdentity(serverURL string, vmid int, flagUser, flagIdentity string, srv *config.Server) (user, keyPath, note string, err error) {
	var rec vmidentity.Identity
	var haveRec bool
	if serverURL != "" {
		if dir, dirErr := guestIdentityStateDir(); dirErr == nil {
			rec, haveRec, _ = vmidentity.Get(dir, serverURL, vmid) // best-effort
		}
	}

	recUser := ""
	if haveRec {
		recUser = rec.User
	}
	user = firstNonEmpty(flagUser, recUser, srv.User, defaultUser)

	keyPath, err = resolveIdentityKey(flagIdentity, srv.SSHPubkey)
	if err != nil {
		return "", "", "", err
	}

	if haveRec {
		userSubstituted := flagUser == "" && rec.User != "" && rec.User != firstNonEmpty(srv.User, defaultUser)
		keyDiffers := flagIdentity == "" && guestIdentityKeyDiffers(rec.SSHPubkeyLine, srv)
		note = guestIdentityNote(vmid, rec, srv, userSubstituted, keyDiffers)
	}
	return user, keyPath, note, nil
}

// guestIdentityKeyDiffers reports whether recordedLine's key body differs
// from the currently configured ssh_pubkey's. It stays silent (false) on
// any read failure rather than guess.
func guestIdentityKeyDiffers(recordedLine string, srv *config.Server) bool {
	if recordedLine == "" || srv.SSHPubkey == "" {
		return false
	}
	current, err := readSSHKey(srv.SSHPubkey)
	if err != nil {
		return false
	}
	return config.PubKeyBody(recordedLine) != config.PubKeyBody(current)
}

// guestIdentityNote builds the one-line, non-blocking drift note printed
// before connecting, or "" when there's nothing to say.
func guestIdentityNote(vmid int, rec vmidentity.Identity, srv *config.Server, userSubstituted, keyDiffers bool) string {
	switch {
	case userSubstituted && keyDiffers:
		return fmt.Sprintf("note: vm %d was provisioned with user %q and a different SSH key than currently configured (current default user is %q); using %q for this VM — if authentication still fails, pass --identity to point at the correct private key.",
			vmid, rec.User, firstNonEmpty(srv.User, defaultUser), rec.User)
	case userSubstituted:
		return fmt.Sprintf("note: vm %d was provisioned with user %q (current default is %q); using %q for this VM.",
			vmid, rec.User, firstNonEmpty(srv.User, defaultUser), rec.User)
	case keyDiffers:
		return fmt.Sprintf("note: vm %d was provisioned with a different SSH key than currently configured; if authentication fails, pass --identity to point at the correct private key.", vmid)
	default:
		return ""
	}
}

// forgetVMIdentity removes the deleted VM's recorded identity, if any.
// Mirrors forgetTackProfile (cmd/pmox/delete.go): best-effort, a failure
// is a warning, never a delete failure.
func forgetVMIdentity(cmd *cobra.Command, serverURL string, vmid int) {
	if serverURL == "" {
		return
	}
	dir, err := guestIdentityStateDir()
	if err != nil {
		return
	}
	if err := vmidentity.Delete(dir, serverURL, vmid); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not forget vm identity for vm %d: %v\n", vmid, err)
	}
}

// vmIdentityItems flags recorded VM identities whose server is gone, or
// whose VMID no longer exists on a reachable server. Mirrors
// tackProfileItems (cmd/pmox/cleanup.go).
func vmIdentityItems(cfg *config.Config, vmidsByURL map[string]map[int]bool, reachableURLs map[string]bool) []cleanupItem {
	dir, err := guestIdentityStateDir()
	if err != nil {
		return nil
	}
	entries, err := vmidentity.All(dir)
	if err != nil {
		return nil
	}
	var items []cleanupItem
	for _, e := range entries {
		_, configured := cfg.Servers[e.ServerURL]
		stale := false
		switch {
		case !configured:
			stale = true
		case reachableURLs[e.ServerURL]:
			if vmids := vmidsByURL[e.ServerURL]; vmids != nil && !vmids[e.VMID] {
				stale = true
			}
		}
		if !stale {
			continue
		}
		ee := e
		items = append(items, cleanupItem{
			Category: "vm-identity",
			Detail:   fmt.Sprintf("%s vmid %d → user %q", contextLabelFor(cfg, ee.ServerURL), ee.VMID, ee.Identity.User),
			apply:    func() error { return vmidentity.Delete(dir, ee.ServerURL, ee.VMID) },
		})
	}
	return items
}

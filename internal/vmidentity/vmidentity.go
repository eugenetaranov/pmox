// Package vmidentity persists, per VM, the SSH user and public-key line
// actually baked into its cloud-init at launch/clone time, so later
// commands that connect to a guest can use the identity it actually has
// instead of blindly trusting the current global config (which can drift
// after a VM is created — a config edit, a key rotation, or a machine
// switch never propagates to already-launched VMs). State is a single
// JSON file under the pmox state dir, keyed by server URL + VMID (VMIDs
// are stable across renames; entries can be pruned when a VM is
// deleted).
package vmidentity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/eugenetaranov/pmox/internal/atomicfile"
)

// Identity is the user + SSH public-key line recorded for a VM.
type Identity struct {
	User          string `json:"user"`
	SSHPubkeyLine string `json:"ssh_pubkey_line"`
}

// store is the on-disk shape: key -> identity.
type store map[string]Identity

func key(serverURL string, vmid int) string {
	return serverURL + "#" + strconv.Itoa(vmid)
}

// Get returns the recorded identity for (serverURL, vmid) and whether one
// was found. A missing state file yields (Identity{}, false, nil); a
// corrupt one is moved aside (see load) and also yields (Identity{},
// false, nil). Any other read failure is returned as an error.
func Get(stateDir, serverURL string, vmid int) (Identity, bool, error) {
	s, err := load(stateDir)
	if err != nil {
		return Identity{}, false, err
	}
	v, ok := s[key(serverURL, vmid)]
	return v, ok, nil
}

// Set records the identity for (serverURL, vmid), creating the state file
// (0600 in a 0700 dir) if needed.
func Set(stateDir, serverURL string, vmid int, identity Identity) error {
	s, err := load(stateDir)
	if err != nil {
		return err
	}
	s[key(serverURL, vmid)] = identity
	return save(stateDir, s)
}

// Entry is one recorded identity record.
type Entry struct {
	ServerURL string
	VMID      int
	Identity  Identity
}

// All returns every recorded identity entry.
func All(stateDir string) ([]Entry, error) {
	s, err := load(stateDir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(s))
	for k, identity := range s {
		i := strings.LastIndex(k, "#")
		if i < 0 {
			continue
		}
		vmid, err := strconv.Atoi(k[i+1:])
		if err != nil {
			continue
		}
		out = append(out, Entry{ServerURL: k[:i], VMID: vmid, Identity: identity})
	}
	return out, nil
}

// Delete removes the recorded identity for (serverURL, vmid), if present.
func Delete(stateDir, serverURL string, vmid int) error {
	s, err := load(stateDir)
	if err != nil {
		return err
	}
	if _, ok := s[key(serverURL, vmid)]; !ok {
		return nil
	}
	delete(s, key(serverURL, vmid))
	return save(stateDir, s)
}

func statePath(stateDir string) string {
	return filepath.Join(stateDir, "identities.json")
}

// corruptPath is where an unparseable state file is moved aside to.
func corruptPath(stateDir string) string {
	return statePath(stateDir) + ".corrupt"
}

func load(stateDir string) (store, error) {
	data, err := os.ReadFile(statePath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return store{}, nil
		}
		return nil, fmt.Errorf("read vm identity state: %w", err)
	}
	var s store
	if err := json.Unmarshal(data, &s); err != nil {
		// A corrupt state file is non-fatal for a convenience cache: start
		// fresh rather than blocking, but move the file aside first so
		// the next save doesn't silently destroy it. Best effort.
		_ = os.Rename(statePath(stateDir), corruptPath(stateDir))
		return store{}, nil
	}
	if s == nil {
		s = store{}
	}
	return s, nil
}

func save(stateDir string, s store) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(statePath(stateDir), data, 0o600); err != nil {
		return fmt.Errorf("write vm identity state: %w", err)
	}
	return nil
}
